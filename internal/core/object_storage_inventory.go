package core

import (
	"context"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log"
	"math/rand"
	"net"
	"os"
	"path"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"
	syfoncommon "github.com/calypr/syfon/common"
	"github.com/calypr/syfon/internal/access"
	"github.com/calypr/syfon/internal/common"
	"github.com/calypr/syfon/internal/models"
	"github.com/calypr/syfon/internal/requestmeta"
)

type StorageBucketObject struct {
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

type StoragePrefixListOptions struct {
	IncludeHead bool
	ExactPrefix bool
	MaxKeys     int32
}

type ProjectStorageInspectMode string

const (
	ProjectStorageInspectItems   ProjectStorageInspectMode = "items"
	ProjectStorageInspectExists  ProjectStorageInspectMode = "exists"
	ProjectStorageInspectSummary ProjectStorageInspectMode = "summary"
)

const (
	storageListCoalesceThreshold     = 25
	storageListFallbackObjectLimit   = 5000
	storageListSlowExactThreshold    = time.Second
	storageListSlowPrefixThreshold   = 3 * time.Second
	storagePrefixListLoggingDisabled = "disabled"
)

const (
	defaultS3ListPageSize             = int32(1000)
	defaultS3ListPageMaxAttempts      = 12
	defaultS3ExactProbeMaxAttempts    = 3
	defaultS3ListPageInitialBackoff   = 500 * time.Millisecond
	defaultS3ListPageMaxBackoff       = 10 * time.Second
	defaultS3InventoryTerminalReplays = 3
	envS3ListPageMaxAttempts          = "SYFON_S3_LIST_PAGE_MAX_ATTEMPTS"
	envS3ExactProbeMaxAttempts        = "SYFON_S3_EXACT_PROBE_MAX_ATTEMPTS"
	envS3ListPageInitialBackoffMillis = "SYFON_S3_LIST_PAGE_INITIAL_BACKOFF_MS"
	envS3ListPageMaxBackoffMillis     = "SYFON_S3_LIST_PAGE_MAX_BACKOFF_MS"
	envS3InventoryTerminalReplays     = "SYFON_S3_INVENTORY_TERMINAL_REPLAY_ATTEMPTS"
)

type ProjectStorageSummary struct {
	Provider          string
	Bucket            string
	Prefix            string
	ObjectURL         string
	Exists            bool
	ObjectCount       int
	TotalBytes        int64
	ComputedAt        time.Time
	Mode              ProjectStorageInspectMode
	InventoryComplete bool
	InventoryWarning  string
}

type ProjectStorageInspectResult struct {
	Summary ProjectStorageSummary
	Items   []StorageBucketObject
}

type ProjectStorageInspectOptions struct {
	Mode        ProjectStorageInspectMode
	IncludeHead bool
	PathPrefix  string
}

type storageListTargetWork struct {
	bucket         string
	key            string
	cred           models.S3Credential
	baseResult     StorageListValidationResult
	requestIndexes []int
}

type storageListRunStats struct {
	mu                   sync.Mutex
	inputItemCount       int
	distinctTargetCount  int
	duplicateCount       int
	bucketCount          int
	coalescedPrefixCount int
	exactListCallCount   int
	prefixListPageCount  int
	fallbackCount        int
}

func (s *storageListRunStats) incrementExactListCalls() {
	s.mu.Lock()
	s.exactListCallCount++
	s.mu.Unlock()
}

type StorageListValidationRequest struct {
	ID                string
	ObjectURL         string
	ExpectedSizeBytes *int64
	ExpectedName      string
}

type StorageListValidationResult struct {
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
	ETag                 string
	LastModTime          time.Time
	ValidationStatus     StorageValidationStatus
	SizeMatch            *bool
	NameMatch            *bool
	ValidationMismatches []string
}

type resolvedStorageScopeTarget struct {
	provider string
	bucket   string
	prefix   string
	cred     models.S3Credential
}

type ProjectStorageDeleteResult struct {
	ObjectURL string
	Status    string
	Error     string
}

type s3ListObjectsV2Client interface {
	ListObjectsV2(context.Context, *awss3.ListObjectsV2Input, ...func(*awss3.Options)) (*awss3.ListObjectsV2Output, error)
}

type s3PrefixListStats struct {
	Pages                  int
	Retries                int
	LastKey                string
	FailedPage             int
	LastTokenID            string
	TerminalReplayAttempts int
	TerminalDisagreements  int
}

type s3ListPageRetryPolicy struct {
	MaxAttempts    int
	InitialBackoff time.Duration
	MaxBackoff     time.Duration
}

func (m *ObjectManager) SetS3PrefixLister(fn func(context.Context, models.S3Credential, string, string, bool) ([]StorageBucketObject, error)) {
	if fn == nil {
		m.listS3Prefix = defaultS3PrefixLister
		return
	}
	m.listS3Prefix = func(ctx context.Context, cred models.S3Credential, bucket string, prefix string, options StoragePrefixListOptions) ([]StorageBucketObject, error) {
		return fn(ctx, cred, bucket, prefix, options.IncludeHead)
	}
}

func (m *ObjectManager) SetS3PrefixListerWithOptions(fn func(context.Context, models.S3Credential, string, string, StoragePrefixListOptions) ([]StorageBucketObject, error)) {
	if fn == nil {
		m.listS3Prefix = defaultS3PrefixLister
		return
	}
	m.listS3Prefix = fn
}

func (m *ObjectManager) ListProjectStorageObjects(ctx context.Context, organization, project string, includeHead bool) ([]StorageBucketObject, error) {
	result, err := m.InspectProjectStorage(ctx, organization, project, ProjectStorageInspectOptions{Mode: ProjectStorageInspectItems, IncludeHead: includeHead})
	if err != nil {
		return nil, err
	}
	return result.Items, nil
}

func (m *ObjectManager) InspectProjectStorage(ctx context.Context, organization, project string, inspectOptions ProjectStorageInspectOptions) (*ProjectStorageInspectResult, error) {
	started := time.Now()
	ctx = withS3ProbeLimiter(ctx, m.s3ProbeLimiter)
	ctx = WithStorageInspectCache(ctx)
	target, err := m.resolveProjectStorageScopeTarget(ctx, organization, project)
	if err != nil {
		log.Printf("INFO: syfon_project_storage_inspect_done organization=%s project=%s mode=%s path_prefix=%q duration_ms=%d error=%q", organization, project, inspectOptions.Mode, inspectOptions.PathPrefix, time.Since(started).Milliseconds(), err.Error())
		return nil, err
	}
	target = target.withPathPrefix(inspectOptions.PathPrefix)
	normalizedMode := normalizeProjectStorageInspectMode(inspectOptions.Mode)
	options := StoragePrefixListOptions{IncludeHead: inspectOptions.IncludeHead}
	if normalizedMode == ProjectStorageInspectExists {
		options.MaxKeys = 1
	}
	items, listErr := m.listS3Prefix(ctx, target.cred, target.bucket, target.prefix, options)
	inventoryComplete := listErr == nil
	inventoryWarning := ""
	if listErr != nil {
		var inspectErr *StorageInspectError
		if len(items) == 0 || !errors.As(listErr, &inspectErr) || inspectErr.Kind != StorageInspectListingIncomplete {
			log.Printf("INFO: syfon_project_storage_inspect_done organization=%s project=%s mode=%s path_prefix=%q bucket=%s prefix=%q max_keys=%d include_head=%t duration_ms=%d error=%q", organization, project, normalizedMode, inspectOptions.PathPrefix, target.bucket, target.prefix, options.MaxKeys, options.IncludeHead, time.Since(started).Milliseconds(), listErr.Error())
			return nil, listErr
		}
		inventoryWarning = strings.TrimSpace(listErr.Error())
	}
	out := normalizeStorageBucketObjects(items, target)
	summary := summarizeProjectStorageObjects(out, target, normalizedMode)
	summary.InventoryComplete = inventoryComplete
	summary.InventoryWarning = inventoryWarning
	if normalizedMode != ProjectStorageInspectItems {
		out = []StorageBucketObject{}
	}
	log.Printf("INFO: syfon_project_storage_inspect_done organization=%s project=%s mode=%s path_prefix=%q bucket=%s prefix=%q max_keys=%d include_head=%t exists=%t object_count=%d returned_items=%d total_bytes=%d inventory_complete=%t inventory_warning=%q duration_ms=%d", organization, project, normalizedMode, inspectOptions.PathPrefix, target.bucket, target.prefix, options.MaxKeys, options.IncludeHead, summary.Exists, summary.ObjectCount, len(out), summary.TotalBytes, summary.InventoryComplete, summary.InventoryWarning, time.Since(started).Milliseconds())
	return &ProjectStorageInspectResult{Summary: summary, Items: out}, nil
}

func (m *ObjectManager) ResolveProjectStoragePathPrefix(ctx context.Context, organization, project, pathPrefix string) (string, error) {
	target, err := m.resolveProjectStorageScopeTarget(ctx, organization, project)
	if err != nil {
		return "", err
	}
	target = target.withPathPrefix(pathPrefix)
	if target == nil {
		return "", nil
	}
	return strings.Trim(strings.TrimSpace(target.prefix), "/"), nil
}

func (target *resolvedStorageScopeTarget) withPathPrefix(pathPrefix string) *resolvedStorageScopeTarget {
	trimmed := strings.Trim(strings.TrimSpace(pathPrefix), "/")
	if target == nil || trimmed == "" {
		return target
	}
	copyTarget := *target
	if copyTarget.prefix == "" {
		copyTarget.prefix = trimmed
	} else {
		copyTarget.prefix = strings.Trim(strings.TrimSpace(copyTarget.prefix), "/") + "/" + trimmed
	}
	return &copyTarget
}

func withStoragePrefixListLogging(ctx context.Context, mode string) context.Context {
	return context.WithValue(ctx, contextKey("storagePrefixListLogging"), strings.TrimSpace(mode))
}

func storagePrefixListLoggingEnabled(ctx context.Context) bool {
	mode, _ := ctx.Value(contextKey("storagePrefixListLogging")).(string)
	return strings.TrimSpace(mode) != storagePrefixListLoggingDisabled
}

func normalizeProjectStorageInspectMode(mode ProjectStorageInspectMode) ProjectStorageInspectMode {
	switch mode {
	case ProjectStorageInspectExists, ProjectStorageInspectSummary:
		return mode
	default:
		return ProjectStorageInspectItems
	}
}

func normalizeStorageBucketObjects(items []StorageBucketObject, target *resolvedStorageScopeTarget) []StorageBucketObject {
	out := make([]StorageBucketObject, 0, len(items))
	for _, item := range items {
		entry := item
		entry.Provider = common.S3Provider
		entry.Bucket = target.bucket
		entry.Key = strings.Trim(strings.TrimSpace(entry.Key), "/")
		entry.ObjectURL = common.BucketToURL(target.bucket, entry.Key)
		if strings.TrimSpace(entry.Path) == "" {
			entry.Path = path.Base(entry.Key)
		}
		out = append(out, entry)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Key < out[j].Key
	})
	return out
}

