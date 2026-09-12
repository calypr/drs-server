package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/calypr/syfon/apigen/errorapi"
	"github.com/calypr/syfon/internal/transfers"
)

func (db *Store) SaveMultipartSession(ctx context.Context, session transfers.MultipartSession) error {
	targetJSON, authorizationJSON, err := encodeMultipartSession(session)
	if err != nil {
		return err
	}
	result, err := db.execContext(ctx, `
		INSERT INTO multipart_upload_session (
			upload_id, completion_id, target_json, authorization_json, state, completion_token, parts_fingerprint,
			completed_location, created_time, updated_time
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (upload_id) DO NOTHING
	`, session.UploadID, session.CompletionID, targetJSON, authorizationJSON, session.State, session.CompletionToken, session.PartsFingerprint, session.CompletedLocation, session.CreatedAt.UTC(), session.UpdatedAt.UTC())
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
	result, err := db.execContext(ctx, `
		UPDATE multipart_upload_session
		SET state = ?, completion_token = ?, parts_fingerprint = ?, updated_time = ?
		WHERE upload_id = ?
		  AND (parts_fingerprint = '' OR parts_fingerprint = ?)
		  AND (state = ? OR (state = ? AND updated_time <= ?))
	`, transfers.MultipartStateCompleting, token, partsFingerprint, now.UTC(), uploadID, partsFingerprint, transfers.MultipartStateActive, transfers.MultipartStateCompleting, staleBefore.UTC())
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
		SET state = ?, completion_token = '', updated_time = ?
		WHERE upload_id = ? AND state = ? AND completion_token = ?
	`, transfers.MultipartStateActive, now.UTC(), uploadID, transfers.MultipartStateCompleting, token)
	if err != nil {
		return fmt.Errorf("release multipart completion %s: %w", uploadID, err)
	}
	return nil
}

func (db *Store) FinishMultipartCompletion(ctx context.Context, uploadID, token, location string, now time.Time) (bool, error) {
	result, err := db.execContext(ctx, `
		UPDATE multipart_upload_session
		SET state = ?, completion_token = '', completed_location = ?, updated_time = ?
		WHERE upload_id = ? AND state = ? AND completion_token = ?
	`, transfers.MultipartStateCompleted, location, now.UTC(), uploadID, transfers.MultipartStateCompleting, token)
	if err != nil {
		return false, fmt.Errorf("finish multipart completion %s: %w", uploadID, err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("inspect finished multipart completion %s: %w", uploadID, err)
	}
	return affected == 1, nil
}

func (db *Store) readMultipartSession(ctx context.Context, executor sqlExecutor, uploadID string) (transfers.MultipartSession, error) {
	var session transfers.MultipartSession
	var targetJSON, authorizationJSON string
	err := db.queryRowOn(ctx, executor, `
		SELECT completion_id, target_json, authorization_json, state, completion_token, parts_fingerprint,
		       completed_location, created_time, updated_time
		FROM multipart_upload_session
		WHERE upload_id = ?
	`, uploadID).Scan(&session.CompletionID, &targetJSON, &authorizationJSON, &session.State, &session.CompletionToken, &session.PartsFingerprint, &session.CompletedLocation, &session.CreatedAt, &session.UpdatedAt)
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
	return session, nil
}

func encodeMultipartSession(session transfers.MultipartSession) (string, string, error) {
	targetJSON, err := json.Marshal(session.Target)
	if err != nil {
		return "", "", fmt.Errorf("encode multipart target %s: %w", session.UploadID, err)
	}
	authorizationJSON, err := json.Marshal(session.Authorization)
	if err != nil {
		return "", "", fmt.Errorf("encode multipart authorization %s: %w", session.UploadID, err)
	}
	return string(targetJSON), string(authorizationJSON), nil
}
