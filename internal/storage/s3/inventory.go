package s3

import (
	"context"
	"errors"
	"fmt"
	"log"
	"path"
	"strings"
	"sync"

	"github.com/aws/aws-sdk-go-v2/aws"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/smithy-go"

	"github.com/calypr/syfon/internal/requestid"
	"github.com/calypr/syfon/internal/storage"
	"github.com/calypr/syfon/internal/storage/address"
)

const listPageSize int32 = 1000

type s3ListClient interface {
	ListObjectsV2(context.Context, *awss3.ListObjectsV2Input, ...func(*awss3.Options)) (*awss3.ListObjectsV2Output, error)
}

type listStats struct {
	Pages                  int
	Retries                int
	LastKey                string
	FailedPage             int
	LastTokenID            string
	TerminalReplayAttempts int
	TerminalDisagreements  int
}

func (s *backend) Inventory(ctx context.Context, request storage.InventoryRequest) (storage.InventoryResult, error) {
	clients, err := s.getClients(ctx, request.Target.Bucket)
	if err != nil {
		return storage.InventoryResult{}, providerError(storage.ErrorProvider, "inventory", err)
	}
	input := &awss3.ListObjectsV2Input{
		Bucket:  aws.String(request.Target.Bucket),
		MaxKeys: aws.Int32(listPageSize),
	}
	if prefix := strings.Trim(strings.TrimSpace(request.Target.Prefix), "/"); prefix != "" {
		if request.ExactPrefix {
			input.Prefix = aws.String(prefix)
		} else {
			input.Prefix = aws.String(prefix + "/")
		}
	}
	if request.MaxKeys > 0 {
		input.MaxKeys = aws.Int32(request.MaxKeys)
	}
	requestPrefix := aws.ToString(input.Prefix)
	items, stats, firstKeys, listErr := s.listPagesWithExactProbeRetry(ctx, clients.client, input, request.Target.Bucket, request.Target.Prefix, requestPrefix, request, storageLoggingEnabled(ctx))
	if listErr != nil {
		if operation, ok := listErr.(*storage.OperationError); ok && operation.Kind == storage.ErrorIncomplete {
			return storage.InventoryResult{Items: items, Complete: false}, listErr
		}
		classified := classifyListError(request.Target.Bucket, request.Target.Prefix, listErr)
		if len(items) > 0 {
			return storage.InventoryResult{Items: items, Complete: false}, &storage.OperationError{
				Kind:       storage.ErrorIncomplete,
				Provider:   address.S3Provider,
				Capability: "inventory",
				Cause:      classified,
			}
		}
		return storage.InventoryResult{}, classified
	}
	_ = stats
	_ = firstKeys
	if request.IncludeHead && len(items) > 0 {
		// Preserve the old IncludeHead contract: one HEAD failure drops the
		// whole listed result and returns only that error.
		if err := s.enrichInventoryHeads(ctx, clients, request.Target.Bucket, items); err != nil {
			return storage.InventoryResult{}, err
		}
	}
	return storage.InventoryResult{Items: items, Complete: true}, nil
}

func (s *backend) enrichInventoryHeads(ctx context.Context, clients *clients, bucket string, items []storage.ObjectMetadata) error {
	workers := len(items)
	if workers > maxProbeWorkers {
		workers = maxProbeWorkers
	}
	indexes := make(chan int)
	var wait sync.WaitGroup
	var firstErr error
	var errMu sync.Mutex
	for worker := 0; worker < workers; worker++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			for index := range indexes {
				output, err := s.headWithRetry(ctx, clients.client, bucket, items[index].Key)
				if err != nil {
					errMu.Lock()
					if firstErr == nil {
						firstErr = classifyHeadError(storage.ObjectTarget{Bucket: bucket, Key: items[index].Key}, err)
					}
					errMu.Unlock()
					continue
				}
				metadata := metadataFromHead(storage.ObjectTarget{Bucket: bucket, Key: items[index].Key}, output)
				items[index].MetaSHA256 = strings.TrimSpace(metadata.MetaSHA256)
				if items[index].ETag == "" {
					items[index].ETag = metadata.ETag
				}
				if items[index].LastModified.IsZero() {
					items[index].LastModified = metadata.LastModified
				}
			}
		}()
	}
	for index := range items {
		indexes <- index
	}
	close(indexes)
	wait.Wait()
	return firstErr
}

func (s *backend) listPagesWithExactProbeRetry(ctx context.Context, client s3ListClient, input *awss3.ListObjectsV2Input, bucket, prefix, requestPrefix string, request storage.InventoryRequest, logging bool) ([]storage.ObjectMetadata, listStats, []string, error) {
	if request.MaxKeys != 1 || !request.ExactPrefix {
		return s.listPages(ctx, client, input, bucket, prefix, requestPrefix, request, logging)
	}
	policy := listPageRetryPolicyFromEnv()
	maxAttempts := intEnvOrDefault(envExactProbeMaxAttempts, defaultExactProbeMaxAttempts, 1)
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		items, stats, firstKeys, err := s.listPages(ctx, client, input, bucket, prefix, requestPrefix, request, logging)
		if err != nil || hasExactListedKey(items, prefix) || attempt == maxAttempts {
			return items, stats, firstKeys, err
		}
		if err := sleepListPageRetry(ctx, policy.backoff(attempt)); err != nil {
			return nil, stats, firstKeys, err
		}
	}
	return nil, listStats{}, nil, nil
}

