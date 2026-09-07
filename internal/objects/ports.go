package objects

import "context"

// RecordReader reads physical object records.
type RecordReader interface {
	GetObject(ctx context.Context, id string) (*Record, error)
	GetBulkObjects(ctx context.Context, ids []string) ([]Record, error)
}

// RecordWriter mutates physical object records.
type RecordWriter interface {
	DeleteObject(ctx context.Context, id string) error
	CreateObject(ctx context.Context, obj *Record) error
	BulkDeleteObjects(ctx context.Context, ids []string) error
	RegisterObjects(ctx context.Context, objects []Record) error
	ReplaceObjects(ctx context.Context, objects []Record) error
}

// AccessMethodWriter updates the provider access methods attached to records.
type AccessMethodWriter interface {
	UpdateObjectAccessMethods(ctx context.Context, objectID string, accessMethods []AccessMethod) error
	BulkUpdateAccessMethods(ctx context.Context, updates map[string][]AccessMethod) error
}

// AccessPolicyWriter updates controlled-access policy on records.
type AccessPolicyWriter interface {
	RemoveObjectControlledAccess(ctx context.Context, objectID, resource string) error
	RemoveObjectControlledAccessBulk(ctx context.Context, objectIDs []string, resource string) (int, error)
}

// AliasStore owns physical-to-canonical object alias operations.
type AliasStore interface {
	DeleteObjectAlias(ctx context.Context, aliasID string) error
	CreateObjectAlias(ctx context.Context, aliasID, canonicalObjectID string) error
	ResolveObjectAlias(ctx context.Context, aliasID string) (string, error)
}

// ContentReader reads physical records that share a checksum.
type ContentReader interface {
	GetObjectsByChecksum(ctx context.Context, checksum string) ([]Record, error)
	GetObjectsByChecksums(ctx context.Context, checksums []string) (map[string][]Record, error)
}

// ChecksumScopeQuery expands checksums within a specific object scope.
type ChecksumScopeQuery interface {
	ListScopedObjectIDsByChecksums(ctx context.Context, organization, project string, checksums []string) (map[string][]string, error)
}

// ScopeQuery lists object IDs in a specific object scope.
type ScopeQuery interface {
	ListObjectIDsByScope(ctx context.Context, organization, project string) ([]string, error)
}

// OptionalResourceQuery is an optional authorization-aware object ID query.
type OptionalResourceQuery interface {
	ListObjectIDsByResources(ctx context.Context, resources []string, includeUnscoped bool) ([]string, error)
}

// OptionalPageQuery is an optional pagination optimization for object IDs.
type OptionalPageQuery interface {
	ListObjectIDsPageByScope(ctx context.Context, organization, project, startAfter string, limit, offset int) ([]string, error)
	ListObjectIDsPageByResources(ctx context.Context, resources []string, includeUnscoped bool, startAfter string, limit, offset int) ([]string, error)
}

// OptionalURLQuery is an optional URL-filtered pagination optimization.
type OptionalURLQuery interface {
	ListObjectIDsPageByURL(ctx context.Context, objectURL, organization, project, startAfter string, limit, offset int, resources []string, includeUnscoped, restrictToResources bool) ([]string, error)
}

// OptionalAuthorizedQuery is an optional authorization-aware bulk query capability.
type OptionalAuthorizedQuery interface {
	ListObjectIDsByScopeAndResources(ctx context.Context, organization, project string, resources []string, restrictToResources bool) ([]string, error)
	ListObjectIDsByChecksumsAndResources(ctx context.Context, checksums []string, resources []string, includeUnscoped, restrictToResources bool) (map[string][]string, error)
}
