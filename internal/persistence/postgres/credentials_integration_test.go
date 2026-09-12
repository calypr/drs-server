package postgres_test

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/calypr/syfon/internal/buckets"
	"github.com/calypr/syfon/internal/persistence/store"
	"github.com/google/uuid"
)

func TestPostgresConcurrentCredentialSavesSerializeBeforeUniquenessCheck(t *testing.T) {
	db := openPostgresTestStore(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	suffix := uuid.NewString()
	bucket := "gated-save-bucket-" + suffix
	gateKey := int64(74011)
	triggerName := "syfon_test_gate_credential_save_" + strings.ReplaceAll(suffix, "-", "")
	functionName := triggerName + "_fn"
	if _, err := db.DB().ExecContext(ctx, `CREATE FUNCTION `+functionName+`() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN PERFORM pg_advisory_xact_lock(`+fmt.Sprint(gateKey)+`); RETURN NEW; END; $$`); err != nil {
		t.Fatalf("create save gate function: %v", err)
	}
	if _, err := db.DB().ExecContext(ctx, `CREATE TRIGGER `+triggerName+` BEFORE INSERT ON s3_credential FOR EACH ROW EXECUTE FUNCTION `+functionName+`() `); err != nil {
		t.Fatalf("create save gate trigger: %v", err)
	}
	t.Cleanup(func() {
		_, _ = db.DB().ExecContext(context.Background(), `DROP TRIGGER IF EXISTS `+triggerName+` ON s3_credential`)
		_, _ = db.DB().ExecContext(context.Background(), `DROP FUNCTION IF EXISTS `+functionName+`() `)
	})

	ownerConn, err := db.DB().Conn(ctx)
	if err != nil {
		t.Fatalf("open save gate owner connection: %v", err)
	}
	if _, err := ownerConn.ExecContext(ctx, `SELECT pg_advisory_lock($1)`, gateKey); err != nil {
		_ = ownerConn.Close()
		t.Fatalf("hold save gate: %v", err)
	}
	t.Cleanup(func() {
		_, _ = ownerConn.ExecContext(context.Background(), `SELECT pg_advisory_unlock($1)`, gateKey)
		_ = ownerConn.Close()
	})

	errs := make(chan error, 2)
	go func() {
		errs <- db.SaveS3Credential(ctx, &buckets.Credential{
			CredentialID: "gated-save-a-" + suffix,
			Bucket:       bucket,
			Provider:     "s3",
			AccessKey:    "access-a",
			SecretKey:    "secret-a",
		})
	}()
	waitForAdvisoryWait(t, db, ctx, gateKey)
	go func() {
		errs <- db.SaveS3Credential(ctx, &buckets.Credential{
			CredentialID: "gated-save-b-" + suffix,
			Bucket:       bucket,
			Provider:     "s3",
			AccessKey:    "access-b",
			SecretKey:    "secret-b",
		})
	}()
	waitForContentWriteBlock(t, db, ctx)
	if _, err := ownerConn.ExecContext(ctx, `SELECT pg_advisory_unlock($1)`, gateKey); err != nil {
		t.Fatalf("release save gate: %v", err)
	}

	var successCount, conflictCount int
	for range 2 {
		err := <-errs
		if err == nil {
			successCount++
			continue
		}
		if strings.Contains(err.Error(), "already configured under credential") {
			conflictCount++
			continue
		}
		t.Fatalf("concurrent credential save: %v", err)
	}
	if successCount != 1 || conflictCount != 1 {
		t.Fatalf("concurrent credential saves = %d successes, %d conflicts, want one each", successCount, conflictCount)
	}
	var rows int
	if err := db.DB().QueryRowContext(ctx, `SELECT count(*) FROM s3_credential WHERE bucket = $1`, bucket).Scan(&rows); err != nil {
		t.Fatalf("count saved credentials: %v", err)
	}
	if rows != 1 {
		t.Fatalf("saved credentials for bucket = %d, want 1", rows)
	}
}

func waitForContentWriteBlock(t *testing.T, db *store.Store, ctx context.Context) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		var blocked int
		if err := db.DB().QueryRowContext(ctx, `
			SELECT count(*)
			FROM pg_stat_activity a
			WHERE a.query LIKE '%hashtextextended%'
			  AND cardinality(pg_blocking_pids(a.pid)) > 0`).Scan(&blocked); err == nil && blocked >= 1 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("concurrent mutation did not block on the content-write lock")
}

