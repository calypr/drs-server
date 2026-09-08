package lfs

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/calypr/syfon/apigen/errorapi"
	"github.com/calypr/syfon/internal/buckets"
	"github.com/calypr/syfon/internal/objects"
	"github.com/calypr/syfon/internal/storage"
	"github.com/calypr/syfon/internal/storage/address"
	"github.com/calypr/syfon/internal/transfers"
)

const multipartPartSize = 64 * 1024 * 1024
const PendingMetadataTTL = 20 * time.Minute

type PendingMetadata struct {
	OID       string
	Candidate objects.Candidate
	CreatedAt time.Time
	ExpiresAt time.Time
}

type PendingStore interface {
	SavePendingMetadata(context.Context, []PendingMetadata) error
	GetPendingMetadata(context.Context, string) (*PendingMetadata, error)
	PopPendingMetadata(context.Context, string) (*PendingMetadata, error)
}

type UploadAccounting interface {
	RecordFileUpload(context.Context, string) error
}

type ObjectPort interface {
	GetObject(context.Context, string, string) (*objects.Record, error)
	RequireObjectResources(context.Context, string, []string) error
	RegisterObjects(context.Context, []objects.Record) error
}

type DownloadPreparation struct{ SignedURL string }

type DownloadLookupError struct{ Err error }

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

type BatchRequest struct {
	Operation string
	Objects   []BatchObject
}

type BatchObject struct {
	OID  string
	Size int64
}

type BatchObjectResult struct {
	OID         string
	Size        int64
	Existing    bool
	DownloadURL string
	Err         error
}

type BatchResult struct{ Objects []BatchObjectResult }

type Service struct {
	transfer    *transfers.Service
	objects     ObjectPort
	credentials buckets.CredentialReader
	pending     PendingStore
	accounting  UploadAccounting
	uploader    storage.SignedPartUploader
	now         func() time.Time
}

func NewService(transfer *transfers.Service, objectPort ObjectPort, credentials buckets.CredentialReader, pending PendingStore, accounting UploadAccounting, uploader storage.SignedPartUploader) *Service {
	if uploader == nil {
		uploader = storage.UploadSignedMultipartPart
	}
	return &Service{transfer: transfer, objects: objectPort, credentials: credentials, pending: pending, accounting: accounting, uploader: uploader, now: time.Now}
}

func (s *Service) Batch(ctx context.Context, request BatchRequest) (BatchResult, error) {
	result := BatchResult{Objects: make([]BatchObjectResult, 0, len(request.Objects))}
	for _, object := range request.Objects {
		item := BatchObjectResult{OID: object.OID, Size: object.Size}
		if request.Operation == "download" {
			preparation, err := s.PrepareDownload(ctx, object.OID)
			if err != nil {
				item.Err = err
			} else {
				item.DownloadURL = preparation.SignedURL
			}
		} else {
			preparation, err := s.PrepareUpload(ctx, object.OID, object.Size)
			item.Size = preparation.Size
			item.Existing = preparation.Existing
			item.Err = err
		}
		result.Objects = append(result.Objects, item)
	}
	return result, nil
}

func (s *Service) PrepareDownload(ctx context.Context, oid string) (DownloadPreparation, error) {
	if s == nil || s.transfer == nil {
		return DownloadPreparation{}, fmt.Errorf("LFS transfer service is not configured")
	}
	result, err := s.transfer.Download(ctx, transfers.DownloadRequest{ObjectID: oid, Accounting: transfers.AccountingDownloadBeforeEvent, AccountingObjectID: oid})
	if err != nil {
		return DownloadPreparation{}, &DownloadLookupError{Err: err}
	}
	return DownloadPreparation{SignedURL: result.URL}, nil
}

func (s *Service) PrepareUpload(ctx context.Context, oid string, size int64) (UploadPreparation, error) {
	result := UploadPreparation{Size: size}
	if s == nil || s.objects == nil {
		return result, fmt.Errorf("LFS object service is not configured")
	}
	existing, err := s.objects.GetObject(ctx, oid, "read")
	if err == nil {
		return UploadPreparation{Existing: true, Size: existing.Size}, nil
	}
	if !errorapi.IsNotFoundError(err) {
		return result, err
	}
	if err := s.objects.RequireObjectResources(ctx, "create", []string{"/data_file"}); err != nil {
		return result, err
	}
	if _, err := s.firstConfiguredBucket(ctx); err != nil {
		return result, err
	}
	if size < 0 {
		result.Size = 0
	}
	return result, nil
}

