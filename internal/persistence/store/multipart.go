package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/calypr/syfon/apigen/errorapi"
	"github.com/calypr/syfon/internal/transfers"
)

func (db *Store) SaveMultipartSession(ctx context.Context, session transfers.MultipartSession) error {
	targetJSON, authorizationJSON, completionPartsJSON, err := encodeMultipartSession(session)
	if err != nil {
		return err
	}
	result, err := db.execContext(ctx, `
		INSERT INTO multipart_upload_session (
			upload_id, completion_id, target_json, authorization_json, state, completion_token, parts_fingerprint,
			completed_location, operation, completion_parts_json, created_time, updated_time
		)
		SELECT ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?
		WHERE NOT EXISTS (SELECT 1 FROM multipart_completion_receipt WHERE upload_id = ?)
		ON CONFLICT (upload_id) DO NOTHING
	`, session.UploadID, session.CompletionID, targetJSON, authorizationJSON, session.State, session.CompletionToken, session.PartsFingerprint, session.CompletedLocation, session.Operation, completionPartsJSON, session.CreatedAt.UTC(), session.UpdatedAt.UTC(), session.UploadID)
	if err != nil {
		return fmt.Errorf("save multipart session %s: %w", session.UploadID, err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("inspect saved multipart session %s: %w", session.UploadID, err)
	}
	if affected != 1 {
		return fmt.Errorf("%w: multipart upload ID %s already exists", errorapi.ErrConflict, session.UploadID)
	}
	return nil
}

func (db *Store) GetMultipartSession(ctx context.Context, uploadID string) (transfers.MultipartSession, error) {
	return db.readMultipartSession(ctx, db.db, uploadID)
}

func (db *Store) ClaimMultipartCompletion(ctx context.Context, uploadID, token, partsFingerprint string, now, staleBefore time.Time) (transfers.MultipartSession, bool, error) {
	return db.claimMultipartCompletion(ctx, uploadID, token, partsFingerprint, "", now, staleBefore)
}

func (db *Store) ClaimMultipartCompletionWithParts(ctx context.Context, uploadID, token, partsFingerprint string, parts []transfers.CompletedPart, now, staleBefore time.Time) (transfers.MultipartSession, bool, error) {
	partsJSON, err := json.Marshal(parts)
	if err != nil {
		return transfers.MultipartSession{}, false, fmt.Errorf("encode multipart completion parts %s: %w", uploadID, err)
	}
	return db.claimMultipartCompletion(ctx, uploadID, token, partsFingerprint, string(partsJSON), now, staleBefore)
}

func (db *Store) claimMultipartCompletion(ctx context.Context, uploadID, token, partsFingerprint, partsJSON string, now, staleBefore time.Time) (transfers.MultipartSession, bool, error) {
	result, err := db.execContext(ctx, `
		UPDATE multipart_upload_session
		SET state = ?, operation = ?, completion_token = ?, parts_fingerprint = ?, completion_parts_json = ?, updated_time = ?
		WHERE upload_id = ?
		  AND (parts_fingerprint = '' OR parts_fingerprint = ?)
		  AND (state = ? OR (state = ? AND (operation = ? OR operation = ?) AND updated_time <= ?))
	`, transfers.MultipartStateCompleting, transfers.MultipartOperationComplete, token, partsFingerprint, partsJSON, now.UTC(), uploadID, partsFingerprint, transfers.MultipartStateActive, transfers.MultipartStateCompleting, transfers.MultipartOperationComplete, transfers.MultipartOperationNone, staleBefore.UTC())
	if err != nil {
		return transfers.MultipartSession{}, false, fmt.Errorf("claim multipart completion %s: %w", uploadID, err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return transfers.MultipartSession{}, false, fmt.Errorf("inspect multipart completion claim %s: %w", uploadID, err)
	}
	session, err := db.GetMultipartSession(ctx, uploadID)
	if err != nil {
		return transfers.MultipartSession{}, false, err
	}
	return session, affected == 1 && session.CompletionToken == token, nil
}

func (db *Store) ReleaseMultipartCompletion(ctx context.Context, uploadID, token string, now time.Time) error {
	_, err := db.execContext(ctx, `
		UPDATE multipart_upload_session
		SET state = ?, operation = '', completion_token = '', completion_parts_json = '', updated_time = ?
		WHERE upload_id = ? AND state = ? AND operation = ? AND completion_token = ?
	`, transfers.MultipartStateActive, now.UTC(), uploadID, transfers.MultipartStateCompleting, transfers.MultipartOperationComplete, token)
	if err != nil {
		return fmt.Errorf("release multipart completion %s: %w", uploadID, err)
	}
	return nil
}

func (db *Store) FinishMultipartCompletion(ctx context.Context, uploadID, token, location string, now time.Time) (bool, error) {
	result, err := db.execContext(ctx, `
		UPDATE multipart_upload_session
		SET state = ?, operation = '', completion_token = '', completion_parts_json = '', completed_location = ?, updated_time = ?
		WHERE upload_id = ? AND state = ? AND operation = ? AND completion_token = ?
	`, transfers.MultipartStateCompleted, location, now.UTC(), uploadID, transfers.MultipartStateCompleting, transfers.MultipartOperationComplete, token)
	if err != nil {
		return false, fmt.Errorf("finish multipart completion %s: %w", uploadID, err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("inspect finished multipart completion %s: %w", uploadID, err)
	}
	return affected == 1, nil
}

func (db *Store) ClaimMultipartAbort(ctx context.Context, uploadID, token string, now, staleBefore time.Time) (transfers.MultipartSession, bool, error) {
	result, err := db.execContext(ctx, `
		UPDATE multipart_upload_session
		SET state = ?, operation = ?, completion_token = ?, updated_time = ?
		WHERE upload_id = ?
		  AND (state = ? OR (state = ? AND operation = ? AND updated_time <= ?))
	`, transfers.MultipartStateCompleting, transfers.MultipartOperationAbort, token, now.UTC(), uploadID, transfers.MultipartStateActive, transfers.MultipartStateCompleting, transfers.MultipartOperationAbort, staleBefore.UTC())
	if err != nil {
		return transfers.MultipartSession{}, false, fmt.Errorf("claim multipart abort %s: %w", uploadID, err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return transfers.MultipartSession{}, false, fmt.Errorf("inspect multipart abort claim %s: %w", uploadID, err)
	}
	session, err := db.GetMultipartSession(ctx, uploadID)
	if err != nil {
		return transfers.MultipartSession{}, false, err
	}
	return session, affected == 1 && session.CompletionToken == token, nil
}

func (db *Store) FinishMultipartAbort(ctx context.Context, uploadID, token string, _ time.Time) (bool, error) {
	result, err := db.execContext(ctx, `DELETE FROM multipart_upload_session WHERE upload_id = ? AND state = ? AND operation = ? AND completion_token = ?`, uploadID, transfers.MultipartStateCompleting, transfers.MultipartOperationAbort, token)
	if err != nil {
		return false, fmt.Errorf("finish multipart abort %s: %w", uploadID, err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("inspect finished multipart abort %s: %w", uploadID, err)
	}
	return affected == 1, nil
}

func (db *Store) TouchMultipartSession(ctx context.Context, uploadID string, now time.Time) error {
	result, err := db.execContext(ctx, `UPDATE multipart_upload_session SET updated_time = ? WHERE upload_id = ? AND state = ?`, now.UTC(), uploadID, transfers.MultipartStateActive)
	if err != nil {
		return fmt.Errorf("touch multipart session %s: %w", uploadID, err)
	}
	if affected, err := result.RowsAffected(); err == nil && affected == 0 {
		return fmt.Errorf("%w: %s", errorapi.ErrMultipartUploadNotFound, uploadID)
	}
	return nil
}

func (db *Store) ListMultipartSessionsForReconcile(ctx context.Context, inactiveBefore, completedBefore time.Time, limit int) ([]transfers.MultipartSession, error) {
	if limit <= 0 {
		return []transfers.MultipartSession{}, nil
	}
	rows, err := db.queryContext(ctx, `
		SELECT upload_id, completion_id, target_json, authorization_json, state, completion_token, parts_fingerprint,
		       completed_location, operation, completion_parts_json, created_time, updated_time
		FROM multipart_upload_session
		WHERE (state = ? AND updated_time <= ?)
		   OR (state <> ? AND updated_time <= ?)
		ORDER BY updated_time, upload_id
		LIMIT ?
	`, transfers.MultipartStateCompleted, completedBefore.UTC(), transfers.MultipartStateCompleted, inactiveBefore.UTC(), limit)
	if err != nil {
		return nil, fmt.Errorf("list multipart sessions for reconciliation: %w", err)
	}
	defer rows.Close()
	result := make([]transfers.MultipartSession, 0, limit)
	for rows.Next() {
		var session transfers.MultipartSession
		var targetJSON, authorizationJSON, partsJSON string
		if err := rows.Scan(&session.UploadID, &session.CompletionID, &targetJSON, &authorizationJSON, &session.State, &session.CompletionToken, &session.PartsFingerprint, &session.CompletedLocation, &session.Operation, &partsJSON, &session.CreatedAt, &session.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan multipart reconciliation session: %w", err)
		}
		if err := json.Unmarshal([]byte(targetJSON), &session.Target); err != nil {
			return nil, fmt.Errorf("decode multipart reconciliation target %s: %w", session.UploadID, err)
		}
		if err := json.Unmarshal([]byte(authorizationJSON), &session.Authorization); err != nil {
			return nil, fmt.Errorf("decode multipart reconciliation authorization %s: %w", session.UploadID, err)
		}
		if strings.TrimSpace(partsJSON) != "" {
			if err := json.Unmarshal([]byte(partsJSON), &session.CompletionParts); err != nil {
				return nil, fmt.Errorf("decode multipart reconciliation parts %s: %w", session.UploadID, err)
			}
		}
		result = append(result, session)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate multipart reconciliation sessions: %w", err)
	}
	return result, nil
}

func (db *Store) GetMultipartCompletionReceipt(ctx context.Context, uploadID string) (transfers.MultipartCompletionReceipt, error) {
	var receipt transfers.MultipartCompletionReceipt
	var authorizationJSON string
	err := db.queryRowOn(ctx, db.db, `
		SELECT authorization_json, parts_fingerprint, completed_location
		FROM multipart_completion_receipt WHERE upload_id = ?
	`, uploadID).Scan(&authorizationJSON, &receipt.PartsFingerprint, &receipt.CompletedLocation)
	if errors.Is(err, sql.ErrNoRows) {
		return transfers.MultipartCompletionReceipt{}, fmt.Errorf("%w: %s", errorapi.ErrMultipartUploadNotFound, uploadID)
	}
	if err != nil {
		return transfers.MultipartCompletionReceipt{}, fmt.Errorf("load multipart completion receipt %s: %w", uploadID, err)
	}
	receipt.UploadID = uploadID
	if err := json.Unmarshal([]byte(authorizationJSON), &receipt.Authorization); err != nil {
		return transfers.MultipartCompletionReceipt{}, fmt.Errorf("decode multipart completion receipt authorization %s: %w", uploadID, err)
	}
	return receipt, nil
}

func (db *Store) CompactCompletedMultipartSessions(ctx context.Context, before time.Time, limit int) error {
	if limit <= 0 {
		return nil
	}
	type compactedReceipt struct {
		uploadID          string
		authorizationJSON string
		partsFingerprint  string
		completedLocation string
		completedTime     time.Time
	}
	return db.withWrite(ctx, func(tx *sql.Tx) error {
		rows, err := db.txQueryContext(ctx, tx, `
			SELECT upload_id, authorization_json, parts_fingerprint, completed_location, updated_time
			FROM multipart_upload_session
			WHERE state = ? AND updated_time <= ?
			ORDER BY updated_time, upload_id LIMIT ?
		`, transfers.MultipartStateCompleted, before.UTC(), limit)
		if err != nil {
			return fmt.Errorf("list completed multipart sessions for compaction: %w", err)
		}
		receipts := make([]compactedReceipt, 0, limit)
		for rows.Next() {
			var receipt compactedReceipt
			if err := rows.Scan(&receipt.uploadID, &receipt.authorizationJSON, &receipt.partsFingerprint, &receipt.completedLocation, &receipt.completedTime); err != nil {
				rows.Close()
				return fmt.Errorf("scan completed multipart session for compaction: %w", err)
			}
			receipts = append(receipts, receipt)
		}
		if err := rows.Close(); err != nil {
			return fmt.Errorf("close completed multipart compaction rows: %w", err)
		}
		if err := rows.Err(); err != nil {
			return fmt.Errorf("iterate completed multipart sessions for compaction: %w", err)
		}
		for _, receipt := range receipts {
			if _, err := db.txExecContext(ctx, tx, `
				INSERT INTO multipart_completion_receipt (
					upload_id, authorization_json, parts_fingerprint, completed_location, completed_time
				) VALUES (?, ?, ?, ?, ?)
				ON CONFLICT (upload_id) DO NOTHING
			`, receipt.uploadID, receipt.authorizationJSON, receipt.partsFingerprint, receipt.completedLocation, receipt.completedTime.UTC()); err != nil {
				return fmt.Errorf("persist multipart completion receipt %s: %w", receipt.uploadID, err)
			}
			var storedAuthorization, storedFingerprint, storedLocation string
			if err := db.txQueryRowContext(ctx, tx, `
				SELECT authorization_json, parts_fingerprint, completed_location
				FROM multipart_completion_receipt WHERE upload_id = ?
			`, receipt.uploadID).Scan(&storedAuthorization, &storedFingerprint, &storedLocation); err != nil {
				return fmt.Errorf("verify multipart completion receipt %s: %w", receipt.uploadID, err)
			}
			authorizationEqual, err := equivalentMultipartAuthorizationJSON(storedAuthorization, receipt.authorizationJSON)
			if err != nil {
				return fmt.Errorf("verify multipart completion receipt authorization %s: %w", receipt.uploadID, err)
			}
			if !authorizationEqual || storedFingerprint != receipt.partsFingerprint || storedLocation != receipt.completedLocation {
				return fmt.Errorf("%w: multipart completion receipt %s conflicts with completed session", errorapi.ErrConflict, receipt.uploadID)
			}
			if _, err := db.txExecContext(ctx, tx, `
				DELETE FROM multipart_upload_session
				WHERE upload_id = ? AND state = ? AND updated_time <= ?
			`, receipt.uploadID, transfers.MultipartStateCompleted, before.UTC()); err != nil {
				return fmt.Errorf("delete compacted multipart session %s: %w", receipt.uploadID, err)
			}
		}
		return nil
	})
}

func equivalentMultipartAuthorizationJSON(first, second string) (bool, error) {
	normalize := func(raw string) (string, error) {
		var authorization transfers.MultipartAuthorization
		if err := json.Unmarshal([]byte(raw), &authorization); err != nil {
			return "", err
		}
		encoded, err := json.Marshal(authorization)
		return string(encoded), err
	}
	firstNormalized, err := normalize(first)
	if err != nil {
		return false, err
	}
	secondNormalized, err := normalize(second)
	if err != nil {
		return false, err
	}
	return firstNormalized == secondNormalized, nil
}

func (db *Store) readMultipartSession(ctx context.Context, executor sqlExecutor, uploadID string) (transfers.MultipartSession, error) {
	var session transfers.MultipartSession
	var targetJSON, authorizationJSON, completionPartsJSON string
	err := db.queryRowOn(ctx, executor, `
		SELECT completion_id, target_json, authorization_json, state, completion_token, parts_fingerprint,
		       completed_location, operation, completion_parts_json, created_time, updated_time
		FROM multipart_upload_session WHERE upload_id = ?
	`, uploadID).Scan(&session.CompletionID, &targetJSON, &authorizationJSON, &session.State, &session.CompletionToken, &session.PartsFingerprint, &session.CompletedLocation, &session.Operation, &completionPartsJSON, &session.CreatedAt, &session.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return transfers.MultipartSession{}, fmt.Errorf("%w: %s", errorapi.ErrMultipartUploadNotFound, uploadID)
	}
	if err != nil {
		return transfers.MultipartSession{}, fmt.Errorf("load multipart session %s: %w", uploadID, err)
	}
	session.UploadID = uploadID
	if err := json.Unmarshal([]byte(targetJSON), &session.Target); err != nil {
		return transfers.MultipartSession{}, fmt.Errorf("decode multipart target %s: %w", uploadID, err)
	}
	if err := json.Unmarshal([]byte(authorizationJSON), &session.Authorization); err != nil {
		return transfers.MultipartSession{}, fmt.Errorf("decode multipart authorization %s: %w", uploadID, err)
	}
	if strings.TrimSpace(completionPartsJSON) != "" {
		if err := json.Unmarshal([]byte(completionPartsJSON), &session.CompletionParts); err != nil {
			return transfers.MultipartSession{}, fmt.Errorf("decode multipart completion parts %s: %w", uploadID, err)
		}
	}
	return session, nil
}

func encodeMultipartSession(session transfers.MultipartSession) (string, string, string, error) {
	targetJSON, err := json.Marshal(session.Target)
	if err != nil {
		return "", "", "", fmt.Errorf("encode multipart target %s: %w", session.UploadID, err)
	}
	authorizationJSON, err := json.Marshal(session.Authorization)
	if err != nil {
		return "", "", "", fmt.Errorf("encode multipart authorization %s: %w", session.UploadID, err)
	}
	completionPartsJSON, err := json.Marshal(session.CompletionParts)
	if err != nil {
		return "", "", "", fmt.Errorf("encode multipart completion parts %s: %w", session.UploadID, err)
	}
	return string(targetJSON), string(authorizationJSON), string(completionPartsJSON), nil
}