func TestPostgresConcurrentCreateAndLastBucketScopeDeleteSerializes(t *testing.T) {
	db := openPostgresTestStore(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	suffix := uuid.NewString()
	credentialID := "gated-scope-credential-" + suffix
	bucket := "gated-scope-bucket-" + suffix
	organization := "gated-scope-org-" + suffix
	if err := db.SaveBucketConfiguration(ctx, buckets.BucketConfiguration{
		Credential: buckets.Credential{
			CredentialID: credentialID,
			Bucket:       bucket,
			Provider:     "s3",
			AccessKey:    "access",
			SecretKey:    "secret",
		},
		Organization: organization,
		ProjectID:    "delete-me",
	}); err != nil {
		t.Fatalf("seed bucket configuration: %v", err)
	}

	gateKey := int64(74012)
	triggerName := "syfon_test_gate_scope_create_" + strings.ReplaceAll(suffix, "-", "")
	functionName := triggerName + "_fn"
	if _, err := db.DB().ExecContext(ctx, `CREATE FUNCTION `+functionName+`() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN PERFORM pg_advisory_xact_lock(`+fmt.Sprint(gateKey)+`); RETURN NEW; END; $$`); err != nil {
		t.Fatalf("create scope gate function: %v", err)
	}
	if _, err := db.DB().ExecContext(ctx, `CREATE TRIGGER `+triggerName+` BEFORE INSERT ON bucket_scope FOR EACH ROW EXECUTE FUNCTION `+functionName+`() `); err != nil {
		t.Fatalf("create scope gate trigger: %v", err)
	}
	t.Cleanup(func() {
		_, _ = db.DB().ExecContext(context.Background(), `DROP TRIGGER IF EXISTS `+triggerName+` ON bucket_scope`)
		_, _ = db.DB().ExecContext(context.Background(), `DROP FUNCTION IF EXISTS `+functionName+`() `)
	})

	ownerConn, err := db.DB().Conn(ctx)
	if err != nil {
		t.Fatalf("open scope gate owner connection: %v", err)
	}
	if _, err := ownerConn.ExecContext(ctx, `SELECT pg_advisory_lock($1)`, gateKey); err != nil {
		_ = ownerConn.Close()
		t.Fatalf("hold scope gate: %v", err)
	}
	t.Cleanup(func() {
		_, _ = ownerConn.ExecContext(context.Background(), `SELECT pg_advisory_unlock($1)`, gateKey)
		_ = ownerConn.Close()
	})

	createDone := make(chan error, 1)
	go func() {
		createDone <- db.CreateBucketScope(ctx, &buckets.Scope{
			Organization: organization,
			ProjectID:    "create-after-create-lock",
			CredentialID: credentialID,
			Bucket:       bucket,
		})
	}()
	waitForAdvisoryWait(t, db, ctx, gateKey)

	deleteDone := make(chan error, 1)
	go func() {
		_, err := db.DeleteBucketScopeConfiguration(ctx, buckets.Scope{
			Organization: organization,
			ProjectID:    "delete-me",
			CredentialID: credentialID,
		})
		deleteDone <- err
	}()
	waitForContentWriteBlock(t, db, ctx)
	if _, err := ownerConn.ExecContext(ctx, `SELECT pg_advisory_unlock($1)`, gateKey); err != nil {
		t.Fatalf("release scope gate: %v", err)
	}

	if err := <-createDone; err != nil {
		t.Fatalf("create concurrent scope: %v", err)
	}
	if err := <-deleteDone; err != nil {
		t.Fatalf("delete concurrent scope: %v", err)
	}
	var scopeCount, credentialCount int
	if err := db.DB().QueryRowContext(ctx, `SELECT count(*) FROM bucket_scope WHERE organization = $1`, organization).Scan(&scopeCount); err != nil {
		t.Fatalf("count concurrent scopes: %v", err)
	}
	if err := db.DB().QueryRowContext(ctx, `SELECT count(*) FROM s3_credential WHERE credential_id = $1`, credentialID).Scan(&credentialCount); err != nil {
		t.Fatalf("count concurrent credentials: %v", err)
	}
	if scopeCount != 1 || credentialCount != 1 {
		t.Fatalf("concurrent create/delete left scope_count=%d credential_count=%d, want one pair", scopeCount, credentialCount)
	}
}

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

func TestPostgresConcurrentLastScopeDeletionRemovesCredential(t *testing.T) {
	db := openPostgresTestStore(t)
	ctx := context.Background()
	suffix := uuid.NewString()
	credentialID := "delete-credential-" + suffix
	bucket := "delete-bucket-" + suffix
	organization := "delete-org-" + suffix
	triggerName := "syfon_test_slow_bucket_delete_" + strings.ReplaceAll(suffix, "-", "")
	functionName := triggerName + "_fn"

	for _, projectID := range []string{"one", "two"} {
		if err := db.SaveBucketConfiguration(ctx, buckets.BucketConfiguration{
			Credential: buckets.Credential{
				CredentialID: credentialID,
				Bucket:       bucket,
				Provider:     "s3",
				AccessKey:    "access-key",
				SecretKey:    "secret-key",
			},
			Organization: organization,
			ProjectID:    projectID,
		}); err != nil {
			t.Fatalf("seed project %s: %v", projectID, err)
		}
	}

	if _, err := db.DB().ExecContext(ctx, `CREATE FUNCTION `+functionName+`() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN PERFORM pg_sleep(0.1); RETURN OLD; END; $$`); err != nil {
		t.Fatalf("create slow-delete function: %v", err)
	}
	if _, err := db.DB().ExecContext(ctx, `CREATE TRIGGER `+triggerName+` BEFORE DELETE ON bucket_scope FOR EACH ROW EXECUTE FUNCTION `+functionName+`() `); err != nil {
		t.Fatalf("create slow-delete trigger: %v", err)
	}
	t.Cleanup(func() {
		_, _ = db.DB().ExecContext(ctx, `DROP TRIGGER IF EXISTS `+triggerName+` ON bucket_scope`)
		_, _ = db.DB().ExecContext(ctx, `DROP FUNCTION IF EXISTS `+functionName+`() `)
	})

	start := make(chan struct{})
	errs := make(chan error, 2)
	var ready sync.WaitGroup
	ready.Add(2)
	for _, projectID := range []string{"one", "two"} {
		go func(projectID string) {
			ready.Done()
			<-start
			_, err := db.DeleteBucketScopeConfiguration(ctx, buckets.Scope{
				Organization: organization,
				ProjectID:    projectID,
				CredentialID: credentialID,
			})
			errs <- err
		}(projectID)
	}
	ready.Wait()
	close(start)
	for range 2 {
		if err := <-errs; err != nil {
			t.Fatalf("delete scope: %v", err)
		}
	}

	var scopeCount, credentialCount int
	if err := db.DB().QueryRowContext(ctx, "SELECT COUNT(*) FROM bucket_scope WHERE organization = $1", organization).Scan(&scopeCount); err != nil {
		t.Fatalf("count scopes: %v", err)
	}
	if err := db.DB().QueryRowContext(ctx, "SELECT COUNT(*) FROM s3_credential WHERE credential_id = $1", credentialID).Scan(&credentialCount); err != nil {
		t.Fatalf("count credentials: %v", err)
	}
	if scopeCount != 0 || credentialCount != 0 {
		t.Fatalf("concurrent deletion left scope_count=%d credential_count=%d", scopeCount, credentialCount)
	}
}
