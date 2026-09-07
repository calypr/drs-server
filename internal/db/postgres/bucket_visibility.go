package postgres

import (
	"context"

	sycommon "github.com/calypr/syfon/common"
	"github.com/calypr/syfon/internal/models"
	"github.com/lib/pq"
)

func (db *PostgresDB) ListBucketVisibilityRows(ctx context.Context, resources []string, includeUnscoped, restrictToResources bool) ([]models.BucketVisibilityRow, error) {
	args := make([]any, 0, 2)
	query := `
		SELECT DISTINCT am.url, am.type, COALESCE(ca.resource, '')
		FROM drs_object o
		INNER JOIN drs_object_access_method am ON am.object_id = o.id
		LEFT JOIN drs_object_controlled_access ca ON ca.object_id = o.id`
	if restrictToResources {
		resources = sycommon.NormalizeAccessResources(resources)
		if len(resources) == 0 && !includeUnscoped {
			return []models.BucketVisibilityRow{}, nil
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

	out := make([]models.BucketVisibilityRow, 0)
	for rows.Next() {
		var row models.BucketVisibilityRow
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
