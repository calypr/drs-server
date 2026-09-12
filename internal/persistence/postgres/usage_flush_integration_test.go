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

func TestPostgresScopedUsageSummaryAndPageBindArgumentsInPlaceholderOrder(t *testing.T) {
	db := openPostgresUsageTestStore(t)
	ctx := context.Background()
	suffix := uuid.NewString()
	resourceOne := "/organization/usage-bind/project/one-" + suffix
	resourceTwo := "/organization/usage-bind/project/two-" + suffix
	objectOne := "usage-bind-one-" + suffix
	objectTwo := "usage-bind-two-" + suffix
	objectUnscoped := "usage-bind-unscoped-" + suffix
	objectOther := "usage-bind-other-" + suffix
	now := time.Now().UTC()
	objects := []drs.DrsObject{
		postgresScopedUsageObject(objectOne, objectOne, now, resourceOne),
		postgresScopedUsageObject(objectTwo, objectTwo, now, resourceTwo),
		postgresScopedUsageObject(objectUnscoped, objectUnscoped, now),
		postgresScopedUsageObject(objectOther, objectOther, now, "/organization/usage-bind/project/other-"+suffix),
	}
	if err := db.RegisterObjects(ctx, objects); err != nil {
		t.Fatalf("RegisterObjects: %v", err)
	}
	t.Cleanup(func() {
		for _, object := range objects {
			_ = db.DeleteObject(ctx, object.Id)
		}
	})
	if err := db.RecordFileUpload(ctx, objectOne); err != nil {
		t.Fatal(err)
	}
	if err := db.RecordFileDownload(ctx, objectOne); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := db.RecordFileUpload(ctx, objectTwo); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.RecordFileUpload(ctx, objectUnscoped); err != nil {
		t.Fatal(err)
	}
	for range 3 {
		if err := db.RecordFileUpload(ctx, objectOther); err != nil {
			t.Fatal(err)
		}
	}

	cutoff := time.Now().UTC().Add(time.Minute)
	tests := []struct {
		name                        string
		resources                   []string
		includeUnscoped             bool
		inactiveSince               *time.Time
		wantIDs                     []string
		wantFiles, wantUploads      int64
		wantDownloads, wantInactive int64
	}{
		{
			name:          "one resource with cutoff",
			resources:     []string{resourceOne},
			inactiveSince: &cutoff,
			wantIDs:       []string{objectOne},
			wantFiles:     1,
			wantUploads:   1,
			wantDownloads: 1,
			wantInactive:  1,
		},
		{
			name:          "mixed resources with cutoff",
			resources:     []string{resourceOne, resourceTwo},
			inactiveSince: &cutoff,
			wantIDs:       []string{objectTwo, objectOne},
			wantFiles:     2,
			wantUploads:   3,
			wantDownloads: 1,
			wantInactive:  2,
		},
		{
			name:            "resource and unscoped with cutoff",
			resources:       []string{resourceOne},
			includeUnscoped: true,
			inactiveSince:   &cutoff,
			wantIDs:         []string{objectUnscoped, objectOne},
			wantFiles:       2,
			wantUploads:     2,
			wantDownloads:   1,
			wantInactive:    2,
		},
		{
			name:          "empty resource result",
			resources:     []string{"/organization/usage-bind/project/missing-" + suffix},
			inactiveSince: &cutoff,
			wantIDs:       []string{},
			wantFiles:     0,
			wantUploads:   0,
			wantDownloads: 0,
			wantInactive:  0,
		},
		{
			name:          "one resource without cutoff",
			resources:     []string{resourceTwo},
			wantIDs:       []string{objectTwo},
			wantFiles:     1,
			wantUploads:   2,
			wantDownloads: 0,
			wantInactive:  0,
		},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			summary, err := db.GetFileUsageSummaryByResources(ctx, testCase.resources, testCase.includeUnscoped, testCase.inactiveSince)
			if err != nil {
				t.Fatalf("GetFileUsageSummaryByResources: %v", err)
			}
			if got := postgresUsageCount(summary.TotalFiles); got != testCase.wantFiles {
				t.Fatalf("summary total files = %d, want %d", got, testCase.wantFiles)
			}
			if got := postgresUsageCount(summary.TotalUploads); got != testCase.wantUploads {
				t.Fatalf("summary total uploads = %d, want %d", got, testCase.wantUploads)
			}
			if got := postgresUsageCount(summary.TotalDownloads); got != testCase.wantDownloads {
				t.Fatalf("summary total downloads = %d, want %d", got, testCase.wantDownloads)
			}
			if got := postgresUsageCount(summary.InactiveFileCount); got != testCase.wantInactive {
				t.Fatalf("summary inactive files = %d, want %d", got, testCase.wantInactive)
			}

			page, err := db.ListFileUsagePageByResources(ctx, testCase.resources, testCase.includeUnscoped, 10, 0, testCase.inactiveSince)
			if err != nil {
				t.Fatalf("ListFileUsagePageByResources: %v", err)
			}
			gotIDs := make([]string, 0, len(page))
			for _, item := range page {
				if item.ObjectId == nil {
					t.Fatal("usage page contained a row without an object ID")
				}
				gotIDs = append(gotIDs, *item.ObjectId)
			}
			if strings.Join(gotIDs, "\x00") != strings.Join(testCase.wantIDs, "\x00") {
				t.Fatalf("usage page IDs = %v, want %v", gotIDs, testCase.wantIDs)
			}
		})
	}
}

func postgresScopedUsageObject(id, checksumSeed string, now time.Time, resources ...string) drs.DrsObject {
	object := postgresUsageObject(id, checksumSeed, now)
	if len(resources) > 0 {
		object.ControlledAccess = &resources
	}
	return object
}

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
