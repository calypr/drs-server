package download

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	internalapi "github.com/calypr/syfon/apigen/client/internalapi"
	"github.com/calypr/syfon/client/common"
	"github.com/calypr/syfon/client/logs"
	"github.com/calypr/syfon/client/transfer"
)

type zeroBackoff struct{}

func (zeroBackoff) WaitTime(int) time.Duration { return 0 }

func transferTestZeroBackoff() transfer.RetryStrategy { return zeroBackoff{} }

type fakeBackend struct {
	logger                 *logs.Gen3Logger
	doFunc                 func(context.Context, string, *int64, *int64) (*http.Response, error)
	resolveDownloadURLFunc func(context.Context, string, string) (string, error)
	data                   []byte
	size                   int64
}

func (f *fakeBackend) Name() string                    { return "Fake" }
func (f *fakeBackend) Logger() transfer.TransferLogger { return f.logger }
func (f *fakeBackend) Validate(ctx context.Context, bucket string) error {
	return nil
}

func (f *fakeBackend) Stat(ctx context.Context, guid string) (*transfer.ObjectMetadata, error) {
	size := f.size
	if size == 0 {
		size = int64(len(f.data))
	}
	return &transfer.ObjectMetadata{Size: size, AcceptRanges: true, Provider: f.Name()}, nil
}

func (f *fakeBackend) GetReader(ctx context.Context, guid string) (io.ReadCloser, error) {
	resp, err := f.Download(ctx, "", nil, nil)
	if err != nil {
		return nil, err
	}
	return resp.Body, nil
}

func (f *fakeBackend) GetRangeReader(ctx context.Context, guid string, offset, length int64) (io.ReadCloser, error) {
	var end *int64
	if length > 0 {
		e := offset + length - 1
		end = &e
	}
	resp, err := f.Download(ctx, "", &offset, end)
	if err != nil {
		return nil, err
	}
	if offset > 0 && resp.StatusCode == http.StatusOK {
		resp.Body.Close()
		return nil, transfer.ErrRangeIgnored
	}
	return resp.Body, nil
}

func (f *fakeBackend) GetWriter(ctx context.Context, guid string) (io.WriteCloser, error) {
	return nil, errors.New("not implemented")
}

func (f *fakeBackend) MultipartInit(ctx context.Context, guid string) (string, error) {
	return "", errors.New("not implemented")
}

func (f *fakeBackend) MultipartPart(ctx context.Context, guid string, uploadID string, partNum int, body io.Reader) (string, error) {
	return "", errors.New("not implemented")
}

func (f *fakeBackend) MultipartComplete(ctx context.Context, guid string, uploadID string, parts []transfer.MultipartPart) error {
	return errors.New("not implemented")
}

func (f *fakeBackend) Delete(ctx context.Context, guid string) error {
	return errors.New("not implemented")
}

func (f *fakeBackend) resolvedObject(guid string) *transfer.ResolvedObject {
	size := f.size
	if size == 0 && len(f.data) > 0 {
		size = int64(len(f.data))
	}
	if size == 0 {
		size = 64
	}
	return &transfer.ResolvedObject{
		Id:           guid,
		Name:         "payload.bin",
		Size:         size,
		ProviderURL:  "https://download.example.com/object",
		AccessMethod: "https",
	}
}

func (f *fakeBackend) ResolveDownloadURL(ctx context.Context, guid string, accessID string) (string, error) {
	if f.resolveDownloadURLFunc != nil {
		return f.resolveDownloadURLFunc(ctx, guid, accessID)
	}
	if guid == "test-fallback" {
		return "", errors.New("fallback")
	}
	return "https://download.example.com/object", nil
}

func (f *fakeBackend) ResolveUploadURL(ctx context.Context, guid string, filename string, metadata common.FileMetadata, bucket string) (string, error) {
	return "", errors.New("not implemented")
}

func (f *fakeBackend) InitMultipartUpload(ctx context.Context, guid string, filename string, bucket string) (string, string, error) {
	return "", "", errors.New("not implemented")
}

