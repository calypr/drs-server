package postgres_test

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/calypr/syfon/apigen/drs"
	"github.com/calypr/syfon/apigen/errorapi"
	"github.com/calypr/syfon/internal/persistence/credentialcipher"
	postgresdb "github.com/calypr/syfon/internal/persistence/postgres"
	"github.com/calypr/syfon/internal/persistence/store"
	"github.com/google/uuid"
)

const usageFlushBarrierKey int64 = 73002
const usageAggregateBarrierKey int64 = 73003

func TestPostgresUsageFlushDoesNotDeleteConcurrentAppend(t *testing.T) {
	db := openPostgresUsageTestStore(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	objectID := "usage-append-" + uuid.NewString()
	now := time.Now().UTC()
	if err := db.RegisterObjects(ctx, []drs.DrsObject{postgresUsageObject(objectID, objectID, now)}); err != nil {
		t.Fatal(err)
	}
	if err := db.RecordFileUpload(ctx, objectID); err != nil {
		t.Fatal(err)
	}
	installUsageAggregateBarrier(t, db, ctx)
	barrierConn := holdUsageFlushBarrier(t, db, ctx, usageAggregateBarrierKey)

	flushDone := make(chan error, 1)
	go func() {
		_, err := db.GetFileUsage(ctx, objectID)
		flushDone <- err
	}()
	waitForAdvisoryWait(t, db, ctx, usageAggregateBarrierKey)

	if err := db.RecordFileUpload(ctx, objectID); err != nil {
		t.Fatalf("RecordFileUpload during flush: %v", err)
	}
	if _, err := barrierConn.ExecContext(ctx, `SELECT pg_advisory_unlock($1)`, usageAggregateBarrierKey); err != nil {
		t.Fatal(err)
	}
	if err := <-flushDone; err != nil {
		t.Fatalf("GetFileUsage flush: %v", err)
	}

	var aggregated, pending int64
	if err := db.DB().QueryRowContext(ctx, `SELECT upload_count FROM object_usage WHERE object_id = $1`, objectID).Scan(&aggregated); err != nil {
		t.Fatal(err)
	}
	if err := db.DB().QueryRowContext(ctx, `SELECT count(*) FROM object_usage_event WHERE object_id = $1`, objectID).Scan(&pending); err != nil {
		t.Fatal(err)
	}
	if aggregated != 1 || pending != 1 {
		t.Fatalf("concurrent append usage = aggregated %d, pending %d, want 1 and 1", aggregated, pending)
	}

	if _, err := db.GetFileUsage(ctx, objectID); err != nil {
		t.Fatal(err)
	}
	if err := db.DB().QueryRowContext(ctx, `SELECT upload_count FROM object_usage WHERE object_id = $1`, objectID).Scan(&aggregated); err != nil {
		t.Fatal(err)
	}
	if err := db.DB().QueryRowContext(ctx, `SELECT count(*) FROM object_usage_event WHERE object_id = $1`, objectID).Scan(&pending); err != nil {
		t.Fatal(err)
	}
	if aggregated != 2 || pending != 0 {
		t.Fatalf("queued append after second flush = aggregated %d, pending %d, want 2 and 0", aggregated, pending)
	}
}

func TestPostgresUsageFlushDoesNotDoubleCountConcurrentFlushes(t *testing.T) {
	db := openPostgresUsageTestStore(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	objectID := "usage-flush-" + uuid.NewString()
	now := time.Now().UTC()
	if err := db.RegisterObjects(ctx, []drs.DrsObject{postgresUsageObject(objectID, objectID, now)}); err != nil {
		t.Fatal(err)
	}
	if err := db.RecordFileUpload(ctx, objectID); err != nil {
		t.Fatal(err)
	}
	installUsageDeleteBarrier(t, db, ctx)
	barrierConn := holdUsageFlushBarrier(t, db, ctx, usageFlushBarrierKey)

	done := make(chan error, 2)
	go func() {
		_, err := db.GetFileUsage(ctx, objectID)
		done <- err
	}()
	waitForAdvisoryWait(t, db, ctx, usageFlushBarrierKey)
	go func() {
		_, err := db.GetFileUsage(ctx, objectID)
		done <- err
	}()
	waitForUsageFlushBlocker(t, db, ctx)

	if _, err := barrierConn.ExecContext(ctx, `SELECT pg_advisory_unlock($1)`, usageFlushBarrierKey); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := <-done; err != nil {
			t.Fatalf("concurrent GetFileUsage: %v", err)
		}
	}

	var aggregated, pending int64
	if err := db.DB().QueryRowContext(ctx, `SELECT upload_count FROM object_usage WHERE object_id = $1`, objectID).Scan(&aggregated); err != nil {
		t.Fatal(err)
	}
	if err := db.DB().QueryRowContext(ctx, `SELECT count(*) FROM object_usage_event WHERE object_id = $1`, objectID).Scan(&pending); err != nil {
		t.Fatal(err)
	}
	if aggregated != 1 || pending != 0 {
		t.Fatalf("concurrent flush usage = aggregated %d, pending %d, want 1 and 0", aggregated, pending)
	}
}

func TestPostgresUsageEventsForMissingObjectsRemainQueued(t *testing.T) {
	db := openPostgresUsageTestStore(t)
	ctx := context.Background()
	objectID := "usage-missing-" + uuid.NewString()
	if err := db.RecordFileUpload(ctx, objectID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.GetFileUsage(ctx, objectID); !errors.Is(err, errorapi.ErrFileUsageNotFound) {
		t.Fatalf("GetFileUsage missing object = %v, want file-usage-not-found", err)
	}
	var pending int
	if err := db.DB().QueryRowContext(ctx, `SELECT count(*) FROM object_usage_event WHERE object_id = $1`, objectID).Scan(&pending); err != nil {
		t.Fatal(err)
	}
	if pending != 1 {
		t.Fatalf("missing object pending events = %d, want 1", pending)
	}
	now := time.Now().UTC()
	if err := db.RegisterObjects(ctx, []drs.DrsObject{postgresUsageObject(objectID, objectID, now)}); err != nil {
		t.Fatal(err)
	}
	usage, err := db.GetFileUsage(ctx, objectID)
	if err != nil {
		t.Fatal(err)
	}
	if usage.UploadCount == nil || *usage.UploadCount != 1 {
		t.Fatalf("queued missing-object upload count = %#v, want 1", usage.UploadCount)
	}
}

func TestPostgresUsageFlushRollbackRetainsEvents(t *testing.T) {
	db := openPostgresUsageTestStore(t)
	ctx := context.Background()
	objectID := "usage-rollback-" + uuid.NewString()
	now := time.Now().UTC()
	if err := db.RegisterObjects(ctx, []drs.DrsObject{postgresUsageObject(objectID, objectID, now)}); err != nil {
		t.Fatal(err)
	}
	if err := db.RecordFileUpload(ctx, objectID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.DB().ExecContext(ctx, `CREATE OR REPLACE FUNCTION audit_usage_flush_failure() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'usage flush blocked'; END; $$`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.DB().ExecContext(ctx, `CREATE TRIGGER audit_usage_flush_failure BEFORE INSERT ON object_usage FOR EACH ROW EXECUTE FUNCTION audit_usage_flush_failure()`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.GetFileUsage(ctx, objectID); err == nil {
		t.Fatal("GetFileUsage succeeded with flush failure trigger")
	}
	if _, err := db.DB().ExecContext(ctx, `DROP TRIGGER audit_usage_flush_failure ON object_usage`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.DB().ExecContext(ctx, `DROP FUNCTION audit_usage_flush_failure()`); err != nil {
		t.Fatal(err)
	}
	usage, err := db.GetFileUsage(ctx, objectID)
	if err != nil {
		t.Fatal(err)
	}
	if usage.UploadCount == nil || *usage.UploadCount != 1 {
		t.Fatalf("rollback upload count = %#v, want 1", usage.UploadCount)
	}
}

func openPostgresUsageTestStore(t *testing.T) *store.Store {
	t.Helper()
	dsn := os.Getenv("SYFON_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("SYFON_TEST_POSTGRES_DSN is not configured")
	}
	t.Setenv(credentialcipher.CredentialLocalKeyFileEnv, filepath.Join(t.TempDir(), "credential.key"))
	raw, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatalf("open PostgreSQL schema admin connection: %v", err)
	}
	schema := "syfon_usage_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err := raw.Exec(`CREATE SCHEMA ` + schema); err != nil {
		_ = raw.Close()
		t.Fatalf("create temporary PostgreSQL schema: %v", err)
	}
	t.Cleanup(func() {
		_, _ = raw.Exec(`DROP SCHEMA IF EXISTS ` + schema + ` CASCADE`)
		_ = raw.Close()
	})
	db, err := postgresdb.NewPostgresDB(postgresTestSchemaDSN(t, dsn, schema), nil)
	if err != nil {
		t.Fatalf("open PostgreSQL usage test store: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func installUsageDeleteBarrier(t *testing.T, db *store.Store, ctx context.Context) {
	t.Helper()
	if _, err := db.DB().ExecContext(ctx, `CREATE OR REPLACE FUNCTION audit_usage_delete_barrier() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN PERFORM pg_advisory_xact_lock(73002); RETURN OLD; END; $$`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.DB().ExecContext(ctx, `CREATE TRIGGER audit_usage_delete_barrier BEFORE DELETE ON object_usage_event FOR EACH ROW EXECUTE FUNCTION audit_usage_delete_barrier()`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = db.DB().ExecContext(context.Background(), `DROP TRIGGER IF EXISTS audit_usage_delete_barrier ON object_usage_event`)
		_, _ = db.DB().ExecContext(context.Background(), `DROP FUNCTION IF EXISTS audit_usage_delete_barrier()`)
	})
}

func installUsageAggregateBarrier(t *testing.T, db *store.Store, ctx context.Context) {
	t.Helper()
	if _, err := db.DB().ExecContext(ctx, `CREATE OR REPLACE FUNCTION audit_usage_aggregate_barrier() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN PERFORM pg_advisory_xact_lock(73003); RETURN NEW; END; $$`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.DB().ExecContext(ctx, `CREATE TRIGGER audit_usage_aggregate_barrier BEFORE INSERT ON object_usage FOR EACH ROW EXECUTE FUNCTION audit_usage_aggregate_barrier()`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = db.DB().ExecContext(context.Background(), `DROP TRIGGER IF EXISTS audit_usage_aggregate_barrier ON object_usage`)
		_, _ = db.DB().ExecContext(context.Background(), `DROP FUNCTION IF EXISTS audit_usage_aggregate_barrier()`)
	})
}

func holdUsageFlushBarrier(t *testing.T, db *store.Store, ctx context.Context, key int64) *sql.Conn {
	t.Helper()
	conn, err := db.DB().Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.ExecContext(ctx, `SELECT pg_advisory_lock($1)`, key); err != nil {
		_ = conn.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = conn.ExecContext(context.Background(), `SELECT pg_advisory_unlock($1)`, key)
		_ = conn.Close()
	})
	return conn
}

func waitForAdvisoryWait(t *testing.T, db *store.Store, ctx context.Context, key int64) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		var waiting int
		if err := db.DB().QueryRowContext(ctx, `SELECT count(*) FROM pg_locks WHERE locktype = 'advisory' AND objid = $1 AND NOT granted`, key).Scan(&waiting); err == nil && waiting == 1 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("usage flush did not reach the delete barrier")
}

func waitForUsageFlushBlocker(t *testing.T, db *store.Store, ctx context.Context) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		var blocked int
		if err := db.DB().QueryRowContext(ctx, `
			SELECT count(*)
			FROM pg_stat_activity a
			WHERE a.query LIKE '%object_usage_event%'
			  AND cardinality(pg_blocking_pids(a.pid)) > 0`).Scan(&blocked); err == nil && blocked >= 2 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("second usage flush did not become blocked by the first")
}
