package postgres

import (
	"database/sql"
	"fmt"

	// Postgres driver
	_ "github.com/lib/pq"
)

type PostgresDB struct {
	db *sql.DB
}

func NewPostgresDB(dsn string) (*PostgresDB, error) {
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}
	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}
	pg := &PostgresDB{db: db}
	if err := pg.ensureObjectSchema(); err != nil {
		return nil, err
	}
	if err := pg.ensureBucketScopeSchema(); err != nil {
		return nil, err
	}
	if err := pg.ensureS3CredentialSchema(); err != nil {
		return nil, err
	}
	if err := pg.ensureLFSPendingSchema(); err != nil {
		return nil, err
	}
	if err := pg.ensureObjectUsageSchema(); err != nil {
		return nil, err
	}
	if err := pg.ensurePendingObjectUsageSchema(); err != nil {
		return nil, err
	}
	if err := pg.ensureTransferAttributionSchema(); err != nil {
		return nil, err
	}
	return pg, nil
}
