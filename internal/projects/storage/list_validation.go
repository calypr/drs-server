package storage

import (
	"context"
	"fmt"
	"path"
	"sort"
	"strings"
	"sync"

	"github.com/calypr/syfon/internal/buckets"
	"github.com/calypr/syfon/internal/storage/address"
)

type validationWork struct {
	bucket         string
	key            string
	base           ListValidationResult
	requestIndexes []int
}

// ValidateInventoryObjects compares requested physical S3 locations with
// inventory evidence. Exact targets are deduplicated, dense sibling keys are
// coalesced at the historical threshold, and output is always restored to
// input order including duplicate requests.
func (s *Inspector) ValidateInventoryObjects(ctx context.Context, requests []ListValidationRequest) []ListValidationResult {
	ctx = withRequestCache(ctx)
	if len(requests) == 0 {
		return []ListValidationResult{}
	}
	visible, visibleErr := s.visibleBuckets(ctx)
	results := make([]ListValidationResult, len(requests))
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
	outcomes := make(map[string]ListValidationResult, len(workByTarget))
	matched := make(map[string]StorageObject, len(workByTarget))
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
			outcome.Status = ProbeNotFound
			outcome.ErrorKind = string(ErrorObjectNotFound)
			outcome.Error = fmt.Sprintf("object %q was not found", work.base.ObjectURL)
		}
		for _, index := range work.requestIndexes {
			request := requests[index]
			if item, exists := matched[key]; exists {
				result := presentValidationResult(request, work.base, item)
				result.ID = strings.TrimSpace(request.ID)
				result.ObjectURL = strings.TrimSpace(request.ObjectURL)
				results[index] = result
				continue
			}
			result := outcome
			result.ID = strings.TrimSpace(request.ID)
			result.ObjectURL = strings.TrimSpace(request.ObjectURL)
			result.ValidationStatus = validationErrorStatus(request)
			results[index] = result
		}
	}
	return results
}

func (s *Inspector) validationTarget(ctx context.Context, request ListValidationRequest, index int, visible map[string]buckets.VisibleBucket, visibleErr error) (ListValidationResult, *validationWork, bool) {
	base := ListValidationResult{
		ID:               strings.TrimSpace(request.ID),
		ObjectURL:        strings.TrimSpace(request.ObjectURL),
		Status:           ProbeError,
		ValidationStatus: ValidationNotRequested,
	}
	bucket, key, ok := address.ParseS3URL(request.ObjectURL)
	if !ok {
		base.Status = ProbeInvalid
		base.ErrorKind = string(ErrorInvalidInput)
		base.Error = "object_url must be a valid s3://bucket/key URL"
		base.ValidationStatus = validationErrorStatus(request)
		return base, nil, false
	}
	base.Provider = address.S3Provider
	base.Bucket = bucket
	base.Key = key
	base.Path = path.Base(key)
	credential, err := s.credentialForBucket(ctx, bucket)
	if err != nil {
		base.Status, base.ErrorKind = classifyError(err)
		base.Error = strings.TrimSpace(err.Error())
		base.ValidationStatus = validationErrorStatus(request)
		return base, nil, false
	}
	if visibleErr != nil {
		base.Status, base.ErrorKind = classifyError(visibleErr)
		base.Error = strings.TrimSpace(visibleErr.Error())
		base.ValidationStatus = validationErrorStatus(request)
		return base, nil, false
	}
	if !buckets.VisibleToCaller(visible, bucket, credential.CredentialID) {
		err := &Error{Kind: ErrorPermissionDenied, Message: fmt.Sprintf("bucket %q is not visible to the caller", bucket)}
		base.Status, base.ErrorKind = classifyError(err)
		base.Error = err.Error()
		base.ValidationStatus = validationErrorStatus(request)
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

func (s *Inspector) runCoalescedValidation(ctx context.Context, group []*validationWork, outcomes map[string]ListValidationResult, matched map[string]StorageObject, unresolved map[string]*validationWork) {
	if len(group) == 0 {
		return
	}
	requested := make(map[string]*validationWork, len(group))
	for _, work := range group {
		requested[validationTargetKey(work.bucket, work.key)] = work
	}
	prefix := validationDirectoryPrefix(group[0].key)
	items, err := s.inventoryObjects(ctx, group[0].bucket, prefix, InventoryOptions{ExactPrefix: true})
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
		item = normalizeObjects([]StorageObject{item}, scopeTarget{Bucket: work.bucket})[0]
		matched[key] = item
		outcome := work.base
		outcome.Exists = true
		outcome.Status = ProbePresent
		outcome.Error = ""
		outcome.ErrorKind = ""
		outcomes[key] = outcome
		delete(unresolved, key)
	}
}

func (s *Inspector) runExactValidation(ctx context.Context, unresolved map[string]*validationWork, outcomes map[string]ListValidationResult, matched map[string]StorageObject) {
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
				items, err := s.inventoryObjects(ctx, work.bucket, work.key, InventoryOptions{ExactPrefix: true, MaxKeys: 1})
				outcome := work.base
				var present *StorageObject
				if err != nil {
					outcome.Status, outcome.ErrorKind = classifyError(err)
					outcome.Error = strings.TrimSpace(err.Error())
				} else {
					for index := range items {
						if strings.Trim(strings.TrimSpace(items[index].Key), "/") != work.key {
							continue
						}
						item := normalizeObjects([]StorageObject{items[index]}, scopeTarget{Bucket: work.bucket})[0]
						present = &item
						break
					}
					if present == nil {
						outcome.Status = ProbeNotFound
						outcome.ErrorKind = string(ErrorObjectNotFound)
						outcome.Error = fmt.Sprintf("object %q was not found", work.base.ObjectURL)
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

func presentValidationResult(request ListValidationRequest, base ListValidationResult, item StorageObject) ListValidationResult {
	base.ObjectURL = item.ObjectURL
	base.Key = item.Key
	base.Path = item.Path
	base.Exists = true
	base.Status = ProbePresent
	base.Error = ""
	base.ErrorKind = ""
	base.SizeBytes = int64Pointer(item.SizeBytes)
	base.ETag = strings.TrimSpace(item.ETag)
	base.LastModTime = item.LastModTime
	base.ValidationStatus, base.SizeMatch, base.NameMatch, base.ValidationMismatches = validateInventory(request, item)
	return base
}

func validationErrorStatus(request ListValidationRequest) ValidationStatus {
	if request.ExpectedSizeBytes == nil && strings.TrimSpace(request.ExpectedName) == "" {
		return ValidationNotRequested
	}
	return ValidationUnverifiable
}

func validateInventory(request ListValidationRequest, item StorageObject) (ValidationStatus, *bool, *bool, []string) {
	if request.ExpectedSizeBytes == nil && strings.TrimSpace(request.ExpectedName) == "" {
		return ValidationNotRequested, nil, nil, nil
	}
	mismatches := make([]string, 0, 2)
	var sizeMatch *bool
	if request.ExpectedSizeBytes != nil {
		matched := item.SizeBytes == *request.ExpectedSizeBytes
		sizeMatch = &matched
		if !matched {
			mismatches = append(mismatches, "size_mismatch")
		}
	}
	var nameMatch *bool
	if expectedName := strings.TrimSpace(request.ExpectedName); expectedName != "" {
		matched := path.Base(item.Key) == expectedName
		nameMatch = &matched
		if !matched {
			mismatches = append(mismatches, "name_mismatch")
		}
	}
	if len(mismatches) > 0 {
		return ValidationMismatched, sizeMatch, nameMatch, mismatches
	}
	return ValidationMatched, sizeMatch, nameMatch, nil
}