func summarizeProjectStorageObjects(items []StorageBucketObject, target *resolvedStorageScopeTarget, mode ProjectStorageInspectMode) ProjectStorageSummary {
	summary := ProjectStorageSummary{
		Provider:          target.provider,
		Bucket:            target.bucket,
		Prefix:            strings.Trim(strings.TrimSpace(target.prefix), "/"),
		ObjectURL:         common.BucketToURL(target.bucket, strings.Trim(strings.TrimSpace(target.prefix), "/")),
		Exists:            len(items) > 0,
		ObjectCount:       len(items),
		ComputedAt:        time.Now().UTC(),
		Mode:              mode,
		InventoryComplete: true,
	}
	for _, item := range items {
		summary.TotalBytes += item.SizeBytes
	}
	return summary
}

func (m *ObjectManager) resolveProjectStorageScopeTarget(ctx context.Context, organization, project string) (*resolvedStorageScopeTarget, error) {
	return m.resolveProjectStorageScopeTargetForMethod(ctx, organization, project, objectMethodRead)
}

func (m *ObjectManager) resolveProjectStorageScopeTargetForMethod(ctx context.Context, organization, project, method string) (*resolvedStorageScopeTarget, error) {
	organization = strings.TrimSpace(organization)
	project = strings.TrimSpace(project)
	if organization == "" {
		return nil, &StorageInspectError{Kind: StorageInspectInvalidInput, Message: "organization is required"}
	}
	resource, err := syfoncommon.ResourcePath(organization, project)
	if err != nil {
		return nil, &StorageInspectError{Kind: StorageInspectInvalidInput, Message: err.Error()}
	}
	if access.IsAuthzEnforced(ctx) && !access.HasMethodAccess(ctx, method, []string{resource}) {
		return nil, &access.AuthorizationError{Method: method, Resources: []string{resource}}
	}

	scopes := make([]models.BucketScope, 0, 2)
	if scope, found, err := m.bucketCatalog.lookupBucketScope(ctx, organization, ""); err != nil {
		return nil, err
	} else if found {
		scopes = append(scopes, scope)
	}
	if project != "" {
		if scope, found, err := m.bucketCatalog.lookupBucketScope(ctx, organization, project); err != nil {
			return nil, err
		} else if found {
			scopes = append(scopes, scope)
		}
	}
	if len(scopes) == 0 {
		if project != "" {
			return nil, &StorageInspectError{Kind: StorageInspectScopeNotFound, Message: fmt.Sprintf("no bucket scope configured for organization %q project %q", organization, project)}
		}
		return nil, &StorageInspectError{Kind: StorageInspectScopeNotFound, Message: fmt.Sprintf("no bucket scope configured for organization %q", organization)}
	}

	bucket := ""
	for _, scope := range scopes {
		if strings.TrimSpace(scope.Bucket) != "" {
			bucket = strings.TrimSpace(scope.Bucket)
		}
	}
	if bucket == "" {
		return nil, &StorageInspectError{Kind: StorageInspectInvalidInput, Message: fmt.Sprintf("unable to resolve scoped storage bucket for organization %q project %q", organization, project)}
	}
	cred, err := m.credentialForBucket(ctx, bucket)
	if err != nil {
		return nil, err
	}
	if common.NormalizeProvider(cred.Provider, common.S3Provider) != common.S3Provider {
		return nil, &StorageInspectError{Kind: StorageInspectUnsupported, Message: fmt.Sprintf("provider %q is not supported for scoped bucket listing", cred.Provider)}
	}
	return &resolvedStorageScopeTarget{
		provider: common.S3Provider,
		bucket:   bucket,
		prefix:   strings.Join(normalizedScopePrefixes(scopes), "/"),
		cred:     *cred,
	}, nil
}

