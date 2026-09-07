package records_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/calypr/syfon/internal/faults"
	"github.com/calypr/syfon/internal/objects"
	objectrecords "github.com/calypr/syfon/internal/objects/records"
	"github.com/calypr/syfon/internal/persistence/sqlite"
)

const deleteResource = "/organization/org/project/owned"
const otherResource = "/organization/org/project/other"

func seedDeletionRecords(t *testing.T) *sqlite.SqliteDB {
	t.Helper()
	db := newSQLiteDatabase(t)
	for _, seed := range []struct {
		id        string
		hash      string
		resources []string
	}{
		{"owned", "a", []string{deleteResource}},
		{"shared", "b", []string{deleteResource, otherResource}},
		{"other", "c", []string{otherResource}},
	} {
		record := objects.Record{
			Id: objects.RecordID(seed.id), CreatedTime: time.Now().UTC(), Size: 1,
			Checksums:        []objects.Checksum{{Type: "sha256", Checksum: strings.Repeat(seed.hash, 64)}},
			ControlledAccess: &seed.resources,
		}
		if err := db.CreateObject(context.Background(), &record); err != nil {
			t.Fatal(err)
		}
	}
	return db
}

func deletionContext() context.Context {
	return buildLocalAuthzContext(map[string]map[string]bool{deleteResource: {"read": true, "update": true, "delete": true}})
}

func assertRecordExists(t *testing.T, db *sqlite.SqliteDB, id string, exists bool) {
	t.Helper()
	_, err := db.GetObject(context.Background(), id)
	if exists && err != nil {
		t.Fatalf("%s should survive: %v", id, err)
	}
	if !exists && !errors.Is(err, faults.ErrNotFound) {
		t.Fatalf("%s should be deleted, got %v", id, err)
	}
}

func TestDeleteObjectRequiresEveryResource(t *testing.T) {
	db := seedDeletionRecords(t)
	service := newTestService(db)
	if err := service.DeleteObject(deletionContext(), "shared"); !errors.Is(err, faults.ErrUnauthorized) {
		t.Fatalf("shared delete: %v", err)
	}
	assertRecordExists(t, db, "shared", true)
	if err := service.DeleteObject(deletionContext(), "owned"); err != nil {
		t.Fatal(err)
	}
	assertRecordExists(t, db, "owned", false)
	if err := service.DeleteObject(deletionContext(), "missing"); !errors.Is(err, faults.ErrNotFound) {
		t.Fatalf("missing delete: %v", err)
	}
}

func TestDeleteRejectsPhysicalStorageOptionWithoutMutation(t *testing.T) {
	db := seedDeletionRecords(t)
	service := newTestService(db)
	opts := objectrecords.DeleteOptions{DeleteStorageData: true}
	if err := service.DeleteObjectWithOptions(deletionContext(), "owned", opts); !errors.Is(err, faults.ErrConflict) {
		t.Fatalf("single delete: %v", err)
	}
	if err := service.BulkDeleteObjectsWithOptions(deletionContext(), []string{"owned"}, opts); !errors.Is(err, faults.ErrConflict) {
		t.Fatalf("bulk delete: %v", err)
	}
	assertRecordExists(t, db, "owned", true)
}

func TestBulkDeleteFiltersUnauthorizedAndDuplicateIDs(t *testing.T) {
	db := seedDeletionRecords(t)
	service := newTestService(db)
	if err := service.BulkDeleteObjects(deletionContext(), []string{"owned", "owned", "shared", "other", "missing", " "}); err != nil {
		t.Fatal(err)
	}
	assertRecordExists(t, db, "owned", false)
	assertRecordExists(t, db, "shared", true)
	assertRecordExists(t, db, "other", true)
	if err := service.BulkDeleteObjects(deletionContext(), []string{"missing", "shared"}); err != nil {
		t.Fatal(err)
	}
}

