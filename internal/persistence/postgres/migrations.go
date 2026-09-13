package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

type schemaMigration struct {
	Version  int64
	Name     string
	Checksum string
}

var schemaMigrations = []schemaMigration{
	{Version: 1, Name: "baseline", Checksum: "73f4fb5eb6ac3e695be61e19d88b60b63d21ccf02e5476c5f6b5f92fb35797ac"},
	{Version: 2, Name: "access-method-url-object-index", Checksum: "d653e7d57d6fd4e2bb80df73b34f1565d4a781bd8eb6db76a162a9b486d449f1"},
	{Version: 3, Name: "multipart-completion-receipt", Checksum: "3339e1c97b156d3e761e606e6b19ff20d6d52f41962fff4f11a7befbd4f55e07"},
}

var requiredSchemaRelations = []string{
	"drs_object",
	"drs_object_access_method",
	"drs_object_controlled_access",
	"drs_object_checksum",
	"drs_object_alias",
	"drs_object_name_alias",
	"drs_object_read_policy",
	"s3_credential",
	"bucket_scope",
	"lfs_pending_metadata",
	"multipart_upload_session",
	"multipart_completion_receipt",
	"object_usage",
	"object_usage_event",
	"transfer_attribution_event",
	"access_grant",
	"provider_transfer_event",
}

var requiredSchemaIndexes = []string{
	"drs_object_access_method_url_object_id_idx",
}

func readAppliedMigrations(ctx context.Context, executor interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}) (map[int64]struct{}, error) {
	rows, err := executor.QueryContext(ctx, `SELECT version, name, checksum FROM syfon_schema_migrations ORDER BY version`)
	if err != nil {
		return nil, fmt.Errorf("read schema migration ledger: %w", err)
	}
	defer rows.Close()
	applied := make(map[int64]struct{})
	for rows.Next() {
		var version int64
		var name, checksum string
		if err := rows.Scan(&version, &name, &checksum); err != nil {
			return nil, fmt.Errorf("scan schema migration ledger: %w", err)
		}
		if version <= 0 {
			return nil, fmt.Errorf("invalid schema migration version %d", version)
		}
		migration, ok := migrationByVersion(version)
		if !ok {
			return nil, fmt.Errorf("database schema migration version %d is newer than this binary", version)
		}
		if migration.Name != name || migration.Checksum != checksum {
			return nil, fmt.Errorf("schema migration %d checksum or name mismatch", version)
		}
		if _, exists := applied[version]; exists {
			return nil, fmt.Errorf("duplicate schema migration version %d", version)
		}
		applied[version] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate schema migration ledger: %w", err)
	}
	return applied, nil
}

func validateAppliedMigrations(applied map[int64]struct{}) error {
	for version := int64(1); version <= int64(len(applied)); version++ {
		if _, ok := applied[version]; !ok {
			return fmt.Errorf("schema migration ledger has a gap before version %d", version)
		}
	}
	return nil
}

func migrationByVersion(version int64) (schemaMigration, bool) {
	for _, migration := range schemaMigrations {
		if migration.Version == version {
			return migration, true
		}
	}
	return schemaMigration{}, false
}

func CheckSchema(ctx context.Context, db *sql.DB) error {
	if db == nil {
		return errors.New("database is required")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	var relation sql.NullString
	if err := db.QueryRowContext(ctx, `SELECT to_regclass('syfon_schema_migrations')`).Scan(&relation); err != nil {
		return fmt.Errorf("check schema migration ledger: %w", err)
	}
	if !relation.Valid || strings.TrimSpace(relation.String) == "" {
		return errors.New("schema migration ledger is missing; wait for the cluster DB-init Job")
	}
	for _, relationName := range requiredSchemaRelations {
		if err := checkSchemaRelation(ctx, db, relationName); err != nil {
			return err
		}
	}
	for _, indexName := range requiredSchemaIndexes {
		if err := checkSchemaRelation(ctx, db, indexName); err != nil {
			return err
		}
	}
	applied, err := readAppliedMigrations(ctx, db)
	if err != nil {
		return err
	}
	if len(applied) != len(schemaMigrations) {
		return fmt.Errorf("database schema is behind supported version %d (applied %d)", schemaMigrations[len(schemaMigrations)-1].Version, len(applied))
	}
	return validateAppliedMigrations(applied)
}

func checkSchemaRelation(ctx context.Context, db *sql.DB, name string) error {
	var relation sql.NullString
	if err := db.QueryRowContext(ctx, `SELECT to_regclass($1)`, name).Scan(&relation); err != nil {
		return fmt.Errorf("check schema relation %s: %w", name, err)
	}
	if !relation.Valid || strings.TrimSpace(relation.String) == "" {
		return fmt.Errorf("required schema relation %q is missing; wait for the cluster DB-init Job", name)
	}
	return nil
}
