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

func (postgresDialect) BulkObjectCondition(ids, checksums, shaQueries, genericQueries []string, start int) (string, []any) {
	first := fmt.Sprintf("$%d", start)
	second := fmt.Sprintf("$%d", start+1)
	third := fmt.Sprintf("$%d", start+2)
	fourth := fmt.Sprintf("$%d", start+3)
	return fmt.Sprintf(`(
		(COALESCE(array_length(%s::text[], 1), 0) > 0 AND o.id = ANY(%s))
		OR
		(COALESCE(array_length(%s::text[], 1), 0) > 0 AND (
			o.id = ANY(%s)
			OR EXISTS (SELECT 1 FROM drs_object_checksum c2
				WHERE c2.object_id = o.id
				  AND replace(lower(trim(c2.type)), '-', '') = 'sha256'
				  AND replace(lower(trim(c2.checksum)), 'sha256:', '') = ANY(%s))
			OR EXISTS (SELECT 1 FROM drs_object_checksum c2
				WHERE c2.object_id = o.id AND c2.checksum = ANY(%s))
		))`, first, first, second, second, third, fourth), []any{pq.Array(ids), pq.Array(checksums), pq.Array(shaQueries), pq.Array(genericQueries)}
}

func (postgresDialect) ResourceFilter(column string, resources []string, includeUnscoped bool, start int) (string, []any) {
	arrayPlaceholder := fmt.Sprintf("$%d", start)
	boolPlaceholder := fmt.Sprintf("$%d", start+1)
	return fmt.Sprintf(`(
		(
			COALESCE(array_length(%s::text[], 1), 0) > 0
			AND EXISTS (
				SELECT 1
				FROM drs_object_controlled_access ca_auth
				WHERE ca_auth.object_id = o.id AND ca_auth.resource = ANY(%s)
			)
		) OR (
			%s
			AND NOT EXISTS (
				SELECT 1
				FROM drs_object_controlled_access ca_auth
				WHERE ca_auth.object_id = o.id
			)
		)
	)`, arrayPlaceholder, arrayPlaceholder, boolPlaceholder), []any{pq.Array(resources), includeUnscoped}
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
