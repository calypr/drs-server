package transfers

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
	"github.com/calypr/syfon/internal/usage"
)

var (
	ErrLFSNoBucketConfigured = errors.New("no bucket configured")
	ErrLFSNoObjectLocation   = errors.New("no object location available")
)

type LFSPreparationObjectPort interface {
	GetObject(context.Context, string, string) (*objects.Record, error)
	RequireObjectResources(context.Context, string, []string) error
}

type LFSDownloadAccounting interface {
	RecordFileDownload(context.Context, string) error
}

type LFSDownloadPreparation struct {
	SignedURL string
}

type LFSDownloadLookupError struct {
	Err error
}

func (e *LFSDownloadLookupError) Error() string {
	if e == nil || e.Err == nil {
		return "object lookup failed"
	}
	return e.Err.Error()
}

func (e *LFSDownloadLookupError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

type LFSUploadPreparation struct {
	Existing bool
	Size     int64
}

type LFSUploadTarget struct {
	Bucket   string
	Key      string
	ObjectID string
}

type LFSPreparationWorkflow struct {
	transfer    *Service
	objects     LFSPreparationObjectPort
	credentials buckets.CredentialReader
	downloads   LFSDownloadAccounting
	now         func() time.Time
}

func NewLFSPreparationWorkflow(transfer *Service, objectPort LFSPreparationObjectPort, credentials buckets.CredentialReader, downloads LFSDownloadAccounting) *LFSPreparationWorkflow {
	return &LFSPreparationWorkflow{
		transfer:    transfer,
		objects:     objectPort,
		credentials: credentials,
		downloads:   downloads,
		now:         time.Now,
	}
}

func (w *LFSPreparationWorkflow) PrepareDownload(ctx context.Context, oid string) (LFSDownloadPreparation, error) {
	object, err := w.objects.GetObject(ctx, oid, "read")
	if err != nil {
		return LFSDownloadPreparation{}, &LFSDownloadLookupError{Err: err}
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
		return LFSDownloadPreparation{}, ErrLFSNoObjectLocation
	}

	signedURL, err := w.transfer.SignObjectURL(ctx, object, sourceURL, storage.AccessOptions{})
	if err != nil {
		return LFSDownloadPreparation{}, err
	}
	if w.downloads == nil {
		return LFSDownloadPreparation{}, errors.New("file counters are not configured")
	}
	if err := w.downloads.RecordFileDownload(ctx, oid); err != nil {
		return LFSDownloadPreparation{}, err
	}
	if err := w.transfer.RecordAccessIssued(ctx, AccessRequest{
		Object:     object,
		AccessID:   accessID,
		Direction:  usage.ProviderTransferDirectionDownload,
		StorageURL: sourceURL,
	}); err != nil {
		return LFSDownloadPreparation{}, err
	}
	return LFSDownloadPreparation{SignedURL: signedURL}, nil
}

func (w *LFSPreparationWorkflow) PrepareUpload(ctx context.Context, oid string, reqSize int64) (LFSUploadPreparation, error) {
	result := LFSUploadPreparation{Size: reqSize}
	existing, err := w.objects.GetObject(ctx, oid, "read")
	if err == nil {
		return LFSUploadPreparation{Existing: true, Size: existing.Size}, nil
	}
	if !faults.IsNotFoundError(err) {
		return result, err
	}
	if err := w.objects.RequireObjectResources(ctx, "create", []string{"/data_file"}); err != nil {
		return result, err
	}
	if !w.hasConfiguredBucket(ctx) {
		return result, ErrLFSNoBucketConfigured
	}
	if reqSize < 0 {
		result.Size = 0
	}
	return result, nil
}

func (w *LFSPreparationWorkflow) ResolveUploadTarget(ctx context.Context, oid string) (LFSUploadTarget, error) {
	defaultBucket, err := w.firstConfiguredBucket(ctx)
	if err != nil {
		return LFSUploadTarget{}, err
	}
	if object, getErr := w.objects.GetObject(ctx, oid, "read"); getErr == nil {
		return w.targetForObject(ctx, object)
	} else if !faults.IsNotFoundError(getErr) {
		return LFSUploadTarget{}, getErr
	}

	if pending, getErr := w.transfer.GetPendingLFSMeta(ctx, oid); getErr == nil {
		object, conversionErr := objects.CandidateToRecord(pending.Candidate, w.now().UTC())
		if conversionErr != nil {
			return LFSUploadTarget{}, conversionErr
		}
		return w.targetForObject(ctx, &object)
	} else if !faults.IsNotFoundError(getErr) {
		return LFSUploadTarget{}, getErr
	}
	return LFSUploadTarget{Bucket: defaultBucket, Key: oid, ObjectID: oid}, nil
}

func (w *LFSPreparationWorkflow) firstConfiguredBucket(ctx context.Context) (string, error) {
	if w.credentials == nil {
		return "", ErrLFSNoBucketConfigured
	}
	credentials, err := w.credentials.ListS3Credentials(ctx)
	if err != nil {
		return "", err
	}
	if len(credentials) == 0 || strings.TrimSpace(credentials[0].Bucket) == "" {
		return "", ErrLFSNoBucketConfigured
	}
	return strings.TrimSpace(credentials[0].Bucket), nil
}

func (w *LFSPreparationWorkflow) hasConfiguredBucket(ctx context.Context) bool {
	_, err := w.firstConfiguredBucket(ctx)
	return err == nil
}

func (w *LFSPreparationWorkflow) targetForObject(ctx context.Context, object *objects.Record) (LFSUploadTarget, error) {
	target, err := w.transfer.ResolveCanonicalStorageTarget(ctx, CanonicalStorageTargetRequest{
		Object:         object,
		PreferChecksum: true,
	})
	if err != nil {
		return LFSUploadTarget{}, err
	}
	if target.Bucket == "" || target.Key == "" {
		return LFSUploadTarget{}, fmt.Errorf("canonical LFS upload location is not an s3 url")
	}
	return LFSUploadTarget{Bucket: target.Bucket, Key: target.Key, ObjectID: string(object.Id)}, nil
}
