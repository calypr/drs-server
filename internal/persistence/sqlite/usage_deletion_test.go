package sqlite

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/calypr/syfon/apigen/drs"
)

func TestDeletedObjectUsageDoesNotAttachToRecreatedObject(t *testing.T) {
	database, err := NewSqliteDB(":memory:", nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })

	ctx := context.Background()
	now := time.Now().UTC()
	if err := database.RegisterObjects(ctx, []drs.DrsObject{usageDeletionObject("reused", "a", now)}); err != nil {
		t.Fatal(err)
	}
	if err := database.RecordFileUpload(ctx, "reused"); err != nil {
		t.Fatal(err)
	}
	if err := database.DeleteObject(ctx, "reused"); err != nil {
		t.Fatal(err)
	}
	if err := database.RegisterObjects(ctx, []drs.DrsObject{usageDeletionObject("reused", "b", now)}); err != nil {
		t.Fatal(err)
	}
	usage, err := database.GetFileUsage(ctx, "reused")
	if err != nil {
		t.Fatal(err)
	}
	if got := sqliteTestInt64Val(usage.UploadCount); got != 0 {
		t.Fatalf("recreated object upload count = %d, want 0", got)
	}
}

func TestBulkDeleteClearsCanonicalUsageForAliasesOnly(t *testing.T) {
	database, err := NewSqliteDB(":memory:", nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })

	ctx := context.Background()
	now := time.Now().UTC()
	objects := []drs.DrsObject{
		usageDeletionObject("one", "c", now),
		usageDeletionObject("two", "d", now),
		usageDeletionObject("retained", "e", now),
	}
	if err := database.RegisterObjects(ctx, objects); err != nil {
		t.Fatal(err)
	}
	if err := database.CreateObjectAlias(ctx, "two-alias", "two"); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"one", "two", "retained"} {
		if err := database.RecordFileUpload(ctx, id); err != nil {
			t.Fatal(err)
		}
	}
	if err := database.BulkDeleteObjects(ctx, []string{"one", "two-alias", "missing"}); err != nil {
		t.Fatal(err)
	}
	if err := database.RegisterObjects(ctx, []drs.DrsObject{
		usageDeletionObject("one", "f", now),
		usageDeletionObject("two", "1", now),
	}); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"one", "two"} {
		usage, err := database.GetFileUsage(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		if got := sqliteTestInt64Val(usage.UploadCount); got != 0 {
			t.Fatalf("recreated object %q upload count = %d, want 0", id, got)
		}
	}
	retained, err := database.GetFileUsage(ctx, "retained")
	if err != nil {
		t.Fatal(err)
	}
	if got := sqliteTestInt64Val(retained.UploadCount); got != 1 {
		t.Fatalf("retained object upload count = %d, want 1", got)
	}
}

func TestObjectDeleteFailureRetainsPendingUsage(t *testing.T) {
	database, err := NewSqliteDB(":memory:", nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })

	ctx := context.Background()
	now := time.Now().UTC()
	if err := database.RegisterObjects(ctx, []drs.DrsObject{usageDeletionObject("blocked", "2", now)}); err != nil {
		t.Fatal(err)
	}
	if err := database.RecordFileUpload(ctx, "blocked"); err != nil {
		t.Fatal(err)
	}
	if _, err := database.DB().ExecContext(ctx, `CREATE TRIGGER block_object_delete BEFORE DELETE ON drs_object BEGIN SELECT RAISE(ABORT, 'blocked'); END`); err != nil {
		t.Fatal(err)
	}
	if err := database.DeleteObject(ctx, "blocked"); err == nil {
		t.Fatal("DeleteObject() succeeded, want trigger failure")
	}
	if _, err := database.DB().ExecContext(ctx, `DROP TRIGGER block_object_delete`); err != nil {
		t.Fatal(err)
	}
	usage, err := database.GetFileUsage(ctx, "blocked")
	if err != nil {
		t.Fatal(err)
	}
	if got := sqliteTestInt64Val(usage.UploadCount); got != 1 {
		t.Fatalf("upload count after rolled-back deletion = %d, want 1", got)
	}
}

func usageDeletionObject(id, checksumByte string, now time.Time) drs.DrsObject {
	return drs.DrsObject{
		Id:          id,
		Size:        1,
		CreatedTime: now,
		UpdatedTime: &now,
		Checksums:   []drs.Checksum{{Type: "sha256", Checksum: strings.Repeat(checksumByte, 64)}},
	}
}