func (s *Service) UploadProxy(ctx context.Context, oid string, body io.Reader) error {
	if s == nil || s.transfer == nil || s.objects == nil {
		return fmt.Errorf("LFS upload service is not configured")
	}
	target, objectID, err := s.resolveUploadTarget(ctx, oid)
	if err != nil {
		return err
	}
	init, err := s.transfer.BeginMultipart(ctx, transfers.MultipartInitRequest{GUID: &objectID, Target: &target})
	if err != nil {
		return fmt.Errorf("failed to initialize multipart upload: %w", err)
	}
	_ = target
	parts := make([]storage.CompletedPart, 0, 16)
	partNumber := int32(1)
	buffer := make([]byte, multipartPartSize)
	for {
		readCount, readErr := io.ReadFull(body, buffer)
		if readErr == io.EOF || (readErr == io.ErrUnexpectedEOF && readCount == 0) {
			break
		}
		if readErr != nil && readErr != io.ErrUnexpectedEOF {
			return fmt.Errorf("failed reading upload stream: %w", readErr)
		}
		partURL, err := s.transfer.SignMultipartPart(ctx, init.UploadID, partNumber)
		if err != nil {
			return fmt.Errorf("failed to sign multipart part: %w", err)
		}
		etag, err := s.uploader(ctx, partURL, buffer[:readCount])
		if err != nil {
			return fmt.Errorf("failed uploading multipart part: %w", err)
		}
		parts = append(parts, storage.CompletedPart{PartNumber: partNumber, ETag: etag})
		partNumber++
		if readErr == io.ErrUnexpectedEOF {
			break
		}
	}
	if err := s.transfer.CompleteMultipart(ctx, init.UploadID, parts); err != nil {
		return fmt.Errorf("failed to complete multipart upload: %w", err)
	}
	if s.accounting == nil {
		return fmt.Errorf("failed to record upload usage: file counters are not configured")
	}
	if err := s.accounting.RecordFileUpload(ctx, objectID); err != nil {
		return fmt.Errorf("failed to record upload usage: %w", err)
	}
	return nil
}

func (s *Service) resolveUploadTarget(ctx context.Context, oid string) (storage.Target, string, error) {
	if object, err := s.objects.GetObject(ctx, oid, "read"); err == nil {
		target, targetErr := s.targetForObject(ctx, object)
		return target, string(object.Id), targetErr
	} else if !errorapi.IsNotFoundError(err) {
		return storage.Target{}, "", err
	}
	if s.pending != nil {
		if pending, err := s.pending.GetPendingMetadata(ctx, oid); err == nil {
			object, conversionErr := objects.CandidateToRecord(pending.Candidate, s.currentTime())
			if conversionErr != nil {
				return storage.Target{}, "", conversionErr
			}
			target, targetErr := s.targetForObject(ctx, &object)
			return target, oid, targetErr
		} else if !errorapi.IsNotFoundError(err) {
			return storage.Target{}, "", err
		}
	}
	bucket, err := s.firstConfiguredBucket(ctx)
	if err != nil {
		return storage.Target{}, "", err
	}
	return storage.Target{Provider: "s3", LookupKey: bucket, PhysicalBucket: bucket, Key: oid, CanonicalURL: address.BucketToURL(bucket, oid), LookupCandidates: []string{bucket}}, oid, nil
}

func (s *Service) targetForObject(ctx context.Context, object *objects.Record) (storage.Target, error) {
	canonical, err := s.transfer.ResolveCanonicalStorageTarget(ctx, transfers.CanonicalStorageTargetRequest{Object: object, PreferChecksum: true})
	if err != nil {
		return storage.Target{}, err
	}
	parsed, parseErr := address.ParseLocation(canonical.URL)
	if parseErr != nil {
		return storage.Target{}, fmt.Errorf("canonical LFS upload location is not an s3 url: %w", parseErr)
	}
	return storage.Target{Provider: parsed.Provider, LookupKey: parsed.Bucket, PhysicalBucket: canonical.Bucket, Key: canonical.Key, Path: parsed.Path, CanonicalURL: canonical.URL, LookupCandidates: []string{canonical.Bucket}}, nil
}

