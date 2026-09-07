package core

import (
	"context"
	"errors"
	"fmt"
	"log"
	"path"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/smithy-go"
	syfoncommon "github.com/calypr/syfon/common"
	"github.com/calypr/syfon/internal/access"
	"github.com/calypr/syfon/internal/common"
	"github.com/calypr/syfon/internal/faults"
	"github.com/calypr/syfon/internal/models"
	"github.com/calypr/syfon/internal/requestmeta"
)

type InspectStorageRequest struct {
	ID                string
	Organization      string
	Project           string
	Key               string
	Scheme            string
	ObjectURL         string
	ExpectedSizeBytes *int64
	ExpectedSHA256    string
}

type StorageObjectMetadata struct {
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

type StorageProbeStatus string

const (
	StorageProbeStatusPresent     StorageProbeStatus = "present"
	StorageProbeStatusNotFound    StorageProbeStatus = "not_found"
	StorageProbeStatusForbidden   StorageProbeStatus = "forbidden"
	StorageProbeStatusInvalid     StorageProbeStatus = "invalid"
	StorageProbeStatusUnsupported StorageProbeStatus = "unsupported"
	StorageProbeStatusError       StorageProbeStatus = "error"
)

type StorageValidationStatus string

const (
	StorageValidationNotRequested StorageValidationStatus = "not_requested"
	StorageValidationMatched      StorageValidationStatus = "matched"
	StorageValidationMismatched   StorageValidationStatus = "mismatched"
	StorageValidationUnverifiable StorageValidationStatus = "unverifiable"
)

type StorageProbeResult struct {
	ID                   string
	ObjectURL            string
	Provider             string
	Bucket               string
	Key                  string
	Path                 string
	Exists               bool
	Status               StorageProbeStatus
	Error                string
	ErrorKind            string
	SizeBytes            *int64
	MetaSHA256           string
	ETag                 string
	LastModTime          time.Time
	ValidationStatus     StorageValidationStatus
	SizeMatch            *bool
	SHA256Match          *bool
	ValidationMismatches []string
}

type StorageInspectErrorKind string

const (
	StorageInspectInvalidInput      StorageInspectErrorKind = "invalid_input"
	StorageInspectScopeNotFound     StorageInspectErrorKind = "scope_not_found"
	StorageInspectCredentialMissing StorageInspectErrorKind = "credential_missing"
	StorageInspectPermissionDenied  StorageInspectErrorKind = "permission_denied"
	StorageInspectObjectNotFound    StorageInspectErrorKind = "object_not_found"
	StorageInspectBucketUnavailable StorageInspectErrorKind = "bucket_unavailable"
	StorageInspectListingIncomplete StorageInspectErrorKind = "listing_incomplete"
	StorageInspectUnsupported       StorageInspectErrorKind = "unsupported"
)

const maxStorageInspectWorkers = 8

const (
	defaultS3HeadMaxAttempts = 3
	envS3HeadMaxAttempts     = "SYFON_S3_HEAD_MAX_ATTEMPTS"
)

type StorageInspectError struct {
	Kind    StorageInspectErrorKind
	Message string
}

type s3HeadObjectClient interface {
	HeadObject(context.Context, *awss3.HeadObjectInput, ...func(*awss3.Options)) (*awss3.HeadObjectOutput, error)
}

var storageInspectCacheKey contextKey = "storageInspectCache"

type storageInspectCredentialCacheEntry struct {
	cred *models.S3Credential
	err  error
}

type storageInspectRequestCache struct {
	mu sync.Mutex

	credentials   map[string]storageInspectCredentialCacheEntry
	s3Clients     map[string]*awss3.Client
	visible       map[string]VisibleBucket
	visibleErr    error
	visibleLoaded bool
}

func (e *StorageInspectError) Error() string {
	if e == nil {
		return "storage inspect failed"
	}
	if strings.TrimSpace(e.Message) != "" {
		return e.Message
	}
	return fmt.Sprintf("storage inspect failed: %s", e.Kind)
}

func WithStorageInspectCache(ctx context.Context) context.Context {
	if storageInspectCacheFromContext(ctx) != nil {
		return ctx
	}
	return context.WithValue(ctx, storageInspectCacheKey, &storageInspectRequestCache{
		credentials: map[string]storageInspectCredentialCacheEntry{},
		s3Clients:   map[string]*awss3.Client{},
	})
}

func storageInspectCacheFromContext(ctx context.Context) *storageInspectRequestCache {
	cache, _ := ctx.Value(storageInspectCacheKey).(*storageInspectRequestCache)
	return cache
}

func (m *ObjectManager) SetS3ObjectInspector(fn func(context.Context, models.S3Credential, string, string) (*StorageObjectMetadata, error)) {
	if fn == nil {
		m.inspectS3Object = defaultS3ObjectInspector
		return
	}
	m.inspectS3Object = fn
}

func (m *ObjectManager) InspectStorageObject(ctx context.Context, req InspectStorageRequest) (*StorageObjectMetadata, error) {
	ctx = withS3ProbeLimiter(ctx, m.s3ProbeLimiter)
	if strings.TrimSpace(req.ObjectURL) != "" {
		return m.inspectRawStorageObject(ctx, req)
	}
	return m.inspectScopedStorageObject(ctx, req)
}

func (m *ObjectManager) InspectStorageObjects(ctx context.Context, items []InspectStorageRequest) []StorageProbeResult {
	ctx = withS3ProbeLimiter(ctx, m.s3ProbeLimiter)
	ctx = WithStorageInspectCache(ctx)
	if len(items) == 0 {
		return []StorageProbeResult{}
	}
	results := make([]StorageProbeResult, len(items))
	workers := len(items)
	if workers > maxStorageInspectWorkers {
		workers = maxStorageInspectWorkers
	}
	type probeWork struct {
		index int
		req   InspectStorageRequest
	}
	workCh := make(chan probeWork)
	cache := newStorageProbeResultCache()
	var wg sync.WaitGroup
	for worker := 0; worker < workers; worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for work := range workCh {
				results[work.index] = cache.probe(ctx, m, work.req)
			}
		}()
	}
	for i, item := range items {
		workCh <- probeWork{index: i, req: item}
	}
	close(workCh)
	wg.Wait()
	return results
}

