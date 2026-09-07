package core

import (
	"context"
	"database/sql"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/calypr/syfon/apigen/server/drs"
	"github.com/calypr/syfon/internal/db"
	"github.com/calypr/syfon/internal/db/sqlite"
	"github.com/calypr/syfon/internal/models"
	"github.com/calypr/syfon/internal/testutils"
)

type pageSpyDB struct {
	db.DatabaseInterface
	pageCalls int
	listCalls int
}

func (s *pageSpyDB) ListObjectIDsPageByScope(ctx context.Context, organization, project, startAfter string, limit, offset int) ([]string, error) {
	s.pageCalls++
	return s.DatabaseInterface.(db.ObjectIDPageLister).ListObjectIDsPageByScope(ctx, organization, project, startAfter, limit, offset)
}

func (s *pageSpyDB) ListObjectIDsPageByResources(ctx context.Context, resources []string, includeUnscoped bool, startAfter string, limit, offset int) ([]string, error) {
	return s.DatabaseInterface.(db.ObjectIDPageLister).ListObjectIDsPageByResources(ctx, resources, includeUnscoped, startAfter, limit, offset)
}

func (s *pageSpyDB) ListObjectIDsByScope(ctx context.Context, organization, project string) ([]string, error) {
	s.listCalls++
	return s.DatabaseInterface.ListObjectIDsByScope(ctx, organization, project)
}

func registerScopedCandidate(t *testing.T, om *ObjectManager, id, checksum, org, project string) {
	t.Helper()
	controlled := []string{"/organization/" + org + "/project/" + project}
	_, err := om.RegisterBulk(context.Background(), []drs.DrsObjectCandidate{{
		Aliases:          ptr([]string{"id:" + id}),
		ControlledAccess: &controlled,
		Checksums: []drs.Checksum{{
			Type:     "sha256",
			Checksum: checksum,
		}},
		AccessMethods: &[]drs.AccessMethod{{
			Type: "s3",
			AccessUrl: &struct {
				Headers *[]string `json:"headers,omitempty"`
				Url     string    `json:"url"`
			}{Url: "s3://bucket/" + id},
		}},
		Size: 1,
	}})
	if err != nil {
		t.Fatalf("RegisterBulk(%s): %v", id, err)
	}
}

