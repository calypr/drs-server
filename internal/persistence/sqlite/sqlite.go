package sqlite

import (
	"database/sql"
	"fmt"
	"strings"

	"github.com/calypr/syfon/internal/persistence/credentialcipher"
	"github.com/calypr/syfon/internal/persistence/store"
	_ "github.com/mattn/go-sqlite3"
)

type SqliteDB struct {
	*store.Store
	db     *sql.DB
	cipher store.CredentialCodec
}

func NewSqliteDB(dsn string, codecs ...store.CredentialCodec) (*SqliteDB, error) {
	cipher, err := credentialCodecFor(codecs)
	if err != nil {
		return nil, err
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
	return &SqliteDB{Store: shared, db: db, cipher: cipher}, nil
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

func sqliteDSN(dsn string) string {
	if marker := strings.Index(dsn, "_txlock="); marker >= 0 {
		end := strings.IndexAny(dsn[marker:], "&")
		if end < 0 {
			end = len(dsn) - marker
		}
		return dsn[:marker] + "_txlock=immediate" + dsn[marker+end:]
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
