package core

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/calypr/syfon/apigen/server/drs"
	"github.com/calypr/syfon/internal/db/sqlite"
	"github.com/calypr/syfon/internal/faults"
	"github.com/calypr/syfon/internal/models"
	"github.com/calypr/syfon/internal/testutils"
)

func ptr[T any](v T) *T { return &v }

func TestRegisterBulk_RegistersCandidate(t *testing.T) {
	database := testutils.NewInMemoryDB()
	om := NewObjectManager(database, nil)

	candidates := []drs.DrsObjectCandidate{
		{
			Aliases: ptr([]string{"id:test-register-bulk"}),
			Checksums: []drs.Checksum{{
				Type:     "sha256",
				Checksum: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			}},
			AccessMethods: &[]drs.AccessMethod{{
				Type: "s3",
				AccessUrl: &struct {
					Headers *[]string `json:"headers,omitempty"`
					Url     string    `json:"url"`
				}{Url: "s3://bucket/test-register-bulk"},
			}},
			Size: 1,
		},
	}

	count, err := om.RegisterBulk(context.Background(), candidates)
	if err != nil {
		t.Fatalf("RegisterBulk error: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected count=1, got=%d", count)
	}

	obj, err := database.GetObject(context.Background(), "test-register-bulk")
	if err != nil {
		t.Fatalf("expected registered object, got error: %v", err)
	}
	if obj == nil || obj.Id != "test-register-bulk" {
		t.Fatalf("unexpected object: %+v", obj)
	}
}

func TestRegisterBulk_InvalidChecksum(t *testing.T) {
	database := testutils.NewInMemoryDB()
	om := NewObjectManager(database, nil)

	candidates := []drs.DrsObjectCandidate{{
		Aliases: ptr([]string{"id:test-invalid-checksum"}),
		Checksums: []drs.Checksum{{
			Type:     "md5",
			Checksum: "abc",
		}},
		Size: 1,
	}}

	if _, err := om.RegisterBulk(context.Background(), candidates); err == nil {
		t.Fatalf("expected RegisterBulk error for invalid checksum")
	}
}

func TestBulkDeleteObjects_DeletesAuthorizedObjects(t *testing.T) {
	database := testutils.NewInMemoryDB()
	om := NewObjectManager(database, nil)

	_, err := om.RegisterBulk(context.Background(), []drs.DrsObjectCandidate{{
		Aliases: ptr([]string{"id:test-delete-bulk"}),
		Checksums: []drs.Checksum{{
			Type:     "sha256",
			Checksum: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		}},
		AccessMethods: &[]drs.AccessMethod{{
			Type: "s3",
			AccessUrl: &struct {
				Headers *[]string `json:"headers,omitempty"`
				Url     string    `json:"url"`
			}{Url: "s3://bucket/test-delete-bulk"},
		}},
		Size: 1,
	}})
	if err != nil {
		t.Fatalf("seed RegisterBulk error: %v", err)
	}

	if err := om.BulkDeleteObjects(context.Background(), []string{"test-delete-bulk"}); err != nil {
		t.Fatalf("BulkDeleteObjects error: %v", err)
	}
	if _, err := database.GetObject(context.Background(), "test-delete-bulk"); err == nil {
		t.Fatalf("expected object to be deleted")
	}
}