func (s *Service) firstConfiguredBucket(ctx context.Context) (string, error) {
	if s.credentials == nil {
		return "", errorapi.ErrBucketNotConfigured
	}
	credentials, err := s.credentials.ListS3Credentials(ctx)
	if err != nil {
		return "", err
	}
	if len(credentials) == 0 || strings.TrimSpace(credentials[0].Bucket) == "" {
		return "", errorapi.ErrBucketNotConfigured
	}
	return strings.TrimSpace(credentials[0].Bucket), nil
}

func (s *Service) StagePendingMetadata(ctx context.Context, metadata PendingMetadata) error {
	if strings.TrimSpace(metadata.OID) == "" {
		if oid, ok := objects.CanonicalSHA256(candidateChecksums(metadata.Candidate)); ok {
			metadata.OID = oid
		} else {
			return fmt.Errorf("%w: pending LFS metadata requires an OID", errorapi.ErrInvalidInput)
		}
	}
	now := s.currentTime()
	if metadata.CreatedAt.IsZero() {
		metadata.CreatedAt = now
	} else {
		metadata.CreatedAt = metadata.CreatedAt.UTC()
	}
	if metadata.ExpiresAt.IsZero() {
		metadata.ExpiresAt = metadata.CreatedAt.Add(PendingMetadataTTL)
	} else {
		metadata.ExpiresAt = metadata.ExpiresAt.UTC()
	}
	if s.pending == nil {
		return fmt.Errorf("pending LFS metadata store is not configured")
	}
	return s.pending.SavePendingMetadata(ctx, []PendingMetadata{metadata})
}

func (s *Service) Stage(ctx context.Context, candidates []objects.Candidate) error {
	now := s.currentTime()
	entries := make([]PendingMetadata, 0, len(candidates))
	for index, candidate := range candidates {
		internalObject, err := objects.CandidateToRecord(candidate, now)
		if err != nil {
			return &MetadataStageError{Index: index, Err: err}
		}
		oid, ok := objects.CanonicalSHA256(internalObject.Checksums)
		if !ok {
			return &MetadataStageError{Index: index, MissingSHA: true}
		}
		entries = append(entries, PendingMetadata{OID: oid, Candidate: candidate, CreatedAt: now, ExpiresAt: now.Add(PendingMetadataTTL)})
	}
	if s.pending == nil {
		return fmt.Errorf("pending LFS metadata store is not configured")
	}
	return s.pending.SavePendingMetadata(ctx, entries)
}

func (s *Service) Verify(ctx context.Context, oid string) error {
	object, err := s.objects.GetObject(ctx, oid, "read")
	if err == nil {
		return s.recordUpload(ctx, string(object.Id))
	}
	if !errorapi.IsNotFoundError(err) {
		return err
	}
	if s.pending == nil {
		return fmt.Errorf("pending LFS metadata store is not configured")
	}
	pending, err := s.pending.PopPendingMetadata(ctx, oid)
	if err != nil {
		return err
	}
	internalObject, err := objects.CandidateToRecord(pending.Candidate, s.currentTime())
	if err != nil {
		return &MetadataCandidateError{Err: err}
	}
	if err := s.objects.RegisterObjects(ctx, []objects.Record{internalObject}); err != nil {
		return err
	}
	return s.recordUpload(ctx, string(internalObject.Id))
}

func (s *Service) recordUpload(ctx context.Context, objectID string) error {
	if s.accounting == nil {
		return fmt.Errorf("file counters are not configured")
	}
	return s.accounting.RecordFileUpload(ctx, objectID)
}

func (s *Service) currentTime() time.Time {
	if s != nil && s.now != nil {
		return s.now().UTC()
	}
	return time.Now().UTC()
}

type MetadataCandidateError struct{ Err error }

func (e *MetadataCandidateError) Error() string {
	if e == nil || e.Err == nil {
		return "invalid LFS metadata candidate"
	}
	return e.Err.Error()
}
func (e *MetadataCandidateError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

type MetadataStageError struct {
	Index      int
	Err        error
	MissingSHA bool
}

func (e *MetadataStageError) Error() string {
	if e.MissingSHA {
		return "candidate missing canonical sha256"
	}
	if e.Err == nil {
		return "candidate is invalid"
	}
	return e.Err.Error()
}
func (e *MetadataStageError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

func candidateChecksums(candidate objects.Candidate) []objects.Checksum {
	if candidate.Checksums == nil {
		return nil
	}
	return *candidate.Checksums
}
