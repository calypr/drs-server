package storage

import (
	"context"
	"errors"
	"fmt"
	"path"
	"strings"
	"sync"

	"github.com/calypr/syfon/internal/buckets"
	"github.com/calypr/syfon/internal/storage"
	"github.com/calypr/syfon/internal/storage/address"
)

const maxProbeWorkers = 8

func (s *Inspector) ProbeObject(ctx context.Context, request InspectRequest) (*ObjectMetadata, error) {
	ctx = withRequestCache(ctx)
	if strings.TrimSpace(request.ObjectURL) != "" {
		return s.inspectRaw(ctx, request)
	}
	return s.inspectScoped(ctx, request)
}

func (s *Inspector) ProbeObjects(ctx context.Context, requests []InspectRequest) []ProbeResult {
	ctx = withRequestCache(ctx)
	if len(requests) == 0 {
		return []ProbeResult{}
	}
	results := make([]ProbeResult, len(requests))
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

func (s *Inspector) probeOne(ctx context.Context, request InspectRequest) ProbeResult {
	key := probeCacheKey(request)
	if cache := cacheFromContext(ctx); cache != nil {
		if result, ok := cache.probe(key); ok {
			result.ID = strings.TrimSpace(request.ID)
			if result.ObjectURL == "" {
				result.ObjectURL = strings.TrimSpace(request.ObjectURL)
			}
			result.ValidationStatus, result.SizeMatch, result.SHA256Match, result.ValidationMismatches = validateProbe(request, &ObjectMetadata{
				SizeBytes:  valueInt64(result.SizeBytes),
				MetaSHA256: result.MetaSHA256,
			})
			return result
		}
	}
	result := ProbeResult{ID: strings.TrimSpace(request.ID), ObjectURL: strings.TrimSpace(request.ObjectURL), Status: ProbeError, ValidationStatus: validationStatusForError(request)}
	metadata, err := s.ProbeObject(ctx, request)
	if err != nil {
		result.Status, result.ErrorKind = classifyError(err)
		result.Error = strings.TrimSpace(err.Error())
		if cache := cacheFromContext(ctx); cache != nil {
			cache.setProbe(key, result)
		}
		return result
	}
	result.ObjectURL = metadata.ObjectURL
	result.Provider = metadata.Provider
	result.Bucket = metadata.Bucket
	result.Key = metadata.Key
	result.Path = metadata.Path
	result.Exists = true
	result.Status = ProbePresent
	result.SizeBytes = int64Pointer(metadata.SizeBytes)
	result.MetaSHA256 = metadata.MetaSHA256
	result.ETag = metadata.ETag
	result.LastModTime = metadata.LastModTime
	result.ValidationStatus, result.SizeMatch, result.SHA256Match, result.ValidationMismatches = validateProbe(request, metadata)
	if cache := cacheFromContext(ctx); cache != nil {
		cache.setProbe(key, result)
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
	return key + "|" + strings.ToLower(strings.TrimSpace(strings.TrimPrefix(request.ExpectedSHA256, "sha256:")))
}

func valueInt64(value *int64) int64 {
	if value == nil {
		return 0
	}
	return *value
}

func (s *Inspector) inspectRaw(ctx context.Context, request InspectRequest) (*ObjectMetadata, error) {
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
	if !buckets.VisibleToCaller(visible, bucket, credential.CredentialID) {
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

func (s *Inspector) inspectScoped(ctx context.Context, request InspectRequest) (*ObjectMetadata, error) {
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
	key = normalizeScopedStorageKey(target.Prefix, key, target.prefixes...)
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
			key = trimLeadingStoragePrefix(key, scopePrefix)
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

func trimLeadingStoragePrefix(key, prefix string) string {
	key = strings.Trim(strings.TrimSpace(key), "/")
	prefix = strings.Trim(strings.TrimSpace(prefix), "/")
	if key == prefix {
		return ""
	}
	return strings.TrimPrefix(key, prefix+"/")
}

func (s *Inspector) probeStorage(ctx context.Context, bucket, key string) (*ObjectMetadata, error) {
	if s.probe == nil {
		return nil, &Error{Kind: ErrorUnsupported, Message: "storage probe is not configured"}
	}
	results := s.probe.Probe(ctx, []storage.ProbeTarget{{ID: "object", Target: storage.ObjectTarget{Bucket: bucket, Key: key}}})
	if len(results) == 0 {
		return nil, &Error{Kind: ErrorBucketUnavailable, Message: "storage probe returned no result"}
	}
	result := results[0]
	if result.Err != nil {
		return nil, mapStorageError(result.Err, "probe", bucket, key)
	}
	metadata := result.Metadata
	return &ObjectMetadata{Provider: strings.TrimSpace(metadata.Provider), Bucket: strings.TrimSpace(metadata.Bucket), Key: strings.TrimSpace(metadata.Key), Path: strings.TrimSpace(metadata.Path), SizeBytes: metadata.SizeBytes, MetaSHA256: strings.TrimSpace(metadata.MetaSHA256), ETag: strings.TrimSpace(metadata.ETag), LastModTime: metadata.LastModified}, nil
}

func classifyError(err error) (ProbeStatus, string) {
	var inspectErr *Error
	if errors.As(err, &inspectErr) {
		switch inspectErr.Kind {
		case ErrorObjectNotFound:
			return ProbeNotFound, string(inspectErr.Kind)
		case ErrorPermissionDenied, ErrorBucketUnavailable:
			return ProbeForbidden, string(inspectErr.Kind)
		case ErrorInvalidInput, ErrorScopeNotFound, ErrorCredentialMissing:
			return ProbeInvalid, string(inspectErr.Kind)
		case ErrorUnsupported:
			return ProbeUnsupported, string(inspectErr.Kind)
		}
		return ProbeError, string(inspectErr.Kind)
	}
	return ProbeError, "error"
}

func validationStatusForError(request InspectRequest) ValidationStatus {
	if request.ExpectedSizeBytes == nil && strings.TrimSpace(request.ExpectedSHA256) == "" {
		return ValidationNotRequested
	}
	return ValidationUnverifiable
}

func validateProbe(request InspectRequest, metadata *ObjectMetadata) (ValidationStatus, *bool, *bool, []string) {
	if request.ExpectedSizeBytes == nil && strings.TrimSpace(request.ExpectedSHA256) == "" {
		return ValidationNotRequested, nil, nil, nil
	}
	mismatches := make([]string, 0, 2)
	var sizeMatch *bool
	if request.ExpectedSizeBytes != nil {
		matched := metadata.SizeBytes == *request.ExpectedSizeBytes
		sizeMatch = &matched
		if !matched {
			mismatches = append(mismatches, "size_mismatch")
		}
	}
	var shaMatch *bool
	expectedSHA := strings.ToLower(strings.TrimSpace(strings.TrimPrefix(request.ExpectedSHA256, "sha256:")))
	if expectedSHA != "" {
		if strings.TrimSpace(metadata.MetaSHA256) == "" {
			return ValidationUnverifiable, sizeMatch, nil, append(mismatches, "missing_remote_sha256")
		}
		matched := strings.EqualFold(strings.TrimSpace(metadata.MetaSHA256), expectedSHA)
		shaMatch = &matched
		if !matched {
			mismatches = append(mismatches, "sha256_mismatch")
		}
	}
	if len(mismatches) > 0 {
		return ValidationMismatched, sizeMatch, shaMatch, mismatches
	}
	return ValidationMatched, sizeMatch, shaMatch, nil
}

func int64Pointer(value int64) *int64 {
	copy := value
	return &copy
}
