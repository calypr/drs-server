package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/calypr/syfon/apigen/drs"
	"github.com/calypr/syfon/apigen/errorapi"
	"github.com/calypr/syfon/internal/objects"
)

func TestSqliteAccessMethodsPreserveFullContractAcrossWrites(t *testing.T) {
	ctx := context.Background()
	db, err := NewSqliteDB(":memory:", nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })

	methods := sqliteFullAccessMethods()
	now := time.Now().UTC()
	register := func(id string) drs.DrsObject {
		return drs.DrsObject{Id: id, CreatedTime: now, UpdatedTime: &now, AccessMethods: &methods}
	}
	if err := db.RegisterObjects(ctx, []drs.DrsObject{register("registered")}); err != nil {
		t.Fatalf("RegisterObjects: %v", err)
	}
	if err := db.RegisterObjects(ctx, []drs.DrsObject{register("replaced")}); err != nil {
		t.Fatalf("RegisterObjects(replaced): %v", err)
	}
	if err := db.ReplaceObjects(ctx, []drs.DrsObject{register("replaced")}); err != nil {
		t.Fatalf("ReplaceObjects: %v", err)
	}
	if err := db.RegisterObjects(ctx, []drs.DrsObject{register("updated")}); err != nil {
		t.Fatalf("RegisterObjects(updated): %v", err)
	}
	if err := db.UpdateObjectAccessMethods(ctx, "updated", methods); err != nil {
		t.Fatalf("UpdateObjectAccessMethods: %v", err)
	}
	if err := db.RegisterObjects(ctx, []drs.DrsObject{register("bulk")}); err != nil {
		t.Fatalf("RegisterObjects(bulk): %v", err)
	}
	if err := db.BulkUpdateAccessMethods(ctx, map[string][]drs.AccessMethod{"bulk": methods}); err != nil {
		t.Fatalf("BulkUpdateAccessMethods: %v", err)
	}

	for _, id := range []string{"registered", "replaced", "updated", "bulk"} {
		got, err := db.GetObject(ctx, id)
		if err != nil {
			t.Fatalf("GetObject(%q): %v", id, err)
		}
		assertSqliteAccessMethodsEqual(t, got.AccessMethods, methods)
		bulk, err := db.GetBulkObjects(ctx, []string{id})
		if err != nil {
			t.Fatalf("GetBulkObjects(%q): %v", id, err)
		}
		if len(bulk) != 1 {
			t.Fatalf("GetBulkObjects(%q) returned %d objects", id, len(bulk))
		}
		assertSqliteAccessMethodsEqual(t, bulk[0].AccessMethods, methods)
	}
}

func TestSqliteAccessMethodsLegacyRowsGetStableGeneratedAccessID(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "legacy-access-methods.db")
	raw, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		`CREATE TABLE drs_object (id TEXT PRIMARY KEY, size INTEGER, created_time TIMESTAMP, updated_time TIMESTAMP, name TEXT, version TEXT, description TEXT)`,
		`CREATE TABLE drs_object_access_method (object_id TEXT, url TEXT, type TEXT)`,
		`INSERT INTO drs_object (id, size, created_time, updated_time, name, version, description) VALUES ('legacy-access', 1, '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z', 'legacy', '1', '')`,
		`INSERT INTO drs_object_access_method (object_id, url, type) VALUES ('legacy-access', 'https://example.test/legacy', 'https')`,
	} {
		if _, err := raw.Exec(statement); err != nil {
			_ = raw.Close()
			t.Fatalf("legacy schema statement %q: %v", statement, err)
		}
	}
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}

	db, err := NewSqliteDB(dbPath, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	got, err := db.GetObject(context.Background(), "legacy-access")
	if err != nil {
		t.Fatal(err)
	}
	if got.AccessMethods == nil || len(*got.AccessMethods) != 1 {
		t.Fatalf("legacy access methods = %#v, want one method", got.AccessMethods)
	}
	method := (*got.AccessMethods)[0]
	if method.AccessId == nil || *method.AccessId != "https-b95943be0074066ad244a6a9" {
		t.Fatalf("legacy access ID = %#v, want stable generated ID", method.AccessId)
	}
	if method.AccessUrl == nil || method.AccessUrl.Url != "https://example.test/legacy" {
		t.Fatalf("legacy access URL = %#v", method.AccessUrl)
	}
}

