package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/calypr/syfon/internal/persistence/store"
)

type sqliteDialect struct{}

var _ store.Dialect = sqliteDialect{}

func (sqliteDialect) Rebind(query string) string {
	return query
}

func (sqliteDialect) ListArgs(column string, values []string) (string, []any) {
	if len(values) == 0 {
		return "1 = 0", nil
	}
	placeholders := makePlaceholders(len(values))
	args := make([]any, len(values))
	for i, value := range values {
		args[i] = value
	}
	return fmt.Sprintf("%s IN (%s)", column, placeholders), args
}

func (sqliteDialect) MaxParameters() int {
	return sqliteMaxParams
}

func (sqliteDialect) BeginContentWrite(ctx context.Context, db *sql.DB) (*sql.Tx, error) {
	return db.BeginTx(ctx, nil)
}

func (sqliteDialect) LockContentWrite(context.Context, *sql.Tx) error {
	// sqliteDSN enables _txlock=immediate, so BeginTx already acquires the
	// write reservation before the caller performs identity reads.
	return nil
}

func (sqliteDialect) Bootstrap(_ context.Context, db *sql.DB) error {
	return (&SqliteDB{db: db}).initSchema()
}

func (sqliteDialect) IsConflict(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "constraint") ||
		strings.Contains(message, "unique") ||
		strings.Contains(message, "already configured")
}
