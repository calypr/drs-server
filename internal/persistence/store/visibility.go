package store

import (
	"context"
	"strings"

	clientaccess "github.com/calypr/syfon/client/access"
	"github.com/calypr/syfon/internal/buckets"
)

func (db *Store) ListBucketVisibilityRows(ctx context.Context, resources []string, includeUnscoped, restrictToResources bool) ([]buckets.VisibilityRow, error) {
	query := `
		SELECT DISTINCT am.url, am.type, COALESCE(ca.resource, '')
		FROM drs_object o
		INNER JOIN drs_object_access_method am ON am.object_id = o.id
		LEFT JOIN drs_object_controlled_access ca ON ca.object_id = o.id`
	var args []any
	if restrictToResources {
		resources = clientaccess.NormalizeAccessResources(resources)
		if len(resources) == 0 && !includeUnscoped {
			return []buckets.VisibilityRow{}, nil
		}
		parts := make([]string, 0, 2)
		if len(resources) > 0 {
			clause, clauseArgs := db.dialect.ListArgs("ca_auth.resource", resources)
			parts = append(parts, `EXISTS (
				SELECT 1
				FROM drs_object_controlled_access ca_auth
				WHERE ca_auth.object_id = o.id AND `+clause+`
			)`)
			args = append(args, clauseArgs...)
		}
		if includeUnscoped {
			parts = append(parts, `? AND NOT EXISTS (
				SELECT 1
				FROM drs_object_controlled_access ca_auth
				WHERE ca_auth.object_id = o.id
			)`)
			args = append(args, includeUnscoped)
		}
		query += ` WHERE (` + strings.Join(parts, " OR ") + `)`
	}
	rows, err := db.queryContext(ctx, query, args...)
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
