package storage

import (
	"context"
	"errors"
	"fmt"
	"path"
	"strings"
	"sync"
	"time"

	internalapi "github.com/calypr/syfon/apigen/internalapi"
	"github.com/calypr/syfon/internal/storage"
	"github.com/calypr/syfon/internal/storage/address"
)

type InspectRequest struct {
	ID                string
	Organization      string
	Project           string
	Key               string
	Scheme            string
	ObjectURL         string
	ExpectedSizeBytes *int64
	ExpectedSHA256    string
	ExpectedName      string
}

type objectMetadata struct {
	ObjectURL   string
	Provider    string
	Bucket      string
	Key         string
	Path        string
	SizeBytes   int64
	MetaSHA256  string
	ETag        string
	LastModTime time.Time
}

type probeStatus string

const (
	probePresent     probeStatus = "present"
	probeNotFound    probeStatus = "not_found"
	probeForbidden   probeStatus = "forbidden"
	probeInvalid     probeStatus = "invalid"
	probeUnsupported probeStatus = "unsupported"
	probeError       probeStatus = "error"
)

const maxProbeWorkers = 8

func (s *Service) ProbeObject(ctx context.Context, request InspectRequest) (*internalapi.InternalInspectObjectResponse, error) {
	metadata, err := s.probeObject(ctx, request)
	if err != nil {
		return nil, err
	}
	return inspectObjectResponse(metadata), nil
}

func (s *Service) probeObject(ctx context.Context, request InspectRequest) (*objectMetadata, error) {
	ctx = withRequestCache(ctx)
	if strings.TrimSpace(request.ObjectURL) != "" {
		return s.inspectRaw(ctx, request)
	}
	return s.inspectScoped(ctx, request)
}

func (s *Service) ProbeObjects(ctx context.Context, requests []InspectRequest) []internalapi.InternalInspectObjectBulkItem {
	ctx = withRequestCache(ctx)
	if len(requests) == 0 {
		return []internalapi.InternalInspectObjectBulkItem{}
	}
	results := make([]internalapi.InternalInspectObjectBulkItem, len(requests))
	workers := len(requests)
	if workers > maxProbeWorkers {
		workers = maxProbeWorkers
	}
	workCh := make(chan int)
	var wg sync.WaitGroup
	for worker := 0; worker < workers; worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for index := range workCh {
				results[index] = s.probeOne(ctx, requests[index])
			}
		}()
	}
	for index := range requests {
		workCh <- index
	}
	close(workCh)
	wg.Wait()
	return results
}

func (s *Service) probeOne(ctx context.Context, request InspectRequest) internalapi.InternalInspectObjectBulkItem {
	key := probeCacheKey(request)
	if cache := cacheFromContext(ctx); cache != nil {
		if result, ok := cache.probe(key); ok {
			result.Id = strings.TrimSpace(request.ID)
			if result.ObjectUrl == "" {
				result.ObjectUrl = strings.TrimSpace(request.ObjectURL)
			}
			sizeBytes := int64(0)
			if result.SizeBytes != nil {
				sizeBytes = *result.SizeBytes
			}
			status, sizeMatch, nameMatch, shaMatch, mismatches := validateProbe(request, &objectMetadata{
				Key:        result.Key,
				SizeBytes:  sizeBytes,
				MetaSHA256: result.MetaSha256,
			})
			result.ValidationStatus, result.SizeMatch, result.NameMatch, result.Sha256Match, result.ValidationMismatches = string(status), sizeMatch, nameMatch, shaMatch, mismatches
			return result
		}
	}
	result := internalapi.InternalInspectObjectBulkItem{Id: strings.TrimSpace(request.ID), ObjectUrl: strings.TrimSpace(request.ObjectURL), Status: string(probeError), ValidationStatus: string(validationStatusForError(request))}
	metadata, err := s.probeObject(ctx, request)
	if err != nil {
		status, kind := classifyError(err)
		result.Status, result.ErrorKind = string(status), kind
		logStorageDiagnostic(ctx, err, "probe")
		result.Error = safeStorageErrorMessage(err, "probe")
		if cache := cacheFromContext(ctx); cache != nil {
			cache.setProbe(key, result)
		}
		return result
	}
	result.ObjectUrl = metadata.ObjectURL
	result.Provider = metadata.Provider
	result.Bucket = metadata.Bucket
	result.Key = metadata.Key
	result.Path = metadata.Path
	result.Exists = true
	result.Status = string(probePresent)
	result.SizeBytes = int64Pointer(metadata.SizeBytes)
	result.MetaSha256 = metadata.MetaSHA256
	result.Etag = metadata.ETag
	if !metadata.LastModTime.IsZero() {
		result.LastModified = metadata.LastModTime.Format(time.RFC3339)
	}
	status, sizeMatch, nameMatch, shaMatch, mismatches := validateProbe(request, metadata)
	result.ValidationStatus, result.SizeMatch, result.NameMatch, result.Sha256Match, result.ValidationMismatches = string(status), sizeMatch, nameMatch, shaMatch, mismatches
	if cache := cacheFromContext(ctx); cache != nil {
		cache.setProbe(key, result)
	}
	return result
}

