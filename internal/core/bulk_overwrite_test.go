package core

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/calypr/syfon/apigen/server/drs"
	sycommon "github.com/calypr/syfon/common"
	"github.com/calypr/syfon/internal/db/sqlite"
	"github.com/calypr/syfon/internal/faults"
	"github.com/calypr/syfon/internal/models"
	"github.com/calypr/syfon/internal/testutils"
)

func TestBulkOverwriteObjects_ReplacesProjectChecksumSibling(t *testing.T) {
	resource, err := sycommon.ResourcePath("org", "project")
	if err != nil {
		t.Fatal(err)
	}
	sha := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	oldName := "old"
	newName := "new"
	db := &coreTestDB{MockDatabase: &testutils.MockDatabase{
		Objects: map[string]*drs.DrsObject{
			"target-did": {Id: "target-did", Name: &oldName, Checksums: []drs.Checksum{{Type: "sha256", Checksum: sha}}},
		},
		ObjectAuthz: map[string]map[string][]string{"target-did": {"org": {"project"}}},
	}}
	om := NewObjectManager(db, &capturingURLManager{})
	candidate := models.InternalObject{
		DrsObject: drs.DrsObject{
			Id:               "source-did",
			Name:             &newName,
			Checksums:        []drs.Checksum{{Type: "sha256", Checksum: sha}},
			ControlledAccess: &[]string{resource},
		},
		Authorizations: map[string][]string{"org": {"project"}},
	}
	result, err := om.BulkOverwriteObjects(buildGen3Context(map[string]map[string]bool{resource: {"update": true}}), "org", "project", []models.InternalObject{candidate})
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
	resource, err := sycommon.ResourcePath("org", "project")
	if err != nil {
		t.Fatal(err)
	}
	sha := "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
	candidate := func(id string) models.InternalObject {
		return models.InternalObject{
			DrsObject:      drs.DrsObject{Id: id, Checksums: []drs.Checksum{{Type: "sha256", Checksum: sha}}, ControlledAccess: &[]string{resource}},
			Authorizations: map[string][]string{"org": {"project"}},
		}
	}

	tests := []struct {
		name       string
		db         *testutils.MockDatabase
		candidates []models.InternalObject
		want       string
		conflict   bool
	}{
		{name: "missing did", db: &testutils.MockDatabase{}, candidates: []models.InternalObject{candidate(" ")}, want: "did is required"},
		{name: "duplicate source did", db: &testutils.MockDatabase{}, candidates: []models.InternalObject{candidate("same"), candidate("same")}, want: "duplicate source did", conflict: true},
		{name: "missing target scope", db: &testutils.MockDatabase{}, candidates: []models.InternalObject{{DrsObject: drs.DrsObject{Id: "did"}}}, want: "must include target project"},
		{
			name: "did exists outside project",
			db: &testutils.MockDatabase{
				Objects:     map[string]*drs.DrsObject{"did": {Id: "did"}},
				ObjectAuthz: map[string]map[string][]string{"did": {"org": {"other"}}},
			},
			candidates: []models.InternalObject{candidate("did")}, want: "outside project", conflict: true,
		},
		{
			name: "ambiguous checksum",
			db: &testutils.MockDatabase{
				Objects: map[string]*drs.DrsObject{
					"one": {Id: "one", Checksums: []drs.Checksum{{Type: "sha256", Checksum: sha}}},
					"two": {Id: "two", Checksums: []drs.Checksum{{Type: "sha256", Checksum: sha}}},
				},
				ObjectAuthz: map[string]map[string][]string{"one": {"org": {"project"}}, "two": {"org": {"project"}}},
			},
			candidates: []models.InternalObject{candidate("source")}, want: "multiple records", conflict: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			om := NewObjectManager(&coreTestDB{MockDatabase: tc.db}, &capturingURLManager{})
			_, err := om.BulkOverwriteObjects(context.Background(), "org", "project", tc.candidates)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("expected error containing %q, got %v", tc.want, err)
			}
			if tc.conflict != errors.Is(err, ErrBulkOverwriteConflict) {
				t.Fatalf("conflict classification = %v, want %v", errors.Is(err, ErrBulkOverwriteConflict), tc.conflict)
			}
		})
	}
}

func TestBulkOverwriteObjects_EmptyInput(t *testing.T) {
	om := NewObjectManager(&coreTestDB{MockDatabase: &testutils.MockDatabase{}}, &capturingURLManager{})
	result, err := om.BulkOverwriteObjects(context.Background(), "", "", nil)
	if err != nil || result != (BulkOverwriteResult{}) {
		t.Fatalf("expected empty result, got %+v err=%v", result, err)
	}
}

