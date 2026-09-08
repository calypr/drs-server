package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	clientaccess "github.com/calypr/syfon/client/access"
	"github.com/calypr/syfon/internal/usage"
)

func (db *Store) prepareTxContext(ctx context.Context, tx *sql.Tx, query string) (*sql.Stmt, error) {
	return tx.PrepareContext(ctx, db.dialect.Rebind(query))
}

func (db *Store) queryTxContext(ctx context.Context, tx *sql.Tx, query string, args ...any) (*sql.Rows, error) {
	return tx.QueryContext(ctx, db.dialect.Rebind(query), args...)
}

func (db *Store) RecordTransferAttributionEvents(ctx context.Context, events []usage.Event) error {
	if len(events) == 0 {
		return nil
	}
	tx, err := db.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	stmt, err := db.prepareTxContext(ctx, tx, `
		INSERT INTO transfer_attribution_event (
			event_id, access_grant_id, event_type, direction, event_time, request_id, object_id, sha256, object_size,
			organization, project, access_id, provider, bucket, storage_url,
			range_start, range_end, bytes_requested, bytes_completed,
			actor_email, actor_subject, auth_mode, client_name, client_version, transfer_session_id
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT DO NOTHING
	`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, ev := range events {
		if ev.EventID == "" || ev.EventType == "" {
			continue
		}
		if ev.EventType != usage.TransferEventAccessIssued {
			continue
		}
		when := ev.EventTime
		if when.IsZero() {
			when = time.Now().UTC()
		}
		ev.AccessGrantID = usage.GrantID(ev)
		ev.EventTime = when.UTC()
		ev.Direction = normalizeTransferDirection(ev.Direction)
		result, err := stmt.ExecContext(ctx,
			ev.EventID, ev.AccessGrantID, ev.EventType, ev.Direction, ev.EventTime, ev.RequestID, ev.ObjectID, ev.SHA256, ev.ObjectSize,
			ev.Organization, ev.Project, ev.AccessID, ev.Provider, ev.Bucket, ev.StorageURL,
			nullableInt64(ev.RangeStart), nullableInt64(ev.RangeEnd), ev.BytesRequested, ev.BytesCompleted,
			ev.ActorEmail, ev.ActorSubject, ev.AuthMode, ev.ClientName, ev.ClientVersion, ev.TransferSessionID,
		)
		if err != nil {
			return err
		}
		if rows, err := result.RowsAffected(); err == nil && rows > 0 {
			if err := db.upsertAccessGrant(ctx, tx, ev); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}

func (db *Store) RecordProviderTransferEvents(ctx context.Context, events []usage.ProviderEvent) error {
	if len(events) == 0 {
		return nil
	}
	tx, err := db.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	stmt, err := db.prepareTxContext(ctx, tx, `
		INSERT INTO provider_transfer_event (
			provider_event_id, access_grant_id, direction, event_time, request_id, provider_request_id,
			object_id, sha256, object_size, organization, project, access_id, provider, bucket,
			object_key, storage_url, range_start, range_end, bytes_transferred, http_method, http_status,
			requester_principal, source_ip, user_agent, raw_event_ref, actor_email, actor_subject, auth_mode,
			reconciliation_status
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT DO NOTHING
	`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for i := range events {
		normalized, err := db.reconcileProviderTransferEvent(ctx, tx, events[i])
		if err != nil {
			return err
		}
		events[i] = normalized
		if normalized.ProviderEventID == "" || normalized.Direction == "" || normalized.Provider == "" {
			continue
		}
		when := normalized.EventTime
		if when.IsZero() {
			when = time.Now().UTC()
		}
		if _, err := stmt.ExecContext(ctx,
			normalized.ProviderEventID, normalized.AccessGrantID, normalized.Direction, when.UTC(), normalized.RequestID, normalized.ProviderRequestID,
			normalized.ObjectID, normalized.SHA256, normalized.ObjectSize, normalized.Organization, normalized.Project, normalized.AccessID, normalized.Provider, normalized.Bucket,
			normalized.ObjectKey, normalized.StorageURL, nullableInt64(normalized.RangeStart), nullableInt64(normalized.RangeEnd), normalized.BytesTransferred, normalized.HTTPMethod, normalized.HTTPStatus,
			normalized.RequesterPrincipal, normalized.SourceIP, normalized.UserAgent, normalized.RawEventRef, normalized.ActorEmail, normalized.ActorSubject, normalized.AuthMode,
			normalized.ReconciliationStatus,
		); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (db *Store) reconcileProviderTransferEvent(ctx context.Context, tx *sql.Tx, ev usage.ProviderEvent) (usage.ProviderEvent, error) {
	ev.Direction = normalizeProviderDirection(ev.Direction, ev.HTTPMethod)
	ev.Provider = strings.TrimSpace(ev.Provider)
	ev.Bucket = strings.TrimSpace(ev.Bucket)
	ev.ObjectKey = strings.TrimLeft(strings.TrimSpace(ev.ObjectKey), "/")
	ev.StorageURL = strings.TrimSpace(ev.StorageURL)
	ev.ReconciliationStatus = usage.ProviderTransferUnmatched
	if ev.AccessGrantID != "" {
		if match, ok, err := db.accessGrantByID(ctx, tx, ev.AccessGrantID); err != nil {
			return ev, err
		} else if ok {
			mergeAccessGrantIntoProviderEvent(&ev, match)
			ev.ReconciliationStatus = usage.ProviderTransferMatched
			return ev, nil
		}
	}
	matches, err := db.accessGrantCandidates(ctx, tx, ev)
	if err != nil {
		return ev, err
	}
	switch len(matches) {
	case 0:
		return ev, nil
	case 1:
		mergeAccessGrantIntoProviderEvent(&ev, matches[0])
		ev.ReconciliationStatus = usage.ProviderTransferMatched
	default:
		ev.ReconciliationStatus = usage.ProviderTransferAmbiguous
	}
	return ev, nil
}

func nullableInt64(v *int64) any {
	if v == nil {
		return nil
	}
	return *v
}

func (db *Store) backfillAccessGrants(ctx context.Context) error {
	tx, err := db.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	rows, err := db.queryTxContext(ctx, tx, `
		SELECT event_id, access_grant_id, event_type, direction, event_time, request_id, object_id, sha256, object_size,
			organization, project, access_id, provider, bucket, storage_url, range_start, range_end,
			bytes_requested, bytes_completed, actor_email, actor_subject, auth_mode, client_name, client_version,
			transfer_session_id
		FROM transfer_attribution_event
		WHERE event_type = 'access_issued'
	`)
	if err != nil {
		return err
	}
	defer rows.Close()

	events := make([]usage.Event, 0)
	for rows.Next() {
		var ev usage.Event
		if err := rows.Scan(
			&ev.EventID, &ev.AccessGrantID, &ev.EventType, &ev.Direction, &ev.EventTime, &ev.RequestID, &ev.ObjectID, &ev.SHA256, &ev.ObjectSize,
			&ev.Organization, &ev.Project, &ev.AccessID, &ev.Provider, &ev.Bucket, &ev.StorageURL, &ev.RangeStart, &ev.RangeEnd,
			&ev.BytesRequested, &ev.BytesCompleted, &ev.ActorEmail, &ev.ActorSubject, &ev.AuthMode, &ev.ClientName, &ev.ClientVersion,
			&ev.TransferSessionID,
		); err != nil {
			return err
		}
		events = append(events, ev)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	grants := make(map[string]usage.Grant)
	for _, ev := range events {
		ev.AccessGrantID = usage.GrantID(ev)
		if _, err := db.execTxContext(ctx, tx, `UPDATE transfer_attribution_event SET access_grant_id = ? WHERE event_id = ?`, ev.AccessGrantID, ev.EventID); err != nil {
			return err
		}
		grant := grants[ev.AccessGrantID]
		when := ev.EventTime.UTC()
		if grant.AccessGrantID == "" {
			grant = usage.Grant{
				AccessGrantID: ev.AccessGrantID,
				FirstIssuedAt: when,
				LastIssuedAt:  when,
				ObjectID:      ev.ObjectID,
				SHA256:        ev.SHA256,
				ObjectSize:    ev.ObjectSize,
				Organization:  ev.Organization,
				Project:       ev.Project,
				AccessID:      ev.AccessID,
				Provider:      ev.Provider,
				Bucket:        ev.Bucket,
				StorageURL:    ev.StorageURL,
				ActorEmail:    ev.ActorEmail,
				ActorSubject:  ev.ActorSubject,
				AuthMode:      ev.AuthMode,
			}
		}
		if when.Before(grant.FirstIssuedAt) {
			grant.FirstIssuedAt = when
		}
		if when.After(grant.LastIssuedAt) {
			grant.LastIssuedAt = when
		}
		grant.IssueCount++
		if grant.ActorEmail == "" {
			grant.ActorEmail = ev.ActorEmail
		}
		if grant.ActorSubject == "" {
			grant.ActorSubject = ev.ActorSubject
		}
		if grant.AuthMode == "" {
			grant.AuthMode = ev.AuthMode
		}
		grants[ev.AccessGrantID] = grant
	}
	for _, grant := range grants {
		if _, err := db.execTxContext(ctx, tx, `
			INSERT INTO access_grant (
				access_grant_id, first_issued_at, last_issued_at, issue_count,
				object_id, sha256, object_size, organization, project, access_id,
				provider, bucket, storage_url, actor_email, actor_subject, auth_mode
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT (access_grant_id) DO NOTHING
		`, grant.AccessGrantID, grant.FirstIssuedAt, grant.LastIssuedAt, grant.IssueCount,
			grant.ObjectID, grant.SHA256, grant.ObjectSize, grant.Organization, grant.Project, grant.AccessID,
			grant.Provider, grant.Bucket, grant.StorageURL, grant.ActorEmail, grant.ActorSubject, grant.AuthMode); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// BackfillAccessGrants runs the transfer attribution bootstrap against an
// already-open database. Backend schema bootstraps use this boundary so the
// migration itself remains owned by Store.
func BackfillAccessGrants(ctx context.Context, dbConn *sql.DB, dialect Dialect) error {
	if dbConn == nil {
		return errors.New("database connection is required")
	}
	if dialect == nil {
		return errors.New("database dialect is required")
	}
	return (&Store{db: dbConn, dialect: dialect}).backfillAccessGrants(ctx)
}

func (db *Store) upsertAccessGrant(ctx context.Context, tx *sql.Tx, ev usage.Event) error {
	if ev.AccessGrantID == "" {
		return nil
	}
	when := ev.EventTime.UTC()
	_, err := db.execTxContext(ctx, tx, `
		INSERT INTO access_grant (
			access_grant_id, first_issued_at, last_issued_at, issue_count,
			object_id, sha256, object_size, organization, project, access_id,
			provider, bucket, storage_url, actor_email, actor_subject, auth_mode
		) VALUES (?, ?, ?, 1, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(access_grant_id) DO UPDATE SET
			first_issued_at = CASE
				WHEN EXCLUDED.first_issued_at < access_grant.first_issued_at THEN EXCLUDED.first_issued_at
				ELSE access_grant.first_issued_at
			END,
			last_issued_at = CASE
				WHEN EXCLUDED.last_issued_at > access_grant.last_issued_at THEN EXCLUDED.last_issued_at
				ELSE access_grant.last_issued_at
			END,
			issue_count = access_grant.issue_count + 1,
			object_id = COALESCE(NULLIF(access_grant.object_id, ''), EXCLUDED.object_id),
			sha256 = COALESCE(NULLIF(access_grant.sha256, ''), EXCLUDED.sha256),
			object_size = CASE WHEN access_grant.object_size = 0 THEN EXCLUDED.object_size ELSE access_grant.object_size END,
			organization = COALESCE(NULLIF(access_grant.organization, ''), EXCLUDED.organization),
			project = COALESCE(NULLIF(access_grant.project, ''), EXCLUDED.project),
			access_id = COALESCE(NULLIF(access_grant.access_id, ''), EXCLUDED.access_id),
			provider = COALESCE(NULLIF(access_grant.provider, ''), EXCLUDED.provider),
			bucket = COALESCE(NULLIF(access_grant.bucket, ''), EXCLUDED.bucket),
			storage_url = COALESCE(NULLIF(access_grant.storage_url, ''), EXCLUDED.storage_url),
			actor_email = COALESCE(NULLIF(access_grant.actor_email, ''), EXCLUDED.actor_email),
			actor_subject = COALESCE(NULLIF(access_grant.actor_subject, ''), EXCLUDED.actor_subject),
			auth_mode = COALESCE(NULLIF(access_grant.auth_mode, ''), EXCLUDED.auth_mode)
	`, ev.AccessGrantID, when, when, ev.ObjectID, ev.SHA256, ev.ObjectSize,
		ev.Organization, ev.Project, ev.AccessID, ev.Provider, ev.Bucket, ev.StorageURL,
		ev.ActorEmail, ev.ActorSubject, ev.AuthMode)
	return err
}

func (db *Store) accessGrantByID(ctx context.Context, tx *sql.Tx, grantID string) (usage.Grant, bool, error) {
	var grant usage.Grant
	err := db.queryRowTxContext(ctx, tx, `
		SELECT access_grant_id, first_issued_at, last_issued_at, issue_count,
			object_id, sha256, object_size, organization, project, access_id,
			provider, bucket, storage_url, actor_email, actor_subject, auth_mode
		FROM access_grant
		WHERE access_grant_id = ?
	`, grantID).Scan(
		&grant.AccessGrantID, &grant.FirstIssuedAt, &grant.LastIssuedAt, &grant.IssueCount,
		&grant.ObjectID, &grant.SHA256, &grant.ObjectSize, &grant.Organization, &grant.Project, &grant.AccessID,
		&grant.Provider, &grant.Bucket, &grant.StorageURL, &grant.ActorEmail, &grant.ActorSubject, &grant.AuthMode,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return usage.Grant{}, false, nil
	}
	return grant, err == nil, err
}

func (db *Store) accessGrantCandidates(ctx context.Context, tx *sql.Tx, ev usage.ProviderEvent) ([]usage.Grant, error) {
	query := `
		SELECT access_grant_id, first_issued_at, last_issued_at, issue_count,
			object_id, sha256, object_size, organization, project, access_id,
			provider, bucket, storage_url, actor_email, actor_subject, auth_mode
		FROM access_grant
		WHERE provider = ?
			AND bucket = ?
			AND last_issued_at <= ?
			AND last_issued_at >= ?
	`
	args := []any{ev.Provider, ev.Bucket, ev.EventTime.UTC().Add(15 * time.Minute), ev.EventTime.UTC().Add(-24 * time.Hour)}
	if ev.StorageURL != "" {
		query += " AND storage_url = ?"
		args = append(args, ev.StorageURL)
	} else if ev.ObjectKey != "" {
		query += " AND (storage_url = ? OR storage_url LIKE ?)"
		args = append(args, providerStorageURL(ev.Provider, ev.Bucket, ev.ObjectKey), "%/"+ev.ObjectKey)
	}
	query += " ORDER BY last_issued_at DESC LIMIT 2"
	rows, err := db.queryTxContext(ctx, tx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []usage.Grant
	for rows.Next() {
		var match usage.Grant
		if err := rows.Scan(
			&match.AccessGrantID, &match.FirstIssuedAt, &match.LastIssuedAt, &match.IssueCount,
			&match.ObjectID, &match.SHA256, &match.ObjectSize, &match.Organization, &match.Project, &match.AccessID,
			&match.Provider, &match.Bucket, &match.StorageURL, &match.ActorEmail, &match.ActorSubject, &match.AuthMode,
		); err != nil {
			return nil, err
		}
		out = append(out, match)
	}
	return out, rows.Err()
}

func mergeAccessGrantIntoProviderEvent(ev *usage.ProviderEvent, grant usage.Grant) {
	if ev.AccessGrantID == "" {
		ev.AccessGrantID = grant.AccessGrantID
	}
	if ev.ObjectID == "" {
		ev.ObjectID = grant.ObjectID
	}
	if ev.SHA256 == "" {
		ev.SHA256 = grant.SHA256
	}
	if ev.ObjectSize == 0 {
		ev.ObjectSize = grant.ObjectSize
	}
	if ev.Organization == "" {
		ev.Organization = grant.Organization
	}
	if ev.Project == "" {
		ev.Project = grant.Project
	}
	if ev.AccessID == "" {
		ev.AccessID = grant.AccessID
	}
	if ev.StorageURL == "" {
		ev.StorageURL = grant.StorageURL
	}
	hasActor := ev.ActorEmail != "" || ev.ActorSubject != ""
	if !hasActor {
		ev.ActorEmail = grant.ActorEmail
	}
	if !hasActor {
		ev.ActorSubject = grant.ActorSubject
	}
	if ev.AuthMode == "" {
		ev.AuthMode = grant.AuthMode
	}
}

func normalizeProviderDirection(direction, method string) string {
	switch strings.ToLower(strings.TrimSpace(direction)) {
	case usage.ProviderTransferDirectionDownload, "get", "read":
		return usage.ProviderTransferDirectionDownload
	case usage.ProviderTransferDirectionUpload, "put", "write":
		return usage.ProviderTransferDirectionUpload
	}
	switch strings.ToUpper(strings.TrimSpace(method)) {
	case "GET":
		return usage.ProviderTransferDirectionDownload
	case "PUT", "POST":
		return usage.ProviderTransferDirectionUpload
	default:
		return strings.ToLower(strings.TrimSpace(direction))
	}
}

func providerStorageURL(provider, bucket, key string) string {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "gcs", "gs":
		return "gs://" + bucket + "/" + strings.TrimLeft(key, "/")
	case "azure", "az":
		return "az://" + bucket + "/" + strings.TrimLeft(key, "/")
	default:
		return "s3://" + bucket + "/" + strings.TrimLeft(key, "/")
	}
}

func (db *Store) GetTransferAttributionSummary(ctx context.Context, filter usage.Filter) (usage.Summary, error) {
	where, args := transferAttributionWhere(filter)
	var out usage.Summary
	err := db.queryRowContext(ctx, `
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

func (db *Store) GetTransferAttributionBreakdown(ctx context.Context, filter usage.Filter, groupBy string) ([]usage.Breakdown, error) {
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
	rows, err := db.queryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanTransferAttributionBreakdown(rows)
}

func (db *Store) GetTransferAttributionSummaryByResources(ctx context.Context, filter usage.Filter, resources []string) (usage.Summary, error) {
	where, args := db.transferAttributionWhereByResources(filter, resources)
	var out usage.Summary
	err := db.queryRowContext(ctx, `
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

func (db *Store) GetTransferAttributionBreakdownByResources(ctx context.Context, filter usage.Filter, groupBy string, resources []string) ([]usage.Breakdown, error) {
	keyExpr, selectExpr := transferAttributionGroupExpr(groupBy)
	where, args := db.transferAttributionWhereByResources(filter, resources)
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
	rows, err := db.queryContext(ctx, query, args...)
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

func (db *Store) transferAttributionWhereByResources(filter usage.Filter, resources []string) (string, []any) {
	where, args := transferAttributionWhere(filter)
	clause, clauseArgs := db.transferResourceClause(resources)
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

func (db *Store) transferResourceClause(resources []string) (string, []any) {
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
		clause, orgArgs := db.dialect.ListArgs("organization", orgOnly)
		clauses = append(clauses, clause)
		args = append(args, orgArgs...)
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
