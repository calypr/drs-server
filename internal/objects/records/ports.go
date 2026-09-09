package records

import (
	"context"

	objectmodel "github.com/calypr/syfon/internal/objects"
)

// ObjectStore is the complete persistence capability required by the record
// service. Concrete SQLite and Postgres stores implement this contract.
type ObjectStore interface {
	GetObject(context.Context, string) (*objectmodel.Record, error)
	GetBulkObjects(context.Context, []string) ([]objectmodel.Record, error)
	DeleteObject(context.Context, string) error
	BulkDeleteObjects(context.Context, []string) error
	RegisterObjects(context.Context, []objectmodel.Record) error
	ReplaceObjects(context.Context, []objectmodel.Record) error
	UpdateObjectAccessMethods(context.Context, string, []objectmodel.AccessMethod) error
	BulkUpdateAccessMethods(context.Context, map[string][]objectmodel.AccessMethod) error
	RemoveObjectControlledAccess(context.Context, string, string) error
	RemoveObjectControlledAccessBulk(context.Context, []string, string) (int, error)
	CreateObjectAlias(context.Context, string, string) error
	ResolveObjectAlias(context.Context, string) (string, error)
	GetObjectsByChecksum(context.Context, string) ([]objectmodel.Record, error)
	GetObjectsByChecksums(context.Context, []string) (map[string][]objectmodel.Record, error)
	ListScopedObjectIDsByChecksums(context.Context, string, string, []string) (map[string][]string, error)
	ListObjectIDsByScope(context.Context, string, string) ([]string, error)
	ListObjectIDsByResources(context.Context, []string, bool) ([]string, error)
	ListObjectIDsPageByScope(context.Context, string, string, string, int, int) ([]string, error)
	ListObjectIDsPageByURL(context.Context, string, string, string, string, int, int, []string, bool, bool) ([]string, error)
	ListObjectIDsByScopeAndResources(context.Context, string, string, []string, bool) ([]string, error)
	ListObjectIDsByChecksumsAndResources(context.Context, []string, []string, bool, bool) (map[string][]string, error)
}
