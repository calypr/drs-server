package objects_test

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/calypr/syfon/internal/objects"
	"github.com/calypr/syfon/internal/persistence/sqlite"
)

type pageSpyDB struct {
	*sqlite.SqliteDB
	pageCalls int
	listCalls int
}

func (s *pageSpyDB) ListObjectIDsPageByScope(ctx context.Context, organization, project, startAfter string, limit, offset int) ([]string, error) {
	s.pageCalls++
	return s.SqliteDB.ListObjectIDsPageByScope(ctx, organization, project, startAfter, limit, offset)
}

func (s *pageSpyDB) ListObjectIDsPageByResources(ctx context.Context, resources []string, includeUnscoped bool, startAfter string, limit, offset int) ([]string, error) {
	return s.SqliteDB.ListObjectIDsPageByResources(ctx, resources, includeUnscoped, startAfter, limit, offset)
}

func (s *pageSpyDB) ListObjectIDsByScope(ctx context.Context, organization, project string) ([]string, error) {
	s.listCalls++
	return s.SqliteDB.ListObjectIDsByScope(ctx, organization, project)
}

func registerScopedCandidate(t *testing.T, om *objects.Service, id, checksum, org, project string) {
	t.Helper()
	controlled := []string{"/organization/" + org + "/project/" + project}
	_, err := registerCandidates(context.Background(), om, []objects.Candidate{{
		Aliases:          ptr([]string{"id:" + id}),
		ControlledAccess: &controlled,
		Checksums: &[]objects.Checksum{{
			Type:     "sha256",
			Checksum: checksum,
		}},
		AccessMethods: &[]objects.AccessMethod{{
			Type:      "s3",
			AccessUrl: &objects.AccessURL{Url: "s3://bucket/" + id},
		}},
		Size: ptr(int64(1)),
	}})
	if err != nil {
		t.Fatalf("RegisterBulk(%s): %v", id, err)
	}
}

func TestGetObjectUsesGlobalSHAIdentityAcrossUUIDs(t *testing.T) {
	database := newSQLiteDatabase(t)
	checksum := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	firstResource := "/organization/org1/project/project1"
	secondResource := "/organization/org2/project/project2"
	created := drsISOTime("2026-01-01T00:00:00Z")
	updated := ptrTime("2026-01-01T00:00:00Z")

	for _, obj := range []objects.Record{
		{
			Authorizations: map[string][]string{"org1": {"project1"}},

			Id:               "uuid-a",
			CreatedTime:      created,
			UpdatedTime:      updated,
			Checksums:        []objects.Checksum{{Type: "sha256", Checksum: checksum}},
			ControlledAccess: &[]string{firstResource},
			AccessMethods: &[]objects.AccessMethod{{
				Type:      "s3",
				AccessUrl: &objects.AccessURL{Url: "s3://bucket/uuid-a"},
			}},
		},
		{
			Authorizations: map[string][]string{"org2": {"project2"}},

			Id:               "uuid-b",
			CreatedTime:      created,
			UpdatedTime:      updated,
			Checksums:        []objects.Checksum{{Type: "sha256", Checksum: checksum}},
			ControlledAccess: &[]string{secondResource},
			AccessMethods: &[]objects.AccessMethod{{
				Type:      "s3",
				AccessUrl: &objects.AccessURL{Url: "s3://bucket/uuid-b"},
			}},
		},
	} {
		if err := database.CreateObject(context.Background(), &obj); err != nil {
			t.Fatalf("CreateObject(%s) failed: %v", obj.Id, err)
		}
	}

	om := newTestService(database, nil)
	ctx := buildLocalAuthzContext(map[string]map[string]bool{
		firstResource: {"read": true},
	})
	byFirstUUID, err := om.GetObject(ctx, "uuid-a", "read")
	if err != nil {
		t.Fatalf("GetObject(uuid-a) failed: %v", err)
	}
	bySecondUUID, err := om.GetObject(ctx, "uuid-b", "read")
	if err != nil {
		t.Fatalf("GetObject(uuid-b) failed: %v", err)
	}
	byChecksum, err := om.GetObject(ctx, checksum, "read")
	if err != nil {
		t.Fatalf("GetObject(checksum) failed: %v", err)
	}
	canonical, err := om.GetCanonicalContent(ctx, "uuid-a", "read")
	if err != nil {
		t.Fatalf("GetCanonicalContent(uuid-a) failed: %v", err)
	}
	if canonical.ContentID != objects.ContentID(checksum) || canonical.Record.Id != "uuid-a" || len(canonical.Records) != 1 {
		t.Fatalf("canonical content view lost identity distinction: %+v", canonical)
	}

	for lookup, got := range map[string]*objects.Record{
		"uuid-a":   byFirstUUID,
		"uuid-b":   bySecondUUID,
		"checksum": byChecksum,
	} {
		if got.Id != "uuid-a" {
			t.Errorf("%s resolved id = %q, want uuid-a", lookup, got.Id)
		}
		if got.AccessMethods == nil || len(*got.AccessMethods) != 2 {
			t.Errorf("%s access methods = %+v, want both locations", lookup, got.AccessMethods)
		}
		if got.ControlledAccess == nil || len(*got.ControlledAccess) != 2 || !slices.Contains(*got.ControlledAccess, firstResource) || !slices.Contains(*got.ControlledAccess, secondResource) {
			t.Errorf("%s controlled access = %+v, want both resources", lookup, got.ControlledAccess)
		}
	}
}

