package lfs

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/calypr/syfon/internal/buckets"
	"github.com/calypr/syfon/internal/faults"
	"github.com/calypr/syfon/internal/objects"
	"github.com/calypr/syfon/internal/storage"
	"github.com/calypr/syfon/internal/transfers"
	"github.com/calypr/syfon/internal/usage"
)

var (
	ErrNoBucketConfigured = errors.New("no bucket configured")
	ErrNoObjectLocation   = errors.New("no object location available")
)

type PreparationObjectPort interface {
	GetObject(context.Context, string, string) (*objects.Record, error)
	RequireObjectResources(context.Context, string, []string) error
}

type DownloadAccounting interface {
	RecordFileDownload(context.Context, string) error
}

type DownloadPreparation struct {
	SignedURL string
}

type DownloadLookupError struct {
	Err error
}

func (e *DownloadLookupError) Error() string {
	if e == nil || e.Err == nil {
		return "object lookup failed"
	}
	return e.Err.Error()
}

func (e *DownloadLookupError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

type UploadPreparation struct {
	Existing bool
	Size     int64
}

type UploadTarget struct {
	Bucket   string
	Key      string
	ObjectID string
}

type PreparationWorkflow struct {
	transfer    *transfers.Service
	objects     PreparationObjectPort
	credentials buckets.CredentialReader
	pending     PendingStore
	downloads   DownloadAccounting
	now         func() time.Time
}

func NewPreparationWorkflow(transfer *transfers.Service, objectPort PreparationObjectPort, credentials buckets.CredentialReader, pending PendingStore, downloads DownloadAccounting) *PreparationWorkflow {
	return &PreparationWorkflow{
		transfer:    transfer,
		objects:     objectPort,
		credentials: credentials,
		pending:     pending,
		downloads:   downloads,
		now:         time.Now,
	}
}

func (w *PreparationWorkflow) PrepareDownload(ctx context.Context, oid string) (DownloadPreparation, error) {
	object, err := w.objects.GetObject(ctx, oid, "read")
	if err != nil {
		return DownloadPreparation{}, &DownloadLookupError{Err: err}
	}

	var sourceURL, accessID string
	if object.AccessMethods != nil {
		for _, method := range *object.AccessMethods {
			if method.AccessUrl == nil || strings.TrimSpace(method.AccessUrl.Url) == "" {
				continue
			}
			sourceURL = method.AccessUrl.Url
			if method.AccessId != nil && strings.TrimSpace(*method.AccessId) != "" {
				accessID = strings.TrimSpace(*method.AccessId)
			} else {
				accessID = strings.TrimSpace(method.Type)
			}
			break
		}
	}
	if sourceURL == "" {
		return DownloadPreparation{}, ErrNoObjectLocation
	}

	signedURL, err := w.transfer.SignObjectURL(ctx, object, sourceURL, storage.AccessOptions{})
	if err != nil {
		return DownloadPreparation{}, err
	}
	if w.downloads == nil {
		return DownloadPreparation{}, errors.New("file counters are not configured")
	}
	if err := w.downloads.RecordFileDownload(ctx, oid); err != nil {
		return DownloadPreparation{}, err
	}
	if err := w.transfer.RecordAccessIssued(ctx, transfers.AccessRequest{
		Object:     object,
		AccessID:   accessID,
		Direction:  usage.ProviderTransferDirectionDownload,
		StorageURL: sourceURL,
	}); err != nil {
		return DownloadPreparation{}, err
	}
	return DownloadPreparation{SignedURL: signedURL}, nil
}

func (w *PreparationWorkflow) PrepareUpload(ctx context.Context, oid string, reqSize int64) (UploadPreparation, error) {
	result := UploadPreparation{Size: reqSize}
	existing, err := w.objects.GetObject(ctx, oid, "read")
	if err == nil {
		return UploadPreparation{Existing: true, Size: existing.Size}, nil
	}
	if !faults.IsNotFoundError(err) {
		return result, err
	}
	if err := w.objects.RequireObjectResources(ctx, "create", []string{"/data_file"}); err != nil {
		return result, err
	}
	if !w.hasConfiguredBucket(ctx) {
		return result, ErrNoBucketConfigured
	}
	if reqSize < 0 {
		result.Size = 0
	}
	return result, nil
}

func (w *PreparationWorkflow) ResolveUploadTarget(ctx context.Context, oid string) (UploadTarget, error) {
	defaultBucket, err := w.firstConfiguredBucket(ctx)
	if err != nil {
		return UploadTarget{}, err
	}
	if object, getErr := w.objects.GetObject(ctx, oid, "read"); getErr == nil {
		return w.targetForObject(ctx, object)
	} else if !faults.IsNotFoundError(getErr) {
		return UploadTarget{}, getErr
	}

	if pending, getErr := w.getPendingMetadata(ctx, oid); getErr == nil {
		object, conversionErr := objects.CandidateToRecord(pending.Candidate, w.now().UTC())
		if conversionErr != nil {
			return UploadTarget{}, conversionErr
		}
		return w.targetForObject(ctx, &object)
	} else if !faults.IsNotFoundError(getErr) {
		return UploadTarget{}, getErr
	}
	return UploadTarget{Bucket: defaultBucket, Key: oid, ObjectID: oid}, nil
}

func (w *PreparationWorkflow) getPendingMetadata(ctx context.Context, oid string) (*PendingMetadata, error) {
	if w.pending == nil {
		return nil, errors.New("pending LFS metadata store is not configured")
	}
	return w.pending.GetPendingMetadata(ctx, oid)
}

func (w *PreparationWorkflow) firstConfiguredBucket(ctx context.Context) (string, error) {
	if w.credentials == nil {
		return "", ErrNoBucketConfigured
	}
	credentials, err := w.credentials.ListS3Credentials(ctx)
	if err != nil {
		return "", err
	}
	if len(credentials) == 0 || strings.TrimSpace(credentials[0].Bucket) == "" {
		return "", ErrNoBucketConfigured
	}
	return strings.TrimSpace(credentials[0].Bucket), nil
}

func (w *PreparationWorkflow) hasConfiguredBucket(ctx context.Context) bool {
	_, err := w.firstConfiguredBucket(ctx)
	return err == nil
}

func (w *PreparationWorkflow) targetForObject(ctx context.Context, object *objects.Record) (UploadTarget, error) {
	target, err := w.transfer.ResolveCanonicalStorageTarget(ctx, transfers.CanonicalStorageTargetRequest{
		Object:         object,
		PreferChecksum: true,
	})
	if err != nil {
		return UploadTarget{}, err
	}
	if target.Bucket == "" || target.Key == "" {
		return UploadTarget{}, fmt.Errorf("canonical LFS upload location is not an s3 url")
	}
	return UploadTarget{Bucket: target.Bucket, Key: target.Key, ObjectID: string(object.Id)}, nil
}
