package engine

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/calypr/syfon/client/common"
	"github.com/calypr/syfon/client/transfer"
)

type zeroRetryStrategy struct{}

func (zeroRetryStrategy) WaitTime(int) time.Duration { return 0 }

type fakeBackend struct {
	mu                 sync.Mutex
	meta               *transfer.ObjectMetadata
	statErr            error
	data               []byte
	getReaderErr       error
	rangeErr           error
	rangeIgnoredOnce   bool
	rangeCalls         [][2]int64
	uploaded           []byte
	uploadSize         int64
	uploadKey          string
	resolveUploadKey   string
	multipartInitID    string
	multipartInitCalls int
	multipartInitKey   string
	partUploads        map[int][]byte
	partEtags          map[int]string
	partFailures       map[int]int
	completedKey       string
	completedUploadID  string
	completedParts     []transfer.MultipartPart
	completeErr        error
	uploadChunkSize    int
	uploadChunkDelay   time.Duration
	uploadDone         func()
	partChunkSize      int
	partChunkDelay     time.Duration
	completeGate       <-chan struct{}
}

func (f *fakeBackend) Name() string { return "fake-backend" }

func (f *fakeBackend) Logger() transfer.TransferLogger { return transfer.NoOpLogger{} }

func (f *fakeBackend) Validate(ctx context.Context, bucket string) error { return nil }

func (f *fakeBackend) Stat(ctx context.Context, guid string) (*transfer.ObjectMetadata, error) {
	if f.statErr != nil {
		return nil, f.statErr
	}
	if f.meta == nil {
		return &transfer.ObjectMetadata{}, nil
	}
	return f.meta, nil
}

func (f *fakeBackend) GetReader(ctx context.Context, guid string) (io.ReadCloser, error) {
	if f.getReaderErr != nil {
		return nil, f.getReaderErr
	}
	return io.NopCloser(bytes.NewReader(f.data)), nil
}

func (f *fakeBackend) GetRangeReader(ctx context.Context, guid string, offset, length int64) (io.ReadCloser, error) {
	f.mu.Lock()
	f.rangeCalls = append(f.rangeCalls, [2]int64{offset, length})
	f.mu.Unlock()
	if f.rangeIgnoredOnce {
		f.rangeIgnoredOnce = false
		return nil, transfer.ErrRangeIgnored
	}
	if f.rangeErr != nil {
		return nil, f.rangeErr
	}
	if offset < 0 {
		offset = 0
	}
	if offset > int64(len(f.data)) {
		offset = int64(len(f.data))
	}
	end := int64(len(f.data))
	if length > 0 && offset+length < end {
		end = offset + length
	}
	return io.NopCloser(bytes.NewReader(f.data[offset:end])), nil
}

func (f *fakeBackend) GetWriter(ctx context.Context, guid string) (io.WriteCloser, error) {
	return nil, fmt.Errorf("unused")
}

func (f *fakeBackend) ResolveUploadURL(ctx context.Context, guid string, filename string, metadata common.FileMetadata, bucket string) (string, error) {
	f.resolveUploadKey = filename
	return "https://upload.example/" + filename, nil
}

func (f *fakeBackend) Upload(ctx context.Context, url string, body io.Reader, size int64) error {
	data, err := readAllChunked(body, f.uploadChunkSize, f.uploadChunkDelay)
	if err != nil {
		return err
	}
	f.uploadKey = url
	f.uploaded = append([]byte(nil), data...)
	f.uploadSize = size
	if f.uploadDone != nil {
		f.uploadDone()
	}
	return nil
}

func (f *fakeBackend) MultipartInit(ctx context.Context, key string) (string, error) {
	f.multipartInitCalls++
	f.multipartInitKey = key
	if f.multipartInitID == "" {
		f.multipartInitID = "upload-1"
	}
	return f.multipartInitID, nil
}

func (f *fakeBackend) MultipartPart(ctx context.Context, key string, uploadID string, partNum int, body io.Reader) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.partUploads == nil {
		f.partUploads = map[int][]byte{}
	}
	if f.partEtags == nil {
		f.partEtags = map[int]string{}
	}
	data, err := readAllChunked(body, f.partChunkSize, f.partChunkDelay)
	if err != nil {
		return "", err
	}
	if remaining := f.partFailures[partNum]; remaining > 0 {
		f.partFailures[partNum] = remaining - 1
		return "", fmt.Errorf("forced multipart failure for part %d", partNum)
	}
	f.partUploads[partNum] = data
	etag := f.partEtags[partNum]
	if etag == "" {
		etag = fmt.Sprintf("etag-%d", partNum)
	}
	return etag, nil
}

