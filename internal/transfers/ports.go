package transfers

import (
	"context"
	"time"

	"github.com/calypr/syfon/apigen/drs"
	"github.com/calypr/syfon/internal/buckets"
	"github.com/calypr/syfon/internal/storage"
	"github.com/calypr/syfon/internal/usage"
)

// ObjectPort is the catalog capability required by transfer operations.
type ObjectPort interface {
	GetObject(context.Context, string, string) (*drs.DrsObject, error)
	GetObjectsByChecksums(context.Context, []string, string) (map[string][]drs.DrsObject, error)
}

// StoragePort is the provider-neutral storage capability required by
// transfer operations. Target selection and credential binding stay below
// this boundary.
type StoragePort interface {
	Sign(context.Context, storage.SignRequest) (storage.SignedAccess, error)
	BeginMultipart(context.Context, storage.Target) (storage.UploadID, error)
	SignMultipartPart(context.Context, storage.MultipartPartRequest) (storage.SignedAccess, error)
	CompleteMultipart(context.Context, storage.CompleteMultipartRequest) error
}

type ScopeReader interface {
	LookupBucketScope(context.Context, string, string) (buckets.Scope, bool, error)
}

type CredentialReader interface {
	ListS3Credentials(context.Context) ([]buckets.Credential, error)
}

type EventRecorder interface {
	RecordTransferAttributionEvents(context.Context, []usage.Event) error
}

type Dependencies struct {
	Objects      ObjectPort
	Storage      StoragePort
	FileCounters usage.FileCounterRecorder
	Scopes       ScopeReader
	Credentials  CredentialReader
	Events       EventRecorder
	Now          func() time.Time
}
