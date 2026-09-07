package postgres

import (
	"context"
	"fmt"
)

func (db *PostgresDB) ensureObjectSchema() error {
	queries, err := objectSchemaStatements()
	if err != nil {
		return fmt.Errorf("failed to load object schema: %w", err)
	}
	for _, q := range queries {
		if _, err := db.db.Exec(q); err != nil {
			return fmt.Errorf("failed to initialize object schema: %w", err)
		}
	}
	if err := db.validateLegacyAccessMethodScopes(context.Background()); err != nil {
		return fmt.Errorf("legacy object scope validation failed: %w", err)
	}
	return nil
}

func (db *PostgresDB) validateLegacyAccessMethodScopes(ctx context.Context) error {
	hasLegacyColumns, err := db.hasLegacyAccessMethodScopeColumns(ctx)
	if err != nil {
		return err
	}
	if !hasLegacyColumns {
		return nil
	}
	var mismatches int
	err = db.db.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM (
			SELECT DISTINCT
				am.object_id,
				CASE
					WHEN btrim(COALESCE(am.project, '')) = ''
						THEN '/organization/' || btrim(am.org)
					ELSE '/organization/' || btrim(am.org) || '/project/' || btrim(am.project)
				END AS resource
			FROM drs_object_access_method am
			INNER JOIN drs_object o ON o.id = am.object_id
			WHERE btrim(COALESCE(am.org, '')) <> ''
		) legacy_scope
		WHERE NOT EXISTS (
			SELECT 1
			FROM drs_object_controlled_access ca
			WHERE ca.object_id = legacy_scope.object_id
				AND ca.resource = legacy_scope.resource
		)
	`).Scan(&mismatches)
	if err != nil {
		return err
	}
	if mismatches > 0 {
		return fmt.Errorf("%d scoped drs_object_access_method rows are missing matching drs_object_controlled_access entries", mismatches)
	}
	return nil
}

func (db *PostgresDB) hasLegacyAccessMethodScopeColumns(ctx context.Context) (bool, error) {
	var count int
	err := db.db.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM information_schema.columns
		WHERE table_name = 'drs_object_access_method'
			AND column_name IN ('org', 'project')
			AND table_schema = ANY (current_schemas(false))
	`).Scan(&count)
	if err != nil {
		return false, err
	}
	return count == 2, nil
}

func (db *PostgresDB) ensureS3CredentialSchema() error {
	queries := []string{
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
	}
	for _, q := range queries {
		if _, err := db.db.Exec(q); err != nil {
			return fmt.Errorf("failed to initialize s3_credential credential identity schema: %w", err)
		}
	}
	return nil
}

func (db *PostgresDB) ensureBucketScopeSchema() error {
	queries := []string{
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
	}
	for _, q := range queries {
		if _, err := db.db.Exec(q); err != nil {
			return fmt.Errorf("failed to initialize bucket scope schema: %w", err)
		}
	}
	return nil
}

func (db *PostgresDB) ensureLFSPendingSchema() error {
	queries := []string{
		`CREATE TABLE IF NOT EXISTS lfs_pending_metadata (
			oid TEXT PRIMARY KEY,
			candidate_json JSONB NOT NULL,
			created_time TIMESTAMPTZ NOT NULL,
			expires_time TIMESTAMPTZ NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_lfs_pending_metadata_expires ON lfs_pending_metadata(expires_time)`,
		`CREATE INDEX IF NOT EXISTS idx_lfs_pending_metadata_created ON lfs_pending_metadata(created_time)`,
	}
	for _, q := range queries {
		if _, err := db.db.Exec(q); err != nil {
			return fmt.Errorf("failed to initialize lfs pending metadata schema: %w", err)
		}
	}
	return nil
}

func (db *PostgresDB) ensureObjectUsageSchema() error {
	queries := []string{
		`CREATE TABLE IF NOT EXISTS object_usage (
			object_id TEXT PRIMARY KEY REFERENCES drs_object(id) ON DELETE CASCADE,
			upload_count BIGINT NOT NULL DEFAULT 0,
			download_count BIGINT NOT NULL DEFAULT 0,
			last_upload_time TIMESTAMPTZ NULL,
			last_download_time TIMESTAMPTZ NULL,
			updated_time TIMESTAMPTZ NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_object_usage_last_download_time ON object_usage(last_download_time)`,
		`CREATE INDEX IF NOT EXISTS idx_object_usage_last_download_time_object_id ON object_usage(last_download_time, object_id)`,
		`CREATE INDEX IF NOT EXISTS idx_object_usage_last_upload_time ON object_usage(last_upload_time)`,
	}
	for _, q := range queries {
		if _, err := db.db.Exec(q); err != nil {
			return fmt.Errorf("failed to initialize object usage schema: %w", err)
		}
	}
	return nil
}