func (f *fakeBackend) MultipartComplete(ctx context.Context, key string, uploadID string, parts []transfer.MultipartPart) error {
	if f.completeGate != nil {
		<-f.completeGate
	}
	f.completedKey = key
	f.completedUploadID = uploadID
	f.completedParts = append([]transfer.MultipartPart(nil), parts...)
	return f.completeErr
}

func (f *fakeBackend) Delete(ctx context.Context, guid string) error { return nil }

func readAllChunked(r io.Reader, chunkSize int, delay time.Duration) ([]byte, error) {
	if chunkSize <= 0 {
		return io.ReadAll(r)
	}
	buf := make([]byte, chunkSize)
	var out bytes.Buffer
	for {
		n, err := r.Read(buf)
		if n > 0 {
			if _, writeErr := out.Write(buf[:n]); writeErr != nil {
				return nil, writeErr
			}
			if delay > 0 {
				time.Sleep(delay)
			}
		}
		if err != nil {
			if err == io.EOF {
				return out.Bytes(), nil
			}
			return nil, err
		}
	}
}

func TestChunkHelpers(t *testing.T) {
	if got := OptimalChunkSize(0); got != 1*common.MB {
		t.Fatalf("OptimalChunkSize(0) = %d, want %d", got, 1*common.MB)
	}
	if got := OptimalChunkSize(50 * common.MB); got != 50*common.MB {
		t.Fatalf("OptimalChunkSize for small file = %d, want file size", got)
	}
	if got := OptimalChunkSize(500 * common.MB); got != 10*common.MB {
		t.Fatalf("OptimalChunkSize mid file = %d, want %d", got, 10*common.MB)
	}
	if got := OptimalChunkSize(50 * common.GB); got != 256*common.MB {
		t.Fatalf("OptimalChunkSize large file = %d, want %d", got, 256*common.MB)
	}

	mid := scaleLinear(5*common.GB, 1*common.GB, 10*common.GB, 25*common.MB, 128*common.MB)
	if mid < 25*common.MB || mid > 128*common.MB || mid%common.MB != 0 {
		t.Fatalf("scaleLinear returned unexpected value %d", mid)
	}

	cache := t.TempDir()
	t.Setenv("DATA_CLIENT_CACHE_DIR", cache)
	p1, err := CheckpointPath("/tmp/source.bin", "guid-1")
	if err != nil {
		t.Fatalf("CheckpointPath returned error: %v", err)
	}
	p2, err := CheckpointPath("/tmp/source.bin", "guid-1")
	if err != nil {
		t.Fatalf("CheckpointPath returned error: %v", err)
	}
	if p1 != p2 {
		t.Fatalf("CheckpointPath should be stable, got %q and %q", p1, p2)
	}
	if !strings.HasPrefix(p1, filepath.Join(cache, "syfon", "multipart")) || !strings.HasSuffix(p1, ".json") {
		t.Fatalf("unexpected checkpoint path %q", p1)
	}

	stale := filepath.Join(cache, "syfon", "multipart", "stale.json")
	if err := os.WriteFile(stale, []byte(`{}`), 0o644); err != nil {
		t.Fatalf("write stale checkpoint: %v", err)
	}
	old := time.Now().Add(-25 * time.Hour)
	if err := os.Chtimes(stale, old, old); err != nil {
		t.Fatalf("age stale checkpoint: %v", err)
	}
	if _, err := CheckpointPath("/tmp/other.bin", "guid-2"); err != nil {
		t.Fatalf("CheckpointPath cleanup call returned error: %v", err)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatalf("expected stale checkpoint cleanup, stat err=%v", err)
	}
}

