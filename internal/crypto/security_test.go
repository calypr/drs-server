package crypto

import (
	"encoding/hex"
	"encoding/base64"
	"os"
	"strings"
	"testing"
)

// Test HIGH-3 fix: KEK default path is /app instead of /tmp
func TestLocalCredentialKeyPath_DefaultPath(t *testing.T) {
	defer func() {
		os.Unsetenv("DRS_CREDENTIAL_LOCAL_KEY_FILE")
		os.Unsetenv("DRS_DB_SQLITE_FILE")
	}()

	// Unset all env vars to get default
	os.Unsetenv("DRS_CREDENTIAL_LOCAL_KEY_FILE")
	os.Unsetenv("DRS_DB_SQLITE_FILE")

	path := localCredentialKeyPath()

	if !strings.HasPrefix(path, "/app") {
		t.Errorf("localCredentialKeyPath() = %q, want to start with /app", path)
	}

	if strings.Contains(path, "/tmp") {
		t.Errorf("localCredentialKeyPath() = %q, should not contain /tmp", path)
	}

	if !strings.HasSuffix(path, ".syfon-credential-kek") {
		t.Errorf("localCredentialKeyPath() = %q, want to end with .syfon-credential-kek", path)
	}
}

// Test HIGH-3 fix: Explicit env var takes precedence
func TestLocalCredentialKeyPath_ExplicitEnvVar(t *testing.T) {
	defer func() {
		os.Unsetenv("DRS_CREDENTIAL_LOCAL_KEY_FILE")
	}()

	os.Setenv("DRS_CREDENTIAL_LOCAL_KEY_FILE", "/etc/syfon/kek")
	path := localCredentialKeyPath()

	if path != "/etc/syfon/kek" {
		t.Errorf("localCredentialKeyPath() = %q, want /etc/syfon/kek", path)
	}
}

// Test HIGH-3 fix: SQLite path env var takes precedence over default
func TestLocalCredentialKeyPath_SQLiteDir(t *testing.T) {
	defer func() {
		os.Unsetenv("DRS_DB_SQLITE_FILE")
		os.Unsetenv("DRS_CREDENTIAL_LOCAL_KEY_FILE")
	}()

	os.Unsetenv("DRS_CREDENTIAL_LOCAL_KEY_FILE")
	os.Setenv("DRS_DB_SQLITE_FILE", "/data/drs.db")

	path := localCredentialKeyPath()

	if !strings.HasPrefix(path, "/data/") {
		t.Errorf("localCredentialKeyPath() = %q, want to start with /data/", path)
	}

	if !strings.HasSuffix(path, ".syfon-credential-kek") {
		t.Errorf("localCredentialKeyPath() = %q, want to end with .syfon-credential-kek", path)
	}

	if strings.Contains(path, "/tmp") {
		t.Errorf("localCredentialKeyPath() = %q, should not contain /tmp", path)
	}
}

// Test LOW-2 fix: KEK fingerprint uses 16 bytes instead of 8
func TestLocalKeyManager_KeyIDLength(t *testing.T) {
	defer func() {
		os.Unsetenv("DRS_CREDENTIAL_MASTER_KEY")
	}()

	// Create a test key (32 bytes for AES-256) as base64-encoded
	testKeyBytes := make([]byte, 32)
	for i := 0; i < 32; i++ {
		testKeyBytes[i] = byte(i)
	}
	testKeyB64 := base64.StdEncoding.EncodeToString(testKeyBytes)
	os.Setenv("DRS_CREDENTIAL_MASTER_KEY", testKeyB64)
	manager := &localKeyManager{}

	// Wrap a data key
	dataKey := []byte("0123456789abcdef0123456789abcdef")
	wrapped, err := manager.WrapDataKey(nil, dataKey)
	if err != nil {
		t.Fatalf("WrapDataKey() error = %v", err)
	}

	// Extract the fingerprint from KeyID
	keyIDParts := strings.Split(wrapped.KeyID, ":")
	if len(keyIDParts) != 2 {
		t.Fatalf("KeyID format invalid: %q", wrapped.KeyID)
	}

	fingerprint := keyIDParts[1]

	// Hex string of 16 bytes (32 hex chars)
	expectedLength := 32 // 16 bytes = 32 hex characters
	if len(fingerprint) != expectedLength {
		t.Errorf("Fingerprint length = %d, want %d (hex string of 16 bytes). KeyID = %s", len(fingerprint), expectedLength, wrapped.KeyID)
	}

	// Verify it's valid hex
	if _, err := hex.DecodeString(fingerprint); err != nil {
		t.Errorf("Fingerprint is not valid hex: %v", err)
	}
}
