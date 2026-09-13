package objects_test

import (
	"context"
	"errors"
	"math"
	"slices"
	"testing"
	"time"

	"github.com/calypr/syfon/apigen/drs"
	"github.com/calypr/syfon/apigen/errorapi"
	"github.com/calypr/syfon/internal/access"
	"github.com/calypr/syfon/internal/objects"
)

func registerScopedCandidate(t *testing.T, om *objects.Service, id, checksum, org, project string) {
	t.Helper()
	controlled := []string{"/organization/" + org + "/project/" + project}
	_, err := om.RegisterCandidates(context.Background(), []drs.DrsObjectCandidate{{
		Aliases:          ptr([]string{"id:" + id}),
		ControlledAccess: &controlled,
		Checksums: []drs.Checksum{{
			Type:     "sha256",
			Checksum: checksum,
		}},
		AccessMethods: &[]drs.AccessMethod{{
			Type:      "s3",
			AccessUrl: &drs.AccessURL{Url: "s3://bucket/" + id},
		}},
		Size: 1,
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

	for _, obj := range []drs.DrsObject{
		{

			Id:               "uuid-a",
			CreatedTime:      created,
			UpdatedTime:      updated,
			Checksums:        []drs.Checksum{{Type: "sha256", Checksum: checksum}},
			ControlledAccess: &[]string{firstResource},
			AccessMethods: &[]drs.AccessMethod{{
				Type:      "s3",
				AccessUrl: &drs.AccessURL{Url: "s3://bucket/uuid-a"},
			}},
		},
		{

			Id:               "uuid-b",
			CreatedTime:      created,
			UpdatedTime:      updated,
			Checksums:        []drs.Checksum{{Type: "sha256", Checksum: checksum}},
			ControlledAccess: &[]string{secondResource},
			AccessMethods: &[]drs.AccessMethod{{
				Type:      "s3",
				AccessUrl: &drs.AccessURL{Url: "s3://bucket/uuid-b"},
			}},
		},
	} {
		if err := database.RegisterObjects(context.Background(), []drs.DrsObject{obj}); err != nil {
			t.Fatalf("RegisterObjects(%s) failed: %v", obj.Id, err)
		}
	}

	om := objects.NewService(database)
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
	for lookup, got := range map[string]*drs.DrsObject{
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

func TestGetObjectPreservesPublicSiblingAccess(t *testing.T) {
	database := newSQLiteDatabase(t)
	checksum := "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	controlledResource := "/organization/org/project/controlled"
	created := drsISOTime("2026-01-01T00:00:00Z")
	for _, obj := range []drs.DrsObject{
		{

			Id:          "public-uuid",
			CreatedTime: created,
			Checksums:   []drs.Checksum{{Type: "sha256", Checksum: checksum}},
			AccessMethods: &[]drs.AccessMethod{{
				Type:      "s3",
				AccessUrl: &drs.AccessURL{Url: "s3://bucket/public"},
			}},
		},
		{

			Id:               "controlled-uuid",
			CreatedTime:      created,
			Checksums:        []drs.Checksum{{Type: "sha256", Checksum: checksum}},
			ControlledAccess: &[]string{controlledResource},
			AccessMethods: &[]drs.AccessMethod{{
				Type:      "s3",
				AccessUrl: &drs.AccessURL{Url: "s3://bucket/controlled"},
			}},
		},
	} {
		if err := database.RegisterObjects(context.Background(), []drs.DrsObject{obj}); err != nil {
			t.Fatalf("RegisterObjects(%s) failed: %v", obj.Id, err)
		}
	}

	om := objects.NewService(database)
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
	for _, obj := range []drs.DrsObject{
		{Id: requestedSHA, Checksums: []drs.Checksum{{Type: "sha256", Checksum: otherSHA}}},
		{Id: "checksum-record", Checksums: []drs.Checksum{{Type: "sha256", Checksum: requestedSHA}}},
	} {
		if err := database.RegisterObjects(context.Background(), []drs.DrsObject{obj}); err != nil {
			t.Fatalf("RegisterObjects(%s) failed: %v", obj.Id, err)
		}
	}

	got, err := objects.NewService(database).GetObject(context.Background(), requestedSHA, "")
	if err != nil {
		t.Fatalf("GetObject failed: %v", err)
	}
	if got.Id != "checksum-record" {
		t.Fatalf("checksum lookup returned physical ID collision %q", got.Id)
	}
}

func TestGetBulkObjectsUsesGlobalSHAIdentity(t *testing.T) {
	database := &objectTestStore{}
	checksum := "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"
	firstResource := "/organization/org/project/first"
	secondResource := "/organization/org/project/second"
	created := drsISOTime("2026-01-01T00:00:00Z")
	for _, obj := range []drs.DrsObject{
		{Id: "bulk-a", CreatedTime: created, Checksums: []drs.Checksum{{Type: "sha256", Checksum: checksum}}, ControlledAccess: &[]string{firstResource}},
		{Id: "bulk-b", CreatedTime: created, Checksums: []drs.Checksum{{Type: "sha256", Checksum: checksum}}, ControlledAccess: &[]string{secondResource}},
	} {
		if err := database.RegisterObjects(context.Background(), []drs.DrsObject{obj}); err != nil {
			t.Fatalf("RegisterObjects(%s) failed: %v", obj.Id, err)
		}
	}

	ctx := buildLocalAuthzContext(map[string]map[string]bool{firstResource: {"read": true}})
	service := objects.NewService(database)
	got, err := service.GetBulkObjects(ctx, []string{"bulk-b"}, "read")
	if err != nil {
		t.Fatalf("GetBulkObjects failed: %v", err)
	}
	if len(got) != 1 || got[0].Id != "bulk-a" || got[0].ControlledAccess == nil || len(*got[0].ControlledAccess) != 2 {
		t.Fatalf("bulk read did not return the merged checksum identity: %+v", got)
	}
}

func TestListRecordsChecksumIdentityAndCursor(t *testing.T) {
	database := newSQLiteDatabase(t)
	om := objects.NewService(database)
	checksum := "1111111111111111111111111111111111111111111111111111111111111111"

	registerScopedCandidate(t, om, "chk-a", checksum, "org1", "proj1")
	registerScopedCandidate(t, om, "chk-b", checksum, "org1", "proj2")
	registerScopedCandidate(t, om, "chk-c", checksum, "org2", "proj1")

	ids, err := om.ListObjects(context.Background(), objects.RecordListQuery{Checksum: &objects.ChecksumQuery{Type: "sha256", Value: checksum}, RequiredMethod: "read", Limit: 2})
	if err != nil {
		t.Fatalf("ListRecords checksum error: %v", err)
	}
	if len(ids) != 1 || ids[0].Id != "chk-a" {
		t.Fatalf("unexpected page ids: %+v", ids)
	}
	ids, err = om.ListObjects(context.Background(), objects.RecordListQuery{Checksum: &objects.ChecksumQuery{Type: "sha256", Value: checksum}, RequiredMethod: "read", StartAfter: "chk-a", Limit: 2})
	if err != nil || len(ids) != 0 {
		t.Fatalf("aliases appeared after the canonical pagination cursor: ids=%v err=%v", ids, err)
	}
}

func TestListRecords_UsesTypedScopeAndPagePolicy(t *testing.T) {
	database := newSQLiteDatabase(t)
	service := objects.NewService(database)
	registerScopedCandidate(t, service, "prepared-a", "7777777777777777777777777777777777777777777777777777777777777777", "org", "proj")
	registerScopedCandidate(t, service, "prepared-b", "8888888888888888888888888888888888888888888888888888888888888888", "org", "proj")

	scope, err := objects.NewScope(" org ", " proj ")
	if err != nil {
		t.Fatalf("NewScope failed: %v", err)
	}
	page, err := service.ListObjects(context.Background(), objects.RecordListQuery{
		Scope:          scope,
		Limit:          1,
		Page:           1,
		RequiredMethod: "read",
	})
	if err != nil {
		t.Fatalf("ListRecords failed: %v", err)
	}
	if len(page) != 1 || page[0].Id != "prepared-b" {
		t.Fatalf("unexpected typed page: %+v", page)
	}
}

func TestListRecordsRejectsPageWindowOverflow(t *testing.T) {
	database := newSQLiteDatabase(t)
	service := objects.NewService(database)
	_, err := service.ListObjects(context.Background(), objects.RecordListQuery{Limit: 2, Page: math.MaxInt})
	if err == nil {
		t.Fatal("ListObjects accepted an overflowing page window")
	}
}

func TestLookupChecksumQueries_PreservesInputOrderAndDuplicates(t *testing.T) {
	database := newSQLiteDatabase(t)
	service := objects.NewService(database)
	checksum := "9999999999999999999999999999999999999999999999999999999999999999"
	registerScopedCandidate(t, service, "checksum-a", checksum, "org", "proj")

	queries := []objects.ChecksumQuery{
		{Type: "sha256", Value: checksum},
		{Type: "sha256", Value: checksum},
		{Type: "md5", Value: checksum},
	}
	matches, err := service.LookupChecksumQueries(context.Background(), queries, "")
	if err != nil {
		t.Fatalf("LookupChecksumQueries failed: %v", err)
	}
	if len(matches) != len(queries) {
		t.Fatalf("match count = %d, want %d", len(matches), len(queries))
	}
	for i := 0; i < 2; i++ {
		if len(matches[i]) != 1 || matches[i][0].Id != "checksum-a" {
			t.Fatalf("duplicate query %d mismatch: %+v", i, matches[i])
		}
	}
	if len(matches[2]) != 0 {
		t.Fatalf("typed mismatch should be empty: %+v", matches[2])
	}
}

func TestNewScope_RejectsProjectWithoutOrganization(t *testing.T) {
	if _, err := objects.NewScope("", "project"); err == nil {
		t.Fatal("expected project-only scope to be rejected")
	}
	scope, err := objects.NewScope("", "")
	if err != nil {
		t.Fatalf("empty scope failed: %v", err)
	}
	if scope != (objects.Scope{}) {
		t.Fatalf("empty scope = %+v, want zero scope", scope)
	}
}

func TestListRecordsScopeAndCursor(t *testing.T) {
	database := newSQLiteDatabase(t)
	om := objects.NewService(database)
	checksumA := "2222222222222222222222222222222222222222222222222222222222222222"
	checksumB := "3333333333333333333333333333333333333333333333333333333333333333"

	registerScopedCandidate(t, om, "scope-a", checksumA, "org1", "proj1")
	registerScopedCandidate(t, om, "scope-b", checksumB, "org1", "proj1")
	registerScopedCandidate(t, om, "scope-c", "4444444444444444444444444444444444444444444444444444444444444444", "org1", "proj2")

	ids, err := om.ListObjects(context.Background(), objects.RecordListQuery{Scope: objects.Scope{Organization: "org1", Project: "proj1"}, RequiredMethod: "read", StartAfter: "scope-a", Limit: 10})
	if err != nil {
		t.Fatalf("ListRecords scope error: %v", err)
	}
	if len(ids) != 1 || ids[0].Id != "scope-b" {
		t.Fatalf("unexpected scoped page ids: %+v", ids)
	}
}

func TestListRecordsFiltersUnauthorizedScopes(t *testing.T) {
	database := newSQLiteDatabase(t)
	om := objects.NewService(database)

	registerScopedCandidate(t, om, "secure-obj", "5555555555555555555555555555555555555555555555555555555555555555", "secure", "p1")
	restrictedCtx := buildLocalAuthzContext(map[string]map[string]bool{
		"/organization/other/project/p2": {"read": true},
	})

	ids, err := om.ListObjects(restrictedCtx, objects.RecordListQuery{Scope: objects.Scope{Organization: "secure", Project: "p1"}, RequiredMethod: "read", Limit: 10})
	if err != nil {
		t.Fatalf("ListRecords scope error: %v", err)
	}
	if len(ids) != 0 {
		t.Fatalf("expected authz fallback to filter ids, got %+v", ids)
	}
}

func TestListRecordsPreservesBroadWriteAuthorizationWithOptionalPager(t *testing.T) {
	database := newSQLiteDatabase(t)
	service := objects.NewService(database)
	registerScopedCandidate(t, service, "broad-write", "5656565656565656565656565656565656565656565656565656565656565656", "secure", "p1")
	ctx := buildLocalAuthzContext(map[string]map[string]bool{
		"/programs": {"update": true},
	})

	records, err := service.ListObjects(ctx, objects.RecordListQuery{
		Scope:          objects.Scope{Organization: "secure", Project: "p1"},
		RequiredMethod: "update",
		Limit:          10,
	})
	if err != nil {
		t.Fatalf("ListObjects returned an error: %v", err)
	}
	if len(records) != 1 || records[0].Id != "broad-write" {
		t.Fatalf("ListObjects returned %+v, want broad-write", records)
	}
}

func TestListRecordsBroadReadDoesNotExposePrivateOrphans(t *testing.T) {
	database := newSQLiteDatabase(t)
	service := objects.NewService(database)
	registerScopedCandidate(t, service, "private-orphan", "5757575757575757575757575757575757575757575757575757575757575757", "secure", "retired")
	resource := "/organization/secure/project/retired"
	deleteCtx := buildLocalAuthzContext(map[string]map[string]bool{
		resource: {"delete": true},
	})
	if removed, err := service.DeleteBulkByScope(deleteCtx, "secure", "retired"); err != nil || removed != 1 {
		t.Fatalf("DeleteBulkByScope removed %d records with error %v, want 1 record and no error", removed, err)
	}

	readCtx := buildLocalAuthzContext(map[string]map[string]bool{
		"/programs": {"read": true},
	})
	records, err := service.ListObjects(readCtx, objects.RecordListQuery{RequiredMethod: "read", Limit: 10})
	if err != nil {
		t.Fatalf("ListObjects returned an error: %v", err)
	}
	if len(records) != 0 {
		t.Fatalf("ListObjects returned private orphan %+v, want no records", records)
	}
}

func TestListRecordsDoesNotBroadenMalformedAuthorizationResource(t *testing.T) {
	database := newSQLiteDatabase(t)
	controlled := []string{"/organization/secure"}
	object := drs.DrsObject{
		Id:               "org-wide",
		Checksums:        []drs.Checksum{{Type: "sha256", Checksum: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}},
		ControlledAccess: &controlled,
		AccessMethods:    &[]drs.AccessMethod{{Type: "s3", AccessUrl: &drs.AccessURL{Url: "s3://bucket/org-wide"}}},
	}
	if err := database.RegisterObjects(context.Background(), []drs.DrsObject{object}); err != nil {
		t.Fatalf("RegisterObjects failed: %v", err)
	}
	session := access.NewSession("local")
	session.AuthzEnforced = true
	session.SetAuthorizations([]string{"/organization/secure/project"}, nil, true)

	got, err := objects.NewService(database).GetObject(access.WithSession(context.Background(), session), object.Id, "read")
	if !errors.Is(err, errorapi.ErrAccessDenied) {
		t.Fatalf("GetObject error=%v, want access denied", err)
	}
	if got != nil {
		t.Fatalf("malformed authorization unexpectedly returned object: %+v", got)
	}
}

func TestListObjectIDsByScope_AuthzFiltering(t *testing.T) {
	database := newSQLiteDatabase(t)
	om := objects.NewService(database)
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

func TestListRecordsPreservesLegacySiblingMethods(t *testing.T) {
	checksum := "6666666666666666666666666666666666666666666666666666666666666666"
	controlled := []string{"/organization/org/project/proj"}
	tracked := &objectTestStore{}
	for _, obj := range []drs.DrsObject{
		{
			Id:               "dup-a",
			CreatedTime:      drsISOTime("2026-01-01T00:00:00Z"),
			UpdatedTime:      ptrTime("2026-01-01T00:00:00Z"),
			Checksums:        []drs.Checksum{{Type: "sha256", Checksum: checksum}},
			ControlledAccess: &controlled,
			AccessMethods: &[]drs.AccessMethod{{
				Type:      "s3",
				AccessUrl: &drs.AccessURL{Url: "s3://bucket/dup-a"},
			}},
		},
		{
			Id:               "dup-b",
			CreatedTime:      drsISOTime("2026-01-02T00:00:00Z"),
			UpdatedTime:      ptrTime("2026-01-02T00:00:00Z"),
			Checksums:        []drs.Checksum{{Type: "sha256", Checksum: checksum}},
			ControlledAccess: &controlled,
			AccessMethods: &[]drs.AccessMethod{{
				Type:      "s3",
				AccessUrl: &drs.AccessURL{Url: "s3://bucket/dup-b"},
			}},
		},
	} {
		if err := tracked.RegisterObjects(context.Background(), []drs.DrsObject{obj}); err != nil {
			t.Fatalf("RegisterObjects(%s) failed: %v", obj.Id, err)
		}
	}
	om := objects.NewService(tracked)

	prepared, err := om.ListObjects(context.Background(), objects.RecordListQuery{
		Scope:    objects.Scope{Organization: "org", Project: "proj"},
		Checksum: &objects.ChecksumQuery{Type: "sha256", Value: checksum},
		Limit:    1,
	})
	if err != nil {
		t.Fatalf("ListRecords failed: %v", err)
	}
	if len(prepared) != 1 {
		t.Fatalf("expected 1 canonical record, got %d", len(prepared))
	}
	if prepared[0].AccessMethods == nil || len(*prepared[0].AccessMethods) != 2 {
		t.Fatalf("expected merged access methods, got %+v", prepared[0].AccessMethods)
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