func (s *backend) listPages(ctx context.Context, client s3ListClient, input *awss3.ListObjectsV2Input, bucket, prefix, requestPrefix string, request storage.InventoryRequest, logging bool) ([]storage.ObjectMetadata, listStats, []string, error) {
	policy := listPageRetryPolicyFromEnv()
	items := make([]storage.ObjectMetadata, 0)
	stats := listStats{}
	firstKeys := make([]string, 0, 5)
	seen := make(map[string]struct{})
	continuationToken := ""
	baseInput := cloneListInput(input, "")
	baseInput.StartAfter = nil
	for {
		pageNumber := stats.Pages + 1
		page, tokenID, retries, err := s.listPageWithRetry(ctx, client, baseInput, continuationToken, bucket, prefix, requestPrefix, policy, pageNumber, len(items), stats.LastKey, logging)
		stats.LastTokenID = tokenID
		stats.Retries += retries
		if err != nil {
			stats.FailedPage = pageNumber
			return items, stats, firstKeys, err
		}
		stats.Pages++
		appendListPageObjects(&items, page, bucket, &firstKeys, seen)
		if len(items) > 0 {
			stats.LastKey = items[len(items)-1].Key
		}
		if logging {
			log.Printf("INFO: syfon_s3_prefix_list_page_done request_id=%s bucket=%s requested_prefix=%q input_prefix=%q page=%d token=%s objects_total=%d last_key=%q truncated=%t", requestid.GetRequestID(ctx), bucket, prefix, requestPrefix, pageNumber, tokenID, len(items), stats.LastKey, aws.ToBool(page.IsTruncated))
		}
		if !aws.ToBool(page.IsTruncated) {
			if request.MaxKeys != 1 {
				replays, replayRetries, replayErr := s.replayTerminalPage(ctx, client, baseInput, continuationToken, page, bucket, prefix, requestPrefix, policy, pageNumber, len(items), stats.LastKey, logging)
				stats.TerminalReplayAttempts += replays
				stats.Retries += replayRetries
				if replayErr != nil {
					stats.TerminalDisagreements++
					stats.FailedPage = pageNumber
					return items, stats, firstKeys, replayErr
				}
			}
			return items, stats, firstKeys, nil
		}
		continuationToken = strings.TrimSpace(aws.ToString(page.NextContinuationToken))
		if continuationToken == "" {
			return items, stats, firstKeys, fmt.Errorf("list s3 objects for %s/%s stopped at page %d after %d objects: provider returned truncated page without next continuation token", bucket, strings.Trim(strings.TrimSpace(prefix), "/"), pageNumber, len(items))
		}
		baseInput.StartAfter = nil
	}
}

func (s *backend) replayTerminalPage(ctx context.Context, client s3ListClient, baseInput *awss3.ListObjectsV2Input, continuationToken string, firstPage *awss3.ListObjectsV2Output, bucket, prefix, requestPrefix string, policy listPageRetryPolicy, pageNumber, objectCount int, lastKey string, logging bool) (int, int, error) {
	maxAttempts := intEnvOrDefault(envInventoryTerminalReplays, defaultInventoryTerminalReplays, 2)
	fingerprint := listPageFingerprint(firstPage)
	replays := 0
	retries := 0
	for attempt := 2; attempt <= maxAttempts; attempt++ {
		page, tokenID, pageRetries, err := s.listPageWithRetry(ctx, client, baseInput, continuationToken, bucket, prefix, requestPrefix, policy, pageNumber, objectCount, lastKey, logging)
		replays++
		if err != nil {
			return replays, retries + pageRetries, incompleteListingError(bucket, prefix, lastKey, "terminal replay failed", err)
		}
		retries += pageRetries
		if listPageFingerprint(page) != fingerprint {
			return replays, retries, incompleteListingError(bucket, prefix, lastKey, "terminal replay returned different page content", nil)
		}
		_ = tokenID
	}
	return replays, retries, nil
}

