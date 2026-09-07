package postgres

import (
	"context"

	"github.com/lib/pq"

	clientaccess "github.com/calypr/syfon/client/access"
	"github.com/calypr/syfon/internal/buckets"
)

func (db *PostgresDB) ListBucketVisibilityRows(ctx context.Context, resources []string, includeUnscoped, restrictToResources bool) ([]buckets.VisibilityRow, error) {
	args := make([]any, 0, 2)
	query := `
		SELECT DISTINCT am.url, am.type, COALESCE(ca.resource, '')
		FROM drs_object o
		INNER JOIN drs_object_access_method am ON am.object_id = o.id
		LEFT JOIN drs_object_controlled_access ca ON ca.object_id = o.id`
	if restrictToResources {
		resources = clientaccess.NormalizeAccessResources(resources)
		if len(resources) == 0 && !includeUnscoped {
			return []buckets.VisibilityRow{}, nil
		}
		args = append(args, pq.Array(resources), includeUnscoped)
		query += `
		WHERE (
			COALESCE(array_length($1::text[], 1), 0) > 0
			AND EXISTS (
				SELECT 1
				FROM drs_object_controlled_access ca_auth
				WHERE ca_auth.object_id = o.id AND ca_auth.resource = ANY($1)
			)
		) OR (
			$2
			AND NOT EXISTS (
				SELECT 1
				FROM drs_object_controlled_access ca_auth
				WHERE ca_auth.object_id = o.id
			)
		)`
	}
	rows, err := db.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]buckets.VisibilityRow, 0)
	for rows.Next() {
		var row buckets.VisibilityRow
		if err := rows.Scan(&row.AccessURL, &row.AccessType, &row.Resource); err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}
