package crypto

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/calypr/syfon/internal/models"
)

func TestEncryptDecryptCredentialField_RoundTrip(t *testing.T) {
	t.Setenv(CredentialMasterKeyEnv, "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY=") // 32 bytes base64

	ciphertext, err := EncryptCredentialField("super-secret")
	if err != nil {
		t.Fatalf("EncryptCredentialField returned error: %v", err)
	}
	if ciphertext == "super-secret" {
		t.Fatal("expected encrypted ciphertext, got plaintext")
	}
	if !strings.HasPrefix(ciphertext, "enc:v2:") {
		t.Fatalf("expected encrypted prefix, got %q", ciphertext)
	}

	plaintext, err := DecryptCredentialField(ciphertext)
	if err != nil {
		t.Fatalf("DecryptCredentialField returned error: %v", err)
	}
	if plaintext != "super-secret" {
		t.Fatalf("expected decrypted plaintext to match, got %q", plaintext)
	}
}

func TestDecryptCredentialField_LegacyPlaintext(t *testing.T) {
	t.Setenv(CredentialMasterKeyEnv, "")
	plaintext, err := DecryptCredentialField("legacy-plaintext")
	if err != nil {
		t.Fatalf("expected legacy plaintext support, got error: %v", err)
	}
	if plaintext != "legacy-plaintext" {
		t.Fatalf("unexpected plaintext parse result: %q", plaintext)
	}
}

func TestDecryptCredentialField_MissingKeyForEncryptedData(t *testing.T) {
	t.Setenv(CredentialMasterKeyEnv, "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY=")
	encrypted, err := EncryptCredentialField("abc")
	if err != nil {
		t.Fatalf("encrypt setup failed: %v", err)
	}
	t.Setenv(CredentialMasterKeyEnv, "")

	_, err = DecryptCredentialField(encrypted)
	if err == nil {
		t.Fatal("expected error when decrypting encrypted data without key")
	}
}

func TestCredentialMasterKeyAcceptsHexBeforeBase64(t *testing.T) {
	t.Setenv(CredentialMasterKeyEnv, "13a53e671b318db3d417f602baa90fac3cd59f96c4cdb5e31ad8bc5c857f3a96")

	key, err := credentialMasterKey()
	if err != nil {
		t.Fatalf("credentialMasterKey returned error: %v", err)
	}
	if len(key) != 32 {
		t.Fatalf("expected 32-byte key, got %d bytes", len(key))
	}
}

