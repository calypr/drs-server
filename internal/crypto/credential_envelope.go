package crypto

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

const (
	credentialCipherPrefixV1 = "enc:v1:"
	credentialCipherPrefixV2 = "enc:v2:"
)

type credentialEnvelopeV2 struct {
	Manager    string `json:"m"`
	KeyID      string `json:"k,omitempty"`
	WrappedDEK string `json:"w"`
	Nonce      string `json:"n"`
	Ciphertext string `json:"c"`
}

func EncryptCredentialField(plaintext string) (string, error) {
	if plaintext == "" {
		return "", nil
	}
	if strings.HasPrefix(plaintext, credentialCipherPrefixV1) || strings.HasPrefix(plaintext, credentialCipherPrefixV2) {
		return plaintext, nil
	}

	manager, err := resolveCredentialKeyManager(configuredCredentialKeyManagerName())
	if err != nil {
		return "", err
	}

	dek := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, dek); err != nil {
		return "", fmt.Errorf("data key generation failed: %w", err)
	}

	nonce, ciphertext, err := encryptAESGCM(dek, []byte(plaintext))
	if err != nil {
		return "", err
	}

	wrapped, err := manager.WrapDataKey(context.Background(), dek)
	if err != nil {
		return "", err
	}

	envelope := credentialEnvelopeV2{
		Manager:    wrapped.Manager,
		KeyID:      wrapped.KeyID,
		WrappedDEK: wrapped.Ciphertext,
		Nonce:      base64.RawStdEncoding.EncodeToString(nonce),
		Ciphertext: base64.RawStdEncoding.EncodeToString(ciphertext),
	}
	b, err := json.Marshal(envelope)
	if err != nil {
		return "", fmt.Errorf("encode credential envelope: %w", err)
	}
	return credentialCipherPrefixV2 + base64.RawStdEncoding.EncodeToString(b), nil
}

func DecryptCredentialField(value string) (string, error) {
	if value == "" {
		return "", nil
	}
	if !strings.HasPrefix(value, credentialCipherPrefixV1) && !strings.HasPrefix(value, credentialCipherPrefixV2) {
		return value, nil
	}

	if strings.HasPrefix(value, credentialCipherPrefixV2) {
		return decryptCredentialFieldV2(value)
	}

	// Backward compatibility for legacy v1 ciphertexts.
	return decryptCredentialFieldV1(value)
}

func decryptCredentialFieldV2(value string) (string, error) {
	payloadB64 := strings.TrimPrefix(value, credentialCipherPrefixV2)
	payload, err := base64.RawStdEncoding.DecodeString(payloadB64)
	if err != nil {
		return "", fmt.Errorf("envelope decode failed: %w", err)
	}

	var envelope credentialEnvelopeV2
	if err := json.Unmarshal(payload, &envelope); err != nil {
		return "", fmt.Errorf("envelope parse failed: %w", err)
	}
	if strings.TrimSpace(envelope.Manager) == "" {
		return "", errors.New("envelope manager is required")
	}

	manager, err := resolveCredentialKeyManager(envelope.Manager)
	if err != nil {
		return "", err
	}

	dek, err := manager.UnwrapDataKey(context.Background(), &WrappedDataKey{
		Manager:    envelope.Manager,
		KeyID:      envelope.KeyID,
		Ciphertext: envelope.WrappedDEK,
	})
	if err != nil {
		return "", err
	}

	nonce, err := base64.RawStdEncoding.DecodeString(envelope.Nonce)
	if err != nil {
		return "", fmt.Errorf("nonce decode failed: %w", err)
	}
	ciphertext, err := base64.RawStdEncoding.DecodeString(envelope.Ciphertext)
	if err != nil {
		return "", fmt.Errorf("ciphertext decode failed: %w", err)
	}
	plaintext, err := decryptAESGCM(dek, nonce, ciphertext)
	if err != nil {
		return "", err
	}
	return string(plaintext), nil
}

func decryptCredentialFieldV1(value string) (string, error) {
	key, err := credentialMasterKey()
	if err != nil {
		return "", err
	}
	if len(key) == 0 {
		return "", errors.New("encrypted credential found but master key is not configured")
	}

	payloadB64 := strings.TrimPrefix(value, credentialCipherPrefixV1)
	payload, err := base64.RawStdEncoding.DecodeString(payloadB64)
	if err != nil {
		return "", fmt.Errorf("ciphertext decode failed: %w", err)
	}
	plaintext, err := decryptPackedAESGCM(key, payload)
	if err != nil {
		return "", err
	}
	return string(plaintext), nil
}

func encryptAESGCM(key, plaintext []byte) ([]byte, []byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, nil, fmt.Errorf("cipher init failed: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, nil, fmt.Errorf("gcm init failed: %w", err)
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, nil, fmt.Errorf("nonce generation failed: %w", err)
	}
	ciphertext := gcm.Seal(nil, nonce, plaintext, nil)
	return nonce, ciphertext, nil
}

func decryptAESGCM(key, nonce, ciphertext []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("cipher init failed: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("gcm init failed: %w", err)
	}
	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return nil, fmt.Errorf("decrypt failed: %w", err)
	}
	return plaintext, nil
}

func decryptPackedAESGCM(key, payload []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("cipher init failed: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("gcm init failed: %w", err)
	}
	nonceSize := gcm.NonceSize()
	if len(payload) < nonceSize {
		return nil, errors.New("ciphertext payload too short")
	}
	nonce := payload[:nonceSize]
	ciphertext := payload[nonceSize:]
	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return nil, fmt.Errorf("decrypt failed: %w", err)
	}
	return plaintext, nil
}
