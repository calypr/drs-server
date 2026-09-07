package buckets

import "context"

// CredentialReader reads configured bucket credentials.
type CredentialReader interface {
	GetS3Credential(ctx context.Context, bucket string) (*Credential, error)
	ListS3Credentials(ctx context.Context) ([]Credential, error)
}

// CredentialAdmin owns credential creation and deletion.
type CredentialAdmin interface {
	SaveS3Credential(ctx context.Context, cred *Credential) error
	DeleteS3Credential(ctx context.Context, bucket string) error
}

// ScopeStore owns bucket-scope lifecycle and lookup.
type ScopeStore interface {
	CreateBucketScope(ctx context.Context, scope *Scope) error
	DeleteBucketScope(ctx context.Context, organization, projectID, credentialID, pathPrefix string) error
	GetBucketScope(ctx context.Context, organization, projectID string) (*Scope, error)
	ListBucketScopes(ctx context.Context) ([]Scope, error)
}

// VisibilityQuery is an optional object-projection optimization for bucket
// visibility. The bucket service supplies the object-scan fallback.
type VisibilityQuery interface {
	ListBucketVisibilityRows(ctx context.Context, resources []string, includeUnscoped, restrictToResources bool) ([]VisibilityRow, error)
}

// VisibilityFallback supplies the object-derived visibility projection when a
// persistence adapter does not provide VisibilityQuery. The composition layer
// owns object scanning and read-policy filtering before returning these rows.
type VisibilityFallback func(context.Context) ([]VisibilityRow, error)
