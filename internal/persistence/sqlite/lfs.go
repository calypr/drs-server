package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/calypr/syfon/internal/faults"
	"github.com/calypr/syfon/internal/objects"
	transferlfs "github.com/calypr/syfon/internal/transfers/lfs"
)

func (db *SqliteDB) SavePendingMetadata(ctx context.Context, entries []transferlfs.PendingMetadata) error {
	if len(entries) == 0 {
		return nil
	}
	tx, err := db.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `DELETE FROM lfs_pending_metadata WHERE expires_time <= ?`, time.Now().UTC()); err != nil {
		return fmt.Errorf("failed to prune expired pending metadata: %w", err)
	}

	for _, e := range entries {
		raw, err := json.Marshal(e.Candidate)
		if err != nil {
			return fmt.Errorf("failed to marshal pending candidate for oid %s: %w", e.OID, err)
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO lfs_pending_metadata (oid, candidate_json, created_time, expires_time)
			VALUES (?, ?, ?, ?)
			ON CONFLICT (oid) DO UPDATE SET
				candidate_json = excluded.candidate_json,
				created_time = excluded.created_time,
				expires_time = excluded.expires_time
		`, e.OID, string(raw), e.CreatedAt.UTC(), e.ExpiresAt.UTC()); err != nil {
			return fmt.Errorf("failed to save pending metadata for oid %s: %w", e.OID, err)
		}
	}
	return tx.Commit()
}

func (db *SqliteDB) GetPendingMetadata(ctx context.Context, oid string) (*transferlfs.PendingMetadata, error) {
	if _, err := db.db.ExecContext(ctx, `DELETE FROM lfs_pending_metadata WHERE expires_time <= ?`, time.Now().UTC()); err != nil {
		return nil, fmt.Errorf("failed to prune expired pending metadata: %w", err)
	}

	var (
		raw       string
		createdAt time.Time
		expiresAt time.Time
	)
	if err := db.db.QueryRowContext(ctx, `
		SELECT candidate_json, created_time, expires_time
		FROM lfs_pending_metadata
		WHERE oid = ? AND expires_time > ?
	`, oid, time.Now().UTC()).Scan(&raw, &createdAt, &expiresAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("%w: pending metadata not found", faults.ErrNotFound)
		}
		return nil, fmt.Errorf("failed to load pending metadata for oid %s: %w", oid, err)
	}

	var c objects.Candidate
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

func (db *SqliteDB) PopPendingMetadata(ctx context.Context, oid string) (*transferlfs.PendingMetadata, error) {
	tx, err := db.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `DELETE FROM lfs_pending_metadata WHERE expires_time <= ?`, time.Now().UTC()); err != nil {
		return nil, fmt.Errorf("failed to prune expired pending metadata: %w", err)
	}

	var (
		raw       string
		createdAt time.Time
		expiresAt time.Time
	)
	if err := tx.QueryRowContext(ctx, `
		SELECT candidate_json, created_time, expires_time
		FROM lfs_pending_metadata
		WHERE oid = ? AND expires_time > ?
	`, oid, time.Now().UTC()).Scan(&raw, &createdAt, &expiresAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("%w: pending metadata not found", faults.ErrNotFound)
		}
		return nil, fmt.Errorf("failed to load pending metadata for oid %s: %w", oid, err)
	}

	if _, err := tx.ExecContext(ctx, `DELETE FROM lfs_pending_metadata WHERE oid = ?`, oid); err != nil {
		return nil, fmt.Errorf("failed to consume pending metadata for oid %s: %w", oid, err)
	}

	var c objects.Candidate
	if err := json.Unmarshal([]byte(raw), &c); err != nil {
		return nil, fmt.Errorf("failed to parse pending metadata candidate for oid %s: %w", oid, err)
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &transferlfs.PendingMetadata{
		OID:       oid,
		Candidate: c,
		CreatedAt: createdAt,
		ExpiresAt: expiresAt,
	}, nil
}
