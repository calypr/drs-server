package s3

import (
	"context"
	"errors"
	"fmt"
	"path"
	"strings"
	"sync"

	"github.com/aws/aws-sdk-go-v2/aws"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/smithy-go"

	"github.com/calypr/syfon/internal/storage"
	"github.com/calypr/syfon/internal/storage/address"
)

const (
	defaultHeadMaxAttempts = 3
	envHeadMaxAttempts     = "SYFON_S3_HEAD_MAX_ATTEMPTS"
	maxProbeWorkers        = 8
)

func (s *backend) Probe(ctx context.Context, targets []storage.ProbeTarget) []storage.ProbeResult {
	if len(targets) == 0 {
		return nil
	}
	results := make([]storage.ProbeResult, len(targets))
	for index, target := range targets {
		results[index] = storage.ProbeResult{ID: target.ID, Target: target.Target}
	}

	workers := len(targets)
	if workers > maxProbeWorkers {
		workers = maxProbeWorkers
	}
	work := make(chan int)
	var wait sync.WaitGroup
	for worker := 0; worker < workers; worker++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			for index := range work {
				results[index].Metadata, results[index].Err = s.probeOne(ctx, targets[index].Target)
			}
		}()
	}
	for index := range targets {
		work <- index
	}
	close(work)
	wait.Wait()
	return results
}

func (s *backend) probeOne(ctx context.Context, target storage.ObjectTarget) (storage.ObjectMetadata, error) {
	clients, err := s.getClients(ctx, target.Bucket)
	if err != nil {
		return storage.ObjectMetadata{}, providerError(storage.ErrorProvider, "probe", err)
	}
	output, err := s.headWithRetry(ctx, clients.client, target.Bucket, target.Key)
	if err != nil {
		return storage.ObjectMetadata{}, classifyHeadError(target, err)
	}
	return metadataFromHead(target, output), nil
}

func (s *backend) headWithRetry(ctx context.Context, client s3HeadClient, bucket, key string) (*awss3.HeadObjectOutput, error) {
	policy := listPageRetryPolicyFromEnv()
	maxAttempts := intEnvOrDefault(envHeadMaxAttempts, defaultHeadMaxAttempts, 1)
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		release, acquireErr := s.acquireProbe(ctx, "head", bucket, key)
		if acquireErr != nil {
			return nil, acquireErr
		}
		output, err := client.HeadObject(ctx, &awss3.HeadObjectInput{
			Bucket: aws.String(bucket),
			Key:    aws.String(key),
		})
		release()
		if err == nil || !isRetryableListPageError(err) || attempt == maxAttempts {
			return output, err
		}
		backoff := policy.backoff(attempt)
		if err := sleepListPageRetry(ctx, backoff); err != nil {
			return nil, err
		}
	}
	return nil, nil
}

type s3HeadClient interface {
	HeadObject(context.Context, *awss3.HeadObjectInput, ...func(*awss3.Options)) (*awss3.HeadObjectOutput, error)
}

func metadataFromHead(target storage.ObjectTarget, output *awss3.HeadObjectOutput) storage.ObjectMetadata {
	metadata := storage.ObjectMetadata{
		Provider: address.S3Provider,
		Bucket:   target.Bucket,
		Key:      target.Key,
		Path:     path.Base(target.Key),
	}
	if output == nil {
		return metadata
	}
	if output.ContentLength != nil {
		metadata.SizeBytes = *output.ContentLength
	}
	if output.LastModified != nil {
		metadata.LastModified = *output.LastModified
	}
	metadata.ETag = strings.Trim(strings.TrimSpace(aws.ToString(output.ETag)), "\"")
	return metadata
}

func classifyHeadError(target storage.ObjectTarget, err error) error {
	var apiErr smithy.APIError
	if errors.As(err, &apiErr) {
		switch strings.ToLower(strings.TrimSpace(apiErr.ErrorCode())) {
		case "forbidden", "accessdenied", "permissiondenied":
			return providerError(storage.ErrorUnavailable, "probe", fmt.Errorf("provider rejected object probe for s3://%s/%s; mapped bucket target may be missing or inaccessible: %w", target.Bucket, target.Key, err))
		case "notfound", "nosuchkey":
			return providerError(storage.ErrorNotFound, "probe", fmt.Errorf("provider could not find s3://%s/%s: %w", target.Bucket, target.Key, err))
		case "nosuchbucket":
			return providerError(storage.ErrorUnavailable, "probe", fmt.Errorf("provider could not find bucket %q: %w", target.Bucket, err))
		}
	}
	return providerError(storage.ErrorProvider, "probe", fmt.Errorf("inspect s3 object %s/%s: %w", target.Bucket, target.Key, err))
}

func providerError(kind storage.ErrorKind, capability string, cause error) error {
	return &storage.OperationError{Kind: kind, Provider: address.S3Provider, Capability: capability, Cause: cause}
}
