package storage

import (
	"context"
	"fmt"
	"path"
	"sort"
	"strings"
	"sync"

	internalapi "github.com/calypr/syfon/apigen/internalapi"
	"github.com/calypr/syfon/internal/buckets"
	"github.com/calypr/syfon/internal/storage/address"
)

type validationStatus string

const (
	validationNotRequested validationStatus = "not_requested"
	validationMatched      validationStatus = "matched"
	validationMismatched   validationStatus = "mismatched"
	validationUnverifiable validationStatus = "unverifiable"
)

type validationWork struct {
	bucket         string
	key            string
	base           internalapi.InternalInspectObjectBulkItem
	requestIndexes []int
}

// ValidateInventoryObjects compares requested physical S3 locations with
// inventory evidence. Exact targets are deduplicated, dense sibling keys are
// coalesced at the historical threshold, and output is always restored to
// input order including duplicate requests.
func (s *Service) ValidateInventoryObjects(ctx context.Context, requests []InspectRequest) []internalapi.InternalInspectObjectBulkItem {
	ctx = withRequestCache(ctx)
	if len(requests) == 0 {
		return []internalapi.InternalInspectObjectBulkItem{}
	}
	visible, visibleErr := s.visibleBuckets(ctx)
	results := make([]internalapi.InternalInspectObjectBulkItem, len(requests))
	workByTarget := make(map[string]*validationWork)
	for index, request := range requests {
		base, work, ok := s.validationTarget(ctx, request, index, visible, visibleErr)
		if !ok {
			results[index] = base
			continue
		}
		key := validationTargetKey(work.bucket, work.key)
		if existing := workByTarget[key]; existing != nil {
			existing.requestIndexes = append(existing.requestIndexes, index)
			continue
		}
		workByTarget[key] = work
	}

	groups := groupValidationTargets(workByTarget)
	outcomes := make(map[string]internalapi.InternalInspectObjectBulkItem, len(workByTarget))
	matched := make(map[string]internalapi.InternalInspectProjectBucketItem, len(workByTarget))
	unresolved := cloneValidationWork(workByTarget)
	for _, group := range groups {
		if len(group) < listCoalesceThreshold {
			continue
		}
		s.runCoalescedValidation(ctx, group, outcomes, matched, unresolved)
	}
	s.runExactValidation(ctx, unresolved, outcomes, matched)

	for key, work := range workByTarget {
		outcome, found := outcomes[key]
		if !found {
			outcome = work.base
			outcome.Status = string(probeNotFound)
			outcome.ErrorKind = string(ErrorObjectNotFound)
			outcome.Error = fmt.Sprintf("object %q was not found", work.base.ObjectUrl)
		}
		for _, index := range work.requestIndexes {
			request := requests[index]
			if item, exists := matched[key]; exists {
				result := presentValidationResult(request, work.base, item)
				result.Id = strings.TrimSpace(request.ID)
				result.ObjectUrl = strings.TrimSpace(request.ObjectURL)
				results[index] = result
				continue
			}
			result := outcome
			result.Id = strings.TrimSpace(request.ID)
			result.ObjectUrl = strings.TrimSpace(request.ObjectURL)
			result.ValidationStatus = string(validationStatusForError(request))
			results[index] = result
		}
	}
	return results
}

func (s *Service) validationTarget(ctx context.Context, request InspectRequest, index int, visible map[string]buckets.VisibleBucket, visibleErr error) (internalapi.InternalInspectObjectBulkItem, *validationWork, bool) {
	base := internalapi.InternalInspectObjectBulkItem{
		Id:               strings.TrimSpace(request.ID),
		ObjectUrl:        strings.TrimSpace(request.ObjectURL),
		Status:           string(probeError),
		ValidationStatus: string(validationNotRequested),
	}
	bucket, key, ok := address.ParseS3URL(request.ObjectURL)
	if !ok {
		base.Status = string(probeInvalid)
		base.ErrorKind = string(ErrorInvalidInput)
		base.Error = "object_url must be a valid s3://bucket/key URL"
		base.ValidationStatus = string(validationStatusForError(request))
		return base, nil, false
	}
	base.Provider = address.S3Provider
	base.Bucket = bucket
	base.Key = key
	base.Path = path.Base(key)
	credential, err := s.credentialForBucket(ctx, bucket)
	if err != nil {
		status, kind := classifyError(err)
		base.Status, base.ErrorKind = string(status), kind
		logStorageDiagnostic(ctx, err, "inventory")
		base.Error = safeStorageErrorMessage(err, "inventory")
		base.ValidationStatus = string(validationStatusForError(request))
		return base, nil, false
	}
	if visibleErr != nil {
		status, kind := classifyError(visibleErr)
		base.Status, base.ErrorKind = string(status), kind
		base.Error = safeStorageErrorMessage(visibleErr, "inventory")
		base.ValidationStatus = string(validationStatusForError(request))
		return base, nil, false
	}
	if !buckets.VisibleToCaller(visible, bucket, credential.CredentialID) {
		err := &Error{Kind: ErrorPermissionDenied, Message: fmt.Sprintf("bucket %q is not visible to the caller", bucket)}
		status, kind := classifyError(err)
		base.Status, base.ErrorKind = string(status), kind
		base.Error = err.Error()
		base.ValidationStatus = string(validationStatusForError(request))
		return base, nil, false
	}
	return base, &validationWork{bucket: bucket, key: key, base: base, requestIndexes: []int{index}}, true
}

