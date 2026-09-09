package records_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/calypr/syfon/apigen/errorapi"
	clientaccess "github.com/calypr/syfon/client/access"
	"github.com/calypr/syfon/internal/objects"
	objectrecords "github.com/calypr/syfon/internal/objects/records"
)

func TestBulkOverwriteObjects_ReplacesProjectChecksumSibling(t *testing.T) {
	resource, err := clientaccess.ResourcePath("org", "project")
	if err != nil {
		t.Fatal(err)
	}
	sha := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	oldName := "old"
	newName := "new"
	db := &bulkOverwriteStore{Objects: map[string]*objects.Record{
		"target-did": {
			Id: "target-did", Name: &oldName, Checksums: []objects.Checksum{{Type: "sha256", Checksum: sha}}, ControlledAccess: &[]string{resource},
		},
	}}
	om := newTestService(db)
	candidate := objects.Record{

		Id:               "source-did",
		Name:             &newName,
		Checksums:        []objects.Checksum{{Type: "sha256", Checksum: sha}},
		ControlledAccess: &[]string{resource},
	}
	result, err := om.BulkOverwriteObjects(buildGen3Context(map[string]map[string]bool{resource: {"update": true}}), "org", "project", []objects.Record{candidate})
	if err != nil {
		t.Fatalf("BulkOverwriteObjects returned error: %v", err)
	}
	if result.Replaced != 1 || result.ChecksumMatched != 1 || result.Created != 0 {
		t.Fatalf("unexpected result: %+v", result)
	}
	if _, ok := db.Objects["source-did"]; ok {
		t.Fatal("checksum sibling should preserve the target DID")
	}
	got := db.Objects["target-did"]
	if got == nil || got.Name == nil || *got.Name != newName {
		t.Fatalf("source metadata did not replace target: %+v", got)
	}
}

func TestBulkOverwriteObjects_ValidationAndConflicts(t *testing.T) {
	resource, err := clientaccess.ResourcePath("org", "project")
	if err != nil {
		t.Fatal(err)
	}
	sha := "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
	candidate := func(id string) objects.Record {
		return objects.Record{
			Id: objects.RecordID(id), Checksums: []objects.Checksum{{Type: "sha256", Checksum: sha}}, ControlledAccess: &[]string{resource},
		}
	}

	tests := []struct {
		name       string
		candidates []objects.Record
		db         *bulkOverwriteStore
		want       string
		conflict   bool
	}{
		{name: "missing did", db: &bulkOverwriteStore{}, candidates: []objects.Record{candidate(" ")}, want: "did is required"},
		{name: "duplicate source did", db: &bulkOverwriteStore{}, candidates: []objects.Record{candidate("same"), candidate("same")}, want: "duplicate source did", conflict: true},
		{
			name:       "did exists outside project",
			db:         &bulkOverwriteStore{Objects: map[string]*objects.Record{"did": {Id: "did", ControlledAccess: &[]string{"/organization/org/project/other"}}}},
			candidates: []objects.Record{candidate("did")}, want: "outside project", conflict: true,
		},
		{
			name: "ambiguous checksum",
			db: &bulkOverwriteStore{Objects: map[string]*objects.Record{
				"one": {Id: "one", Checksums: []objects.Checksum{{Type: "sha256", Checksum: sha}}, ControlledAccess: &[]string{resource}},
				"two": {Id: "two", Checksums: []objects.Checksum{{Type: "sha256", Checksum: sha}}, ControlledAccess: &[]string{resource}},
			}},
			candidates: []objects.Record{candidate("source")}, want: "multiple records", conflict: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			om := newTestService(tc.db)
			_, err := om.BulkOverwriteObjects(context.Background(), "org", "project", tc.candidates)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("expected error containing %q, got %v", tc.want, err)
			}
			if tc.conflict != errors.Is(err, errorapi.ErrBulkOverwriteConflict) {
				t.Fatalf("conflict classification = %v, want %v", errors.Is(err, errorapi.ErrBulkOverwriteConflict), tc.conflict)
			}
		})
	}
}

func TestBulkOverwriteObjects_NormalizesTargetScope(t *testing.T) {
	resource, err := clientaccess.ResourcePath("org", "project")
	if err != nil {
		t.Fatal(err)
	}
	db := &bulkOverwriteStore{Objects: map[string]*objects.Record{}}
	service := newTestService(db)
	result, err := service.BulkOverwriteObjects(buildGen3Context(map[string]map[string]bool{
		resource: {"create": true},
	}), "org", "project", []objects.Record{{Id: "did"}})
	if err != nil {
		t.Fatalf("BulkOverwriteObjects returned error: %v", err)
	}
	if result.Created != 1 {
		t.Fatalf("result = %+v, want one created record", result)
	}
	stored := db.Objects["did"]
	if stored == nil || !recordInScope(stored, "org", "project") {
		t.Fatalf("target scope was not normalized: %+v", stored)
	}
}

