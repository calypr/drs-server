package core

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/smithy-go"
)

func TestClassifyProbeErrorCredentialMissingIsNotBucketMiss(t *testing.T) {
	status, kind := classifyStorageProbeError(&StorageInspectError{
		Kind:    StorageInspectCredentialMissing,
		Message: `no stored bucket credential found for bucket "missing"`,
	})
	if status != StorageProbeStatusInvalid || kind != "credential_missing" {
		t.Fatalf("expected credential_missing to classify as invalid status, got status=%q kind=%q", status, kind)
	}
}

func TestClassifyProbeErrorObjectNotFoundRemainsBucketMiss(t *testing.T) {
	status, kind := classifyStorageProbeError(&StorageInspectError{
		Kind:    StorageInspectObjectNotFound,
		Message: "provider could not find s3://bucket/key",
	})
	if status != StorageProbeStatusNotFound || kind != "object_not_found" {
		t.Fatalf("expected object_not_found to classify as not_found, got status=%q kind=%q", status, kind)
	}
}

type fakeS3HeadClient struct {
	calls atomic.Int32
}

func (client *fakeS3HeadClient) HeadObject(_ context.Context, _ *awss3.HeadObjectInput, _ ...func(*awss3.Options)) (*awss3.HeadObjectOutput, error) {
	if client.calls.Add(1) == 1 {
		return nil, &smithy.GenericAPIError{Code: "SlowDown", Message: "retry"}
	}
	return &awss3.HeadObjectOutput{ContentLength: aws.Int64(42)}, nil
}

func TestHeadS3ObjectWithRetryRecoversTransientFailure(t *testing.T) {
	restore := disableS3ListRetrySleep(t)
	defer restore()
	t.Setenv(envS3HeadMaxAttempts, "2")
	client := &fakeS3HeadClient{}

	out, err := headS3ObjectWithRetry(context.Background(), client, "bucket", "key")
	if err != nil {
		t.Fatalf("head object: %v", err)
	}
	if out == nil || aws.ToInt64(out.ContentLength) != 42 || client.calls.Load() != 2 {
		t.Fatalf("expected retry then successful HEAD, output=%+v calls=%d", out, client.calls.Load())
	}
}

func TestS3ProbeLimiterSharesCapacityAcrossContexts(t *testing.T) {
	limiter := newS3ProbeLimiter(1)
	ctx := withS3ProbeLimiter(context.Background(), limiter)
	release, err := acquireS3Probe(ctx, "head", "bucket", "first")
	if err != nil {
		t.Fatalf("acquire first permit: %v", err)
	}

	acquired := make(chan struct{})
	go func() {
		secondRelease, err := acquireS3Probe(ctx, "list", "bucket", "second")
		if err == nil {
			secondRelease()
			close(acquired)
		}
	}()
	select {
	case <-acquired:
		t.Fatal("second operation acquired before the first released its permit")
	case <-time.After(20 * time.Millisecond):
	}
	release()
	select {
	case <-acquired:
	case <-time.After(time.Second):
		t.Fatal("second operation did not acquire after release")
	}
}
