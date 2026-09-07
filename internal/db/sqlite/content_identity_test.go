package sqlite

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/calypr/syfon/apigen/server/drs"
	"github.com/calypr/syfon/internal/access"
	"github.com/calypr/syfon/internal/common"
	"github.com/calypr/syfon/internal/faults"
	"github.com/calypr/syfon/internal/models"
)

func TestContentIdentityRegistrationMergesAliasesGrantsAndLocations(t *testing.T) {
	db, err := NewSqliteDB(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	sha := strings.Repeat("a", 64)
	resourceA := "/organization/org/project/a"
	resourceB := "/organization/org/project/b"
	first := identityTestObject("uuid-a", sha, resourceA, "s3://bucket/a")
	second := identityTestObject("uuid-b", "sha256:"+strings.ToUpper(sha), resourceB, "s3://bucket/b")
	if err := db.RegisterObjects(testIdentityAuth(resourceA, "create", "read"), []models.InternalObject{first}); err != nil {
		t.Fatal(err)
	}
	if err := db.RegisterObjects(testIdentityAuth(resourceA, "read", "create", "update"), []models.InternalObject{second}); !errors.Is(err, faults.ErrUnauthorized) {
		t.Fatalf("expected missing target B create to deny merge, got %v", err)
	}

	admin := testIdentityAuth(resourceA, "read", "create", "update", "delete")
	admin = withIdentityPrivileges(admin, resourceB, "read", "create", "update", "delete")
	if err := db.RegisterObjects(admin, []models.InternalObject{second}); err != nil {
		t.Fatal(err)
	}
	var rows int
	if err := db.db.QueryRow(`SELECT COUNT(*) FROM drs_object`).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 1 {
		t.Fatalf("expected one physical object, got %d", rows)
	}
	obj, err := db.GetObject(context.Background(), "uuid-b")
	if err != nil {
		t.Fatal(err)
	}
	if obj.Id != "uuid-a" || obj.ControlledAccess == nil || len(*obj.ControlledAccess) != 2 || obj.AccessMethods == nil || len(*obj.AccessMethods) != 2 {
		t.Fatalf("unexpected merged object: %+v", obj)
	}
	if (*obj.AccessMethods)[0].AccessId == nil || (*obj.AccessMethods)[1].AccessId == nil || *(*obj.AccessMethods)[0].AccessId == *(*obj.AccessMethods)[1].AccessId {
		t.Fatalf("access IDs are not stable and unambiguous: %+v", *obj.AccessMethods)
	}
}

func TestContentIdentityRejectsConflictingSHAAtomically(t *testing.T) {
	db, err := NewSqliteDB(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	checksums := []drs.Checksum{
		{Type: "sha256", Checksum: strings.Repeat("a", 64)},
		{Type: "SHA-256", Checksum: strings.Repeat("b", 64)},
	}
	obj := identityTestObject("conflict", checksums[0].Checksum, "/organization/org/project/p", "s3://bucket/conflict")
	obj.Checksums = checksums
	err = db.RegisterObjects(context.Background(), []models.InternalObject{obj})
	if !errors.Is(err, common.ErrConflictingSHA256) {
		t.Fatalf("expected conflicting SHA error, got %v", err)
	}
	var rows int
	if err := db.db.QueryRow(`SELECT COUNT(*) FROM drs_object`).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 0 {
		t.Fatalf("conflicting registration wrote %d physical rows", rows)
	}
}

func TestContentIdentityReplaceIsAtomicAndPreservesSHA(t *testing.T) {
	db, err := NewSqliteDB(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	resource := "/organization/org/project/p"
	sha := strings.Repeat("c", 64)
	obj := identityTestObject("replace", sha, resource, "s3://bucket/old")
	ctx := testIdentityAuth(resource, "create", "read", "update", "delete")
	if err := db.RegisterObjects(ctx, []models.InternalObject{obj}); err != nil {
		t.Fatal(err)
	}
	name := "new-name"
	replacement := obj
	replacement.Name = &name
	replacement.AccessMethods = accessMethods("s3://bucket/new")
	if err := db.ReplaceObjects(ctx, []models.InternalObject{replacement}); err != nil {
		t.Fatal(err)
	}
	got, err := db.GetObject(context.Background(), "replace")
	if err != nil || got.Name == nil || *got.Name != name || got.AccessMethods == nil || (*got.AccessMethods)[0].AccessUrl.Url != "s3://bucket/new" {
		t.Fatalf("replacement did not apply: %v %+v", err, got)
	}
	bad := replacement
	bad.Size++
	if err := db.ReplaceObjects(ctx, []models.InternalObject{bad}); !errors.Is(err, faults.ErrConflict) {
		t.Fatalf("expected immutable-size conflict, got %v", err)
	}
	got, err = db.GetObject(context.Background(), "replace")
	if err != nil || got.Size != obj.Size || (*got.AccessMethods)[0].AccessUrl.Url != "s3://bucket/new" {
		t.Fatalf("failed replacement was not rolled back: %v %+v", err, got)
	}
}

func TestContentIdentityConcurrentRegistrationsShareOnePhysicalRow(t *testing.T) {
	dsn := filepath.Join(t.TempDir(), "identity.db")
	first, err := NewSqliteDB(dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer first.db.Close()
	second, err := NewSqliteDB(dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer second.db.Close()

	sha := strings.Repeat("d", 64)
	resource := "/organization/org/project/p"
	ctx := testIdentityAuth(resource, "create", "read", "update", "delete")
	objects := make([]models.InternalObject, 8)
	for i := range objects {
		objects[i] = identityTestObject(fmt.Sprintf("concurrent-%c", 'a'+i), sha, resource, fmt.Sprintf("s3://bucket/replica-%c", 'a'+i))
	}
	dbs := []*SqliteDB{first, second}
	start := make(chan struct{})
	var wg sync.WaitGroup
	errs := make(chan error, len(objects))
	for i := range objects {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			errs <- dbs[i%len(dbs)].RegisterObjects(ctx, []models.InternalObject{objects[i]})
		}(i)
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent registration failed: %v", err)
		}
	}

	var rows, aliases int
	if err := first.db.QueryRow(`SELECT COUNT(*) FROM drs_object`).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if err := first.db.QueryRow(`SELECT COUNT(*) FROM drs_object_alias`).Scan(&aliases); err != nil {
		t.Fatal(err)
	}
	if rows != 1 || aliases != len(objects)-1 {
		t.Fatalf("expected one canonical row and %d aliases, got rows=%d aliases=%d", 1, rows, aliases)
	}
	var canonicalID string
	if err := first.db.QueryRow(`SELECT id FROM drs_object`).Scan(&canonicalID); err != nil {
		t.Fatal(err)
	}
	for _, object := range objects {
		got, err := first.GetObject(context.Background(), object.Id)
		if err != nil {
			t.Fatalf("get %s: %v", object.Id, err)
		}
		if got.Id != canonicalID {
			t.Fatalf("lookup %s returned %s, want canonical %s", object.Id, got.Id, canonicalID)
		}
	}
}

func TestContentIdentityChecksumQueriesNormalizeOnlySHA256(t *testing.T) {
	db, err := NewSqliteDB(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	sha := strings.Repeat("e", 64)
	obj := identityTestObject("lookup", sha, "/organization/org/project/p", "s3://bucket/lookup")
	obj.Checksums = append(obj.Checksums, drs.Checksum{Type: "md5", Checksum: "ABC"})
	if err := db.RegisterObjects(testIdentityAuth("/organization/org/project/p", "create", "read"), []models.InternalObject{obj}); err != nil {
		t.Fatal(err)
	}

	exact, err := db.GetObjectsByChecksum(context.Background(), "ABC")
	if err != nil || len(exact) != 1 {
		t.Fatalf("exact generic checksum lookup got %d objects, err=%v", len(exact), err)
	}
	caseChanged, err := db.GetObjectsByChecksum(context.Background(), "abc")
	if err != nil || len(caseChanged) != 0 {
		t.Fatalf("generic checksum lookup unexpectedly normalized case: %d objects, err=%v", len(caseChanged), err)
	}
	prefixedKey := "SHA256:" + strings.ToUpper(sha)
	prefixed, err := db.GetObjectsByChecksums(context.Background(), []string{prefixedKey})
	if err != nil || len(prefixed[prefixedKey]) != 1 {
		t.Fatalf("normalized SHA bulk lookup missed result: %#v, err=%v", prefixed, err)
	}
}

func identityTestObject(id, sha, resource, url string) models.InternalObject {
	now := time.Now().UTC()
	controlled := []string{resource}
	return models.InternalObject{DrsObject: drs.DrsObject{
		Id: id, Size: 7, CreatedTime: now, UpdatedTime: &now,
		Name: common.Ptr(id), Checksums: []drs.Checksum{{Type: "sha256", Checksum: sha}},
		AccessMethods: accessMethods(url), ControlledAccess: &controlled,
	}}
}

func accessMethods(url string) *[]drs.AccessMethod {
	return &[]drs.AccessMethod{{Type: drs.AccessMethodTypeS3, AccessUrl: &struct {
		Headers *[]string `json:"headers,omitempty"`
		Url     string    `json:"url"`
	}{Url: url}}}
}

func testIdentityAuth(resource string, methods ...string) context.Context {
	return withIdentityPrivileges(context.Background(), resource, methods...)
}

func withIdentityPrivileges(ctx context.Context, resource string, methods ...string) context.Context {
	session := access.FromContext(ctx)
	privileges := make(map[string]map[string]bool, len(session.Privileges)+1)
	for existing, existingMethods := range session.Privileges {
		privileges[existing] = make(map[string]bool, len(existingMethods))
		for method, allowed := range existingMethods {
			privileges[existing][method] = allowed
		}
	}
	if _, ok := privileges[resource]; !ok {
		privileges[resource] = map[string]bool{}
	}
	for _, method := range methods {
		privileges[resource][method] = true
	}
	resources := append([]string(nil), session.Resources...)
	resources = append(resources, resource)
	session.SetAuthorizations(resources, privileges, true)
	return access.WithSession(ctx, session)
}
