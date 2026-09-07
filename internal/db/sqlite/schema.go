package sqlite

import (
	"context"
	"strings"
)

func (db *SqliteDB) initSchema() error {
	queries := []string{
		`CREATE TABLE IF NOT EXISTS drs_object (
			id TEXT PRIMARY KEY,
			size INTEGER,
			created_time TIMESTAMP,
			updated_time TIMESTAMP,
			name TEXT,
			version TEXT,
			description TEXT
		)`,
		`CREATE TABLE IF NOT EXISTS drs_object_access_method (
			object_id TEXT,
			url TEXT,
			type TEXT,
			FOREIGN KEY(object_id) REFERENCES drs_object(id) ON DELETE CASCADE
		)`,
		`CREATE TABLE IF NOT EXISTS drs_object_controlled_access (
			object_id TEXT,
			resource TEXT,
			FOREIGN KEY(object_id) REFERENCES drs_object(id) ON DELETE CASCADE
		)`,
		`CREATE TABLE IF NOT EXISTS drs_object_checksum (
			object_id TEXT,
			type TEXT,
			checksum TEXT,
			FOREIGN KEY(object_id) REFERENCES drs_object(id) ON DELETE CASCADE
		)`,
		`CREATE TABLE IF NOT EXISTS drs_object_read_policy (
			object_id TEXT PRIMARY KEY,
			public_read BOOLEAN NOT NULL DEFAULT 0,
			FOREIGN KEY(object_id) REFERENCES drs_object(id) ON DELETE CASCADE
		)`,
		`CREATE INDEX IF NOT EXISTS idx_drs_object_access_method_object_id ON drs_object_access_method(object_id)`,
		`CREATE INDEX IF NOT EXISTS idx_drs_object_controlled_access_object_id ON drs_object_controlled_access(object_id)`,
		`CREATE INDEX IF NOT EXISTS idx_drs_object_controlled_access_resource ON drs_object_controlled_access(resource)`,
		`CREATE INDEX IF NOT EXISTS idx_drs_object_controlled_access_resource_object_id ON drs_object_controlled_access(resource, object_id)`,
		`CREATE INDEX IF NOT EXISTS idx_drs_object_controlled_access_object_id_resource ON drs_object_controlled_access(object_id, resource)`,
		`CREATE INDEX IF NOT EXISTS idx_drs_object_checksum_object_id ON drs_object_checksum(object_id)`,
		`CREATE INDEX IF NOT EXISTS idx_drs_object_checksum_checksum ON drs_object_checksum(checksum)`,
		`CREATE INDEX IF NOT EXISTS idx_drs_object_checksum_checksum_type_object_id ON drs_object_checksum(checksum, type, object_id)`,
		`CREATE INDEX IF NOT EXISTS idx_drs_object_checksum_sha256_identity ON drs_object_checksum(replace(lower(trim(type)), '-', ''), replace(lower(trim(checksum)), 'sha256:', ''), object_id)`,
		`CREATE TABLE IF NOT EXISTS drs_object_alias (
			alias_id TEXT PRIMARY KEY,
			object_id TEXT NOT NULL,
			FOREIGN KEY(object_id) REFERENCES drs_object(id) ON DELETE CASCADE
		)`,
		`CREATE INDEX IF NOT EXISTS idx_drs_object_alias_object_id ON drs_object_alias(object_id)`,
		`CREATE TABLE IF NOT EXISTS drs_object_name_alias (
			object_id TEXT NOT NULL,
			name_alias TEXT NOT NULL,
			PRIMARY KEY (object_id, name_alias),
			FOREIGN KEY(object_id) REFERENCES drs_object(id) ON DELETE CASCADE
		)`,
		`CREATE INDEX IF NOT EXISTS idx_drs_object_name_alias_object_id ON drs_object_name_alias(object_id)`,
		`CREATE TABLE IF NOT EXISTS s3_credential (
			credential_id TEXT PRIMARY KEY,
			bucket TEXT NOT NULL,
			provider TEXT NOT NULL DEFAULT 's3',
			region TEXT,
			access_key TEXT,
			secret_key TEXT,
			endpoint TEXT
		)`,
		`CREATE TRIGGER IF NOT EXISTS s3_credential_unique_bucket_insert
		BEFORE INSERT ON s3_credential
		FOR EACH ROW
		WHEN EXISTS (
			SELECT 1
			FROM s3_credential
			WHERE bucket = NEW.bucket AND credential_id <> NEW.credential_id
		)
		BEGIN
			SELECT RAISE(ABORT, 'physical bucket is already configured under another credential');
		END`,
		`CREATE TRIGGER IF NOT EXISTS s3_credential_unique_bucket_update
		BEFORE UPDATE OF bucket, credential_id ON s3_credential
		FOR EACH ROW
		WHEN EXISTS (
			SELECT 1
			FROM s3_credential
			WHERE bucket = NEW.bucket AND credential_id <> NEW.credential_id
		)
		BEGIN
			SELECT RAISE(ABORT, 'physical bucket is already configured under another credential');
		END`,
		`CREATE TABLE IF NOT EXISTS bucket_scope (
			organization TEXT NOT NULL,
			project_id TEXT NOT NULL,
			credential_id TEXT NOT NULL,
			bucket TEXT NOT NULL,
			path_prefix TEXT,
			PRIMARY KEY (organization, project_id)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_bucket_scope_credential_id ON bucket_scope(credential_id)`,
		`CREATE INDEX IF NOT EXISTS idx_bucket_scope_bucket ON bucket_scope(bucket)`,
		`CREATE TABLE IF NOT EXISTS lfs_pending_metadata (
			oid TEXT PRIMARY KEY,
			candidate_json TEXT NOT NULL,
			created_time TIMESTAMP NOT NULL,
			expires_time TIMESTAMP NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_lfs_pending_metadata_expires ON lfs_pending_metadata(expires_time)`,
		`CREATE INDEX IF NOT EXISTS idx_lfs_pending_metadata_created ON lfs_pending_metadata(created_time)`,
		`CREATE TABLE IF NOT EXISTS object_usage (
			object_id TEXT PRIMARY KEY,
			upload_count INTEGER NOT NULL DEFAULT 0,
			download_count INTEGER NOT NULL DEFAULT 0,
			last_upload_time TIMESTAMP NULL,
			last_download_time TIMESTAMP NULL,
			updated_time TIMESTAMP NOT NULL,
			FOREIGN KEY(object_id) REFERENCES drs_object(id) ON DELETE CASCADE
		)`,
		`CREATE INDEX IF NOT EXISTS idx_object_usage_last_download_time ON object_usage(last_download_time)`,
		`CREATE INDEX IF NOT EXISTS idx_object_usage_last_download_time_object_id ON object_usage(last_download_time, object_id)`,
		`CREATE INDEX IF NOT EXISTS idx_object_usage_last_upload_time ON object_usage(last_upload_time)`,
		`CREATE TABLE IF NOT EXISTS object_usage_event (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			object_id TEXT NOT NULL,
			event_type TEXT NOT NULL CHECK(event_type IN ('upload','download')),
			event_time TIMESTAMP NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_object_usage_event_object_id ON object_usage_event(object_id)`,
		`CREATE INDEX IF NOT EXISTS idx_object_usage_event_event_time ON object_usage_event(event_time)`,
		`CREATE TABLE IF NOT EXISTS transfer_attribution_event (
			event_id TEXT PRIMARY KEY,
			access_grant_id TEXT NOT NULL DEFAULT '',
			event_type TEXT NOT NULL CHECK(event_type IN ('access_issued')),
			direction TEXT NOT NULL DEFAULT 'download' CHECK(direction IN ('download','upload')),
			event_time TIMESTAMP NOT NULL,
			request_id TEXT NOT NULL DEFAULT '',
			object_id TEXT NOT NULL DEFAULT '',
			sha256 TEXT NOT NULL DEFAULT '',
			object_size INTEGER NOT NULL DEFAULT 0,
			organization TEXT NOT NULL DEFAULT '',
			project TEXT NOT NULL DEFAULT '',
			access_id TEXT NOT NULL DEFAULT '',
			provider TEXT NOT NULL DEFAULT '',
			bucket TEXT NOT NULL DEFAULT '',
			storage_url TEXT NOT NULL DEFAULT '',
			range_start INTEGER NULL,
			range_end INTEGER NULL,
			bytes_requested INTEGER NOT NULL DEFAULT 0,
			bytes_completed INTEGER NOT NULL DEFAULT 0,
			actor_email TEXT NOT NULL DEFAULT '',
			actor_subject TEXT NOT NULL DEFAULT '',
			auth_mode TEXT NOT NULL DEFAULT '',
			client_name TEXT NOT NULL DEFAULT '',
			client_version TEXT NOT NULL DEFAULT '',
			transfer_session_id TEXT NOT NULL DEFAULT ''
		)`,
		`CREATE TABLE IF NOT EXISTS access_grant (
			access_grant_id TEXT PRIMARY KEY,
			first_issued_at TIMESTAMP NOT NULL,
			last_issued_at TIMESTAMP NOT NULL,
			issue_count INTEGER NOT NULL DEFAULT 0,
			object_id TEXT NOT NULL DEFAULT '',
			sha256 TEXT NOT NULL DEFAULT '',
			object_size INTEGER NOT NULL DEFAULT 0,
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
			direction TEXT NOT NULL CHECK(direction IN ('download','upload')),
			event_time TIMESTAMP NOT NULL,
			request_id TEXT NOT NULL DEFAULT '',
			provider_request_id TEXT NOT NULL DEFAULT '',
			object_id TEXT NOT NULL DEFAULT '',
			sha256 TEXT NOT NULL DEFAULT '',
			object_size INTEGER NOT NULL DEFAULT 0,
			organization TEXT NOT NULL DEFAULT '',
			project TEXT NOT NULL DEFAULT '',
			access_id TEXT NOT NULL DEFAULT '',
			provider TEXT NOT NULL DEFAULT '',
			bucket TEXT NOT NULL DEFAULT '',
			object_key TEXT NOT NULL DEFAULT '',
			storage_url TEXT NOT NULL DEFAULT '',
			range_start INTEGER NULL,
			range_end INTEGER NULL,
			bytes_transferred INTEGER NOT NULL DEFAULT 0,
			http_method TEXT NOT NULL DEFAULT '',
			http_status INTEGER NOT NULL DEFAULT 0,
			requester_principal TEXT NOT NULL DEFAULT '',
			source_ip TEXT NOT NULL DEFAULT '',
			user_agent TEXT NOT NULL DEFAULT '',
			raw_event_ref TEXT NOT NULL DEFAULT '',
			actor_email TEXT NOT NULL DEFAULT '',
			actor_subject TEXT NOT NULL DEFAULT '',
			auth_mode TEXT NOT NULL DEFAULT '',
			reconciliation_status TEXT NOT NULL DEFAULT 'unmatched' CHECK(reconciliation_status IN ('matched','ambiguous','unmatched'))
		)`,
		`CREATE INDEX IF NOT EXISTS idx_transfer_attr_scope_time ON transfer_attribution_event(organization, project, event_type, event_time)`,
		`CREATE INDEX IF NOT EXISTS idx_transfer_attr_scope_event_time ON transfer_attribution_event(organization, project, event_time)`,
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
			return err
		}
	}
	if _, err := db.db.Exec(`ALTER TABLE s3_credential ADD COLUMN provider TEXT NOT NULL DEFAULT 's3'`); err != nil {
		if !strings.Contains(strings.ToLower(err.Error()), "duplicate column name") {
			return err
		}
	}
	if err := db.ensureCredentialIdentitySchema(); err != nil {
		return err
	}
	if err := db.ensureObjectTableShape(); err != nil {
		return err
	}
	if _, err := db.db.Exec(`DROP TABLE IF EXISTS drs_object_browse_index`); err != nil {
		return err
	}
	if _, err := db.db.Exec(`ALTER TABLE transfer_attribution_event ADD COLUMN access_grant_id TEXT NOT NULL DEFAULT ''`); err != nil {
		if !strings.Contains(strings.ToLower(err.Error()), "duplicate column name") {
			return err
		}
	}
	if _, err := db.db.Exec(`ALTER TABLE transfer_attribution_event ADD COLUMN direction TEXT NOT NULL DEFAULT 'download'`); err != nil {
		if !strings.Contains(strings.ToLower(err.Error()), "duplicate column name") {
			return err
		}
	}
	if _, err := db.db.Exec(`CREATE INDEX IF NOT EXISTS idx_transfer_attr_direction_time ON transfer_attribution_event(direction, event_time)`); err != nil {
		return err
	}
	if err := db.backfillAccessGrants(context.Background()); err != nil {
		return err
	}
	return nil
}
