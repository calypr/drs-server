package store

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"testing"

	"github.com/calypr/syfon/internal/buckets"
)

var errCredentialRowsInterrupted = errors.New("credential rows interrupted")

func TestCredentialListsReturnIterationErrors(t *testing.T) {
	tests := []struct {
		name    string
		columns []string
		row     []driver.Value
		list    func(context.Context, *Store) error
	}{
		{
			name:    "credentials",
			columns: []string{"credential_id", "bucket", "provider", "region", "access_key", "secret_key", "endpoint"},
			row:     []driver.Value{"credential", "bucket", "s3", "region", "access", "secret", "endpoint"},
			list: func(ctx context.Context, store *Store) error {
				_, err := store.ListS3Credentials(ctx)
				return err
			},
		},
		{
			name:    "scopes",
			columns: []string{"organization", "project_id", "credential_id", "bucket", "path_prefix"},
			row:     []driver.Value{"org", "project", "credential", "bucket", "prefix"},
			list: func(ctx context.Context, store *Store) error {
				_, err := store.ListBucketScopes(ctx)
				return err
			},
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			db := sql.OpenDB(iterationErrorConnector{columns: testCase.columns, row: testCase.row})
			t.Cleanup(func() { _ = db.Close() })
			store := &Store{db: db, dialect: iterationTestDialect{}, cipher: passthroughCredentialCodec{}}

			if err := testCase.list(context.Background(), store); !errors.Is(err, errCredentialRowsInterrupted) {
				t.Fatalf("list error=%v, want %v", err, errCredentialRowsInterrupted)
			}
		})
	}
}

type iterationErrorConnector struct {
	columns []string
	row     []driver.Value
}

func (c iterationErrorConnector) Connect(context.Context) (driver.Conn, error) {
	return iterationErrorConn{columns: c.columns, row: c.row}, nil
}

func (iterationErrorConnector) Driver() driver.Driver { return iterationErrorDriver{} }

type iterationErrorDriver struct{}

func (iterationErrorDriver) Open(string) (driver.Conn, error) {
	return nil, errors.New("use connector")
}

type iterationErrorConn struct {
	columns []string
	row     []driver.Value
}

func (iterationErrorConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("prepare is not supported")
}

func (iterationErrorConn) Close() error { return nil }

func (iterationErrorConn) Begin() (driver.Tx, error) {
	return nil, errors.New("transactions are not supported")
}

func (c iterationErrorConn) QueryContext(context.Context, string, []driver.NamedValue) (driver.Rows, error) {
	return &iterationErrorRows{columns: c.columns, row: c.row}, nil
}

type iterationErrorRows struct {
	columns []string
	row     []driver.Value
	yielded bool
}

func (r *iterationErrorRows) Columns() []string { return r.columns }

func (*iterationErrorRows) Close() error { return nil }

func (r *iterationErrorRows) Next(dest []driver.Value) error {
	if r.yielded {
		return errCredentialRowsInterrupted
	}
	if len(dest) != len(r.row) {
		return fmt.Errorf("destination columns=%d, want %d", len(dest), len(r.row))
	}
	copy(dest, r.row)
	r.yielded = true
	return nil
}

type iterationTestDialect struct{}

func (iterationTestDialect) Rebind(query string) string { return query }
func (iterationTestDialect) ListArgs(string, []string) (string, []any) {
	return "", nil
}
func (iterationTestDialect) MaxParameters() int { return 1 }
func (iterationTestDialect) LockContentWrite(context.Context, *sql.Tx) error {
	return nil
}
func (iterationTestDialect) Bootstrap(context.Context, *sql.DB) error { return nil }

type passthroughCredentialCodec struct{}

func (passthroughCredentialCodec) Prepare(_ context.Context, credential *buckets.Credential) (*buckets.Credential, error) {
	return credential, nil
}

func (passthroughCredentialCodec) Parse(_ context.Context, credential *buckets.Credential) (*buckets.Credential, error) {
	return credential, nil
}

func (passthroughCredentialCodec) Enabled() (bool, error) { return false, nil }

var _ driver.QueryerContext = iterationErrorConn{}
var _ driver.Rows = (*iterationErrorRows)(nil)
