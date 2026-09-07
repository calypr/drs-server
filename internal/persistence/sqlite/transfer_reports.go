package sqlite

import (
	"context"
	"fmt"
	"strings"
	"time"

	clientaccess "github.com/calypr/syfon/client/access"
	"github.com/calypr/syfon/internal/usage"
)

func (db *SqliteDB) GetTransferAttributionSummary(ctx context.Context, filter usage.Filter) (usage.Summary, error) {
	where, args := transferAttributionWhere(filter)
	var out usage.Summary
	err := db.db.QueryRowContext(ctx, `
		SELECT
			COUNT(*),
			COALESCE(SUM(CASE WHEN event_type = 'access_issued' THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN direction = 'download' THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN direction = 'upload' THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(bytes_requested), 0),
			COALESCE(SUM(CASE WHEN direction = 'download' THEN bytes_requested ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN direction = 'upload' THEN bytes_requested ELSE 0 END), 0)
		FROM transfer_attribution_event`+where, args...).Scan(
		&out.EventCount,
		&out.AccessIssuedCount,
		&out.DownloadEventCount,
		&out.UploadEventCount,
		&out.BytesRequested,
		&out.BytesDownloaded,
		&out.BytesUploaded,
	)
	return out, err
}

func (db *SqliteDB) GetTransferAttributionBreakdown(ctx context.Context, filter usage.Filter, groupBy string) ([]usage.Breakdown, error) {
	keyExpr, selectExpr := transferAttributionGroupExpr(groupBy)
	where, args := transferAttributionWhere(filter)
	query := fmt.Sprintf(`
		SELECT %s,
			COUNT(*),
			COALESCE(SUM(bytes_requested), 0),
			COALESCE(SUM(CASE WHEN direction = 'download' THEN bytes_requested ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN direction = 'upload' THEN bytes_requested ELSE 0 END), 0),
			MAX(event_time)
		FROM transfer_attribution_event%s
		GROUP BY %s
		ORDER BY MAX(event_time) DESC, key ASC
		LIMIT 1000
	`, selectExpr, where, keyExpr)
	rows, err := db.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanTransferAttributionBreakdown(rows)
}

func (db *SqliteDB) GetTransferAttributionSummaryByResources(ctx context.Context, filter usage.Filter, resources []string) (usage.Summary, error) {
	where, args := transferAttributionWhereByResources(filter, resources)
	var out usage.Summary
	err := db.db.QueryRowContext(ctx, `
		SELECT
			COUNT(*),
			COALESCE(SUM(CASE WHEN event_type = 'access_issued' THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN direction = 'download' THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN direction = 'upload' THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(bytes_requested), 0),
			COALESCE(SUM(CASE WHEN direction = 'download' THEN bytes_requested ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN direction = 'upload' THEN bytes_requested ELSE 0 END), 0)
		FROM transfer_attribution_event`+where, args...).Scan(
		&out.EventCount,
		&out.AccessIssuedCount,
		&out.DownloadEventCount,
		&out.UploadEventCount,
		&out.BytesRequested,
		&out.BytesDownloaded,
		&out.BytesUploaded,
	)
	return out, err
}

func (db *SqliteDB) GetTransferAttributionBreakdownByResources(ctx context.Context, filter usage.Filter, groupBy string, resources []string) ([]usage.Breakdown, error) {
	keyExpr, selectExpr := transferAttributionGroupExpr(groupBy)
	where, args := transferAttributionWhereByResources(filter, resources)
	query := fmt.Sprintf(`
		SELECT %s,
			COUNT(*),
			COALESCE(SUM(bytes_requested), 0),
			COALESCE(SUM(CASE WHEN direction = 'download' THEN bytes_requested ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN direction = 'upload' THEN bytes_requested ELSE 0 END), 0),
			MAX(event_time)
		FROM transfer_attribution_event%s
		GROUP BY %s
		ORDER BY MAX(event_time) DESC, key ASC
		LIMIT 1000
	`, selectExpr, where, keyExpr)
	rows, err := db.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanTransferAttributionBreakdown(rows)
}

func transferAttributionWhere(filter usage.Filter) (string, []any) {
	parts := make([]string, 0)
	args := make([]any, 0)
	add := func(clause string, value any) {
		parts = append(parts, clause)
		args = append(args, value)
	}
	if strings.TrimSpace(filter.Organization) != "" {
		add("organization = ?", strings.TrimSpace(filter.Organization))
	}
	if strings.TrimSpace(filter.Project) != "" {
		add("project = ?", strings.TrimSpace(filter.Project))
	}
	if strings.TrimSpace(filter.EventType) != "" && strings.TrimSpace(filter.EventType) != "all" {
		add("event_type = ?", strings.TrimSpace(filter.EventType))
	}
	direction := strings.TrimSpace(filter.Direction)
	if direction == "" {
		switch strings.TrimSpace(filter.EventType) {
		case usage.ProviderTransferDirectionDownload:
			direction = usage.ProviderTransferDirectionDownload
		case usage.ProviderTransferDirectionUpload:
			direction = usage.ProviderTransferDirectionUpload
		}
	}
	if direction != "" && direction != "all" {
		add("direction = ?", direction)
	}
	if filter.From != nil {
		add("event_time >= ?", filter.From.UTC())
	}
	if filter.To != nil {
		add("event_time <= ?", filter.To.UTC())
	}
	if strings.TrimSpace(filter.Provider) != "" {
		add("provider = ?", strings.TrimSpace(filter.Provider))
	}
	if strings.TrimSpace(filter.Bucket) != "" {
		add("bucket = ?", strings.TrimSpace(filter.Bucket))
	}
	if strings.TrimSpace(filter.SHA256) != "" {
		add("sha256 = ?", strings.TrimSpace(filter.SHA256))
	}
	if strings.TrimSpace(filter.User) != "" {
		user := strings.TrimSpace(filter.User)
		parts = append(parts, "(actor_email = ? OR actor_subject = ?)")
		args = append(args, user, user)
	}
	if len(parts) == 0 {
		return "", args
	}
	return " WHERE " + strings.Join(parts, " AND "), args
}