func TestDecryptCredentialField_LegacyV1Ciphertext(t *testing.T) {
	t.Setenv(CredentialMasterKeyEnv, "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY=")

	key, err := credentialMasterKey()
	if err != nil {
		t.Fatalf("credentialMasterKey setup failed: %v", err)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		t.Fatalf("cipher init failed: %v", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		t.Fatalf("gcm init failed: %v", err)
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		t.Fatalf("nonce generation failed: %v", err)
	}
	ciphertext := gcm.Seal(nil, nonce, []byte("legacy-secret"), nil)
	payload := append(nonce, ciphertext...)
	legacy := "enc:v1:" + base64.RawStdEncoding.EncodeToString(payload)

	plaintext, err := DecryptCredentialField(legacy)
	if err != nil {
		t.Fatalf("DecryptCredentialField returned error: %v", err)
	}
	if plaintext != "legacy-secret" {
		t.Fatalf("expected legacy plaintext, got %q", plaintext)
	}
}

func TestPrepareAndParseS3CredentialForStorage(t *testing.T) {
	t.Setenv(CredentialMasterKeyEnv, "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY=")

	cred := &models.S3Credential{
		Bucket:    "b",
		Provider:  "s3",
		Region:    "us-east-1",
		AccessKey: "ak",
		SecretKey: "sk",
		Endpoint:  "https://s3.example",
	}
	stored, err := PrepareS3CredentialForStorage(cred)
	if err != nil {
		t.Fatalf("PrepareS3CredentialForStorage returned error: %v", err)
	}
	if stored.AccessKey == "ak" || stored.SecretKey == "sk" {
		t.Fatalf("expected encrypted values, got %+v", stored)
	}

	parsed, err := ParseS3CredentialFromStorage(stored)
	if err != nil {
		t.Fatalf("ParseS3CredentialFromStorage returned error: %v", err)
	}
	if parsed.AccessKey != "ak" || parsed.SecretKey != "sk" {
		t.Fatalf("expected decrypted values, got %+v", parsed)
	}
}

func TestCredentialMasterKey_LocalKeyFile_IsDeterministic(t *testing.T) {
	t.Setenv(CredentialMasterKeyEnv, "")
	t.Setenv(CredentialLocalKeyFileEnv, filepath.Join(t.TempDir(), "local-kek"))

	key1, err := credentialMasterKey()
	if err != nil {
		t.Fatalf("credentialMasterKey returned error: %v", err)
	}
	key2, err := credentialMasterKey()
	if err != nil {
		t.Fatalf("credentialMasterKey returned error on second read: %v", err)
	}
	if len(key1) != 32 {
		t.Fatalf("expected 32-byte key, got %d", len(key1))
	}
	if !bytes.Equal(key1, key2) {
		t.Fatalf("expected deterministic key loading")
	}
}

func TestCredentialMasterKey_LocalKeyFile_DefaultPathFromSqlite(t *testing.T) {
	t.Setenv(CredentialMasterKeyEnv, "")
	sqliteDir := t.TempDir()
	t.Setenv(DatabaseSQLiteFileEnv, filepath.Join(sqliteDir, "drs.db"))
	t.Setenv(CredentialLocalKeyFileEnv, "")

	path := localCredentialKeyPath()
	if !strings.HasPrefix(path, sqliteDir) {
		t.Fatalf("expected local key path under sqlite dir, got %q", path)
	}
}

func TestEncryptCredentialField_EnvelopeV2ContainsWrappedDEKAndMetadata(t *testing.T) {
	t.Setenv(CredentialMasterKeyEnv, "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY=")
	t.Setenv(CredentialKeyManagerEnv, "")
	t.Setenv(CredentialKMSKeyIDEnv, "")

	ciphertext, err := EncryptCredentialField("metadata-check")
	if err != nil {
		t.Fatalf("EncryptCredentialField returned error: %v", err)
	}
	if !strings.HasPrefix(ciphertext, "enc:v2:") {
		t.Fatalf("expected enc:v2 payload, got %q", ciphertext)
	}

	payloadB64 := strings.TrimPrefix(ciphertext, "enc:v2:")
	payload, err := base64.RawStdEncoding.DecodeString(payloadB64)
	if err != nil {
		t.Fatalf("envelope decode failed: %v", err)
	}
	var env credentialEnvelopeV2
	if err := json.Unmarshal(payload, &env); err != nil {
		t.Fatalf("envelope parse failed: %v", err)
	}
	if strings.TrimSpace(env.Manager) == "" {
		t.Fatal("expected envelope manager metadata")
	}
	if strings.TrimSpace(env.WrappedDEK) == "" {
		t.Fatal("expected wrapped DEK metadata")
	}
	if strings.TrimSpace(env.Nonce) == "" || strings.TrimSpace(env.Ciphertext) == "" {
		t.Fatal("expected nonce and ciphertext metadata")
	}
}

func TestEncryptCredentialField_UsesRandomDEKPerRecord(t *testing.T) {
	t.Setenv(CredentialMasterKeyEnv, "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY=")
	t.Setenv(CredentialKeyManagerEnv, "")
	t.Setenv(CredentialKMSKeyIDEnv, "")

	c1, err := EncryptCredentialField("same-plaintext")
	if err != nil {
		t.Fatalf("EncryptCredentialField(1) error: %v", err)
	}
	c2, err := EncryptCredentialField("same-plaintext")
	if err != nil {
		t.Fatalf("EncryptCredentialField(2) error: %v", err)
	}
	if c1 == c2 {
		t.Fatal("expected different ciphertexts for same plaintext due random DEK/nonce")
	}
	p1, err := DecryptCredentialField(c1)
	if err != nil {
		t.Fatalf("DecryptCredentialField(1) error: %v", err)
	}
	p2, err := DecryptCredentialField(c2)
	if err != nil {
		t.Fatalf("DecryptCredentialField(2) error: %v", err)
	}
	if p1 != "same-plaintext" || p2 != "same-plaintext" {
		t.Fatalf("unexpected decrypted plaintexts: %q %q", p1, p2)
	}
}

func TestConfiguredCredentialKeyManagerName_AutoSelectsAWSWhenKMSKeySet(t *testing.T) {
	t.Setenv(CredentialKeyManagerEnv, "")
	t.Setenv(CredentialKMSKeyIDEnv, "arn:aws:kms:us-east-1:123456789012:key/test")
	if got := configuredCredentialKeyManagerName(); got != awsKMSKeyManagerName {
		t.Fatalf("expected %q, got %q", awsKMSKeyManagerName, got)
	}
}