func TestBulkOverwriteObjects_EmptyInput(t *testing.T) {
	db := &bulkOverwriteStore{}
	om := newTestService(db)
	result, err := om.BulkOverwriteObjects(context.Background(), "", "", nil)
	if err != nil || result != (objectrecords.BulkOverwriteResult{}) {
		t.Fatalf("expected empty result, got %+v err=%v", result, err)
	}
}

func TestBulkOverwriteObjects_DoesNotMatchChecksumOutsideProject(t *testing.T) {
	resource, err := clientaccess.ResourcePath("org", "project")
	if err != nil {
		t.Fatal(err)
	}
	sha := "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	db := &bulkOverwriteStore{Objects: map[string]*objects.Record{
		"other-project": {
			Id: "other-project", Checksums: []objects.Checksum{{Type: "sha256", Checksum: sha}}, ControlledAccess: &[]string{"/organization/org/project/other"},
		},
	}}
	om := newTestService(db)
	candidate := objects.Record{
		Id: "source-did", Checksums: []objects.Checksum{{Type: "sha256", Checksum: sha}}, ControlledAccess: &[]string{resource},
	}
	result, err := om.BulkOverwriteObjects(context.Background(), "org", "project", []objects.Record{candidate})
	if err != nil {
		t.Fatalf("BulkOverwriteObjects returned error: %v", err)
	}
	if result.Created != 1 || result.ChecksumMatched != 0 || db.Objects["source-did"] == nil {
		t.Fatalf("checksum from another project must not be matched: %+v", result)
	}
}

func TestBulkOverwriteObjects_RejectsAliasTarget(t *testing.T) {
	resource, err := clientaccess.ResourcePath("org", "project")
	if err != nil {
		t.Fatal(err)
	}
	database := newSQLiteDatabase(t)
	sha := "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"
	originalName := "original"
	canonical := objects.Record{

		Id:               "canonical-did",
		Name:             &originalName,
		Checksums:        []objects.Checksum{{Type: "sha256", Checksum: sha}},
		ControlledAccess: &[]string{resource},
	}
	if err := database.RegisterObjects(context.Background(), []objects.Record{canonical}); err != nil {
		t.Fatalf("RegisterObjects failed: %v", err)
	}
	if err := database.CreateObjectAlias(context.Background(), "alias-did", string(canonical.Id)); err != nil {
		t.Fatalf("CreateObjectAlias failed: %v", err)
	}

	replacementName := "replacement"
	candidate := objects.Record{

		Id:               "alias-did",
		Name:             &replacementName,
		Checksums:        []objects.Checksum{{Type: "sha256", Checksum: sha}},
		ControlledAccess: &[]string{resource},
	}
	om := newTestService(database)
	_, err = om.BulkOverwriteObjects(context.Background(), "org", "project", []objects.Record{candidate})
	if !errors.Is(err, errorapi.ErrBulkOverwriteConflict) || !strings.Contains(err.Error(), "alias") {
		t.Fatalf("expected alias conflict, got %v", err)
	}

	got, err := database.GetObject(context.Background(), string(canonical.Id))
	if err != nil {
		t.Fatalf("GetObject failed: %v", err)
	}
	if got.Name == nil || *got.Name != originalName {
		t.Fatalf("alias overwrite changed canonical record: %+v", got)
	}
}

func TestBulkOverwriteObjects_RequiresTargetProjectPermission(t *testing.T) {
	targetResource, err := clientaccess.ResourcePath("org", "target")
	if err != nil {
		t.Fatal(err)
	}
	allowedResource, err := clientaccess.ResourcePath("org", "allowed")
	if err != nil {
		t.Fatal(err)
	}
	resources := []string{targetResource, allowedResource}
	candidate := objects.Record{

		Id:               "new-did",
		ControlledAccess: &resources,
	}
	t.Run("create", func(t *testing.T) {
		db := &bulkOverwriteStore{}
		om := newTestService(db)
		ctx := buildLocalAuthzContext(map[string]map[string]bool{
			allowedResource: {"create": true},
		})

		_, err := om.BulkOverwriteObjects(ctx, "org", "target", []objects.Record{candidate})
		if !errors.Is(err, errorapi.ErrAccessDenied) {
			t.Fatalf("expected target-project authorization failure, got %v", err)
		}
	})

	t.Run("update", func(t *testing.T) {
		database := &bulkOverwriteStore{Objects: map[string]*objects.Record{}}
		om := newTestService(database)
		ctx := buildLocalAuthzContext(map[string]map[string]bool{
			allowedResource: {"update": true},
		})

		_, err := om.BulkOverwriteObjects(ctx, "org", "target", []objects.Record{candidate})
		if !errors.Is(err, errorapi.ErrAccessDenied) {
			t.Fatalf("expected target-project authorization failure, got %v", err)
		}
	})
}