func TestGetObjectUsesGlobalSHAIdentityAcrossUUIDs(t *testing.T) {
	database, err := sqlite.NewSqliteDB(":memory:")
	if err != nil {
		t.Fatalf("NewSqliteDB failed: %v", err)
	}
	checksum := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	firstResource := "/organization/org1/project/project1"
	secondResource := "/organization/org2/project/project2"
	created := drsISOTime("2026-01-01T00:00:00Z")
	updated := ptrTime("2026-01-01T00:00:00Z")

	for _, obj := range []models.InternalObject{
		{
			Authorizations: map[string][]string{"org1": {"project1"}},
			DrsObject: drs.DrsObject{
				Id:               "uuid-a",
				CreatedTime:      created,
				UpdatedTime:      updated,
				Checksums:        []drs.Checksum{{Type: "sha256", Checksum: checksum}},
				ControlledAccess: &[]string{firstResource},
				AccessMethods: &[]drs.AccessMethod{{
					Type: drs.AccessMethodTypeS3,
					AccessUrl: &struct {
						Headers *[]string `json:"headers,omitempty"`
						Url     string    `json:"url"`
					}{Url: "s3://bucket/uuid-a"},
				}},
			},
		},
		{
			Authorizations: map[string][]string{"org2": {"project2"}},
			DrsObject: drs.DrsObject{
				Id:               "uuid-b",
				CreatedTime:      created,
				UpdatedTime:      updated,
				Checksums:        []drs.Checksum{{Type: "sha256", Checksum: checksum}},
				ControlledAccess: &[]string{secondResource},
				AccessMethods: &[]drs.AccessMethod{{
					Type: drs.AccessMethodTypeS3,
					AccessUrl: &struct {
						Headers *[]string `json:"headers,omitempty"`
						Url     string    `json:"url"`
					}{Url: "s3://bucket/uuid-b"},
				}},
			},
		},
	} {
		if err := database.CreateObject(context.Background(), &obj); err != nil {
			t.Fatalf("CreateObject(%s) failed: %v", obj.Id, err)
		}
	}

	om := NewObjectManager(database, nil)
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

	for lookup, got := range map[string]*models.InternalObject{
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
	database, err := sqlite.NewSqliteDB(":memory:")
	if err != nil {
		t.Fatalf("NewSqliteDB failed: %v", err)
	}
	checksum := "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	controlledResource := "/organization/org/project/controlled"
	created := drsISOTime("2026-01-01T00:00:00Z")
	for _, obj := range []models.InternalObject{
		{
			DrsObject: drs.DrsObject{
				Id:          "public-uuid",
				CreatedTime: created,
				Checksums:   []drs.Checksum{{Type: "sha256", Checksum: checksum}},
				AccessMethods: &[]drs.AccessMethod{{
					Type: drs.AccessMethodTypeS3,
					AccessUrl: &struct {
						Headers *[]string `json:"headers,omitempty"`
						Url     string    `json:"url"`
					}{Url: "s3://bucket/public"},
				}},
			},
		},
		{
			Authorizations: map[string][]string{"org": {"controlled"}},
			DrsObject: drs.DrsObject{
				Id:               "controlled-uuid",
				CreatedTime:      created,
				Checksums:        []drs.Checksum{{Type: "sha256", Checksum: checksum}},
				ControlledAccess: &[]string{controlledResource},
				AccessMethods: &[]drs.AccessMethod{{
					Type: drs.AccessMethodTypeS3,
					AccessUrl: &struct {
						Headers *[]string `json:"headers,omitempty"`
						Url     string    `json:"url"`
					}{Url: "s3://bucket/controlled"},
				}},
			},
		},
	} {
		if err := database.CreateObject(context.Background(), &obj); err != nil {
			t.Fatalf("CreateObject(%s) failed: %v", obj.Id, err)
		}
	}

	om := NewObjectManager(database, nil)
	got, err := om.GetObject(buildLocalAuthzContext(nil), "controlled-uuid", "read")
	if err != nil {
		t.Fatalf("public checksum family should be readable: %v", err)
	}
	if got.AccessMethods == nil || len(*got.AccessMethods) != 2 {
		t.Fatalf("expected both public and controlled locations, got %+v", got.AccessMethods)
	}
}

func TestGetObjectPrefersSHAIdentityOverCollidingPhysicalID(t *testing.T) {
	database, err := sqlite.NewSqliteDB(":memory:")
	if err != nil {
		t.Fatalf("NewSqliteDB failed: %v", err)
	}
	requestedSHA := "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
	otherSHA := "dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd"
	for _, obj := range []models.InternalObject{
		{DrsObject: drs.DrsObject{Id: requestedSHA, Checksums: []drs.Checksum{{Type: "sha256", Checksum: otherSHA}}}},
		{DrsObject: drs.DrsObject{Id: "checksum-record", Checksums: []drs.Checksum{{Type: "sha256", Checksum: requestedSHA}}}},
	} {
		if err := database.CreateObject(context.Background(), &obj); err != nil {
			t.Fatalf("CreateObject(%s) failed: %v", obj.Id, err)
		}
	}

	got, err := NewObjectManager(database, nil).GetObject(context.Background(), requestedSHA, "")
	if err != nil {
		t.Fatalf("GetObject failed: %v", err)
	}
	if got.Id != "checksum-record" {
		t.Fatalf("checksum lookup returned physical ID collision %q", got.Id)
	}
}

func TestGetBulkObjectsUsesGlobalSHAIdentity(t *testing.T) {
	database, err := sqlite.NewSqliteDB(":memory:")
	if err != nil {
		t.Fatalf("NewSqliteDB failed: %v", err)
	}
	checksum := "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"
	firstResource := "/organization/org/project/first"
	secondResource := "/organization/org/project/second"
	created := drsISOTime("2026-01-01T00:00:00Z")
	for _, obj := range []models.InternalObject{
		{Authorizations: map[string][]string{"org": {"first"}}, DrsObject: drs.DrsObject{Id: "bulk-a", CreatedTime: created, Checksums: []drs.Checksum{{Type: "sha256", Checksum: checksum}}, ControlledAccess: &[]string{firstResource}}},
		{Authorizations: map[string][]string{"org": {"second"}}, DrsObject: drs.DrsObject{Id: "bulk-b", CreatedTime: created, Checksums: []drs.Checksum{{Type: "sha256", Checksum: checksum}}, ControlledAccess: &[]string{secondResource}}},
	} {
		if err := database.CreateObject(context.Background(), &obj); err != nil {
			t.Fatalf("CreateObject(%s) failed: %v", obj.Id, err)
		}
	}

	ctx := buildLocalAuthzContext(map[string]map[string]bool{firstResource: {"read": true}})
	got, err := NewObjectManager(database, nil).GetBulkObjects(ctx, []string{"bulk-b"}, "read")
	if err != nil {
		t.Fatalf("GetBulkObjects failed: %v", err)
	}
	if len(got) != 1 || got[0].Id != "bulk-a" || got[0].ControlledAccess == nil || len(*got[0].ControlledAccess) != 2 {
		t.Fatalf("bulk read did not return the merged checksum identity: %+v", got)
	}
}

func TestCanonicalContentMetadataIsDeterministicOnTimestampTie(t *testing.T) {
	created := drsISOTime("2026-01-01T00:00:00Z")
	lowName := "low"
	highName := "high"
	lowDescription := "low description"
	highDescription := "high description"
	low := models.InternalObject{DrsObject: drs.DrsObject{Id: "uuid-a", Name: &lowName, Description: &lowDescription, Size: 1, CreatedTime: created, Checksums: []drs.Checksum{{Type: "sha256", Checksum: strings.Repeat("f", 64)}}}}
	high := models.InternalObject{DrsObject: drs.DrsObject{Id: "uuid-b", Name: &highName, Description: &highDescription, Size: 2, CreatedTime: created, Checksums: low.Checksums}}

	forward := canonicalizeContentObjects([]models.InternalObject{low, high})
	reverse := canonicalizeContentObjects([]models.InternalObject{high, low})
	if !reflect.DeepEqual(forward, reverse) {
		t.Fatalf("canonical metadata depends on input order: forward=%+v reverse=%+v", forward, reverse)
	}
	if len(forward) != 1 || forward[0].Id != low.Id || forward[0].Size != high.Size || forward[0].Description == nil || *forward[0].Description != highDescription {
		t.Fatalf("expected stable uuid-a identity and deterministic latest metadata: %+v", forward)
	}
}

func TestListObjectIDsPageByChecksum_ReturnsCanonicalContentID(t *testing.T) {
	database := testutils.NewInMemoryDB()
	om := NewObjectManager(database, nil)
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
	database := testutils.NewInMemoryDB()
	om := NewObjectManager(database, nil)
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
	database := &pageSpyDB{DatabaseInterface: testutils.NewInMemoryDB()}
	om := NewObjectManager(database, nil)

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
	database := &pageSpyDB{DatabaseInterface: testutils.NewInMemoryDB()}
	om := NewObjectManager(database, nil)

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
	database := testutils.NewInMemoryDB()
	om := NewObjectManager(database, nil)
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

func TestSearchAfterID(t *testing.T) {
	ids := []string{"a", "b", "c", "d"}
	if got := searchAfterID(ids, "b"); got != 2 {
		t.Fatalf("expected index 2 for startAfter=b, got %d", got)
	}
	if got := searchAfterID(ids, "bb"); got != 2 {
		t.Fatalf("expected index 2 for startAfter=bb, got %d", got)
	}
	if got := searchAfterID(ids, "z"); got != len(ids) {
		t.Fatalf("expected index %d for startAfter=z, got %d", len(ids), got)
	}
}

func TestObjectMatchesScope(t *testing.T) {
	obj := &models.InternalObject{Authorizations: map[string][]string{"org1": {"p1", "p2"}}}
	if !objectMatchesScope(obj, "org1", "p1") {
		t.Fatalf("expected org1/p1 to match")
	}
	if objectMatchesScope(obj, "org1", "p3") {
		t.Fatalf("expected org1/p3 not to match")
	}
	if !objectMatchesScope(obj, "org1", "") {
		t.Fatalf("expected org-wide org1 to match")
	}
}

type trackingDB struct {
	db.DatabaseInterface
	bulkCalls [][]string
}

func (t *trackingDB) GetBulkObjects(ctx context.Context, ids []string) ([]models.InternalObject, error) {
	copyIDs := append([]string(nil), ids...)
	t.bulkCalls = append(t.bulkCalls, copyIDs)
	return t.DatabaseInterface.GetBulkObjects(ctx, ids)
}

func TestPrepareScopedObjects_HydratesOnlyMissingSiblingIDs(t *testing.T) {
	fixturePath := filepath.Join(t.TempDir(), "legacy.sqlite")
	base, err := sqlite.NewSqliteDB(fixturePath)
	if err != nil {
		t.Fatalf("NewSqliteDB failed: %v", err)
	}
	raw, err := sql.Open("sqlite3", fixturePath)
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	tracked := &trackingDB{DatabaseInterface: base}
	om := NewObjectManager(tracked, nil)
	checksum := "6666666666666666666666666666666666666666666666666666666666666666"
	controlled := []string{"/organization/org/project/proj"}

	for _, obj := range []models.InternalObject{
		{
			Authorizations: map[string][]string{"org": {"proj"}},
			DrsObject: drs.DrsObject{
				Id:               "dup-a",
				CreatedTime:      drsISOTime("2026-01-01T00:00:00Z"),
				UpdatedTime:      ptrTime("2026-01-01T00:00:00Z"),
				Checksums:        []drs.Checksum{{Type: "sha256", Checksum: checksum}},
				ControlledAccess: &controlled,
				AccessMethods: &[]drs.AccessMethod{{
					Type: drs.AccessMethodTypeS3,
					AccessUrl: &struct {
						Headers *[]string `json:"headers,omitempty"`
						Url     string    `json:"url"`
					}{Url: "s3://bucket/dup-a"},
				}},
			},
		},
		{
			Authorizations: map[string][]string{"org": {"proj"}},
			DrsObject: drs.DrsObject{
				Id:               "dup-b",
				CreatedTime:      drsISOTime("2026-01-02T00:00:00Z"),
				UpdatedTime:      ptrTime("2026-01-02T00:00:00Z"),
				Checksums:        []drs.Checksum{{Type: "sha256", Checksum: checksum}},
				ControlledAccess: &controlled,
				AccessMethods: &[]drs.AccessMethod{{
					Type: drs.AccessMethodTypeS3,
					AccessUrl: &struct {
						Headers *[]string `json:"headers,omitempty"`
						Url     string    `json:"url"`
					}{Url: "s3://bucket/dup-b"},
				}},
			},
		},
	} {
		if _, err := raw.Exec(`INSERT INTO drs_object (id,size,created_time,updated_time,name,version,description) VALUES (?,0,?,?, '', '', '')`, obj.Id, obj.CreatedTime, *obj.UpdatedTime); err != nil {
			t.Fatal(err)
		}
		if _, err := raw.Exec(`INSERT INTO drs_object_checksum (object_id,type,checksum) VALUES (?, 'sha256', ?)`, obj.Id, checksum); err != nil {
			t.Fatal(err)
		}
		if _, err := raw.Exec(`INSERT INTO drs_object_controlled_access (object_id,resource) VALUES (?, ?)`, obj.Id, controlled[0]); err != nil {
			t.Fatal(err)
		}
		if _, err := raw.Exec(`INSERT INTO drs_object_access_method (object_id,type,url) VALUES (?, 's3', ?)`, obj.Id, (*obj.AccessMethods)[0].AccessUrl.Url); err != nil {
			t.Fatal(err)
		}
	}

	initial, err := tracked.GetBulkObjects(context.Background(), []string{"dup-a"})
	if err != nil {
		t.Fatalf("GetBulkObjects failed: %v", err)
	}
	tracked.bulkCalls = nil

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
	if len(tracked.bulkCalls) != 1 {
		t.Fatalf("expected 1 sibling hydration call, got %d", len(tracked.bulkCalls))
	}
	if !slices.Equal(tracked.bulkCalls[0], []string{"dup-b"}) {
		t.Fatalf("expected only missing sibling id to be hydrated, got %+v", tracked.bulkCalls[0])
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

func TestReadableChecksumFilter(t *testing.T) {
	database := testutils.NewInMemoryDB()
	om := NewObjectManager(database, nil)

	unenforcedCtx := context.Background()
	res, includeUnscoped, restrict, ok := om.readableChecksumFilter(unenforcedCtx, "", "")
	if !ok || includeUnscoped || restrict || res != nil {
		t.Fatalf("unexpected unenforced filter: res=%+v includeUnscoped=%v restrict=%v ok=%v", res, includeUnscoped, restrict, ok)
	}

	forbiddenCtx := buildGen3Context(map[string]map[string]bool{})
	res, includeUnscoped, restrict, ok = om.readableChecksumFilter(forbiddenCtx, "", "")
	if !ok || !includeUnscoped || !restrict {
		t.Fatalf("expected restricted filter under enforced authz, got res=%+v includeUnscoped=%v restrict=%v ok=%v", res, includeUnscoped, restrict, ok)
	}
}
