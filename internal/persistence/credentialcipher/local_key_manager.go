package credentialcipher

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

type localKeyManager struct{}

func credentialMasterKey() ([]byte, error) {
	raw := strings.TrimSpace(os.Getenv(CredentialMasterKeyEnv))
	if raw != "" {
		return parseUserProvidedKey(raw, CredentialMasterKeyEnv)
	}
	// Default behavior: managed local KEK persisted on the server.
	return loadOrCreateLocalCredentialKey()
}

func parseUserProvidedKey(raw string, envName string) ([]byte, error) {
	if len(raw) == 64 {
		hexDecoded, hexErr := hex.DecodeString(raw)
		if hexErr == nil && len(hexDecoded) == 32 {
			return hexDecoded, nil
		}
	}

	decoded, err := base64.StdEncoding.DecodeString(raw)
	if err == nil {
		if len(decoded) != 32 {
			return nil, fmt.Errorf("%s must decode to 32 bytes for AES-256", envName)
		}
		return decoded, nil
	}

	if len(raw) == 32 {
		return []byte(raw), nil
	}
	return nil, fmt.Errorf("%s must be a 32-byte raw key, 64-char hex key, or base64-encoded 32-byte key", envName)
}

func localCredentialKeyPath() string {
	if p := strings.TrimSpace(os.Getenv(CredentialLocalKeyFileEnv)); p != "" {
		return p
	}
	if sqlitePath := strings.TrimSpace(os.Getenv(DatabaseSQLiteFileEnv)); sqlitePath != "" {
		return filepath.Join(filepath.Dir(sqlitePath), ".syfon-credential-kek")
	}
	// SECURITY FIX HIGH-3: Default to /app instead of /tmp or user home directories
	return "/app/.syfon-credential-kek"
}

func loadOrCreateLocalCredentialKey() ([]byte, error) {
	keyPath := localCredentialKeyPath()
	if b, err := os.ReadFile(keyPath); err == nil {
		return parseUserProvidedKey(strings.TrimSpace(string(b)), CredentialLocalKeyFileEnv)
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("read local credential key file %s: %w", keyPath, err)
	}

	if err := os.MkdirAll(filepath.Dir(keyPath), 0o700); err != nil {
		return nil, fmt.Errorf("create local credential key directory for %s: %w", keyPath, err)
	}

	key := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, key); err != nil {
		return nil, fmt.Errorf("generate local credential key: %w", err)
	}
	encoded := base64.StdEncoding.EncodeToString(key) + "\n"

	f, err := os.CreateTemp(filepath.Dir(keyPath), ".syfon-credential-kek-*")
	if err != nil {
		return nil, fmt.Errorf("create temporary local credential key file for %s: %w", keyPath, err)
	}
	tempPath := f.Name()
	cleanupTemp := func(cause error) error {
		closeErr := f.Close()
		removeErr := os.Remove(tempPath)
		cleanupErrs := make([]error, 0, 2)
		if closeErr != nil {
			cleanupErrs = append(cleanupErrs, fmt.Errorf("close temporary local credential key file %s: %w", tempPath, closeErr))
		}
		if removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
			cleanupErrs = append(cleanupErrs, fmt.Errorf("remove temporary local credential key file %s: %w", tempPath, removeErr))
		}
		if len(cleanupErrs) == 0 {
			return cause
		}
		return errors.Join(append([]error{cause}, cleanupErrs...)...)
	}

	if err := f.Chmod(0o600); err != nil {
		return nil, cleanupTemp(fmt.Errorf("set local credential key file permissions %s: %w", tempPath, err))
	}

	if _, err := f.WriteString(encoded); err != nil {
		return nil, cleanupTemp(fmt.Errorf("write temporary local credential key file %s: %w", tempPath, err))
	}
	if err := f.Sync(); err != nil {
		return nil, cleanupTemp(fmt.Errorf("sync temporary local credential key file %s: %w", tempPath, err))
	}
	if err := f.Close(); err != nil {
		removeErr := os.Remove(tempPath)
		if removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
			return nil, errors.Join(
				fmt.Errorf("close temporary local credential key file %s: %w", tempPath, err),
				fmt.Errorf("remove temporary local credential key file %s: %w", tempPath, removeErr),
			)
		}
		return nil, fmt.Errorf("close temporary local credential key file %s: %w", tempPath, err)
	}

	if err := os.Link(tempPath, keyPath); err != nil {
		removeErr := os.Remove(tempPath)
		if errors.Is(err, os.ErrExist) {
			if removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
				return nil, fmt.Errorf("remove losing local credential key file %s: %w", tempPath, removeErr)
			}
			b, readErr := os.ReadFile(keyPath)
			if readErr != nil {
				return nil, fmt.Errorf("read concurrent local credential key file %s: %w", keyPath, readErr)
			}
			return parseUserProvidedKey(strings.TrimSpace(string(b)), CredentialLocalKeyFileEnv)
		}
		if removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
			return nil, errors.Join(
				fmt.Errorf("publish local credential key file %s: %w", keyPath, err),
				fmt.Errorf("remove temporary local credential key file %s: %w", tempPath, removeErr),
			)
		}
		return nil, fmt.Errorf("publish local credential key file %s: %w", keyPath, err)
	}
	if err := os.Remove(tempPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("remove published local credential key temporary file %s: %w", tempPath, err)
	}
	return key, nil
}

func (m *localKeyManager) Name() string { return defaultCredentialKeyManager }

func (m *localKeyManager) WrapDataKey(_ context.Context, dataKey []byte) (*WrappedDataKey, error) {
	kek, err := credentialMasterKey()
	if err != nil {
		return nil, err
	}
	if len(kek) == 0 {
		return nil, fmt.Errorf("%s is required to store non-empty credentials securely", CredentialMasterKeyEnv)
	}

	nonce, ciphertext, err := encryptAESGCM(kek, dataKey)
	if err != nil {
		return nil, fmt.Errorf("wrap data key: %w", err)
	}
	payload := append(nonce, ciphertext...)
	fingerprint := sha256.Sum256(kek)
	// SECURITY FIX LOW-2: Use 16 bytes (128-bit) instead of 8 bytes for fingerprint
	return &WrappedDataKey{
		Manager:    m.Name(),
		KeyID:      "local:" + hex.EncodeToString(fingerprint[:16]),
		Ciphertext: base64.RawStdEncoding.EncodeToString(payload),
	}, nil
}

func (m *localKeyManager) UnwrapDataKey(_ context.Context, wrapped *WrappedDataKey) ([]byte, error) {
	if wrapped == nil {
		return nil, fmt.Errorf("wrapped data key is required")
	}
	kek, err := credentialMasterKey()
	if err != nil {
		return nil, err
	}
	if len(kek) == 0 {
		return nil, fmt.Errorf("encrypted credential found but master key is not configured")
	}
	payload, err := base64.RawStdEncoding.DecodeString(wrapped.Ciphertext)
	if err != nil {
		return nil, fmt.Errorf("decode wrapped data key: %w", err)
	}
	return decryptPackedAESGCM(kek, payload)
}