func TestSqliteLegacyAccessMethodPayloadUpgradeUsesRawCoordinates(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "legacy-padded-access-methods.db")
	raw, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		`CREATE TABLE drs_object (id TEXT PRIMARY KEY, size INTEGER, created_time TIMESTAMP, updated_time TIMESTAMP, name TEXT, version TEXT, description TEXT)`,
		`CREATE TABLE drs_object_access_method (object_id TEXT, url TEXT, type TEXT)`,
		`INSERT INTO drs_object (id, size, created_time, updated_time, name, version, description) VALUES ('legacy-padded-access', 1, '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z', 'legacy', '1', '')`,
		`INSERT INTO drs_object_access_method (object_id, url, type) VALUES ('legacy-padded-access', '  https://example.test/padded  ', ' HTTPS ')`,
		`INSERT INTO drs_object_access_method (object_id, url, type) VALUES ('legacy-padded-access', 'https://example.test/padded ', 'https')`,
	} {
		if _, err := raw.Exec(statement); err != nil {
			_ = raw.Close()
			t.Fatalf("legacy schema statement %q: %v", statement, err)
		}
	}
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}

	db, err := NewSqliteDB(dbPath, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	headers := []string{"X-Legacy-Upgrade: preserved"}
	methods := []drs.AccessMethod{{
		AccessUrl: &drs.AccessURL{Url: "https://example.test/padded", Headers: &headers},
		Type:      drs.AccessMethodTypeHttps,
	}}
	now := time.Now().UTC()
	if err := db.RegisterObjects(context.Background(), []drs.DrsObject{{Id: "legacy-padded-access", CreatedTime: now, UpdatedTime: &now, AccessMethods: &methods}}); err != nil {
		t.Fatalf("RegisterObjects legacy payload upgrade: %v", err)
	}
	var upgradedRows int
	if err := db.DB().QueryRow(`SELECT count(*) FROM drs_object_access_method WHERE object_id = ? AND access_method_json IS NOT NULL AND trim(access_method_json) <> ''`, "legacy-padded-access").Scan(&upgradedRows); err != nil {
		t.Fatalf("count upgraded legacy rows: %v", err)
	}
	if upgradedRows != 2 {
		t.Fatalf("upgraded legacy payload rows = %d, want 2", upgradedRows)
	}
	got, err := db.GetObject(context.Background(), "legacy-padded-access")
	if err != nil {
		t.Fatal(err)
	}
	if got.AccessMethods == nil || len(*got.AccessMethods) != 1 {
		t.Fatalf("upgraded legacy access methods = %#v, want one method", got.AccessMethods)
	}
	method := (*got.AccessMethods)[0]
	if method.AccessId == nil {
		t.Fatalf("upgraded legacy method has no generated access ID: %#v", method)
	}
	if method.AccessUrl == nil || method.AccessUrl.Url != "https://example.test/padded" || method.AccessUrl.Headers == nil || !reflect.DeepEqual(*method.AccessUrl.Headers, headers) {
		t.Fatalf("upgraded legacy method = %#v, want trimmed URL and headers", method)
	}
}

func TestSqliteAccessMethodsPreserveSameURLDistinctIDs(t *testing.T) {
	db, err := NewSqliteDB(":memory:", nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })

	now := time.Now().UTC()
	url := "https://example.test/same-location"
	firstID := "download-primary"
	secondID := "download-secondary"
	caseOnlyID := "DOWNLOAD-PRIMARY"
	methods := []drs.AccessMethod{
		{AccessId: &firstID, AccessUrl: &drs.AccessURL{Url: url}, Type: drs.AccessMethodTypeHttps},
		{AccessId: &secondID, AccessUrl: &drs.AccessURL{Url: url}, Type: drs.AccessMethodTypeHttps},
		{AccessId: &caseOnlyID, AccessUrl: &drs.AccessURL{Url: url}, Type: drs.AccessMethodTypeHttps},
	}
	object := drs.DrsObject{Id: "same-location-ids", CreatedTime: now, UpdatedTime: &now, AccessMethods: &methods}
	ctx := context.Background()
	if err := db.RegisterObjects(ctx, []drs.DrsObject{object}); err != nil {
		t.Fatalf("RegisterObjects: %v", err)
	}
	assertMethods := func(operation string) {
		t.Helper()
		got, err := db.GetObject(ctx, object.Id)
		if err != nil {
			t.Fatalf("GetObject after %s: %v", operation, err)
		}
		if got.AccessMethods == nil || len(*got.AccessMethods) != 2 {
			t.Fatalf("same URL access methods after %s = %#v, want two distinct IDs with one case-only duplicate", operation, got.AccessMethods)
		}
		ids := make(map[string]struct{}, len(*got.AccessMethods))
		for _, method := range *got.AccessMethods {
			if method.AccessId == nil {
				t.Fatalf("hydrated method after %s %#v has no access ID", operation, method)
			}
			ids[*method.AccessId] = struct{}{}
		}
		if _, ok := ids[firstID]; !ok {
			t.Fatalf("same URL methods after %s lost first ID %q: %#v", operation, firstID, ids)
		}
		if _, ok := ids[secondID]; !ok {
			t.Fatalf("same URL methods after %s lost second ID %q: %#v", operation, secondID, ids)
		}
	}
	assertMethods("registration")
	if err := db.ReplaceObjects(ctx, []drs.DrsObject{object}); err != nil {
		t.Fatalf("ReplaceObjects: %v", err)
	}
	assertMethods("replacement")
	if err := db.UpdateObjectAccessMethods(ctx, object.Id, methods); err != nil {
		t.Fatalf("UpdateObjectAccessMethods: %v", err)
	}
	assertMethods("single update")
	if err := db.BulkUpdateAccessMethods(ctx, map[string][]drs.AccessMethod{object.Id: methods}); err != nil {
		t.Fatalf("BulkUpdateAccessMethods: %v", err)
	}
	assertMethods("bulk update")
}

