package postgres

import (
	"database/sql"
	"fmt"

	"github.com/calypr/syfon/internal/persistence/credentialcipher"
	"github.com/calypr/syfon/internal/persistence/store"

	// Postgres driver
	_ "github.com/lib/pq"
)

type PostgresDB struct {
	*store.Store
	db     *sql.DB
	cipher store.CredentialCodec
}

func NewPostgresDB(dsn string, codecs ...store.CredentialCodec) (*PostgresDB, error) {
	cipher, err := credentialCodecFor(codecs)
	if err != nil {
		return nil, err
	}
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}
	shared, err := store.Open(db, postgresDialect{}, cipher)
	if err != nil {
		return nil, err
	}
	return &PostgresDB{Store: shared, db: db, cipher: cipher}, nil
}

func credentialCodecFor(codecs []store.CredentialCodec) (store.CredentialCodec, error) {
	switch len(codecs) {
	case 0:
		return credentialcipher.NewFromEnv()
	case 1:
		if codecs[0] == nil {
			return nil, fmt.Errorf("credential cipher is required")
		}
		return codecs[0], nil
	default:
		return nil, fmt.Errorf("at most one credential cipher may be supplied")
	}
}