func TestBulkOverwriteObjects_DoesNotMatchChecksumOutsideProject(t *testing.T) {
	resource, err := sycommon.ResourcePath("org", "project")
	if err != nil {
		t.Fatal(err)
	}
	sha := "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	db := &coreTestDB{MockDatabase: &testutils.MockDatabase{
		Objects:     map[string]*drs.DrsObject{"other-project": {Id: "other-project", Checksums: []drs.Checksum{{Type: "sha256", Checksum: sha}}}},
		ObjectAuthz: map[string]map[string][]string{"other-project": {"org": {"other"}}},
	}}
	om := NewObjectManager(db, &capturingURLManager{})
	candidate := models.InternalObject{
		DrsObject:      drs.DrsObject{Id: "source-did", Checksums: []drs.Checksum{{Type: "sha256", Checksum: sha}}, ControlledAccess: &[]string{resource}},
		Authorizations: map[string][]string{"org": {"project"}},
	}
	result, err := om.BulkOverwriteObjects(context.Background(), "org", "project", []models.InternalObject{candidate})
	if err != nil {
		t.Fatalf("BulkOverwriteObjects returned error: %v", err)
	}
	if result.Created != 1 || result.ChecksumMatched != 0 || db.Objects["source-did"] == nil {
		t.Fatalf("checksum from another project must not be matched: %+v", result)
	}
}

func TestBulkOverwriteObjects_RejectsAliasTarget(t *testing.T) {
	resource, err := sycommon.ResourcePath("org", "project")
	if err != nil {
		t.Fatal(err)
	}
	database, err := sqlite.NewSqliteDB(":memory:")
	if err != nil {
		t.Fatalf("NewSqliteDB failed: %v", err)
	}
	sha := "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"
	originalName := "original"
	canonical := models.InternalObject{
		Authorizations: map[string][]string{"org": {"project"}},
		DrsObject: drs.DrsObject{
			Id:               "canonical-did",
			Name:             &originalName,
			Checksums:        []drs.Checksum{{Type: "sha256", Checksum: sha}},
			ControlledAccess: &[]string{resource},
		},
	}
	if err := database.CreateObject(context.Background(), &canonical); err != nil {
		t.Fatalf("CreateObject failed: %v", err)
	}
	if err := database.CreateObjectAlias(context.Background(), "alias-did", canonical.Id); err != nil {
		t.Fatalf("CreateObjectAlias failed: %v", err)
	}

	replacementName := "replacement"
	candidate := models.InternalObject{
		Authorizations: map[string][]string{"org": {"project"}},
		DrsObject: drs.DrsObject{
			Id:               "alias-did",
			Name:             &replacementName,
			Checksums:        []drs.Checksum{{Type: "sha256", Checksum: sha}},
			ControlledAccess: &[]string{resource},
		},
	}
	om := NewObjectManager(database, nil)
	_, err = om.BulkOverwriteObjects(context.Background(), "org", "project", []models.InternalObject{candidate})
	if !errors.Is(err, ErrBulkOverwriteConflict) || !strings.Contains(err.Error(), "alias") {
		t.Fatalf("expected alias conflict, got %v", err)
	}

	got, err := database.GetObject(context.Background(), canonical.Id)
	if err != nil {
		t.Fatalf("GetObject failed: %v", err)
	}
	if got.Name == nil || *got.Name != originalName {
		t.Fatalf("alias overwrite changed canonical record: %+v", got)
	}
}

func TestBulkOverwriteObjects_RequiresTargetProjectPermission(t *testing.T) {
	targetResource, err := sycommon.ResourcePath("org", "target")
	if err != nil {
		t.Fatal(err)
	}
	allowedResource, err := sycommon.ResourcePath("org", "allowed")
	if err != nil {
		t.Fatal(err)
	}
	resources := []string{targetResource, allowedResource}
	candidate := models.InternalObject{
		Authorizations: map[string][]string{"org": {"target", "allowed"}},
		DrsObject: drs.DrsObject{
			Id:               "new-did",
			ControlledAccess: &resources,
		},
	}
	t.Run("create", func(t *testing.T) {
		om := NewObjectManager(&coreTestDB{MockDatabase: &testutils.MockDatabase{}}, nil)
		ctx := buildLocalAuthzContext(map[string]map[string]bool{
			allowedResource: {"create": true},
		})

		_, err := om.BulkOverwriteObjects(ctx, "org", "target", []models.InternalObject{candidate})
		if !errors.Is(err, faults.ErrUnauthorized) {
			t.Fatalf("expected target-project authorization failure, got %v", err)
		}
	})

	t.Run("update", func(t *testing.T) {
		database := &coreTestDB{MockDatabase: &testutils.MockDatabase{
			Objects: map[string]*drs.DrsObject{
				candidate.Id: {Id: candidate.Id},
			},
			ObjectAuthz: map[string]map[string][]string{
				candidate.Id: {"org": {"target", "allowed"}},
			},
		}}
		om := NewObjectManager(database, nil)
		ctx := buildLocalAuthzContext(map[string]map[string]bool{
			allowedResource: {"update": true},
		})

		_, err := om.BulkOverwriteObjects(ctx, "org", "target", []models.InternalObject{candidate})
		if !errors.Is(err, faults.ErrUnauthorized) {
			t.Fatalf("expected target-project authorization failure, got %v", err)
		}
	})
}
