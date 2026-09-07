package sqlite

import "strings"

func (db *SqliteDB) ensureObjectTableShape() error {
	rows, err := db.db.Query(`PRAGMA table_info(drs_object)`)
	if err != nil {
		return err
	}
	defer rows.Close()

	hasFileName := false
	for rows.Next() {
		var cid int
		var name, typ string
		var notNull, pk int
		var dflt any
		if err := rows.Scan(&cid, &name, &typ, &notNull, &dflt, &pk); err != nil {
			return err
		}
		if strings.EqualFold(strings.TrimSpace(name), "file_name") {
			hasFileName = true
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if !hasFileName {
		return nil
	}

	tx, err := db.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	for _, stmt := range []string{
		`CREATE TABLE drs_object_new (
			id TEXT PRIMARY KEY,
			size INTEGER,
			created_time TIMESTAMP,
			updated_time TIMESTAMP,
			name TEXT,
			version TEXT,
			description TEXT
		)`,
		`INSERT INTO drs_object_new (id, size, created_time, updated_time, name, version, description)
		 SELECT id, size, created_time, updated_time, name, version, description FROM drs_object`,
		`DROP TABLE drs_object`,
		`ALTER TABLE drs_object_new RENAME TO drs_object`,
	} {
		if _, err := tx.Exec(stmt); err != nil {
			return err
		}
	}

	return tx.Commit()
}

func (db *SqliteDB) ensureCredentialIdentitySchema() error {
	rows, err := db.db.Query(`PRAGMA table_info(s3_credential)`)
	if err != nil {
		return err
	}
	hasCredentialID := false
	for rows.Next() {
		var cid int
		var name, typ string
		var notNull, pk int
		var dflt any
		if err := rows.Scan(&cid, &name, &typ, &notNull, &dflt, &pk); err != nil {
			_ = rows.Close()
			return err
		}
		if name == "credential_id" {
			hasCredentialID = true
		}
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if !hasCredentialID {
		if _, err := db.db.Exec(`
			ALTER TABLE s3_credential RENAME TO s3_credential_legacy;
			CREATE TABLE s3_credential (
				credential_id TEXT PRIMARY KEY,
				bucket TEXT NOT NULL,
				provider TEXT NOT NULL DEFAULT 's3',
				region TEXT,
				access_key TEXT,
				secret_key TEXT,
				endpoint TEXT
			);
			INSERT INTO s3_credential (credential_id, bucket, provider, region, access_key, secret_key, endpoint)
				SELECT bucket, bucket, provider, region, access_key, secret_key, endpoint FROM s3_credential_legacy;
			DROP TABLE s3_credential_legacy;
		`); err != nil {
			return err
		}
	}
	if _, err := db.db.Exec(`ALTER TABLE bucket_scope ADD COLUMN credential_id TEXT`); err != nil {
		if !strings.Contains(strings.ToLower(err.Error()), "duplicate column name") {
			return err
		}
	}
	if _, err := db.db.Exec(`UPDATE bucket_scope SET credential_id = bucket WHERE COALESCE(TRIM(credential_id), '') = ''`); err != nil {
		return err
	}
	if _, err := db.db.Exec(`CREATE INDEX IF NOT EXISTS idx_bucket_scope_credential_id ON bucket_scope(credential_id)`); err != nil {
		return err
	}
	if _, err := db.db.Exec(`CREATE INDEX IF NOT EXISTS idx_s3_credential_bucket ON s3_credential(bucket)`); err != nil {
		return err
	}
	return nil
}