type storageProbeResultCache struct {
	mu      sync.Mutex
	results map[string]StorageProbeResult
}

func newStorageProbeResultCache() *storageProbeResultCache {
	return &storageProbeResultCache{results: map[string]StorageProbeResult{}}
}

func (c *storageProbeResultCache) probe(ctx context.Context, m *ObjectManager, req InspectStorageRequest) StorageProbeResult {
	key := storageProbeCacheKey(req)
	c.mu.Lock()
	if result, ok := c.results[key]; ok {
		c.mu.Unlock()
		return cloneStorageProbeResultForRequest(result, req)
	}
	c.mu.Unlock()

	result := m.inspectStorageProbe(ctx, req)

	c.mu.Lock()
	c.results[key] = result
	c.mu.Unlock()
	return result
}

func storageProbeCacheKey(req InspectStorageRequest) string {
	return strings.TrimSpace(req.ObjectURL) + "|" +
		strings.TrimSpace(req.Organization) + "|" +
		strings.TrimSpace(req.Project) + "|" +
		strings.TrimSpace(req.Key) + "|" +
		strings.TrimSpace(req.Scheme) + "|" +
		normalizeExpectedSize(req.ExpectedSizeBytes) + "|" +
		strings.ToLower(strings.TrimSpace(strings.TrimPrefix(req.ExpectedSHA256, "sha256:")))
}

func normalizeExpectedSize(v *int64) string {
	if v == nil {
		return ""
	}
	return fmt.Sprintf("%d", *v)
}

func cloneStorageProbeResultForRequest(result StorageProbeResult, req InspectStorageRequest) StorageProbeResult {
	clone := result
	clone.ID = strings.TrimSpace(req.ID)
	if clone.ID == "" {
		clone.ID = strings.TrimSpace(result.ID)
	}
	if clone.ObjectURL == "" {
		clone.ObjectURL = strings.TrimSpace(req.ObjectURL)
	}
	clone.ValidationMismatches = append([]string(nil), result.ValidationMismatches...)
	return clone
}

