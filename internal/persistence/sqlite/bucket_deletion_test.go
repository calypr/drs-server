package sqlite

import (
	"context"
	"errors"
	"testing"

	"github.com/calypr/syfon/apigen/errorapi"
	"github.com/calypr/syfon/internal/buckets"
	"github.com/calypr/syfon/internal/persistence/store"
)

func TestBucketScopeConfigurationDeletionRollsBackCredentialFailure(t *testing.T) {
	database := seedBucketDeletion(t)
	ctx := context.Background()
	if _, err := database.DB().ExecContext(ctx, `CREATE TRIGGER fail_bucket_credential_delete BEFORE DELETE ON s3_credential BEGIN SELECT RAISE(ABORT, 'blocked credential delete'); END`); err != nil {
		t.Fatal(err)
	}

	_, err := database.DeleteBucketScopeConfiguration(ctx, buckets.Scope{
		Organization: "org", ProjectID: "project", CredentialID: "physical-bucket", PathPrefix: "prefix",
	})
	if err == nil {
		t.Fatal("DeleteBucketScopeConfiguration() succeeded, want credential deletion failure")
	}
	if _, err := database.GetBucketScope(ctx, "org", "project"); err != nil {
		t.Fatalf("scope was not rolled back: %v", err)
	}
	if _, err := database.GetS3Credential(ctx, "credential-id"); err != nil {
		t.Fatalf("credential was not rolled back: %v", err)
	}
}

func TestStandaloneCredentialDeletionRollsBackScopeFailure(t *testing.T) {
	database := seedBucketDeletion(t)
	ctx := context.Background()
	if _, err := database.DB().ExecContext(ctx, `CREATE TRIGGER fail_bucket_scope_delete BEFORE DELETE ON bucket_scope BEGIN SELECT RAISE(ABORT, 'blocked scope delete'); END`); err != nil {
		t.Fatal(err)
	}

	if err := database.DeleteS3Credential(ctx, "physical-bucket"); err == nil {
		t.Fatal("DeleteS3Credential() succeeded, want scope deletion failure")
	}
	if _, err := database.GetBucketScope(ctx, "org", "project"); err != nil {
		t.Fatalf("scope changed after rollback: %v", err)
	}
	if _, err := database.GetS3Credential(ctx, "credential-id"); err != nil {
		t.Fatalf("credential changed after rollback: %v", err)
	}
}

func TestBucketScopeConfigurationDeletionReturnsAliasesAfterCommit(t *testing.T) {
	database := seedBucketDeletion(t)
	ctx := context.Background()
	aliases, err := database.DeleteBucketScopeConfiguration(ctx, buckets.Scope{
		Organization: "org", ProjectID: "project", CredentialID: "physical-bucket", PathPrefix: "prefix",
	})
	if err != nil {
		t.Fatal(err)
	}
	gotAliases := make(map[string]bool)
	for _, alias := range aliases {
		gotAliases[alias] = true
	}
	if !gotAliases["credential-id"] || !gotAliases["physical-bucket"] {
		t.Fatalf("committed aliases = %v", aliases)
	}
	if _, err := database.GetBucketScope(ctx, "org", "project"); !errors.Is(err, errorapi.ErrBucketScopeNotFound) {
		t.Fatalf("deleted scope lookup error = %v", err)
	}
	if _, err := database.GetS3Credential(ctx, "credential-id"); !errors.Is(err, errorapi.ErrStorageCredentialMissing) {
		t.Fatalf("deleted credential lookup error = %v", err)
	}
}

func TestBucketScopeConfigurationDeletionKeepsCredentialWithSiblingScope(t *testing.T) {
	database := seedBucketDeletion(t)
	ctx := context.Background()
	if err := database.CreateBucketScope(ctx, &buckets.Scope{
		Organization: "org", ProjectID: "sibling", CredentialID: "credential-id", Bucket: "physical-bucket",
	}); err != nil {
		t.Fatal(err)
	}
	aliases, err := database.DeleteBucketScopeConfiguration(ctx, buckets.Scope{
		Organization: "org", ProjectID: "project", CredentialID: "physical-bucket", PathPrefix: "prefix",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(aliases) != 0 {
		t.Fatalf("aliases = %v, want none while credential remains", aliases)
	}
	if _, err := database.GetS3Credential(ctx, "credential-id"); err != nil {
		t.Fatalf("shared credential was deleted: %v", err)
	}
}

func seedBucketDeletion(t *testing.T) *store.Store {
	t.Helper()
	database, err := NewSqliteDB(":memory:", nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	configuration := buckets.BucketConfiguration{
		Credential:   buckets.Credential{CredentialID: "credential-id", Bucket: "physical-bucket", Provider: "s3"},
		Organization: "org",
		ProjectID:    "project",
		PathPrefix:   "prefix",
	}
	if err := database.SaveBucketConfiguration(context.Background(), configuration); err != nil {
		t.Fatal(err)
	}
	return database
}
