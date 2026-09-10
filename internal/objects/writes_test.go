package objects_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/calypr/syfon/apigen/drs"
	"github.com/calypr/syfon/apigen/errorapi"
	clientaccess "github.com/calypr/syfon/client/access"
	"github.com/calypr/syfon/internal/objects"
)

func TestRegisterBulk_InvalidChecksum(t *testing.T) {
	database := newSQLiteDatabase(t)
	om := objects.NewService(database)

	candidates := []drs.DrsObjectCandidate{{
		Aliases: ptr([]string{"id:test-invalid-checksum"}),
		Checksums: []drs.Checksum{{
			Type:     "md5",
			Checksum: "abc",
		}},
		Size: 1,
	}}

	if _, err := om.RegisterCandidates(context.Background(), candidates); err == nil {
		t.Fatalf("expected RegisterBulk error for invalid checksum")
	}
}

func TestRegisterObjects_CanonicalizesProjectChecksumDuplicates(t *testing.T) {
	database := newSQLiteDatabase(t)
	om := objects.NewService(database)
	now := time.Now().UTC()
	later := now.Add(time.Minute)
	accessURL1 := "s3://bucket/original"
	accessURL2 := "s3://bucket/renamed"

	first := drs.DrsObject{

		Id:               "did-1",
		ControlledAccess: &[]string{"/organization/org/project/proj"},
		Name:             ptr("original.tsv"),
		Size:             42,
		CreatedTime:      now,
		UpdatedTime:      &now,
		Checksums:        []drs.Checksum{{Type: "sha256", Checksum: "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"}},
		AccessMethods: &[]drs.AccessMethod{{
			Type:      "s3",
			AccessUrl: &drs.AccessURL{Url: accessURL1},
		}},
	}
	second := drs.DrsObject{

		Id:               "did-2",
		ControlledAccess: &[]string{"/organization/org/project/proj"},
		Name:             ptr("renamed.tsv"),
		Size:             42,
		CreatedTime:      later,
		UpdatedTime:      &later,
		Checksums:        first.Checksums,
		AccessMethods: &[]drs.AccessMethod{{
			Type:      "s3",
			AccessUrl: &drs.AccessURL{Url: accessURL2},
		}},
	}

	if err := om.RegisterObjects(context.Background(), []drs.DrsObject{first}); err != nil {
		t.Fatalf("RegisterObjects(first) error: %v", err)
	}
	if err := om.RegisterObjects(context.Background(), []drs.DrsObject{second}); err != nil {
		t.Fatalf("RegisterObjects(second) error: %v", err)
	}

	byChecksum, err := om.GetObjectsByChecksums(context.Background(), []string{first.Checksums[0].Checksum}, "")
	if err != nil {
		t.Fatalf("GetObjectsByChecksum error: %v", err)
	}
	records := byChecksum[first.Checksums[0].Checksum]
	if len(records) != 1 {
		t.Fatalf("expected 1 canonical record, got %d", len(records))
	}
	if records[0].Id != "did-1" {
		t.Fatalf("expected canonical did-1, got %q", records[0].Id)
	}
	if got := records[0].Name; got == nil || *got != "renamed.tsv" {
		t.Fatalf("expected latest name renamed.tsv, got %+v", got)
	}
	if records[0].NameAliases == nil || !slices.Equal(*records[0].NameAliases, []string{"original.tsv"}) {
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
	database := newSQLiteDatabase(t)
	om := objects.NewService(database)
	sha := "dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd"
	now := time.Date(2026, 9, 4, 16, 0, 0, 0, time.UTC)
	later := now.Add(time.Minute)
	firstResource := "/organization/org/project/first"
	secondResource := "/organization/org/project/second"

	first := drs.DrsObject{

		Id:               "canonical-did",
		Name:             ptr("first.tsv"),
		Size:             42,
		CreatedTime:      now,
		UpdatedTime:      &now,
		Checksums:        []drs.Checksum{{Type: "sha256", Checksum: sha}},
		ControlledAccess: &[]string{firstResource},
		AccessMethods: &[]drs.AccessMethod{{
			Type:      "s3",
			AccessUrl: &drs.AccessURL{Url: "s3://bucket/first"},
		}},
	}
	second := drs.DrsObject{

		Id:               "second-did",
		Name:             ptr("second.tsv"),
		Size:             42,
		CreatedTime:      later,
		UpdatedTime:      &later,
		Checksums:        []drs.Checksum{{Type: "sha256", Checksum: sha}},
		ControlledAccess: &[]string{secondResource},
		AccessMethods: &[]drs.AccessMethod{{
			Type:      "s3",
			AccessUrl: &drs.AccessURL{Url: "s3://bucket/second"},
		}},
	}

	if err := om.RegisterObjects(context.Background(), []drs.DrsObject{first}); err != nil {
		t.Fatalf("RegisterObjects(first) error: %v", err)
	}
	if err := om.RegisterObjects(context.Background(), []drs.DrsObject{second}); err != nil {
		t.Fatalf("RegisterObjects(second) error: %v", err)
	}

	physicalByChecksum, err := database.GetObjectsByChecksums(context.Background(), []string{sha})
	if err != nil {
		t.Fatalf("database.GetObjectsByChecksum error: %v", err)
	}
	physicalRecords := physicalByChecksum[sha]
	if len(physicalRecords) != 1 {
		t.Fatalf("expected one physical content record, got %d", len(physicalRecords))
	}

	canonicalByChecksum, err := om.GetObjectsByChecksums(context.Background(), []string{sha}, "")
	if err != nil {
		t.Fatalf("GetObjectsByChecksum error: %v", err)
	}
	byChecksum := canonicalByChecksum[sha]
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

func TestRegisterCandidatesReturnsMaterializedRecordsInRequestOrder(t *testing.T) {
	store := &objectTestStore{Objects: make(map[string]*drs.DrsObject)}
	service := objects.NewService(store)

	registered, err := service.RegisterCandidates(context.Background(), []drs.DrsObjectCandidate{
		drsCandidate("first", "first.tsv", "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"),
		drsCandidate("second", "second.tsv", "ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"),
	})
	if err != nil {
		t.Fatalf("RegisterCandidates() error = %v", err)
	}
	if len(registered) != 2 || registered[0].Name == nil || *registered[0].Name != "first.tsv" || registered[1].Name == nil || *registered[1].Name != "second.tsv" {
		t.Fatalf("registered records = %#v", registered)
	}
	for i, record := range registered {
		if record.CreatedTime.IsZero() || record.UpdatedTime == nil {
			t.Fatalf("record %d was not materialized: %#v", i, record)
		}
	}
}

func TestUpdateAccessMethodsAndReadReturnsUpdatedRecord(t *testing.T) {
	store := &objectTestStore{Objects: map[string]*drs.DrsObject{"one": {Id: "one"}}}
	service := objects.NewService(store)

	updated, err := service.UpdateAccessMethodsAndRead(context.Background(), "one", []drs.AccessMethod{drsAccessMethod("s3")})
	if err != nil {
		t.Fatalf("UpdateAccessMethodsAndRead() error = %v", err)
	}
	if updated == nil || updated.AccessMethods == nil || len(*updated.AccessMethods) != 1 || (*updated.AccessMethods)[0].Type != "s3" {
		t.Fatalf("updated durable record = %#v", updated)
	}
}

func TestBulkUpdateAccessMethodsAndReadPreservesOrderAndLastDuplicate(t *testing.T) {
	store := &objectTestStore{Objects: map[string]*drs.DrsObject{
		"one": {Id: "one"},
		"two": {Id: "two"},
	}}
	service := objects.NewService(store)

	updated, err := service.BulkUpdateAccessMethodsAndRead(context.Background(), []drs.AccessMethodUpdate{
		{ObjectId: "one", AccessMethods: []drs.AccessMethod{drsAccessMethod("first")}},
		{ObjectId: "two", AccessMethods: []drs.AccessMethod{drsAccessMethod("second")}},
		{ObjectId: "one", AccessMethods: []drs.AccessMethod{drsAccessMethod("last")}},
	})
	if err != nil {
		t.Fatalf("BulkUpdateAccessMethodsAndRead() error = %v", err)
	}
	if len(updated) != 2 || updated[0].Id != "one" || updated[1].Id != "two" {
		t.Fatalf("bulk response order = %#v", updated)
	}
	if updated[0].AccessMethods == nil || (*updated[0].AccessMethods)[0].Type != "last" {
		t.Fatalf("duplicate update did not use last value: %#v", updated[0].AccessMethods)
	}
}

func drsCandidate(id, name, checksum string) drs.DrsObjectCandidate {
	return drs.DrsObjectCandidate{
		Aliases:   ptr([]string{"id:" + id}),
		Name:      ptr(name),
		Checksums: []drs.Checksum{{Type: "sha256", Checksum: checksum}},
		AccessMethods: ptr([]drs.AccessMethod{{
			Type:      "s3",
			AccessUrl: &drs.AccessURL{Url: "s3://bucket/" + id},
		}}),
	}
}

func drsAccessMethod(kind string) drs.AccessMethod {
	return drs.AccessMethod{Type: drs.AccessMethodType(kind), AccessUrl: &drs.AccessURL{Url: "s3://bucket/" + kind}}
}

func TestBulkOverwriteObjects_ReplacesProjectChecksumSibling(t *testing.T) {
	resource, err := clientaccess.ResourcePath("org", "project")
	if err != nil {
		t.Fatal(err)
	}
	sha := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	oldName := "old"
	newName := "new"
	db := &objectTestStore{Objects: map[string]*drs.DrsObject{
		"target-did": {
			Id: "target-did", Name: &oldName, Checksums: []drs.Checksum{{Type: "sha256", Checksum: sha}}, ControlledAccess: &[]string{resource},
		},
	}}
	om := objects.NewService(db)
	candidate := drs.DrsObject{

		Id:               "source-did",
		Name:             &newName,
		Checksums:        []drs.Checksum{{Type: "sha256", Checksum: sha}},
		ControlledAccess: &[]string{resource},
	}
	result, err := om.BulkOverwriteObjects(buildGen3Context(map[string]map[string]bool{resource: {"update": true}}), "org", "project", []drs.DrsObject{candidate})
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
	candidate := func(id string) drs.DrsObject {
		return drs.DrsObject{
			Id: id, Checksums: []drs.Checksum{{Type: "sha256", Checksum: sha}}, ControlledAccess: &[]string{resource},
		}
	}

	tests := []struct {
		name       string
		candidates []drs.DrsObject
		db         *objectTestStore
		want       string
		conflict   bool
	}{
		{name: "missing did", db: &objectTestStore{}, candidates: []drs.DrsObject{candidate(" ")}, want: "did is required"},
		{name: "duplicate source did", db: &objectTestStore{}, candidates: []drs.DrsObject{candidate("same"), candidate("same")}, want: "duplicate source did", conflict: true},
		{
			name:       "did exists outside project",
			db:         &objectTestStore{Objects: map[string]*drs.DrsObject{"did": {Id: "did", ControlledAccess: &[]string{"/organization/org/project/other"}}}},
			candidates: []drs.DrsObject{candidate("did")}, want: "outside project", conflict: true,
		},
		{
			name: "ambiguous checksum",
			db: &objectTestStore{Objects: map[string]*drs.DrsObject{
				"one": {Id: "one", Checksums: []drs.Checksum{{Type: "sha256", Checksum: sha}}, ControlledAccess: &[]string{resource}},
				"two": {Id: "two", Checksums: []drs.Checksum{{Type: "sha256", Checksum: sha}}, ControlledAccess: &[]string{resource}},
			}},
			candidates: []drs.DrsObject{candidate("source")}, want: "multiple records", conflict: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			om := objects.NewService(tc.db)
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

func TestBulkOverwriteObjects_DoesNotMatchChecksumOutsideProject(t *testing.T) {
	resource, err := clientaccess.ResourcePath("org", "project")
	if err != nil {
		t.Fatal(err)
	}
	sha := "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	db := &objectTestStore{Objects: map[string]*drs.DrsObject{
		"other-project": {
			Id: "other-project", Checksums: []drs.Checksum{{Type: "sha256", Checksum: sha}}, ControlledAccess: &[]string{"/organization/org/project/other"},
		},
	}}
	om := objects.NewService(db)
	candidate := drs.DrsObject{
		Id: "source-did", Checksums: []drs.Checksum{{Type: "sha256", Checksum: sha}}, ControlledAccess: &[]string{resource},
	}
	result, err := om.BulkOverwriteObjects(context.Background(), "org", "project", []drs.DrsObject{candidate})
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
	canonical := drs.DrsObject{

		Id:               "canonical-did",
		Name:             &originalName,
		Checksums:        []drs.Checksum{{Type: "sha256", Checksum: sha}},
		ControlledAccess: &[]string{resource},
	}
	if err := database.RegisterObjects(context.Background(), []drs.DrsObject{canonical}); err != nil {
		t.Fatalf("RegisterObjects failed: %v", err)
	}
	if err := database.CreateObjectAlias(context.Background(), "alias-did", canonical.Id); err != nil {
		t.Fatalf("CreateObjectAlias failed: %v", err)
	}

	replacementName := "replacement"
	candidate := drs.DrsObject{

		Id:               "alias-did",
		Name:             &replacementName,
		Checksums:        []drs.Checksum{{Type: "sha256", Checksum: sha}},
		ControlledAccess: &[]string{resource},
	}
	om := objects.NewService(database)
	_, err = om.BulkOverwriteObjects(context.Background(), "org", "project", []drs.DrsObject{candidate})
	if !errors.Is(err, errorapi.ErrBulkOverwriteConflict) || !strings.Contains(err.Error(), "alias") {
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
	targetResource, err := clientaccess.ResourcePath("org", "target")
	if err != nil {
		t.Fatal(err)
	}
	allowedResource, err := clientaccess.ResourcePath("org", "allowed")
	if err != nil {
		t.Fatal(err)
	}
	resources := []string{targetResource, allowedResource}
	candidate := drs.DrsObject{

		Id:               "new-did",
		ControlledAccess: &resources,
	}
	t.Run("create", func(t *testing.T) {
		db := &objectTestStore{}
		om := objects.NewService(db)
		ctx := buildLocalAuthzContext(map[string]map[string]bool{
			allowedResource: {"create": true},
		})

		_, err := om.BulkOverwriteObjects(ctx, "org", "target", []drs.DrsObject{candidate})
		if !errors.Is(err, errorapi.ErrAccessDenied) {
			t.Fatalf("expected target-project authorization failure, got %v", err)
		}
	})

	t.Run("update", func(t *testing.T) {
		database := &objectTestStore{Objects: map[string]*drs.DrsObject{}}
		om := objects.NewService(database)
		ctx := buildLocalAuthzContext(map[string]map[string]bool{
			allowedResource: {"update": true},
		})

		_, err := om.BulkOverwriteObjects(ctx, "org", "target", []drs.DrsObject{candidate})
		if !errors.Is(err, errorapi.ErrAccessDenied) {
			t.Fatalf("expected target-project authorization failure, got %v", err)
		}
	})
}
