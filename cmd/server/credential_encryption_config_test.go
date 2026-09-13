package server

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/calypr/syfon/internal/buckets"
	"github.com/calypr/syfon/internal/config"
	"github.com/calypr/syfon/internal/persistence/credentialcipher"
)

func TestApplyCredentialEncryptionConfig(t *testing.T) {
	t.Setenv(credentialcipher.CredentialMasterKeyEnv, "")
	t.Setenv(credentialcipher.CredentialLocalKeyFileEnv, "")
	t.Setenv(credentialcipher.DatabaseSQLiteFileEnv, "")

	cfg := &config.Config{
		Database: config.DatabaseConfig{
			Sqlite: &config.SqliteConfig{File: "drs.db"},
		},
		CredentialEncryption: config.CredentialEncryptionConfig{
			LocalKeyFile: ".syfon-credential-kek",
		},
	}

	applyCredentialEncryptionConfig(cfg)

	if got := os.Getenv(credentialcipher.CredentialLocalKeyFileEnv); got != ".syfon-credential-kek" {
		t.Fatalf("expected local key file env to be set from config, got %q", got)
	}
	if got := os.Getenv(credentialcipher.DatabaseSQLiteFileEnv); got != "drs.db" {
		t.Fatalf("expected sqlite file env to be set from config, got %q", got)
	}
}

func TestNormalizedCredentialConfigurationReachesPersistence(t *testing.T) {
	t.Setenv(credentialcipher.CredentialMasterKeyEnv, "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY=")
	t.Setenv(credentialcipher.CredentialLocalKeyFileEnv, "")
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	content := `
auth:
  mode: local
  allow_unauthenticated: true
database:
  sqlite:
    file: ":memory:"
routes:
  docs: false
  ga4gh: false
  metrics: false
  internal: false
  lfs: false
buckets:
  - bucket: "  EllrottLab  "
    provider: " S3 "
    region: " US-EAST-1 "
    endpoint: " https://MINIO.example/ "
    access_key: " access-key "
    secret_key: " secret-key "
`
	if err := os.WriteFile(configPath, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := buildServerRuntime(context.Background(), cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close(context.Background()) })
	credential, err := runtime.database.GetS3Credential(context.Background(), cfg.Buckets[0].CredentialID)
	if err != nil {
		t.Fatal(err)
	}
	if credential.Bucket != "EllrottLab" || credential.Region != "us-east-1" || credential.Endpoint != "https://MINIO.example" {
		t.Fatalf("persisted credential identity = %+v", credential)
	}
	if credential.AccessKey != " access-key " || credential.SecretKey != " secret-key " {
		t.Fatalf("persisted credential material changed: access=%q secret=%q", credential.AccessKey, credential.SecretKey)
	}
}

func TestApplyCredentialEncryptionConfigSetsMasterKey(t *testing.T) {
	t.Setenv(credentialcipher.CredentialMasterKeyEnv, "")

	cfg := &config.Config{
		CredentialEncryption: config.CredentialEncryptionConfig{
			MasterKey: "ee605db033f6992534def23f9594ffaa58142f8bd9b7ee8ae3de199aed435d97",
		},
	}

	applyCredentialEncryptionConfig(cfg)

	if got := os.Getenv(credentialcipher.CredentialMasterKeyEnv); got != "ee605db033f6992534def23f9594ffaa58142f8bd9b7ee8ae3de199aed435d97" {
		t.Fatalf("expected master key env to be set from config, got %q", got)
	}
}

func TestApplyCredentialEncryptionConfigDoesNotOverrideEnv(t *testing.T) {
	t.Setenv(credentialcipher.CredentialMasterKeyEnv, "existing-master-key")
	t.Setenv(credentialcipher.CredentialLocalKeyFileEnv, "/existing/kek")
	t.Setenv(credentialcipher.DatabaseSQLiteFileEnv, "/existing/drs.db")

	cfg := &config.Config{
		Database: config.DatabaseConfig{
			Sqlite: &config.SqliteConfig{File: "drs.db"},
		},
		CredentialEncryption: config.CredentialEncryptionConfig{
			LocalKeyFile: ".syfon-credential-kek",
			MasterKey:    "ee605db033f6992534def23f9594ffaa58142f8bd9b7ee8ae3de199aed435d97",
		},
	}

	applyCredentialEncryptionConfig(cfg)

	if got := os.Getenv(credentialcipher.CredentialMasterKeyEnv); got != "existing-master-key" {
		t.Fatalf("expected existing master key env to win, got %q", got)
	}
	if got := os.Getenv(credentialcipher.CredentialLocalKeyFileEnv); got != "/existing/kek" {
		t.Fatalf("expected existing local key file env to win, got %q", got)
	}
	if got := os.Getenv(credentialcipher.DatabaseSQLiteFileEnv); got != "/existing/drs.db" {
		t.Fatalf("expected existing sqlite file env to win, got %q", got)
	}
}

func TestLoadConfiguredBucketScopes(t *testing.T) {
	database := &serverBucketStore{
		credentials: map[string]buckets.Credential{
			"calypr": {CredentialID: "calypr", Bucket: "calypr"},
		},
		scopes: make(map[string]buckets.Scope),
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	err := loadConfiguredBucketScopes(context.Background(), database, database, []config.BucketScopeConfig{
		{
			Organization: "calypr",
			ProjectID:    "training",
			Bucket:       "calypr",
			PathPrefix:   "008b435e-c1da-58b8-80f1-3ad2882c43cd",
		},
	}, logger)
	if err != nil {
		t.Fatalf("loadConfiguredBucketScopes returned error: %v", err)
	}

	scope, ok := database.scopes["calypr|training"]
	if !ok {
		t.Fatal("expected bucket scope to be saved")
	}
	if scope.Bucket != "calypr" || scope.PathPrefix != "008b435e-c1da-58b8-80f1-3ad2882c43cd" {
		t.Fatalf("unexpected saved bucket scope: %+v", scope)
	}
}
