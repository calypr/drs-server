package postgres

import (
	"database/sql"
	"fmt"

	"github.com/calypr/syfon/internal/persistence/credentialcipher"
	"github.com/calypr/syfon/internal/persistence/store"

	// Postgres driver
	_ "github.com/lib/pq"
)

func NewPostgresDB(dsn string, cipher store.CredentialCodec) (*store.Store, error) {
	var err error
	if cipher == nil {
		cipher, err = credentialcipher.NewFromEnv()
		if err != nil {
			return nil, err
		}
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
	return shared, nil
}
