package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	sycommon "github.com/calypr/syfon/common"
	"github.com/calypr/syfon/internal/faults"
	"github.com/calypr/syfon/internal/usage"
)

func (db *SqliteDB) flushObjectUsageEventsForIDsTx(ctx context.Context, tx *sql.Tx, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	now := time.Now().UTC()
	// Account for the timestamp bind variable so a normal 1,000-record
	// RegisterObjects batch stays below SQLite's 900-parameter ceiling.
	for start := 0; start < len(ids); start += sqliteMaxParams - 1 {
		end := start + sqliteMaxParams - 1
		if end > len(ids) {
			end = len(ids)
		}
		if err := db.flushObjectUsageEventsForIDChunkTx(ctx, tx, ids[start:end], now); err != nil {
			return err
		}
	}
	return nil
}

func (db *SqliteDB) flushObjectUsageEventsForIDChunkTx(ctx context.Context, tx *sql.Tx, ids []string, now time.Time) error {
	placeholders := make([]string, len(ids))
	capArgs, err := safeSliceCapacity(len(ids), 1)
	if err != nil {
		return err
	}
	args := make([]interface{}, 0, capArgs)
	args = append(args, now)
	for i, id := range ids {
		placeholders[i] = "?"
		args = append(args, id)
	}
	inClause := strings.Join(placeholders, ",")
	query := fmt.Sprintf(`
		INSERT INTO object_usage (object_id, upload_count, download_count, last_upload_time, last_download_time, updated_time)
		SELECT e.object_id,
			COALESCE(SUM(CASE WHEN e.event_type = 'upload' THEN 1 ELSE 0 END), 0) AS upload_count,
			COALESCE(SUM(CASE WHEN e.event_type = 'download' THEN 1 ELSE 0 END), 0) AS download_count,
			MAX(CASE WHEN e.event_type = 'upload' THEN e.event_time END) AS last_upload_time,
			MAX(CASE WHEN e.event_type = 'download' THEN e.event_time END) AS last_download_time,
			?
		FROM object_usage_event e
		JOIN drs_object o ON o.id = e.object_id
		WHERE e.object_id IN (%s)
		GROUP BY e.object_id
		ON CONFLICT(object_id) DO UPDATE SET
			upload_count = object_usage.upload_count + excluded.upload_count,
			download_count = object_usage.download_count + excluded.download_count,
			last_upload_time = CASE
				WHEN excluded.last_upload_time IS NULL THEN object_usage.last_upload_time
				WHEN object_usage.last_upload_time IS NULL THEN excluded.last_upload_time
				WHEN excluded.last_upload_time > object_usage.last_upload_time THEN excluded.last_upload_time
				ELSE object_usage.last_upload_time
			END,
			last_download_time = CASE
				WHEN excluded.last_download_time IS NULL THEN object_usage.last_download_time
				WHEN object_usage.last_download_time IS NULL THEN excluded.last_download_time
				WHEN excluded.last_download_time > object_usage.last_download_time THEN excluded.last_download_time
				ELSE object_usage.last_download_time
			END,
			updated_time = excluded.updated_time
	`, inClause)
	if _, err := tx.ExecContext(ctx, query, args...); err != nil {
		return err
	}
	return execSQLiteDeleteByIDs(tx, "object_usage_event", ids)
}

func (db *SqliteDB) GetFileUsage(ctx context.Context, objectID string) (*usage.FileUsage, error) {
	if err := db.flushObjectUsageEvents(ctx); err != nil {
		return nil, err
	}
	var fileUsage usage.FileUsage
	var lastUpload sql.NullTime
	var lastDownload sql.NullTime
	err := db.db.QueryRowContext(ctx, `
		SELECT o.id, o.name, o.size,
			COALESCE(u.upload_count, 0),
			COALESCE(u.download_count, 0),
			u.last_upload_time,
			u.last_download_time
		FROM drs_object o
		LEFT JOIN object_usage u ON u.object_id = o.id
		WHERE o.id = ?
	`, objectID).Scan(
		&fileUsage.ObjectID,
		&fileUsage.Name,
		&fileUsage.Size,
		&fileUsage.UploadCount,
		&fileUsage.DownloadCount,
		&lastUpload,
		&lastDownload,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("%w: file usage not found", faults.ErrNotFound)
	}
	if err != nil {
		return nil, err
	}
	if lastUpload.Valid {
		t := lastUpload.Time
		fileUsage.LastUploadTime = &t
	}
	if lastDownload.Valid {
		t := lastDownload.Time
		fileUsage.LastDownloadTime = &t
	}
	fileUsage.LastAccessTime = latestTime(fileUsage.LastUploadTime, fileUsage.LastDownloadTime)
	return &fileUsage, nil
}

