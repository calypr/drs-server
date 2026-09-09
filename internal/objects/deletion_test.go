package objects_test

import (
	"context"
	"database/sql"
	"errors"
	"github.com/calypr/syfon/apigen/errorapi"
	"github.com/calypr/syfon/internal/objects"
	"github.com/calypr/syfon/internal/persistence/credentialcipher"
	"github.com/calypr/syfon/internal/persistence/sqlite"
	"github.com/calypr/syfon/internal/persistence/store"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

const deleteResource = "/organization/org/project/owned"
const otherResource = "/organization/org/project/other"

func seedDeletionRecords(t *testing.T) *store.Store {
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
		if err := db.RegisterObjects(context.Background(), []objects.Record{record}); err != nil {
			t.Fatal(err)
		}
	}
	return db
}

func deletionContext() context.Context {
	return buildLocalAuthzContext(map[string]map[string]bool{deleteResource: {"read": true, "update": true, "delete": true}})
}

func assertRecordExists(t *testing.T, db *store.Store, id string, exists bool) {
	t.Helper()
	_, err := db.GetObject(context.Background(), id)
	if exists && err != nil {
		t.Fatalf("%s should survive: %v", id, err)
	}
	if !exists && !errors.Is(err, errorapi.ErrNotFound) {
		t.Fatalf("%s should be deleted, got %v", id, err)
	}
}

func TestDeleteObjectRequiresEveryResource(t *testing.T) {
	db := seedDeletionRecords(t)
	service := newTestService(db)
	if err := service.DeleteObject(deletionContext(), "shared"); !errors.Is(err, errorapi.ErrAccessDenied) {
		t.Fatalf("shared delete: %v", err)
	}
	assertRecordExists(t, db, "shared", true)
	if err := service.DeleteObject(deletionContext(), "owned"); err != nil {
		t.Fatal(err)
	}
	assertRecordExists(t, db, "owned", false)
	if err := service.DeleteObject(deletionContext(), "missing"); !errors.Is(err, errorapi.ErrNotFound) {
		t.Fatalf("missing delete: %v", err)
	}
}

