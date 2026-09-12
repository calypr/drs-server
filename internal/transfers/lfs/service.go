package lfs

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/calypr/syfon/apigen/drs"
	"github.com/calypr/syfon/apigen/errorapi"
	"github.com/calypr/syfon/apigen/lfsapi"
	"github.com/calypr/syfon/internal/access"
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
	Candidate lfsapi.DrsObjectCandidate
	CreatedAt time.Time
	ExpiresAt time.Time
}

type PendingStore interface {
	SavePendingMetadata(context.Context, []PendingMetadata) error
	GetPendingMetadata(context.Context, string) (*PendingMetadata, error)
	ConsumePendingMetadata(context.Context, PendingMetadata) (bool, error)
}

type UploadAccounting interface {
	RecordFileUpload(context.Context, string) error
}

type signedPartUploader func(context.Context, string, []byte) (string, error)

func uploadSignedMultipartPart(ctx context.Context, signedURL string, content []byte) (string, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodPut, signedURL, bytes.NewReader(content))
	if err != nil {
		return "", err
	}
	request.ContentLength = int64(len(content))
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		body, readErr := io.ReadAll(io.LimitReader(response.Body, 2048))
		if readErr != nil {
			return "", fmt.Errorf("read multipart part error body: %w", readErr)
		}
		return "", fmt.Errorf("multipart part put failed status=%d body=%s", response.StatusCode, strings.TrimSpace(string(body)))
	}
	etag := strings.Trim(strings.TrimSpace(response.Header.Get("ETag")), "\"")
	if etag == "" {
		return "", fmt.Errorf("multipart part upload missing etag")
	}
	return etag, nil
}

type ObjectPort interface {
	GetObject(context.Context, string, string) (*drs.DrsObject, error)
	RegisterObjects(context.Context, []drs.DrsObject) ([]drs.DrsObject, error)
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
	uploader    signedPartUploader
	now         func() time.Time
}