func TestSqliteAccessMethodIDCollisionsRejectAndPreserveState(t *testing.T) {
	db, err := NewSqliteDB(":memory:", nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })

	ctx := context.Background()
	now := time.Now().UTC()
	objectID := "access-id-collision"
	url := "https://example.test/original"
	originalID := "shared-download"
	original := drs.AccessMethod{AccessId: &originalID, AccessUrl: &drs.AccessURL{Url: url}, Type: drs.AccessMethodTypeHttps}
	originalMethods := []drs.AccessMethod{original}
	object := drs.DrsObject{Id: objectID, CreatedTime: now, UpdatedTime: &now, AccessMethods: &originalMethods}
	if err := db.RegisterObjects(ctx, []drs.DrsObject{object}); err != nil {
		t.Fatalf("seed RegisterObjects: %v", err)
	}

	conflictingID := "SHARED-DOWNLOAD"
	conflicting := drs.AccessMethod{
		AccessId:  &conflictingID,
		AccessUrl: &drs.AccessURL{Url: "https://example.test/conflicting"},
		Type:      drs.AccessMethodTypeHttps,
	}
	changedHeaders := []string{"X-Changed: payload"}
	payloadConflict := drs.AccessMethod{
		AccessId:  &originalID,
		AccessUrl: &drs.AccessURL{Url: url, Headers: &changedHeaders},
		Type:      drs.AccessMethodTypeHttps,
	}
	assertPreserved := func(operation string, err error) {
		t.Helper()
		if !errors.Is(err, errorapi.ErrInvalidInput) {
			t.Fatalf("%s error = %v, want ErrInvalidInput", operation, err)
		}
		got, getErr := db.GetObject(ctx, objectID)
		if getErr != nil {
			t.Fatalf("GetObject after %s: %v", operation, getErr)
		}
		if got.AccessMethods == nil || len(*got.AccessMethods) != 1 || !reflect.DeepEqual((*got.AccessMethods)[0], original) {
			t.Fatalf("state after %s = %#v, want %#v", operation, got.AccessMethods, originalMethods)
		}
	}

	registerMethods := []drs.AccessMethod{conflicting}
	assertPreserved("registration merge", db.RegisterObjects(ctx, []drs.DrsObject{{Id: objectID, CreatedTime: now, UpdatedTime: &now, AccessMethods: &registerMethods}}))
	assertPreserved("registration payload conflict", db.RegisterObjects(ctx, []drs.DrsObject{{Id: objectID, CreatedTime: now, UpdatedTime: &now, AccessMethods: &[]drs.AccessMethod{payloadConflict}}}))
	replacement := []drs.AccessMethod{original, conflicting}
	assertPreserved("single update", db.UpdateObjectAccessMethods(ctx, objectID, replacement))
	assertPreserved("bulk update", db.BulkUpdateAccessMethods(ctx, map[string][]drs.AccessMethod{objectID: replacement}))
	replacement = []drs.AccessMethod{original, payloadConflict}
	assertPreserved("single payload update", db.UpdateObjectAccessMethods(ctx, objectID, replacement))
	assertPreserved("bulk payload update", db.BulkUpdateAccessMethods(ctx, map[string][]drs.AccessMethod{objectID: replacement}))
}

func TestSqliteAccessMethodGeneratedIDCollisionRejected(t *testing.T) {
	db, err := NewSqliteDB(":memory:", nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })

	ctx := context.Background()
	now := time.Now().UTC()
	generatedURL := "https://example.test/generated"
	generatedID := objects.AccessMethodID(string(drs.AccessMethodTypeHttps), generatedURL)
	methods := []drs.AccessMethod{
		{AccessId: &generatedID, AccessUrl: &drs.AccessURL{Url: "https://example.test/explicit"}, Type: drs.AccessMethodTypeHttps},
		{AccessUrl: &drs.AccessURL{Url: generatedURL}, Type: drs.AccessMethodTypeHttps},
	}
	err = db.RegisterObjects(ctx, []drs.DrsObject{{Id: "generated-id-collision", CreatedTime: now, UpdatedTime: &now, AccessMethods: &methods}})
	if !errors.Is(err, errorapi.ErrInvalidInput) {
		t.Fatalf("generated ID collision error = %v, want ErrInvalidInput", err)
	}
	if _, getErr := db.GetObject(ctx, "generated-id-collision"); !errors.Is(getErr, errorapi.ErrObjectNotFound) {
		t.Fatalf("generated ID collision left object, GetObject error = %v", getErr)
	}
}