func (m *ObjectManager) inspectStorageProbe(ctx context.Context, req InspectStorageRequest) StorageProbeResult {
	result := StorageProbeResult{
		ID:               strings.TrimSpace(req.ID),
		ObjectURL:        strings.TrimSpace(req.ObjectURL),
		Status:           StorageProbeStatusError,
		ValidationStatus: StorageValidationNotRequested,
	}
	meta, err := m.InspectStorageObject(ctx, req)
	if err != nil {
		result.Status, result.ErrorKind = classifyStorageProbeError(err)
		result.Error = strings.TrimSpace(err.Error())
		result.Exists = false
		result.ValidationStatus = storageValidationStatusForError(req)
		return result
	}
	result.ObjectURL = strings.TrimSpace(meta.ObjectURL)
	result.Provider = strings.TrimSpace(meta.Provider)
	result.Bucket = strings.TrimSpace(meta.Bucket)
	result.Key = strings.TrimSpace(meta.Key)
	result.Path = strings.TrimSpace(meta.Path)
	result.Exists = true
	result.Status = StorageProbeStatusPresent
	result.SizeBytes = ptrInt64(meta.SizeBytes)
	result.MetaSHA256 = strings.TrimSpace(meta.MetaSHA256)
	result.ETag = strings.TrimSpace(meta.ETag)
	result.LastModTime = meta.LastModTime
	result.ValidationStatus, result.SizeMatch, result.SHA256Match, result.ValidationMismatches = validateStorageProbe(req, meta)
	return result
}

func classifyStorageProbeError(err error) (StorageProbeStatus, string) {
	var inspectErr *StorageInspectError
	if errors.As(err, &inspectErr) {
		switch inspectErr.Kind {
		case StorageInspectObjectNotFound:
			return StorageProbeStatusNotFound, string(inspectErr.Kind)
		case StorageInspectPermissionDenied, StorageInspectBucketUnavailable:
			return StorageProbeStatusForbidden, string(inspectErr.Kind)
		case StorageInspectInvalidInput, StorageInspectScopeNotFound, StorageInspectCredentialMissing:
			return StorageProbeStatusInvalid, string(inspectErr.Kind)
		case StorageInspectUnsupported:
			return StorageProbeStatusUnsupported, string(inspectErr.Kind)
		default:
			return StorageProbeStatusError, string(inspectErr.Kind)
		}
	}
	return StorageProbeStatusError, "error"
}

func storageValidationStatusForError(req InspectStorageRequest) StorageValidationStatus {
	if req.ExpectedSizeBytes == nil && strings.TrimSpace(req.ExpectedSHA256) == "" {
		return StorageValidationNotRequested
	}
	return StorageValidationUnverifiable
}

func validateStorageProbe(req InspectStorageRequest, meta *StorageObjectMetadata) (StorageValidationStatus, *bool, *bool, []string) {
	if req.ExpectedSizeBytes == nil && strings.TrimSpace(req.ExpectedSHA256) == "" {
		return StorageValidationNotRequested, nil, nil, nil
	}
	mismatches := make([]string, 0, 2)
	var sizeMatch *bool
	if req.ExpectedSizeBytes != nil {
		matched := meta.SizeBytes == *req.ExpectedSizeBytes
		sizeMatch = &matched
		if !matched {
			mismatches = append(mismatches, "size_mismatch")
		}
	}
	var shaMatch *bool
	expectedSHA := strings.ToLower(strings.TrimSpace(strings.TrimPrefix(req.ExpectedSHA256, "sha256:")))
	if expectedSHA != "" {
		if strings.TrimSpace(meta.MetaSHA256) == "" {
			return StorageValidationUnverifiable, sizeMatch, nil, append(mismatches, "missing_remote_sha256")
		}
		matched := strings.EqualFold(strings.TrimSpace(meta.MetaSHA256), expectedSHA)
		shaMatch = &matched
		if !matched {
			mismatches = append(mismatches, "sha256_mismatch")
		}
	}
	if len(mismatches) > 0 {
		return StorageValidationMismatched, sizeMatch, shaMatch, mismatches
	}
	return StorageValidationMatched, sizeMatch, shaMatch, nil
}

func ptrInt64(v int64) *int64 {
	copy := v
	return &copy
}

