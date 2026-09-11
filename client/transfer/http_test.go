package transfer

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/calypr/syfon/client/request"
)

type captureRequester struct {
	method  string
	path    string
	request *http.Request
	resp    *http.Response
	err     error
}

func (c *captureRequester) Do(req *http.Request) (*http.Response, error) {
	c.request = req
	c.method = req.Method
	c.path = req.URL.String()
	if c.resp == nil {
		c.resp = &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader("")), Header: make(http.Header), Request: req}
	}
	return c.resp, c.err
}

func TestDoUploadLocalPathAndFileScheme(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	tmp := t.TempDir()
	body := "hello upload"
	req := &captureRequester{}

	rawPath := filepath.Join(tmp, "plain", "payload.txt")
	if _, err := DoUpload(ctx, req, rawPath, strings.NewReader(body), int64(len(body))); err != nil {
		t.Fatalf("DoUpload raw path returned error: %v", err)
	}
	got, err := os.ReadFile(rawPath)
	if err != nil {
		t.Fatalf("ReadFile returned error: %v", err)
	}
	if string(got) != body {
		t.Fatalf("unexpected raw path payload %q", got)
	}

	fileURL := (&url.URL{Scheme: "file", Path: filepath.ToSlash(filepath.Join(tmp, "url", "payload.txt"))}).String()
	if _, err := DoUpload(ctx, req, fileURL, strings.NewReader(body), int64(len(body))); err != nil {
		t.Fatalf("DoUpload file URL returned error: %v", err)
	}
	urlPath := filepath.Join(tmp, "url", "payload.txt")
	got, err = os.ReadFile(urlPath)
	if err != nil {
		t.Fatalf("ReadFile file URL returned error: %v", err)
	}
	if string(got) != body {
		t.Fatalf("unexpected file URL payload %q", got)
	}

	if _, err := DoUpload(ctx, req, "", strings.NewReader("x"), 1); err == nil || !strings.Contains(err.Error(), "invalid file upload url") {
		t.Fatalf("expected invalid file upload url error, got %v", err)
	}
}

func TestDoUploadHTTPModesAndErrors(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	t.Run("azure signed put sets headers and trims etag", func(t *testing.T) {
		req := &captureRequester{resp: &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Etag": []string{"\"etag-123\""}}, Body: io.NopCloser(strings.NewReader("ok"))}}
		urlStr := "https://acct.blob.core.windows.net/container/blob?sr=b&sig=sig&sv=2024-01-01&X-Amz-Signature=1"
		etag, err := DoUpload(ctx, req, urlStr, strings.NewReader("payload"), 7)
		if err != nil {
			t.Fatalf("DoUpload returned error: %v", err)
		}
		if etag != "etag-123" {
			t.Fatalf("expected trimmed etag, got %q", etag)
		}
		if req.method != http.MethodPut {
			t.Fatalf("expected PUT method, got %s", req.method)
		}
		if req.request.Header.Get("x-ms-blob-type") != "BlockBlob" {
			t.Fatalf("expected azure blob header, got %s", req.request.Header.Get("x-ms-blob-type"))
		}
		if req.request.Header.Get(request.SkipAuthHeader) != "true" {
			t.Fatal("expected skip auth for signed URL")
		}
		if req.request.ContentLength != 7 {
			t.Fatalf("expected content length 7, got %d", req.request.ContentLength)
		}
	})

	t.Run("gcs media upload uses post", func(t *testing.T) {
		req := &captureRequester{resp: &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("ok"))}}
		urlStr := "https://storage.googleapis.com/upload/storage/v1/b/my-bucket/o?uploadType=media&name=obj.txt&X-Goog-Signature=sig"
		if _, err := DoUpload(ctx, req, urlStr, strings.NewReader("payload"), 0); err != nil {
			t.Fatalf("DoUpload returned error: %v", err)
		}
		if req.method != http.MethodPost {
			t.Fatalf("expected POST for gcs media upload, got %s", req.method)
		}
		if _, ok := req.request.Header["X-Ms-Blob-Type"]; ok {
			t.Fatalf("did not expect azure header in gcs mode, got %+v", req.request.Header)
		}
	})

	t.Run("request and status errors bubble up", func(t *testing.T) {
		reqErr := &captureRequester{err: errors.New("boom")}
		if _, err := DoUpload(ctx, reqErr, "https://example.test/upload", strings.NewReader("x"), 1); err == nil || !strings.Contains(err.Error(), "upload to https://example.test/upload failed") {
			t.Fatalf("expected wrapped requester error, got %v", err)
		}

		statusErr := &captureRequester{resp: &http.Response{StatusCode: http.StatusForbidden, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("denied"))}}
		if _, err := DoUpload(ctx, statusErr, "https://example.test/upload", strings.NewReader("x"), 1); err == nil || !strings.Contains(err.Error(), "status 403 body=denied") {
			t.Fatalf("expected status/body error, got %v", err)
		}
	})
}

