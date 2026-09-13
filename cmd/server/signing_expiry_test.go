package server

import (
	"context"
	"log/slog"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/calypr/syfon/internal/config"
	"github.com/calypr/syfon/internal/persistence/credentialcipher"
	"github.com/calypr/syfon/internal/transfers"
)

func TestResolveSigningExpiryDefaultsAndRejectsOverflow(t *testing.T) {
	got, err := resolveSigningExpiry(60)
	if err != nil || got != time.Minute {
		t.Fatalf("configured expiry = %s, want %s", got, time.Minute)
	}
	for _, seconds := range []int{0, -1} {
		got, err := resolveSigningExpiry(seconds)
		if err != nil || got != 15*time.Minute {
			t.Fatalf("expiry for %d seconds = %s, want %s", seconds, got, 15*time.Minute)
		}
	}
	if _, err := resolveSigningExpiry(int(maxSigningExpirySeconds + 1)); err == nil {
		t.Fatal("expected duration overflow to be rejected")
	}
}

func TestRuntimeUsesConfiguredSigningExpiry(t *testing.T) {
	t.Setenv(credentialcipher.CredentialMasterKeyEnv, strings.Repeat("a", 64))
	runtime, err := buildServerRuntime(context.Background(), &config.Config{
		Database: config.DatabaseConfig{Sqlite: &config.SqliteConfig{File: ":memory:"}},
		Auth:     config.AuthConfig{Mode: config.AuthModeLocal},
		Routes:   config.RoutesConfig{Internal: true},
		Signing:  config.SigningConfig{DefaultExpirySeconds: 60},
		Buckets: []config.BucketConfig{{
			Bucket: "bucket", Provider: "s3", Region: "us-east-1", AccessKey: "key", SecretKey: "secret", Endpoint: "http://example.test",
		}},
		BucketScopes: []config.BucketScopeConfig{{Organization: "org", ProjectID: "project", Bucket: "bucket"}},
	}, slog.Default())
	if err != nil {
		t.Fatalf("build runtime: %v", err)
	}
	t.Cleanup(func() {
		if err := runtime.Close(context.Background()); err != nil {
			t.Errorf("close runtime: %v", err)
		}
	})

	result, err := runtime.transferService.UploadURL(context.Background(), transfers.UploadRequest{
		Key: "object", Scope: &transfers.AccessScope{Organization: "org", Project: "project"},
	})
	if err != nil {
		t.Fatalf("issue upload URL: %v", err)
	}
	signed, err := url.Parse(result.URL)
	if err != nil {
		t.Fatalf("parse signed URL: %v", err)
	}
	if got := signed.Query().Get("X-Amz-Expires"); got != "60" {
		t.Fatalf("X-Amz-Expires = %q, want 60", got)
	}
}