func TestGenericUploaderUploadSingle(t *testing.T) {
	t.Parallel()

	file := filepath.Join(t.TempDir(), "small.bin")
	content := []byte("hello uploader")
	if err := os.WriteFile(file, content, 0o644); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}

	backend := &fakeBackend{}
	uploader := &GenericUploader{Backend: backend}

	var events []common.ProgressEvent
	ctx := common.WithOid(context.Background(), "oid-1")
	ctx = common.WithProgress(ctx, func(ev common.ProgressEvent) error {
		events = append(events, ev)
		return nil
	})

	err := uploader.Upload(ctx, transfer.TransferRequest{SourcePath: file, GUID: "guid-1", ObjectKey: "object-1"}, true)
	if err != nil {
		t.Fatalf("Upload returned error: %v", err)
	}
	if backend.resolveUploadKey != "object-1" {
		t.Fatalf("unexpected resolved upload key %q", backend.resolveUploadKey)
	}
	if backend.uploadKey != "https://upload.example/object-1" {
		t.Fatalf("unexpected upload url %q", backend.uploadKey)
	}
	if !bytes.Equal(backend.uploaded, content) || backend.uploadSize != int64(len(content)) {
		t.Fatalf("unexpected uploaded payload size=%d body=%q", backend.uploadSize, backend.uploaded)
	}
	if len(events) == 0 {
		t.Fatal("expected progress events")
	}
	last := events[len(events)-1]
	if last.BytesSoFar != int64(len(content)) || last.Oid != "oid-1" {
		t.Fatalf("unexpected progress events: %+v", events)
	}
}

func TestGenericUploaderUploadSingleZeroByteStillEmitsCompletion(t *testing.T) {
	t.Parallel()

	file := filepath.Join(t.TempDir(), "empty.bin")
	if err := os.WriteFile(file, nil, 0o644); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}

	backend := &fakeBackend{}
	uploader := &GenericUploader{Backend: backend}

	var events []common.ProgressEvent
	ctx := common.WithOid(context.Background(), "oid-empty")
	ctx = common.WithProgress(ctx, func(ev common.ProgressEvent) error {
		events = append(events, ev)
		return nil
	})

	if err := uploader.Upload(ctx, transfer.TransferRequest{SourcePath: file, GUID: "guid-empty", ObjectKey: "object-empty"}, true); err != nil {
		t.Fatalf("Upload returned error: %v", err)
	}
	if len(events) == 0 {
		t.Fatal("expected progress event for zero-byte upload")
	}
	last := events[len(events)-1]
	if last.Oid != "oid-empty" || last.BytesSoFar != 0 || last.BytesSinceLast != 0 {
		t.Fatalf("unexpected zero-byte progress event: %+v", last)
	}
}

func TestGenericUploaderUploadSinglePropagatesFinalizeCallbackError(t *testing.T) {
	t.Parallel()

	file := filepath.Join(t.TempDir(), "small.bin")
	content := []byte("hello")
	if err := os.WriteFile(file, content, 0o644); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}

	backend := &fakeBackend{}
	uploader := &GenericUploader{Backend: backend}

	wantErr := fmt.Errorf("progress sink failed")
	ctx := common.WithOid(context.Background(), "oid-small")
	ctx = common.WithProgress(ctx, func(ev common.ProgressEvent) error {
		return wantErr
	})

	err := uploader.Upload(ctx, transfer.TransferRequest{SourcePath: file, GUID: "guid-small", ObjectKey: "object-small"}, true)
	if err == nil {
		t.Fatal("expected finalize callback error")
	}
	if !strings.Contains(err.Error(), wantErr.Error()) {
		t.Fatalf("expected error %q, got %v", wantErr, err)
	}
}

