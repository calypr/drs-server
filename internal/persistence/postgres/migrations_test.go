package postgres

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/calypr/syfon/internal/persistence/credentialcipher"
)

func TestValidateAppliedMigrationsRequiresContiguousPrefix(t *testing.T) {
	if err := validateAppliedMigrations(map[int64]struct{}{1: {}}); err != nil {
		t.Fatalf("contiguous migrations rejected: %v", err)
	}
	if err := validateAppliedMigrations(map[int64]struct{}{2: {}}); err == nil {
		t.Fatal("migration gap was accepted")
	}
}

func TestPostgresProductionOpenUsesPreparedSchemaAndPoolLimits(t *testing.T) {
	dsn := os.Getenv("SYFON_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("SYFON_TEST_POSTGRES_DSN is not configured")
	}
	preparePostgresCheckSchemaTest(t, dsn)
	database, err := NewPostgresDBWithOptions(dsn, nil, OpenOptions{
		SchemaMode:            SchemaModeCheck,
		MaxOpenConnections:    3,
		MaxIdleConnections:    2,
		ConnectionMaxLifetime: time.Minute,
		ConnectionMaxIdleTime: 30 * time.Second,
		PingTimeout:           5 * time.Second,
	})
	if err != nil {
		t.Fatalf("NewPostgresDBWithOptions() error = %v", err)
	}
	defer database.Close()
	if got := database.DB().Stats().MaxOpenConnections; got != 3 {
		t.Fatalf("MaxOpenConnections = %d, want 3", got)
	}
}

func TestSchemaMigrationChecksumsAreStableAndUnique(t *testing.T) {
	seen := make(map[string]struct{}, len(schemaMigrations))
	for _, migration := range schemaMigrations {
		if migration.Version <= 0 || migration.Name == "" || migration.Checksum == "" {
			t.Fatalf("invalid migration metadata: %+v", migration)
		}
		if _, exists := seen[migration.Checksum]; exists {
			t.Fatalf("duplicate migration checksum for version %d", migration.Version)
		}
		seen[migration.Checksum] = struct{}{}
	}
}

func TestPostgresCheckSchemaIntegrationIsReadOnly(t *testing.T) {
	dsn := os.Getenv("SYFON_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("SYFON_TEST_POSTGRES_DSN is not configured")
	}
	preparePostgresCheckSchemaTest(t, dsn)
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	var before int
	if err := db.QueryRowContext(context.Background(), `SELECT count(*) FROM syfon_schema_migrations`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	if err := CheckSchema(context.Background(), db); err != nil {
		t.Fatalf("CheckSchema() error = %v", err)
	}
	var after int
	if err := db.QueryRowContext(context.Background(), `SELECT count(*) FROM syfon_schema_migrations`).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if after != before {
		t.Fatalf("CheckSchema changed the migration ledger from %d rows to %d", before, after)
	}
}

func preparePostgresCheckSchemaTest(t *testing.T, dsn string) {
	t.Helper()
	t.Setenv(credentialcipher.CredentialLocalKeyFileEnv, filepath.Join(t.TempDir(), "credential.key"))
	database, err := NewPostgresDB(dsn, nil)
	if err != nil {
		t.Fatalf("prepare PostgreSQL schema: %v", err)
	}
	if err := database.Close(); err != nil {
		t.Fatalf("close schema preparation database: %v", err)
	}

	db, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatalf("open prepared PostgreSQL schema: %v", err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS syfon_schema_migrations (
		version BIGINT PRIMARY KEY,
		name TEXT NOT NULL,
		checksum TEXT NOT NULL,
		applied_at TIMESTAMPTZ NOT NULL DEFAULT now()
	)`); err != nil {
		t.Fatalf("create migration ledger: %v", err)
	}
	if _, err := db.Exec(`CREATE INDEX IF NOT EXISTS drs_object_access_method_url_object_id_idx ON drs_object_access_method(url, object_id)`); err != nil {
		t.Fatalf("create required schema index: %v", err)
	}
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS multipart_completion_receipt (
		upload_id TEXT PRIMARY KEY,
		authorization_json JSONB NOT NULL,
		parts_fingerprint TEXT NOT NULL,
		completed_location TEXT NOT NULL,
		completed_time TIMESTAMPTZ NOT NULL
	)`); err != nil {
		t.Fatalf("create multipart completion receipt table: %v", err)
	}
	for _, migration := range schemaMigrations {
		if _, err := db.Exec(`INSERT INTO syfon_schema_migrations (version, name, checksum) VALUES ($1, $2, $3) ON CONFLICT (version) DO UPDATE SET name = EXCLUDED.name, checksum = EXCLUDED.checksum`, migration.Version, migration.Name, migration.Checksum); err != nil {
			t.Fatalf("record migration %d: %v", migration.Version, err)
		}
	}
}
