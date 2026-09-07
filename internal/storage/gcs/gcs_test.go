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
	"reflect"
	"strings"
	"testing"

	"cloud.google.com/go/storage"
	"google.golang.org/api/option"

	"github.com/calypr/syfon/internal/buckets"
	storageports "github.com/calypr/syfon/internal/storage"
)

type testLookup struct {
	credential *buckets.Credential
	err        error
}

type roundTripperFunc func(*http.Request) *http.Response

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

func (l testLookup) GetS3Credential(context.Context, string) (*buckets.Credential, error) {
	return l.credential, l.err
}

func TestEndpointAccessPreservesPathAndOmitsRange(t *testing.T) {
	b := &backend{credentials: testLookup{credential: &buckets.Credential{Endpoint: "http://localhost:4443"}}}

	access, err := b.SignURL(context.Background(), storageports.ObjectTarget{Bucket: "test-bucket", Key: "nested/file.txt"}, storageports.AccessOptions{DownloadFilename: "report.txt"})
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

	ranged, err := b.SignDownloadPart(context.Background(), storageports.ObjectTarget{Bucket: "test-bucket", Key: "nested/file.txt"}, storageports.ByteRange{Start: 5, End: 12}, storageports.AccessOptions{})
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

	part, err := b.SignMultipartPart(context.Background(), storageports.MultipartPartRequest{
		Target: storageports.ObjectTarget{Bucket: "test-bucket", Key: "nested/file.txt"}, UploadID: "upload", PartNumber: 4,
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

func TestNativeRangedAccessUsesV4RangeSignature(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 1024)
	if err != nil {
		t.Fatalf("generate RSA key: %v", err)
	}
	privateKey := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: marshalPKCS8PrivateKeyMust(key)})
	b := &backend{credentials: testLookup{credential: &buckets.Credential{
		AccessKey: "service-account@example.test", SecretKey: string(privateKey),
	}}}

	access, err := b.SignDownloadPart(context.Background(), storageports.ObjectTarget{Bucket: "bucket", Key: "object"}, storageports.ByteRange{Start: 10, End: 19}, storageports.AccessOptions{})
	if err != nil {
		t.Fatalf("SignDownloadPart returned error: %v", err)
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

	b := &backend{credentials: testLookup{credential: &buckets.Credential{Bucket: "bucket"}}}
	parts := make([]storageports.CompletedPart, 33)
	for i := range parts {
		parts[i] = storageports.CompletedPart{PartNumber: int32(len(parts) - i), ETag: "ignored"}
	}
	if err := b.CompleteMultipartUpload(context.Background(), storageports.CompleteMultipartRequest{
		Target: storageports.ObjectTarget{Bucket: "bucket", Key: "obj.bin"}, UploadID: "upload", Parts: parts,
	}); err != nil {
		t.Fatalf("CompleteMultipartUpload returned error: %v", err)
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

func TestInvalidateBucketEvictsCachedNativeClientByLookupKey(t *testing.T) {
	previous := newClient
	newClient = func(ctx context.Context, _ *buckets.Credential) (*storage.Client, error) {
		return testClient(ctx, func(r *http.Request) *http.Response {
			return responseFor(r, http.StatusNotFound, "")
		})
	}
	defer func() { newClient = previous }()

	b := &backend{credentials: testLookup{credential: &buckets.Credential{Bucket: "bucket"}}}
	first, err := b.getClient(context.Background(), "bucket")
	if err != nil {
		t.Fatalf("first getClient returned error: %v", err)
	}
	second, err := b.getClient(context.Background(), "bucket")
	if err != nil {
		t.Fatalf("second getClient returned error: %v", err)
	}
	if first != second {
		t.Fatal("cache did not return the same client")
	}
	b.InvalidateBucket(" bucket ")
	third, err := b.getClient(context.Background(), "bucket")
	if err != nil {
		t.Fatalf("evicted getClient returned error: %v", err)
	}
	if third == first {
		t.Fatal("invalidation did not evict cached client")
	}
	_ = first.Close()
	_ = third.Close()
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

	b := &backend{credentials: testLookup{credential: &buckets.Credential{Bucket: "bucket"}}}
	if err := b.Delete(context.Background(), []storageports.PhysicalTarget{{Provider: "gcs", Bucket: "bucket", Key: "gone"}}); err != nil {
		t.Fatalf("Delete returned error for missing object: %v", err)
	}
}

func TestRegistrationDoesNotAdvertiseProbeOrInventory(t *testing.T) {
	lookup := testLookup{credential: &buckets.Credential{Provider: "gcs", Bucket: "bucket"}}
	manager, err := storageports.NewManager(lookup, New(lookup))
	if err != nil {
		t.Fatalf("NewManager returned error: %v", err)
	}
	probe := manager.Probe(context.Background(), []storageports.ProbeTarget{{ID: "one", Target: storageports.ObjectTarget{Bucket: "bucket", Key: "object"}}})
	var probeErr *storageports.OperationError
	if len(probe) != 1 || !errors.As(probe[0].Err, &probeErr) || probeErr.Kind != storageports.ErrorUnsupported {
		t.Fatalf("probe result = %#v, want unsupported capability", probe)
	}
	_, err = manager.Inventory(context.Background(), storageports.InventoryRequest{Target: storageports.PrefixTarget{Bucket: "bucket"}})
	var inventoryErr *storageports.OperationError
	if !errors.As(err, &inventoryErr) || inventoryErr.Kind != storageports.ErrorUnsupported {
		t.Fatalf("inventory error = %v, want unsupported capability", err)
	}
}

func TestCredentialErrorsPassThrough(t *testing.T) {
	wantErr := errors.New("lookup failed")
	b := &backend{credentials: testLookup{err: wantErr}}
	_, err := b.SignURL(context.Background(), storageports.ObjectTarget{Bucket: "bucket", Key: "object"}, storageports.AccessOptions{})
	if !errors.Is(err, wantErr) {
		t.Fatalf("SignURL error = %v, want %v", err, wantErr)
	}
}

func TestMultipartPartsUseExpectedLayout(t *testing.T) {
	got := storageports.MultipartPartObjectKey("/nested/file.txt/", "upload", 4)
	want := ".syfon-multipart/upload/nested/file.txt/parts/4"
	if got != want {
		t.Fatalf("multipart key = %q, want %q", got, want)
	}
	if !reflect.DeepEqual(storageports.NormalizedMultipartParts([]storageports.CompletedPart{{PartNumber: 2}, {PartNumber: 1}}), []storageports.CompletedPart{{PartNumber: 1}, {PartNumber: 2}}) {
		t.Fatal("multipart normalization did not sort by part number")
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