func inspectObjectResponse(metadata *objectMetadata) *internalapi.InternalInspectObjectResponse {
	result := &internalapi.InternalInspectObjectResponse{
		ObjectUrl:  metadata.ObjectURL,
		Provider:   metadata.Provider,
		Bucket:     metadata.Bucket,
		Key:        metadata.Key,
		Path:       metadata.Path,
		SizeBytes:  metadata.SizeBytes,
		MetaSha256: metadata.MetaSHA256,
		Etag:       metadata.ETag,
	}
	if !metadata.LastModTime.IsZero() {
		result.LastModified = metadata.LastModTime.Format(time.RFC3339)
	}
	return result
}

func probeCacheKey(request InspectRequest) string {
	key := strings.TrimSpace(request.ObjectURL) + "|" + strings.TrimSpace(request.Organization) + "|" + strings.TrimSpace(request.Project) + "|" + strings.TrimSpace(request.Key) + "|" + strings.TrimSpace(request.Scheme)
	if request.ExpectedSizeBytes != nil {
		key += fmt.Sprintf("|%d", *request.ExpectedSizeBytes)
	} else {
		key += "|"
	}
	return key + "|" + strings.ToLower(strings.TrimSpace(strings.TrimPrefix(request.ExpectedSHA256, "sha256:"))) + "|" + strings.TrimSpace(request.ExpectedName)
}

func (s *Service) inspectRaw(ctx context.Context, request InspectRequest) (*objectMetadata, error) {
	bucket, key, ok := address.ParseS3URL(strings.TrimSpace(request.ObjectURL))
	if !ok {
		return nil, &Error{Kind: ErrorInvalidInput, Message: "object_url must be a valid s3://bucket/key URL"}
	}
	credential, err := s.credentialForBucket(ctx, bucket)
	if err != nil {
		return nil, err
	}
	visible, err := s.visibleBuckets(ctx)
	if err != nil {
		return nil, err
	}
	if !visibleBucketContains(visible, bucket, credential.CredentialID) {
		return nil, &Error{Kind: ErrorPermissionDenied, Message: fmt.Sprintf("bucket %q is not visible to the caller", bucket)}
	}
	if address.NormalizeProvider(credential.Provider, address.S3Provider) != address.S3Provider {
		return nil, &Error{Kind: ErrorUnsupported, Message: fmt.Sprintf("provider %q is not supported for server-backed add-url inspection", credential.Provider)}
	}
	metadata, err := s.probeStorage(ctx, bucket, key)
	if err != nil {
		return nil, err
	}
	metadata.ObjectURL = address.BucketToURL(bucket, key)
	metadata.Provider = address.S3Provider
	metadata.Bucket = bucket
	metadata.Key = key
	if metadata.Path == "" {
		metadata.Path = path.Base(key)
	}
	return metadata, nil
}

func (s *Service) inspectScoped(ctx context.Context, request InspectRequest) (*objectMetadata, error) {
	organization := strings.TrimSpace(request.Organization)
	project := strings.TrimSpace(request.Project)
	key := strings.Trim(strings.TrimSpace(request.Key), "/")
	if organization == "" {
		return nil, &Error{Kind: ErrorInvalidInput, Message: "organization is required for scoped object inspection"}
	}
	if key == "" {
		return nil, &Error{Kind: ErrorInvalidInput, Message: "key is required for scoped object inspection"}
	}
	scheme := strings.ToLower(strings.TrimSpace(request.Scheme))
	if scheme == "" {
		scheme = address.S3Provider
	}
	if address.ProviderFromScheme(scheme) != address.S3Provider && scheme != address.S3Provider {
		return nil, &Error{Kind: ErrorUnsupported, Message: fmt.Sprintf("provider scheme %q is not supported for server-backed add-url inspection", scheme)}
	}
	target, err := s.resolveScope(ctx, organization, project, readMethod)
	if err != nil {
		return nil, err
	}
	key = normalizeScopedStorageKey(target.Prefix, key, target.Prefixes...)
	metadata, err := s.probeStorage(ctx, target.Bucket, key)
	if err != nil {
		return nil, err
	}
	metadata.ObjectURL = address.BucketToURL(target.Bucket, key)
	metadata.Provider = address.S3Provider
	metadata.Bucket = target.Bucket
	metadata.Key = key
	if metadata.Path == "" {
		metadata.Path = path.Base(key)
	}
	return metadata, nil
}