func (db *SqliteDB) ListFileUsageByObjectIDs(ctx context.Context, ids []string) ([]usage.FileUsage, error) {
	if len(ids) == 0 {
		return []usage.FileUsage{}, nil
	}
	if err := db.flushObjectUsageEvents(ctx); err != nil {
		return nil, err
	}

	out := make([]usage.FileUsage, 0, len(ids))
	for start := 0; start < len(ids); start += sqliteMaxParams {
		end := start + sqliteMaxParams
		if end > len(ids) {
			end = len(ids)
		}
		chunk := ids[start:end]
		args := make([]any, 0, len(chunk))
		for _, id := range chunk {
			args = append(args, id)
		}
		rows, err := db.db.QueryContext(ctx, `
			SELECT o.id, o.name, o.size,
				COALESCE(u.upload_count, 0),
				COALESCE(u.download_count, 0),
				u.last_upload_time,
				u.last_download_time
			FROM drs_object o
			LEFT JOIN object_usage u ON u.object_id = o.id
			WHERE o.id IN (`+makePlaceholders(len(chunk))+`)
			ORDER BY o.id
		`, args...)
		if err != nil {
			return nil, err
		}
		usages, err := scanFileUsageRows(rows, len(chunk))
		rows.Close()
		if err != nil {
			return nil, err
		}
		out = append(out, usages...)
	}
	return out, nil
}

