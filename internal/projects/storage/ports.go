package storage

import (
	"context"
	"time"

	"github.com/calypr/syfon/internal/buckets"
	"github.com/calypr/syfon/internal/objects"
	"github.com/calypr/syfon/internal/storage"
)

type ScopeResolver interface {
	ResolveStorageScope(context.Context, string, string) (buckets.StorageScope, error)
}

type CredentialReader interface {
	GetS3Credential(context.Context, string) (*buckets.Credential, error)
	ListS3Credentials(context.Context) ([]buckets.Credential, error)
}

type VisibilityReader interface {
	ListVisibleBuckets(context.Context) (map[string]buckets.VisibleBucket, error)
}

type InventoryPort interface {
	Inventory(context.Context, storage.InventoryRequest) (storage.InventoryResult, error)
}

type ProbePort interface {
	Probe(context.Context, []storage.ProbeTarget) []storage.ProbeResult
}

type DeletePort interface {
	DeleteExact(context.Context, []storage.DeleteTarget) error
}

type ObjectScopeDeleter interface {
	DeleteBulkByScope(context.Context, string, string) (int, error)
}

// RecordRepairer is the record capability used by the repair workflow. It is
// deliberately narrower than the objects service so tests can exercise repair
// behavior without constructing a persistence backend.
type RecordRepairer interface {
	ListPreparedObjectsPageByScope(context.Context, string, string, string, string, int, int) ([]objects.Record, error)
	UpdateRecord(context.Context, string, objects.Record, *int64, time.Time) (objects.Record, error)
	CollapseProjectChecksumDuplicates(context.Context, string, string) (int, error)
}

type ScopeCatalog interface {
	ListBucketScopes(context.Context) ([]buckets.Scope, error)
	DeleteBucketScope(context.Context, string, string, string, string) error
}

type Providers struct {
	Inventory InventoryPort
	Probe     ProbePort
	Delete    DeletePort
}

type Dependencies struct {
	ScopeResolver ScopeResolver
	Credentials   CredentialReader
	Visibility    VisibilityReader
	Records       RecordRepairer
	ObjectCleanup ObjectScopeDeleter
	ScopeCatalog  ScopeCatalog
	Providers     Providers
}
