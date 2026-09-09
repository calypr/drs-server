package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/calypr/syfon/apigen/errorapi"
	clientaccess "github.com/calypr/syfon/client/access"
	"github.com/calypr/syfon/internal/usage"
)

func (db *Store) RecordFileUpload(ctx context.Context, objectID string) error {
	_, err := db.execContext(ctx, `
		INSERT INTO object_usage_event (object_id, event_type, event_time)
		VALUES (?, 'upload', ?)
	`, objectID, time.Now().UTC())
	return err
}

func (db *Store) RecordFileDownload(ctx context.Context, objectID string) error {
	_, err := db.execContext(ctx, `
		INSERT INTO object_usage_event (object_id, event_type, event_time)
		VALUES (?, 'download', ?)
	`, objectID, time.Now().UTC())
	return err
}

func (db *Store) GetFileUsage(ctx context.Context, objectID string) (*usage.FileUsage, error) {
	if err := db.flushObjectUsageEvents(ctx); err != nil {
		return nil, err
	}
	var item usage.FileUsage
	var lastUpload, lastDownload sql.NullTime
	err := db.queryRowContext(ctx, `
		SELECT o.id, o.name, o.size,
			COALESCE(u.upload_count, 0),
			COALESCE(u.download_count, 0),
			u.last_upload_time,
			u.last_download_time
		FROM drs_object o
		LEFT JOIN object_usage u ON u.object_id = o.id
		WHERE o.id = ?
	`, objectID).Scan(
		&item.ObjectID, &item.Name, &item.Size,
		&item.UploadCount, &item.DownloadCount,
		&lastUpload, &lastDownload,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, errorapi.ErrFileUsageNotFound
	}
	if err != nil {
		return nil, err
	}
	item.LastUploadTime = nullableUsageTime(lastUpload)
	item.LastDownloadTime = nullableUsageTime(lastDownload)
	item.LastAccessTime = latestUsageTime(item.LastUploadTime, item.LastDownloadTime)
	return &item, nil
}

func (db *Store) ListFileUsageByObjectIDs(ctx context.Context, ids []string) ([]usage.FileUsage, error) {
	if len(ids) == 0 {
		return []usage.FileUsage{}, nil
	}
	if err := db.flushObjectUsageEvents(ctx); err != nil {
		return nil, err
	}
	maxIDs := db.dialect.MaxParameters()
	if maxIDs <= 0 {
		maxIDs = len(ids)
	}
	out := make([]usage.FileUsage, 0, len(ids))
	for start := 0; start < len(ids); start += maxIDs {
		end := start + maxIDs
		if end > len(ids) {
			end = len(ids)
		}
		chunk := ids[start:end]
		condition, args := db.dialect.ListArgs("o.id", chunk)
		rows, err := db.queryContext(ctx, `
			SELECT o.id, o.name, o.size,
				COALESCE(u.upload_count, 0),
				COALESCE(u.download_count, 0),
				u.last_upload_time,
				u.last_download_time
			FROM drs_object o
			LEFT JOIN object_usage u ON u.object_id = o.id
			WHERE `+condition+`
			ORDER BY o.id
		`, args...)
		if err != nil {
			return nil, err
		}
		items, err := scanFileUsageRows(rows, len(chunk))
		rows.Close()
		if err != nil {
			return nil, err
		}
		out = append(out, items...)
	}
	return out, nil
}

func (db *Store) ListFileUsage(ctx context.Context, limit, offset int, inactiveSince *time.Time) ([]usage.FileUsage, error) {
	if err := db.flushObjectUsageEvents(ctx); err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = 200
	}
	if limit > 1000 {
		limit = 1000
	}
	if offset < 0 {
		offset = 0
	}
	query := `
		SELECT o.id, o.name, o.size,
			COALESCE(u.upload_count, 0),
			COALESCE(u.download_count, 0),
			u.last_upload_time,
			u.last_download_time
		FROM drs_object o
		LEFT JOIN object_usage u ON u.object_id = o.id`
	args := make([]any, 0, 3)
	if inactiveSince != nil {
		query += ` WHERE u.last_download_time IS NULL OR u.last_download_time < ?`
		args = append(args, inactiveSince.UTC())
	}
	query += ` ORDER BY COALESCE(u.last_download_time, '1970-01-01T00:00:00Z') ASC, o.id ASC LIMIT ? OFFSET ?`
	args = append(args, limit, offset)
	rows, err := db.queryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanFileUsageRows(rows, limit)
}

func (db *Store) ListFileUsagePageByScope(ctx context.Context, organization, project string, limit, offset int, inactiveSince *time.Time) ([]usage.FileUsage, error) {
	resource, err := clientaccess.ResourcePath(strings.TrimSpace(organization), strings.TrimSpace(project))
	if err != nil {
		return nil, err
	}
	return db.listScopedFileUsagePage(ctx, []string{resource}, false, limit, offset, inactiveSince)
}

