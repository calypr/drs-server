package services

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/calypr/syfon/apigen/drs"
	"github.com/calypr/syfon/apigen/internalapi"
	"github.com/calypr/syfon/client/transfer"
	"github.com/calypr/syfon/client/transfer/engine"
)

func TestDownloadObjectIdentityUsesCanonicalSHA256(t *testing.T) {
	checksum := strings.Repeat("AB", 32)
	object := &drs.DrsObject{Checksums: []drs.Checksum{{Type: "SHA-256", Checksum: "SHA256:" + checksum}}}
	if got := downloadObjectIdentity(object); got != "sha256:"+strings.ToLower(checksum) {
		t.Fatalf("download identity = %q, want canonical SHA-256", got)
	}
	object.Checksums[0].Checksum = "SHA256:not-a-checksum"
	if got := downloadObjectIdentity(object); got != "" {
		t.Fatalf("invalid download identity = %q, want empty", got)
	}
}

type sizedDataService struct {
	*DataService
	size int64
}

func (s sizedDataService) Stat(context.Context, string) (*transfer.ObjectMetadata, error) {
	return &transfer.ObjectMetadata{Size: s.size, AcceptRanges: true}, nil
}

func TestIgnoredZeroOffsetRangeFallsBackToFullDownload(t *testing.T) {
	data := bytes.Repeat([]byte("x"), 2*1024*1024)
	fullReads, rangeReads := 0, 0
	transport := roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		if req.URL.Host == "api.example" {
			return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"url":"https://storage.example/object"}`)), Request: req}, nil
		}
		if req.Header.Get("Range") == "" {
			fullReads++
		} else {
			rangeReads++
		}
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(data)), ContentLength: int64(len(data)), Request: req}, nil
	})
	client := &http.Client{Transport: transport}
	generated, err := internalapi.NewClientWithResponses("https://api.example", internalapi.WithHTTPClient(client))
	if err != nil {
		t.Fatal(err)
	}
	source := sizedDataService{DataService: NewDataService(generated, client, discardLogger(), nil), size: int64(len(data))}
	destination := filepath.Join(t.TempDir(), "output.bin")
	err = engine.Download(context.Background(), source, "object", destination, engine.DownloadOptions{MultipartThreshold: 1, ChunkSize: 1024 * 1024, Concurrency: 1, RetryStrategy: &transfer.ExponentialBackoff{MaxWaitSeconds: 0}})
	if err != nil {
		t.Fatalf("range-ignoring server did not fall back: %v; full reads=%d, range reads=%d", err, fullReads, rangeReads)
	}
	got, err := os.ReadFile(destination)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, data) {
		t.Fatal("download content mismatch")
	}
}