func normalizeScopedStorageKey(prefix, key string, scopePrefixes ...string) string {
	prefix = strings.Trim(strings.TrimSpace(prefix), "/")
	key = strings.Trim(strings.TrimSpace(key), "/")
	if len(scopePrefixes) > 0 {
		for _, scopePrefix := range scopePrefixes {
			key = address.TrimLeadingStoragePrefix(key, scopePrefix)
		}
		if prefix == "" {
			return key
		}
		if key == "" {
			return prefix
		}
		return path.Join(prefix, key)
	}
	if prefix == "" || key == prefix || strings.HasPrefix(key, prefix+"/") {
		return key
	}
	return path.Join(prefix, key)
}

func (s *Service) probeStorage(ctx context.Context, bucket, key string) (*objectMetadata, error) {
	if s.probe == nil {
		return nil, &Error{Kind: ErrorUnsupported, Message: "storage probe is not configured"}
	}
	results := s.probe.Probe(ctx, []storage.ProbeTarget{{ID: "object", Target: storage.Target{
		Provider: address.S3Provider, PhysicalBucket: bucket, LookupKey: bucket, LookupCandidates: []string{bucket}, Key: key,
	}}})
	if len(results) == 0 {
		return nil, &Error{Kind: ErrorBucketUnavailable, Message: "storage probe returned no result"}
	}
	result := results[0]
	if result.Err != nil {
		return nil, mapStorageError(result.Err, "probe", bucket, key)
	}
	metadata := result.Metadata
	return &objectMetadata{Provider: strings.TrimSpace(metadata.Provider), Bucket: strings.TrimSpace(metadata.Bucket), Key: strings.TrimSpace(metadata.Key), Path: strings.TrimSpace(metadata.Path), SizeBytes: metadata.SizeBytes, MetaSHA256: strings.TrimSpace(metadata.MetaSHA256), ETag: strings.TrimSpace(metadata.ETag), LastModTime: metadata.LastModified}, nil
}

func classifyError(err error) (probeStatus, string) {
	var inspectErr *Error
	if errors.As(err, &inspectErr) {
		switch inspectErr.Kind {
		case ErrorObjectNotFound:
			return probeNotFound, string(inspectErr.Kind)
		case ErrorPermissionDenied, ErrorBucketUnavailable:
			return probeForbidden, string(inspectErr.Kind)
		case ErrorInvalidInput, ErrorScopeNotFound, ErrorCredentialMissing:
			return probeInvalid, string(inspectErr.Kind)
		case ErrorUnsupported:
			return probeUnsupported, string(inspectErr.Kind)
		}
		return probeError, string(inspectErr.Kind)
	}
	var operation *storage.OperationError
	if errors.As(err, &operation) {
		return probeError, string(operation.ErrorCode())
	}
	return probeError, "error"
}

func validationStatusForError(request InspectRequest) validationStatus {
	if request.ExpectedSizeBytes == nil && strings.TrimSpace(request.ExpectedSHA256) == "" && strings.TrimSpace(request.ExpectedName) == "" {
		return validationNotRequested
	}
	return validationUnverifiable
}

func validateProbe(request InspectRequest, metadata *objectMetadata) (validationStatus, *bool, *bool, *bool, []string) {
	return validateObject(request, *metadata)
}

func validateObject(request InspectRequest, metadata objectMetadata) (validationStatus, *bool, *bool, *bool, []string) {
	if request.ExpectedSizeBytes == nil && strings.TrimSpace(request.ExpectedSHA256) == "" && strings.TrimSpace(request.ExpectedName) == "" {
		return validationNotRequested, nil, nil, nil, nil
	}
	mismatches := make([]string, 0, 3)
	var sizeMatch *bool
	if request.ExpectedSizeBytes != nil {
		matched := metadata.SizeBytes == *request.ExpectedSizeBytes
		sizeMatch = &matched
		if !matched {
			mismatches = append(mismatches, "size_mismatch")
		}
	}
	var nameMatch *bool
	if expectedName := strings.TrimSpace(request.ExpectedName); expectedName != "" {
		matched := path.Base(metadata.Key) == expectedName
		nameMatch = &matched
		if !matched {
			mismatches = append(mismatches, "name_mismatch")
		}
	}
	var shaMatch *bool
	expectedSHA := strings.ToLower(strings.TrimSpace(strings.TrimPrefix(request.ExpectedSHA256, "sha256:")))
	if expectedSHA != "" {
		if strings.TrimSpace(metadata.MetaSHA256) == "" {
			return validationUnverifiable, sizeMatch, nameMatch, nil, append(mismatches, "missing_remote_sha256")
		}
		matched := strings.EqualFold(strings.TrimSpace(metadata.MetaSHA256), expectedSHA)
		shaMatch = &matched
		if !matched {
			mismatches = append(mismatches, "sha256_mismatch")
		}
	}
	if len(mismatches) > 0 {
		return validationMismatched, sizeMatch, nameMatch, shaMatch, mismatches
	}
	return validationMatched, sizeMatch, nameMatch, shaMatch, nil
}

func int64Pointer(value int64) *int64 {
	copy := value
	return &copy
}