func TestGetObjectKeepsCanonicalContentPublicWhenAnySiblingIsPublic(t *testing.T) {
	database := newSQLiteDatabase(t)
	checksum := "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	controlledResource := "/organization/org/project/controlled"
	created := drsISOTime("2026-01-01T00:00:00Z")
	for _, obj := range []objects.Record{
		{

			Id:          "public-uuid",
			CreatedTime: created,
			Checksums:   []objects.Checksum{{Type: "sha256", Checksum: checksum}},
			AccessMethods: &[]objects.AccessMethod{{
				Type:      "s3",
				AccessUrl: &objects.AccessURL{Url: "s3://bucket/public"},
			}},
		},
		{
			Authorizations: map[string][]string{"org": {"controlled"}},

			Id:               "controlled-uuid",
			CreatedTime:      created,
			Checksums:        []objects.Checksum{{Type: "sha256", Checksum: checksum}},
			ControlledAccess: &[]string{controlledResource},
			AccessMethods: &[]objects.AccessMethod{{
				Type:      "s3",
				AccessUrl: &objects.AccessURL{Url: "s3://bucket/controlled"},
			}},
		},
	} {
		if err := database.CreateObject(context.Background(), &obj); err != nil {
			t.Fatalf("CreateObject(%s) failed: %v", obj.Id, err)
		}
	}

	om := newTestService(database, nil)
	got, err := om.GetObject(buildLocalAuthzContext(nil), "controlled-uuid", "read")
	if err != nil {
		t.Fatalf("public checksum family should be readable: %v", err)
	}
	if got.AccessMethods == nil || len(*got.AccessMethods) != 2 {
		t.Fatalf("expected both public and controlled locations, got %+v", got.AccessMethods)
	}
}

func TestGetObjectPrefersSHAIdentityOverCollidingPhysicalID(t *testing.T) {
	database := newSQLiteDatabase(t)
	requestedSHA := "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
	otherSHA := "dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd"
	for _, obj := range []objects.Record{
		{Id: objects.RecordID(requestedSHA), Checksums: []objects.Checksum{{Type: "sha256", Checksum: otherSHA}}},
		{Id: "checksum-record", Checksums: []objects.Checksum{{Type: "sha256", Checksum: requestedSHA}}},
	} {
		if err := database.CreateObject(context.Background(), &obj); err != nil {
			t.Fatalf("CreateObject(%s) failed: %v", obj.Id, err)
		}
	}

	got, err := newTestService(database, nil).GetObject(context.Background(), requestedSHA, "")
	if err != nil {
		t.Fatalf("GetObject failed: %v", err)
	}
	if got.Id != "checksum-record" {
		t.Fatalf("checksum lookup returned physical ID collision %q", got.Id)
	}
}

func TestGetBulkObjectsUsesGlobalSHAIdentity(t *testing.T) {
	database := &readObjectStore{}
	checksum := "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"
	firstResource := "/organization/org/project/first"
	secondResource := "/organization/org/project/second"
	created := drsISOTime("2026-01-01T00:00:00Z")
	for _, obj := range []objects.Record{
		{Authorizations: map[string][]string{"org": {"first"}}, Id: "bulk-a", CreatedTime: created, Checksums: []objects.Checksum{{Type: "sha256", Checksum: checksum}}, ControlledAccess: &[]string{firstResource}},
		{Authorizations: map[string][]string{"org": {"second"}}, Id: "bulk-b", CreatedTime: created, Checksums: []objects.Checksum{{Type: "sha256", Checksum: checksum}}, ControlledAccess: &[]string{secondResource}},
	} {
		if err := database.CreateObject(context.Background(), &obj); err != nil {
			t.Fatalf("CreateObject(%s) failed: %v", obj.Id, err)
		}
	}

	ctx := buildLocalAuthzContext(map[string]map[string]bool{firstResource: {"read": true}})
	service := objects.NewService(objects.Dependencies{Reader: database, Content: database})
	got, err := service.GetBulkObjects(ctx, []string{"bulk-b"}, "read")
	if err != nil {
		t.Fatalf("GetBulkObjects failed: %v", err)
	}
	if len(got) != 1 || got[0].Id != "bulk-a" || got[0].ControlledAccess == nil || len(*got[0].ControlledAccess) != 2 {
		t.Fatalf("bulk read did not return the merged checksum identity: %+v", got)
	}
	view, err := service.GetCanonicalContent(ctx, checksum, "read")
	if err != nil {
		t.Fatalf("GetCanonicalContent(checksum) failed: %v", err)
	}
	if len(view.Records) != 2 {
		t.Fatalf("checksum lookup lost physical siblings: got %d records", len(view.Records))
	}
}

