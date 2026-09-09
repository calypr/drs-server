// Package testsuite contains the backend-independent persistence contract.
package testsuite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"testing"

	"github.com/calypr/syfon/apigen/errorapi"
	"github.com/calypr/syfon/internal/buckets"
	"github.com/calypr/syfon/internal/objects"
	"github.com/calypr/syfon/internal/persistence/store"
	transferlfs "github.com/calypr/syfon/internal/transfers/lfs"
	"github.com/calypr/syfon/internal/usage"
)

// StoreFactory opens a fully bootstrapped backend store and returns its cleanup.
type StoreFactory func(*testing.T) (*store.Store, func())

// StoreCase is a behavior assertion that can run against either SQL backend.
type StoreCase func(*testing.T, *store.Store)

// RunStoreContract runs the common cases and closes the factory-owned store.
func RunStoreContract(t *testing.T, factory StoreFactory, cases []StoreCase) {
	t.Helper()
	for i, testCase := range cases {
		t.Run(caseName(i), func(t *testing.T) {
			shared, cleanup := factory(t)
			if cleanup != nil {
				defer cleanup()
			}
			if shared == nil {
				t.Fatal("store factory returned nil store")
			}
			testCase(t, shared)
		})
	}
}

// BasicStoreCases covers behavior that is independent of SQL placeholder and
// migration details. Backend packages add their dialect-specific assertions.
func BasicStoreCases() []StoreCase {
	return []StoreCase{
		func(t *testing.T, shared *store.Store) {
			if _, err := shared.GetObject(context.Background(), "missing"); err == nil {
				t.Fatal("GetObject(missing) returned nil error")
			}
		},
		func(t *testing.T, shared *store.Store) {
			credentials, err := shared.ListS3Credentials(context.Background())
			if err != nil {
				t.Fatalf("ListS3Credentials: %v", err)
			}
			if credentials == nil {
				t.Fatal("ListS3Credentials returned nil slice")
			}
		},
		func(t *testing.T, shared *store.Store) {
			if _, err := shared.GetS3Credential(context.Background(), "missing"); !errors.Is(err, errorapi.ErrStorageCredentialMissing) {
				t.Fatalf("GetS3Credential error=%v, want storage credential missing", err)
			}
		},
		func(t *testing.T, shared *store.Store) {
			if err := shared.DeleteS3Credential(context.Background(), "missing"); !errors.Is(err, errorapi.ErrStorageCredentialMissing) {
				t.Fatalf("DeleteS3Credential error=%v, want storage credential missing", err)
			}
		},
		func(t *testing.T, shared *store.Store) {
			rows, err := shared.ListBucketVisibilityRows(context.Background(), nil, false, true)
			if err != nil {
				t.Fatalf("ListBucketVisibilityRows: %v", err)
			}
			if len(rows) != 0 {
				t.Fatalf("ListBucketVisibilityRows returned %+v for an empty restricted resource set", rows)
			}
		},
		func(t *testing.T, shared *store.Store) {
			if err := shared.RecordTransferAttributionEvents(context.Background(), nil); err != nil {
				t.Fatalf("RecordTransferAttributionEvents(nil): %v", err)
			}
		},
		func(t *testing.T, shared *store.Store) {
			if err := shared.RecordProviderTransferEvents(context.Background(), nil); err != nil {
				t.Fatalf("RecordProviderTransferEvents(nil): %v", err)
			}
		},
		func(t *testing.T, shared *store.Store) {
			if err := shared.BulkDeleteObjects(context.Background(), nil); err != nil {
				t.Fatalf("BulkDeleteObjects(nil): %v", err)
			}
		},
	}
}

func caseName(i int) string { return fmt.Sprintf("case-%d", i) }

// SQLMockDialect suppresses schema bootstrap while retaining the backend SQL
// dialect for sqlmock assertions.
type SQLMockDialect struct{ store.Dialect }

func (SQLMockDialect) Bootstrap(context.Context, *sql.DB) error { return nil }

// BulkObjectCondition preserves optional backend SQL capabilities through the
// bootstrap-suppressing test wrapper. The shared Store discovers these
// capabilities by interface, so embedding only store.Dialect would otherwise
// silently select its generic SQL path.
func (d SQLMockDialect) BulkObjectCondition(ids, checksums, shaQueries, genericQueries []string, start int) (string, []any) {
	if dialect, ok := d.Dialect.(interface {
		BulkObjectCondition([]string, []string, []string, []string, int) (string, []any)
	}); ok {
		return dialect.BulkObjectCondition(ids, checksums, shaQueries, genericQueries, start)
	}
	return "", nil
}

// ResourceFilter preserves optional backend SQL capabilities through the same
// wrapper for scoped URL queries.
func (d SQLMockDialect) ResourceFilter(column string, resources []string, includeUnscoped bool, start int) (string, []any) {
	if dialect, ok := d.Dialect.(interface {
		ResourceFilter(string, []string, bool, int) (string, []any)
	}); ok {
		return dialect.ResourceFilter(column, resources, includeUnscoped, start)
	}
	return "", nil
}

// OpenSQLMockStore is the explicit constructor for PostgreSQL sqlmock cases.
func OpenSQLMockStore(db *sql.DB, dialect store.Dialect, codec store.CredentialCodec) (*store.Store, error) {
	return store.Open(db, SQLMockDialect{Dialect: dialect}, codec)
}

// Compile-time capability checks keep both backends on the same concrete API.
var (
	_ objects.ObjectStore      = (*store.Store)(nil)
	_ usage.ReportStore        = (*store.Store)(nil)
	_ buckets.CredentialReader = (*store.Store)(nil)
	_ buckets.CredentialAdmin  = (*store.Store)(nil)
	_ buckets.ScopeStore       = (*store.Store)(nil)
	_ buckets.VisibilityQuery  = (*store.Store)(nil)
	_ transferlfs.PendingStore = (*store.Store)(nil)
)