func TestGenericUploaderMultipartAndState(t *testing.T) {
	cache := t.TempDir()
	t.Setenv("DATA_CLIENT_CACHE_DIR", cache)

	file := filepath.Join(t.TempDir(), "large.bin")
	f, err := os.Create(file)
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}
	if err := f.Truncate(101 * common.MB); err != nil {
		_ = f.Close()
		t.Fatalf("Truncate returned error: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}

	backend := &fakeBackend{multipartInitID: "upload-123"}
	uploader := &GenericUploader{Backend: backend}

	var events []common.ProgressEvent
	ctx := common.WithOid(context.Background(), "oid-multipart")
	ctx = common.WithProgress(ctx, func(ev common.ProgressEvent) error {
		events = append(events, ev)
		return nil
	})

	err = uploader.Upload(ctx, transfer.TransferRequest{SourcePath: file, GUID: "guid-large", ObjectKey: "object-large", ForceMultipart: true}, true)
	if err != nil {
		t.Fatalf("Upload returned error: %v", err)
	}
	if backend.multipartInitCalls != 1 {
		t.Fatalf("expected one multipart init call, got %d", backend.multipartInitCalls)
	}
	if backend.multipartInitKey != "object-large" {
		t.Fatalf("unexpected multipart init key %q", backend.multipartInitKey)
	}
	if backend.completedKey != "object-large" || backend.completedUploadID != "upload-123" {
		t.Fatalf("unexpected completion target key=%q uploadID=%q", backend.completedKey, backend.completedUploadID)
	}
	if len(backend.completedParts) < 2 {
		t.Fatalf("expected multiple completed parts, got %+v", backend.completedParts)
	}
	if !sort.SliceIsSorted(backend.completedParts, func(i, j int) bool {
		return backend.completedParts[i].PartNumber < backend.completedParts[j].PartNumber
	}) {
		t.Fatalf("multipart completion parts were not sorted: %+v", backend.completedParts)
	}
	if len(events) == 0 {
		t.Fatal("expected multipart progress events")
	}
	if events[len(events)-1].BytesSoFar != 101*common.MB {
		t.Fatalf("expected final multipart bytesSoFar=%d, got %+v", 101*common.MB, events[len(events)-1])
	}

	checkpointPath, err := CheckpointPath(file, "guid-large")
	if err != nil {
		t.Fatalf("CheckpointPath returned error: %v", err)
	}
	if _, err := os.Stat(checkpointPath); !os.IsNotExist(err) {
		t.Fatalf("expected checkpoint to be removed, stat err=%v", err)
	}

	state := &uploaderResumeState{SourcePath: file, GUID: "guid-large", ObjectKey: "object-large", FileSize: 101 * common.MB, ChunkSize: 10 * common.MB, Completed: map[int]string{1: "etag-1"}}
	uploader.saveState(checkpointPath, state)
	loaded, ok := uploader.loadState(checkpointPath)
	if !ok || !reflect.DeepEqual(loaded, state) {
		t.Fatalf("unexpected loaded state: ok=%v got=%+v want=%+v", ok, loaded, state)
	}
	info, err := os.Stat(file)
	if err != nil {
		t.Fatalf("Stat returned error: %v", err)
	}
	if !uploader.matches(loaded, transfer.TransferRequest{SourcePath: file, GUID: "guid-large", ObjectKey: "object-large"}, info, 10*common.MB) {
		t.Fatal("expected resume state to match request")
	}
	if uploader.matches(nil, transfer.TransferRequest{}, info, 10*common.MB) {
		t.Fatal("nil state should not match")
	}
}

func TestGenericUploaderUploadSingleDelaysTerminalProgressUntilSuccess(t *testing.T) {
	t.Parallel()

	file := filepath.Join(t.TempDir(), "slow-single.bin")
	content := bytes.Repeat([]byte("a"), int(3*common.MB+123))
	if err := os.WriteFile(file, content, 0o644); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}

	var backendReturned atomic.Bool
	backend := &fakeBackend{
		uploadChunkSize:  256 * 1024,
		uploadChunkDelay: 1 * time.Millisecond,
		uploadDone: func() {
			backendReturned.Store(true)
		},
	}
	uploader := &GenericUploader{Backend: backend}

	var events []common.ProgressEvent
	ctx := common.WithOid(context.Background(), "oid-slow-single")
	ctx = common.WithProgress(ctx, func(ev common.ProgressEvent) error {
		events = append(events, ev)
		if ev.BytesSoFar == int64(len(content)) && !backendReturned.Load() {
			t.Fatalf("terminal progress emitted before upload success: %+v", ev)
		}
		return nil
	})

	if err := uploader.Upload(ctx, transfer.TransferRequest{SourcePath: file, GUID: "guid-slow-single", ObjectKey: "object-slow-single"}, true); err != nil {
		t.Fatalf("Upload returned error: %v", err)
	}

	if len(events) < 3 {
		t.Fatalf("expected multiple progress events, got %+v", events)
	}
	last := events[len(events)-1]
	if last.BytesSoFar != int64(len(content)) {
		t.Fatalf("expected final BytesSoFar=%d, got %+v", len(content), last)
	}
}

