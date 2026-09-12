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
	SaveBucketConfiguration(ctx context.Context, configuration BucketConfiguration) error
	DeleteBucketScopeConfiguration(ctx context.Context, scope Scope) ([]string, error)
	DeleteS3Credential(ctx context.Context, bucket string) error
}

// ScopeStore owns bucket-scope lifecycle and lookup.
type ScopeStore interface {
	CreateBucketScope(ctx context.Context, scope *Scope) error
	GetBucketScope(ctx context.Context, organization, projectID string) (*Scope, error)
	ListBucketScopes(ctx context.Context) ([]Scope, error)
}

// VisibilityQuery supplies the object projection used to resolve bucket
// visibility.
type VisibilityQuery interface {
	ListBucketVisibilityRows(ctx context.Context, resources []string, includeUnscoped, restrictToResources bool) ([]VisibilityRow, error)
}
