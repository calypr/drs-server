package s3

import (
	"context"
	"errors"
	"net/http"
	"reflect"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/aws/signer/v4"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"

	"github.com/calypr/syfon/internal/buckets"
	"github.com/calypr/syfon/internal/storage"
)

type fakeClient struct {
	createOutput  *awss3.CreateMultipartUploadOutput
	createErr     error
	completeInput *awss3.CompleteMultipartUploadInput
	completeErr   error
	headOutput    *awss3.HeadObjectOutput
	headErrs      []error
	headInputs    []*awss3.HeadObjectInput
	listOutputs   []*awss3.ListObjectsV2Output
	listErrs      []error
	listInputs    []*awss3.ListObjectsV2Input
	deleteInputs  []*awss3.DeleteObjectInput
	deleteObjects []*awss3.DeleteObjectsInput
	deleteOutput  *awss3.DeleteObjectsOutput
	deleteErr     error
}

func (f *fakeClient) CreateMultipartUpload(context.Context, *awss3.CreateMultipartUploadInput, ...func(*awss3.Options)) (*awss3.CreateMultipartUploadOutput, error) {
	return f.createOutput, f.createErr
}

func (f *fakeClient) CompleteMultipartUpload(_ context.Context, input *awss3.CompleteMultipartUploadInput, _ ...func(*awss3.Options)) (*awss3.CompleteMultipartUploadOutput, error) {
	f.completeInput = input
	return nil, f.completeErr
}

func (f *fakeClient) HeadObject(_ context.Context, input *awss3.HeadObjectInput, _ ...func(*awss3.Options)) (*awss3.HeadObjectOutput, error) {
	f.headInputs = append(f.headInputs, input)
	if len(f.headErrs) > 0 {
		err := f.headErrs[0]
		f.headErrs = f.headErrs[1:]
		return nil, err
	}
	return f.headOutput, nil
}

func (f *fakeClient) ListObjectsV2(_ context.Context, input *awss3.ListObjectsV2Input, _ ...func(*awss3.Options)) (*awss3.ListObjectsV2Output, error) {
	f.listInputs = append(f.listInputs, input)
	index := len(f.listInputs) - 1
	if index < len(f.listErrs) && f.listErrs[index] != nil {
		return nil, f.listErrs[index]
	}
	if index >= len(f.listOutputs) {
		return nil, errors.New("unexpected list call")
	}
	return f.listOutputs[index], nil
}

func (f *fakeClient) DeleteObject(_ context.Context, input *awss3.DeleteObjectInput, _ ...func(*awss3.Options)) (*awss3.DeleteObjectOutput, error) {
	f.deleteInputs = append(f.deleteInputs, input)
	return nil, f.deleteErr
}

func (f *fakeClient) DeleteObjects(_ context.Context, input *awss3.DeleteObjectsInput, _ ...func(*awss3.Options)) (*awss3.DeleteObjectsOutput, error) {
	f.deleteObjects = append(f.deleteObjects, input)
	return f.deleteOutput, f.deleteErr
}

type fakePresigner struct {
	getInput    *awss3.GetObjectInput
	getExpires  time.Duration
	putInput    *awss3.PutObjectInput
	putExpires  time.Duration
	partInput   *awss3.UploadPartInput
	partExpires time.Duration
	getURL      string
	putURL      string
	partURL     string
}

func (f *fakePresigner) PresignGetObject(_ context.Context, input *awss3.GetObjectInput, options ...func(*awss3.PresignOptions)) (*v4.PresignedHTTPRequest, error) {
	f.getInput = input
	var presign awss3.PresignOptions
	for _, option := range options {
		option(&presign)
	}
	f.getExpires = presign.Expires
	return &v4.PresignedHTTPRequest{URL: f.getURL}, nil
}

func (f *fakePresigner) PresignPutObject(_ context.Context, input *awss3.PutObjectInput, options ...func(*awss3.PresignOptions)) (*v4.PresignedHTTPRequest, error) {
	f.putInput = input
	var presign awss3.PresignOptions
	for _, option := range options {
		option(&presign)
	}
	f.putExpires = presign.Expires
	return &v4.PresignedHTTPRequest{URL: f.putURL}, nil
}

func (f *fakePresigner) PresignUploadPart(_ context.Context, input *awss3.UploadPartInput, options ...func(*awss3.PresignOptions)) (*v4.PresignedHTTPRequest, error) {
	f.partInput = input
	var presign awss3.PresignOptions
	for _, option := range options {
		option(&presign)
	}
	f.partExpires = presign.Expires
	return &v4.PresignedHTTPRequest{URL: f.partURL}, nil
}

func cachedBackend(client *fakeClient, presigner *fakePresigner) *backend {
	provider := newBackend(nil)
	provider.cache.Store("bucket", &clients{client: client, presigner: presigner})
	return provider
}