func (m *ObjectManager) inspectScopedStorageObject(ctx context.Context, req InspectStorageRequest) (*StorageObjectMetadata, error) {
	organization := strings.TrimSpace(req.Organization)
	project := strings.TrimSpace(req.Project)
	key := strings.Trim(strings.TrimSpace(req.Key), "/")
	if organization == "" {
		return nil, &StorageInspectError{Kind: StorageInspectInvalidInput, Message: "organization is required for scoped object inspection"}
	}
	if key == "" {
		return nil, &StorageInspectError{Kind: StorageInspectInvalidInput, Message: "key is required for scoped object inspection"}
	}
	scheme := strings.ToLower(strings.TrimSpace(req.Scheme))
	if scheme == "" {
		scheme = common.S3Provider
	}
	if scheme != common.S3Provider {
		return nil, &StorageInspectError{Kind: StorageInspectUnsupported, Message: fmt.Sprintf("provider scheme %q is not supported for server-backed add-url inspection", scheme)}
	}

	resource, err := syfoncommon.ResourcePath(organization, project)
	if err != nil {
		return nil, &StorageInspectError{Kind: StorageInspectInvalidInput, Message: err.Error()}
	}
	if access.IsAuthzEnforced(ctx) && !access.HasMethodAccess(ctx, objectMethodRead, []string{resource}) {
		return nil, &access.AuthorizationError{Method: objectMethodRead, Resources: []string{resource}}
	}

	target, err := m.ResolveScopedUploadTarget(ctx, organization, project, key)
	if err != nil {
		if errors.Is(err, faults.ErrInvalidInput) && strings.Contains(err.Error(), "no bucket scope configured") {
			return nil, &StorageInspectError{Kind: StorageInspectScopeNotFound, Message: err.Error()}
		}
		return nil, &StorageInspectError{Kind: StorageInspectInvalidInput, Message: err.Error()}
	}
	cred, err := m.credentialForBucket(ctx, target.Bucket)
	if err != nil {
		return nil, err
	}
	if common.NormalizeProvider(cred.Provider, common.S3Provider) != common.S3Provider {
		return nil, &StorageInspectError{Kind: StorageInspectUnsupported, Message: fmt.Sprintf("provider %q is not supported for server-backed add-url inspection", cred.Provider)}
	}
	meta, err := m.inspectS3Object(ctx, *cred, target.Bucket, target.Key)
	if err != nil {
		return nil, err
	}
	meta.ObjectURL = target.URL
	meta.Provider = common.S3Provider
	meta.Bucket = target.Bucket
	meta.Key = target.Key
	if strings.TrimSpace(meta.Path) == "" {
		meta.Path = path.Base(target.Key)
	}
	return meta, nil
}

func (m *ObjectManager) inspectRawStorageObject(ctx context.Context, req InspectStorageRequest) (*StorageObjectMetadata, error) {
	rawURL := strings.TrimSpace(req.ObjectURL)
	bucket, key, ok := common.ParseS3URL(rawURL)
	if !ok {
		return nil, &StorageInspectError{Kind: StorageInspectInvalidInput, Message: "object_url must be a valid s3://bucket/key URL"}
	}
	cred, err := m.credentialForBucket(ctx, bucket)
	if err != nil {
		return nil, err
	}
	visible, err := m.ListVisibleBuckets(ctx)
	if err != nil {
		return nil, err
	}
	if !bucketVisibleToCaller(visible, bucket, m.bucketCatalog.credentialIDForCredential(*cred)) {
		return nil, &StorageInspectError{Kind: StorageInspectPermissionDenied, Message: fmt.Sprintf("bucket %q is not visible to the caller", bucket)}
	}
	if common.NormalizeProvider(cred.Provider, common.S3Provider) != common.S3Provider {
		return nil, &StorageInspectError{Kind: StorageInspectUnsupported, Message: fmt.Sprintf("provider %q is not supported for server-backed add-url inspection", cred.Provider)}
	}
	meta, err := m.inspectS3Object(ctx, *cred, bucket, key)
	if err != nil {
		return nil, err
	}
	meta.ObjectURL = common.BucketToURL(bucket, key)
	meta.Provider = common.S3Provider
	meta.Bucket = bucket
	meta.Key = key
	if strings.TrimSpace(meta.Path) == "" {
		meta.Path = path.Base(key)
	}
	return meta, nil
}

