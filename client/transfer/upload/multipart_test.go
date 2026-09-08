package upload

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	internalapi "github.com/calypr/syfon/apigen/internalapi"
	"github.com/calypr/syfon/client/common"
	conf "github.com/calypr/syfon/client/config"
	"github.com/calypr/syfon/client/logs"
	"github.com/calypr/syfon/client/request"
	"github.com/calypr/syfon/client/transfer"
	"github.com/calypr/syfon/client/transfer/engine"
)

type fakeGen3Upload struct {
	cred   *conf.Credential
	logger *logs.Gen3Logger
	doFunc func(context.Context, *request.RequestBuilder) (*http.Response, error)
}

func (f *fakeGen3Upload) Name() string                    { return "fake" }
func (f *fakeGen3Upload) Logger() transfer.TransferLogger { return f.logger }

func (f *fakeGen3Upload) Validate(ctx context.Context, bucket string) error { return nil }

func (f *fakeGen3Upload) Stat(ctx context.Context, guid string) (*transfer.ObjectMetadata, error) {
	return &transfer.ObjectMetadata{Size: 0, AcceptRanges: true, Provider: "fake"}, nil
}

func (f *fakeGen3Upload) GetReader(ctx context.Context, guid string) (io.ReadCloser, error) {
	return io.NopCloser(strings.NewReader("")), nil
}

func (f *fakeGen3Upload) GetRangeReader(ctx context.Context, guid string, offset, length int64) (io.ReadCloser, error) {
	return io.NopCloser(strings.NewReader("")), nil
}

func (f *fakeGen3Upload) GetWriter(ctx context.Context, guid string) (io.WriteCloser, error) {
	return nil, fmt.Errorf("not implemented")
}

func (f *fakeGen3Upload) Delete(ctx context.Context, guid string) error { return nil }

func (f *fakeGen3Upload) Do(ctx context.Context, req *request.RequestBuilder) (*http.Response, error) {
	return f.doFunc(ctx, req)
}
func (f *fakeGen3Upload) New(method, url string) *request.RequestBuilder {
	return &request.RequestBuilder{Method: method, Url: url}
}

func (f *fakeGen3Upload) ResolveUploadURL(ctx context.Context, guid string, filename string, metadata common.FileMetadata, bucket string) (string, error) {
	return "", fmt.Errorf("not implemented")
}
func (f *fakeGen3Upload) InitMultipartUpload(ctx context.Context, guid string, filename string, bucket string) (string, string, error) {
	resp, err := f.Do(ctx, &request.RequestBuilder{Url: common.DataMultipartInitEndpoint})
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()
	var msg struct {
		UploadID string `json:"uploadId"`
		GUID     string `json:"guid"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&msg); err != nil {
		return "", "", err
	}
	return msg.UploadID, msg.GUID, nil
}
func (f *fakeGen3Upload) GetMultipartUploadURL(ctx context.Context, key string, uploadID string, partNumber int32, bucket string) (string, error) {
	resp, err := f.Do(ctx, &request.RequestBuilder{Url: common.DataMultipartUploadEndpoint})
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var msg struct {
		PresignedURL string `json:"presigned_url"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&msg); err != nil {
		return "", err
	}
	return msg.PresignedURL, nil
}
func (f *fakeGen3Upload) CompleteMultipartUpload(ctx context.Context, key string, uploadID string, parts []internalapi.InternalMultipartPart, bucket string) error {
	_, err := f.Do(ctx, &request.RequestBuilder{Url: common.DataMultipartCompleteEndpoint})
	return err
}

func (f *fakeGen3Upload) MultipartInit(ctx context.Context, guid string) (string, error) {
	uploadID, _, err := f.InitMultipartUpload(ctx, guid, "", "")
	return uploadID, err
}

func (f *fakeGen3Upload) MultipartPart(ctx context.Context, guid string, uploadID string, partNum int, body io.Reader) (string, error) {
	url, err := f.GetMultipartUploadURL(ctx, guid, uploadID, int32(partNum), "")
	if err != nil {
		return "", err
	}
	return f.UploadPart(ctx, url, body, -1)
}