func TestAccessPreservesS3MethodRangeExpiryAndDisposition(t *testing.T) {
	client := &fakeClient{}
	presigner := &fakePresigner{getURL: "get", putURL: "put"}
	provider := cachedBackend(client, presigner)
	ctx := context.Background()

	if got, err := provider.SignURL(ctx, storage.ObjectTarget{Bucket: "bucket", Key: "key"}, storage.AccessOptions{Method: "put", ExpiresIn: 7 * time.Minute, DownloadFilename: "dir/name.txt"}); err != nil || got.Location != "get" {
		t.Fatalf("lowercase put = %#v, %v; want GET URL", got, err)
	}
	if presigner.getInput == nil || aws.ToString(presigner.getInput.ResponseContentDisposition) == "" {
		t.Fatalf("GET did not carry response content disposition: %#v", presigner.getInput)
	}
	if presigner.getExpires != 7*time.Minute || aws.ToString(presigner.getInput.Bucket) != "bucket" || aws.ToString(presigner.getInput.Key) != "key" {
		t.Fatalf("GET inputs = %#v expiry=%s", presigner.getInput, presigner.getExpires)
	}
	if got, err := provider.SignURL(ctx, storage.ObjectTarget{Bucket: "bucket", Key: "key"}, storage.AccessOptions{Method: http.MethodPut}); err != nil || got.Location != "put" {
		t.Fatalf("PUT = %#v, %v", got, err)
	}
	if presigner.putExpires != defaultExpiry {
		t.Fatalf("PUT expiry = %s, want %s", presigner.putExpires, defaultExpiry)
	}
	if _, err := provider.SignDownloadPart(ctx, storage.ObjectTarget{Bucket: "bucket", Key: "key"}, storage.ByteRange{Start: -2, End: 9}, storage.AccessOptions{DownloadFilename: "x"}); err != nil {
		t.Fatal(err)
	}
	if got := aws.ToString(presigner.getInput.Range); got != "bytes=-2-9" {
		t.Fatalf("range = %q", got)
	}
}

