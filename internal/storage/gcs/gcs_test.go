package gcs

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"cloud.google.com/go/storage"
	"google.golang.org/api/option"

	"github.com/calypr/syfon/internal/buckets"
	storageports "github.com/calypr/syfon/internal/storage"
)

type roundTripperFunc func(*http.Request) *http.Response

type credentialLookupFunc func(context.Context, string) (*buckets.Credential, error)

func (f credentialLookupFunc) GetS3Credential(ctx context.Context, bucket string) (*buckets.Credential, error) {
	return f(ctx, bucket)
}

func (f roundTripperFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request), nil
}

func testClient(ctx context.Context, transport roundTripperFunc) (*storage.Client, error) {
	return storage.NewClient(ctx,
		option.WithEndpoint("http://storage.test"),
		option.WithoutAuthentication(),
		option.WithHTTPClient(&http.Client{Transport: transport}),
	)
}

func responseFor(request *http.Request, status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Status:     http.StatusText(status),
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
		Request:    request,
	}
}

func TestEndpointAccessPreservesPathAndOmitsRange(t *testing.T) {
	binding := storageports.ProviderBinding{Provider: "gcs", LookupKey: "test-bucket", PhysicalBucket: "test-bucket", Credential: &buckets.Credential{Endpoint: "http://localhost:4443"}}
	b := &backend{}

	access, err := b.Sign(context.Background(), binding, storageports.SignRequest{Target: storageports.Target{PhysicalBucket: "test-bucket", Key: "nested/file.txt"}, DownloadFilename: "report.txt"})
	if err != nil {
		t.Fatalf("SignURL returned error: %v", err)
	}
	parsed, err := url.Parse(access.Location)
	if err != nil {
		t.Fatalf("parse endpoint URL: %v", err)
	}
	if got, want := parsed.Path, "/storage/v1/b/test-bucket/o/nested/file.txt"; got != want {
		t.Fatalf("endpoint object path = %q, want %q", got, want)
	}
	if got, want := parsed.RawPath, "/storage/v1/b/test-bucket/o/nested%2Ffile.txt"; got != want {
		t.Fatalf("endpoint raw object path = %q, want %q", got, want)
	}
	if got, want := parsed.EscapedPath(), "/storage/v1/b/test-bucket/o/nested%2Ffile.txt"; got != want {
		t.Fatalf("endpoint escaped object path = %q, want %q", got, want)
	}
	if strings.Contains(access.Location, "%252F") {
		t.Fatalf("endpoint object URL double-escaped nested key: %s", access.Location)
	}
	if got := parsed.Query().Get("alt"); got != "media" {
		t.Fatalf("endpoint alt query = %q, want media", got)
	}
	if !strings.Contains(parsed.Query().Get("response-content-disposition"), "report.txt") {
		t.Fatalf("endpoint content disposition = %q", parsed.Query().Get("response-content-disposition"))
	}

	ranged, err := b.Sign(context.Background(), binding, storageports.SignRequest{Target: storageports.Target{PhysicalBucket: "test-bucket", Key: "nested/file.txt"}, Range: &storageports.ByteRange{Start: 5, End: 12}})
	if err != nil {
		t.Fatalf("SignDownloadPart returned error: %v", err)
	}
	rangedURL, err := url.Parse(ranged.Location)
	if err != nil {
		t.Fatalf("parse ranged endpoint URL: %v", err)
	}
	if got := rangedURL.Query().Get("range"); got != "" {
		t.Fatalf("endpoint range query = %q, want omitted", got)
	}

	part, err := b.SignMultipartPart(context.Background(), binding, storageports.MultipartPartRequest{
		Target: storageports.Target{PhysicalBucket: "test-bucket", Key: "nested/file.txt"}, UploadID: "upload", PartNumber: 4,
	})
	if err != nil {
		t.Fatalf("SignMultipartPart returned error: %v", err)
	}
	partURL, err := url.Parse(part.Location)
	if err != nil {
		t.Fatalf("parse endpoint part URL: %v", err)
	}
	if got, want := partURL.Path, "/upload/storage/v1/b/test-bucket/o"; got != want {
		t.Fatalf("endpoint part path = %q, want %q", got, want)
	}
	if got := partURL.Query().Get("uploadType"); got != "media" {
		t.Fatalf("endpoint part uploadType = %q, want media", got)
	}
	if got := partURL.Query().Get("name"); got != ".syfon-multipart/upload/nested/file.txt/parts/4" {
		t.Fatalf("endpoint part name = %q", got)
	}
}

