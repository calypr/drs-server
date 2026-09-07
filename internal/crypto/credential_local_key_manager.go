package crypto

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

	f, err := os.OpenFile(keyPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			b, readErr := os.ReadFile(keyPath)
			if readErr != nil {
				return nil, fmt.Errorf("read concurrent local credential key file %s: %w", keyPath, readErr)
			}
			return parseUserProvidedKey(strings.TrimSpace(string(b)), CredentialLocalKeyFileEnv)
		}
		return nil, fmt.Errorf("create local credential key file %s: %w", keyPath, err)
	}
	defer f.Close()

	if _, err := f.WriteString(encoded); err != nil {
		return nil, fmt.Errorf("write local credential key file %s: %w", keyPath, err)
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
		return nil, errors.New("wrapped data key is required")
	}
	kek, err := credentialMasterKey()
	if err != nil {
		return nil, err
	}
	if len(kek) == 0 {
		return nil, errors.New("encrypted credential found but master key is not configured")
	}
	payload, err := base64.RawStdEncoding.DecodeString(wrapped.Ciphertext)
	if err != nil {
		return nil, fmt.Errorf("decode wrapped data key: %w", err)
	}
	return decryptPackedAESGCM(kek, payload)
}