func TestMultipartPreservesOpaqueIDETagsAndCallerOrder(t *testing.T) {
	client := &fakeClient{createOutput: &awss3.CreateMultipartUploadOutput{UploadId: aws.String("opaque")}}
	presigner := &fakePresigner{partURL: "part"}
	provider := cachedBackend(client, presigner)
	target := storage.ObjectTarget{Bucket: "bucket", Key: "key"}
	if got, err := provider.InitMultipartUpload(context.Background(), target); err != nil || got != "opaque" {
		t.Fatalf("init = %q, %v", got, err)
	}
	if _, err := provider.SignMultipartPart(context.Background(), storage.MultipartPartRequest{Target: target, UploadID: "opaque", PartNumber: 4}); err != nil {
		t.Fatal(err)
	}
	if aws.ToInt32(presigner.partInput.PartNumber) != 4 || aws.ToString(presigner.partInput.UploadId) != "opaque" {
		t.Fatalf("part input = %#v", presigner.partInput)
	}
	request := storage.CompleteMultipartRequest{Target: target, UploadID: "opaque", Parts: []storage.CompletedPart{{PartNumber: 4, ETag: "\"four\""}, {PartNumber: 1, ETag: "\"one\""}}}
	if err := provider.CompleteMultipartUpload(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	if got := client.completeInput.MultipartUpload.Parts; len(got) != 2 || aws.ToInt32(got[0].PartNumber) != 4 || aws.ToString(got[0].ETag) != "\"four\"" || aws.ToInt32(got[1].PartNumber) != 1 {
		t.Fatalf("completed parts = %#v", got)
	}
}

func TestProbeInventoryAndDeleteUseSDKFakes(t *testing.T) {
	lastModified := time.Unix(100, 0).UTC()
	client := &fakeClient{
		headOutput: &awss3.HeadObjectOutput{ContentLength: aws.Int64(12), ETag: aws.String("\"etag\""), LastModified: &lastModified},
		listOutputs: []*awss3.ListObjectsV2Output{
			{IsTruncated: aws.Bool(true), NextContinuationToken: aws.String("next"), Contents: []types.Object{{Key: aws.String("prefix/b"), Size: aws.Int64(2)}}},
			{Contents: []types.Object{{Key: aws.String("prefix/a"), Size: aws.Int64(1)}}},
			{Contents: []types.Object{{Key: aws.String("prefix/a"), Size: aws.Int64(1)}}},
			{Contents: []types.Object{{Key: aws.String("prefix/a"), Size: aws.Int64(1)}}},
		},
	}
	provider := cachedBackend(client, &fakePresigner{})
	probe := provider.Probe(context.Background(), []storage.ProbeTarget{{ID: "first", Target: storage.ObjectTarget{Bucket: "bucket", Key: "x"}}})
	if len(probe) != 1 || probe[0].Err != nil || probe[0].Metadata.ETag != "etag" || probe[0].Metadata.SizeBytes != 12 || !probe[0].Metadata.LastModified.Equal(lastModified) {
		t.Fatalf("probe = %#v", probe)
	}
	result, err := provider.Inventory(context.Background(), storage.InventoryRequest{Target: storage.PrefixTarget{Bucket: "bucket", Prefix: "prefix"}})
	if err != nil || !result.Complete || len(result.Items) != 2 || result.Items[0].Key != "prefix/b" || result.Items[1].Key != "prefix/a" {
		t.Fatalf("inventory = %#v, %v", result, err)
	}
	if len(client.listInputs) != 4 || aws.ToString(client.listInputs[1].ContinuationToken) != "next" {
		t.Fatalf("list calls = %d inputs=%#v", len(client.listInputs), client.listInputs)
	}
	if err := provider.Delete(context.Background(), []storage.PhysicalTarget{{Bucket: "bucket", Key: "z"}, {Bucket: "bucket", Key: "a"}, {Bucket: "bucket", Key: "z"}}); err != nil {
		t.Fatal(err)
	}
	if len(client.deleteObjects) != 1 || len(client.deleteObjects[0].Delete.Objects) != 2 || aws.ToString(client.deleteObjects[0].Delete.Objects[0].Key) != "a" || !aws.ToBool(client.deleteObjects[0].Delete.Quiet) {
		t.Fatalf("bulk delete = %#v", client.deleteObjects)
	}
}

func TestProbeClassifiesProviderErrors(t *testing.T) {
	provider := cachedBackend(&fakeClient{headErrs: []error{&smithy.GenericAPIError{Code: "NoSuchKey", Message: "gone"}}}, &fakePresigner{})
	result := provider.Probe(context.Background(), []storage.ProbeTarget{{Target: storage.ObjectTarget{Bucket: "bucket", Key: "missing"}}})
	var operation *storage.OperationError
	if len(result) != 1 || !errors.As(result[0].Err, &operation) || operation.Kind != storage.ErrorNotFound || operation.Provider != "s3" {
		t.Fatalf("probe error = %#v", result)
	}
}

func TestTerminalReplayFingerprintPreservesTokenOmission(t *testing.T) {
	page := &awss3.ListObjectsV2Output{IsTruncated: aws.Bool(false), NextContinuationToken: aws.String("token-one"), Contents: []types.Object{{Key: aws.String("a")}}}
	replay := &awss3.ListObjectsV2Output{IsTruncated: aws.Bool(false), NextContinuationToken: aws.String("token-two"), Contents: []types.Object{{Key: aws.String("a")}}}
	if listPageFingerprint(page) != listPageFingerprint(replay) {
		t.Fatal("terminal replay identity unexpectedly includes continuation token")
	}
}

func TestNewExposesStorageRegistration(t *testing.T) {
	var lookup storage.CredentialLookup = credentialLookupFunc(func(context.Context, string) (*buckets.Credential, error) { return nil, nil })
	registration := New(lookup)
	if reflect.ValueOf(registration).IsZero() {
		t.Fatal("empty registration")
	}
}

func TestGetClientsNormalizesEndpointWhitespace(t *testing.T) {
	provider := newBackend(credentialLookupFunc(func(context.Context, string) (*buckets.Credential, error) {
		return &buckets.Credential{
			Region:    "us-east-1",
			AccessKey: "access",
			SecretKey: "secret",
			Endpoint:  "  localhost:9000  ",
		}, nil
	}))

	result, err := provider.getClients(context.Background(), "bucket")
	if err != nil {
		t.Fatalf("get clients: %v", err)
	}
	client, ok := result.client.(*awss3.Client)
	if !ok {
		t.Fatalf("client type = %T, want *s3.Client", result.client)
	}
	options := client.Options()
	if options.BaseEndpoint == nil || *options.BaseEndpoint != "http://localhost:9000" {
		if options.BaseEndpoint == nil {
			t.Fatal("BaseEndpoint = nil, want http://localhost:9000")
		}
		t.Fatalf("BaseEndpoint = %q, want http://localhost:9000", *options.BaseEndpoint)
	}
	if !options.UsePathStyle {
		t.Fatal("UsePathStyle = false, want true for custom endpoint")
	}
}

type credentialLookupFunc func(context.Context, string) (*buckets.Credential, error)

func (f credentialLookupFunc) GetS3Credential(ctx context.Context, bucket string) (*buckets.Credential, error) {
	return f(ctx, bucket)
}