func TestNewClientFallsBackToApplicationDefaultCredentials(t *testing.T) {
	credentialsPath := t.TempDir() + "/application-default-credentials.json"
	credentials := `{"type":"authorized_user","client_id":"test-client","client_secret":"test-secret","refresh_token":"test-token"}`
	if err := os.WriteFile(credentialsPath, []byte(credentials), 0o600); err != nil {
		t.Fatalf("write application default credentials: %v", err)
	}
	t.Setenv("GOOGLE_APPLICATION_CREDENTIALS", credentialsPath)

	client, err := newClient(context.Background(), &buckets.Credential{
		AccessKey: "split-form@example.test",
		SecretKey: "not-service-account-json",
	})
	if err != nil {
		t.Fatalf("newClient returned error for ADC credentials: %v", err)
	}
	if client == nil {
		t.Fatal("newClient returned a nil client")
	}
	if err := client.Close(); err != nil {
		t.Fatalf("close ADC client: %v", err)
	}
}

func TestNativeRangedAccessUsesV4RangeSignature(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 1024)
	if err != nil {
		t.Fatalf("generate RSA key: %v", err)
	}
	privateKey := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: marshalPKCS8PrivateKeyMust(key)})
	b := &backend{}
	binding := storageports.ProviderBinding{LookupKey: "bucket", PhysicalBucket: "bucket", Credential: &buckets.Credential{
		AccessKey: "service-account@example.test", SecretKey: string(privateKey),
	}}

	access, err := b.Sign(context.Background(), binding, storageports.SignRequest{Target: storageports.Target{PhysicalBucket: "bucket", Key: "object"}, Range: &storageports.ByteRange{Start: 10, End: 19}})
	if err != nil {
		t.Fatalf("Sign returned error: %v", err)
	}
	parsed, err := url.Parse(access.Location)
	if err != nil {
		t.Fatalf("parse signed URL: %v", err)
	}
	if got, want := parsed.Query().Get("X-Goog-Algorithm"), "GOOG4-RSA-SHA256"; got != want {
		t.Fatalf("signed algorithm = %q, want %q", got, want)
	}
	if got := parsed.Query().Get("X-Goog-SignedHeaders"); !strings.Contains(got, "range") {
		t.Fatalf("signed headers = %q, want range", got)
	}
}

func TestNativeMultipartAccessUsesRequestedExpiry(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 1024)
	if err != nil {
		t.Fatalf("generate RSA key: %v", err)
	}
	privateKey := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: marshalPKCS8PrivateKeyMust(key)})
	b := &backend{}
	binding := storageports.ProviderBinding{LookupKey: "bucket", PhysicalBucket: "bucket", Credential: &buckets.Credential{
		AccessKey: "service-account@example.test", SecretKey: string(privateKey),
	}}

	access, err := b.SignMultipartPart(context.Background(), binding, storageports.MultipartPartRequest{
		Target: storageports.Target{PhysicalBucket: "bucket", Key: "object"}, UploadID: "upload", PartNumber: 1, ExpiresIn: 7 * time.Minute,
	})
	if err != nil {
		t.Fatalf("SignMultipartPart returned error: %v", err)
	}
	parsed, err := url.Parse(access.Location)
	if err != nil {
		t.Fatalf("parse signed URL: %v", err)
	}
	got, err := strconv.Atoi(parsed.Query().Get("X-Goog-Expires"))
	if err != nil || got < 419 || got > 420 {
		t.Fatalf("multipart expiry = %d, want 419 or 420", got)
	}
}