func TestSqliteDB_ExplicitTxLockEnablesForeignKeysAndCascade(t *testing.T) {
	dsn := filepath.Join(t.TempDir(), "txlock.db") + "?_txlock=immediate"
	db, err := NewSqliteDB(dsn, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	var enabled int
	if err := db.DB().QueryRow(`PRAGMA foreign_keys`).Scan(&enabled); err != nil {
		t.Fatal(err)
	}
	if enabled != 1 {
		t.Fatalf("PRAGMA foreign_keys = %d, want 1", enabled)
	}

	now := time.Now().UTC()
	object := drs.DrsObject{
		Id:          "txlock-cascade",
		CreatedTime: now,
		UpdatedTime: &now,
		Checksums:   []drs.Checksum{{Type: "sha256", Checksum: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}},
		AccessMethods: func() *[]drs.AccessMethod {
			methods := []drs.AccessMethod{{Type: "s3", AccessUrl: &drs.AccessURL{Url: "s3://bucket/key"}}}
			return &methods
		}(),
	}
	if err := db.RegisterObjects(context.Background(), []drs.DrsObject{object}); err != nil {
		t.Fatalf("RegisterObjects: %v", err)
	}
	if err := db.DeleteObject(context.Background(), object.Id); err != nil {
		t.Fatalf("DeleteObject: %v", err)
	}
	for _, table := range []string{"drs_object_access_method", "drs_object_checksum"} {
		var remaining int
		if err := db.DB().QueryRow(`SELECT count(*) FROM `+table+` WHERE object_id = ?`, object.Id).Scan(&remaining); err != nil {
			t.Fatalf("count %s rows: %v", table, err)
		}
		if remaining != 0 {
			t.Fatalf("delete left %d orphan %s rows", remaining, table)
		}
	}
}

func sqliteFullAccessMethods() []drs.AccessMethod {
	accessID := "protected-http"
	headers := []string{"Authorization: Bearer synthetic-token", "X-Trace: persistence"}
	url := "https://example.test/protected"
	region := "us-west-2"
	cloud := "aws"
	available := false
	drsObjectID := "object-for-authz"
	bearerIssuers := []string{"https://issuer.example"}
	passportIssuers := []string{"passport-issuer"}
	supportedTypes := []drs.AuthorizationsSupportedTypes{drs.BearerAuth, drs.PassportAuth}
	return []drs.AccessMethod{
		{
			AccessId: &accessID,
			AccessUrl: &drs.AccessURL{
				Url:     url,
				Headers: &headers,
			},
			Authorizations: &drs.Authorizations{
				BearerAuthIssuers:   &bearerIssuers,
				DrsObjectId:         &drsObjectID,
				PassportAuthIssuers: &passportIssuers,
				SupportedTypes:      &supportedTypes,
			},
			Available: &available,
			Cloud:     &cloud,
			Region:    &region,
			Type:      drs.AccessMethodTypeHttps,
		},
		{AccessId: func() *string { v := "resolver-only"; return &v }(), Type: drs.AccessMethodTypeHttps},
		{AccessId: func() *string { v := "resolver-only-second"; return &v }(), Type: drs.AccessMethodTypeHttps},
	}
}

func assertSqliteAccessMethodsEqual(t *testing.T, got *[]drs.AccessMethod, want []drs.AccessMethod) {
	t.Helper()
	if got == nil {
		t.Fatalf("access methods are nil, want %#v", want)
	}
	if len(*got) != len(want) {
		t.Fatalf("access method count = %d, want %d (%#v)", len(*got), len(want), *got)
	}
	byID := make(map[string]drs.AccessMethod, len(*got))
	for _, method := range *got {
		if method.AccessId == nil {
			t.Fatalf("hydrated method %#v has no access ID", method)
		}
		byID[*method.AccessId] = method
	}
	for _, expected := range want {
		if expected.AccessId == nil {
			t.Fatalf("test method %#v has no access ID", expected)
		}
		actual, ok := byID[*expected.AccessId]
		if !ok {
			t.Fatalf("missing access ID %q in %#v", *expected.AccessId, byID)
		}
		if !reflect.DeepEqual(actual, expected) {
			t.Errorf("access method %q = %#v, want %#v", *expected.AccessId, actual, expected)
		}
	}
}