func (m *ObjectManager) DeleteProjectStorageObjects(ctx context.Context, organization, project string, objectURLs []string) []ProjectStorageDeleteResult {
	ctx = WithStorageInspectCache(ctx)
	normalized := uniqueStorageObjectURLs(objectURLs)
	if len(normalized) == 0 {
		return []ProjectStorageDeleteResult{}
	}
	results := make([]ProjectStorageDeleteResult, 0, len(normalized))
	target, err := m.resolveProjectStorageScopeTargetForMethod(ctx, organization, project, objectMethodDelete)
	if err != nil {
		for _, objectURL := range normalized {
			results = append(results, ProjectStorageDeleteResult{
				ObjectURL: objectURL,
				Status:    "error",
				Error:     err.Error(),
			})
		}
		return results
	}
	for _, objectURL := range normalized {
		result := ProjectStorageDeleteResult{
			ObjectURL: objectURL,
			Status:    "deleted",
		}
		storageTarget, ok, targetErr := m.storageTargetFromURL(ctx, objectURL)
		switch {
		case targetErr != nil:
			result.Status = "error"
			result.Error = targetErr.Error()
		case !ok:
			result.Status = "invalid"
			result.Error = "object_url must resolve to a deletable storage target"
		case !projectStorageTargetAllowed(storageTarget, target):
			result.Status = "forbidden"
			result.Error = fmt.Sprintf("object_url %q is outside configured project bucket scope", objectURL)
		default:
			err := m.deleteStorageTarget(ctx, storageTarget)
			if err == nil {
				break
			}
			result.Status = "error"
			result.Error = err.Error()
		}
		results = append(results, result)
	}
	return results
}

func projectStorageTargetAllowed(candidate storageTarget, target *resolvedStorageScopeTarget) bool {
	if target == nil {
		return false
	}
	if common.NormalizeProvider(candidate.provider, common.S3Provider) != common.S3Provider {
		return false
	}
	if strings.TrimSpace(candidate.bucket) != strings.TrimSpace(target.bucket) {
		return false
	}
	return projectStorageKeyWithinPrefix(candidate.key, target.prefix)
}

func projectStorageKeyWithinPrefix(key, prefix string) bool {
	cleanKey := strings.Trim(strings.TrimSpace(key), "/")
	cleanPrefix := strings.Trim(strings.TrimSpace(prefix), "/")
	if cleanPrefix == "" {
		return cleanKey != ""
	}
	return cleanKey == cleanPrefix || strings.HasPrefix(cleanKey, cleanPrefix+"/")
}

func uniqueStorageObjectURLs(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		if _, ok := seen[trimmed]; ok {
			continue
		}
		seen[trimmed] = struct{}{}
		out = append(out, trimmed)
	}
	return out
}

func (m *ObjectManager) ListValidateStorageObjects(ctx context.Context, items []StorageListValidationRequest) []StorageListValidationResult {
	started := time.Now()
	ctx = withS3ProbeLimiter(ctx, m.s3ProbeLimiter)
	ctx = WithStorageInspectCache(ctx)
	ctx = withStoragePrefixListLogging(ctx, storagePrefixListLoggingDisabled)
	if len(items) == 0 {
		log.Printf("INFO: syfon_bulk_list_validate_done items=0 duration_ms=0")
		return []StorageListValidationResult{}
	}
	visible, visibleErr := m.ListVisibleBuckets(ctx)
	results := make([]StorageListValidationResult, len(items))
	workByTarget := make(map[string]*storageListTargetWork)
	buckets := map[string]struct{}{}
	stats := storageListRunStats{inputItemCount: len(items)}
	for index, item := range items {
		result, targetWork, ok := m.resolveListValidationTarget(ctx, item, index, visible, visibleErr)
		if !ok {
			results[index] = result
			continue
		}
		targetKey := storageListTargetKey(targetWork.bucket, targetWork.key)
		if existing, found := workByTarget[targetKey]; found {
			existing.requestIndexes = append(existing.requestIndexes, index)
			stats.duplicateCount++
			continue
		}
		workByTarget[targetKey] = targetWork
		buckets[targetWork.bucket] = struct{}{}
	}
	stats.distinctTargetCount = len(workByTarget)
	stats.bucketCount = len(buckets)

	prefixGroups := groupStorageListTargetsByDirectory(workByTarget)
	coalescedPrefixGroups := make([][]*storageListTargetWork, 0, len(prefixGroups))
	for _, group := range prefixGroups {
		if len(group) < storageListCoalesceThreshold {
			continue
		}
		stats.coalescedPrefixCount++
		coalescedPrefixGroups = append(coalescedPrefixGroups, group)
	}
	log.Printf(
		"INFO: syfon_bulk_list_validate_start items=%d distinct_targets=%d duplicates=%d buckets=%d coalesced_prefixes=%d",
		stats.inputItemCount,
		stats.distinctTargetCount,
		stats.duplicateCount,
		stats.bucketCount,
		stats.coalescedPrefixCount,
	)

	outcomes := make(map[string]StorageListValidationResult, len(workByTarget))
	matchedItems := make(map[string]StorageBucketObject, len(workByTarget))
	unresolved := cloneStorageListWorkMap(workByTarget)

	for _, group := range coalescedPrefixGroups {
		m.runCoalescedStorageListGroup(ctx, group, outcomes, matchedItems, unresolved, &stats)
	}
	m.runExactStorageListTargets(ctx, unresolved, outcomes, matchedItems, &stats)

	for targetKey, work := range workByTarget {
		outcome, ok := outcomes[targetKey]
		if !ok {
			outcome = work.baseResult
			outcome.Status = StorageProbeStatusNotFound
			outcome.ErrorKind = string(StorageInspectObjectNotFound)
			outcome.Error = fmt.Sprintf("object %q was not found", work.baseResult.ObjectURL)
		}
		for _, index := range work.requestIndexes {
			req := items[index]
			if item, found := matchedItems[targetKey]; found {
				result := storageListValidationPresentResult(req, work.baseResult, item)
				result.ID = strings.TrimSpace(req.ID)
				result.ObjectURL = strings.TrimSpace(req.ObjectURL)
				results[index] = result
				continue
			}
			result := outcome
			result.ID = strings.TrimSpace(req.ID)
			result.ObjectURL = strings.TrimSpace(req.ObjectURL)
			result.ValidationStatus = storageListValidationStatusForError(req)
			results[index] = result
		}
	}

	statusCounts := map[StorageProbeStatus]int{}
	validationCounts := map[StorageValidationStatus]int{}
	for _, result := range results {
		statusCounts[result.Status]++
		validationCounts[result.ValidationStatus]++
	}
	log.Printf(
		"INFO: syfon_bulk_list_validate_done items=%d distinct_targets=%d duplicates=%d buckets=%d coalesced_prefixes=%d exact_list_calls=%d prefix_list_pages=%d fallbacks=%d visible_error=%t present=%d not_found=%d invalid=%d error=%d matched=%d mismatched=%d unverifiable=%d duration_ms=%d",
		stats.inputItemCount,
		stats.distinctTargetCount,
		stats.duplicateCount,
		stats.bucketCount,
		stats.coalescedPrefixCount,
		stats.exactListCallCount,
		stats.prefixListPageCount,
		stats.fallbackCount,
		visibleErr != nil,
		statusCounts[StorageProbeStatusPresent],
		statusCounts[StorageProbeStatusNotFound],
		statusCounts[StorageProbeStatusInvalid],
		statusCounts[StorageProbeStatusError],
		validationCounts[StorageValidationMatched],
		validationCounts[StorageValidationMismatched],
		validationCounts[StorageValidationUnverifiable],
		time.Since(started).Milliseconds(),
	)
	return results
}