func (f *fakeBackend) GetMultipartUploadURL(ctx context.Context, key string, uploadID string, partNumber int32, bucket string) (string, error) {
	return "", errors.New("not implemented")
}

func (f *fakeBackend) CompleteMultipartUpload(ctx context.Context, key string, uploadID string, parts []internalapi.InternalMultipartPart, bucket string) error {
	return errors.New("not implemented")
}

func (f *fakeBackend) Upload(ctx context.Context, url string, body io.Reader, size int64) error {
	return errors.New("not implemented")
}

func (f *fakeBackend) UploadPart(ctx context.Context, url string, body io.Reader, size int64) (string, error) {
	return "", errors.New("not implemented")
}

func (f *fakeBackend) DeleteFile(ctx context.Context, guid string) (string, error) {
	return "", errors.New("not implemented")
}

func (f *fakeBackend) Download(ctx context.Context, url string, rangeStart, rangeEnd *int64) (*http.Response, error) {
	if f.doFunc != nil {
		return f.doFunc(ctx, url, rangeStart, rangeEnd)
	}
	if rangeStart != nil && rangeEnd != nil {
		start, end := *rangeStart, *rangeEnd
		if start < 0 || end >= int64(len(f.data)) || start > end {
			return nil, errors.New("invalid range")
		}
		return newDownloadResponse(url, f.data[start:end+1], http.StatusPartialContent), nil
	}
	if rangeStart != nil {
		start := *rangeStart
		if start < 0 || start > int64(len(f.data)) {
			return nil, errors.New("invalid resume range")
		}
		if start == int64(len(f.data)) {
			return newDownloadResponse(url, []byte{}, http.StatusPartialContent), nil
		}
		return newDownloadResponse(url, f.data[start:], http.StatusPartialContent), nil
	}
	return newDownloadResponse(url, f.data, http.StatusOK), nil
}

type fakeResolver struct {
	backend *fakeBackend
}

func (f *fakeResolver) Resolve(ctx context.Context, id string) (*transfer.ResolvedObject, error) {
	return f.backend.resolvedObject(id), nil
}

func (f *fakeResolver) Name() string {
	return f.backend.Name()
}

func (f *fakeResolver) Logger() transfer.TransferLogger {
	return f.backend.Logger()
}

func newDownloadJSONResponse(rawURL, body string) *http.Response {
	parsedURL, err := url.Parse(rawURL)
	if err != nil {
		parsedURL = &url.URL{}
	}
	return &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader(body)),
		Request:    &http.Request{URL: parsedURL},
		Header:     make(http.Header),
	}
}

func TestDownloadToPathMultipart(t *testing.T) {
	payload := bytes.Repeat([]byte("z"), 2*1024*1024) // 2MB
	tmpDir := t.TempDir()
	dst := filepath.Join(tmpDir, "multipart.bin")

	fake := &fakeBackend{
		logger: logs.NewGen3Logger(nil, "", ""),
		data:   payload,
		size:   int64(len(payload)),
	}
	err := DownloadToPathWithOptions(
		context.Background(),
		fake,
		"guid-789",
		dst,
		DownloadOptions{
			MultipartThreshold: 1 * 1024 * 1024,
			ChunkSize:          256 * 1024,
			Concurrency:        4,
			RetryStrategy:      transferTestZeroBackoff(),
		},
	)
	if err != nil {
		t.Fatalf("multipart download failed: %v", err)
	}

	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatalf("read failed: %v", err)
	}
	if !bytes.Equal(payload, got) {
		t.Fatal("downloaded payload mismatch")
	}
}

func TestDownloadToPathMultipartUsesProtocolAccessID(t *testing.T) {
	payload := bytes.Repeat([]byte("p"), 2*1024*1024)
	tmpDir := t.TempDir()
	dst := filepath.Join(tmpDir, "multipart-protocol.bin")

	fake := &fakeBackend{
		logger: logs.NewGen3Logger(nil, "", ""),
		data:   payload,
		size:   int64(len(payload)),
		resolveDownloadURLFunc: func(_ context.Context, _ string, accessID string) (string, error) {
			return "https://download.example.com/object", nil
		},
	}
	err := DownloadToPathWithOptions(
		context.Background(),
		fake,
		"guid-protocol",
		dst,
		DownloadOptions{
			MultipartThreshold: 1 * 1024 * 1024,
			ChunkSize:          256 * 1024,
			Concurrency:        4,
			RetryStrategy:      transferTestZeroBackoff(),
		},
	)
	if err != nil {
		t.Fatalf("multipart download failed: %v", err)
	}
}