func validationTargetKey(bucket, key string) string {
	return strings.TrimSpace(bucket) + "\x00" + strings.Trim(strings.TrimSpace(key), "/")
}

func validationDirectoryPrefix(key string) string {
	directory := strings.Trim(strings.TrimSpace(path.Dir(strings.Trim(key, "/"))), "/")
	if directory == "." {
		return ""
	}
	if directory == "" {
		return ""
	}
	return directory + "/"
}

func groupValidationTargets(workByTarget map[string]*validationWork) map[string][]*validationWork {
	groups := make(map[string][]*validationWork)
	for _, work := range workByTarget {
		key := strings.TrimSpace(work.bucket) + "\x00" + validationDirectoryPrefix(work.key)
		groups[key] = append(groups[key], work)
	}
	return groups
}

func cloneValidationWork(input map[string]*validationWork) map[string]*validationWork {
	output := make(map[string]*validationWork, len(input))
	for key, value := range input {
		output[key] = value
	}
	return output
}

func (s *Service) runCoalescedValidation(ctx context.Context, group []*validationWork, outcomes map[string]internalapi.InternalInspectObjectBulkItem, matched map[string]internalapi.InternalInspectProjectBucketItem, unresolved map[string]*validationWork) {
	if len(group) == 0 {
		return
	}
	requested := make(map[string]*validationWork, len(group))
	for _, work := range group {
		requested[validationTargetKey(work.bucket, work.key)] = work
	}
	prefix := validationDirectoryPrefix(group[0].key)
	items, err := s.inventoryObjects(ctx, group[0].bucket, prefix, inventoryOptions{ExactPrefix: true})
	if err != nil {
		return
	}
	if len(items) > listFallbackObjectLimit {
		// Keep the exact fallback for unresolved items. This protects the
		// request from treating a truncated/coalesced page as evidence.
		return
	}
	for _, item := range items {
		key := validationTargetKey(group[0].bucket, item.Key)
		work := requested[key]
		if work == nil {
			continue
		}
		item = normalizeObjects([]internalapi.InternalInspectProjectBucketItem{item}, scopeTarget{Bucket: work.bucket})[0]
		matched[key] = item
		outcome := work.base
		outcome.Exists = true
		outcome.Status = string(probePresent)
		outcome.Error = ""
		outcome.ErrorKind = ""
		outcomes[key] = outcome
		delete(unresolved, key)
	}
}

func (s *Service) runExactValidation(ctx context.Context, unresolved map[string]*validationWork, outcomes map[string]internalapi.InternalInspectObjectBulkItem, matched map[string]internalapi.InternalInspectProjectBucketItem) {
	keys := make([]string, 0, len(unresolved))
	for key := range unresolved {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	if len(keys) == 0 {
		return
	}
	workers := len(keys)
	if workers > maxProbeWorkers {
		workers = maxProbeWorkers
	}
	workCh := make(chan string)
	var wg sync.WaitGroup
	var mu sync.Mutex
	for worker := 0; worker < workers; worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for key := range workCh {
				work := unresolved[key]
				items, err := s.inventoryObjects(ctx, work.bucket, work.key, inventoryOptions{ExactPrefix: true, MaxKeys: 1})
				outcome := work.base
				var present *internalapi.InternalInspectProjectBucketItem
				if err != nil {
					status, kind := classifyError(err)
					outcome.Status, outcome.ErrorKind = string(status), kind
					logStorageDiagnostic(ctx, err, "inventory")
					outcome.Error = safeStorageErrorMessage(err, "inventory")
				} else {
					for index := range items {
						if strings.Trim(strings.TrimSpace(items[index].Key), "/") != work.key {
							continue
						}
						item := normalizeObjects([]internalapi.InternalInspectProjectBucketItem{items[index]}, scopeTarget{Bucket: work.bucket})[0]
						present = &item
						break
					}
					if present == nil {
						outcome.Status = string(probeNotFound)
						outcome.ErrorKind = string(ErrorObjectNotFound)
						outcome.Error = fmt.Sprintf("object %q was not found", work.base.ObjectUrl)
					}
				}
				mu.Lock()
				outcomes[key] = outcome
				if present != nil {
					matched[key] = *present
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

func presentValidationResult(request InspectRequest, base internalapi.InternalInspectObjectBulkItem, item internalapi.InternalInspectProjectBucketItem) internalapi.InternalInspectObjectBulkItem {
	base.ObjectUrl = item.ObjectUrl
	base.Key = item.Key
	base.Path = item.Path
	base.Exists = true
	base.Status = string(probePresent)
	base.Error = ""
	base.ErrorKind = ""
	base.SizeBytes = int64Pointer(item.SizeBytes)
	base.Etag = strings.TrimSpace(item.Etag)
	base.LastModified = item.LastModified
	status, sizeMatch, nameMatch, shaMatch, mismatches := validateObject(request, objectMetadata{
		Key:        item.Key,
		SizeBytes:  item.SizeBytes,
		MetaSHA256: item.MetaSha256,
	})
	base.ValidationStatus = string(status)
	base.SizeMatch, base.NameMatch, base.Sha256Match, base.ValidationMismatches = sizeMatch, nameMatch, shaMatch, mismatches
	return base
}
