package sqlite

import (
	"database/sql"
	"fmt"
	"strings"

	"github.com/calypr/syfon/internal/persistence/credentialcipher"
	"github.com/calypr/syfon/internal/persistence/store"
	_ "github.com/mattn/go-sqlite3"
)

func NewSqliteDB(dsn string, cipher store.CredentialCodec) (*store.Store, error) {
	var err error
	if cipher == nil {
		cipher, err = credentialcipher.NewFromEnv()
		if err != nil {
			return nil, err
		}
	}
	db, err := sql.Open("sqlite3", sqliteDSN(dsn))
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}
	// Keep a single connection so in-memory SQLite databases remain consistent
	// across schema initialization and subsequent queries.
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}

	shared, err := store.Open(db, sqliteDialect{}, cipher)
	if err != nil {
		return nil, err
	}
	return shared, nil
}

func sqliteDSN(dsn string) string {
	if marker := strings.Index(dsn, "_txlock="); marker >= 0 {
		end := strings.IndexAny(dsn[marker:], "&")
		if end < 0 {
			end = len(dsn) - marker
		}
		dsn = dsn[:marker] + "_txlock=immediate" + dsn[marker+end:]
	}
	params := make([]string, 0, 2)
	if !strings.Contains(dsn, "_foreign_keys=") {
		params = append(params, "_foreign_keys=on")
	}
	if !strings.Contains(dsn, "_txlock=") {
		params = append(params, "_txlock=immediate")
	}
	if dsn == ":memory:" {
		return "file::memory:?" + strings.Join(params, "&")
	}
	if len(params) == 0 {
		return dsn
	}
	separator := "?"
	if strings.Contains(dsn, "?") {
		separator = "&"
	}
	return dsn + separator + strings.Join(params, "&")
}