func (f *fakeGen3Upload) MultipartComplete(ctx context.Context, guid string, uploadID string, parts []transfer.MultipartPart) error {
	converted := make([]internalapi.InternalMultipartPart, 0, len(parts))
	for _, part := range parts {
		converted = append(converted, internalapi.InternalMultipartPart{
			PartNumber: part.PartNumber,
			ETag:       part.ETag,
		})
	}
	return f.CompleteMultipartUpload(ctx, guid, uploadID, converted, "")
}
func (f *fakeGen3Upload) Upload(ctx context.Context, url string, body io.Reader, size int64) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, url, body)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("upload failed: %s", resp.Status)
	}
	return nil
}
func (f *fakeGen3Upload) UploadPart(ctx context.Context, url string, body io.Reader, size int64) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, url, body)
	if err != nil {
		return "", err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("upload part failed: %s", resp.Status)
	}
	return resp.Header.Get("ETag"), nil
}
func (f *fakeGen3Upload) DeleteFile(ctx context.Context, guid string) (string, error) {
	return "", nil
}
func (f *fakeGen3Upload) CanonicalObjectURL(signedURL, bucketHint, fallbackDID string) (string, error) {
	return signedURL, nil
}

func TestMultipartUploadProgressIntegration(t *testing.T) {
	ctx := context.Background()
	tmp := t.TempDir()
	t.Setenv("DATA_CLIENT_CACHE_DIR", tmp)
	origTransport := http.DefaultClient.Transport
	http.DefaultClient.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.Method != http.MethodPut {
			return &http.Response{
				StatusCode: http.StatusMethodNotAllowed,
				Body:       io.NopCloser(strings.NewReader("method not allowed")),
				Header:     make(http.Header),
				Request:    req,
			}, nil
		}
		_, _ = io.Copy(io.Discard, req.Body)
		_ = req.Body.Close()
		h := make(http.Header)
		h.Set("ETag", "etag-123")
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader("")),
			Header:     h,
			Request:    req,
		}, nil
	})
	defer func() { http.DefaultClient.Transport = origTransport }()

	file, err := os.CreateTemp(t.TempDir(), "multipart-*.bin")
	if err != nil {
		t.Fatalf("create temp file: %v", err)
	}
	defer file.Close()

	fileSize := int64(101 * common.MB)
	if err := file.Truncate(fileSize); err != nil {
		t.Fatalf("truncate file: %v", err)
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		t.Fatalf("seek file: %v", err)
	}

	var (
		events []common.ProgressEvent
		mu     sync.Mutex
	)
	progress := func(event common.ProgressEvent) error {
		mu.Lock()
		defer mu.Unlock()
		events = append(events, event)
		return nil
	}

	logger := logs.NewGen3Logger(nil, "", "")
	fake := &fakeGen3Upload{
		cred: &conf.Credential{
			APIEndpoint: "https://example.com",
			AccessToken: "token",
		},
		logger: logger,
		doFunc: func(_ context.Context, req *request.RequestBuilder) (*http.Response, error) {
			switch {
			case strings.Contains(req.Url, common.DataMultipartInitEndpoint):
				return newJSONResponse(req.Url, `{"uploadId":"upload-123","guid":"guid-123"}`), nil
			case strings.Contains(req.Url, common.DataMultipartUploadEndpoint):
				return newJSONResponse(req.Url, fmt.Sprintf(`{"presigned_url":"%s"}`, "https://upload.invalid/part")), nil
			case strings.Contains(req.Url, common.DataMultipartCompleteEndpoint):
				return newJSONResponse(req.Url, `{}`), nil
			default:
				return nil, fmt.Errorf("unexpected request url: %s", req.Url)
			}
		},
	}

	ctx = common.WithProgress(ctx, progress)
	ctx = common.WithOid(ctx, "guid-123")

	uploader := &engine.GenericUploader{Backend: fake}
	req := transfer.TransferRequest{
		SourcePath:     file.Name(),
		ObjectKey:      "multipart.bin",
		GUID:           "guid-123",
		Bucket:         "bucket",
		Metadata:       common.FileMetadata{},
		ForceMultipart: true,
	}
	if err := uploader.Upload(ctx, req, false); err != nil {
		t.Fatalf("multipart upload failed: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(events) == 0 {
		t.Fatal("expected progress events")
	}
	for i := 1; i < len(events); i++ {
		if events[i].BytesSoFar < events[i-1].BytesSoFar {
			t.Fatalf("bytesSoFar not monotonic: %d then %d", events[i-1].BytesSoFar, events[i].BytesSoFar)
		}
	}
	last := events[len(events)-1]
	if last.BytesSoFar != fileSize {
		t.Fatalf("expected final bytesSoFar %d, got %d", fileSize, last.BytesSoFar)
	}
}

