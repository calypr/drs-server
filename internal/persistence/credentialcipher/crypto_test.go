package credentialcipher

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/calypr/syfon/internal/buckets"
)

func TestEncryptDecryptField_RoundTrip(t *testing.T) {
	t.Setenv(CredentialMasterKeyEnv, "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY=") // 32 bytes base64
	cipher := newTestCipher(t)

	ciphertext, err := cipher.EncryptField(context.Background(), "super-secret")
	if err != nil {
		t.Fatalf("EncryptField returned error: %v", err)
	}
	if ciphertext == "super-secret" {
		t.Fatal("expected encrypted ciphertext, got plaintext")
	}
	if !strings.HasPrefix(ciphertext, "enc:v2:") {
		t.Fatalf("expected encrypted prefix, got %q", ciphertext)
	}

	plaintext, err := cipher.DecryptField(context.Background(), ciphertext)
	if err != nil {
		t.Fatalf("DecryptField returned error: %v", err)
	}
	if plaintext != "super-secret" {
		t.Fatalf("expected decrypted plaintext to match, got %q", plaintext)
	}
}

func TestDecryptField_LegacyPlaintext(t *testing.T) {
	t.Setenv(CredentialMasterKeyEnv, "")
	cipher := newTestCipher(t)
	plaintext, err := cipher.DecryptField(context.Background(), "legacy-plaintext")
	if err != nil {
		t.Fatalf("expected legacy plaintext support, got error: %v", err)
	}
	if plaintext != "legacy-plaintext" {
		t.Fatalf("unexpected plaintext parse result: %q", plaintext)
	}
}

