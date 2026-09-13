package store

import (
	"context"
	"database/sql"
)

// Dialect contains the SQL and locking behavior that cannot be shared across
// the SQLite and PostgreSQL implementations.
type Dialect interface {
	Rebind(string) string
	ListArgs(string, []string) (string, []any)
	LockContentWrite(context.Context, *sql.Tx) error
	Bootstrap(context.Context, *sql.DB) error
}