func TestDeleteRejectsPhysicalStorageOptionWithoutMutation(t *testing.T) {
	db := seedDeletionRecords(t)
	service := newTestService(db)
	opts := objects.DeleteOptions{DeleteStorageData: true}
	if err := service.DeleteObjectWithOptions(deletionContext(), "owned", opts); !errors.Is(err, errorapi.ErrConflict) {
		t.Fatalf("single delete: %v", err)
	}
	if err := service.BulkDeleteObjectsWithOptions(deletionContext(), []string{"owned"}, opts); !errors.Is(err, errorapi.ErrConflict) {
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
	if err := db.CreateObjectAlias(context.Background(), "alias", "owned"); err != nil {
		t.Fatal(err)
	}
	if err := service.BulkDeleteObjects(deletionContext(), []string{"owned", "alias"}); !errors.Is(err, errorapi.ErrConflict) {
		t.Fatalf("alias delete: %v", err)
	}
	assertRecordExists(t, db, "owned", true)
	if id, err := db.ResolveObjectAlias(context.Background(), "alias"); err != nil || id != "owned" {
		t.Fatalf("alias changed: %q, %v", id, err)
	}
}

func TestDeleteByChecksumsPreservesSharedRecords(t *testing.T) {
	db := seedDeletionRecords(t)
	service := newTestService(db)
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
}

func TestDeleteByScopeRemovesOnlyThatProjectReference(t *testing.T) {
	db := seedDeletionRecords(t)
	service := newTestService(db)
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
	if _, err := service.DeleteBulkByScope(deletionContext(), "org", "other"); !errors.Is(err, errorapi.ErrAccessDenied) {
		t.Fatalf("unauthorized scope delete: %v", err)
	}
}

func TestObjectServiceBulkMutationsTargetLegacyDuplicatePhysicalUUID(t *testing.T) {
	ctx := context.Background()
	fixturePath := filepath.Join(t.TempDir(), "legacy.sqlite")
	cipher, err := credentialcipher.NewFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	database, err := sqlite.NewSqliteDB(fixturePath, cipher)
	if err != nil {
		t.Fatalf("NewSqliteDB failed: %v", err)
	}
	raw, err := sql.Open("sqlite3", fixturePath)
	if err != nil {
		t.Fatalf("open fixture database: %v", err)
	}

	const (
		objectA = "3f5b5dac-f07d-5fdb-998d-532a95dd42d1"
		objectB = "f9be6500-ea29-5427-843f-eb44dcdc6fb5"
		aliasID = "legacy-alias"
		sha     = "faec17cafc7af76bbdbe96a499545ff00ce2ef0ff4c65e05571dbbe0f17435ce"
	)
	resource := "/organization/org/project/repair"
	now := time.Date(2026, 9, 4, 16, 0, 0, 0, time.UTC)
	insertObject := func(id, accessURL string) {
		t.Helper()
		if _, err := raw.ExecContext(ctx, `
			INSERT INTO drs_object (id, size, created_time, updated_time, name, version, description)
			VALUES (?, ?, ?, ?, ?, ?, ?)
		`, id, 1, now, now, "", "", ""); err != nil {
			t.Fatalf("insert object %q: %v", id, err)
		}
		if _, err := raw.ExecContext(ctx, `
			INSERT INTO drs_object_checksum (object_id, type, checksum)
			VALUES (?, ?, ?)
		`, id, "sha256", sha); err != nil {
			t.Fatalf("insert checksum for %q: %v", id, err)
		}
		if _, err := raw.ExecContext(ctx, `
			INSERT INTO drs_object_controlled_access (object_id, resource)
			VALUES (?, ?)
		`, id, resource); err != nil {
			t.Fatalf("insert authorization for %q: %v", id, err)
		}
		if _, err := raw.ExecContext(ctx, `
			INSERT INTO drs_object_access_method (object_id, url, type)
			VALUES (?, ?, ?)
		`, id, accessURL, "s3"); err != nil {
			t.Fatalf("insert access method for %q: %v", id, err)
		}
	}
	insertObject(objectA, "s3://bucket/original-a")
	insertObject(objectB, "s3://bucket/original-b")
	if _, err := raw.ExecContext(ctx, `
		INSERT INTO drs_object_alias (alias_id, object_id)
		VALUES (?, ?)
	`, aliasID, objectA); err != nil {
		t.Fatalf("insert alias %q: %v", aliasID, err)
	}
	if err := raw.Close(); err != nil {
		t.Fatalf("close fixture database: %v", err)
	}

	service := newTestService(database)
	authenticatedTargetProject := buildGen3Context(map[string]map[string]bool{
		resource: {"update": true, "delete": true},
	})
	if err := service.BulkUpdateAccessMethods(authenticatedTargetProject, map[string][]objects.AccessMethod{
		objectA: {{
			Type:      "s3",
			AccessUrl: &objects.AccessURL{Url: "s3://bucket/repaired-a"},
		}},
	}); err != nil {
		t.Fatalf("BulkUpdateAccessMethods through object service failed: %v", err)
	}

	readAccessURL := func(id string) string {
		t.Helper()
		obj, err := database.GetObject(ctx, id)
		if err != nil {
			t.Fatalf("GetObject(%q) failed: %v", id, err)
		}
		if obj.AccessMethods == nil || len(*obj.AccessMethods) != 1 || (*obj.AccessMethods)[0].AccessUrl == nil {
			t.Fatalf("expected one access method for %q, got %+v", id, obj.AccessMethods)
		}
		return (*obj.AccessMethods)[0].AccessUrl.Url
	}
	if got := readAccessURL(objectA); got != "s3://bucket/repaired-a" {
		t.Fatalf("expected target physical UUID %q to be updated, got %q", objectA, got)
	}
	if got := readAccessURL(objectB); got != "s3://bucket/original-b" {
		t.Fatalf("expected sibling physical UUID %q to remain unchanged, got %q", objectB, got)
	}

	if err := service.BulkDeleteObjects(authenticatedTargetProject, []string{aliasID}); !errors.Is(err, errorapi.ErrConflict) {
		t.Fatalf("expected alias bulk deletion to be rejected with conflict, got %v", err)
	}
	if _, err := database.GetObject(ctx, objectA); err != nil {
		t.Fatalf("alias rejection must preserve target physical UUID %q: %v", objectA, err)
	}
	if got := readAccessURL(objectB); got != "s3://bucket/original-b" {
		t.Fatalf("alias rejection must preserve sibling physical UUID %q, got %q", objectB, got)
	}
	if err := database.BulkDeleteObjects(authenticatedTargetProject, []string{aliasID}); !errors.Is(err, errorapi.ErrConflict) {
		t.Fatalf("expected direct database alias bulk deletion to preserve ambiguity guard, got %v", err)
	}

	if err := service.BulkDeleteObjects(authenticatedTargetProject, []string{objectA}); err != nil {
		t.Fatalf("BulkDeleteObjects through object service failed: %v", err)
	}
	if _, err := database.GetObject(ctx, objectA); err == nil {
		t.Fatalf("expected target physical UUID %q to be deleted", objectA)
	}
	if got := readAccessURL(objectB); got != "s3://bucket/original-b" {
		t.Fatalf("expected sibling physical UUID %q to remain readable after deletion, got %q", objectB, got)
	}
}
