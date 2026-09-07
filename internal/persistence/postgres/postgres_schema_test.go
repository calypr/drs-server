package postgres

import (
	"context"
	"regexp"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestSchemaEnsurers(t *testing.T) {
	t.Run("ensureObjectSchema", func(t *testing.T) {
		pg, mock, rawDB := newMockPostgresDB(t)
		defer rawDB.Close()

		mock.ExpectExec("CREATE TABLE IF NOT EXISTS drs_object").WillReturnResult(sqlmock.NewResult(0, 0))
		mock.ExpectExec(regexp.QuoteMeta("ALTER TABLE drs_object DROP COLUMN IF EXISTS file_name")).WillReturnResult(sqlmock.NewResult(0, 0))
		mock.ExpectExec("CREATE TABLE IF NOT EXISTS drs_object_access_method").WillReturnResult(sqlmock.NewResult(0, 0))
		mock.ExpectExec("CREATE TABLE IF NOT EXISTS drs_object_controlled_access").WillReturnResult(sqlmock.NewResult(0, 0))
		mock.ExpectExec("CREATE TABLE IF NOT EXISTS drs_object_checksum").WillReturnResult(sqlmock.NewResult(0, 0))
		mock.ExpectExec("CREATE TABLE IF NOT EXISTS drs_object_alias").WillReturnResult(sqlmock.NewResult(0, 0))
		mock.ExpectExec("CREATE TABLE IF NOT EXISTS drs_object_name_alias").WillReturnResult(sqlmock.NewResult(0, 0))
		mock.ExpectExec("CREATE TABLE IF NOT EXISTS drs_object_read_policy").WillReturnResult(sqlmock.NewResult(0, 0))
		mock.ExpectExec("DROP TABLE IF EXISTS drs_object_browse_index").WillReturnResult(sqlmock.NewResult(0, 0))
		mock.ExpectExec(regexp.QuoteMeta("CREATE INDEX IF NOT EXISTS drs_object_access_method_object_id_idx ON drs_object_access_method(object_id)")).
			WillReturnResult(sqlmock.NewResult(0, 0))
		mock.ExpectExec(regexp.QuoteMeta("CREATE INDEX IF NOT EXISTS drs_object_checksum_object_id_idx ON drs_object_checksum(object_id)")).
			WillReturnResult(sqlmock.NewResult(0, 0))
		mock.ExpectExec(regexp.QuoteMeta("CREATE INDEX IF NOT EXISTS drs_object_checksum_checksum_idx ON drs_object_checksum(checksum)")).
			WillReturnResult(sqlmock.NewResult(0, 0))
		mock.ExpectExec(regexp.QuoteMeta("CREATE INDEX IF NOT EXISTS drs_object_checksum_checksum_type_object_id_idx ON drs_object_checksum(checksum, type, object_id)")).
			WillReturnResult(sqlmock.NewResult(0, 0))
		mock.ExpectExec(regexp.QuoteMeta(`CREATE INDEX IF NOT EXISTS drs_object_checksum_sha256_identity_idx
		  ON drs_object_checksum(
		    (replace(lower(trim(type)), '-', '')),
		    (replace(lower(trim(checksum)), 'sha256:', '')),
		    object_id
		  )`)).
			WillReturnResult(sqlmock.NewResult(0, 0))
		mock.ExpectExec(regexp.QuoteMeta("CREATE INDEX IF NOT EXISTS drs_object_controlled_access_object_id_idx ON drs_object_controlled_access(object_id)")).
			WillReturnResult(sqlmock.NewResult(0, 0))
		mock.ExpectExec(regexp.QuoteMeta("CREATE INDEX IF NOT EXISTS drs_object_controlled_access_resource_idx ON drs_object_controlled_access(resource)")).
			WillReturnResult(sqlmock.NewResult(0, 0))
		mock.ExpectExec(regexp.QuoteMeta("CREATE INDEX IF NOT EXISTS drs_object_controlled_access_resource_object_id_idx ON drs_object_controlled_access(resource, object_id)")).
			WillReturnResult(sqlmock.NewResult(0, 0))
		mock.ExpectExec(regexp.QuoteMeta("CREATE INDEX IF NOT EXISTS drs_object_controlled_access_object_id_resource_idx ON drs_object_controlled_access(object_id, resource)")).
			WillReturnResult(sqlmock.NewResult(0, 0))
		mock.ExpectExec(regexp.QuoteMeta("CREATE INDEX IF NOT EXISTS drs_object_alias_object_id_idx ON drs_object_alias(object_id)")).
			WillReturnResult(sqlmock.NewResult(0, 0))
		mock.ExpectExec(regexp.QuoteMeta("CREATE INDEX IF NOT EXISTS drs_object_name_alias_object_id_idx ON drs_object_name_alias(object_id)")).
			WillReturnResult(sqlmock.NewResult(0, 0))
		mock.ExpectQuery("information_schema\\.columns").
			WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))

		if err := pg.ensureObjectSchema(); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("validateLegacyAccessMethodScopes_legacyColumnsWithoutScopedRows", func(t *testing.T) {
		pg, mock, rawDB := newMockPostgresDB(t)
		defer rawDB.Close()

		mock.ExpectQuery("information_schema\\.columns").
			WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(2))
		mock.ExpectQuery("SELECT COUNT\\(\\*\\)").
			WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))

		if err := pg.validateLegacyAccessMethodScopes(context.Background()); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("validateLegacyAccessMethodScopes_rejectsMismatchedScopedRows", func(t *testing.T) {
		pg, mock, rawDB := newMockPostgresDB(t)
		defer rawDB.Close()

		mock.ExpectQuery("information_schema\\.columns").
			WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(2))
		mock.ExpectQuery("SELECT COUNT\\(\\*\\)").
			WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(3))

		err := pg.validateLegacyAccessMethodScopes(context.Background())
		if err == nil {
			t.Fatal("expected mismatch validation error")
		}
	})

	t.Run("ensureS3CredentialSchema", func(t *testing.T) {
		pg, mock, rawDB := newMockPostgresDB(t)
		defer rawDB.Close()

		for _, query := range []string{
			`CREATE TABLE IF NOT EXISTS s3_credential (
			credential_id TEXT PRIMARY KEY,
			bucket TEXT NOT NULL,
			provider TEXT NOT NULL DEFAULT 's3',
			region TEXT,
			access_key TEXT,
			secret_key TEXT,
			endpoint TEXT
		)`,
			`ALTER TABLE s3_credential ADD COLUMN IF NOT EXISTS credential_id TEXT`,
			`ALTER TABLE s3_credential ADD COLUMN IF NOT EXISTS provider TEXT NOT NULL DEFAULT 's3'`,
			`UPDATE s3_credential SET credential_id = bucket WHERE COALESCE(BTRIM(credential_id), '') = ''`,
			`ALTER TABLE s3_credential ALTER COLUMN credential_id SET NOT NULL`,
			`ALTER TABLE s3_credential DROP CONSTRAINT IF EXISTS s3_credential_pkey`,
			`ALTER TABLE s3_credential ADD PRIMARY KEY (credential_id)`,
			`CREATE INDEX IF NOT EXISTS idx_s3_credential_bucket ON s3_credential(bucket)`,
			`CREATE OR REPLACE FUNCTION enforce_s3_credential_unique_bucket() RETURNS trigger AS $$
		BEGIN
			IF EXISTS (
				SELECT 1
				FROM s3_credential
				WHERE bucket = NEW.bucket AND credential_id <> NEW.credential_id
			) THEN
				RAISE EXCEPTION 'physical bucket "%" is already configured under another credential', NEW.bucket;
			END IF;
			RETURN NEW;
		END;
		$$ LANGUAGE plpgsql`,
			`DROP TRIGGER IF EXISTS s3_credential_unique_bucket_trigger ON s3_credential`,
			`CREATE TRIGGER s3_credential_unique_bucket_trigger
		BEFORE INSERT OR UPDATE OF bucket, credential_id ON s3_credential
		FOR EACH ROW
		EXECUTE FUNCTION enforce_s3_credential_unique_bucket()`,
		} {
			mock.ExpectExec(regexp.QuoteMeta(query)).WillReturnResult(sqlmock.NewResult(0, 0))
		}

		if err := pg.ensureS3CredentialSchema(); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("ensureBucketScopeSchema", func(t *testing.T) {
		pg, mock, rawDB := newMockPostgresDB(t)
		defer rawDB.Close()

		for _, query := range []string{
			`CREATE TABLE IF NOT EXISTS bucket_scope (
			organization TEXT NOT NULL,
			project_id TEXT NOT NULL,
			credential_id TEXT NOT NULL,
			bucket TEXT NOT NULL,
			path_prefix TEXT NULL,
			PRIMARY KEY (organization, project_id)
		)`,
			`ALTER TABLE bucket_scope ADD COLUMN IF NOT EXISTS credential_id TEXT`,
			`UPDATE bucket_scope SET credential_id = bucket WHERE COALESCE(BTRIM(credential_id), '') = ''`,
			`ALTER TABLE bucket_scope ALTER COLUMN credential_id SET NOT NULL`,
			`ALTER TABLE bucket_scope ADD COLUMN IF NOT EXISTS bucket TEXT`,
			`UPDATE bucket_scope SET bucket = credential_id WHERE COALESCE(BTRIM(bucket), '') = ''`,
			`ALTER TABLE bucket_scope ALTER COLUMN bucket SET NOT NULL`,
			`CREATE INDEX IF NOT EXISTS idx_bucket_scope_credential_id ON bucket_scope(credential_id)`,
			`CREATE INDEX IF NOT EXISTS idx_bucket_scope_bucket ON bucket_scope(bucket)`,
		} {
			mock.ExpectExec(regexp.QuoteMeta(query)).WillReturnResult(sqlmock.NewResult(0, 0))
		}

		if err := pg.ensureBucketScopeSchema(); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("ensureLFSPendingSchema", func(t *testing.T) {
		pg, mock, rawDB := newMockPostgresDB(t)
		defer rawDB.Close()

		mock.ExpectExec("CREATE TABLE IF NOT EXISTS lfs_pending_metadata").WillReturnResult(sqlmock.NewResult(0, 0))
		mock.ExpectExec("CREATE INDEX IF NOT EXISTS idx_lfs_pending_metadata_expires").WillReturnResult(sqlmock.NewResult(0, 0))
		mock.ExpectExec("CREATE INDEX IF NOT EXISTS idx_lfs_pending_metadata_created").WillReturnResult(sqlmock.NewResult(0, 0))

		if err := pg.ensureLFSPendingSchema(); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("ensureObjectUsageSchema", func(t *testing.T) {
		pg, mock, rawDB := newMockPostgresDB(t)
		defer rawDB.Close()

		mock.ExpectExec("CREATE TABLE IF NOT EXISTS object_usage").WillReturnResult(sqlmock.NewResult(0, 0))
		mock.ExpectExec("CREATE INDEX IF NOT EXISTS idx_object_usage_last_download_time").WillReturnResult(sqlmock.NewResult(0, 0))
		mock.ExpectExec("CREATE INDEX IF NOT EXISTS idx_object_usage_last_download_time_object_id").WillReturnResult(sqlmock.NewResult(0, 0))
		mock.ExpectExec("CREATE INDEX IF NOT EXISTS idx_object_usage_last_upload_time").WillReturnResult(sqlmock.NewResult(0, 0))

		if err := pg.ensureObjectUsageSchema(); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("ensurePendingObjectUsageSchema", func(t *testing.T) {
		pg, mock, rawDB := newMockPostgresDB(t)
		defer rawDB.Close()

		mock.ExpectExec("CREATE TABLE IF NOT EXISTS object_usage_event").WillReturnResult(sqlmock.NewResult(0, 0))
		mock.ExpectExec("CREATE INDEX IF NOT EXISTS idx_object_usage_event_object_id").WillReturnResult(sqlmock.NewResult(0, 0))
		mock.ExpectExec("CREATE INDEX IF NOT EXISTS idx_object_usage_event_event_time").WillReturnResult(sqlmock.NewResult(0, 0))

		if err := pg.ensurePendingObjectUsageSchema(); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

}
