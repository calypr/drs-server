package sqlite

import (
	"context"
	"strings"

	clientaccess "github.com/calypr/syfon/client/access"
	"github.com/calypr/syfon/internal/buckets"
)

func (db *SqliteDB) ListBucketVisibilityRows(ctx context.Context, resources []string, includeUnscoped, restrictToResources bool) ([]buckets.VisibilityRow, error) {
	args := make([]any, 0, len(resources))
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
		parts := make([]string, 0, 2)
		if len(resources) > 0 {
			placeholders := make([]string, 0, len(resources))
			for _, resource := range resources {
				args = append(args, resource)
				placeholders = append(placeholders, "?")
			}
			parts = append(parts, `EXISTS (
				SELECT 1
				FROM drs_object_controlled_access ca_auth
				WHERE ca_auth.object_id = o.id AND ca_auth.resource IN (`+strings.Join(placeholders, ",")+`)
			)`)
		}
		if includeUnscoped {
			parts = append(parts, `NOT EXISTS (
				SELECT 1
				FROM drs_object_controlled_access ca_auth
				WHERE ca_auth.object_id = o.id
			)`)
		}
		query += ` WHERE (` + strings.Join(parts, " OR ") + `)`
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

func uniqueNonEmptyStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		normalized := strings.TrimSpace(value)
		if normalized == "" {
			continue
		}
		if _, ok := seen[normalized]; ok {
			continue
		}
		seen[normalized] = struct{}{}
		out = append(out, normalized)
	}
	return out
}