func TestGenericUploaderMultipartStreamsProgressAndCompletesAfterFinalize(t *testing.T) {
	t.Parallel()

	file := filepath.Join(t.TempDir(), "slow-multipart.bin")
	f, err := os.Create(file)
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}
	if err := f.Truncate(21 * common.MB); err != nil {
		_ = f.Close()
		t.Fatalf("Truncate returned error: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}

	completeGate := make(chan struct{})
	backend := &fakeBackend{
		multipartInitID: "upload-slow",
		partChunkSize:   256 * 1024,
		partChunkDelay:  1 * time.Millisecond,
		completeGate:    completeGate,
	}
	uploader := &GenericUploader{Backend: backend}

	var finalized atomic.Bool
	var closeGate sync.Once
	releaseComplete := func() {
		closeGate.Do(func() {
			close(completeGate)
		})
	}

	var eventsMu sync.Mutex
	var events []common.ProgressEvent
	progressReady := make(chan struct{}, 1)
	progressErr := make(chan error, 1)
	ctx := common.WithOid(context.Background(), "oid-slow-multipart")
	ctx = common.WithProgress(ctx, func(ev common.ProgressEvent) error {
		eventsMu.Lock()
		events = append(events, ev)
		eventCount := len(events)
		eventsMu.Unlock()
		if ev.BytesSoFar == 21*common.MB && !finalized.Load() {
			err := fmt.Errorf("terminal multipart progress emitted before completion: %+v", ev)
			select {
			case progressErr <- err:
			default:
			}
			return err
		}
		if eventCount >= 2 {
			select {
			case progressReady <- struct{}{}:
			default:
			}
		}
		return nil
	})

	done := make(chan error, 1)
	go func() {
		done <- uploader.Upload(ctx, transfer.TransferRequest{
			SourcePath:     file,
			GUID:           "guid-slow-multipart",
			ObjectKey:      "object-slow-multipart",
			ForceMultipart: true,
		}, true)
	}()

	select {
	case <-progressReady:
	case err := <-progressErr:
		releaseComplete()
		if uploadErr := <-done; uploadErr != nil && uploadErr.Error() != err.Error() {
			t.Fatalf("Upload returned error %v after progress error %v", uploadErr, err)
		}
		t.Fatal(err)
	case <-time.After(2 * time.Second):
		releaseComplete()
		select {
		case err := <-done:
			if err != nil {
				t.Fatalf("Upload returned error: %v", err)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("multipart upload did not emit streaming progress before completion gate release")
		}
		eventsMu.Lock()
		gotEvents := append([]common.ProgressEvent(nil), events...)
		eventsMu.Unlock()
		t.Fatalf("expected streaming multipart progress before completion, got %+v", gotEvents)
	}

	finalized.Store(true)
	releaseComplete()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Upload returned error: %v", err)
		}
	case err := <-progressErr:
		t.Fatal(err)
	case <-time.After(2 * time.Second):
		t.Fatal("multipart upload did not complete after completion gate release")
	}

	eventsMu.Lock()
	last := events[len(events)-1]
	eventsMu.Unlock()
	if last.BytesSoFar != 21*common.MB {
		t.Fatalf("expected final multipart BytesSoFar=%d, got %+v", 21*common.MB, last)
	}
}