func transferAttributionWhereByResources(filter usage.Filter, resources []string) (string, []any) {
	where, args := transferAttributionWhere(filter)
	clause, clauseArgs := sqliteTransferResourceClause(resources)
	if clause == "" {
		if where == "" {
			return " WHERE 1 = 0", args
		}
		return where + " AND 1 = 0", args
	}
	if where == "" {
		return " WHERE " + clause, append(args, clauseArgs...)
	}
	return where + " AND (" + clause + ")", append(args, clauseArgs...)
}

func sqliteTransferResourceClause(resources []string) (string, []any) {
	resources = clientaccess.NormalizeAccessResources(resources)
	if len(resources) == 0 {
		return "", nil
	}

	orgOnly := make([]string, 0)
	orgSeen := make(map[string]struct{})
	projectClauses := make([]string, 0)
	args := make([]any, 0, len(resources)*2)
	for _, resource := range resources {
		org, project, ok := clientaccess.ResourceScope(resource)
		if !ok {
			continue
		}
		if project == "" {
			if _, exists := orgSeen[org]; exists {
				continue
			}
			orgSeen[org] = struct{}{}
			orgOnly = append(orgOnly, org)
			continue
		}
		projectClauses = append(projectClauses, "(organization = ? AND project = ?)")
		args = append(args, org, project)
	}

	clauses := make([]string, 0, 2)
	if len(orgOnly) > 0 {
		placeholders := make([]string, 0, len(orgOnly))
		for _, org := range orgOnly {
			placeholders = append(placeholders, "?")
			args = append(args, org)
		}
		clauses = append(clauses, "organization IN ("+strings.Join(placeholders, ",")+")")
	}
	if len(projectClauses) > 0 {
		clauses = append(clauses, strings.Join(projectClauses, " OR "))
	}
	return strings.Join(clauses, " OR "), args
}

func transferAttributionGroupExpr(groupBy string) (string, string) {
	switch strings.ToLower(strings.TrimSpace(groupBy)) {
	case "user":
		return "COALESCE(NULLIF(actor_email, ''), actor_subject), actor_email, actor_subject", "COALESCE(NULLIF(actor_email, ''), actor_subject) AS key, '' AS organization, '' AS project, '' AS provider, '' AS bucket, '' AS sha256, actor_email, actor_subject"
	case "provider":
		return "provider, bucket", "provider || ':' || bucket AS key, '' AS organization, '' AS project, provider, bucket, '' AS sha256, '' AS actor_email, '' AS actor_subject"
	case "object":
		return "sha256", "sha256 AS key, '' AS organization, '' AS project, '' AS provider, '' AS bucket, sha256, '' AS actor_email, '' AS actor_subject"
	default:
		return "organization, project", "organization || '/' || project AS key, organization, project, '' AS provider, '' AS bucket, '' AS sha256, '' AS actor_email, '' AS actor_subject"
	}
}

func normalizeTransferDirection(direction string) string {
	switch strings.ToLower(strings.TrimSpace(direction)) {
	case usage.ProviderTransferDirectionUpload:
		return usage.ProviderTransferDirectionUpload
	default:
		return usage.ProviderTransferDirectionDownload
	}
}

type transferRows interface {
	Next() bool
	Scan(dest ...any) error
	Err() error
}

func scanTransferAttributionBreakdown(rows transferRows) ([]usage.Breakdown, error) {
	out := make([]usage.Breakdown, 0)
	for rows.Next() {
		var item usage.Breakdown
		var last any
		if err := rows.Scan(
			&item.Key,
			&item.Organization,
			&item.Project,
			&item.Provider,
			&item.Bucket,
			&item.SHA256,
			&item.ActorEmail,
			&item.ActorSubject,
			&item.EventCount,
			&item.BytesRequested,
			&item.BytesDownloaded,
			&item.BytesUploaded,
			&last,
		); err != nil {
			return nil, err
		}
		if t, ok := parseSQLiteTransferTime(last); ok {
			item.LastTransferTime = &t
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func parseSQLiteTransferTime(value any) (time.Time, bool) {
	switch v := value.(type) {
	case time.Time:
		if v.IsZero() {
			return time.Time{}, false
		}
		return v.UTC(), true
	case string:
		return parseSQLiteTransferTimeString(v)
	case []byte:
		return parseSQLiteTransferTimeString(string(v))
	default:
		return time.Time{}, false
	}
}

func parseSQLiteTransferTimeString(raw string) (time.Time, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}, false
	}
	layouts := []string{
		time.RFC3339Nano,
		"2006-01-02 15:04:05.999999999-07:00",
		"2006-01-02 15:04:05.999999999Z07:00",
		"2006-01-02 15:04:05-07:00",
		"2006-01-02 15:04:05Z07:00",
		"2006-01-02 15:04:05.999999999",
		"2006-01-02 15:04:05",
	}
	for _, layout := range layouts {
		t, err := time.Parse(layout, raw)
		if err == nil {
			return t.UTC(), true
		}
	}
	return time.Time{}, false
}