func TestDecryptField_MissingKeyForEncryptedData(t *testing.T) {
	t.Setenv(CredentialMasterKeyEnv, "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY=")
	cipher := newTestCipher(t)
	encrypted, err := cipher.EncryptField(context.Background(), "abc")
	if err != nil {
		t.Fatalf("encrypt setup failed: %v", err)
	}
	t.Setenv(CredentialMasterKeyEnv, "")

	_, err = cipher.DecryptField(context.Background(), encrypted)
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

func TestEnabledValidatesSelectedKeyManager(t *testing.T) {
	t.Setenv(CredentialMasterKeyEnv, "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY=")
	t.Setenv(CredentialKeyManagerEnv, "not-registered")
	t.Setenv(CredentialKMSKeyIDEnv, "")

	cipher := newTestCipher(t)
	if enabled, err := cipher.Enabled(); err == nil || enabled || !strings.Contains(err.Error(), `credential key manager "not-registered" is not registered`) {
		t.Fatalf("Enabled() = (%v, %v), want selected-manager error", enabled, err)
	}
}

func TestEnabledRejectsAWSKMSWithoutKeyID(t *testing.T) {
	t.Setenv(CredentialMasterKeyEnv, "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY=")
	t.Setenv(CredentialKeyManagerEnv, awsKMSKeyManagerName)
	t.Setenv(CredentialKMSKeyIDEnv, "")
	t.Setenv("AWS_EC2_METADATA_DISABLED", "true")

	cipher := newTestCipher(t)
	if enabled, err := cipher.Enabled(); err == nil || enabled || !strings.Contains(err.Error(), CredentialKMSKeyIDEnv) {
		t.Fatalf("Enabled() = (%v, %v), want missing KMS key ID error", enabled, err)
	}
}

func TestDecryptField_LegacyV1Ciphertext(t *testing.T) {
	t.Setenv(CredentialMasterKeyEnv, "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY=")
	codec := newTestCipher(t)

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

	plaintext, err := codec.DecryptField(context.Background(), legacy)
	if err != nil {
		t.Fatalf("DecryptField returned error: %v", err)
	}
	if plaintext != "legacy-secret" {
		t.Fatalf("expected legacy plaintext, got %q", plaintext)
	}
}

func TestPrepareAndParseCredential(t *testing.T) {
	t.Setenv(CredentialMasterKeyEnv, "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY=")
	cipher := newTestCipher(t)

	cred := &buckets.Credential{
		Bucket:    "b",
		Provider:  "s3",
		Region:    "us-east-1",
		AccessKey: "ak",
		SecretKey: "sk",
		Endpoint:  "https://s3.example",
	}
	stored, err := cipher.Prepare(context.Background(), cred)
	if err != nil {
		t.Fatalf("Prepare returned error: %v", err)
	}
	if stored.AccessKey == "ak" || stored.SecretKey == "sk" {
		t.Fatalf("expected encrypted values, got %+v", stored)
	}

	parsed, err := cipher.Parse(context.Background(), stored)
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
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

func TestEncryptField_EnvelopeV2ContainsWrappedDEKAndMetadata(t *testing.T) {
	t.Setenv(CredentialMasterKeyEnv, "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY=")
	t.Setenv(CredentialKeyManagerEnv, "")
	t.Setenv(CredentialKMSKeyIDEnv, "")
	cipher := newTestCipher(t)

	ciphertext, err := cipher.EncryptField(context.Background(), "metadata-check")
	if err != nil {
		t.Fatalf("EncryptField returned error: %v", err)
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

func TestEncryptField_UsesRandomDEKPerRecord(t *testing.T) {
	t.Setenv(CredentialMasterKeyEnv, "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY=")
	t.Setenv(CredentialKeyManagerEnv, "")
	t.Setenv(CredentialKMSKeyIDEnv, "")
	cipher := newTestCipher(t)

	c1, err := cipher.EncryptField(context.Background(), "same-plaintext")
	if err != nil {
		t.Fatalf("EncryptField(1) error: %v", err)
	}
	c2, err := cipher.EncryptField(context.Background(), "same-plaintext")
	if err != nil {
		t.Fatalf("EncryptField(2) error: %v", err)
	}
	if c1 == c2 {
		t.Fatal("expected different ciphertexts for same plaintext due random DEK/nonce")
	}
	p1, err := cipher.DecryptField(context.Background(), c1)
	if err != nil {
		t.Fatalf("DecryptField(1) error: %v", err)
	}
	p2, err := cipher.DecryptField(context.Background(), c2)
	if err != nil {
		t.Fatalf("DecryptField(2) error: %v", err)
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

func TestEncryptFieldPropagatesContextToKeyManager(t *testing.T) {
	manager := &contextRecordingKeyManager{}
	registerTestCredentialKeyManager(t, manager)
	cipher := newTestCipher(t)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := cipher.EncryptField(ctx, "secret")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected canceled context error, got %v", err)
	}
	if manager.wrapContext != ctx {
		t.Fatal("expected canceled context to reach WrapDataKey")
	}
}

func TestDecryptFieldPropagatesContextToKeyManager(t *testing.T) {
	manager := &contextRecordingKeyManager{}
	registerTestCredentialKeyManager(t, manager)
	cipher := newTestCipher(t)

	payload, err := json.Marshal(credentialEnvelopeV2{Manager: "test"})
	if err != nil {
		t.Fatalf("marshal credential envelope: %v", err)
	}
	value := credentialCipherPrefixV2 + base64.RawStdEncoding.EncodeToString(payload)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = cipher.DecryptField(ctx, value)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected canceled context error, got %v", err)
	}
	if manager.unwrapContext != ctx {
		t.Fatal("expected canceled context to reach UnwrapDataKey")
	}
}

func newTestCipher(t *testing.T) *Cipher {
	t.Helper()
	cipher, err := NewFromEnv()
	if err != nil {
		t.Fatalf("NewFromEnv: %v", err)
	}
	return cipher
}

func registerTestCredentialKeyManager(t *testing.T, manager CredentialKeyManager) {
	t.Helper()
	credentialKeyManagerRegistryMu.Lock()
	original, exists := credentialKeyManagerRegistry["test"]
	credentialKeyManagerRegistry["test"] = func() (CredentialKeyManager, error) { return manager, nil }
	credentialKeyManagerRegistryMu.Unlock()
	t.Cleanup(func() {
		credentialKeyManagerRegistryMu.Lock()
		if exists {
			credentialKeyManagerRegistry["test"] = original
		} else {
			delete(credentialKeyManagerRegistry, "test")
		}
		credentialKeyManagerRegistryMu.Unlock()
	})
	t.Setenv(CredentialKeyManagerEnv, "test")
}

type contextRecordingKeyManager struct {
	wrapContext   context.Context
	unwrapContext context.Context
}

func (m *contextRecordingKeyManager) Name() string { return "test" }

func (m *contextRecordingKeyManager) WrapDataKey(ctx context.Context, _ []byte) (*WrappedDataKey, error) {
	m.wrapContext = ctx
	return nil, ctx.Err()
}

func (m *contextRecordingKeyManager) UnwrapDataKey(ctx context.Context, _ *WrappedDataKey) ([]byte, error) {
	m.unwrapContext = ctx
	return nil, ctx.Err()
}
