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
	"testing"
	"time"

	"github.com/calypr/syfon/internal/buckets"
	"github.com/calypr/syfon/internal/storage"
	"github.com/google/uuid"
)

type credentialLookupFunc func(context.Context, string) (*buckets.Credential, error)

func (f credentialLookupFunc) GetS3Credential(ctx context.Context, bucket string) (*buckets.Credential, error) {
	return f(ctx, bucket)
}

type recordingTransport struct {
	status         int
	header         http.Header
	statuses       []int
	headers        []http.Header
	requests       int
	requestHost    string
	requestPath    string
	requestHeaders http.Header
	body           []byte
}

func (r *recordingTransport) Do(request *http.Request) (*http.Response, error) {
	r.requests++
	r.requestHost = request.URL.Host
	r.requestPath = request.URL.Path
	r.requestHeaders = request.Header.Clone()
	if request.Body != nil {
		body, err := io.ReadAll(request.Body)
		if err != nil {
			return nil, err
		}
		r.body = body
	}
	responseIndex := r.requests - 1
	status := r.status
	if responseIndex < len(r.statuses) {
		status = r.statuses[responseIndex]
	}
	if status == 0 {
		status = http.StatusOK
	}
	header := r.header
	if responseIndex < len(r.headers) {
		header = r.headers[responseIndex]
	}
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
	b := &backend{}
	binding := storage.ProviderBinding{LookupKey: "test-bucket", PhysicalBucket: "test-bucket", Credential: azureCredential("https://acct.blob.db.windows.net")}

	before := time.Now().UTC()
	read, err := b.Sign(context.Background(), binding, storage.SignRequest{Target: storage.Target{PhysicalBucket: "test-bucket", Key: "path with spaces/object.txt"}})
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

	put, err := b.Sign(context.Background(), binding, storage.SignRequest{Target: storage.Target{PhysicalBucket: "test-bucket", Key: "object.txt"}, ExpiresIn: 7 * time.Minute, Method: http.MethodPut})
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
	b := &backend{}
	binding := storage.ProviderBinding{LookupKey: "test-bucket", PhysicalBucket: "test-bucket", Credential: azureCredential("https://acct.blob.db.windows.net")}

	access, err := b.Sign(context.Background(), binding, storage.SignRequest{Target: storage.Target{PhysicalBucket: "test-bucket", Key: "nested/object.txt"}, Range: &storage.ByteRange{Start: 4, End: 12}, DownloadFilename: "chunk.txt"})
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
	b := &backend{}
	binding := storage.ProviderBinding{LookupKey: "test-bucket", PhysicalBucket: "test-bucket", Credential: azureCredential("https://acct.blob.db.windows.net")}
	first, err := b.getCreds(binding)
	if err != nil {
		t.Fatalf("first credential derivation returned error: %v", err)
	}
	second, err := b.getCreds(binding)
	if err != nil {
		t.Fatalf("cached credential derivation returned error: %v", err)
	}
	if first != second {
		t.Fatal("credential cache did not return the same value")
	}
	b.InvalidateBucket(" test-bucket ")
	third, err := b.getCreds(binding)
	if err != nil {
		t.Fatalf("post-invalidation credential derivation returned error: %v", err)
	}
	if third == first {
		t.Fatal("invalidation did not evict cached credential")
	}
}

