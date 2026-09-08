package config

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestLoadConfig_LocalModeWithoutBasicAuthRejected(t *testing.T) {
	t.Setenv("DRS_AUTH_MODE", "local")
	t.Setenv("DRS_DB_SQLITE_FILE", ":memory:")
	t.Setenv("DRS_BASIC_AUTH_USER", "")
	t.Setenv("DRS_BASIC_AUTH_PASSWORD", "")
	t.Setenv("DRS_LOCAL_AUTHZ_CSV", "")
	t.Setenv("DRS_ALLOW_UNAUTHENTICATED_LOCAL", "")

	_, err := LoadConfig("")
	if err == nil {
		t.Fatal("expected local mode without auth to fail")
	}
	if !bytes.Contains([]byte(err.Error()), []byte("auth.basic.username/password")) {
		t.Fatalf("expected auth guidance in error, got: %v", err)
	}
}

func TestLoadConfig_LocalModeAllowsExplicitUnauthenticatedDevMode(t *testing.T) {
	t.Setenv("DRS_AUTH_MODE", "local")
	t.Setenv("DRS_DB_SQLITE_FILE", ":memory:")
	t.Setenv("DRS_ALLOW_UNAUTHENTICATED_LOCAL", "true")

	cfg, err := LoadConfig("")
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	if cfg.Auth.Mode != AuthModeLocal {
		t.Errorf("Auth.Mode = %q, want local", cfg.Auth.Mode)
	}
	if !cfg.Auth.AllowUnauthenticated {
		t.Fatal("expected explicit unauthenticated local opt-in")
	}
}

// Test HIGH-2 fix: Mock auth is supported in gen3 mode
func TestLoadConfig_MockAuthAllowsGen3Mode(t *testing.T) {
	t.Setenv("DRS_AUTH_MODE", "gen3")
	t.Setenv("DRS_AUTH_MOCK_ENABLED", "true")
	t.Setenv("DRS_DB_HOST", "localhost")
	t.Setenv("DRS_DB_DATABASE", "testdb")

	cfg, err := LoadConfig("")
	if err != nil {
		t.Fatalf("LoadConfig() unexpected error = %v", err)
	}
	if cfg.Auth.Mode != AuthModeGen3 {
		t.Errorf("Auth.Mode = %q, want gen3", cfg.Auth.Mode)
	}
}

// Test MED-2 fix: Postgres SSL mode defaults to "require"
func TestLoadConfig_PostgresSSLModeDefault(t *testing.T) {
	t.Setenv("DRS_AUTH_MODE", "local")
	t.Setenv("DRS_BASIC_AUTH_USER", "drs-user")
	t.Setenv("DRS_BASIC_AUTH_PASSWORD", "drs-pass")
	t.Setenv("DRS_DB_HOST", "localhost")
	t.Setenv("DRS_DB_DATABASE", "testdb")

	cfg, err := LoadConfig("")
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}

	if cfg.Database.Postgres == nil {
		t.Fatal("Database.Postgres is nil")
	}

	if cfg.Database.Postgres.SSLMode != "require" {
		t.Errorf("Postgres.SSLMode = %q, want require", cfg.Database.Postgres.SSLMode)
	}
}

// Test MED-1 fix: Secrets are redacted in JSON marshaling
func TestSecretRedaction_BasicAuthConfig(t *testing.T) {
	cfg := BasicAuthConfig{
		Username: "admin",
		Password: "super_secret_password",
	}

	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}

	// Check that password is not in the output
	output := string(data)
	if bytes.Contains([]byte(output), []byte("super_secret_password")) {
		t.Errorf("Password leaked in JSON output: %s", output)
	}
	if !bytes.Contains([]byte(output), []byte("***REDACTED***")) {
		t.Errorf("Password not redacted in JSON output: %s", output)
	}
}

func TestSecretRedaction_PostgresConfig(t *testing.T) {
	cfg := PostgresConfig{
		Host:     "db.example.com",
		Port:     5432,
		User:     "admin",
		Password: "db_secret_pass",
		Database: "mydb",
		SSLMode:  "require",
	}

	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}

	output := string(data)
	if bytes.Contains([]byte(output), []byte("db_secret_pass")) {
		t.Errorf("Password leaked in JSON output: %s", output)
	}
	if !bytes.Contains([]byte(output), []byte("***REDACTED***")) {
		t.Errorf("Password not redacted in JSON output: %s", output)
	}
}

func TestSecretRedaction_S3Config(t *testing.T) {
	cfg := BucketConfig{
		Bucket:    "my-bucket",
		Provider:  "s3",
		Region:    "us-east-1",
		AccessKey: "AKIAIOSFODNN7EXAMPLE",
		SecretKey: "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
		Endpoint:  "https://s3.amazonaws.com",
	}

	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}

	output := string(data)
	if bytes.Contains([]byte(output), []byte("wJalrXUtnFEMI")) {
		t.Errorf("SecretKey leaked in JSON output: %s", output)
	}
	if bytes.Contains([]byte(output), []byte("AKIAIOSFODNN7EXAMPLE")) {
		t.Errorf("AccessKey leaked in JSON output: %s", output)
	}

	// Redaction marker should appear twice (for both AccessKey and SecretKey)
	redactCount := bytes.Count([]byte(output), []byte("***REDACTED***"))
	if redactCount < 2 {
		t.Errorf("Expected at least 2 REDACTED markers, got %d: %s", redactCount, output)
	}
}