func (m *ObjectManager) credentialForBucket(ctx context.Context, bucket string) (*models.S3Credential, error) {
	bucket = strings.TrimSpace(bucket)
	if bucket == "" {
		return nil, &StorageInspectError{Kind: StorageInspectInvalidInput, Message: "bucket is required"}
	}
	if cache := storageInspectCacheFromContext(ctx); cache != nil {
		if cred, err, ok := cache.getCredential(bucket); ok {
			return cred, err
		}
	}
	if cred, err := m.bucketCatalog.getS3Credential(ctx, bucket); err == nil && cred != nil {
		if cache := storageInspectCacheFromContext(ctx); cache != nil {
			cache.setCredential(bucket, cred, nil)
		}
		return cred, nil
	}
	creds, err := m.bucketCatalog.listS3Credentials(ctx)
	if err != nil {
		if cache := storageInspectCacheFromContext(ctx); cache != nil {
			cache.setCredential(bucket, nil, err)
		}
		return nil, err
	}
	for _, cred := range creds {
		if strings.EqualFold(strings.TrimSpace(cred.Bucket), bucket) || strings.EqualFold(strings.TrimSpace(cred.CredentialID), bucket) {
			copy := cred
			if cache := storageInspectCacheFromContext(ctx); cache != nil {
				cache.setCredential(bucket, &copy, nil)
			}
			return &copy, nil
		}
	}
	err = &StorageInspectError{Kind: StorageInspectCredentialMissing, Message: fmt.Sprintf("no stored bucket credential found for bucket %q", bucket)}
	if cache := storageInspectCacheFromContext(ctx); cache != nil {
		cache.setCredential(bucket, nil, err)
	}
	return nil, err
}

func bucketVisibleToCaller(visible map[string]VisibleBucket, bucket string, credentialID string) bool {
	for key, entry := range visible {
		if strings.EqualFold(strings.TrimSpace(entry.Credential.Bucket), bucket) || strings.EqualFold(strings.TrimSpace(key), credentialID) || strings.EqualFold(strings.TrimSpace(entry.Credential.CredentialID), credentialID) {
			return true
		}
	}
	return false
}

func (m *ObjectManager) listVisibleBucketsCached(ctx context.Context) (map[string]VisibleBucket, error) {
	if cache := storageInspectCacheFromContext(ctx); cache != nil {
		if visible, err, ok := cache.getVisible(); ok {
			return visible, err
		}
	}
	visible, err := m.listVisibleBucketsUncached(ctx)
	if cache := storageInspectCacheFromContext(ctx); cache != nil {
		cache.setVisible(visible, err)
	}
	return visible, err
}

func (c *storageInspectRequestCache) getCredential(bucket string) (*models.S3Credential, error, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.credentials[strings.ToLower(strings.TrimSpace(bucket))]
	if !ok {
		return nil, nil, false
	}
	if entry.cred == nil {
		return nil, entry.err, true
	}
	copy := *entry.cred
	return &copy, entry.err, true
}

func (c *storageInspectRequestCache) setCredential(bucket string, cred *models.S3Credential, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	key := strings.ToLower(strings.TrimSpace(bucket))
	if cred == nil {
		c.credentials[key] = storageInspectCredentialCacheEntry{err: err}
		return
	}
	copy := *cred
	c.credentials[key] = storageInspectCredentialCacheEntry{cred: &copy, err: err}
}

func (c *storageInspectRequestCache) getVisible() (map[string]VisibleBucket, error, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.visibleLoaded {
		return nil, nil, false
	}
	return cloneVisibleBuckets(c.visible), c.visibleErr, true
}

func (c *storageInspectRequestCache) setVisible(visible map[string]VisibleBucket, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.visible = cloneVisibleBuckets(visible)
	c.visibleErr = err
	c.visibleLoaded = true
}

func (c *storageInspectRequestCache) getS3Client(key string) (*awss3.Client, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	client, ok := c.s3Clients[key]
	return client, ok
}

func (c *storageInspectRequestCache) setS3Client(key string, client *awss3.Client) {
	if client == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.s3Clients[key] = client
}

func cloneVisibleBuckets(in map[string]VisibleBucket) map[string]VisibleBucket {
	if in == nil {
		return nil
	}
	out := make(map[string]VisibleBucket, len(in))
	for key, bucket := range in {
		programs := append([]string(nil), bucket.Programs...)
		sort.Strings(programs)
		out[key] = VisibleBucket{
			Credential: bucket.Credential,
			Programs:   programs,
		}
	}
	return out
}