func TestGenericUploaderMultipartRetryDoesNotOvercountProgress(t *testing.T) {
	t.Parallel()

	file := filepath.Join(t.TempDir(), "retry-multipart.bin")
	f, err := os.Create(file)
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}
	if err := f.Truncate(21 * common.MB); err != nil {
		_ = f.Close()
		t.Fatalf("Truncate returned error: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}

	backend := &fakeBackend{
		multipartInitID: "upload-retry",
		partChunkSize:   256 * 1024,
		partFailures:    map[int]int{2: 1},
	}
	uploader := &GenericUploader{Backend: backend}

	var events []common.ProgressEvent
	ctx := common.WithOid(context.Background(), "oid-retry-multipart")
	ctx = common.WithProgress(ctx, func(ev common.ProgressEvent) error {
		events = append(events, ev)
		if ev.BytesSoFar > 21*common.MB {
			t.Fatalf("progress overcounted beyond total: %+v", ev)
		}
		return nil
	})

	if err := uploader.Upload(ctx, transfer.TransferRequest{
		SourcePath:     file,
		GUID:           "guid-retry-multipart",
		ObjectKey:      "object-retry-multipart",
		ForceMultipart: true,
	}, true); err != nil {
		t.Fatalf("Upload returned error: %v", err)
	}

	if len(events) == 0 {
		t.Fatal("expected multipart progress events")
	}
	last := events[len(events)-1]
	if last.BytesSoFar != 21*common.MB {
		t.Fatalf("expected final multipart BytesSoFar=%d, got %+v", 21*common.MB, last)
	}
}

func TestGenericDownloaderDownloadSingleVariants(t *testing.T) {
	t.Parallel()

	t.Run("resume range download", func(t *testing.T) {
		backend := &fakeBackend{data: []byte("hello world"), meta: &transfer.ObjectMetadata{Size: 11}}
		d := &GenericDownloader{Source: backend, RetryStrategy: zeroRetryStrategy{}}
		dst := filepath.Join(t.TempDir(), "resume.bin")
		if err := os.WriteFile(dst, []byte("hello "), 0o644); err != nil {
			t.Fatalf("WriteFile returned error: %v", err)
		}

		var events []common.ProgressEvent
		ctx := common.WithOid(context.Background(), "oid-download")
		ctx = common.WithProgress(ctx, func(ev common.ProgressEvent) error {
			events = append(events, ev)
			return nil
		})

		if err := d.downloadSingle(ctx, "guid-1", dst, 11); err != nil {
			t.Fatalf("downloadSingle returned error: %v", err)
		}
		got, err := os.ReadFile(dst)
		if err != nil {
			t.Fatalf("ReadFile returned error: %v", err)
		}
		if string(got) != "hello world" {
			t.Fatalf("unexpected resumed content %q", got)
		}
		if len(backend.rangeCalls) != 1 || backend.rangeCalls[0] != [2]int64{6, 5} {
			t.Fatalf("unexpected range calls: %+v", backend.rangeCalls)
		}
		if len(events) != 1 || events[0].BytesSinceLast != 5 || events[0].BytesSoFar != 11 {
			t.Fatalf("unexpected progress events: %+v", events)
		}

	})

	t.Run("range ignored restarts from zero", func(t *testing.T) {
		backend := &fakeBackend{data: []byte("abcdef"), rangeIgnoredOnce: true}
		d := &GenericDownloader{Source: backend}
		dst := filepath.Join(t.TempDir(), "ignored.bin")
		if err := os.WriteFile(dst, []byte("abc"), 0o644); err != nil {
			t.Fatalf("WriteFile returned error: %v", err)
		}

		if err := d.downloadSingle(context.Background(), "guid-2", dst, 6); err != nil {
			t.Fatalf("downloadSingle returned error: %v", err)
		}
		got, err := os.ReadFile(dst)
		if err != nil {
			t.Fatalf("ReadFile returned error: %v", err)
		}
		if string(got) != "abcdef" {
			t.Fatalf("unexpected restarted content %q", got)
		}
	})

	t.Run("already complete returns early", func(t *testing.T) {
		backend := &fakeBackend{data: []byte("abc")}
		d := &GenericDownloader{Source: backend}
		dst := filepath.Join(t.TempDir(), "done.bin")
		if err := os.WriteFile(dst, []byte("abc"), 0o644); err != nil {
			t.Fatalf("WriteFile returned error: %v", err)
		}

		if err := d.downloadSingle(context.Background(), "guid-3", dst, 3); err != nil {
			t.Fatalf("downloadSingle returned error: %v", err)
		}
		if len(backend.rangeCalls) != 0 {
			t.Fatalf("expected no range calls, got %+v", backend.rangeCalls)
		}
	})

	t.Run("short download returns error", func(t *testing.T) {
		backend := &fakeBackend{data: []byte("abc")}
		d := &GenericDownloader{Source: backend}
		err := d.downloadSingle(context.Background(), "guid-4", filepath.Join(t.TempDir(), "short.bin"), 5)
		if err == nil || !strings.Contains(err.Error(), "short download") {
			t.Fatalf("expected short download error, got %v", err)
		}
	})
}

func TestGenericDownloaderDownloadAndParallel(t *testing.T) {
	t.Parallel()

	content := bytes.Repeat([]byte("abcdef0123456789"), int((2*common.MB+123)/16)+1)
	content = content[:2*common.MB+123]
	backend := &fakeBackend{
		data: content,
		meta: &transfer.ObjectMetadata{Size: int64(len(content)), AcceptRanges: true, Provider: "http"},
	}
	d := &GenericDownloader{Source: backend}
	dst := filepath.Join(t.TempDir(), "parallel.bin")

	var eventsMu sync.Mutex
	var events []common.ProgressEvent
	ctx := common.WithOid(context.Background(), "parallel-oid")
	ctx = common.WithProgress(ctx, func(ev common.ProgressEvent) error {
		eventsMu.Lock()
		events = append(events, ev)
		eventsMu.Unlock()
		return nil
	})

	if err := d.Download(ctx, "guid-p", dst, 3, 1*common.MB, 1*common.MB); err != nil {
		t.Fatalf("Download returned error: %v", err)
	}
	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatalf("ReadFile returned error: %v", err)
	}
	if !bytes.Equal(got, content) {
		t.Fatal("parallel download content mismatch")
	}
	if len(backend.rangeCalls) != 3 {
		t.Fatalf("expected 3 range calls, got %+v", backend.rangeCalls)
	}
	if len(events) < 3 {
		t.Fatalf("expected incremental progress events, got %+v", events)
	}

	lastEvent := events[len(events)-1]
	if lastEvent.BytesSoFar != int64(len(content)) {
		t.Fatalf("expected final progress to reach %d, got %+v", len(content), lastEvent)
	}


	backend2 := &fakeBackend{data: []byte("single"), meta: &transfer.ObjectMetadata{Size: 6, AcceptRanges: true}}
	d2 := &GenericDownloader{Source: backend2}
	dst2 := filepath.Join(t.TempDir(), "single.bin")
	if err := d2.Download(context.Background(), "guid-s", dst2, 4, 1*common.MB, 1*common.MB); err != nil {
		t.Fatalf("Download returned error: %v", err)
	}
	if len(backend2.rangeCalls) != 0 {
		t.Fatalf("expected single-stream path with no range calls, got %+v", backend2.rangeCalls)
	}

	backend3 := &fakeBackend{data: []byte("norange"), meta: &transfer.ObjectMetadata{Size: 7, AcceptRanges: false}}
	d3 := &GenericDownloader{Source: backend3}
	if err := d3.Download(context.Background(), "guid-nr", filepath.Join(t.TempDir(), "norange.bin"), 2, 1*common.MB, 0); err != nil {
		t.Fatalf("Download returned error: %v", err)
	}
	if len(backend3.rangeCalls) != 0 {
		t.Fatalf("expected single-stream fallback when ranges unsupported, got %+v", backend3.rangeCalls)
	}

	t.Run("parallel failure removes preallocated destination", func(t *testing.T) {
		backend := &fakeBackend{
			data:     content,
			rangeErr: fmt.Errorf("boom"),
			meta:     &transfer.ObjectMetadata{Size: int64(len(content)), AcceptRanges: true, Provider: "http"},
		}
		d := &GenericDownloader{Source: backend}
		dst := filepath.Join(t.TempDir(), "failed-parallel.bin")
		ctx := context.Background()

		err := d.Download(ctx, "guid-fail", dst, 2, 1*common.MB, 1*common.MB)
		if err == nil || !strings.Contains(err.Error(), "range download") {
			t.Fatalf("expected range download error, got %v", err)
		}

		if _, statErr := os.Stat(dst); !os.IsNotExist(statErr) {
			t.Fatalf("expected failed parallel download to remove %q, stat err=%v", dst, statErr)
		}
	})
}

func TestGenericDownloaderSingleDownloadStreamsProgress(t *testing.T) {
	t.Parallel()

	content := bytes.Repeat([]byte("abcdef0123456789"), int((2*common.MB+123)/16)+1)
	content = content[:2*common.MB+123]
	backend := &fakeBackend{
		data: content,
		meta: &transfer.ObjectMetadata{Size: int64(len(content)), AcceptRanges: false, Provider: "http"},
	}
	d := &GenericDownloader{Source: backend}
	dst := filepath.Join(t.TempDir(), "single-progress.bin")

	var events []common.ProgressEvent
	ctx := common.WithOid(context.Background(), "single-download-oid")
	ctx = common.WithProgress(ctx, func(ev common.ProgressEvent) error {
		events = append(events, ev)
		return nil
	})

	if err := d.Download(ctx, "guid-single", dst, 2, 1*common.MB, 5*common.GB); err != nil {
		t.Fatalf("Download returned error: %v", err)
	}
	if len(events) < 2 {
		t.Fatalf("expected incremental progress events, got %+v", events)
	}
	last := events[len(events)-1]
	if last.BytesSoFar != int64(len(content)) || last.Oid != "single-download-oid" {
		t.Fatalf("unexpected final progress event: %+v", last)
	}
}