func (db *SqliteDB) ListFileUsage(ctx context.Context, limit, offset int, inactiveSince *time.Time) ([]usage.FileUsage, error) {
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
		LEFT JOIN object_usage u ON u.object_id = o.id
	`
	args := []any{}
	if inactiveSince != nil {
		query += ` WHERE u.last_download_time IS NULL OR u.last_download_time < ?`
		args = append(args, inactiveSince.UTC())
	}
	query += ` ORDER BY COALESCE(u.last_download_time, '1970-01-01T00:00:00Z') ASC, o.id ASC LIMIT ? OFFSET ?`
	args = append(args, limit, offset)

	rows, err := db.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return scanFileUsageRows(rows, limit)
}

func (db *SqliteDB) ListFileUsagePageByScope(ctx context.Context, organization, project string, limit, offset int, inactiveSince *time.Time) ([]usage.FileUsage, error) {
	resource, err := sycommon.ResourcePath(strings.TrimSpace(organization), strings.TrimSpace(project))
	if err != nil {
		return nil, err
	}
	return db.listScopedFileUsagePage(ctx, []string{resource}, false, limit, offset, inactiveSince)
}

func (db *SqliteDB) ListFileUsagePageByResources(ctx context.Context, resources []string, includeUnscoped bool, limit, offset int, inactiveSince *time.Time) ([]usage.FileUsage, error) {
	return db.listScopedFileUsagePage(ctx, resources, includeUnscoped, limit, offset, inactiveSince)
}

func (db *SqliteDB) GetFileUsageSummaryByScope(ctx context.Context, organization, project string, inactiveSince *time.Time) (usage.FileUsageSummary, error) {
	resource, err := sycommon.ResourcePath(strings.TrimSpace(organization), strings.TrimSpace(project))
	if err != nil {
		return usage.FileUsageSummary{}, err
	}
	return db.getScopedFileUsageSummary(ctx, []string{resource}, false, inactiveSince)
}

func (db *SqliteDB) GetFileUsageSummaryByResources(ctx context.Context, resources []string, includeUnscoped bool, inactiveSince *time.Time) (usage.FileUsageSummary, error) {
	return db.getScopedFileUsageSummary(ctx, resources, includeUnscoped, inactiveSince)
}

func (db *SqliteDB) GetProjectRecordSummaryByScope(ctx context.Context, organization, project string) (usage.FileUsageSummary, error) {
	resource, err := sycommon.ResourcePath(strings.TrimSpace(organization), strings.TrimSpace(project))
	if err != nil {
		return usage.FileUsageSummary{}, err
	}
	var summary usage.FileUsageSummary
	var latest any
	if err := db.db.QueryRowContext(ctx, `
		SELECT COUNT(DISTINCT o.id), MAX(o.updated_time)
		FROM drs_object o
		INNER JOIN drs_object_controlled_access ca ON ca.object_id = o.id
		WHERE ca.resource = ?`, resource).Scan(&summary.RecordCount, &latest); err != nil {
		return usage.FileUsageSummary{}, err
	}
	if parsed, ok := parseSQLiteTransferTime(latest); ok {
		t := parsed.UTC()
		summary.RecordLatestUpdatedTime = &t
	}
	return summary, nil
}

func scanFileUsageRows(rows *sql.Rows, capacity int) ([]usage.FileUsage, error) {
	out := make([]usage.FileUsage, 0, capacity)
	for rows.Next() {
		var fileUsage usage.FileUsage
		var lastUpload sql.NullTime
		var lastDownload sql.NullTime
		if err := rows.Scan(
			&fileUsage.ObjectID,
			&fileUsage.Name,
			&fileUsage.Size,
			&fileUsage.UploadCount,
			&fileUsage.DownloadCount,
			&lastUpload,
			&lastDownload,
		); err != nil {
			return nil, err
		}
		if lastUpload.Valid {
			t := lastUpload.Time
			fileUsage.LastUploadTime = &t
		}
		if lastDownload.Valid {
			t := lastDownload.Time
			fileUsage.LastDownloadTime = &t
		}
		fileUsage.LastAccessTime = latestTime(fileUsage.LastUploadTime, fileUsage.LastDownloadTime)
		out = append(out, fileUsage)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func (db *SqliteDB) listScopedFileUsagePage(ctx context.Context, resources []string, includeUnscoped bool, limit, offset int, inactiveSince *time.Time) ([]usage.FileUsage, error) {
	if err := db.flushObjectUsageEvents(ctx); err != nil {
		return nil, err
	}
	if limit <= 0 {
		return []usage.FileUsage{}, nil
	}
	if offset < 0 {
		offset = 0
	}
	resources = sycommon.NormalizeAccessResources(resources)
	if len(resources) == 0 && !includeUnscoped {
		return []usage.FileUsage{}, nil
	}

	query, args := sqliteScopedFileUsageQuery(resources, includeUnscoped, inactiveSince, false)
	query += ` ORDER BY COALESCE(u.last_download_time, '1970-01-01T00:00:00Z') ASC, o.id ASC LIMIT ? OFFSET ?`
	args = append(args, limit, offset)
	rows, err := db.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanFileUsageRows(rows, limit)
}

func (db *SqliteDB) getScopedFileUsageSummary(ctx context.Context, resources []string, includeUnscoped bool, inactiveSince *time.Time) (usage.FileUsageSummary, error) {
	if err := db.flushObjectUsageEvents(ctx); err != nil {
		return usage.FileUsageSummary{}, err
	}
	resources = sycommon.NormalizeAccessResources(resources)
	if len(resources) == 0 && !includeUnscoped {
		return usage.FileUsageSummary{}, nil
	}

	query, args := sqliteScopedFileUsageQuery(resources, includeUnscoped, inactiveSince, true)
	var summary usage.FileUsageSummary
	if err := db.db.QueryRowContext(ctx, query, args...).Scan(
		&summary.TotalFiles,
		&summary.TotalUploads,
		&summary.TotalDownloads,
		&summary.InactiveFileCount,
	); err != nil {
		return usage.FileUsageSummary{}, err
	}
	return summary, nil
}

func sqliteScopedFileUsageQuery(resources []string, includeUnscoped bool, inactiveSince *time.Time, summary bool) (string, []any) {
	args := make([]any, 0, len(resources)+2)
	parts := make([]string, 0, 2)
	if len(resources) > 0 {
		placeholders := make([]string, 0, len(resources))
		for _, resource := range resources {
			args = append(args, resource)
			placeholders = append(placeholders, "?")
		}
		parts = append(parts, `EXISTS (
			SELECT 1
			FROM drs_object_controlled_access ca
			WHERE ca.object_id = o.id AND ca.resource IN (`+strings.Join(placeholders, ",")+`)
		)`)
	}
	if includeUnscoped {
		parts = append(parts, `NOT EXISTS (
			SELECT 1
			FROM drs_object_controlled_access ca
			WHERE ca.object_id = o.id
		)`)
	}

	var selectClause string
	if summary {
		if inactiveSince != nil {
			args = append(args, inactiveSince.UTC())
			selectClause = `
		SELECT
			COUNT(o.id) AS total_files,
			COALESCE(SUM(COALESCE(u.upload_count, 0)), 0) AS total_uploads,
			COALESCE(SUM(COALESCE(u.download_count, 0)), 0) AS total_downloads,
			COALESCE(SUM(CASE WHEN u.last_download_time IS NULL OR u.last_download_time < ? THEN 1 ELSE 0 END), 0) AS inactive_files`
		} else {
			selectClause = `
		SELECT
			COUNT(o.id) AS total_files,
			COALESCE(SUM(COALESCE(u.upload_count, 0)), 0) AS total_uploads,
			COALESCE(SUM(COALESCE(u.download_count, 0)), 0) AS total_downloads,
			0 AS inactive_files`
		}
	} else {
		selectClause = `
		SELECT o.id, o.name, o.size,
			COALESCE(u.upload_count, 0),
			COALESCE(u.download_count, 0),
			u.last_upload_time,
			u.last_download_time`
	}

	query := selectClause + `
		FROM drs_object o
		LEFT JOIN object_usage u ON u.object_id = o.id
		WHERE ((` + strings.Join(parts, " OR ") + `))`
	if !summary && inactiveSince != nil {
		args = append(args, inactiveSince.UTC())
		query += ` AND (u.last_download_time IS NULL OR u.last_download_time < ?)`
	}
	return query, args
}

func (db *SqliteDB) GetFileUsageSummary(ctx context.Context, inactiveSince *time.Time) (usage.FileUsageSummary, error) {
	if err := db.flushObjectUsageEvents(ctx); err != nil {
		return usage.FileUsageSummary{}, err
	}
	summary := usage.FileUsageSummary{}
	query := `
		SELECT
			COUNT(o.id) AS total_files,
			COALESCE(SUM(COALESCE(u.upload_count, 0)), 0) AS total_uploads,
			COALESCE(SUM(COALESCE(u.download_count, 0)), 0) AS total_downloads,
			COALESCE(SUM(CASE WHEN u.last_download_time IS NULL OR u.last_download_time < ? THEN 1 ELSE 0 END), 0) AS inactive_files
		FROM drs_object o
		LEFT JOIN object_usage u ON u.object_id = o.id
	`
	inactiveCutoff := time.Now().UTC().AddDate(0, 0, -730)
	if inactiveSince != nil {
		inactiveCutoff = inactiveSince.UTC()
	}
	if err := db.db.QueryRowContext(ctx, query, inactiveCutoff).Scan(
		&summary.TotalFiles,
		&summary.TotalUploads,
		&summary.TotalDownloads,
		&summary.InactiveFileCount,
	); err != nil {
		return usage.FileUsageSummary{}, err
	}
	return summary, nil
}

func (db *SqliteDB) flushObjectUsageEvents(ctx context.Context) error {
	tx, err := db.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	rows, err := tx.QueryContext(ctx, `
		SELECT DISTINCT e.object_id
		FROM object_usage_event e
		JOIN drs_object o ON o.id = e.object_id
	`)
	if err != nil {
		return err
	}
	ids := make([]string, 0)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()

	if err := db.flushObjectUsageEventsForIDsTx(ctx, tx, ids); err != nil {
		return err
	}
	return tx.Commit()
}

func (db *SqliteDB) RecordFileUpload(ctx context.Context, objectID string) error {
	now := time.Now().UTC()
	_, err := db.db.ExecContext(ctx, `
		INSERT INTO object_usage_event (object_id, event_type, event_time)
		VALUES (?, 'upload', ?)
	`, objectID, now)
	return err
}

func (db *SqliteDB) RecordFileDownload(ctx context.Context, objectID string) error {
	now := time.Now().UTC()
	_, err := db.db.ExecContext(ctx, `
		INSERT INTO object_usage_event (object_id, event_type, event_time)
		VALUES (?, 'download', ?)
	`, objectID, now)
	return err
}
