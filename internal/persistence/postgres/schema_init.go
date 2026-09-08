package postgres

import "context"

// These receiver bridges keep the existing package-level schema characterization
// tests on the backend wrapper while runtime bootstrap lives in postgresDialect.
func (db *PostgresDB) ensureObjectSchema() error {
	return (&postgresSchemaBootstrap{db: db.db}).ensureObjectSchema()
}

func (db *PostgresDB) validateLegacyAccessMethodScopes(ctx context.Context) error {
	return (&postgresSchemaBootstrap{db: db.db}).validateLegacyAccessMethodScopes(ctx)
}

func (db *PostgresDB) hasLegacyAccessMethodScopeColumns(ctx context.Context) (bool, error) {
	return (&postgresSchemaBootstrap{db: db.db}).hasLegacyAccessMethodScopeColumns(ctx)
}

func (db *PostgresDB) ensureS3CredentialSchema() error {
	return (&postgresSchemaBootstrap{db: db.db}).ensureS3CredentialSchema()
}

func (db *PostgresDB) ensureBucketScopeSchema() error {
	return (&postgresSchemaBootstrap{db: db.db}).ensureBucketScopeSchema()
}

func (db *PostgresDB) ensureLFSPendingSchema() error {
	return (&postgresSchemaBootstrap{db: db.db}).ensureLFSPendingSchema()
}

func (db *PostgresDB) ensureObjectUsageSchema() error {
	return (&postgresSchemaBootstrap{db: db.db}).ensureObjectUsageSchema()
}

func (db *PostgresDB) ensurePendingObjectUsageSchema() error {
	return (&postgresSchemaBootstrap{db: db.db}).ensurePendingObjectUsageSchema()
}

func (db *PostgresDB) ensureTransferAttributionSchema() error {
	return (&postgresSchemaBootstrap{db: db.db}).ensureTransferAttributionSchema()
}