func TestCompleteMultipartSortsComposesInBatchesAndCleansUp(t *testing.T) {
	var composePaths []string
	var deletePaths []string
	transport := roundTripperFunc(func(r *http.Request) *http.Response {
		switch r.Method {
		case http.MethodPost:
			composePaths = append(composePaths, r.URL.Path)
			_, _ = io.ReadAll(r.Body)
			response := responseFor(r, http.StatusOK, `{"name":"composed"}`)
			response.Header.Set("Content-Type", "application/json")
			return response
		case http.MethodDelete:
			deletePaths = append(deletePaths, r.URL.Path)
			return responseFor(r, http.StatusNoContent, "")
		default:
			return responseFor(r, http.StatusNotFound, "")
		}
	})

	previous := newClient
	newClient = func(ctx context.Context, _ *buckets.Credential) (*storage.Client, error) {
		return testClient(ctx, transport)
	}
	defer func() { newClient = previous }()

	b := &backend{}
	binding := storageports.ProviderBinding{LookupKey: "bucket", PhysicalBucket: "bucket", Credential: &buckets.Credential{Bucket: "bucket"}}
	parts := make([]storageports.CompletedPart, 33)
	for i := range parts {
		parts[i] = storageports.CompletedPart{PartNumber: int32(len(parts) - i), ETag: "ignored"}
	}
	if err := b.CompleteMultipart(context.Background(), binding, storageports.CompleteMultipartRequest{
		Target: storageports.Target{PhysicalBucket: "bucket", Key: "obj.bin"}, UploadID: "upload", Parts: parts,
	}); err != nil {
		t.Fatalf("CompleteMultipart returned error: %v", err)
	}

	if len(composePaths) != 3 {
		t.Fatalf("compose request count = %d, want 3 (%#v)", len(composePaths), composePaths)
	}
	if !strings.Contains(composePaths[0], ".syfon-multipart") || !strings.HasSuffix(composePaths[2], "/obj.bin/compose") {
		t.Fatalf("compose paths = %#v", composePaths)
	}
	if len(deletePaths) != 35 {
		t.Fatalf("cleanup request count = %d, want 35", len(deletePaths))
	}
	if !strings.Contains(deletePaths[len(deletePaths)-1], ".syfon-multipart") {
		t.Fatalf("temporary compose object was not deleted last: %q", deletePaths[len(deletePaths)-1])
	}
	if client, ok := b.cache.Load("bucket"); ok {
		_ = client.(*storage.Client).Close()
	}
}

func TestCompleteMultipartWritesMarkerOnFinalCompose(t *testing.T) {
	var composeBody []byte
	transport := roundTripperFunc(func(r *http.Request) *http.Response {
		switch r.Method {
		case http.MethodGet:
			return responseFor(r, http.StatusNotFound, "")
		case http.MethodPost:
			composeBody, _ = io.ReadAll(r.Body)
			response := responseFor(r, http.StatusOK, `{"name":"composed"}`)
			response.Header.Set("Content-Type", "application/json")
			return response
		case http.MethodDelete:
			return responseFor(r, http.StatusNoContent, "")
		default:
			return responseFor(r, http.StatusNotFound, "")
		}
	})
	previous := newClient
	newClient = func(ctx context.Context, _ *buckets.Credential) (*storage.Client, error) {
		return testClient(ctx, transport)
	}
	defer func() { newClient = previous }()

	b := &backend{}
	binding := storageports.ProviderBinding{LookupKey: "bucket", PhysicalBucket: "bucket", Credential: &buckets.Credential{Bucket: "bucket"}}
	err := b.CompleteMultipart(context.Background(), binding, storageports.CompleteMultipartRequest{
		Target:       storageports.Target{PhysicalBucket: "bucket", Key: "object.bin"},
		UploadID:     "upload",
		CompletionID: "completion",
		Parts:        []storageports.CompletedPart{{PartNumber: 1}},
	})
	if err != nil {
		t.Fatalf("CompleteMultipart returned error: %v", err)
	}
	if !strings.Contains(string(composeBody), `"syfon-multipart-id":"completion"`) {
		t.Fatalf("final compose body = %s, want completion marker", composeBody)
	}
	if client, ok := b.cache.Load("bucket"); ok {
		_ = client.(*storage.Client).Close()
	}
}

func TestCompleteMultipartSkipsProviderWhenMarkerMatches(t *testing.T) {
	var posts int
	transport := roundTripperFunc(func(r *http.Request) *http.Response {
		if r.Method == http.MethodGet {
			return responseFor(r, http.StatusOK, `{"metadata":{"syfon-multipart-id":"completion"}}`)
		}
		if r.Method == http.MethodPost {
			posts++
		}
		if r.Method == http.MethodDelete {
			return responseFor(r, http.StatusNotFound, "")
		}
		return responseFor(r, http.StatusInternalServerError, "unexpected provider finalize")
	})
	previous := newClient
	newClient = func(ctx context.Context, _ *buckets.Credential) (*storage.Client, error) {
		return testClient(ctx, transport)
	}
	defer func() { newClient = previous }()

	b := &backend{}
	binding := storageports.ProviderBinding{LookupKey: "bucket", PhysicalBucket: "bucket", Credential: &buckets.Credential{Bucket: "bucket"}}
	if err := b.CompleteMultipart(context.Background(), binding, storageports.CompleteMultipartRequest{
		Target:       storageports.Target{PhysicalBucket: "bucket", Key: "object.bin"},
		UploadID:     "upload",
		CompletionID: "completion",
		Parts:        []storageports.CompletedPart{{PartNumber: 1}},
	}); err != nil {
		t.Fatalf("CompleteMultipart returned error: %v", err)
	}
	if posts != 0 {
		t.Fatalf("provider finalize requests = %d, want 0 after matching marker", posts)
	}
	if client, ok := b.cache.Load("bucket"); ok {
		_ = client.(*storage.Client).Close()
	}
}