func s3ClientFromContext(ctx context.Context, cred models.S3Credential) (*awss3.Client, error) {
	cacheKey := s3ClientCacheKey(cred)
	if cache := storageInspectCacheFromContext(ctx); cache != nil {
		if client, ok := cache.getS3Client(cacheKey); ok {
			return client, nil
		}
	}
	loadOpts := []func(*awsconfig.LoadOptions) error{
		awsconfig.WithRegion(cred.Region),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(cred.AccessKey, cred.SecretKey, "")),
	}
	cfg, err := awsconfig.LoadDefaultConfig(ctx, loadOpts...)
	if err != nil {
		return nil, fmt.Errorf("load aws config: %w", err)
	}
	if endpoint := strings.TrimSpace(cred.Endpoint); endpoint != "" {
		if !strings.HasPrefix(endpoint, "http://") && !strings.HasPrefix(endpoint, "https://") {
			if strings.Contains(endpoint, "localhost") || strings.Contains(endpoint, "127.0.0.1") {
				endpoint = "http://" + endpoint
			} else {
				endpoint = "https://" + endpoint
			}
		}
		cfg.BaseEndpoint = aws.String(endpoint)
	}
	client := awss3.NewFromConfig(cfg, func(o *awss3.Options) {
		if strings.TrimSpace(cred.Endpoint) != "" {
			o.UsePathStyle = true
		}
	})
	if cache := storageInspectCacheFromContext(ctx); cache != nil {
		cache.setS3Client(cacheKey, client)
	}
	return client, nil
}

func s3ClientCacheKey(cred models.S3Credential) string {
	return strings.ToLower(strings.TrimSpace(cred.Provider)) + "|" +
		strings.TrimSpace(cred.Endpoint) + "|" +
		strings.TrimSpace(cred.Region) + "|" +
		strings.TrimSpace(cred.AccessKey) + "|" +
		strings.TrimSpace(cred.Bucket) + "|" +
		strings.TrimSpace(cred.CredentialID)
}

func defaultS3ObjectInspector(ctx context.Context, cred models.S3Credential, bucket string, key string) (*StorageObjectMetadata, error) {
	client, err := s3ClientFromContext(ctx, cred)
	if err != nil {
		return nil, err
	}
	out, err := headS3ObjectWithRetry(ctx, client, bucket, key)
	if err != nil {
		var apiErr smithy.APIError
		if errors.As(err, &apiErr) {
			switch strings.ToLower(strings.TrimSpace(apiErr.ErrorCode())) {
			case "forbidden", "accessdenied", "permissiondenied":
				return nil, &StorageInspectError{Kind: StorageInspectBucketUnavailable, Message: fmt.Sprintf("provider rejected object probe for s3://%s/%s; mapped bucket target may be missing or inaccessible", bucket, key)}
			case "notfound", "nosuchkey":
				return nil, &StorageInspectError{Kind: StorageInspectObjectNotFound, Message: fmt.Sprintf("provider could not find s3://%s/%s", bucket, key)}
			case "nosuchbucket":
				return nil, &StorageInspectError{Kind: StorageInspectBucketUnavailable, Message: fmt.Sprintf("provider could not find bucket %q", bucket)}
			}
		}
		return nil, fmt.Errorf("inspect s3 object %s/%s: %w", bucket, key, err)
	}
	var size int64
	if out.ContentLength != nil {
		size = *out.ContentLength
	}
	lastMod := time.Time{}
	if out.LastModified != nil {
		lastMod = *out.LastModified
	}
	return &StorageObjectMetadata{
		Provider:    common.S3Provider,
		Bucket:      bucket,
		Key:         key,
		ObjectURL:   common.BucketToURL(bucket, key),
		Path:        path.Base(key),
		SizeBytes:   size,
		ETag:        strings.Trim(strings.TrimSpace(aws.ToString(out.ETag)), "\""),
		LastModTime: lastMod,
	}, nil
}

func headS3ObjectWithRetry(ctx context.Context, client s3HeadObjectClient, bucket, key string) (*awss3.HeadObjectOutput, error) {
	policy := s3ListPageRetryPolicyFromEnv()
	maxAttempts := intEnvOrDefault(envS3HeadMaxAttempts, defaultS3HeadMaxAttempts, 1)
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		release, acquireErr := acquireS3Probe(ctx, "head", bucket, key)
		if acquireErr != nil {
			return nil, acquireErr
		}
		out, err := client.HeadObject(ctx, &awss3.HeadObjectInput{Bucket: aws.String(bucket), Key: aws.String(key)})
		release()
		if err == nil || !isRetryableS3ListPageError(err) || attempt == maxAttempts {
			return out, err
		}
		backoff := policy.backoff(attempt)
		log.Printf("INFO: syfon_s3_head_retry request_id=%s bucket=%s key=%q attempt=%d max_attempts=%d backoff_ms=%d error=%q", requestmeta.GetRequestID(ctx), bucket, key, attempt+1, maxAttempts, backoff.Milliseconds(), err.Error())
		if err := sleepS3ListPageRetry(ctx, backoff); err != nil {
			return nil, err
		}
	}
	return nil, nil
}