func (db *PostgresDB) ensurePendingObjectUsageSchema() error {
	queries := []string{
		`CREATE TABLE IF NOT EXISTS object_usage_event (
			id BIGSERIAL PRIMARY KEY,
			object_id TEXT NOT NULL,
			event_type TEXT NOT NULL CHECK (event_type IN ('upload','download')),
			event_time TIMESTAMPTZ NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_object_usage_event_object_id ON object_usage_event(object_id)`,
		`CREATE INDEX IF NOT EXISTS idx_object_usage_event_event_time ON object_usage_event(event_time)`,
	}
	for _, q := range queries {
		if _, err := db.db.Exec(q); err != nil {
			return fmt.Errorf("failed to initialize object usage event schema: %w", err)
		}
	}
	return nil
}

func (db *PostgresDB) ensureTransferAttributionSchema() error {
	queries := []string{
		`CREATE TABLE IF NOT EXISTS transfer_attribution_event (
			event_id TEXT PRIMARY KEY,
			access_grant_id TEXT NOT NULL DEFAULT '',
			event_type TEXT NOT NULL CHECK (event_type IN ('access_issued')),
			direction TEXT NOT NULL DEFAULT 'download' CHECK (direction IN ('download','upload')),
			event_time TIMESTAMPTZ NOT NULL,
			request_id TEXT NOT NULL DEFAULT '',
			object_id TEXT NOT NULL DEFAULT '',
			sha256 TEXT NOT NULL DEFAULT '',
			object_size BIGINT NOT NULL DEFAULT 0,
			organization TEXT NOT NULL DEFAULT '',
			project TEXT NOT NULL DEFAULT '',
			access_id TEXT NOT NULL DEFAULT '',
			provider TEXT NOT NULL DEFAULT '',
			bucket TEXT NOT NULL DEFAULT '',
			storage_url TEXT NOT NULL DEFAULT '',
			range_start BIGINT NULL,
			range_end BIGINT NULL,
			bytes_requested BIGINT NOT NULL DEFAULT 0,
			bytes_completed BIGINT NOT NULL DEFAULT 0,
			actor_email TEXT NOT NULL DEFAULT '',
			actor_subject TEXT NOT NULL DEFAULT '',
			auth_mode TEXT NOT NULL DEFAULT '',
			client_name TEXT NOT NULL DEFAULT '',
			client_version TEXT NOT NULL DEFAULT '',
			transfer_session_id TEXT NOT NULL DEFAULT ''
		)`,
		`CREATE TABLE IF NOT EXISTS access_grant (
			access_grant_id TEXT PRIMARY KEY,
			first_issued_at TIMESTAMPTZ NOT NULL,
			last_issued_at TIMESTAMPTZ NOT NULL,
			issue_count BIGINT NOT NULL DEFAULT 0,
			object_id TEXT NOT NULL DEFAULT '',
			sha256 TEXT NOT NULL DEFAULT '',
			object_size BIGINT NOT NULL DEFAULT 0,
			organization TEXT NOT NULL DEFAULT '',
			project TEXT NOT NULL DEFAULT '',
			access_id TEXT NOT NULL DEFAULT '',
			provider TEXT NOT NULL DEFAULT '',
			bucket TEXT NOT NULL DEFAULT '',
			storage_url TEXT NOT NULL DEFAULT '',
			actor_email TEXT NOT NULL DEFAULT '',
			actor_subject TEXT NOT NULL DEFAULT '',
			auth_mode TEXT NOT NULL DEFAULT ''
		)`,
		`CREATE TABLE IF NOT EXISTS provider_transfer_event (
			provider_event_id TEXT PRIMARY KEY,
			access_grant_id TEXT NOT NULL DEFAULT '',
			direction TEXT NOT NULL CHECK (direction IN ('download','upload')),
			event_time TIMESTAMPTZ NOT NULL,
			request_id TEXT NOT NULL DEFAULT '',
			provider_request_id TEXT NOT NULL DEFAULT '',
			object_id TEXT NOT NULL DEFAULT '',
			sha256 TEXT NOT NULL DEFAULT '',
			object_size BIGINT NOT NULL DEFAULT 0,
			organization TEXT NOT NULL DEFAULT '',
			project TEXT NOT NULL DEFAULT '',
			access_id TEXT NOT NULL DEFAULT '',
			provider TEXT NOT NULL DEFAULT '',
			bucket TEXT NOT NULL DEFAULT '',
			object_key TEXT NOT NULL DEFAULT '',
			storage_url TEXT NOT NULL DEFAULT '',
			range_start BIGINT NULL,
			range_end BIGINT NULL,
			bytes_transferred BIGINT NOT NULL DEFAULT 0,
			http_method TEXT NOT NULL DEFAULT '',
			http_status INTEGER NOT NULL DEFAULT 0,
			requester_principal TEXT NOT NULL DEFAULT '',
			source_ip TEXT NOT NULL DEFAULT '',
			user_agent TEXT NOT NULL DEFAULT '',
			raw_event_ref TEXT NOT NULL DEFAULT '',
			actor_email TEXT NOT NULL DEFAULT '',
			actor_subject TEXT NOT NULL DEFAULT '',
			auth_mode TEXT NOT NULL DEFAULT '',
			reconciliation_status TEXT NOT NULL DEFAULT 'unmatched' CHECK (reconciliation_status IN ('matched','ambiguous','unmatched'))
		)`,
		`ALTER TABLE transfer_attribution_event ADD COLUMN IF NOT EXISTS access_grant_id TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE transfer_attribution_event ADD COLUMN IF NOT EXISTS direction TEXT NOT NULL DEFAULT 'download'`,
		`CREATE INDEX IF NOT EXISTS idx_transfer_attr_scope_time ON transfer_attribution_event(organization, project, event_type, event_time)`,
		`CREATE INDEX IF NOT EXISTS idx_transfer_attr_scope_event_time ON transfer_attribution_event(organization, project, event_time)`,
		`CREATE INDEX IF NOT EXISTS idx_transfer_attr_direction_time ON transfer_attribution_event(direction, event_time)`,
		`CREATE INDEX IF NOT EXISTS idx_transfer_attr_actor_time ON transfer_attribution_event(actor_email, actor_subject, event_time)`,
		`CREATE INDEX IF NOT EXISTS idx_transfer_attr_provider_time ON transfer_attribution_event(provider, bucket, event_time)`,
		`CREATE INDEX IF NOT EXISTS idx_transfer_attr_sha_time ON transfer_attribution_event(sha256, event_time)`,
		`CREATE INDEX IF NOT EXISTS idx_transfer_attr_session ON transfer_attribution_event(transfer_session_id)`,
		`CREATE INDEX IF NOT EXISTS idx_access_grant_storage_time ON access_grant(provider, bucket, storage_url, last_issued_at)`,
		`CREATE INDEX IF NOT EXISTS idx_access_grant_scope_time ON access_grant(organization, project, last_issued_at)`,
		`CREATE INDEX IF NOT EXISTS idx_access_grant_sha_time ON access_grant(sha256, last_issued_at)`,
		`CREATE INDEX IF NOT EXISTS idx_provider_transfer_scope_time ON provider_transfer_event(organization, project, direction, event_time)`,
		`CREATE INDEX IF NOT EXISTS idx_provider_transfer_actor_time ON provider_transfer_event(actor_email, actor_subject, event_time)`,
		`CREATE INDEX IF NOT EXISTS idx_provider_transfer_provider_time ON provider_transfer_event(provider, bucket, event_time)`,
		`CREATE INDEX IF NOT EXISTS idx_provider_transfer_sha_time ON provider_transfer_event(sha256, event_time)`,
		`CREATE INDEX IF NOT EXISTS idx_provider_transfer_status ON provider_transfer_event(reconciliation_status, event_time)`,
		`CREATE INDEX IF NOT EXISTS idx_provider_transfer_grant ON provider_transfer_event(access_grant_id)`,
	}
	for _, q := range queries {
		if _, err := db.db.Exec(q); err != nil {
			return fmt.Errorf("failed to initialize transfer attribution schema: %w", err)
		}
	}
	if err := db.backfillAccessGrants(context.Background()); err != nil {
		return fmt.Errorf("failed to backfill access grants: %w", err)
	}
	return nil
}