func (m *ObjectManager) resolveListValidationTarget(ctx context.Context, req StorageListValidationRequest, index int, visible map[string]VisibleBucket, visibleErr error) (StorageListValidationResult, *storageListTargetWork, bool) {
	result := StorageListValidationResult{
		ID:               strings.TrimSpace(req.ID),
		ObjectURL:        strings.TrimSpace(req.ObjectURL),
		Status:           StorageProbeStatusError,
		ValidationStatus: StorageValidationNotRequested,
	}
	target, ok, err := m.storageTargetFromURL(ctx, req.ObjectURL)
	if err != nil {
		result.Status, result.ErrorKind = classifyStorageProbeError(err)
		result.Error = strings.TrimSpace(err.Error())
		result.ValidationStatus = storageListValidationStatusForError(req)
		return result, nil, false
	}
	if !ok || target.provider != common.S3Provider {
		result.Status = StorageProbeStatusInvalid
		result.ErrorKind = string(StorageInspectInvalidInput)
		result.Error = "object_url must be a valid s3://bucket/key URL"
		result.ValidationStatus = storageListValidationStatusForError(req)
		return result, nil, false
	}
	result.Provider = common.S3Provider
	result.Bucket = target.bucket
	result.Key = target.key
	result.Path = path.Base(target.key)

	cred, err := m.credentialForBucket(ctx, target.bucket)
	if err != nil {
		log.Printf("INFO: syfon_bulk_list_validate_credential id=%s bucket=%s key=%q error=%q", result.ID, target.bucket, target.key, err.Error())
		result.Status, result.ErrorKind = classifyStorageProbeError(err)
		result.Error = strings.TrimSpace(err.Error())
		result.ValidationStatus = storageListValidationStatusForError(req)
		return result, nil, false
	}
	if visibleErr != nil {
		log.Printf("INFO: syfon_bulk_list_validate_visible id=%s bucket=%s key=%q error=%q", result.ID, target.bucket, target.key, visibleErr.Error())
		result.Status, result.ErrorKind = classifyStorageProbeError(visibleErr)
		result.Error = strings.TrimSpace(visibleErr.Error())
		result.ValidationStatus = storageListValidationStatusForError(req)
		return result, nil, false
	}
	if !bucketVisibleToCaller(visible, target.bucket, m.bucketCatalog.credentialIDForCredential(*cred)) {
		err := &StorageInspectError{Kind: StorageInspectPermissionDenied, Message: fmt.Sprintf("bucket %q is not visible to the caller", target.bucket)}
		log.Printf("INFO: syfon_bulk_list_validate_visible id=%s bucket=%s key=%q visible_count=%d error=%q", result.ID, target.bucket, target.key, len(visible), err.Error())
		result.Status, result.ErrorKind = classifyStorageProbeError(err)
		result.Error = err.Error()
		result.ValidationStatus = storageListValidationStatusForError(req)
		return result, nil, false
	}
	return result, &storageListTargetWork{
		bucket:         target.bucket,
		key:            target.key,
		cred:           *cred,
		baseResult:     result,
		requestIndexes: []int{index},
	}, true
}

func groupStorageListTargetsByDirectory(workByTarget map[string]*storageListTargetWork) map[string][]*storageListTargetWork {
	groups := make(map[string][]*storageListTargetWork)
	for _, work := range workByTarget {
		dirPrefix := storageListDirectoryPrefix(work.key)
		groupKey := strings.TrimSpace(work.bucket) + "\x00" + dirPrefix
		groups[groupKey] = append(groups[groupKey], work)
	}
	return groups
}

func cloneStorageListWorkMap(input map[string]*storageListTargetWork) map[string]*storageListTargetWork {
	out := make(map[string]*storageListTargetWork, len(input))
	for key, work := range input {
		out[key] = work
	}
	return out
}

func storageListTargetKey(bucket string, key string) string {
	return strings.TrimSpace(bucket) + "\x00" + strings.Trim(strings.TrimSpace(key), "/")
}

func storageListDirectoryPrefix(key string) string {
	trimmed := strings.Trim(strings.TrimSpace(key), "/")
	if trimmed == "" {
		return ""
	}
	dir := strings.Trim(strings.TrimSpace(path.Dir(trimmed)), "/")
	if dir == "." || dir == "" {
		return ""
	}
	return dir + "/"
}

