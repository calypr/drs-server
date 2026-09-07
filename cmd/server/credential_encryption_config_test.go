package server

import (
	"context"
	"io"
	"log/slog"
	"os"
	"testing"

	"github.com/calypr/syfon/internal/config"
	"github.com/calypr/syfon/internal/crypto"
	"github.com/calypr/syfon/internal/testutils"
)

func TestApplyCredentialEncryptionConfig(t *testing.T) {
	t.Setenv(crypto.CredentialMasterKeyEnv, "")
	t.Setenv(crypto.CredentialLocalKeyFileEnv, "")
	t.Setenv(crypto.DatabaseSQLiteFileEnv, "")

	cfg := &config.Config{
		Database: config.DatabaseConfig{
			Sqlite: &config.SqliteConfig{File: "drs.db"},
		},
		CredentialEncryption: config.CredentialEncryptionConfig{
			LocalKeyFile: ".syfon-credential-kek",
		},
	}

	applyCredentialEncryptionConfig(cfg)

	if got := os.Getenv(crypto.CredentialLocalKeyFileEnv); got != ".syfon-credential-kek" {
		t.Fatalf("expected local key file env to be set from config, got %q", got)
	}
	if got := os.Getenv(crypto.DatabaseSQLiteFileEnv); got != "drs.db" {
		t.Fatalf("expected sqlite file env to be set from config, got %q", got)
	}
}

func TestApplyCredentialEncryptionConfigSetsMasterKey(t *testing.T) {
	t.Setenv(crypto.CredentialMasterKeyEnv, "")

	cfg := &config.Config{
		CredentialEncryption: config.CredentialEncryptionConfig{
			MasterKey: "ee605db033f6992534def23f9594ffaa58142f8bd9b7ee8ae3de199aed435d97",
		},
	}

	applyCredentialEncryptionConfig(cfg)

	if got := os.Getenv(crypto.CredentialMasterKeyEnv); got != "ee605db033f6992534def23f9594ffaa58142f8bd9b7ee8ae3de199aed435d97" {
		t.Fatalf("expected master key env to be set from config, got %q", got)
	}
}

func TestApplyCredentialEncryptionConfigDoesNotOverrideEnv(t *testing.T) {
	t.Setenv(crypto.CredentialMasterKeyEnv, "existing-master-key")
	t.Setenv(crypto.CredentialLocalKeyFileEnv, "/existing/kek")
	t.Setenv(crypto.DatabaseSQLiteFileEnv, "/existing/drs.db")

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

	if got := os.Getenv(crypto.CredentialMasterKeyEnv); got != "existing-master-key" {
		t.Fatalf("expected existing master key env to win, got %q", got)
	}
	if got := os.Getenv(crypto.CredentialLocalKeyFileEnv); got != "/existing/kek" {
		t.Fatalf("expected existing local key file env to win, got %q", got)
	}
	if got := os.Getenv(crypto.DatabaseSQLiteFileEnv); got != "/existing/drs.db" {
		t.Fatalf("expected existing sqlite file env to win, got %q", got)
	}
}

func TestLoadConfiguredBucketScopes(t *testing.T) {
	database := &testutils.MockDatabase{}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	err := loadConfiguredBucketScopes(context.Background(), database, []config.BucketScopeConfig{
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

	scope, err := database.GetBucketScope(context.Background(), "calypr", "training")
	if err != nil {
		t.Fatalf("expected bucket scope to be saved: %v", err)
	}
	if scope.Bucket != "calypr" || scope.PathPrefix != "008b435e-c1da-58b8-80f1-3ad2882c43cd" {
		t.Fatalf("unexpected saved bucket scope: %+v", scope)
	}
}
