package store

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/calypr/syfon/apigen/errorapi"
	"github.com/calypr/syfon/apigen/lfsapi"
	transferlfs "github.com/calypr/syfon/internal/transfers/lfs"
)

func (db *Store) SavePendingMetadata(ctx context.Context, entries []transferlfs.PendingMetadata) error {
	if len(entries) == 0 {
		return nil
	}
	tx, err := db.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := db.txExecContext(ctx, tx, `DELETE FROM lfs_pending_metadata WHERE expires_time <= ?`, time.Now().UTC()); err != nil {
		return fmt.Errorf("failed to prune expired pending metadata: %w", err)
	}

	for _, e := range entries {
		raw, err := json.Marshal(e.Candidate)
		if err != nil {
			return fmt.Errorf("failed to marshal pending candidate for oid %s: %w", e.OID, err)
		}
		if _, err := db.txExecContext(ctx, tx, `
			INSERT INTO lfs_pending_metadata (oid, candidate_json, created_time, expires_time)
			VALUES (?, ?, ?, ?)
			ON CONFLICT (oid) DO UPDATE SET
				candidate_json = EXCLUDED.candidate_json,
				created_time = EXCLUDED.created_time,
				expires_time = EXCLUDED.expires_time
		`, e.OID, string(raw), e.CreatedAt.UTC(), e.ExpiresAt.UTC()); err != nil {
			return fmt.Errorf("failed to save pending metadata for oid %s: %w", e.OID, err)
		}
	}
	return tx.Commit()
}

func (db *Store) GetPendingMetadata(ctx context.Context, oid string) (*transferlfs.PendingMetadata, error) {
	if _, err := db.execContext(ctx, `DELETE FROM lfs_pending_metadata WHERE expires_time <= ?`, time.Now().UTC()); err != nil {
		return nil, fmt.Errorf("failed to prune expired pending metadata: %w", err)
	}

	var (
		raw       string
		createdAt time.Time
		expiresAt time.Time
	)
	if err := db.queryRowContext(ctx, `
		SELECT candidate_json, created_time, expires_time
		FROM lfs_pending_metadata
		WHERE oid = ? AND expires_time > ?
	`, oid, time.Now().UTC()).Scan(&raw, &createdAt, &expiresAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("%w: pending metadata not found", errorapi.ErrNotFound)
		}
		return nil, fmt.Errorf("failed to load pending metadata for oid %s: %w", oid, err)
	}

	var c lfsapi.DrsObjectCandidate
	if err := json.Unmarshal([]byte(raw), &c); err != nil {
		return nil, fmt.Errorf("failed to parse pending metadata candidate for oid %s: %w", oid, err)
	}

	return &transferlfs.PendingMetadata{
		OID:       oid,
		Candidate: c,
		CreatedAt: createdAt,
		ExpiresAt: expiresAt,
	}, nil
}

// ConsumePendingMetadata removes the expected pending entry if it is still
// the entry that was read by the caller. A missing or replaced entry is an
// expected no-op: another stage or verification operation owns that metadata.
func (db *Store) ConsumePendingMetadata(ctx context.Context, expected transferlfs.PendingMetadata) (bool, error) {
	tx, err := db.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()

	var (
		raw       string
		createdAt time.Time
		expiresAt time.Time
	)
	if err := db.txQueryRowContext(ctx, tx, `
		SELECT candidate_json, created_time, expires_time
		FROM lfs_pending_metadata
		WHERE oid = ?
	`, expected.OID).Scan(&raw, &createdAt, &expiresAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, fmt.Errorf("failed to load current pending metadata for oid %s: %w", expected.OID, err)
	}

	var current lfsapi.DrsObjectCandidate
	if err := json.Unmarshal([]byte(raw), &current); err != nil {
		return false, fmt.Errorf("failed to parse current pending metadata candidate for oid %s: %w", expected.OID, err)
	}
	currentCanonical, err := json.Marshal(current)
	if err != nil {
		return false, fmt.Errorf("failed to marshal current pending metadata candidate for oid %s: %w", expected.OID, err)
	}
	expectedCanonical, err := json.Marshal(expected.Candidate)
	if err != nil {
		return false, fmt.Errorf("failed to marshal expected pending metadata candidate for oid %s: %w", expected.OID, err)
	}
	if !createdAt.Equal(expected.CreatedAt) || !expiresAt.Equal(expected.ExpiresAt) || !bytes.Equal(currentCanonical, expectedCanonical) {
		return false, nil
	}

	candidateCondition := "candidate_json = ?"
	if strings.HasPrefix(db.dialect.Rebind("?"), "$") {
		candidateCondition = "candidate_json = CAST(? AS JSONB)"
	}
	result, err := db.txExecContext(ctx, tx, fmt.Sprintf(`
		DELETE FROM lfs_pending_metadata
		WHERE oid = ? AND %s AND created_time = ? AND expires_time = ?
	`, candidateCondition), expected.OID, raw, createdAt, expiresAt)
	if err != nil {
		return false, fmt.Errorf("failed to consume pending metadata for oid %s: %w", expected.OID, err)
	}
	if affected, err := result.RowsAffected(); err != nil {
		return false, fmt.Errorf("failed to inspect pending metadata consumption for oid %s: %w", expected.OID, err)
	} else if affected == 0 {
		return false, nil
	}

	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("failed to commit pending metadata consumption for oid %s: %w", expected.OID, err)
	}
	return true, nil
}