func (m *ObjectManager) runCoalescedStorageListGroup(ctx context.Context, group []*storageListTargetWork, outcomes map[string]StorageListValidationResult, matchedItems map[string]StorageBucketObject, unresolved map[string]*storageListTargetWork, stats *storageListRunStats) {
	if len(group) == 0 {
		return
	}
	requestedKeys := make(map[string]*storageListTargetWork, len(group))
	for _, work := range group {
		requestedKeys[storageListTargetKey(work.bucket, work.key)] = work
	}
	dirPrefix := storageListDirectoryPrefix(group[0].key)
	started := time.Now()
	listed, err := m.listS3Prefix(ctx, group[0].cred, group[0].bucket, dirPrefix, StoragePrefixListOptions{ExactPrefix: true})
	if err != nil {
		log.Printf("INFO: syfon_bulk_list_validate_prefix bucket=%s prefix=%q targets=%d duration_ms=%d error=%q", group[0].bucket, dirPrefix, len(group), time.Since(started).Milliseconds(), err.Error())
		stats.fallbackCount += len(group)
		return
	}
	pageEstimate := 1
	if len(listed) > 1000 {
		pageEstimate = (len(listed) + 999) / 1000
	}
	stats.prefixListPageCount += pageEstimate
	found := 0
	for _, item := range listed {
		key := storageListTargetKey(group[0].bucket, item.Key)
		work, ok := requestedKeys[key]
		if !ok {
			continue
		}
		normalized := normalizeStorageBucketObjects([]StorageBucketObject{item}, &resolvedStorageScopeTarget{provider: common.S3Provider, bucket: work.bucket, prefix: work.key})
		if len(normalized) == 0 {
			continue
		}
		matchedItems[key] = normalized[0]
		result := work.baseResult
		result.Exists = true
		result.Status = StorageProbeStatusPresent
		result.Error = ""
		result.ErrorKind = ""
		outcomes[key] = result
		delete(unresolved, key)
		found++
	}
	if len(listed) > storageListFallbackObjectLimit && found < len(group) {
		stats.fallbackCount += len(group) - found
	}
	if duration := time.Since(started); duration > storageListSlowPrefixThreshold {
		log.Printf("INFO: syfon_bulk_list_validate_prefix bucket=%s prefix=%q targets=%d listed_objects=%d matched_targets=%d duration_ms=%d", group[0].bucket, dirPrefix, len(group), len(listed), found, duration.Milliseconds())
	}
}

func (m *ObjectManager) runExactStorageListTargets(ctx context.Context, unresolved map[string]*storageListTargetWork, outcomes map[string]StorageListValidationResult, matchedItems map[string]StorageBucketObject, stats *storageListRunStats) {
	workers := len(unresolved)
	if workers > maxStorageInspectWorkers {
		workers = maxStorageInspectWorkers
	}
	if workers == 0 {
		return
	}
	keys := make([]string, 0, len(unresolved))
	for key := range unresolved {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	workCh := make(chan string)
	var wg sync.WaitGroup
	var mu sync.Mutex
	for worker := 0; worker < workers; worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for targetKey := range workCh {
				work := unresolved[targetKey]
				outcome, item, matched := m.runExactStorageListTarget(ctx, work, stats)
				mu.Lock()
				outcomes[targetKey] = outcome
				if matched {
					matchedItems[targetKey] = item
				}
				mu.Unlock()
			}
		}()
	}
	for _, key := range keys {
		workCh <- key
	}
	close(workCh)
	wg.Wait()
}

func (m *ObjectManager) runExactStorageListTarget(ctx context.Context, work *storageListTargetWork, stats *storageListRunStats) (StorageListValidationResult, StorageBucketObject, bool) {
	started := time.Now()
	matches, err := m.listS3Prefix(ctx, work.cred, work.bucket, work.key, StoragePrefixListOptions{ExactPrefix: true, MaxKeys: 1})
	if err != nil {
		log.Printf("INFO: syfon_bulk_list_validate_exact request_id=%s bucket=%s key=%q duration_ms=%d error=%q", requestmeta.GetRequestID(ctx), work.bucket, work.key, time.Since(started).Milliseconds(), err.Error())
		result := work.baseResult
		result.Status, result.ErrorKind = classifyStorageProbeError(err)
		result.Error = strings.TrimSpace(err.Error())
		stats.incrementExactListCalls()
		return result, StorageBucketObject{}, false
	}
	stats.incrementExactListCalls()
	for _, item := range matches {
		key := strings.Trim(strings.TrimSpace(item.Key), "/")
		if key != work.key {
			continue
		}
		normalized := normalizeStorageBucketObjects([]StorageBucketObject{item}, &resolvedStorageScopeTarget{provider: common.S3Provider, bucket: work.bucket, prefix: work.key})
		if len(normalized) == 0 {
			break
		}
		if duration := time.Since(started); duration > storageListSlowExactThreshold {
			log.Printf("INFO: syfon_bulk_list_validate_exact request_id=%s bucket=%s key=%q duration_ms=%d status=present", requestmeta.GetRequestID(ctx), work.bucket, work.key, duration.Milliseconds())
		}
		result := work.baseResult
		result.Exists = true
		result.Status = StorageProbeStatusPresent
		result.Error = ""
		result.ErrorKind = ""
		return result, normalized[0], true
	}
	if duration := time.Since(started); duration > storageListSlowExactThreshold {
		log.Printf("INFO: syfon_bulk_list_validate_exact request_id=%s bucket=%s key=%q duration_ms=%d status=not_found", requestmeta.GetRequestID(ctx), work.bucket, work.key, duration.Milliseconds())
	}
	result := work.baseResult
	result.Status = StorageProbeStatusNotFound
	result.ErrorKind = string(StorageInspectObjectNotFound)
	result.Error = fmt.Sprintf("object %q was not found", work.baseResult.ObjectURL)
	return result, StorageBucketObject{}, false
}

func storageListValidationPresentResult(req StorageListValidationRequest, result StorageListValidationResult, item StorageBucketObject) StorageListValidationResult {
	result.ObjectURL = item.ObjectURL
	result.Key = item.Key
	result.Path = item.Path
	result.Exists = true
	result.Status = StorageProbeStatusPresent
	result.Error = ""
	result.ErrorKind = ""
	result.SizeBytes = ptrInt64(item.SizeBytes)
	result.ETag = strings.TrimSpace(item.ETag)
	result.LastModTime = item.LastModTime
	result.ValidationStatus, result.SizeMatch, result.NameMatch, result.ValidationMismatches = validateStorageListResult(req, item)
	return result
}

func storageListValidationStatusForError(req StorageListValidationRequest) StorageValidationStatus {
	if req.ExpectedSizeBytes == nil && strings.TrimSpace(req.ExpectedName) == "" {
		return StorageValidationNotRequested
	}
	return StorageValidationUnverifiable
}

func validateStorageListResult(req StorageListValidationRequest, item StorageBucketObject) (StorageValidationStatus, *bool, *bool, []string) {
	if req.ExpectedSizeBytes == nil && strings.TrimSpace(req.ExpectedName) == "" {
		return StorageValidationNotRequested, nil, nil, nil
	}
	mismatches := make([]string, 0, 2)
	var sizeMatch *bool
	if req.ExpectedSizeBytes != nil {
		matched := item.SizeBytes == *req.ExpectedSizeBytes
		sizeMatch = &matched
		if !matched {
			mismatches = append(mismatches, "size_mismatch")
		}
	}
	var nameMatch *bool
	if expectedName := strings.TrimSpace(req.ExpectedName); expectedName != "" {
		matched := path.Base(item.Key) == expectedName
		nameMatch = &matched
		if !matched {
			mismatches = append(mismatches, "name_mismatch")
		}
	}
	if len(mismatches) > 0 {
		return StorageValidationMismatched, sizeMatch, nameMatch, mismatches
	}
	return StorageValidationMatched, sizeMatch, nameMatch, nil
}

