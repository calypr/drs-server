package sqlite

import (
	"database/sql"
	"fmt"
	"strings"

	_ "github.com/mattn/go-sqlite3"
)

type SqliteDB struct {
	db *sql.DB
}

func NewSqliteDB(dsn string) (*SqliteDB, error) {
	db, err := sql.Open("sqlite3", sqliteDSN(dsn))
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}
	// Keep a single connection so in-memory SQLite databases remain consistent
	// across schema initialization and subsequent queries.
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}

	s := &SqliteDB{db: db}
	if err := s.initSchema(); err != nil {
		return nil, fmt.Errorf("failed to init schema: %w", err)
	}

	return s, nil
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
