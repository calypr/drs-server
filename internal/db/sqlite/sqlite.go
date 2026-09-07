package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/calypr/syfon/apigen/server/drs"
	"github.com/calypr/syfon/internal/db"
	_ "github.com/mattn/go-sqlite3"
)

// SqliteDB implements db.DatabaseInterface.

type SqliteDB struct {
	db *sql.DB
}

var _ db.DatabaseInterface = (*SqliteDB)(nil)

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

func (db *SqliteDB) GetServiceInfo(ctx context.Context) (*drs.Service, error) {
	name := "Calypr-backed DRS server (SQLite)"
	createdAt := time.Now()
	updatedAt := time.Now()
	environment := "prod"
	return &drs.Service{
		Id:          "drs-service-calypr",
		Name:        "Calypr DRS Server",
		Type:        drs.ServiceType{Group: "org.ga4gh", Artifact: "drs", Version: "1.2.0"},
		Description: &name,
		CreatedAt:   &createdAt,
		UpdatedAt:   &updatedAt,
		Environment: &environment,
		Version:     "1.0.0",
	}, nil
}
