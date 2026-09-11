package postgres_test

import (
	"context"
	"strings"
	"testing"

	"github.com/calypr/syfon/internal/buckets"
	"github.com/google/uuid"
)

func TestPostgresSaveBucketConfigurationRollsBackCredentialWhenScopeWriteFails(t *testing.T) {
	db := openPostgresTestStore(t)
	ctx := context.Background()
	suffix := uuid.NewString()
	credentialID := "atomic-credential-" + suffix
	bucket := "atomic-bucket-" + suffix
	organization := "atomic-org-" + suffix
	triggerName := "syfon_test_fail_bucket_scope_" + strings.ReplaceAll(suffix, "-", "")
	functionName := triggerName + "_fn"
	if _, err := db.DB().ExecContext(ctx, `DROP TRIGGER IF EXISTS `+triggerName+` ON bucket_scope`); err != nil {
		t.Fatalf("drop stale failure trigger: %v", err)
	}
	if _, err := db.DB().ExecContext(ctx, `DROP FUNCTION IF EXISTS `+functionName+`()`); err != nil {
		t.Fatalf("drop stale failure function: %v", err)
	}
	if _, err := db.DB().ExecContext(ctx, `CREATE FUNCTION `+functionName+`() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'forced bucket scope failure'; END; $$`); err != nil {
		t.Fatalf("create failure function: %v", err)
	}
	if _, err := db.DB().ExecContext(ctx, `CREATE TRIGGER `+triggerName+` BEFORE INSERT ON bucket_scope FOR EACH ROW EXECUTE FUNCTION `+functionName+`() `); err != nil {
		t.Fatalf("create failure trigger: %v", err)
	}
	t.Cleanup(func() {
		_, _ = db.DB().ExecContext(ctx, `DROP TRIGGER IF EXISTS `+triggerName+` ON bucket_scope`)
		_, _ = db.DB().ExecContext(ctx, `DROP FUNCTION IF EXISTS `+functionName+`() `)
	})

	err := db.SaveBucketConfiguration(ctx, buckets.BucketConfiguration{
		Credential: buckets.Credential{
			CredentialID: credentialID,
			Bucket:       bucket,
			Provider:     "s3",
			AccessKey:    "access-key",
			SecretKey:    "secret-key",
		},
		Organization: organization,
		ProjectID:    "project",
	})
	if err == nil || !strings.Contains(err.Error(), "forced bucket scope failure") {
		t.Fatalf("SaveBucketConfiguration error=%v, want forced scope failure", err)
	}

	var credentialCount, scopeCount int
	if err := db.DB().QueryRowContext(ctx, "SELECT COUNT(*) FROM s3_credential WHERE credential_id = $1", credentialID).Scan(&credentialCount); err != nil {
		t.Fatalf("count credentials: %v", err)
	}
	if err := db.DB().QueryRowContext(ctx, "SELECT COUNT(*) FROM bucket_scope WHERE organization = $1", organization).Scan(&scopeCount); err != nil {
		t.Fatalf("count scopes: %v", err)
	}
	if credentialCount != 0 || scopeCount != 0 {
		t.Fatalf("failed aggregate write left credential_count=%d scope_count=%d", credentialCount, scopeCount)
	}
}