func TestInvalidateBucketEvictsCachedNativeClientByLookupKey(t *testing.T) {
	previous := newClient
	newClient = func(ctx context.Context, _ *buckets.Credential) (*storage.Client, error) {
		return testClient(ctx, func(r *http.Request) *http.Response {
			return responseFor(r, http.StatusNotFound, "")
		})
	}
	defer func() { newClient = previous }()

	b := &backend{}
	binding := storageports.ProviderBinding{LookupKey: "bucket", PhysicalBucket: "bucket", Credential: &buckets.Credential{Bucket: "bucket"}}
	first, err := b.getClient(context.Background(), binding)
	if err != nil {
		t.Fatalf("first getClient returned error: %v", err)
	}
	second, err := b.getClient(context.Background(), binding)
	if err != nil {
		t.Fatalf("second getClient returned error: %v", err)
	}
	if first != second {
		t.Fatal("cache did not return the same client")
	}
	b.InvalidateBucket(" bucket ")
	third, err := b.getClient(context.Background(), binding)
	if err != nil {
		t.Fatalf("evicted getClient returned error: %v", err)
	}
	if third == first {
		t.Fatal("invalidation did not evict cached client")
	}
	_ = first.Close()
	_ = third.Close()
}

func TestInvalidateCannotPublishOldConfiguration(t *testing.T) {
	previous := newClient
	entered := make(chan struct{})
	release := make(chan struct{})
	var calls int
	newClient = func(ctx context.Context, _ *buckets.Credential) (*storage.Client, error) {
		calls++
		if calls == 1 {
			close(entered)
			<-release
		}
		return testClient(ctx, func(r *http.Request) *http.Response { return responseFor(r, http.StatusNotFound, "") })
	}
	defer func() { newClient = previous }()

	b := &backend{}
	oldBinding := storageports.ProviderBinding{LookupKey: "bucket", PhysicalBucket: "bucket", Credential: &buckets.Credential{Bucket: "bucket", SecretKey: "old"}}
	result := make(chan *storage.Client, 1)
	go func() {
		client, err := b.getClient(context.Background(), oldBinding)
		if err != nil {
			t.Errorf("old getClient failed: %v", err)
			return
		}
		result <- client
	}()
	<-entered
	invalidateDone := make(chan struct{})
	go func() {
		b.InvalidateBucket("BUCKET")
		close(invalidateDone)
	}()
	select {
	case <-invalidateDone:
		t.Fatal("invalidation completed before the factory returned")
	case <-time.After(20 * time.Millisecond):
	}
	close(release)
	oldClient := <-result
	<-invalidateDone

	newBinding := oldBinding
	newBinding.Credential = &buckets.Credential{Bucket: "bucket", SecretKey: "new"}
	newClientValue, err := b.getClient(context.Background(), newBinding)
	if err != nil {
		t.Fatalf("new getClient failed: %v", err)
	}
	if newClientValue == oldClient {
		t.Fatal("new configuration reused stale client")
	}
	if err := b.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}
}