func NewService(transfer *transfers.Service, objectPort ObjectPort, credentials buckets.CredentialReader, pending PendingStore, accounting UploadAccounting, uploader signedPartUploader) *Service {
	if uploader == nil {
		uploader = uploadSignedMultipartPart
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
	result, err := s.transfer.Download(ctx, transfers.DownloadRequest{ObjectID: oid, Accounting: transfers.AccountingDownloadBeforeEvent})
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
	if !access.HasObjectMethodAccess(ctx, "create", []string{"/data_file"}) {
		return result, errorapi.ErrAccessDenied
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
	target, objectID, authorization, err := s.resolveUploadTarget(ctx, oid)
	if err != nil {
		return err
	}
	init, err := s.transfer.BeginMultipart(ctx, transfers.MultipartInitRequest{GUID: &objectID, Target: &target, Authorization: &authorization})
	if err != nil {
		return fmt.Errorf("failed to initialize multipart upload: %w", err)
	}
	_ = target
	parts := make([]transfers.CompletedPart, 0, 16)
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
		parts = append(parts, transfers.CompletedPart{PartNumber: partNumber, ETag: etag})
		partNumber++
		if readErr == io.ErrUnexpectedEOF {
			break
		}
	}
	if len(parts) == 0 {
		partURL, err := s.transfer.SignMultipartPart(ctx, init.UploadID, 1)
		if err != nil {
			return fmt.Errorf("failed to sign multipart part: %w", err)
		}
		etag, err := s.uploader(ctx, partURL, nil)
		if err != nil {
			return fmt.Errorf("failed uploading multipart part: %w", err)
		}
		parts = append(parts, transfers.CompletedPart{PartNumber: 1, ETag: etag})
	}
	if _, err := s.transfer.CompleteMultipart(ctx, init.UploadID, parts); err != nil {
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

func (s *Service) resolveUploadTarget(ctx context.Context, oid string) (storage.Target, string, transfers.MultipartAuthorization, error) {
	if object, err := s.objects.GetObject(ctx, oid, "update"); err == nil {
		target, targetErr := s.targetForObject(ctx, object)
		return target, object.Id, transfers.MultipartAuthorization{Resources: objects.AccessResources(object), Methods: []string{"update"}}, targetErr
	} else if !errorapi.IsNotFoundError(err) {
		return storage.Target{}, "", transfers.MultipartAuthorization{}, err
	}
	createAuthorization := transfers.MultipartAuthorization{Resources: []string{"/data_file"}, Methods: []string{"create"}}
	if s.pending != nil {
		if pending, err := s.pending.GetPendingMetadata(ctx, oid); err == nil {
			object, conversionErr := materializeCandidate(pending.Candidate, s.currentTime())
			if conversionErr != nil {
				return storage.Target{}, "", transfers.MultipartAuthorization{}, conversionErr
			}
			target, targetErr := s.targetForObject(ctx, &object)
			return target, oid, createAuthorization, targetErr
		} else if !errorapi.IsNotFoundError(err) {
			return storage.Target{}, "", transfers.MultipartAuthorization{}, err
		}
	}
	bucket, err := s.firstConfiguredBucket(ctx)
	if err != nil {
		return storage.Target{}, "", transfers.MultipartAuthorization{}, err
	}
	return storage.Target{Provider: "s3", LookupKey: bucket, PhysicalBucket: bucket, Key: oid, CanonicalURL: address.BucketToURL(bucket, oid), LookupCandidates: []string{bucket}}, oid, createAuthorization, nil
}

func (s *Service) targetForObject(ctx context.Context, object *drs.DrsObject) (storage.Target, error) {
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

func (s *Service) Stage(ctx context.Context, candidates []lfsapi.DrsObjectCandidate) error {
	now := s.currentTime()
	entries := make([]PendingMetadata, 0, len(candidates))
	for index, candidate := range candidates {
		internalObject, err := materializeCandidate(candidate, now)
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

func (s *Service) Verify(ctx context.Context, oid string, expectedSize int64) error {
	object, objectErr := s.objects.GetObject(ctx, oid, "read")
	if objectErr != nil && !errorapi.IsNotFoundError(objectErr) {
		return objectErr
	}
	if objectErr == nil && object != nil {
		if err := verifyRecordedSize(expectedSize, object.Size); err != nil {
			return err
		}
	}
	if s.pending == nil {
		if objectErr == nil {
			return nil
		}
		return fmt.Errorf("pending LFS metadata store is not configured")
	}
	pending, err := s.pending.GetPendingMetadata(ctx, oid)
	if err == nil {
		internalObject, err := materializeCandidate(pending.Candidate, s.currentTime())
		if err != nil {
			return &MetadataCandidateError{Err: err}
		}
		if err := verifyRecordedSize(expectedSize, internalObject.Size); err != nil {
			return err
		}
		registered, err := s.objects.RegisterObjects(ctx, []drs.DrsObject{internalObject})
		if err != nil {
			return err
		}
		if len(registered) != 1 {
			return fmt.Errorf("registration returned %d records, want 1", len(registered))
		}
		owned, err := s.pending.ConsumePendingMetadata(ctx, *pending)
		if err != nil {
			return err
		}
		if !owned {
			return nil
		}
		return s.recordUpload(ctx, registered[0].Id)
	}
	if objectErr == nil && errorapi.IsNotFoundError(err) {
		return nil
	}
	return err
}

func verifyRecordedSize(expected, recorded int64) error {
	if expected == recorded {
		return nil
	}
	return &MetadataCandidateError{Err: fmt.Errorf("size mismatch: expected %d, recorded %d", expected, recorded)}
}

func materializeCandidate(value lfsapi.DrsObjectCandidate, now time.Time) (drs.DrsObject, error) {
	var aliases []string
	if value.Aliases != nil {
		aliases = append(aliases, (*value.Aliases)...)
	}
	explicitID := ""
	if value.Id != nil {
		explicitID = strings.TrimSpace(*value.Id)
	}
	if explicitID == "" {
		var sourceChecksums []lfsapi.Checksum
		if value.Checksums != nil {
			sourceChecksums = *value.Checksums
		}
		for _, checksum := range sourceChecksums {
			if strings.EqualFold(strings.TrimSpace(checksum.Type), "sha256") {
				explicitID = objects.NormalizeOID(checksum.Checksum)
				break
			}
		}
	}
	if explicitID != "" {
		aliases = append([]string{"id:" + explicitID}, aliases...)
	}
	var sourceMethods []lfsapi.AccessMethod
	if value.AccessMethods != nil {
		sourceMethods = *value.AccessMethods
	}
	methods := make([]drs.AccessMethod, 0, len(sourceMethods))
	if value.AccessMethods != nil {
		for _, method := range sourceMethods {
			converted := drs.AccessMethod{AccessId: method.AccessId}
			if method.Type != nil {
				converted.Type = drs.AccessMethodType(*method.Type)
			}
			if method.AccessUrl != nil && method.AccessUrl.Url != nil {
				converted.AccessUrl = &drs.AccessURL{Url: *method.AccessUrl.Url}
			}
			methods = append(methods, converted)
		}
	}
	var accessMethods *[]drs.AccessMethod
	if value.AccessMethods != nil {
		accessMethods = &methods
	}
	var size int64
	if value.Size != nil {
		size = *value.Size
	}
	var sourceChecksums []lfsapi.Checksum
	if value.Checksums != nil {
		sourceChecksums = *value.Checksums
	}
	checksums := make([]drs.Checksum, 0, len(sourceChecksums))
	for _, checksum := range sourceChecksums {
		checksums = append(checksums, drs.Checksum{Type: checksum.Type, Checksum: checksum.Checksum})
	}
	return objects.MaterializeCandidate(drs.DrsObjectCandidate{
		AccessMethods:    accessMethods,
		Aliases:          &aliases,
		Checksums:        checksums,
		ControlledAccess: value.ControlledAccess,
		Description:      value.Description,
		Name:             value.Name,
		Size:             size,
	}, now)
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
