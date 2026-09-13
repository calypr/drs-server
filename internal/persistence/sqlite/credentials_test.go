package sqlite

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/calypr/syfon/apigen/errorapi"
	"github.com/calypr/syfon/internal/buckets"
	"github.com/calypr/syfon/internal/persistence/credentialcipher"
)

func TestGetS3CredentialDatabaseFailureIsNotMissing(t *testing.T) {
	db, err := NewSqliteDB(":memory:", nil)
	if err != nil {
		t.Fatalf("NewSqliteDB: %v", err)
	}
	db.DB().Close()

	_, err = db.GetS3Credential(context.Background(), "missing")
	if err == nil || errors.Is(err, errorapi.ErrStorageCredentialMissing) {
		t.Fatalf("GetS3Credential error=%v, want non-missing database failure", err)
	}
}

func TestSaveBucketConfigurationRollsBackOnScopeWriteFailure(t *testing.T) {
	for _, tc := range []struct {
		name          string
		triggerTiming string
		seed          bool
	}{
		{name: "insert", triggerTiming: "INSERT"},
		{name: "update", triggerTiming: "UPDATE", seed: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(credentialcipher.CredentialMasterKeyEnv, "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY=")
			db, err := NewSqliteDB(":memory:", nil)
			if err != nil {
				t.Fatalf("NewSqliteDB: %v", err)
			}
			t.Cleanup(func() { _ = db.Close() })
			ctx := context.Background()
			original := buckets.BucketConfiguration{
				Credential: buckets.Credential{
					CredentialID: "credential-id",
					Bucket:       "physical-bucket",
					Provider:     "s3",
					AccessKey:    "original-access-key",
					SecretKey:    "original-secret-key",
				},
				Organization: "org",
				ProjectID:    "project",
				PathPrefix:   "original-prefix",
			}
			if tc.seed {
				if err := db.SaveS3Credential(ctx, &original.Credential); err != nil {
					t.Fatalf("seed credential: %v", err)
				}
				if err := db.CreateBucketScope(ctx, &buckets.Scope{
					Organization: original.Organization,
					ProjectID:    original.ProjectID,
					CredentialID: original.Credential.CredentialID,
					Bucket:       original.Credential.Bucket,
					PathPrefix:   original.PathPrefix,
				}); err != nil {
					t.Fatalf("seed scope: %v", err)
				}
			}
			if _, err := db.DB().ExecContext(ctx, `
				CREATE TRIGGER fail_bucket_scope_write
				BEFORE `+tc.triggerTiming+` ON bucket_scope
				BEGIN
					SELECT RAISE(ABORT, 'forced bucket scope failure');
				END`); err != nil {
				t.Fatalf("create failure trigger: %v", err)
			}

			updated := original
			updated.Credential.AccessKey = "updated-access-key"
			updated.Credential.SecretKey = "updated-secret-key"
			updated.PathPrefix = "updated-prefix"
			err = db.SaveBucketConfiguration(ctx, updated)
			if err == nil || !strings.Contains(err.Error(), "forced bucket scope failure") {
				t.Fatalf("SaveBucketConfiguration error=%v, want forced scope failure", err)
			}

			if tc.seed {
				gotCredential, err := db.GetS3Credential(ctx, original.Credential.CredentialID)
				if err != nil {
					t.Fatalf("read rolled-back credential: %v", err)
				}
				if gotCredential.AccessKey != original.Credential.AccessKey || gotCredential.SecretKey != original.Credential.SecretKey {
					t.Fatalf("credential after failed update=%+v, want original secrets", gotCredential)
				}
				gotScope, err := db.GetBucketScope(ctx, original.Organization, original.ProjectID)
				if err != nil {
					t.Fatalf("read rolled-back scope: %v", err)
				}
				if gotScope.PathPrefix != original.PathPrefix {
					t.Fatalf("scope after failed update=%+v, want original prefix %q", gotScope, original.PathPrefix)
				}
				return
			}

			var credentialCount, scopeCount int
			if err := db.DB().QueryRowContext(ctx, "SELECT COUNT(*) FROM s3_credential").Scan(&credentialCount); err != nil {
				t.Fatalf("count credentials: %v", err)
			}
			if err := db.DB().QueryRowContext(ctx, "SELECT COUNT(*) FROM bucket_scope").Scan(&scopeCount); err != nil {
				t.Fatalf("count scopes: %v", err)
			}
			if credentialCount != 0 || scopeCount != 0 {
				t.Fatalf("failed aggregate write left credential_count=%d scope_count=%d", credentialCount, scopeCount)
			}
		})
	}
}
