package transfers

import (
	"context"

	"github.com/calypr/syfon/internal/buckets"
	"github.com/calypr/syfon/internal/storage"
	"github.com/calypr/syfon/internal/usage"
)

type AccessPort interface {
	Access(context.Context, storage.AccessRequest) (storage.Access, error)
}

// MultipartPort owns provider-specific multipart operations. Upload IDs are
// intentionally opaque and are never parsed or normalized by transfers.
type MultipartPort interface {
	BeginMultipart(context.Context, storage.ObjectTarget) (storage.UploadID, error)
	AccessMultipartPart(context.Context, storage.MultipartPartRequest) (storage.Access, error)
	CompleteMultipart(context.Context, storage.CompleteMultipartRequest) error
}

type ScopeReader interface {
	LookupBucketScope(context.Context, string, string) (buckets.Scope, bool, error)
}

type CredentialReader interface {
	ListS3Credentials(context.Context) ([]buckets.Credential, error)
}

type EventRecorder interface {
	RecordTransferAttributionEvents(ctx context.Context, events []usage.Event) error
}

// Dependencies are the consumer-owned ports used by Service. Each field is
// optional until the corresponding workflow is invoked, which lets callers
// compose only the capabilities they expose.
type Dependencies struct {
	Access      AccessPort
	Multipart   MultipartPort
	Scopes      ScopeReader
	Credentials CredentialReader
	Events      EventRecorder
}
