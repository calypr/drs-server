package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/calypr/syfon/internal/models"
	"time"

	"github.com/calypr/syfon/apigen/server/drs"
)

func (db *PostgresDB) SavePendingLFSMeta(ctx context.Context, entries []models.PendingLFSMeta) error {
	if len(entries) == 0 {
		return nil
	}
	tx, err := db.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// 1. Housekeeping: remove expired
	if _, err := tx.ExecContext(ctx, `DELETE FROM lfs_pending_metadata WHERE expires_time <= $1`, time.Now().UTC()); err != nil {
		return fmt.Errorf("failed to prune expired pending metadata: %w", err)
	}

	// 2. Insert or update candidates
	for _, e := range entries {
		raw, err := json.Marshal(e.Candidate)
		if err != nil {
			return fmt.Errorf("failed to marshal candidate for oid %s: %w", e.OID, err)
		}
		_, err = tx.ExecContext(ctx, `
			INSERT INTO lfs_pending_metadata (oid, candidate_json, created_time, expires_time)
			VALUES ($1, $2, $3, $4)
			ON CONFLICT (oid) DO UPDATE SET
				candidate_json = EXCLUDED.candidate_json,
				created_time = EXCLUDED.created_time,
				expires_time = EXCLUDED.expires_time
		`, e.OID, string(raw), e.CreatedAt.UTC(), e.ExpiresAt.UTC())
		if err != nil {
			return fmt.Errorf("failed to save candidate for oid %s: %w", e.OID, err)
		}
	}
	return tx.Commit()
}

func (db *PostgresDB) GetPendingLFSMeta(ctx context.Context, oid string) (*models.PendingLFSMeta, error) {
	// Housekeeping (optional here but good for safety)
	if _, err := db.db.ExecContext(ctx, "DELETE FROM lfs_pending_metadata WHERE expires_time <= $1", time.Now().UTC()); err != nil {
		return nil, fmt.Errorf("failed to prune expired pending metadata: %w", err)
	}

	var (
		raw       []byte
		createdAt time.Time
		expiresAt time.Time
	)
	err := db.db.QueryRowContext(ctx, `
		SELECT candidate_json, created_time, expires_time
		FROM lfs_pending_metadata
		WHERE oid = $1 AND expires_time > $2
	`, oid, time.Now().UTC()).Scan(&raw, &createdAt, &expiresAt)

	if err != nil {
		return nil, fmt.Errorf("pending metadata not found: %w", err)
	}

	var candidate drs.DrsObjectCandidate
	if err := json.Unmarshal(raw, &candidate); err != nil {
		return nil, fmt.Errorf("failed to unmarshal candidate: %w", err)
	}

	return &models.PendingLFSMeta{
		OID:       oid,
		Candidate: candidate,
		CreatedAt: createdAt,
		ExpiresAt: expiresAt,
	}, nil
}

func (db *PostgresDB) PopPendingLFSMeta(ctx context.Context, oid string) (*models.PendingLFSMeta, error) {
	tx, err := db.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	// 1. Housekeeping
	if _, err := tx.ExecContext(ctx, "DELETE FROM lfs_pending_metadata WHERE expires_time <= $1", time.Now().UTC()); err != nil {
		return nil, fmt.Errorf("failed to prune expired pending metadata: %w", err)
	}

	// 2. Fetch
	var (
		raw       []byte
		createdAt time.Time
		expiresAt time.Time
	)
	err = tx.QueryRowContext(ctx, `
		SELECT candidate_json, created_time, expires_time
		FROM lfs_pending_metadata
		WHERE oid = $1 AND expires_time > $2
	`, oid, time.Now().UTC()).Scan(&raw, &createdAt, &expiresAt)

	if err != nil {
		return nil, fmt.Errorf("pending metadata not found: %w", err)
	}

	// 3. Delete
	if _, err := tx.ExecContext(ctx, "DELETE FROM lfs_pending_metadata WHERE oid = $1", oid); err != nil {
		return nil, err
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}

	var candidate drs.DrsObjectCandidate
	if err := json.Unmarshal(raw, &candidate); err != nil {
		return nil, fmt.Errorf("failed to unmarshal candidate: %w", err)
	}

	return &models.PendingLFSMeta{
		OID:       oid,
		Candidate: candidate,
		CreatedAt: createdAt,
		ExpiresAt: expiresAt,
	}, nil
}