func (db *Store) ListFileUsagePageByResources(ctx context.Context, resources []string, includeUnscoped bool, limit, offset int, inactiveSince *time.Time) ([]usage.FileUsage, error) {
	return db.listScopedFileUsagePage(ctx, resources, includeUnscoped, limit, offset, inactiveSince)
}

func (db *Store) GetFileUsageSummaryByScope(ctx context.Context, organization, project string, inactiveSince *time.Time) (usage.FileUsageSummary, error) {
	resource, err := clientaccess.ResourcePath(strings.TrimSpace(organization), strings.TrimSpace(project))
	if err != nil {
		return usage.FileUsageSummary{}, err
	}
	return db.getScopedFileUsageSummary(ctx, []string{resource}, false, inactiveSince)
}

func (db *Store) GetFileUsageSummaryByResources(ctx context.Context, resources []string, includeUnscoped bool, inactiveSince *time.Time) (usage.FileUsageSummary, error) {
	return db.getScopedFileUsageSummary(ctx, resources, includeUnscoped, inactiveSince)
}

func (db *Store) GetProjectRecordSummaryByScope(ctx context.Context, organization, project string) (usage.FileUsageSummary, error) {
	resource, err := clientaccess.ResourcePath(strings.TrimSpace(organization), strings.TrimSpace(project))
	if err != nil {
		return usage.FileUsageSummary{}, err
	}
	var summary usage.FileUsageSummary
	var latest any
	if err := db.queryRowContext(ctx, `
		SELECT COUNT(DISTINCT o.id), MAX(o.updated_time)
		FROM drs_object o
		INNER JOIN drs_object_controlled_access ca ON ca.object_id = o.id
		WHERE ca.resource = ?
	`, resource).Scan(&summary.RecordCount, &latest); err != nil {
		return usage.FileUsageSummary{}, err
	}
	if parsed, ok := parseSQLiteTransferTime(latest); ok {
		t := parsed.UTC()
		summary.RecordLatestUpdatedTime = &t
	}
	return summary, nil
}

func (db *Store) GetFileUsageSummary(ctx context.Context, inactiveSince *time.Time) (usage.FileUsageSummary, error) {
	if err := db.flushObjectUsageEvents(ctx); err != nil {
		return usage.FileUsageSummary{}, err
	}
	cutoff := time.Now().UTC().AddDate(0, 0, -730)
	if inactiveSince != nil {
		cutoff = inactiveSince.UTC()
	}
	var summary usage.FileUsageSummary
	if err := db.queryRowContext(ctx, `
		SELECT
			COUNT(o.id),
			COALESCE(SUM(COALESCE(u.upload_count, 0)), 0),
			COALESCE(SUM(COALESCE(u.download_count, 0)), 0),
			COALESCE(SUM(CASE WHEN u.last_download_time IS NULL OR u.last_download_time < ? THEN 1 ELSE 0 END), 0)
		FROM drs_object o
		LEFT JOIN object_usage u ON u.object_id = o.id
	`, cutoff).Scan(&summary.TotalFiles, &summary.TotalUploads, &summary.TotalDownloads, &summary.InactiveFileCount); err != nil {
		return usage.FileUsageSummary{}, err
	}
	return summary, nil
}

func (db *Store) listScopedFileUsagePage(ctx context.Context, resources []string, includeUnscoped bool, limit, offset int, inactiveSince *time.Time) ([]usage.FileUsage, error) {
	if err := db.flushObjectUsageEvents(ctx); err != nil {
		return nil, err
	}
	if limit <= 0 {
		return []usage.FileUsage{}, nil
	}
	if offset < 0 {
		offset = 0
	}
	resources = clientaccess.NormalizeAccessResources(resources)
	if len(resources) == 0 && !includeUnscoped {
		return []usage.FileUsage{}, nil
	}
	query, args := db.scopedFileUsageQuery(resources, includeUnscoped, inactiveSince, false)
	query += ` ORDER BY COALESCE(u.last_download_time, '1970-01-01T00:00:00Z') ASC, o.id ASC LIMIT ? OFFSET ?`
	args = append(args, limit, offset)
	rows, err := db.queryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanFileUsageRows(rows, limit)
}

