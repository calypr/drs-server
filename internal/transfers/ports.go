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
	BeginMultipart(context.Context, storage.BeginMultipartRequest) (storage.UploadID, error)
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

type MultipartSessionStore interface {
	SaveMultipartSession(context.Context, MultipartSession) error
	GetMultipartSession(context.Context, string) (MultipartSession, error)
	ClaimMultipartCompletion(context.Context, string, string, string, time.Time, time.Time) (MultipartSession, bool, error)
	ReleaseMultipartCompletion(context.Context, string, string, time.Time) error
	FinishMultipartCompletion(context.Context, string, string, string, time.Time) (bool, error)
}

// Optional multipart capabilities preserve compatibility for callers with
// lightweight session stores while the durable store can persist operation
// intent and completion inputs atomically.
type MultipartCompletionPartsClaimer interface {
	ClaimMultipartCompletionWithParts(context.Context, string, string, string, []CompletedPart, time.Time, time.Time) (MultipartSession, bool, error)
}

type MultipartAbortStore interface {
	ClaimMultipartAbort(context.Context, string, string, time.Time, time.Time) (MultipartSession, bool, error)
	FinishMultipartAbort(context.Context, string, string, time.Time) (bool, error)
}

type MultipartActivityStore interface {
	TouchMultipartSession(context.Context, string, time.Time) error
}

type MultipartCompletionReceipt struct {
	UploadID          string
	Authorization     MultipartAuthorization
	PartsFingerprint  string
	CompletedLocation string
}

type MultipartCompletionReceiptStore interface {
	GetMultipartCompletionReceipt(context.Context, string) (MultipartCompletionReceipt, error)
}

type MultipartReconcilerStore interface {
	ListMultipartSessionsForReconcile(context.Context, time.Time, time.Time, int) ([]MultipartSession, error)
	CompactCompletedMultipartSessions(context.Context, time.Time, int) error
}

type MultipartAborter interface {
	AbortMultipart(context.Context, storage.AbortMultipartRequest) error
}

type Dependencies struct {
	Objects              ObjectPort
	Storage              StoragePort
	FileCounters         usage.FileCounterRecorder
	Scopes               ScopeReader
	Credentials          CredentialReader
	Events               EventRecorder
	MultipartSessions    MultipartSessionStore
	Now                  func() time.Time
	DefaultSigningExpiry time.Duration
}