func TestGenericDownloadOptions(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	start := int64(3)
	end := int64(9)
	resp := &http.Response{StatusCode: http.StatusPartialContent, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("ok"))}
	req := &captureRequester{resp: resp}
	urlStr := "https://download.example/file?Expires=1700000000"

	got, err := GenericDownload(ctx, req, urlStr, &start, &end)
	if err != nil {
		t.Fatalf("GenericDownload returned error: %v", err)
	}
	if got != resp {
		t.Fatal("expected returned response pointer")
	}
	if req.method != http.MethodGet {
		t.Fatalf("expected GET method, got %s", req.method)
	}
	if req.request.Header.Get("Range") != "bytes=3-9" {
		t.Fatalf("unexpected Range header: %s", req.request.Header.Get("Range"))
	}
	if req.request.Header.Get(request.SkipAuthHeader) != "true" {
		t.Fatal("expected skip auth for signed URL")
	}

	noEnd := &captureRequester{resp: resp}
	if _, err := GenericDownload(ctx, noEnd, "https://download.example/file", &start, nil); err != nil {
		t.Fatalf("GenericDownload without end returned error: %v", err)
	}
	if noEnd.request.Header.Get("Range") != "bytes=3-" {
		t.Fatalf("unexpected open-ended range header: %s", noEnd.request.Header.Get("Range"))
	}
	if noEnd.request.Header.Get(request.SkipAuthHeader) == "true" {
		t.Fatal("did not expect skip auth for non-presigned URL")
	}
}

func TestSignedURLHelpers(t *testing.T) {
	t.Parallel()

	azureURL, _ := url.Parse("https://acct.blob.core.windows.net/c/blob?sr=b&sig=s&sv=2024")
	if !needsAzureBlobTypeHeader(azureURL) {
		t.Fatal("expected azure blob type header requirement")
	}
	if needsAzureBlobTypeHeader(nil) {
		t.Fatal("nil URL should not require azure header")
	}
	noSig, _ := url.Parse("https://acct.blob.core.windows.net/c/blob?sr=b&sv=2024")
	if needsAzureBlobTypeHeader(noSig) {
		t.Fatal("missing sig should not require azure header")
	}
	withComp, _ := url.Parse("https://acct.blob.core.windows.net/c/blob?sr=b&sv=2024&sig=s&comp=block")
	if needsAzureBlobTypeHeader(withComp) {
		t.Fatal("comp query should disable azure blob header")
	}

	gcsURL, _ := url.Parse("https://storage.googleapis.com/upload/storage/v1/b/bucket/o?uploadType=media&name=object")
	if !useGCSJSONMediaUpload(gcsURL) {
		t.Fatal("expected gcs media upload detection")
	}
	noName, _ := url.Parse("https://storage.googleapis.com/upload/storage/v1/b/bucket/o?uploadType=media")
	if useGCSJSONMediaUpload(noName) {
		t.Fatal("missing name should disable gcs media upload")
	}
	wrongPath, _ := url.Parse("https://storage.googleapis.com/storage/v1/b/bucket/o?uploadType=media&name=object")
	if useGCSJSONMediaUpload(wrongPath) {
		t.Fatal("wrong path should disable gcs media upload")
	}
}
