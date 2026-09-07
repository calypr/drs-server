package postgres

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	sycommon "github.com/calypr/syfon/common"
	"github.com/calypr/syfon/internal/faults"
	"github.com/calypr/syfon/internal/models"

	"github.com/lib/pq"
)

func (db *PostgresDB) RecordFileUpload(ctx context.Context, objectID string) error {
	now := time.Now().UTC()
	_, err := db.db.ExecContext(ctx, `
		INSERT INTO object_usage_event (object_id, event_type, event_time)
		VALUES ($1, 'upload', $2)
	`, objectID, now)
	return err
}

func (db *PostgresDB) RecordFileDownload(ctx context.Context, objectID string) error {
	now := time.Now().UTC()
	_, err := db.db.ExecContext(ctx, `
		INSERT INTO object_usage_event (object_id, event_type, event_time)
		VALUES ($1, 'download', $2)
	`, objectID, now)
	return err
}

func (db *PostgresDB) GetFileUsage(ctx context.Context, objectID string) (*models.FileUsage, error) {
	if err := db.flushObjectUsageEvents(ctx); err != nil {
		return nil, err
	}
	var usage models.FileUsage
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
		WHERE o.id = $1
	`, objectID).Scan(
		&usage.ObjectID,
		&usage.Name,
		&usage.Size,
		&usage.UploadCount,
		&usage.DownloadCount,
		&lastUpload,
		&lastDownload,
	)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("%w: file usage not found", faults.ErrNotFound)
	}
	if err != nil {
		return nil, err
	}
	if lastUpload.Valid {
		t := lastUpload.Time
		usage.LastUploadTime = &t
	}
	if lastDownload.Valid {
		t := lastDownload.Time
		usage.LastDownloadTime = &t
	}
	usage.LastAccessTime = latestUsageTime(usage.LastUploadTime, usage.LastDownloadTime)
	return &usage, nil
}

func (db *PostgresDB) ListFileUsageByObjectIDs(ctx context.Context, ids []string) ([]models.FileUsage, error) {
	if len(ids) == 0 {
		return []models.FileUsage{}, nil
	}
	if err := db.flushObjectUsageEvents(ctx); err != nil {
		return nil, err
	}
	rows, err := db.db.QueryContext(ctx, `
		SELECT o.id, o.name, o.size,
			COALESCE(u.upload_count, 0),
			COALESCE(u.download_count, 0),
			u.last_upload_time,
			u.last_download_time
		FROM drs_object o
		LEFT JOIN object_usage u ON u.object_id = o.id
		WHERE o.id = ANY($1)
		ORDER BY o.id
	`, pq.Array(ids))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanFileUsageRows(rows, len(ids))
}

func (db *PostgresDB) ListFileUsage(ctx context.Context, limit, offset int, inactiveSince *time.Time) ([]models.FileUsage, error) {
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
		query += ` WHERE u.last_download_time IS NULL OR u.last_download_time < $1`
		args = append(args, inactiveSince.UTC())
	}
	query += fmt.Sprintf(` ORDER BY COALESCE(u.last_download_time, '1970-01-01T00:00:00Z') ASC, o.id ASC LIMIT $%d OFFSET $%d`, len(args)+1, len(args)+2)
	args = append(args, limit, offset)

	rows, err := db.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return scanFileUsageRows(rows, limit)
}

func (db *PostgresDB) ListFileUsagePageByScope(ctx context.Context, organization, project string, limit, offset int, inactiveSince *time.Time) ([]models.FileUsage, error) {
	resource, err := sycommon.ResourcePath(strings.TrimSpace(organization), strings.TrimSpace(project))
	if err != nil {
		return nil, err
	}
	return db.listScopedFileUsagePage(ctx, []string{resource}, false, limit, offset, inactiveSince)
}

func (db *PostgresDB) ListFileUsagePageByResources(ctx context.Context, resources []string, includeUnscoped bool, limit, offset int, inactiveSince *time.Time) ([]models.FileUsage, error) {
	return db.listScopedFileUsagePage(ctx, resources, includeUnscoped, limit, offset, inactiveSince)
}

func (db *PostgresDB) GetFileUsageSummaryByScope(ctx context.Context, organization, project string, inactiveSince *time.Time) (models.FileUsageSummary, error) {
	resource, err := sycommon.ResourcePath(strings.TrimSpace(organization), strings.TrimSpace(project))
	if err != nil {
		return models.FileUsageSummary{}, err
	}
	return db.getScopedFileUsageSummary(ctx, []string{resource}, false, inactiveSince)
}

func (db *PostgresDB) GetFileUsageSummaryByResources(ctx context.Context, resources []string, includeUnscoped bool, inactiveSince *time.Time) (models.FileUsageSummary, error) {
	return db.getScopedFileUsageSummary(ctx, resources, includeUnscoped, inactiveSince)
}

func (db *PostgresDB) GetProjectRecordSummaryByScope(ctx context.Context, organization, project string) (models.FileUsageSummary, error) {
	resource, err := sycommon.ResourcePath(strings.TrimSpace(organization), strings.TrimSpace(project))
	if err != nil {
		return models.FileUsageSummary{}, err
	}
	var summary models.FileUsageSummary
	var latest sql.NullTime
	if err := db.db.QueryRowContext(ctx, `
		SELECT COUNT(DISTINCT o.id), MAX(o.updated_time)
		FROM drs_object o
		INNER JOIN drs_object_controlled_access ca ON ca.object_id = o.id
		WHERE ca.resource = $1`, resource).Scan(&summary.RecordCount, &latest); err != nil {
		return models.FileUsageSummary{}, err
	}
	if latest.Valid {
		t := latest.Time.UTC()
		summary.RecordLatestUpdatedTime = &t
	}
	return summary, nil
}

func scanFileUsageRows(rows *sql.Rows, capacity int) ([]models.FileUsage, error) {
	out := make([]models.FileUsage, 0, capacity)
	for rows.Next() {
		var usage models.FileUsage
		var lastUpload sql.NullTime
		var lastDownload sql.NullTime
		if err := rows.Scan(
			&usage.ObjectID,
			&usage.Name,
			&usage.Size,
			&usage.UploadCount,
			&usage.DownloadCount,
			&lastUpload,
			&lastDownload,
		); err != nil {
			return nil, err
		}
		if lastUpload.Valid {
			t := lastUpload.Time
			usage.LastUploadTime = &t
		}
		if lastDownload.Valid {
			t := lastDownload.Time
			usage.LastDownloadTime = &t
		}
		usage.LastAccessTime = latestUsageTime(usage.LastUploadTime, usage.LastDownloadTime)
		out = append(out, usage)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func (db *PostgresDB) listScopedFileUsagePage(ctx context.Context, resources []string, includeUnscoped bool, limit, offset int, inactiveSince *time.Time) ([]models.FileUsage, error) {
	if err := db.flushObjectUsageEvents(ctx); err != nil {
		return nil, err
	}
	if limit <= 0 {
		return []models.FileUsage{}, nil
	}
	if offset < 0 {
		offset = 0
	}
	resources = sycommon.NormalizeAccessResources(resources)
	if len(resources) == 0 && !includeUnscoped {
		return []models.FileUsage{}, nil
	}

	query, args := postgresScopedFileUsageQuery(resources, includeUnscoped, inactiveSince, false)
	args = append(args, limit, offset)
	query += fmt.Sprintf(` ORDER BY COALESCE(u.last_download_time, '1970-01-01T00:00:00Z') ASC, o.id ASC LIMIT $%d OFFSET $%d`, len(args)-1, len(args))
	rows, err := db.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanFileUsageRows(rows, limit)
}

func (db *PostgresDB) getScopedFileUsageSummary(ctx context.Context, resources []string, includeUnscoped bool, inactiveSince *time.Time) (models.FileUsageSummary, error) {
	if err := db.flushObjectUsageEvents(ctx); err != nil {
		return models.FileUsageSummary{}, err
	}
	resources = sycommon.NormalizeAccessResources(resources)
	if len(resources) == 0 && !includeUnscoped {
		return models.FileUsageSummary{}, nil
	}

	query, args := postgresScopedFileUsageQuery(resources, includeUnscoped, inactiveSince, true)
	var summary models.FileUsageSummary
	if err := db.db.QueryRowContext(ctx, query, args...).Scan(
		&summary.TotalFiles,
		&summary.TotalUploads,
		&summary.TotalDownloads,
		&summary.InactiveFileCount,
	); err != nil {
		return models.FileUsageSummary{}, err
	}
	return summary, nil
}

func postgresScopedFileUsageQuery(resources []string, includeUnscoped bool, inactiveSince *time.Time, summary bool) (string, []any) {
	args := []any{pq.Array(resources), includeUnscoped}
	var selectClause string
	if summary {
		if inactiveSince != nil {
			args = append(args, inactiveSince.UTC())
			selectClause = `
		SELECT
			COUNT(o.id) AS total_files,
			COALESCE(SUM(COALESCE(u.upload_count, 0)), 0) AS total_uploads,
			COALESCE(SUM(COALESCE(u.download_count, 0)), 0) AS total_downloads,
			COALESCE(SUM(CASE WHEN u.last_download_time IS NULL OR u.last_download_time < $3 THEN 1 ELSE 0 END), 0) AS inactive_files`
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
		WHERE ((
			COALESCE(array_length($1::text[], 1), 0) > 0
			AND EXISTS (
				SELECT 1
				FROM drs_object_controlled_access ca
				WHERE ca.object_id = o.id AND ca.resource = ANY($1)
			)
		) OR (
			$2
			AND NOT EXISTS (
				SELECT 1
				FROM drs_object_controlled_access ca
				WHERE ca.object_id = o.id
			)
		))`
	if !summary && inactiveSince != nil {
		args = append(args, inactiveSince.UTC())
		query += fmt.Sprintf(" AND (u.last_download_time IS NULL OR u.last_download_time < $%d)", len(args))
	}
	return query, args
}

