package azure

import (
	"context"
	"encoding/base64"
	"encoding/xml"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/calypr/syfon/internal/buckets"
	"github.com/calypr/syfon/internal/storage"
	"github.com/google/uuid"
)

type credentialLookupFunc func(context.Context, string) (*buckets.Credential, error)

type recordingTransport struct {
	status      int
	header      http.Header
	requests    int
	requestHost string
	requestPath string
	body        []byte
}

func (r *recordingTransport) Do(request *http.Request) (*http.Response, error) {
	r.requests++
	r.requestHost = request.URL.Host
	r.requestPath = request.URL.Path
	if request.Body != nil {
		body, err := io.ReadAll(request.Body)
		if err != nil {
			return nil, err
		}
		r.body = body
	}
	status := r.status
	if status == 0 {
		status = http.StatusOK
	}
	header := r.header
	if header == nil {
		header = make(http.Header)
	}
	return &http.Response{
		StatusCode: status,
		Header:     header,
		Body:       io.NopCloser(strings.NewReader("<BlockList/>")),
		Request:    request,
	}, nil
}

func (f credentialLookupFunc) GetS3Credential(ctx context.Context, bucket string) (*buckets.Credential, error) {
	return f(ctx, bucket)
}

func azureCredential(endpoint string) *buckets.Credential {
	return &buckets.Credential{
		Bucket:    "test-bucket",
		Provider:  "azure",
		AccessKey: "acct",
		SecretKey: "dGVzdA==",
		Endpoint:  endpoint,
	}
}

func TestAzureAccessSASPermissionsAndExpiry(t *testing.T) {
	b := &backend{credentials: credentialLookupFunc(func(context.Context, string) (*buckets.Credential, error) {
		return azureCredential("https://acct.blob.db.windows.net"), nil
	})}

	before := time.Now().UTC()
	read, err := b.SignURL(context.Background(), storage.ObjectTarget{Bucket: "test-bucket", Key: "path with spaces/object.txt"}, storage.AccessOptions{})
	if err != nil {
		t.Fatalf("SignURL returned error: %v", err)
	}
	readURL, err := url.Parse(read.Location)
	if err != nil {
		t.Fatalf("parse read URL: %v", err)
	}
	if got := readURL.Query().Get("sp"); got != "r" {
		t.Fatalf("read SAS permissions = %q, want r", got)
	}
	if got, want := readURL.EscapedPath(), "/test-bucket/path%20with%20spaces/object.txt"; got != want {
		t.Fatalf("read URL path = %q, want %q", got, want)
	}
	assertSASWindow(t, readURL.Query(), before, 15*time.Minute)

	put, err := b.SignURL(context.Background(), storage.ObjectTarget{Bucket: "test-bucket", Key: "object.txt"}, storage.AccessOptions{
		ExpiresIn: 7 * time.Minute,
		Method:    http.MethodPut,
	})
	if err != nil {
		t.Fatalf("PUT SignURL returned error: %v", err)
	}
	putURL, err := url.Parse(put.Location)
	if err != nil {
		t.Fatalf("parse PUT URL: %v", err)
	}
	if got := putURL.Query().Get("sp"); got != "acw" {
		t.Fatalf("PUT SAS permissions = %q, want acw", got)
	}
	assertSASWindow(t, putURL.Query(), before, 7*time.Minute)
}

func TestAzureCredentialLookupPort(t *testing.T) {
	var _ storage.CredentialLookup = credentialLookupFunc(nil)

	t.Run("accepts one-method lookup", func(t *testing.T) {
		b := &backend{credentials: credentialLookupFunc(func(context.Context, string) (*buckets.Credential, error) {
			return azureCredential("https://acct.blob.db.windows.net"), nil
		})}
		if _, err := b.SignURL(context.Background(), storage.ObjectTarget{Bucket: "bucket", Key: "object"}, storage.AccessOptions{}); err != nil {
			t.Fatalf("SignURL with one-method lookup failed: %v", err)
		}
	})

	t.Run("preserves lookup error", func(t *testing.T) {
		wantErr := errors.New("lookup failed")
		b := &backend{credentials: credentialLookupFunc(func(context.Context, string) (*buckets.Credential, error) {
			return nil, wantErr
		})}
		_, err := b.SignURL(context.Background(), storage.ObjectTarget{Bucket: "bucket", Key: "object"}, storage.AccessOptions{})
		if !errors.Is(err, wantErr) {
			t.Fatalf("SignURL error = %v, want %v", err, wantErr)
		}
	})

	t.Run("preserves nil credential error", func(t *testing.T) {
		b := &backend{credentials: credentialLookupFunc(func(context.Context, string) (*buckets.Credential, error) {
			return nil, nil
		})}
		_, err := b.SignURL(context.Background(), storage.ObjectTarget{Bucket: "bucket", Key: "object"}, storage.AccessOptions{})
		if err == nil || !strings.Contains(err.Error(), "credentials not found for bucket bucket") {
			t.Fatalf("SignURL error = %v, want missing-credential error", err)
		}
	})
}

