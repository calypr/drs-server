package server

import (
	"context"
	"io"
	"log/slog"
	"os"
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
	database := &configuredBucketStore{
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

type configuredBucketStore struct {
	credentials map[string]buckets.Credential
	scopes      map[string]buckets.Scope
}

func (s *configuredBucketStore) GetS3Credential(_ context.Context, id string) (*buckets.Credential, error) {
	credential, ok := s.credentials[id]
	if !ok {
		return nil, nil
	}
	return &credential, nil
}

func (s *configuredBucketStore) ListS3Credentials(context.Context) ([]buckets.Credential, error) {
	credentials := make([]buckets.Credential, 0, len(s.credentials))
	for _, credential := range s.credentials {
		credentials = append(credentials, credential)
	}
	return credentials, nil
}

func (s *configuredBucketStore) CreateBucketScope(_ context.Context, scope *buckets.Scope) error {
	if scope == nil {
		return nil
	}
	s.scopes[scope.Organization+"|"+scope.ProjectID] = *scope
	return nil
}