func defaultS3PrefixLister(ctx context.Context, cred models.S3Credential, bucket string, prefix string, options StoragePrefixListOptions) ([]StorageBucketObject, error) {
	started := time.Now()
	client, err := s3ClientFromContext(ctx, cred)
	if err != nil {
		return nil, err
	}
	input := &awss3.ListObjectsV2Input{
		Bucket:  aws.String(bucket),
		MaxKeys: aws.Int32(defaultS3ListPageSize),
	}
	if trimmedPrefix := strings.Trim(strings.TrimSpace(prefix), "/"); trimmedPrefix != "" {
		if options.ExactPrefix {
			input.Prefix = aws.String(trimmedPrefix)
		} else {
			input.Prefix = aws.String(trimmedPrefix + "/")
		}
	}
	if options.MaxKeys > 0 {
		input.MaxKeys = aws.Int32(options.MaxKeys)
	}
	requestPrefix := aws.ToString(input.Prefix)
	logEnabled := storagePrefixListLoggingEnabled(ctx)
	if logEnabled {
		log.Printf("INFO: syfon_s3_prefix_list_start request_id=%s bucket=%s requested_prefix=%q input_prefix=%q exact_prefix=%t max_keys=%d include_head=%t", requestmeta.GetRequestID(ctx), bucket, prefix, requestPrefix, options.ExactPrefix, aws.ToInt32(input.MaxKeys), options.IncludeHead)
	}
	out, stats, firstKeys, err := listS3PrefixPagesWithExactProbeRetry(ctx, client, input, bucket, prefix, requestPrefix, options, logEnabled)
	if err != nil {
		if logEnabled {
			log.Printf("INFO: syfon_s3_prefix_list_done request_id=%s bucket=%s requested_prefix=%q input_prefix=%q exact_prefix=%t max_keys=%d include_head=%t pages=%d objects=%d retries=%d terminal_replay_attempts=%d terminal_disagreements=%d failed_page=%d token=%s last_key=%q duration_ms=%d error=%q", requestmeta.GetRequestID(ctx), bucket, prefix, requestPrefix, options.ExactPrefix, aws.ToInt32(input.MaxKeys), options.IncludeHead, stats.Pages, len(out), stats.Retries, stats.TerminalReplayAttempts, stats.TerminalDisagreements, stats.FailedPage, stats.LastTokenID, stats.LastKey, time.Since(started).Milliseconds(), err.Error())
		}
		var inspectErr *StorageInspectError
		if errors.As(err, &inspectErr) {
			if inspectErr.Kind == StorageInspectListingIncomplete && len(out) > 0 {
				return out, inspectErr
			}
			return nil, inspectErr
		}
		return nil, classifyS3ListError(bucket, prefix, err)
	}
	if logEnabled {
		log.Printf("INFO: syfon_s3_prefix_list_done request_id=%s bucket=%s requested_prefix=%q input_prefix=%q exact_prefix=%t max_keys=%d include_head=%t pages=%d objects=%d retries=%d terminal_replay_attempts=%d terminal_disagreements=%d first_keys=%q duration_ms=%d", requestmeta.GetRequestID(ctx), bucket, prefix, requestPrefix, options.ExactPrefix, aws.ToInt32(input.MaxKeys), options.IncludeHead, stats.Pages, len(out), stats.Retries, stats.TerminalReplayAttempts, stats.TerminalDisagreements, strings.Join(firstKeys, ","), time.Since(started).Milliseconds())
	}
	if !options.IncludeHead || len(out) == 0 {
		return out, nil
	}

	workers := len(out)
	if workers > maxStorageInspectWorkers {
		workers = maxStorageInspectWorkers
	}
	if workers == 0 {
		return out, nil
	}
	indexCh := make(chan int)
	var wg sync.WaitGroup
	var firstErr error
	var errMu sync.Mutex
	for worker := 0; worker < workers; worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for index := range indexCh {
				meta, err := defaultS3ObjectInspector(ctx, cred, bucket, out[index].Key)
				if err != nil {
					errMu.Lock()
					if firstErr == nil {
						firstErr = err
					}
					errMu.Unlock()
					continue
				}
				out[index].MetaSHA256 = strings.TrimSpace(meta.MetaSHA256)
				if out[index].ETag == "" {
					out[index].ETag = strings.TrimSpace(meta.ETag)
				}
				if out[index].LastModTime.IsZero() {
					out[index].LastModTime = meta.LastModTime
				}
			}
		}()
	}
	for index := range out {
		indexCh <- index
	}
	close(indexCh)
	wg.Wait()
	if firstErr != nil {
		return nil, firstErr
	}
	return out, nil
}

// Full-prefix inventories use one token-only scan per request. Exact probes
// retain their cheaper bounded retry behavior because a transient omission is
// more common than a real deletion for a single known key.
func listS3PrefixPagesWithExactProbeRetry(ctx context.Context, client s3ListObjectsV2Client, input *awss3.ListObjectsV2Input, bucket, prefix, requestPrefix string, options StoragePrefixListOptions, logEnabled bool) ([]StorageBucketObject, s3PrefixListStats, []string, error) {
	if options.MaxKeys != 1 {
		return listS3PrefixPages(ctx, client, input, bucket, prefix, requestPrefix, options, logEnabled)
	}
	if !isExactProbeList(options) {
		return listS3PrefixPages(ctx, client, input, bucket, prefix, requestPrefix, options, logEnabled)
	}
	policy := s3ListPageRetryPolicyFromEnv()
	maxAttempts := intEnvOrDefault(envS3ExactProbeMaxAttempts, defaultS3ExactProbeMaxAttempts, 1)
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		out, stats, firstKeys, err := listS3PrefixPages(ctx, client, input, bucket, prefix, requestPrefix, options, logEnabled)
		if err != nil || hasExactListedKey(out, prefix) || attempt == maxAttempts {
			return out, stats, firstKeys, err
		}
		backoff := policy.backoff(attempt)
		if logEnabled {
			log.Printf("INFO: syfon_s3_exact_probe_retry request_id=%s bucket=%s key=%q attempt=%d max_attempts=%d backoff_ms=%d objects=%d", requestmeta.GetRequestID(ctx), bucket, prefix, attempt+1, maxAttempts, backoff.Milliseconds(), len(out))
		}
		if err := sleepS3ListPageRetry(ctx, backoff); err != nil {
			return nil, stats, firstKeys, err
		}
	}
	return nil, s3PrefixListStats{}, nil, nil
}

func isExactProbeList(options StoragePrefixListOptions) bool {
	return options.ExactPrefix && options.MaxKeys == 1
}

func hasExactListedKey(items []StorageBucketObject, key string) bool {
	want := strings.Trim(strings.TrimSpace(key), "/")
	for _, item := range items {
		if strings.Trim(strings.TrimSpace(item.Key), "/") == want {
			return true
		}
	}
	return false
}

