package store

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/calypr/syfon/internal/buckets"
)

// CredentialCodec is the persistence boundary for credential field encoding.
type CredentialCodec interface {
	Prepare(context.Context, *buckets.Credential) (*buckets.Credential, error)
	Parse(context.Context, *buckets.Credential) (*buckets.Credential, error)
	Enabled() (bool, error)
}

// Store owns the database handle and the shared, dialect-independent SQL
// operation state. SQLite and PostgreSQL constructors return this concrete
// implementation directly.
type Store struct {
	db      *sql.DB
	dialect Dialect
	cipher  CredentialCodec
}

type sqlExecutor interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func (s *Store) execOn(ctx context.Context, executor sqlExecutor, query string, args ...any) (sql.Result, error) {
	return executor.ExecContext(ctx, s.dialect.Rebind(query), args...)
}

func (s *Store) queryRowOn(ctx context.Context, executor sqlExecutor, query string, args ...any) *sql.Row {
	return executor.QueryRowContext(ctx, s.dialect.Rebind(query), args...)
}

// Open bootstraps db through dialect and returns the shared store. A nil codec
// is accepted for callers that do not use credential persistence.
func Open(db *sql.DB, dialect Dialect, cipher CredentialCodec) (*Store, error) {
	if db == nil {
		return nil, fmt.Errorf("database is required")
	}
	if dialect == nil {
		return nil, fmt.Errorf("database dialect is required")
	}
	if err := dialect.Bootstrap(context.Background(), db); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("database bootstrap failed: %w", err)
	}
	return &Store{db: db, dialect: dialect, cipher: cipher}, nil
}

// Close closes the database handle owned by the store.
func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

// DB exposes the owned handle to backend migration tests that must assert
// schema and transaction behavior directly.
func (s *Store) DB() *sql.DB {
	if s == nil {
		return nil
	}
	return s.db
}

func (s *Store) withContentWrite(ctx context.Context, fn func(*sql.Tx) error) error {
	if s == nil || s.db == nil {
		return fmt.Errorf("database store is required")
	}
	if s.dialect == nil {
		return fmt.Errorf("database dialect is required")
	}
	if fn == nil {
		return fmt.Errorf("content write callback is required")
	}
	return s.withWrite(ctx, func(tx *sql.Tx) error {
		if err := s.dialect.LockContentWrite(ctx, tx); err != nil {
			return err
		}
		return fn(tx)
	})
}

func (s *Store) withWrite(ctx context.Context, fn func(*sql.Tx) error) error {
	if s == nil || s.db == nil {
		return fmt.Errorf("database store is required")
	}
	if fn == nil {
		return fmt.Errorf("write callback is required")
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit()
}
