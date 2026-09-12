package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/calypr/syfon/apigen/errorapi"
	"github.com/calypr/syfon/apigen/metricsapi"
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

func (db *Store) GetFileUsage(ctx context.Context, objectID string) (*metricsapi.FileUsage, error) {
	if err := db.flushObjectUsageEvents(ctx); err != nil {
		return nil, err
	}
	var item metricsapi.FileUsage
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
		&item.ObjectId, &item.Name, &item.Size,
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

func (db *Store) ListFileUsageByObjectIDs(ctx context.Context, ids []string) ([]metricsapi.FileUsage, error) {
	if len(ids) == 0 {
		return []metricsapi.FileUsage{}, nil
	}
	if err := db.flushObjectUsageEvents(ctx); err != nil {
		return nil, err
	}
	condition, args := db.dialect.ListArgs("o.id", ids)
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
	defer rows.Close()
	return scanFileUsageRows(rows, len(ids))
}

func (db *Store) ListFileUsage(ctx context.Context, limit, offset int, inactiveSince *time.Time) ([]metricsapi.FileUsage, error) {
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

func (db *Store) ListFileUsagePageByScope(ctx context.Context, organization, project string, limit, offset int, inactiveSince *time.Time) ([]metricsapi.FileUsage, error) {
	resource, err := clientaccess.ResourcePath(strings.TrimSpace(organization), strings.TrimSpace(project))
	if err != nil {
		return nil, err
	}
	return db.listScopedFileUsagePage(ctx, []string{resource}, false, limit, offset, inactiveSince)
}

func (db *Store) ListFileUsagePageByResources(ctx context.Context, resources []string, includeUnscoped bool, limit, offset int, inactiveSince *time.Time) ([]metricsapi.FileUsage, error) {
	return db.listScopedFileUsagePage(ctx, resources, includeUnscoped, limit, offset, inactiveSince)
}

func (db *Store) GetFileUsageSummaryByScope(ctx context.Context, organization, project string, inactiveSince *time.Time) (metricsapi.FileUsageSummary, error) {
	resource, err := clientaccess.ResourcePath(strings.TrimSpace(organization), strings.TrimSpace(project))
	if err != nil {
		return metricsapi.FileUsageSummary{}, err
	}
	return db.getScopedFileUsageSummary(ctx, []string{resource}, false, inactiveSince)
}

func (db *Store) GetFileUsageSummaryByResources(ctx context.Context, resources []string, includeUnscoped bool, inactiveSince *time.Time) (metricsapi.FileUsageSummary, error) {
	return db.getScopedFileUsageSummary(ctx, resources, includeUnscoped, inactiveSince)
}

func (db *Store) GetProjectRecordSummaryByScope(ctx context.Context, organization, project string) (metricsapi.FileUsageSummary, error) {
	resource, err := clientaccess.ResourcePath(strings.TrimSpace(organization), strings.TrimSpace(project))
	if err != nil {
		return metricsapi.FileUsageSummary{}, err
	}
	var summary metricsapi.FileUsageSummary
	var latest any
	if err := db.queryRowContext(ctx, `
		SELECT COUNT(DISTINCT o.id), MAX(o.updated_time)
		FROM drs_object o
		INNER JOIN drs_object_controlled_access ca ON ca.object_id = o.id
		WHERE ca.resource = ?
	`, resource).Scan(&summary.RecordCount, &latest); err != nil {
		return metricsapi.FileUsageSummary{}, err
	}
	if parsed, ok := parseSQLiteTransferTime(latest); ok {
		t := parsed.UTC()
		summary.RecordLatestUpdatedTime = &t
	}
	return summary, nil
}

func (db *Store) GetFileUsageSummary(ctx context.Context, inactiveSince *time.Time) (metricsapi.FileUsageSummary, error) {
	if err := db.flushObjectUsageEvents(ctx); err != nil {
		return metricsapi.FileUsageSummary{}, err
	}
	cutoff := time.Now().UTC().AddDate(0, 0, -730)
	if inactiveSince != nil {
		cutoff = inactiveSince.UTC()
	}
	var summary metricsapi.FileUsageSummary
	if err := db.queryRowContext(ctx, `
		SELECT
			COUNT(o.id),
			COALESCE(SUM(COALESCE(u.upload_count, 0)), 0),
			COALESCE(SUM(COALESCE(u.download_count, 0)), 0),
			COALESCE(SUM(CASE WHEN u.last_download_time IS NULL OR u.last_download_time < ? THEN 1 ELSE 0 END), 0)
		FROM drs_object o
		LEFT JOIN object_usage u ON u.object_id = o.id
	`, cutoff).Scan(&summary.TotalFiles, &summary.TotalUploads, &summary.TotalDownloads, &summary.InactiveFileCount); err != nil {
		return metricsapi.FileUsageSummary{}, err
	}
	return summary, nil
}

func (db *Store) listScopedFileUsagePage(ctx context.Context, resources []string, includeUnscoped bool, limit, offset int, inactiveSince *time.Time) ([]metricsapi.FileUsage, error) {
	if err := db.flushObjectUsageEvents(ctx); err != nil {
		return nil, err
	}
	if limit <= 0 {
		return []metricsapi.FileUsage{}, nil
	}
	if offset < 0 {
		offset = 0
	}
	resources = clientaccess.NormalizeAccessResources(resources)
	if len(resources) == 0 && !includeUnscoped {
		return []metricsapi.FileUsage{}, nil
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

func (db *Store) getScopedFileUsageSummary(ctx context.Context, resources []string, includeUnscoped bool, inactiveSince *time.Time) (metricsapi.FileUsageSummary, error) {
	if err := db.flushObjectUsageEvents(ctx); err != nil {
		return metricsapi.FileUsageSummary{}, err
	}
	resources = clientaccess.NormalizeAccessResources(resources)
	if len(resources) == 0 && !includeUnscoped {
		return metricsapi.FileUsageSummary{}, nil
	}
	query, args := db.scopedFileUsageQuery(resources, includeUnscoped, inactiveSince, true)
	var summary metricsapi.FileUsageSummary
	if err := db.queryRowContext(ctx, query, args...).Scan(&summary.TotalFiles, &summary.TotalUploads, &summary.TotalDownloads, &summary.InactiveFileCount); err != nil {
		return metricsapi.FileUsageSummary{}, err
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
	if err := db.flushObjectUsageEventsForIDsTx(ctx, tx, ids); err != nil {
		return err
	}
	return tx.Commit()
}

func scanFileUsageRows(rows *sql.Rows, capacity int) ([]metricsapi.FileUsage, error) {
	out := make([]metricsapi.FileUsage, 0, capacity)
	for rows.Next() {
		var item metricsapi.FileUsage
		var lastUpload, lastDownload sql.NullTime
		if err := rows.Scan(&item.ObjectId, &item.Name, &item.Size, &item.UploadCount, &item.DownloadCount, &lastUpload, &lastDownload); err != nil {
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