func (db *Store) getScopedFileUsageSummary(ctx context.Context, resources []string, includeUnscoped bool, inactiveSince *time.Time) (usage.FileUsageSummary, error) {
	if err := db.flushObjectUsageEvents(ctx); err != nil {
		return usage.FileUsageSummary{}, err
	}
	resources = clientaccess.NormalizeAccessResources(resources)
	if len(resources) == 0 && !includeUnscoped {
		return usage.FileUsageSummary{}, nil
	}
	query, args := db.scopedFileUsageQuery(resources, includeUnscoped, inactiveSince, true)
	var summary usage.FileUsageSummary
	if err := db.queryRowContext(ctx, query, args...).Scan(&summary.TotalFiles, &summary.TotalUploads, &summary.TotalDownloads, &summary.InactiveFileCount); err != nil {
		return usage.FileUsageSummary{}, err
	}
	return summary, nil
}

func (db *Store) scopedFileUsageQuery(resources []string, includeUnscoped bool, inactiveSince *time.Time, summary bool) (string, []any) {
	parts := make([]string, 0, 2)
	args := make([]any, 0, len(resources)+2)
	if len(resources) > 0 {
		clause, clauseArgs := db.dialect.ListArgs("ca.resource", resources)
		parts = append(parts, `EXISTS (
			SELECT 1
			FROM drs_object_controlled_access ca
			WHERE ca.object_id = o.id AND `+clause+`
		)`)
		args = append(args, clauseArgs...)
	}
	if includeUnscoped {
		parts = append(parts, `? AND NOT EXISTS (
			SELECT 1
			FROM drs_object_controlled_access ca
			WHERE ca.object_id = o.id
		)`)
		args = append(args, includeUnscoped)
	}
	var selectClause string
	if summary {
		inactive := "0 AS inactive_files"
		if inactiveSince != nil {
			args = append(args, inactiveSince.UTC())
			inactive = "COALESCE(SUM(CASE WHEN u.last_download_time IS NULL OR u.last_download_time < ? THEN 1 ELSE 0 END), 0) AS inactive_files"
		}
		selectClause = `SELECT COUNT(o.id),
			COALESCE(SUM(COALESCE(u.upload_count, 0)), 0),
			COALESCE(SUM(COALESCE(u.download_count, 0)), 0), ` + inactive
	} else {
		selectClause = `SELECT o.id, o.name, o.size,
			COALESCE(u.upload_count, 0),
			COALESCE(u.download_count, 0),
			u.last_upload_time,
			u.last_download_time`
	}
	query := selectClause + `
		FROM drs_object o
		LEFT JOIN object_usage u ON u.object_id = o.id
		WHERE (` + strings.Join(parts, " OR ") + ")"
	if !summary && inactiveSince != nil {
		args = append(args, inactiveSince.UTC())
		query += ` AND (u.last_download_time IS NULL OR u.last_download_time < ?)`
	}
	return query, args
}

func (db *Store) flushObjectUsageEvents(ctx context.Context) error {
	tx, err := db.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	rows, err := db.txQueryContext(ctx, tx, `
		SELECT DISTINCT e.object_id
		FROM object_usage_event e
		JOIN drs_object o ON o.id = e.object_id
	`)
	if err != nil {
		return err
	}
	ids, err := scanObjectIDs(rows)
	rows.Close()
	if err != nil {
		return err
	}
	maxIDs := db.dialect.MaxParameters() - 1
	if maxIDs <= 0 {
		maxIDs = len(ids)
	}
	for start := 0; start < len(ids); start += maxIDs {
		end := start + maxIDs
		if end > len(ids) {
			end = len(ids)
		}
		if err := db.flushObjectUsageEventsForIDsTx(ctx, tx, ids[start:end]); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func scanFileUsageRows(rows *sql.Rows, capacity int) ([]usage.FileUsage, error) {
	out := make([]usage.FileUsage, 0, capacity)
	for rows.Next() {
		var item usage.FileUsage
		var lastUpload, lastDownload sql.NullTime
		if err := rows.Scan(&item.ObjectID, &item.Name, &item.Size, &item.UploadCount, &item.DownloadCount, &lastUpload, &lastDownload); err != nil {
			return nil, err
		}
		item.LastUploadTime = nullableUsageTime(lastUpload)
		item.LastDownloadTime = nullableUsageTime(lastDownload)
		item.LastAccessTime = latestUsageTime(item.LastUploadTime, item.LastDownloadTime)
		out = append(out, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func nullableUsageTime(value sql.NullTime) *time.Time {
	if !value.Valid {
		return nil
	}
	t := value.Time
	return &t
}

func latestUsageTime(values ...*time.Time) *time.Time {
	var latest *time.Time
	for _, value := range values {
		if value == nil {
			continue
		}
		if latest == nil || value.After(*latest) {
			copyValue := *value
			latest = &copyValue
		}
	}
	return latest
}

var _ usage.ReportStore = (*Store)(nil)
var _ usage.Ingestor = (*Store)(nil)