func TestDownloadToPathMultipartErrorPropagation(t *testing.T) {
	payload := bytes.Repeat([]byte("e"), 2*1024*1024)
	tmpDir := t.TempDir()
	dst := filepath.Join(tmpDir, "multipart-error.bin")

	fake := &fakeBackend{
		logger: logs.NewGen3Logger(nil, "", ""),
		data:   payload,
		size:   int64(len(payload)),
		doFunc: func(_ context.Context, url string, rangeStart, rangeEnd *int64) (*http.Response, error) {
			if rangeStart != nil && rangeEnd != nil && *rangeStart == 256*1024 {
				return nil, errors.New("boom")
			}
			if rangeStart != nil && rangeEnd != nil {
				start, end := *rangeStart, *rangeEnd
				return newDownloadResponse(url, payload[start:end+1], http.StatusPartialContent), nil
			}
			return newDownloadResponse(url, payload, http.StatusOK), nil
		},
	}
	err := DownloadToPathWithOptions(
		context.Background(),
		fake,
		"guid-multipart-error",
		dst,
		DownloadOptions{
			MultipartThreshold: 1 * 1024 * 1024,
			ChunkSize:          256 * 1024,
			Concurrency:        4,
			RetryStrategy:      transferTestZeroBackoff(),
		},
	)
	if err == nil {
		t.Fatal("expected multipart download error")
	}
	if !strings.Contains(err.Error(), "range download") {
		t.Fatalf("expected range error context, got: %v", err)
	}
}