func TestAzureMultipartBlockIDAndCompletionOrder(t *testing.T) {
	var requestBody []byte
	transport := &recordingTransport{status: http.StatusCreated, header: http.Header{"Content-Type": []string{"application/xml"}}}

	b := &backend{transport: transport}
	binding := storage.ProviderBinding{LookupKey: "test-bucket", PhysicalBucket: "test-bucket", Credential: azureCredential("http://azure.test")}
	uploadID := storage.UploadID("upload-abc")
	before := time.Now().UTC()
	part, err := b.SignMultipartPart(context.Background(), binding, storage.MultipartPartRequest{
		Target:     storage.Target{PhysicalBucket: "test-bucket", Key: "object.bin"},
		UploadID:   uploadID,
		PartNumber: 2,
		ExpiresIn:  7 * time.Minute,
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
	assertSASWindow(t, partURL.Query(), before, 7*time.Minute)
	decodedBlockID, err := base64.StdEncoding.DecodeString(partURL.Query().Get("blockid"))
	if err != nil {
		t.Fatalf("decode block ID: %v", err)
	}
	if got, want := string(decodedBlockID), "upload-abc:00000002"; got != want {
		t.Fatalf("block ID = %q, want %q", got, want)
	}

	err = b.CompleteMultipart(context.Background(), binding, storage.CompleteMultipartRequest{
		Target:   storage.Target{PhysicalBucket: "test-bucket", Key: "object.bin"},
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

func TestAzureMultipartWritesCompletionMarker(t *testing.T) {
	transport := &recordingTransport{
		statuses: []int{http.StatusOK, http.StatusCreated},
		headers:  []http.Header{{}, {"Content-Type": []string{"application/xml"}}},
	}
	b := &backend{transport: transport}
	binding := storage.ProviderBinding{LookupKey: "test-bucket", PhysicalBucket: "test-bucket", Credential: azureCredential("http://azure.test")}
	if err := b.CompleteMultipart(context.Background(), binding, storage.CompleteMultipartRequest{
		Target:       storage.Target{PhysicalBucket: "test-bucket", Key: "object.bin"},
		UploadID:     "upload",
		CompletionID: "completion",
		Parts:        []storage.CompletedPart{{PartNumber: 1}},
	}); err != nil {
		t.Fatalf("CompleteMultipart returned error: %v", err)
	}
	var got string
	for key, values := range transport.requestHeaders {
		if strings.EqualFold(key, "x-ms-meta-"+storage.MultipartCompletionMarkerMetadataKey) && len(values) > 0 {
			got = values[0]
		}
	}
	if got != "completion" {
		t.Fatalf("completion metadata header = %q, headers=%#v, want completion", got, transport.requestHeaders)
	}
}

func TestAzureCompleteMultipartSkipsProviderWhenMarkerMatches(t *testing.T) {
	header := make(http.Header)
	header.Set("x-ms-meta-"+storage.MultipartCompletionMarkerMetadataKey, "completion")
	transport := &recordingTransport{status: http.StatusOK, header: header}
	b := &backend{transport: transport}
	binding := storage.ProviderBinding{LookupKey: "test-bucket", PhysicalBucket: "test-bucket", Credential: azureCredential("http://azure.test")}
	if err := b.CompleteMultipart(context.Background(), binding, storage.CompleteMultipartRequest{
		Target:       storage.Target{PhysicalBucket: "test-bucket", Key: "object.bin"},
		UploadID:     "upload",
		CompletionID: "completion",
		Parts:        []storage.CompletedPart{{PartNumber: 1}},
	}); err != nil {
		t.Fatalf("CompleteMultipart returned error: %v", err)
	}
	if transport.requests != 1 {
		t.Fatalf("provider requests = %d, want one marker read and no commit", transport.requests)
	}
}

func TestAzureInitMultipartUploadReturnsUUID(t *testing.T) {
	b := &backend{}
	uploadID, err := b.BeginMultipart(context.Background(), storage.ProviderBinding{}, storage.BeginMultipartRequest{Target: storage.Target{PhysicalBucket: "test-bucket", Key: "object.bin"}, CompletionID: "completion"})
	if err != nil {
		t.Fatalf("InitMultipartUpload returned error: %v", err)
	}
	if _, err := uuid.Parse(string(uploadID)); err != nil {
		t.Fatalf("upload ID = %q, want UUID: %v", uploadID, err)
	}
}

func TestAzureEmptyMultipartCompletionCallsProvider(t *testing.T) {
	transport := &recordingTransport{status: http.StatusCreated, header: http.Header{"Content-Type": []string{"application/xml"}}}
	b := &backend{transport: transport}
	binding := storage.ProviderBinding{LookupKey: "test-bucket", PhysicalBucket: "test-bucket", Credential: azureCredential("http://azure.test")}

	err := b.CompleteMultipart(context.Background(), binding, storage.CompleteMultipartRequest{
		Target:   storage.Target{PhysicalBucket: "test-bucket", Key: "object.bin"},
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

	b := &backend{transport: transport}
	binding := storage.ProviderBinding{LookupKey: "test-bucket", PhysicalBucket: "test-bucket", Credential: azureCredential("")}
	err := b.Delete(context.Background(), binding, []storage.PhysicalTarget{{Provider: "azure", PhysicalBucket: "test-bucket", Key: "path/object.txt"}})
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
			b := &backend{transport: transport}
			binding := storage.ProviderBinding{LookupKey: "test-bucket", PhysicalBucket: "test-bucket", Credential: azureCredential("http://azure.test")}
			err := b.Delete(context.Background(), binding, []storage.PhysicalTarget{{Provider: "azure", PhysicalBucket: "test-bucket", Key: "object.txt"}})
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
	manager, err := storage.NewManager(lookup, New())
	if err != nil {
		t.Fatalf("NewManager returned error: %v", err)
	}
	results := manager.Probe(context.Background(), []storage.ProbeTarget{{ID: "one", Target: storage.Target{PhysicalBucket: "test-bucket", Key: "object"}}})
	var probeErr *storage.OperationError
	if len(results) != 1 || !errors.As(results[0].Err, &probeErr) || probeErr.Kind != storage.ErrorUnsupported {
		t.Fatalf("probe result = %#v, want unsupported operation error", results)
	}
	_, err = manager.Inventory(context.Background(), storage.InventoryRequest{Target: storage.Target{PhysicalBucket: "test-bucket"}})
	var inventoryErr *storage.OperationError
	if !errors.As(err, &inventoryErr) || inventoryErr.Kind != storage.ErrorUnsupported {
		t.Fatalf("inventory error = %v, want unsupported operation error", err)
	}
}