func TestAzureSASProtocolAndCredentialDerivation(t *testing.T) {
	if got := azureSASProtocol("http://localhost:10000/devstoreaccount1"); string(got) != "https,http" {
		t.Fatalf("HTTP endpoint SAS protocol = %q, want https,http", got)
	}
	if got := azureSASProtocol("https://acct.blob.db.windows.net"); string(got) != "https" {
		t.Fatalf("HTTPS endpoint SAS protocol = %q, want https", got)
	}
	if got := azureSASProtocol("://bad-url"); string(got) != "https" {
		t.Fatalf("invalid endpoint SAS protocol = %q, want https", got)
	}

	b := &backend{}
	if got, want := b.azureServiceURL("acct", ""), "https://acct.blob.db.windows.net"; got != want {
		t.Fatalf("default service URL = %q, want %q", got, want)
	}
	if got, want := b.azureServiceURL("", "localhost:10000/devstoreaccount1"), "https://localhost:10000/devstoreaccount1"; got != want {
		t.Fatalf("endpoint-normalized service URL = %q, want %q", got, want)
	}
	if got, want := b.azureAccountFromEndpoint("http://localhost:10000/devstoreaccount1"), "localhost"; got != want {
		t.Fatalf("localhost account = %q, want %q", got, want)
	}
	if got, want := b.azureAccountFromEndpoint("https://myacct.blob.db.windows.net"), "myacct"; got != want {
		t.Fatalf("Azure account = %q, want %q", got, want)
	}
	if got := b.azureAccountFromEndpoint("not a url"); got != "" {
		t.Fatalf("invalid endpoint account = %q, want empty", got)
	}
}

func assertSASWindow(t *testing.T, query url.Values, before time.Time, expiry time.Duration) {
	t.Helper()
	start, err := time.Parse(time.RFC3339, query.Get("st"))
	if err != nil {
		t.Fatalf("parse SAS start: %v", err)
	}
	if start.Before(before.Add(-6*time.Minute)) || start.After(before.Add(-4*time.Minute)) {
		t.Fatalf("SAS start = %s, outside expected five-minute window", start)
	}
	expires, err := time.Parse(time.RFC3339, query.Get("se"))
	if err != nil {
		t.Fatalf("parse SAS expiry: %v", err)
	}
	if expires.Before(before.Add(expiry-time.Minute)) || expires.After(before.Add(expiry+time.Minute)) {
		t.Fatalf("SAS expiry = %s, outside expected %s window", expires, expiry)
	}
}

func TestAzureRangeAndDownloadFilenameArePreserved(t *testing.T) {
	b := &backend{credentials: credentialLookupFunc(func(context.Context, string) (*buckets.Credential, error) {
		return azureCredential("https://acct.blob.db.windows.net"), nil
	})}

	access, err := b.SignDownloadPart(context.Background(), storage.ObjectTarget{Bucket: "test-bucket", Key: "nested/object.txt"}, storage.ByteRange{Start: 4, End: 12}, storage.AccessOptions{
		DownloadFilename: "chunk.txt",
	})
	if err != nil {
		t.Fatalf("SignDownloadPart returned error: %v", err)
	}
	u, err := url.Parse(access.Location)
	if err != nil {
		t.Fatalf("parse signed range URL: %v", err)
	}
	if got := u.Query().Get("rscd"); !strings.Contains(got, "chunk.txt") {
		t.Fatalf("content disposition = %q, want chunk filename", got)
	}
	if got := u.Query().Get("range"); got != "" {
		t.Fatalf("Azure SAS unexpectedly encoded range %q", got)
	}
}