func (s *backend) listPageWithRetry(ctx context.Context, client s3ListClient, baseInput *awss3.ListObjectsV2Input, continuationToken, bucket, prefix, requestPrefix string, policy listPageRetryPolicy, pageNumber, objectCount int, lastKey string, logging bool) (*awss3.ListObjectsV2Output, string, int, error) {
	tokenID := continuationTokenFingerprint(continuationToken)
	retries := 0
	for attempt := 1; ; attempt++ {
		release, acquireErr := s.acquireProbe(ctx, "list", bucket, prefix)
		if acquireErr != nil {
			return nil, tokenID, retries, acquireErr
		}
		page, err := client.ListObjectsV2(ctx, cloneListInput(baseInput, continuationToken))
		release()
		if err == nil {
			switch {
			case page == nil:
				err = errors.New("provider returned an empty list page")
			case aws.ToBool(page.IsTruncated) && strings.TrimSpace(aws.ToString(page.NextContinuationToken)) == "":
				err = errors.New("provider returned a malformed truncated list page without next continuation token")
			case aws.ToBool(page.IsTruncated) && len(page.Contents) == 0:
				err = errors.New("provider returned an empty malformed truncated list page")
			default:
				return page, tokenID, retries, nil
			}
		}
		if !isRetryableListPageError(err) || attempt >= policy.MaxAttempts {
			if logging {
				log.Printf("INFO: syfon_s3_prefix_list_page_failed request_id=%s bucket=%s requested_prefix=%q input_prefix=%q page=%d token=%s objects=%d attempt=%d max_attempts=%d last_key=%q retryable=%t error=%q", requestid.GetRequestID(ctx), bucket, prefix, requestPrefix, pageNumber, tokenID, objectCount, attempt, policy.MaxAttempts, lastKey, isRetryableListPageError(err), err.Error())
			}
			return nil, tokenID, retries, fmt.Errorf("list s3 objects for %s/%s failed at page %d after %d objects and %d attempts: %w", bucket, strings.Trim(strings.TrimSpace(prefix), "/"), pageNumber, objectCount, attempt, err)
		}
		retries++
		if err := sleepListPageRetry(ctx, policy.backoff(attempt)); err != nil {
			return nil, tokenID, retries, err
		}
	}
}

func appendListPageObjects(items *[]storage.ObjectMetadata, page *awss3.ListObjectsV2Output, bucket string, firstKeys *[]string, seen map[string]struct{}) int {
	before := len(*items)
	for _, object := range page.Contents {
		key := strings.Trim(strings.TrimSpace(aws.ToString(object.Key)), "/")
		if key == "" {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		if len(*firstKeys) < cap(*firstKeys) {
			*firstKeys = append(*firstKeys, key)
		}
		metadata := storage.ObjectMetadata{Provider: address.S3Provider, Bucket: bucket, Key: key, Path: path.Base(key)}
		if object.Size != nil {
			metadata.SizeBytes = *object.Size
		}
		if object.LastModified != nil {
			metadata.LastModified = *object.LastModified
		}
		metadata.ETag = strings.Trim(strings.TrimSpace(aws.ToString(object.ETag)), "\"")
		*items = append(*items, metadata)
	}
	return len(*items) - before
}

func cloneListInput(input *awss3.ListObjectsV2Input, continuationToken string) *awss3.ListObjectsV2Input {
	clone := *input
	clone.StartAfter = nil
	if strings.TrimSpace(continuationToken) == "" {
		clone.ContinuationToken = nil
	} else {
		clone.ContinuationToken = aws.String(continuationToken)
	}
	return &clone
}

func hasExactListedKey(items []storage.ObjectMetadata, key string) bool {
	want := strings.Trim(strings.TrimSpace(key), "/")
	for _, item := range items {
		if strings.Trim(strings.TrimSpace(item.Key), "/") == want {
			return true
		}
	}
	return false
}

func incompleteListingError(bucket, prefix, lastKey, reason string, cause error) error {
	message := fmt.Sprintf("provider returned an incomplete listing for s3://%s/%s after key %q: %s", bucket, strings.Trim(strings.TrimSpace(prefix), "/"), lastKey, reason)
	if cause != nil {
		message += ": " + cause.Error()
	}
	return &storage.OperationError{Kind: storage.ErrorIncomplete, Provider: address.S3Provider, Capability: "inventory", Cause: errors.New(message)}
}

func classifyListError(bucket, prefix string, err error) error {
	var apiErr smithy.APIError
	if errors.As(err, &apiErr) {
		switch strings.ToLower(strings.TrimSpace(apiErr.ErrorCode())) {
		case "forbidden", "accessdenied", "permissiondenied":
			return providerError(storage.ErrorUnavailable, "inventory", fmt.Errorf("provider rejected bucket inventory request for s3://%s/%s; mapped bucket target may be missing or inaccessible: %w", bucket, strings.Trim(strings.TrimSpace(prefix), "/"), err))
		case "nosuchbucket":
			return providerError(storage.ErrorUnavailable, "inventory", fmt.Errorf("provider could not find bucket %q: %w", bucket, err))
		case "notfound":
			return providerError(storage.ErrorUnavailable, "inventory", fmt.Errorf("provider could not resolve bucket inventory target %q: %w", bucket, err))
		}
	}
	return providerError(storage.ErrorProvider, "inventory", fmt.Errorf("list s3 objects for %s/%s: %w", bucket, strings.Trim(strings.TrimSpace(prefix), "/"), err))
}

func storageLoggingEnabled(ctx context.Context) bool {
	value, _ := ctx.Value(storageLoggingContextKey{}).(bool)
	return value
}

type storageLoggingContextKey struct{}
