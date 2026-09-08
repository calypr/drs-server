package postgres

import (
	"database/sql"
	"fmt"

	"github.com/calypr/syfon/internal/persistence/credentialcipher"

	// Postgres driver
	_ "github.com/lib/pq"
)

type PostgresDB struct {
	db     *sql.DB
	cipher *credentialcipher.Cipher
}

func NewPostgresDB(dsn string, ciphers ...*credentialcipher.Cipher) (*PostgresDB, error) {
	cipher, err := credentialCipherFor(ciphers)
	if err != nil {
		return nil, err
	}
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}
	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}
	pg := &PostgresDB{db: db, cipher: cipher}
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

func credentialCipherFor(ciphers []*credentialcipher.Cipher) (*credentialcipher.Cipher, error) {
	switch len(ciphers) {
	case 0:
		return credentialcipher.NewFromEnv()
	case 1:
		if ciphers[0] == nil {
			return nil, fmt.Errorf("credential cipher is required")
		}
		return ciphers[0], nil
	default:
		return nil, fmt.Errorf("at most one credential cipher may be supplied")
	}
}
