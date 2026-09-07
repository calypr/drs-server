package s3

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/smithy-go"

	"github.com/calypr/syfon/internal/storage"
)

func TestHeadRetryRetriesTransientErrorAndReturnsMetadata(t *testing.T) {
	restore := noRetrySleep(t)
	defer restore()
	t.Setenv(envHeadMaxAttempts, "3")
	t.Setenv(envListPageMaxAttempts, "3")
	lastModified := time.Unix(200, 0).UTC()
	client := &fakeClient{
		headErrs: []error{
			&smithy.GenericAPIError{Code: "SlowDown", Message: "try again"},
			&smithy.GenericAPIError{Code: "SlowDown", Message: "try again"},
		},
		headOutput: &awss3.HeadObjectOutput{
			ContentLength: aws.Int64(42),
			ETag:          aws.String("\"quoted-etag\""),
			LastModified:  &lastModified,
		},
	}
	provider := cachedBackend(client, &fakePresigner{})
	metadata, err := provider.probeOne(context.Background(), objectTarget("bucket", "key"))
	if err != nil {
		t.Fatalf("probe: %v", err)
	}
	if got := len(client.headInputs); got != 3 {
		t.Fatalf("HEAD call count = %d, want 3", got)
	}
	if metadata.SizeBytes != 42 || metadata.ETag != "quoted-etag" || !metadata.LastModified.Equal(lastModified) {
		t.Fatalf("metadata = %#v", metadata)
	}
}

func TestHeadAndListShareOnePermitAndCancellationIsReleasable(t *testing.T) {
	t.Setenv(envHeadMaxAttempts, "1")
	provider := newBackend(nil)
	provider.limiter = newProbeLimiter(1)
	started := make(chan struct{})
	unblock := make(chan struct{})
	client := &blockingProbeClient{started: started, unblock: unblock}

	headDone := make(chan error, 1)
	go func() {
		_, err := provider.headWithRetry(context.Background(), client, "bucket", "head")
		headDone <- err
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("HEAD did not acquire the permit")
	}

	listContext, cancel := context.WithCancel(context.Background())
	listDone := make(chan error, 1)
	go func() {
		_, _, _, err := provider.listPageWithRetry(listContext, client, &awss3.ListObjectsV2Input{Bucket: aws.String("bucket")}, "", "bucket", "", "", listPageRetryPolicy{MaxAttempts: 1}, 1, 0, "", false)
		listDone <- err
	}()
	cancel()
	select {
	case err := <-listDone:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("blocked LIST error = %v, want cancellation", err)
		}
	case <-time.After(time.Second):
		t.Fatal("blocked LIST did not observe cancellation")
	}

	close(unblock)
	if err := <-headDone; err != nil {
		t.Fatalf("HEAD after release: %v", err)
	}
	if client.listCalls != 0 {
		t.Fatalf("LIST SDK call happened despite canceled permit: %d", client.listCalls)
	}
}

func TestHeadReleasesPermitAfterProviderError(t *testing.T) {
	t.Setenv(envHeadMaxAttempts, "1")
	provider := newBackend(nil)
	provider.limiter = newProbeLimiter(1)
	client := &fakeClient{headErrs: []error{&smithy.GenericAPIError{Code: "AccessDenied", Message: "no"}}, headOutput: &awss3.HeadObjectOutput{}}
	if _, err := provider.headWithRetry(context.Background(), client, "bucket", "key"); err == nil {
		t.Fatal("expected first HEAD error")
	}
	if _, err := provider.headWithRetry(context.Background(), client, "bucket", "key"); err != nil {
		t.Fatalf("second HEAD blocked after first error: %v", err)
	}
}

type blockingProbeClient struct {
	mu          sync.Mutex
	started     chan struct{}
	unblock     chan struct{}
	listCalls   int
	startedOnce sync.Once
}

func (client *blockingProbeClient) HeadObject(context.Context, *awss3.HeadObjectInput, ...func(*awss3.Options)) (*awss3.HeadObjectOutput, error) {
	client.startedOnce.Do(func() { close(client.started) })
	<-client.unblock
	return &awss3.HeadObjectOutput{}, nil
}

func (client *blockingProbeClient) ListObjectsV2(_ context.Context, _ *awss3.ListObjectsV2Input, _ ...func(*awss3.Options)) (*awss3.ListObjectsV2Output, error) {
	client.mu.Lock()
	client.listCalls++
	client.mu.Unlock()
	return &awss3.ListObjectsV2Output{}, nil
}

func objectTarget(bucket, key string) storage.ObjectTarget {
	return storage.ObjectTarget{Bucket: bucket, Key: key}
}
