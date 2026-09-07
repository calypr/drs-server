package records_test

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/calypr/syfon/internal/faults"
	"github.com/calypr/syfon/internal/objects"
	"github.com/calypr/syfon/internal/persistence/sqlite"
)

func TestObjectServiceBulkMutationsTargetLegacyDuplicatePhysicalUUID(t *testing.T) {
	ctx := context.Background()
	fixturePath := filepath.Join(t.TempDir(), "legacy.sqlite")
	database, err := sqlite.NewSqliteDB(fixturePath)
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

	if err := service.BulkDeleteObjects(authenticatedTargetProject, []string{aliasID}); !errors.Is(err, faults.ErrConflict) {
		t.Fatalf("expected alias bulk deletion to be rejected with conflict, got %v", err)
	}
	if _, err := database.GetObject(ctx, objectA); err != nil {
		t.Fatalf("alias rejection must preserve target physical UUID %q: %v", objectA, err)
	}
	if got := readAccessURL(objectB); got != "s3://bucket/original-b" {
		t.Fatalf("alias rejection must preserve sibling physical UUID %q, got %q", objectB, got)
	}
	if err := database.BulkDeleteObjects(authenticatedTargetProject, []string{aliasID}); !errors.Is(err, faults.ErrConflict) {
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
