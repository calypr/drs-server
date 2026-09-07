package postgres

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/calypr/syfon/internal/usage"
)

func (db *PostgresDB) RecordTransferAttributionEvents(ctx context.Context, events []usage.Event) error {
	if len(events) == 0 {
		return nil
	}
	tx, err := db.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO transfer_attribution_event (
			event_id, access_grant_id, event_type, direction, event_time, request_id, object_id, sha256, object_size,
			organization, project, access_id, provider, bucket, storage_url,
			range_start, range_end, bytes_requested, bytes_completed,
			actor_email, actor_subject, auth_mode, client_name, client_version, transfer_session_id
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20, $21, $22, $23, $24, $25)
		ON CONFLICT (event_id) DO NOTHING
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
			if err := postgresUpsertAccessGrant(ctx, tx, ev); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}

func (db *PostgresDB) RecordProviderTransferEvents(ctx context.Context, events []usage.ProviderEvent) error {
	if len(events) == 0 {
		return nil
	}
	tx, err := db.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO provider_transfer_event (
			provider_event_id, access_grant_id, direction, event_time, request_id, provider_request_id,
			object_id, sha256, object_size, organization, project, access_id, provider, bucket,
			object_key, storage_url, range_start, range_end, bytes_transferred, http_method, http_status,
			requester_principal, source_ip, user_agent, raw_event_ref, actor_email, actor_subject, auth_mode,
			reconciliation_status
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20, $21, $22, $23, $24, $25, $26, $27, $28, $29)
		ON CONFLICT (provider_event_id) DO NOTHING
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

func nullableInt64(v *int64) any {
	if v == nil {
		return nil
	}
	return *v
}

func (db *PostgresDB) backfillAccessGrants(ctx context.Context) error {
	tx, err := db.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	rows, err := tx.QueryContext(ctx, `
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
		if _, err := tx.ExecContext(ctx, `UPDATE transfer_attribution_event SET access_grant_id = $1 WHERE event_id = $2`, ev.AccessGrantID, ev.EventID); err != nil {
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
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO access_grant (
				access_grant_id, first_issued_at, last_issued_at, issue_count,
				object_id, sha256, object_size, organization, project, access_id,
				provider, bucket, storage_url, actor_email, actor_subject, auth_mode
			) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16)
			ON CONFLICT (access_grant_id) DO NOTHING
		`, grant.AccessGrantID, grant.FirstIssuedAt, grant.LastIssuedAt, grant.IssueCount,
			grant.ObjectID, grant.SHA256, grant.ObjectSize, grant.Organization, grant.Project, grant.AccessID,
			grant.Provider, grant.Bucket, grant.StorageURL, grant.ActorEmail, grant.ActorSubject, grant.AuthMode); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (db *PostgresDB) reconcileProviderTransferEvent(ctx context.Context, tx *sql.Tx, ev usage.ProviderEvent) (usage.ProviderEvent, error) {
	ev.Direction = normalizeProviderDirection(ev.Direction, ev.HTTPMethod)
	ev.Provider = strings.TrimSpace(ev.Provider)
	ev.Bucket = strings.TrimSpace(ev.Bucket)
	ev.ObjectKey = strings.TrimLeft(strings.TrimSpace(ev.ObjectKey), "/")
	ev.StorageURL = strings.TrimSpace(ev.StorageURL)
	ev.ReconciliationStatus = usage.ProviderTransferUnmatched
	if ev.AccessGrantID != "" {
		if match, ok, err := postgresAccessGrantByID(ctx, tx, ev.AccessGrantID); err != nil {
			return ev, err
		} else if ok {
			mergeAccessGrantIntoProviderEvent(&ev, match)
			ev.ReconciliationStatus = usage.ProviderTransferMatched
			return ev, nil
		}
	}
	matches, err := postgresAccessGrantCandidates(ctx, tx, ev)
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

func postgresUpsertAccessGrant(ctx context.Context, tx *sql.Tx, ev usage.Event) error {
	if ev.AccessGrantID == "" {
		return nil
	}
	when := ev.EventTime.UTC()
	_, err := tx.ExecContext(ctx, `
		INSERT INTO access_grant (
			access_grant_id, first_issued_at, last_issued_at, issue_count,
			object_id, sha256, object_size, organization, project, access_id,
			provider, bucket, storage_url, actor_email, actor_subject, auth_mode
		) VALUES ($1, $2, $3, 1, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15)
		ON CONFLICT (access_grant_id) DO UPDATE SET
			first_issued_at = LEAST(access_grant.first_issued_at, EXCLUDED.first_issued_at),
			last_issued_at = GREATEST(access_grant.last_issued_at, EXCLUDED.last_issued_at),
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

func postgresAccessGrantByID(ctx context.Context, tx *sql.Tx, grantID string) (usage.Grant, bool, error) {
	var grant usage.Grant
	err := tx.QueryRowContext(ctx, `
		SELECT access_grant_id, first_issued_at, last_issued_at, issue_count,
			object_id, sha256, object_size, organization, project, access_id,
			provider, bucket, storage_url, actor_email, actor_subject, auth_mode
		FROM access_grant
		WHERE access_grant_id = $1
	`, grantID).Scan(
		&grant.AccessGrantID, &grant.FirstIssuedAt, &grant.LastIssuedAt, &grant.IssueCount,
		&grant.ObjectID, &grant.SHA256, &grant.ObjectSize, &grant.Organization, &grant.Project, &grant.AccessID,
		&grant.Provider, &grant.Bucket, &grant.StorageURL, &grant.ActorEmail, &grant.ActorSubject, &grant.AuthMode,
	)
	if err == sql.ErrNoRows {
		return usage.Grant{}, false, nil
	}
	return grant, err == nil, err
}

func postgresAccessGrantCandidates(ctx context.Context, tx *sql.Tx, ev usage.ProviderEvent) ([]usage.Grant, error) {
	args := []any{ev.Provider, ev.Bucket, ev.EventTime.UTC().Add(15 * time.Minute), ev.EventTime.UTC().Add(-24 * time.Hour)}
	query := `
		SELECT access_grant_id, first_issued_at, last_issued_at, issue_count,
			object_id, sha256, object_size, organization, project, access_id,
			provider, bucket, storage_url, actor_email, actor_subject, auth_mode
		FROM access_grant
		WHERE provider = $1
			AND bucket = $2
			AND last_issued_at <= $3
			AND last_issued_at >= $4
	`
	if ev.StorageURL != "" {
		args = append(args, ev.StorageURL)
		query += fmt.Sprintf(" AND storage_url = $%d", len(args))
	} else if ev.ObjectKey != "" {
		args = append(args, providerStorageURL(ev.Provider, ev.Bucket, ev.ObjectKey), "%/"+ev.ObjectKey)
		query += fmt.Sprintf(" AND (storage_url = $%d OR storage_url LIKE $%d)", len(args)-1, len(args))
	}
	query += " ORDER BY last_issued_at DESC LIMIT 2"
	rows, err := tx.QueryContext(ctx, query, args...)
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

func normalizeTransferDirection(direction string) string {
	switch strings.ToLower(strings.TrimSpace(direction)) {
	case usage.ProviderTransferDirectionUpload:
		return usage.ProviderTransferDirectionUpload
	default:
		return usage.ProviderTransferDirectionDownload
	}
}
