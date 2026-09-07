package objects_test

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/calypr/syfon/internal/objects"
)

func TestRegisterBulk_RegistersCandidate(t *testing.T) {
	database := newSQLiteDatabase(t)
	om := newTestService(database, nil)

	candidates := []objects.Candidate{
		{
			Aliases: ptr([]string{"id:test-register-bulk"}),
			Checksums: &[]objects.Checksum{{
				Type:     "sha256",
				Checksum: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			}},
			AccessMethods: &[]objects.AccessMethod{{
				Type:      "s3",
				AccessUrl: &objects.AccessURL{Url: "s3://bucket/test-register-bulk"},
			}},
			Size: ptr(int64(1)),
		},
	}

	count, err := registerCandidates(context.Background(), om, candidates)
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
	database := newSQLiteDatabase(t)
	om := newTestService(database, nil)

	candidates := []objects.Candidate{{
		Aliases: ptr([]string{"id:test-invalid-checksum"}),
		Checksums: &[]objects.Checksum{{
			Type:     "md5",
			Checksum: "abc",
		}},
		Size: ptr(int64(1)),
	}}

	if _, err := registerCandidates(context.Background(), om, candidates); err == nil {
		t.Fatalf("expected RegisterBulk error for invalid checksum")
	}
}

func TestBulkDeleteObjects_DeletesAuthorizedObjects(t *testing.T) {
	database := newSQLiteDatabase(t)
	om := newTestService(database, nil)

	_, err := registerCandidates(context.Background(), om, []objects.Candidate{{
		Aliases: ptr([]string{"id:test-delete-bulk"}),
		Checksums: &[]objects.Checksum{{
			Type:     "sha256",
			Checksum: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		}},
		AccessMethods: &[]objects.AccessMethod{{
			Type:      "s3",
			AccessUrl: &objects.AccessURL{Url: "s3://bucket/test-delete-bulk"},
		}},
		Size: ptr(int64(1)),
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

func TestRegisterObjects_CanonicalizesProjectChecksumDuplicates(t *testing.T) {
	database := newSQLiteDatabase(t)
	om := newTestService(database, nil)
	now := time.Now().UTC()
	later := now.Add(time.Minute)
	accessURL1 := "s3://bucket/original"
	accessURL2 := "s3://bucket/renamed"

	first := objects.Record{
		Authorizations: map[string][]string{"org": {"proj"}},

		Id:          "did-1",
		Name:        ptr("original.tsv"),
		Size:        42,
		CreatedTime: now,
		UpdatedTime: &now,
		Checksums:   []objects.Checksum{{Type: "sha256", Checksum: "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"}},
		AccessMethods: &[]objects.AccessMethod{{
			Type:      "s3",
			AccessUrl: &objects.AccessURL{Url: accessURL1},
		}},
	}
	second := objects.Record{
		Authorizations: map[string][]string{"org": {"proj"}},

		Id:          "did-2",
		Name:        ptr("renamed.tsv"),
		Size:        42,
		CreatedTime: later,
		UpdatedTime: &later,
		Checksums:   first.Checksums,
		AccessMethods: &[]objects.AccessMethod{{
			Type:      "s3",
			AccessUrl: &objects.AccessURL{Url: accessURL2},
		}},
	}

	if err := om.RegisterObjects(context.Background(), []objects.Record{first}); err != nil {
		t.Fatalf("RegisterObjects(first) error: %v", err)
	}
	if err := om.RegisterObjects(context.Background(), []objects.Record{second}); err != nil {
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
	database := newSQLiteDatabase(t)
	om := newTestService(database, nil)
	sha := "dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd"
	now := time.Date(2026, 9, 4, 16, 0, 0, 0, time.UTC)
	later := now.Add(time.Minute)
	firstResource := "/organization/org/project/first"
	secondResource := "/organization/org/project/second"

	first := objects.Record{
		Authorizations: map[string][]string{"org": {"first"}},

		Id:               "canonical-did",
		Name:             ptr("first.tsv"),
		Size:             42,
		CreatedTime:      now,
		UpdatedTime:      &now,
		Checksums:        []objects.Checksum{{Type: "sha256", Checksum: sha}},
		ControlledAccess: &[]string{firstResource},
		AccessMethods: &[]objects.AccessMethod{{
			Type:      "s3",
			AccessUrl: &objects.AccessURL{Url: "s3://bucket/first"},
		}},
	}
	second := objects.Record{
		Authorizations: map[string][]string{"org": {"second"}},

		Id:               "second-did",
		Name:             ptr("second.tsv"),
		Size:             42,
		CreatedTime:      later,
		UpdatedTime:      &later,
		Checksums:        []objects.Checksum{{Type: "sha256", Checksum: sha}},
		ControlledAccess: &[]string{secondResource},
		AccessMethods: &[]objects.AccessMethod{{
			Type:      "s3",
			AccessUrl: &objects.AccessURL{Url: "s3://bucket/second"},
		}},
	}

	if err := om.RegisterObjects(context.Background(), []objects.Record{first}); err != nil {
		t.Fatalf("RegisterObjects(first) error: %v", err)
	}
	if err := om.RegisterObjects(context.Background(), []objects.Record{second}); err != nil {
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
	if string(canonical.Id) != string(first.Id) {
		t.Fatalf("expected deterministic read representative %q, got %q", string(first.Id), string(canonical.Id))
	}
	if canonical.AccessMethods == nil || len(*canonical.AccessMethods) != 2 {
		t.Fatalf("expected both access methods, got %+v", canonical.AccessMethods)
	}
	if canonical.ControlledAccess == nil || len(*canonical.ControlledAccess) != 2 || !slices.Contains(*canonical.ControlledAccess, firstResource) || !slices.Contains(*canonical.ControlledAccess, secondResource) {
		t.Fatalf("expected both controlled-access resources, got %+v", canonical.ControlledAccess)
	}

	for _, ident := range []string{string(first.Id), string(second.Id)} {
		got, err := om.GetObject(context.Background(), ident, "")
		if err != nil {
			t.Fatalf("GetObject(%q) error: %v", ident, err)
		}
		if got.Id != canonical.Id {
			t.Fatalf("GetObject(%q) returned id %q, want canonical %q", ident, got.Id, string(canonical.Id))
		}
		if got.AccessMethods == nil || len(*got.AccessMethods) != 2 {
			t.Fatalf("GetObject(%q) lost merged access methods: %+v", ident, got.AccessMethods)
		}
	}
}