func TestMarkerCleanupFailureRetriesWithoutRecomposition(t *testing.T) {
	var posts, deletes int
	published := false
	transport := roundTripperFunc(func(r *http.Request) *http.Response {
		switch r.Method {
		case http.MethodGet:
			if published {
				return responseFor(r, http.StatusOK, `{"metadata":{"syfon-multipart-id":"completion"}}`)
			}
			return responseFor(r, http.StatusNotFound, "")
		case http.MethodPost:
			posts++
			published = true
			return responseFor(r, http.StatusOK, `{"name":"composed"}`)
		case http.MethodDelete:
			deletes++
			if deletes == 1 {
				return responseFor(r, http.StatusInternalServerError, "cleanup failed")
			}
			return responseFor(r, http.StatusNoContent, "")
		default:
			return responseFor(r, http.StatusNotFound, "")
		}
	})
	previous := newClient
	newClient = func(ctx context.Context, _ *buckets.Credential) (*storage.Client, error) {
		return testClient(ctx, transport)
	}
	defer func() { newClient = previous }()

	b := &backend{}
	binding := storageports.ProviderBinding{LookupKey: "bucket", PhysicalBucket: "bucket", Credential: &buckets.Credential{Bucket: "bucket"}}
	request := storageports.CompleteMultipartRequest{
		Target: storageports.Target{PhysicalBucket: "bucket", Key: "object.bin"}, UploadID: "upload", CompletionID: "completion",
		Parts: []storageports.CompletedPart{{PartNumber: 1}},
	}
	if err := b.CompleteMultipart(context.Background(), binding, request); err == nil {
		t.Fatal("first completion succeeded despite cleanup failure")
	}
	if err := b.CompleteMultipart(context.Background(), binding, request); err != nil {
		t.Fatalf("cleanup retry failed: %v", err)
	}
	if posts != 1 {
		t.Fatalf("compose requests = %d, want 1", posts)
	}
	if deletes != 2 {
		t.Fatalf("delete requests = %d, want 2", deletes)
	}
	if err := b.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}
}

func TestDeleteNormalizesNotFoundToIdempotentSuccess(t *testing.T) {
	previous := newClient
	newClient = func(ctx context.Context, _ *buckets.Credential) (*storage.Client, error) {
		return testClient(ctx, func(r *http.Request) *http.Response {
			if r.Method != http.MethodDelete {
				return responseFor(r, http.StatusNotFound, "")
			}
			response := responseFor(r, http.StatusNotFound, `{"error":{"code":404,"message":"not found"}}`)
			response.Header.Set("Content-Type", "application/json")
			return response
		})
	}
	defer func() { newClient = previous }()

	b := &backend{}
	binding := storageports.ProviderBinding{LookupKey: "bucket", PhysicalBucket: "bucket", Credential: &buckets.Credential{Bucket: "bucket"}}
	if err := b.Delete(context.Background(), binding, []storageports.PhysicalTarget{{Provider: "gcs", PhysicalBucket: "bucket", Key: "gone"}}); err != nil {
		t.Fatalf("Delete returned error for missing object: %v", err)
	}
}

func TestRegistrationDoesNotAdvertiseProbeOrInventory(t *testing.T) {
	lookup := credentialLookupFunc(func(context.Context, string) (*buckets.Credential, error) {
		return &buckets.Credential{Provider: "gcs", Bucket: "bucket"}, nil
	})
	manager, err := storageports.NewManager(lookup, New())
	if err != nil {
		t.Fatalf("NewManager returned error: %v", err)
	}
	probe := manager.Probe(context.Background(), []storageports.ProbeTarget{{ID: "one", Target: storageports.Target{PhysicalBucket: "bucket", Key: "object"}}})
	var probeErr *storageports.OperationError
	if len(probe) != 1 || !errors.As(probe[0].Err, &probeErr) || probeErr.Kind != storageports.ErrorUnsupported {
		t.Fatalf("probe result = %#v, want unsupported capability", probe)
	}
	_, err = manager.Inventory(context.Background(), storageports.InventoryRequest{Target: storageports.Target{PhysicalBucket: "bucket"}})
	var inventoryErr *storageports.OperationError
	if !errors.As(err, &inventoryErr) || inventoryErr.Kind != storageports.ErrorUnsupported {
		t.Fatalf("inventory error = %v, want unsupported capability", err)
	}
}

func TestCredentialErrorsPassThrough(t *testing.T) {
	b := &backend{}
	_, err := b.Sign(context.Background(), storageports.ProviderBinding{PhysicalBucket: "bucket"}, storageports.SignRequest{Target: storageports.Target{PhysicalBucket: "bucket", Key: "object"}})
	if err == nil || !strings.Contains(err.Error(), "credentials not found") {
		t.Fatalf("Sign error = %v, want missing credentials", err)
	}
}

func TestMultipartPartsUseExpectedLayout(t *testing.T) {
	got := storageports.MultipartPartObjectKey("/nested/file.txt/", "upload", 4)
	want := ".syfon-multipart/upload/nested/file.txt/parts/4"
	if got != want {
		t.Fatalf("multipart key = %q, want %q", got, want)
	}
}

// marshalPKCS8PrivateKeyMust keeps the native-signing test focused on the
// provider contract while retaining the normal crypto/x509 error path in the
// test setup.
func marshalPKCS8PrivateKeyMust(key *rsa.PrivateKey) []byte {
	encoded, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		panic(err)
	}
	return encoded
}
