package projectstorage

import (
	"context"

	"github.com/calypr/syfon/internal/buckets"
	"github.com/calypr/syfon/internal/storage"
)

type ScopeReader interface {
	LookupBucketScope(context.Context, string, string) (buckets.Scope, bool, error)
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

type ScopeCatalog interface {
	ListBucketScopes(context.Context) ([]buckets.Scope, error)
	DeleteBucketScope(context.Context, string, string, string, string) error
}

type Dependencies struct {
	Scopes         ScopeReader
	Credentials    CredentialReader
	Visibility     VisibilityReader
	Inventory      InventoryPort
	Probe          ProbePort
	Delete         DeletePort
	Physical       PhysicalScopeReader
	CleanupObjects ObjectScopeDeleter
	CleanupScopes  ScopeCatalog
}