func TestAzureCacheInvalidationReloadsCredentialAndServiceURL(t *testing.T) {
	var mu sync.Mutex
	lookups := 0
	b := &backend{credentials: credentialLookupFunc(func(context.Context, string) (*buckets.Credential, error) {
		mu.Lock()
		lookups++
		mu.Unlock()
		return azureCredential("https://acct.blob.db.windows.net"), nil
	})}
	target := storage.ObjectTarget{Bucket: "test-bucket", Key: "object.txt"}
	if _, err := b.SignURL(context.Background(), target, storage.AccessOptions{}); err != nil {
		t.Fatalf("first SignURL returned error: %v", err)
	}
	if _, err := b.SignURL(context.Background(), target, storage.AccessOptions{}); err != nil {
		t.Fatalf("cached SignURL returned error: %v", err)
	}
	b.InvalidateBucket(" test-bucket ")
	if _, err := b.SignURL(context.Background(), target, storage.AccessOptions{}); err != nil {
		t.Fatalf("post-invalidation SignURL returned error: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if lookups != 2 {
		t.Fatalf("credential lookups = %d, want 2", lookups)
	}
}

func TestAzureMultipartBlockIDAndCompletionOrder(t *testing.T) {
	var requestBody []byte
	transport := &recordingTransport{status: http.StatusCreated, header: http.Header{"Content-Type": []string{"application/xml"}}}

	b := &backend{credentials: credentialLookupFunc(func(context.Context, string) (*buckets.Credential, error) {
		return azureCredential("http://azure.test"), nil
	}), transport: transport}
	uploadID := storage.UploadID("upload-abc")
	part, err := b.SignMultipartPart(context.Background(), storage.MultipartPartRequest{
		Target:     storage.ObjectTarget{Bucket: "test-bucket", Key: "object.bin"},
		UploadID:   uploadID,
		PartNumber: 2,
	})
	if err != nil {
		t.Fatalf("SignMultipartPart returned error: %v", err)
	}
	partURL, err := url.Parse(part.Location)
	if err != nil {
		t.Fatalf("parse block URL: %v", err)
	}
	if got := partURL.Query().Get("comp"); got != "block" {
		t.Fatalf("block comp query = %q, want block", got)
	}
	decodedBlockID, err := base64.StdEncoding.DecodeString(partURL.Query().Get("blockid"))
	if err != nil {
		t.Fatalf("decode block ID: %v", err)
	}
	if got, want := string(decodedBlockID), "upload-abc:00000002"; got != want {
		t.Fatalf("block ID = %q, want %q", got, want)
	}

	err = b.CompleteMultipartUpload(context.Background(), storage.CompleteMultipartRequest{
		Target:   storage.ObjectTarget{Bucket: "test-bucket", Key: "object.bin"},
		UploadID: uploadID,
		Parts: []storage.CompletedPart{
			{PartNumber: 3, ETag: "ignored-3"},
			{PartNumber: 1, ETag: "ignored-1"},
			{PartNumber: 2, ETag: "ignored-2"},
		},
	})
	if err != nil {
		t.Fatalf("CompleteMultipartUpload returned error: %v", err)
	}
	requestBody = transport.body
	var blockList struct {
		Latest []string `xml:"Latest"`
	}
	if err := xml.Unmarshal(requestBody, &blockList); err != nil {
		t.Fatalf("decode block list: %v; body=%q", err, requestBody)
	}
	want := []string{
		b.azureBlockID(uploadID, 1),
		b.azureBlockID(uploadID, 2),
		b.azureBlockID(uploadID, 3),
	}
	if len(blockList.Latest) != len(want) {
		t.Fatalf("committed block count = %d, want %d; body=%q", len(blockList.Latest), len(want), requestBody)
	}
	for i := range want {
		if blockList.Latest[i] != want[i] {
			t.Fatalf("committed block %d = %q, want %q", i, blockList.Latest[i], want[i])
		}
	}
}

func TestAzureInitMultipartUploadReturnsUUID(t *testing.T) {
	b := &backend{}
	uploadID, err := b.InitMultipartUpload(context.Background(), storage.ObjectTarget{Bucket: "test-bucket", Key: "object.bin"})
	if err != nil {
		t.Fatalf("InitMultipartUpload returned error: %v", err)
	}
	if _, err := uuid.Parse(string(uploadID)); err != nil {
		t.Fatalf("upload ID = %q, want UUID: %v", uploadID, err)
	}
}

func TestAzureEmptyMultipartCompletionCallsProvider(t *testing.T) {
	transport := &recordingTransport{status: http.StatusCreated, header: http.Header{"Content-Type": []string{"application/xml"}}}
	b := &backend{credentials: credentialLookupFunc(func(context.Context, string) (*buckets.Credential, error) {
		return azureCredential("http://azure.test"), nil
	}), transport: transport}

	err := b.CompleteMultipartUpload(context.Background(), storage.CompleteMultipartRequest{
		Target:   storage.ObjectTarget{Bucket: "test-bucket", Key: "object.bin"},
		UploadID: "upload-empty",
	})
	if err != nil {
		t.Fatalf("empty CompleteMultipartUpload returned error: %v", err)
	}
	if transport.requests != 1 {
		t.Fatalf("empty completion requests = %d, want 1", transport.requests)
	}
	var blockList struct {
		Latest []string `xml:"Latest"`
	}
	if err := xml.Unmarshal(transport.body, &blockList); err != nil {
		t.Fatalf("decode empty block list: %v; body=%q", err, transport.body)
	}
	if len(blockList.Latest) != 0 {
		t.Fatalf("empty completion sent %d block IDs, want 0", len(blockList.Latest))
	}
}

func TestAzureDeleteUsesHistoricalEndpointAndNotFoundIsIdempotent(t *testing.T) {
	if got, want := (&backend{}).azureServiceURL("acct", ""), "https://acct.blob.db.windows.net"; got != want {
		t.Fatalf("signing default endpoint = %q, want %q", got, want)
	}
	if got, want := (&backend{}).azureDeleteServiceURL("acct", ""), "https://acct.blob.core.windows.net"; got != want {
		t.Fatalf("deletion default endpoint = %q, want %q", got, want)
	}

	transport := &recordingTransport{status: http.StatusNotFound, header: http.Header{"X-Ms-Error-Code": []string{"BlobNotFound"}}}

	b := &backend{credentials: credentialLookupFunc(func(context.Context, string) (*buckets.Credential, error) {
		return azureCredential(""), nil
	}), transport: transport}
	err := b.Delete(context.Background(), []storage.PhysicalTarget{{Provider: "azure", Bucket: "test-bucket", Key: "path/object.txt"}})
	if err != nil {
		t.Fatalf("Delete returned error for missing blob: %v", err)
	}
	if transport.requests != 1 {
		t.Fatalf("delete requests = %d, want 1", transport.requests)
	}
	if got, want := transport.requestHost, "acct.blob.core.windows.net"; got != want {
		t.Fatalf("delete host = %q, want %q", got, want)
	}
	if got, want := transport.requestPath, "/test-bucket/path/object.txt"; got != want {
		t.Fatalf("delete path = %q, want %q", got, want)
	}
}

func TestAzureDeleteNotFoundMapping(t *testing.T) {
	for _, test := range []struct {
		name      string
		errorCode string
		wantError bool
	}{
		{name: "blob not found", errorCode: "BlobNotFound"},
		{name: "container not found", errorCode: "ContainerNotFound"},
		{name: "unknown not found", wantError: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			header := http.Header{}
			if test.errorCode != "" {
				header.Set("X-Ms-Error-Code", test.errorCode)
			}
			transport := &recordingTransport{status: http.StatusNotFound, header: header}
			b := &backend{credentials: credentialLookupFunc(func(context.Context, string) (*buckets.Credential, error) {
				return azureCredential("http://azure.test"), nil
			}), transport: transport}
			err := b.Delete(context.Background(), []storage.PhysicalTarget{{Provider: "azure", Bucket: "test-bucket", Key: "object.txt"}})
			if (err != nil) != test.wantError {
				t.Fatalf("Delete error = %v, want error=%t", err, test.wantError)
			}
		})
	}
}

func TestAzureRegistrationDoesNotClaimProbeOrInventory(t *testing.T) {
	lookup := credentialLookupFunc(func(context.Context, string) (*buckets.Credential, error) {
		return azureCredential("https://acct.blob.db.windows.net"), nil
	})
	manager, err := storage.NewManager(lookup, New(lookup))
	if err != nil {
		t.Fatalf("NewManager returned error: %v", err)
	}
	results := manager.Probe(context.Background(), []storage.ProbeTarget{{ID: "one", Target: storage.ObjectTarget{Bucket: "test-bucket", Key: "object"}}})
	var probeErr *storage.OperationError
	if len(results) != 1 || !errors.As(results[0].Err, &probeErr) || probeErr.Kind != storage.ErrorUnsupported {
		t.Fatalf("probe result = %#v, want unsupported operation error", results)
	}
	_, err = manager.Inventory(context.Background(), storage.InventoryRequest{Target: storage.PrefixTarget{Bucket: "test-bucket"}})
	var inventoryErr *storage.OperationError
	if !errors.As(err, &inventoryErr) || inventoryErr.Kind != storage.ErrorUnsupported {
		t.Fatalf("inventory error = %v, want unsupported operation error", err)
	}
}