func (db *PostgresDB) GetFileUsageSummary(ctx context.Context, inactiveSince *time.Time) (models.FileUsageSummary, error) {
	if err := db.flushObjectUsageEvents(ctx); err != nil {
		return models.FileUsageSummary{}, err
	}
	cutoff := time.Now().UTC().AddDate(0, 0, -730)
	if inactiveSince != nil {
		cutoff = inactiveSince.UTC()
	}
	var summary models.FileUsageSummary
	if err := db.db.QueryRowContext(ctx, `
		SELECT
			COUNT(o.id) AS total_files,
			COALESCE(SUM(COALESCE(u.upload_count, 0)), 0) AS total_uploads,
			COALESCE(SUM(COALESCE(u.download_count, 0)), 0) AS total_downloads,
			COALESCE(SUM(CASE WHEN u.last_download_time IS NULL OR u.last_download_time < $1 THEN 1 ELSE 0 END), 0) AS inactive_files
		FROM drs_object o
		LEFT JOIN object_usage u ON u.object_id = o.id
	`, cutoff).Scan(
		&summary.TotalFiles,
		&summary.TotalUploads,
		&summary.TotalDownloads,
		&summary.InactiveFileCount,
	); err != nil {
		return models.FileUsageSummary{}, err
	}
	return summary, nil
}