func TestMultipartUploadResumesWithoutReinit(t *testing.T) {
	ctx := context.Background()

	origTransport := http.DefaultClient.Transport
	http.DefaultClient.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.Method != http.MethodPut {
			return &http.Response{
				StatusCode: http.StatusMethodNotAllowed,
				Body:       io.NopCloser(strings.NewReader("method not allowed")),
				Header:     make(http.Header),
				Request:    req,
			}, nil
		}
		_, _ = io.Copy(io.Discard, req.Body)
		_ = req.Body.Close()
		h := make(http.Header)
		h.Set("ETag", "etag-1")
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader("")),
			Header:     h,
			Request:    req,
		}, nil
	})
	defer func() { http.DefaultClient.Transport = origTransport }()

	tmp := t.TempDir()
	t.Setenv("DATA_CLIENT_CACHE_DIR", tmp)
	path := filepath.Join(tmp, "large.bin")
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("create temp file: %v", err)
	}
	// Sparse file >100MB triggers multipart with multiple parts.
	if err := f.Truncate(120 * common.MB); err != nil {
		_ = f.Close()
		t.Fatalf("truncate temp file: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close temp file: %v", err)
	}

	initCalls := 0
	completeCalls := 0
	failComplete := true
	logger := logs.NewGen3Logger(nil, "", "")
	fake := &fakeGen3Upload{
		cred: &conf.Credential{
			APIEndpoint: "https://example.com",
			AccessToken: "token",
		},
		logger: logger,
		doFunc: func(_ context.Context, req *request.RequestBuilder) (*http.Response, error) {
			switch {
			case strings.Contains(req.Url, common.DataMultipartInitEndpoint):
				initCalls++
				return newJSONResponse(req.Url, `{"uploadId":"upload-resume-1","guid":"guid-resume-1"}`), nil
			case strings.Contains(req.Url, common.DataMultipartUploadEndpoint):
				return newJSONResponse(req.Url, `{"presigned_url":"https://upload.invalid/part"}`), nil
			case strings.Contains(req.Url, common.DataMultipartCompleteEndpoint):
				if failComplete {
					failComplete = false
					return nil, fmt.Errorf("simulated failure")
				}
				completeCalls++
				return newJSONResponse(req.Url, `{}`), nil
			default:
				return nil, fmt.Errorf("unexpected request url: %s", req.Url)
			}
		},
	}

	checkpointPath, err := engine.CheckpointPath(path, "guid-resume-1")
	if err != nil {
		t.Fatalf("checkpoint path: %v", err)
	}
	_ = os.Remove(checkpointPath)

	uploader := &engine.GenericUploader{Backend: fake}
	req := transfer.TransferRequest{
		SourcePath:     path,
		ObjectKey:      "resume.bin",
		GUID:           "guid-resume-1",
		Bucket:         "bucket",
		Metadata:       common.FileMetadata{},
		ForceMultipart: true,
	}
	err = uploader.Upload(ctx, req, false)
	if err == nil {
		t.Fatal("expected first multipart upload to fail")
	}
	if initCalls != 1 {
		t.Fatalf("expected one init after first run, got %d", initCalls)
	}
	if _, statErr := os.Stat(checkpointPath); statErr != nil {
		t.Fatalf("expected checkpoint to exist after failure: %v", statErr)
	}

	err = uploader.Upload(ctx, req, false)
	if err != nil {
		t.Fatalf("resume multipart upload failed: %v", err)
	}

	if initCalls != 1 {
		t.Fatalf("expected resume to reuse existing upload init; init calls = %d", initCalls)
	}
	if completeCalls != 1 {
		t.Fatalf("expected one complete call, got %d", completeCalls)
	}
	if _, statErr := os.Stat(checkpointPath); !os.IsNotExist(statErr) {
		t.Fatalf("expected checkpoint cleanup after success, stat err: %v", statErr)
	}
}

func newJSONResponse(rawURL, body string) *http.Response {
	parsedURL, err := url.Parse(rawURL)
	if err != nil {
		parsedURL = &url.URL{}
	}
	return &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(bytes.NewBufferString(body)),
		Request:    &http.Request{URL: parsedURL},
		Header:     make(http.Header),
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}