func listS3PrefixPages(ctx context.Context, client s3ListObjectsV2Client, input *awss3.ListObjectsV2Input, bucket string, prefix string, requestPrefix string, options StoragePrefixListOptions, logEnabled bool) ([]StorageBucketObject, s3PrefixListStats, []string, error) {
	policy := s3ListPageRetryPolicyFromEnv()
	out := make([]StorageBucketObject, 0)
	stats := s3PrefixListStats{}
	firstKeys := make([]string, 0, 5)
	seenKeys := make(map[string]struct{})
	continuationToken := ""
	baseInput := cloneListObjectsV2Input(input, "")
	baseInput.StartAfter = nil
	for {
		pageNumber := stats.Pages + 1
		page, tokenID, retries, err := listS3PrefixPageWithRetry(ctx, client, baseInput, continuationToken, bucket, prefix, requestPrefix, policy, pageNumber, len(out), stats.LastKey, logEnabled)
		stats.LastTokenID = tokenID
		stats.Retries += retries
		if err != nil {
			stats.FailedPage = pageNumber
			return out, stats, firstKeys, err
		}
		stats.Pages++
		added := appendS3ListPageObjects(&out, page, bucket, &firstKeys, seenKeys)
		if len(out) > 0 {
			stats.LastKey = out[len(out)-1].Key
		}
		if logEnabled {
			log.Printf("INFO: syfon_s3_prefix_list_page_done request_id=%s bucket=%s requested_prefix=%q input_prefix=%q page=%d token=%s objects_added=%d objects_total=%d last_key=%q truncated=%t", requestmeta.GetRequestID(ctx), bucket, prefix, requestPrefix, pageNumber, tokenID, added, len(out), stats.LastKey, aws.ToBool(page.IsTruncated))
		}
		if !aws.ToBool(page.IsTruncated) {
			if options.MaxKeys != 1 {
				replays, replayRetries, replayErr := replayS3TerminalPage(ctx, client, baseInput, continuationToken, page, bucket, prefix, requestPrefix, policy, pageNumber, len(out), stats.LastKey, logEnabled)
				stats.TerminalReplayAttempts += replays
				stats.Retries += replayRetries
				if replayErr != nil {
					stats.TerminalDisagreements++
					stats.FailedPage = pageNumber
					return out, stats, firstKeys, replayErr
				}
			}
			return out, stats, firstKeys, nil
		}
		continuationToken = strings.TrimSpace(aws.ToString(page.NextContinuationToken))
		if continuationToken == "" {
			err := fmt.Errorf("list s3 objects for %s/%s stopped at page %d after %d objects: provider returned truncated page without next continuation token", bucket, strings.Trim(strings.TrimSpace(prefix), "/"), pageNumber, len(out))
			stats.FailedPage = pageNumber
			return out, stats, firstKeys, err
		}
		baseInput.StartAfter = nil
	}
}

func replayS3TerminalPage(ctx context.Context, client s3ListObjectsV2Client, baseInput *awss3.ListObjectsV2Input, continuationToken string, firstPage *awss3.ListObjectsV2Output, bucket, prefix, requestPrefix string, policy s3ListPageRetryPolicy, pageNumber, objectCount int, lastKey string, logEnabled bool) (int, int, error) {
	maxAttempts := intEnvOrDefault(envS3InventoryTerminalReplays, defaultS3InventoryTerminalReplays, 2)
	firstFingerprint := s3ListPageFingerprint(firstPage)
	replays := 0
	retries := 0
	for attempt := 2; attempt <= maxAttempts; attempt++ {
		page, tokenID, pageRetries, err := listS3PrefixPageWithRetry(ctx, client, baseInput, continuationToken, bucket, prefix, requestPrefix, policy, pageNumber, objectCount, lastKey, logEnabled)
		replays++
		if err != nil {
			return replays, retries + pageRetries, incompleteS3ListingError(bucket, prefix, lastKey, "terminal replay failed", err)
		}
		retries += pageRetries
		if logEnabled {
			log.Printf("INFO: syfon_s3_inventory_terminal_replay request_id=%s bucket=%s requested_prefix=%q page=%d token=%s replay=%d max_replays=%d objects=%d truncated=%t", requestmeta.GetRequestID(ctx), bucket, prefix, pageNumber, tokenID, attempt-1, maxAttempts-1, len(page.Contents), aws.ToBool(page.IsTruncated))
		}
		if s3ListPageFingerprint(page) != firstFingerprint {
			return replays, retries, incompleteS3ListingError(bucket, prefix, lastKey, "terminal replay returned different page content", nil)
		}
	}
	return replays, retries, nil
}

func s3ListPageFingerprint(page *awss3.ListObjectsV2Output) string {
	if page == nil {
		return "nil"
	}
	objects := append([]types.Object(nil), page.Contents...)
	sort.Slice(objects, func(i, j int) bool {
		return aws.ToString(objects[i].Key) < aws.ToString(objects[j].Key)
	})
	var builder strings.Builder
	fmt.Fprintf(&builder, "truncated=%t\n", aws.ToBool(page.IsTruncated))
	for _, object := range objects {
		lastModified := ""
		if object.LastModified != nil {
			lastModified = object.LastModified.UTC().Format(time.RFC3339Nano)
		}
		fmt.Fprintf(&builder, "%s\x00%d\x00%s\x00%s\n", aws.ToString(object.Key), aws.ToInt64(object.Size), aws.ToString(object.ETag), lastModified)
	}
	sum := sha256.Sum256([]byte(builder.String()))
	return hex.EncodeToString(sum[:])[:16]
}

func incompleteS3ListingError(bucket, prefix, lastKey, reason string, cause error) error {
	message := fmt.Sprintf("provider returned an incomplete listing for s3://%s/%s after key %q: %s", bucket, strings.Trim(strings.TrimSpace(prefix), "/"), lastKey, reason)
	if cause != nil {
		message += ": " + cause.Error()
	}
	return &StorageInspectError{Kind: StorageInspectListingIncomplete, Message: message}
}