func (db *PostgresDB) flushObjectUsageEvents(ctx context.Context) error {
	tx, err := db.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	ids, err := db.existingObjectIDsWithEvents(ctx, tx)
	if err != nil {
		return err
	}
	if err := db.flushObjectUsageEventsForIDsTx(ctx, tx, ids); err != nil {
		return err
	}
	return tx.Commit()
}

func (db *PostgresDB) existingObjectIDsWithEvents(ctx context.Context, tx *sql.Tx) ([]string, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT DISTINCT e.object_id
		FROM object_usage_event e
		JOIN drs_object o ON o.id = e.object_id
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := make([]string, 0)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, nil
}

func (db *PostgresDB) flushObjectUsageEventsForIDsTx(ctx context.Context, tx *sql.Tx, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	now := time.Now().UTC()
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO object_usage (object_id, upload_count, download_count, last_upload_time, last_download_time, updated_time)
		SELECT e.object_id,
			COALESCE(SUM(CASE WHEN e.event_type = 'upload' THEN 1 ELSE 0 END), 0) AS upload_count,
			COALESCE(SUM(CASE WHEN e.event_type = 'download' THEN 1 ELSE 0 END), 0) AS download_count,
			MAX(CASE WHEN e.event_type = 'upload' THEN e.event_time END) AS last_upload_time,
			MAX(CASE WHEN e.event_type = 'download' THEN e.event_time END) AS last_download_time,
			$2
		FROM object_usage_event e
		JOIN drs_object o ON o.id = e.object_id
		WHERE e.object_id = ANY($1)
		GROUP BY e.object_id
		ON CONFLICT (object_id) DO UPDATE SET
			upload_count = object_usage.upload_count + EXCLUDED.upload_count,
			download_count = object_usage.download_count + EXCLUDED.download_count,
			last_upload_time = CASE
				WHEN EXCLUDED.last_upload_time IS NULL THEN object_usage.last_upload_time
				WHEN object_usage.last_upload_time IS NULL THEN EXCLUDED.last_upload_time
				WHEN EXCLUDED.last_upload_time > object_usage.last_upload_time THEN EXCLUDED.last_upload_time
				ELSE object_usage.last_upload_time
			END,
			last_download_time = CASE
				WHEN EXCLUDED.last_download_time IS NULL THEN object_usage.last_download_time
				WHEN object_usage.last_download_time IS NULL THEN EXCLUDED.last_download_time
				WHEN EXCLUDED.last_download_time > object_usage.last_download_time THEN EXCLUDED.last_download_time
				ELSE object_usage.last_download_time
			END,
			updated_time = EXCLUDED.updated_time
	`, pq.Array(ids), now); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `
		DELETE FROM object_usage_event e
		USING drs_object o
		WHERE e.object_id = o.id AND e.object_id = ANY($1)
	`, pq.Array(ids))
	return err
}