func TestListObjectIDsPageByChecksum_ReturnsCanonicalContentID(t *testing.T) {
	database := newSQLiteDatabase(t)
	om := newTestService(database, nil)
	checksum := "1111111111111111111111111111111111111111111111111111111111111111"

	registerScopedCandidate(t, om, "chk-a", checksum, "org1", "proj1")
	registerScopedCandidate(t, om, "chk-b", checksum, "org1", "proj2")
	registerScopedCandidate(t, om, "chk-c", checksum, "org2", "proj1")

	ids, err := om.ListObjectIDsPageByChecksum(context.Background(), checksum, "sha256", "", "", "read", "", 2, 0)
	if err != nil {
		t.Fatalf("ListObjectIDsPageByChecksum error: %v", err)
	}
	if len(ids) != 1 || ids[0] != "chk-a" {
		t.Fatalf("unexpected page ids: %+v", ids)
	}
	ids, err = om.ListObjectIDsPageByChecksum(context.Background(), checksum, "sha256", "", "", "read", "chk-a", 2, 0)
	if err != nil || len(ids) != 0 {
		t.Fatalf("aliases appeared after the canonical pagination cursor: ids=%v err=%v", ids, err)
	}
}

func TestListObjectIDsPageByScope_StartAfterAndScopeFilter(t *testing.T) {
	database := newSQLiteDatabase(t)
	om := newTestService(database, nil)
	checksumA := "2222222222222222222222222222222222222222222222222222222222222222"
	checksumB := "3333333333333333333333333333333333333333333333333333333333333333"

	registerScopedCandidate(t, om, "scope-a", checksumA, "org1", "proj1")
	registerScopedCandidate(t, om, "scope-b", checksumB, "org1", "proj1")
	registerScopedCandidate(t, om, "scope-c", "4444444444444444444444444444444444444444444444444444444444444444", "org1", "proj2")

	ids, err := om.ListObjectIDsPageByScope(context.Background(), "org1", "proj1", "read", "scope-a", 10, 0)
	if err != nil {
		t.Fatalf("ListObjectIDsPageByScope error: %v", err)
	}
	if len(ids) != 1 || ids[0] != "scope-b" {
		t.Fatalf("unexpected scoped page ids: %+v", ids)
	}
}

func TestListObjectIDsPageByScope_UsesDatabasePaginationForUnrestrictedScope(t *testing.T) {
	database := &pageSpyDB{SqliteDB: newSQLiteDatabase(t)}
	om := newTestService(database, nil)

	registerScopedCandidate(t, om, "scope-a", "2222222222222222222222222222222222222222222222222222222222222222", "org1", "proj1")
	registerScopedCandidate(t, om, "scope-b", "3333333333333333333333333333333333333333333333333333333333333333", "org1", "proj1")
	registerScopedCandidate(t, om, "scope-c", "4444444444444444444444444444444444444444444444444444444444444444", "org1", "proj1")

	ids, err := om.ListObjectIDsPageByScope(context.Background(), "org1", "proj1", "read", "scope-a", 1, 0)
	if err != nil {
		t.Fatalf("ListObjectIDsPageByScope error: %v", err)
	}
	if len(ids) != 1 || ids[0] != "scope-b" {
		t.Fatalf("unexpected scoped page ids: %+v", ids)
	}
	if database.pageCalls != 1 {
		t.Fatalf("expected one database page call, got %d", database.pageCalls)
	}
	if database.listCalls != 0 {
		t.Fatalf("expected no full scope list calls, got %d", database.listCalls)
	}
}

func TestListObjectIDsPageByScope_FallsBackWhenAuthzRestrictsResources(t *testing.T) {
	database := &pageSpyDB{SqliteDB: newSQLiteDatabase(t)}
	om := newTestService(database, nil)

	registerScopedCandidate(t, om, "secure-obj", "5555555555555555555555555555555555555555555555555555555555555555", "secure", "p1")
	restrictedCtx := buildLocalAuthzContext(map[string]map[string]bool{
		"/organization/other/project/p2": {"read": true},
	})

	ids, err := om.ListObjectIDsPageByScope(restrictedCtx, "secure", "p1", "read", "", 10, 0)
	if err != nil {
		t.Fatalf("ListObjectIDsPageByScope error: %v", err)
	}
	if len(ids) != 0 {
		t.Fatalf("expected authz fallback to filter ids, got %+v", ids)
	}
	if database.pageCalls != 0 {
		t.Fatalf("expected no unrestricted page calls, got %d", database.pageCalls)
	}
	if database.listCalls == 0 {
		t.Fatalf("expected fallback to full scope list")
	}
}