func TestBulkDeleteRejectsAliasBeforeDeletingAnyRecord(t *testing.T) {
	db := seedDeletionRecords(t)
	service := newTestService(db)
	if err := service.CreateObjectAlias(deletionContext(), "alias", "owned"); err != nil {
		t.Fatal(err)
	}
	if err := service.BulkDeleteObjects(deletionContext(), []string{"owned", "alias"}); !errors.Is(err, faults.ErrConflict) {
		t.Fatalf("alias delete: %v", err)
	}
	assertRecordExists(t, db, "owned", true)
	if id, err := db.ResolveObjectAlias(context.Background(), "alias"); err != nil || id != "owned" {
		t.Fatalf("alias changed: %q, %v", id, err)
	}
}

func TestCreateObjectAliasRequiresUpdateAccess(t *testing.T) {
	db := seedDeletionRecords(t)
	service := newTestService(db)
	if err := service.CreateObjectAlias(deletionContext(), "denied", "other"); !errors.Is(err, faults.ErrUnauthorized) {
		t.Fatalf("unauthorized alias: %v", err)
	}
	if _, err := db.ResolveObjectAlias(context.Background(), "denied"); !errors.Is(err, faults.ErrNotFound) {
		t.Fatalf("unauthorized alias persisted: %v", err)
	}
	if err := service.CreateObjectAlias(deletionContext(), "missing", "missing"); !errors.Is(err, faults.ErrNotFound) {
		t.Fatalf("missing alias target: %v", err)
	}
}

func TestDeleteByChecksumsPreservesSharedRecords(t *testing.T) {
	for _, optimized := range []bool{false, true} {
		t.Run(map[bool]string{false: "fallback", true: "optimized"}[optimized], func(t *testing.T) {
			db := seedDeletionRecords(t)
			deps := objectrecords.Dependencies{Reader: db, Writer: db, Content: db}
			if optimized {
				deps.Authorized = db
			}
			service := objectrecords.NewService(deps)
			hashes := []string{strings.Repeat("a", 64), strings.Repeat("a", 64), strings.Repeat("b", 64), strings.Repeat("c", 64), "missing"}
			count, err := service.DeleteObjectsByChecksums(deletionContext(), hashes)
			if err != nil || count != 1 {
				t.Fatalf("delete checksums = %d, %v", count, err)
			}
			assertRecordExists(t, db, "owned", false)
			assertRecordExists(t, db, "shared", true)
			assertRecordExists(t, db, "other", true)
			count, err = service.DeleteObjectsByChecksums(deletionContext(), hashes)
			if err != nil || count != 0 {
				t.Fatalf("repeat delete = %d, %v", count, err)
			}
		})
	}
}

func TestDeleteByScopeRemovesOnlyThatProjectReference(t *testing.T) {
	for _, optimized := range []bool{false, true} {
		t.Run(map[bool]string{false: "fallback", true: "optimized"}[optimized], func(t *testing.T) {
			db := seedDeletionRecords(t)
			deps := objectrecords.Dependencies{Reader: db, Scope: db, AccessPolicy: db}
			if optimized {
				deps.Authorized = db
			}
			service := objectrecords.NewService(deps)
			count, err := service.DeleteBulkByScope(deletionContext(), "org", "owned")
			if err != nil || count != 2 {
				t.Fatalf("scope delete = %d, %v", count, err)
			}
			shared, err := db.GetObject(context.Background(), "shared")
			if err != nil {
				t.Fatal(err)
			}
			if got := objects.AccessResources(shared); !slices.Equal(got, []string{otherResource}) {
				t.Fatalf("remaining shared resources = %v", got)
			}
			assertRecordExists(t, db, "other", true)
			count, err = service.DeleteBulkByScope(deletionContext(), "org", "owned")
			if err != nil || count != 0 {
				t.Fatalf("repeat scope delete = %d, %v", count, err)
			}
			if _, err := service.DeleteBulkByScope(deletionContext(), "org", "other"); !errors.Is(err, faults.ErrUnauthorized) {
				t.Fatalf("unauthorized scope delete: %v", err)
			}
		})
	}
}