func TestDownloadToPathMultipartProgressAccounting(t *testing.T) {
	payload := bytes.Repeat([]byte("q"), 2*1024*1024)
	tmpDir := t.TempDir()
	dst := filepath.Join(tmpDir, "multipart-progress.bin")

	var (
		mu     sync.Mutex
		events []common.ProgressEvent
	)
	progress := func(event common.ProgressEvent) error {
		mu.Lock()
		defer mu.Unlock()
		events = append(events, event)
		return nil
	}

	fake := &fakeBackend{
		logger: logs.NewGen3Logger(nil, "", ""),
		data:   payload,
		size:   int64(len(payload)),
	}
	ctx := common.WithProgress(context.Background(), progress)

	err := DownloadToPathWithOptions(
		ctx,
		fake,
		"guid-progress",
		dst,
		DownloadOptions{
			MultipartThreshold: 1 * 1024 * 1024,
			ChunkSize:          256 * 1024,
			Concurrency:        4,
			RetryStrategy:      transferTestZeroBackoff(),
		},
	)
	if err != nil {
		t.Fatalf("multipart download failed: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(events) == 0 {
		t.Fatal("expected progress events")
	}

	var sum int64
	for _, event := range events {
		sum += event.BytesSinceLast
	}
	if sum != int64(len(payload)) {
		t.Fatalf("expected progress sum %d, got %d", len(payload), sum)
	}

	last := events[len(events)-1]
	if last.BytesSoFar != int64(len(payload)) {
		t.Fatalf("expected final bytesSoFar %d, got %d", len(payload), last.BytesSoFar)
	}
}

func TestDownloadToPathSingleResumeFromPartial(t *testing.T) {
	payload := bytes.Repeat([]byte("r"), 1024)
	tmpDir := t.TempDir()
	dst := filepath.Join(tmpDir, "resume.bin")
	prefix := payload[:300]
	if err := os.WriteFile(dst, prefix, 0o666); err != nil {
		t.Fatalf("write partial file: %v", err)
	}

	var gotRange int64 = -1
	fake := &fakeBackend{
		logger: logs.NewGen3Logger(nil, "", ""),
		data:   payload,
		size:   int64(len(payload)),
		doFunc: func(_ context.Context, url string, rangeStart, rangeEnd *int64) (*http.Response, error) {
			if rangeStart == nil {
				gotRange = 0
			} else {
				gotRange = *rangeStart
			}
			if gotRange <= 0 {
				return nil, errors.New("expected resume range")
			}
			return newDownloadResponse(url, payload[gotRange:], http.StatusPartialContent), nil
		},
	}
	err := DownloadToPathWithOptions(
		context.Background(),
		fake,
		"guid-resume",
		dst,
		DownloadOptions{
			MultipartThreshold: 1 * common.GB, // force single-stream path
			ChunkSize:          64 * common.MB,
			Concurrency:        2,
		},
	)
	if err != nil {
		t.Fatalf("resume download failed: %v", err)
	}
	if gotRange != int64(len(prefix)) {
		t.Fatalf("expected range %d, got %d", len(prefix), gotRange)
	}

	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatalf("read result: %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatal("resumed file mismatch")
	}
}

func TestDownloadToPathSingleRangeIgnoredRestarts(t *testing.T) {
	payload := bytes.Repeat([]byte("k"), 2048)
	tmpDir := t.TempDir()
	dst := filepath.Join(tmpDir, "range-ignored.bin")
	if err := os.WriteFile(dst, payload[:500], 0o666); err != nil {
		t.Fatalf("write partial: %v", err)
	}

	fake := &fakeBackend{
		logger: logs.NewGen3Logger(nil, "", ""),
		data:   payload,
		size:   int64(len(payload)),
		doFunc: func(_ context.Context, url string, rangeStart, rangeEnd *int64) (*http.Response, error) {
			// Simulate server ignoring Range and returning full body with 200.
			// The first attempt will HAVE a rangeStart, the second attempt (fallback) will NOT.
			if rangeStart != nil && *rangeStart > 0 {
				return newDownloadResponse(url, payload, http.StatusOK), nil
			}
			// Fallback attempt or initial full download
			return newDownloadResponse(url, payload, http.StatusOK), nil
		},
	}
	err := DownloadToPathWithOptions(
		context.Background(),
		fake,
		"guid-range-ignored",
		dst,
		DownloadOptions{MultipartThreshold: 1 * common.GB, ChunkSize: 64 * common.MB, Concurrency: 2},
	)
	if err != nil {
		t.Fatalf("download failed: %v", err)
	}

	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatalf("read result: %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatal("range-ignored restart did not produce full file")
	}
}

func TestDownloadToPathAlreadyCompleteSkipsDownload(t *testing.T) {
	payload := bytes.Repeat([]byte("c"), 512)
	tmpDir := t.TempDir()
	dst := filepath.Join(tmpDir, "complete.bin")
	if err := os.WriteFile(dst, payload, 0o666); err != nil {
		t.Fatalf("write complete file: %v", err)
	}

	calls := 0
	fake := &fakeBackend{
		logger: logs.NewGen3Logger(nil, "", ""),
		data:   payload,
		size:   int64(len(payload)),
		doFunc: func(_ context.Context, _ string, _ *int64, _ *int64) (*http.Response, error) {
			calls++
			return newDownloadResponse("https://download.example.com/object", payload, http.StatusOK), nil
		},
	}
	err := DownloadToPathWithOptions(
		context.Background(),
		fake,
		"guid-complete",
		dst,
		DownloadOptions{MultipartThreshold: 1 * common.GB, ChunkSize: 64 * common.MB, Concurrency: 2},
	)
	if err != nil {
		t.Fatalf("download call failed: %v", err)
	}
	if calls != 0 {
		t.Fatalf("expected no backend download calls, got %d", calls)
	}
}

func newDownloadResponse(rawURL string, payload []byte, status int) *http.Response {
	parsedURL, err := url.Parse(rawURL)
	if err != nil {
		parsedURL = &url.URL{}
	}
	return &http.Response{
		StatusCode:    status,
		Body:          io.NopCloser(bytes.NewReader(payload)),
		ContentLength: int64(len(payload)),
		Request:       &http.Request{URL: parsedURL},
		Header:        make(http.Header),
	}
}