func TestListObjectIDsByScope_AuthzFiltering(t *testing.T) {
	database := newSQLiteDatabase(t)
	om := newTestService(database, nil)
	checksum := "5555555555555555555555555555555555555555555555555555555555555555"

	registerScopedCandidate(t, om, "secure-obj", checksum, "secure", "p1")

	unauthorizedCtx := buildLocalAuthzContext(map[string]map[string]bool{
		"/organization/other/project/p2": {"read": true},
	})
	ids, err := om.ListObjectIDsByScope(unauthorizedCtx, "secure", "p1", "read")
	if err != nil {
		t.Fatalf("ListObjectIDsByScope unauthorized error: %v", err)
	}
	if len(ids) != 0 {
		t.Fatalf("expected no ids for unauthorized context, got %+v", ids)
	}

	authorizedCtx := buildLocalAuthzContext(map[string]map[string]bool{
		"/organization/secure/project/p1": {"read": true},
	})
	ids, err = om.ListObjectIDsByScope(authorizedCtx, "secure", "p1", "read")
	if err != nil {
		t.Fatalf("ListObjectIDsByScope authorized error: %v", err)
	}
	if len(ids) != 1 || ids[0] != "secure-obj" {
		t.Fatalf("expected secure-obj, got %+v", ids)
	}
}

func TestPrepareScopedObjects_HydratesOnlyMissingSiblingIDs(t *testing.T) {
	checksum := "6666666666666666666666666666666666666666666666666666666666666666"
	controlled := []string{"/organization/org/project/proj"}
	tracked := &readObjectStore{}
	for _, obj := range []objects.Record{
		{
			Id:               "dup-a",
			CreatedTime:      drsISOTime("2026-01-01T00:00:00Z"),
			UpdatedTime:      ptrTime("2026-01-01T00:00:00Z"),
			Checksums:        []objects.Checksum{{Type: "sha256", Checksum: checksum}},
			ControlledAccess: &controlled,
			Authorizations:   map[string][]string{"org": {"proj"}},
			AccessMethods: &[]objects.AccessMethod{{
				Type:      "s3",
				AccessUrl: &objects.AccessURL{Url: "s3://bucket/dup-a"},
			}},
		},
		{
			Id:               "dup-b",
			CreatedTime:      drsISOTime("2026-01-02T00:00:00Z"),
			UpdatedTime:      ptrTime("2026-01-02T00:00:00Z"),
			Checksums:        []objects.Checksum{{Type: "sha256", Checksum: checksum}},
			ControlledAccess: &controlled,
			Authorizations:   map[string][]string{"org": {"proj"}},
			AccessMethods: &[]objects.AccessMethod{{
				Type:      "s3",
				AccessUrl: &objects.AccessURL{Url: "s3://bucket/dup-b"},
			}},
		},
	} {
		if err := tracked.CreateObject(context.Background(), &obj); err != nil {
			t.Fatalf("CreateObject(%s) failed: %v", obj.Id, err)
		}
	}
	om := objects.NewService(objects.Dependencies{Reader: tracked, Content: tracked, ChecksumScope: tracked})

	initial, err := tracked.GetBulkObjects(context.Background(), []string{"dup-a"})
	if err != nil {
		t.Fatalf("GetBulkObjects failed: %v", err)
	}
	tracked.BulkCalls = nil

	prepared, err := om.PrepareScopedObjects(context.Background(), initial, "org", "proj", "")
	if err != nil {
		t.Fatalf("PrepareScopedObjects failed: %v", err)
	}
	if len(prepared) != 1 {
		t.Fatalf("expected 1 canonical record, got %d", len(prepared))
	}
	if prepared[0].AccessMethods == nil || len(*prepared[0].AccessMethods) != 2 {
		t.Fatalf("expected merged access methods, got %+v", prepared[0].AccessMethods)
	}
	if len(tracked.BulkCalls) != 1 {
		t.Fatalf("expected 1 sibling hydration call, got %d", len(tracked.BulkCalls))
	}
	if !slices.Equal(tracked.BulkCalls[0], []string{"dup-b"}) {
		t.Fatalf("expected only missing sibling id to be hydrated, got %+v", tracked.BulkCalls[0])
	}
}

func drsISOTime(raw string) time.Time {
	tm, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		panic(err)
	}
	return tm
}

func ptrTime(raw string) *time.Time {
	tm := drsISOTime(raw)
	return &tm
}
