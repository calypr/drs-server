package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/calypr/syfon/internal/persistence/store"
	"github.com/lib/pq"
)

type postgresDialect struct{}

var _ store.Dialect = postgresDialect{}

func (postgresDialect) Rebind(query string) string {
	return postgresRebindQuestionPlaceholders(query, 1)
}

func (postgresDialect) ListArgs(column string, values []string) (string, []any) {
	if len(values) == 0 {
		return "1 = 0", nil
	}
	return fmt.Sprintf("%s = ANY(?)", column), []any{pq.Array(values)}
}

func (postgresDialect) MaxParameters() int {
	return 65535
}

func (postgresDialect) BeginContentWrite(ctx context.Context, db *sql.DB) (*sql.Tx, error) {
	return db.BeginTx(ctx, nil)
}

func (postgresDialect) LockContentWrite(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('syfon-content-write', 0))`)
	return err
}

func (postgresDialect) Bootstrap(ctx context.Context, db *sql.DB) error {
	backend := &PostgresDB{db: db}
	if err := backend.ensureObjectSchema(); err != nil {
		return err
	}
	if err := backend.ensureBucketScopeSchema(); err != nil {
		return err
	}
	if err := backend.ensureS3CredentialSchema(); err != nil {
		return err
	}
	if err := backend.ensureLFSPendingSchema(); err != nil {
		return err
	}
	if err := backend.ensureObjectUsageSchema(); err != nil {
		return err
	}
	if err := backend.ensurePendingObjectUsageSchema(); err != nil {
		return err
	}
	if err := backend.ensureTransferAttributionSchema(); err != nil {
		return err
	}
	return nil
}

func (postgresDialect) IsConflict(err error) bool {
	if err == nil {
		return false
	}
	var pqErr *pq.Error
	if errors.As(err, &pqErr) && strings.HasPrefix(string(pqErr.Code), "23") {
		return true
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "duplicate key") ||
		strings.Contains(message, "unique constraint") ||
		strings.Contains(message, "already configured")
}