func TestObjectManagerBulkMutationsTargetLegacyDuplicatePhysicalUUID(t *testing.T) {
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
		`, id, accessURL, drs.AccessMethodTypeS3); err != nil {
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

	om := NewObjectManager(database, nil)
	authenticatedTargetProject := buildGen3Context(map[string]map[string]bool{
		resource: {"update": true, "delete": true},
	})
	if err := om.BulkUpdateAccessMethods(authenticatedTargetProject, map[string][]drs.AccessMethod{
		objectA: {{
			Type: drs.AccessMethodTypeS3,
			AccessUrl: &struct {
				Headers *[]string `json:"headers,omitempty"`
				Url     string    `json:"url"`
			}{Url: "s3://bucket/repaired-a"},
		}},
	}); err != nil {
		t.Fatalf("BulkUpdateAccessMethods through ObjectManager failed: %v", err)
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

	if err := om.BulkDeleteObjects(authenticatedTargetProject, []string{aliasID}); !errors.Is(err, faults.ErrConflict) {
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

	if err := om.BulkDeleteObjects(authenticatedTargetProject, []string{objectA}); err != nil {
		t.Fatalf("BulkDeleteObjects through ObjectManager failed: %v", err)
	}
	if _, err := database.GetObject(ctx, objectA); err == nil {
		t.Fatalf("expected target physical UUID %q to be deleted", objectA)
	}
	if got := readAccessURL(objectB); got != "s3://bucket/original-b" {
		t.Fatalf("expected sibling physical UUID %q to remain readable after deletion, got %q", objectB, got)
	}
}

func TestRegisterObjects_CanonicalizesProjectChecksumDuplicates(t *testing.T) {
	database := testutils.NewInMemoryDB()
	om := NewObjectManager(database, nil)
	now := time.Now().UTC()
	later := now.Add(time.Minute)
	accessURL1 := "s3://bucket/original"
	accessURL2 := "s3://bucket/renamed"

	first := models.InternalObject{
		Authorizations: map[string][]string{"org": {"proj"}},
		DrsObject: drs.DrsObject{
			Id:          "did-1",
			Name:        ptr("original.tsv"),
			Size:        42,
			CreatedTime: now,
			UpdatedTime: &now,
			Checksums:   []drs.Checksum{{Type: "sha256", Checksum: "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"}},
			AccessMethods: &[]drs.AccessMethod{{
				Type: "s3",
				AccessUrl: &struct {
					Headers *[]string `json:"headers,omitempty"`
					Url     string    `json:"url"`
				}{Url: accessURL1},
			}},
		},
	}
	second := models.InternalObject{
		Authorizations: map[string][]string{"org": {"proj"}},
		DrsObject: drs.DrsObject{
			Id:          "did-2",
			Name:        ptr("renamed.tsv"),
			Size:        42,
			CreatedTime: later,
			UpdatedTime: &later,
			Checksums:   first.Checksums,
			AccessMethods: &[]drs.AccessMethod{{
				Type: "s3",
				AccessUrl: &struct {
					Headers *[]string `json:"headers,omitempty"`
					Url     string    `json:"url"`
				}{Url: accessURL2},
			}},
		},
	}

	if err := om.RegisterObjects(context.Background(), []models.InternalObject{first}); err != nil {
		t.Fatalf("RegisterObjects(first) error: %v", err)
	}
	if err := om.RegisterObjects(context.Background(), []models.InternalObject{second}); err != nil {
		t.Fatalf("RegisterObjects(second) error: %v", err)
	}

	records, err := om.GetObjectsByChecksum(context.Background(), first.Checksums[0].Checksum, "")
	if err != nil {
		t.Fatalf("GetObjectsByChecksum error: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("expected 1 canonical record, got %d", len(records))
	}
	if records[0].Id != "did-1" {
		t.Fatalf("expected canonical did-1, got %q", records[0].Id)
	}
	if got := records[0].Name; got == nil || *got != "renamed.tsv" {
		t.Fatalf("expected latest name renamed.tsv, got %+v", got)
	}
	if !slices.Equal(records[0].NameAliases, []string{"original.tsv"}) {
		t.Fatalf("unexpected name aliases: %#v", records[0].NameAliases)
	}

	aliasObj, err := om.GetObject(context.Background(), "did-2", "")
	if err != nil {
		t.Fatalf("GetObject(alias) error: %v", err)
	}
	if aliasObj.Id != "did-1" {
		t.Fatalf("expected alias lookup to return canonical did-1, got %q", aliasObj.Id)
	}
	scopeIDs, err := om.ListObjectIDsByScope(context.Background(), "org", "proj", "")
	if err != nil {
		t.Fatalf("ListObjectIDsByScope error: %v", err)
	}
	if !slices.Equal(scopeIDs, []string{"did-1"}) {
		t.Fatalf("unexpected scoped ids: %#v", scopeIDs)
	}
}

func TestRegisterObjects_ReusesContentAcrossProjects(t *testing.T) {
	database, err := sqlite.NewSqliteDB(":memory:")
	if err != nil {
		t.Fatalf("NewSqliteDB failed: %v", err)
	}
	om := NewObjectManager(database, nil)
	sha := "dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd"
	now := time.Date(2026, 9, 4, 16, 0, 0, 0, time.UTC)
	later := now.Add(time.Minute)
	firstResource := "/organization/org/project/first"
	secondResource := "/organization/org/project/second"

	first := models.InternalObject{
		Authorizations: map[string][]string{"org": {"first"}},
		DrsObject: drs.DrsObject{
			Id:               "canonical-did",
			Name:             ptr("first.tsv"),
			Size:             42,
			CreatedTime:      now,
			UpdatedTime:      &now,
			Checksums:        []drs.Checksum{{Type: "sha256", Checksum: sha}},
			ControlledAccess: &[]string{firstResource},
			AccessMethods: &[]drs.AccessMethod{{
				Type: "s3",
				AccessUrl: &struct {
					Headers *[]string `json:"headers,omitempty"`
					Url     string    `json:"url"`
				}{Url: "s3://bucket/first"},
			}},
		},
	}
	second := models.InternalObject{
		Authorizations: map[string][]string{"org": {"second"}},
		DrsObject: drs.DrsObject{
			Id:               "second-did",
			Name:             ptr("second.tsv"),
			Size:             42,
			CreatedTime:      later,
			UpdatedTime:      &later,
			Checksums:        []drs.Checksum{{Type: "sha256", Checksum: sha}},
			ControlledAccess: &[]string{secondResource},
			AccessMethods: &[]drs.AccessMethod{{
				Type: "s3",
				AccessUrl: &struct {
					Headers *[]string `json:"headers,omitempty"`
					Url     string    `json:"url"`
				}{Url: "s3://bucket/second"},
			}},
		},
	}

	if err := om.RegisterObjects(context.Background(), []models.InternalObject{first}); err != nil {
		t.Fatalf("RegisterObjects(first) error: %v", err)
	}
	if err := om.RegisterObjects(context.Background(), []models.InternalObject{second}); err != nil {
		t.Fatalf("RegisterObjects(second) error: %v", err)
	}

	physicalRecords, err := database.GetObjectsByChecksum(context.Background(), sha)
	if err != nil {
		t.Fatalf("database.GetObjectsByChecksum error: %v", err)
	}
	if len(physicalRecords) != 1 {
		t.Fatalf("expected one physical content record, got %d", len(physicalRecords))
	}

	byChecksum, err := om.GetObjectsByChecksum(context.Background(), sha, "")
	if err != nil {
		t.Fatalf("GetObjectsByChecksum error: %v", err)
	}
	if len(byChecksum) != 1 {
		t.Fatalf("expected one canonical checksum record, got %d", len(byChecksum))
	}
	canonical := byChecksum[0]
	if canonical.Id != first.Id {
		t.Fatalf("expected deterministic read representative %q, got %q", first.Id, canonical.Id)
	}
	if canonical.AccessMethods == nil || len(*canonical.AccessMethods) != 2 {
		t.Fatalf("expected both access methods, got %+v", canonical.AccessMethods)
	}
	if canonical.ControlledAccess == nil || len(*canonical.ControlledAccess) != 2 || !slices.Contains(*canonical.ControlledAccess, firstResource) || !slices.Contains(*canonical.ControlledAccess, secondResource) {
		t.Fatalf("expected both controlled-access resources, got %+v", canonical.ControlledAccess)
	}

	for _, ident := range []string{first.Id, second.Id} {
		got, err := om.GetObject(context.Background(), ident, "")
		if err != nil {
			t.Fatalf("GetObject(%q) error: %v", ident, err)
		}
		if got.Id != canonical.Id {
			t.Fatalf("GetObject(%q) returned id %q, want canonical %q", ident, got.Id, canonical.Id)
		}
		if got.AccessMethods == nil || len(*got.AccessMethods) != 2 {
			t.Fatalf("GetObject(%q) lost merged access methods: %+v", ident, got.AccessMethods)
		}
	}
}