func listS3PrefixPageWithRetry(ctx context.Context, client s3ListObjectsV2Client, baseInput *awss3.ListObjectsV2Input, continuationToken string, bucket string, prefix string, requestPrefix string, policy s3ListPageRetryPolicy, pageNumber int, objectCount int, lastKey string, logEnabled bool) (*awss3.ListObjectsV2Output, string, int, error) {
	tokenID := s3ContinuationTokenFingerprint(continuationToken)
	retries := 0
	for attempt := 1; ; attempt++ {
		release, acquireErr := acquireS3Probe(ctx, "list", bucket, prefix)
		if acquireErr != nil {
			return nil, tokenID, retries, acquireErr
		}
		page, err := client.ListObjectsV2(ctx, cloneListObjectsV2Input(baseInput, continuationToken))
		release()
		if err == nil {
			if page == nil {
				err = errors.New("provider returned an empty list page")
			} else if aws.ToBool(page.IsTruncated) && strings.TrimSpace(aws.ToString(page.NextContinuationToken)) == "" {
				err = errors.New("provider returned a malformed truncated list page without next continuation token")
			} else if aws.ToBool(page.IsTruncated) && len(page.Contents) == 0 {
				err = errors.New("provider returned an empty malformed truncated list page")
			} else {
				return page, tokenID, retries, nil
			}
		}
		if !isRetryableS3ListPageError(err) || attempt >= policy.MaxAttempts {
			if logEnabled {
				log.Printf("INFO: syfon_s3_prefix_list_page_failed request_id=%s bucket=%s requested_prefix=%q input_prefix=%q page=%d token=%s objects=%d attempt=%d max_attempts=%d last_key=%q retryable=%t error=%q", requestmeta.GetRequestID(ctx), bucket, prefix, requestPrefix, pageNumber, tokenID, objectCount, attempt, policy.MaxAttempts, lastKey, isRetryableS3ListPageError(err), err.Error())
			}
			return nil, tokenID, retries, fmt.Errorf("list s3 objects for %s/%s failed at page %d after %d objects and %d attempts: %w", bucket, strings.Trim(strings.TrimSpace(prefix), "/"), pageNumber, objectCount, attempt, err)
		}
		retries++
		backoff := policy.backoff(attempt)
		if logEnabled {
			log.Printf("INFO: syfon_s3_prefix_list_page_retry request_id=%s bucket=%s requested_prefix=%q input_prefix=%q page=%d token=%s objects=%d attempt=%d max_attempts=%d backoff_ms=%d last_key=%q error=%q", requestmeta.GetRequestID(ctx), bucket, prefix, requestPrefix, pageNumber, tokenID, objectCount, attempt, policy.MaxAttempts, backoff.Milliseconds(), lastKey, err.Error())
		}
		if err := sleepS3ListPageRetry(ctx, backoff); err != nil {
			return nil, tokenID, retries, err
		}
	}
}

func appendS3ListPageObjects(out *[]StorageBucketObject, page *awss3.ListObjectsV2Output, bucket string, firstKeys *[]string, seenKeys map[string]struct{}) int {
	before := len(*out)
	for _, item := range page.Contents {
		key := strings.Trim(strings.TrimSpace(aws.ToString(item.Key)), "/")
		if key == "" {
			continue
		}
		if _, seen := seenKeys[key]; seen {
			continue
		}
		seenKeys[key] = struct{}{}
		if len(*firstKeys) < cap(*firstKeys) {
			*firstKeys = append(*firstKeys, key)
		}
		var size int64
		if item.Size != nil {
			size = *item.Size
		}
		lastMod := time.Time{}
		if item.LastModified != nil {
			lastMod = *item.LastModified
		}
		*out = append(*out, StorageBucketObject{
			Provider:    common.S3Provider,
			Bucket:      bucket,
			Key:         key,
			ObjectURL:   common.BucketToURL(bucket, key),
			Path:        path.Base(key),
			SizeBytes:   size,
			ETag:        strings.Trim(strings.TrimSpace(aws.ToString(item.ETag)), "\""),
			LastModTime: lastMod,
		})
	}
	return len(*out) - before
}

func cloneListObjectsV2Input(input *awss3.ListObjectsV2Input, continuationToken string) *awss3.ListObjectsV2Input {
	clone := *input
	clone.StartAfter = nil
	if strings.TrimSpace(continuationToken) == "" {
		clone.ContinuationToken = nil
	} else {
		clone.ContinuationToken = aws.String(continuationToken)
	}
	return &clone
}

func isRetryableS3ListPageError(err error) bool {
	if err == nil {
		return false
	}
	var apiErr smithy.APIError
	if errors.As(err, &apiErr) {
		switch strings.ToLower(strings.TrimSpace(apiErr.ErrorCode())) {
		case "internalerror", "slowdown", "serviceunavailable", "requesttimeout", "requesttimeoutexception", "toomanyrequests", "throttling", "throttlingexception", "requestlimitexceeded":
			return true
		default:
			return false
		}
	}
	if errors.Is(err, io.EOF) {
		return true
	}
	var netErr net.Error
	if errors.As(err, &netErr) && (netErr.Timeout() || netErr.Temporary()) {
		return true
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "eof") || strings.Contains(msg, "connection reset") || strings.Contains(msg, "timeout") ||
		strings.Contains(msg, "malformed truncated list page") || strings.Contains(msg, "empty list page")
}

func s3ListPageRetryPolicyFromEnv() s3ListPageRetryPolicy {
	return s3ListPageRetryPolicy{
		MaxAttempts:    intEnvOrDefault(envS3ListPageMaxAttempts, defaultS3ListPageMaxAttempts, 1),
		InitialBackoff: millisEnvOrDefault(envS3ListPageInitialBackoffMillis, defaultS3ListPageInitialBackoff, 0),
		MaxBackoff:     millisEnvOrDefault(envS3ListPageMaxBackoffMillis, defaultS3ListPageMaxBackoff, 0),
	}
}

func intEnvOrDefault(name string, fallback int, minimum int) int {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < minimum {
		return fallback
	}
	return value
}

func millisEnvOrDefault(name string, fallback time.Duration, minimumMillis int) time.Duration {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < minimumMillis {
		return fallback
	}
	return time.Duration(value) * time.Millisecond
}

func (policy s3ListPageRetryPolicy) backoff(attempt int) time.Duration {
	backoff := policy.InitialBackoff
	for i := 1; i < attempt; i++ {
		backoff *= 2
		if backoff >= policy.MaxBackoff {
			backoff = policy.MaxBackoff
			break
		}
	}
	if backoff <= 0 {
		return 0
	}
	jitterMax := backoff / 4
	if jitterMax <= 0 {
		return backoff
	}
	return backoff + time.Duration(rand.Int63n(int64(jitterMax)+1))
}

func s3ContinuationTokenFingerprint(token string) string {
	token = strings.TrimSpace(token)
	if token == "" {
		return "start"
	}
	sum := sha1.Sum([]byte(token))
	return hex.EncodeToString(sum[:])[:12]
}

var sleepS3ListPageRetry = func(ctx context.Context, duration time.Duration) error {
	if duration <= 0 {
		return nil
	}
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func classifyS3ListError(bucket string, prefix string, err error) error {
	var apiErr smithy.APIError
	if errors.As(err, &apiErr) {
		switch strings.ToLower(strings.TrimSpace(apiErr.ErrorCode())) {
		case "forbidden", "accessdenied", "permissiondenied":
			return &StorageInspectError{
				Kind: StorageInspectBucketUnavailable,
				Message: fmt.Sprintf(
					"provider rejected bucket inventory request for s3://%s/%s; mapped bucket target may be missing or inaccessible",
					bucket,
					strings.Trim(strings.TrimSpace(prefix), "/"),
				),
			}
		case "nosuchbucket":
			return &StorageInspectError{Kind: StorageInspectBucketUnavailable, Message: fmt.Sprintf("provider could not find bucket %q", bucket)}
		case "notfound":
			return &StorageInspectError{Kind: StorageInspectBucketUnavailable, Message: fmt.Sprintf("provider could not resolve bucket inventory target %q", bucket)}
		}
	}
	return fmt.Errorf("list s3 objects for %s/%s: %w", bucket, strings.Trim(strings.TrimSpace(prefix), "/"), err)
}
