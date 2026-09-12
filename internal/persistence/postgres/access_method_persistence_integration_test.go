package postgres_test

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/calypr/syfon/apigen/drs"
	"github.com/calypr/syfon/internal/persistence/credentialcipher"
	postgresdb "github.com/calypr/syfon/internal/persistence/postgres"
	"github.com/google/uuid"
)

func TestPostgresAccessMethodsPreserveFullContractAcrossWrites(t *testing.T) {
	db := openPostgresTestStore(t)
	ctx := context.Background()
	methods := postgresFullAccessMethods()
	now := time.Now().UTC()
	register := func(id string) drs.DrsObject {
		return drs.DrsObject{Id: id, CreatedTime: now, UpdatedTime: &now, AccessMethods: &methods}
	}
	ids := []string{"pg-registered", "pg-replaced", "pg-updated", "pg-bulk"}
	t.Cleanup(func() {
		for _, id := range ids {
			_ = db.DeleteObject(ctx, id)
		}
	})
	if err := db.RegisterObjects(ctx, []drs.DrsObject{register(ids[0]), register(ids[1]), register(ids[2]), register(ids[3])}); err != nil {
		t.Fatalf("RegisterObjects: %v", err)
	}
	if err := db.ReplaceObjects(ctx, []drs.DrsObject{register(ids[1])}); err != nil {
		t.Fatalf("ReplaceObjects: %v", err)
	}
	if err := db.UpdateObjectAccessMethods(ctx, ids[2], methods); err != nil {
		t.Fatalf("UpdateObjectAccessMethods: %v", err)
	}
	if err := db.BulkUpdateAccessMethods(ctx, map[string][]drs.AccessMethod{ids[3]: methods}); err != nil {
		t.Fatalf("BulkUpdateAccessMethods: %v", err)
	}

	for _, id := range ids {
		got, err := db.GetObject(ctx, id)
		if err != nil {
			t.Fatalf("GetObject(%q): %v", id, err)
		}
		assertPostgresAccessMethodsEqual(t, got.AccessMethods, methods)
		bulk, err := db.GetBulkObjects(ctx, []string{id})
		if err != nil {
			t.Fatalf("GetBulkObjects(%q): %v", id, err)
		}
		if len(bulk) != 1 {
			t.Fatalf("GetBulkObjects(%q) returned %d objects", id, len(bulk))
		}
		assertPostgresAccessMethodsEqual(t, bulk[0].AccessMethods, methods)
	}
}

func TestPostgresAccessMethodsLegacyRowsGetStableGeneratedAccessID(t *testing.T) {
	db := openPostgresTestStore(t)
	ctx := context.Background()
	now := time.Now().UTC()
	objectID := "pg-legacy-access-" + uuid.NewString()
	t.Cleanup(func() { _ = db.DeleteObject(ctx, objectID) })
	if _, err := db.DB().ExecContext(ctx, `
		INSERT INTO drs_object (id, size, created_time, updated_time, name, version, description)
		VALUES ($1, 1, $2, $2, 'legacy', '1', '')
		ON CONFLICT (id) DO NOTHING`, objectID, now); err != nil {
		t.Fatal(err)
	}
	if _, err := db.DB().ExecContext(ctx, `
		INSERT INTO drs_object_access_method (object_id, url, type)
		VALUES ($1, 'https://example.test/legacy', 'https')`, objectID); err != nil {
		t.Fatal(err)
	}
	got, err := db.GetObject(ctx, objectID)
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
}

func TestPostgresAccessMethodColumnMigratesLegacySchemaOnReopen(t *testing.T) {
	dsn := os.Getenv("SYFON_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("SYFON_TEST_POSTGRES_DSN is not configured")
	}
	t.Setenv(credentialcipher.CredentialLocalKeyFileEnv, filepath.Join(t.TempDir(), "credential.key"))
	raw, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatal(err)
	}
	if err := raw.PingContext(context.Background()); err != nil {
		_ = raw.Close()
		t.Fatal(err)
	}
	schema := "syfon_legacy_access_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err := raw.Exec(`CREATE SCHEMA ` + schema); err != nil {
		_ = raw.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = raw.Exec(`DROP SCHEMA ` + schema + ` CASCADE`)
		_ = raw.Close()
	})
	legacyObjectID := "legacy-reopen"
	if _, err := raw.Exec(fmt.Sprintf(`CREATE TABLE %s.drs_object (id TEXT PRIMARY KEY, size BIGINT, created_time TIMESTAMPTZ, updated_time TIMESTAMPTZ, name TEXT, version TEXT, description TEXT)`, schema)); err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(fmt.Sprintf(`CREATE TABLE %s.drs_object_access_method (object_id TEXT, url TEXT, type TEXT)`, schema)); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if _, err := raw.ExecContext(context.Background(), fmt.Sprintf(`INSERT INTO %s.drs_object (id, size, created_time, updated_time, name, version, description) VALUES ($1, 1, $2, $2, 'legacy', '1', '')`, schema), legacyObjectID, now); err != nil {
		t.Fatal(err)
	}
	if _, err := raw.ExecContext(context.Background(), fmt.Sprintf(`INSERT INTO %s.drs_object_access_method (object_id, url, type) VALUES ($1, 'https://example.test/legacy-pg', 'https')`, schema), legacyObjectID); err != nil {
		t.Fatal(err)
	}
	legacyStore, err := postgresdb.NewPostgresDB(postgresTestSchemaDSN(t, dsn, schema), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = legacyStore.Close() })
	var columnCount int
	if err := legacyStore.DB().QueryRowContext(context.Background(), `
		SELECT count(*)
		FROM information_schema.columns
		WHERE table_schema = current_schema() AND table_name = 'drs_object_access_method' AND column_name = 'access_method_json'`).Scan(&columnCount); err != nil {
		t.Fatal(err)
	}
	if columnCount != 1 {
		t.Fatalf("legacy reopen access_method_json columns = %d, want 1", columnCount)
	}
	got, err := legacyStore.GetObject(context.Background(), legacyObjectID)
	if err != nil {
		t.Fatal(err)
	}
	if got.AccessMethods == nil || len(*got.AccessMethods) != 1 || (*got.AccessMethods)[0].AccessId == nil || *(*got.AccessMethods)[0].AccessId != "https-a8895cd23f18c562a24d5bb3" {
		t.Fatalf("legacy reopen access methods = %#v", got.AccessMethods)
	}
}

func postgresFullAccessMethods() []drs.AccessMethod {
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
	idOnly := "resolver-only"
	idOnlySecond := "resolver-only-second"
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
		{AccessId: &idOnly, Type: drs.AccessMethodTypeHttps},
		{AccessId: &idOnlySecond, Type: drs.AccessMethodTypeHttps},
	}
}

func assertPostgresAccessMethodsEqual(t *testing.T, got *[]drs.AccessMethod, want []drs.AccessMethod) {
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
		actual, ok := byID[*expected.AccessId]
		if !ok {
			t.Fatalf("missing access ID %q in %#v", *expected.AccessId, byID)
		}
		if !reflect.DeepEqual(actual, expected) {
			t.Errorf("access method %q = %#v, want %#v", *expected.AccessId, actual, expected)
		}
	}
}
