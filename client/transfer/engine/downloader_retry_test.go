package engine

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/calypr/syfon/client/common"
	"github.com/calypr/syfon/client/transfer"
)

type instantDownloadRetry struct{}

func (instantDownloadRetry) WaitTime(int) time.Duration { return 0 }

type partialDownloadBody struct {
	reader    *bytes.Reader
	failAfter int64
	read      int64
	failErr   error
	onFailure func()
}

func (r *partialDownloadBody) Read(p []byte) (int, error) {
	if r.failAfter >= 0 && r.read >= r.failAfter {
		if r.onFailure != nil {
			r.onFailure()
			r.onFailure = nil
		}
		return 0, r.failErr
	}
	if r.failAfter >= 0 && int64(len(p)) > r.failAfter-r.read {
		p = p[:r.failAfter-r.read]
	}
	n, err := r.reader.Read(p)
	r.read += int64(n)
	return n, err
}

func (r *partialDownloadBody) Close() error { return nil }

type retryDownloadBackend struct {
	data []byte

	mu             sync.Mutex
	fullCalls      int
	rangeCalls     [][2]int64
	failFullOnce   bool
	failRangeOnce  bool
	failRangeStart int64
	onFailure      func()
}

func (b *retryDownloadBackend) Logger() transfer.TransferLogger { return transfer.NoOpLogger{} }

func (b *retryDownloadBackend) Stat(context.Context, string) (*transfer.ObjectMetadata, error) {
	return &transfer.ObjectMetadata{Size: int64(len(b.data)), AcceptRanges: true}, nil
}

func (b *retryDownloadBackend) GetReader(context.Context, string) (io.ReadCloser, error) {
	b.mu.Lock()
	b.fullCalls++
	shouldFail := b.failFullOnce && b.fullCalls == 1
	onFailure := b.onFailure
	b.mu.Unlock()
	if shouldFail {
		return &partialDownloadBody{reader: bytes.NewReader(b.data), failAfter: 3, failErr: errors.New("transient full read"), onFailure: onFailure}, nil
	}
	return io.NopCloser(bytes.NewReader(b.data)), nil
}

func (b *retryDownloadBackend) GetRangeReader(_ context.Context, _ string, offset, length int64) (io.ReadCloser, error) {
	b.mu.Lock()
	b.rangeCalls = append(b.rangeCalls, [2]int64{offset, length})
	shouldFail := b.failRangeOnce && offset == b.failRangeStart
	if shouldFail {
		b.failRangeOnce = false
	}
	onFailure := b.onFailure
	b.mu.Unlock()
	if offset < 0 || length < 0 || offset+length > int64(len(b.data)) {
		return nil, fmt.Errorf("invalid range %d:%d", offset, length)
	}
	if shouldFail {
		return &partialDownloadBody{reader: bytes.NewReader(b.data[offset : offset+length]), failAfter: 1, failErr: errors.New("transient range read"), onFailure: onFailure}, nil
	}
	return io.NopCloser(bytes.NewReader(b.data[offset : offset+length])), nil
}

func (b *retryDownloadBackend) GetWriter(context.Context, string) (io.WriteCloser, error) {
	return nil, errors.New("unused")
}

func progressEventsForDownload(t *testing.T, ctx context.Context) (context.Context, *[]common.ProgressEvent) {
	t.Helper()
	var mu sync.Mutex
	var events []common.ProgressEvent
	return common.WithProgress(ctx, func(event common.ProgressEvent) error {
		mu.Lock()
		events = append(events, event)
		mu.Unlock()
		return nil
	}), &events
}

func assertDownloadProgress(t *testing.T, events []common.ProgressEvent, total, downloaded int64) {
	t.Helper()
	if len(events) == 0 {
		t.Fatal("expected progress events")
	}
	var deltaTotal int64
	for _, event := range events {
		if event.BytesSoFar < 0 || event.BytesSoFar > total {
			t.Fatalf("progress exceeded logical total: %+v", event)
		}
		if event.BytesSinceLast < 0 {
			t.Fatalf("progress delta rolled back: %+v", event)
		}
		deltaTotal += event.BytesSinceLast
	}
	if deltaTotal != downloaded {
		t.Fatalf("progress delta total = %d, want %d; events=%+v", deltaTotal, downloaded, events)
	}
	if got := events[len(events)-1].BytesSoFar; got != total {
		t.Fatalf("final progress = %d, want %d; events=%+v", got, total, events)
	}
}

func TestDownloaderRetriesPartialFullReadWithoutDuplicatingProgress(t *testing.T) {
	data := []byte("download")
	backend := &retryDownloadBackend{data: data, failFullOnce: true}
	destination := filepath.Join(t.TempDir(), "download.bin")
	ctx, events := progressEventsForDownload(t, context.Background())

	if err := (&downloader{Source: backend, RetryStrategy: instantDownloadRetry{}}).downloadSingle(ctx, "object", destination, int64(len(data))); err != nil {
		t.Fatalf("downloadSingle returned error: %v", err)
	}
	got, err := os.ReadFile(destination)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, data) {
		t.Fatalf("downloaded bytes = %q, want %q", got, data)
	}
	if backend.fullCalls != 2 {
		t.Fatalf("full reader calls = %d, want 2", backend.fullCalls)
	}
	assertDownloadProgress(t, *events, int64(len(data)), int64(len(data)))
}

func TestDownloaderRetriesPartialRangeFromCurrentDestinationOffset(t *testing.T) {
	data := []byte("download")
	backend := &retryDownloadBackend{data: data, failRangeOnce: true, failRangeStart: 3}
	destination := filepath.Join(t.TempDir(), "download.bin")
	if err := os.WriteFile(destination, data[:3], 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, events := progressEventsForDownload(t, context.Background())

	if err := (&downloader{Source: backend, RetryStrategy: instantDownloadRetry{}}).downloadSingle(ctx, "object", destination, int64(len(data))); err != nil {
		t.Fatalf("downloadSingle returned error: %v", err)
	}
	got, err := os.ReadFile(destination)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, data) {
		t.Fatalf("downloaded bytes = %q, want %q", got, data)
	}
	backend.mu.Lock()
	calls := append([][2]int64(nil), backend.rangeCalls...)
	backend.mu.Unlock()
	if len(calls) != 2 || calls[0] != [2]int64{3, 5} || calls[1] != [2]int64{4, 4} {
		t.Fatalf("range calls = %+v, want [{3 5} {4 4}]", calls)
	}
	assertDownloadProgress(t, *events, int64(len(data)), int64(len(data)-3))
}

func TestDownloaderParallelRetryPublishesOnlyCommittedBytes(t *testing.T) {
	data := bytes.Repeat([]byte("x"), int(common.MB))
	backend := &retryDownloadBackend{data: data, failRangeOnce: true, failRangeStart: 0}
	destination := filepath.Join(t.TempDir(), "download.bin")
	ctx, events := progressEventsForDownload(t, context.Background())

	if err := (&downloader{Source: backend, RetryStrategy: instantDownloadRetry{}}).downloadParallel(ctx, "object", destination, int64(len(data)), 1, int64(len(data))); err != nil {
		t.Fatalf("downloadParallel returned error: %v", err)
	}
	got, err := os.ReadFile(destination)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, data) {
		t.Fatal("parallel retry changed downloaded bytes")
	}
	assertDownloadProgress(t, *events, int64(len(data)), int64(len(data)))
	backend.mu.Lock()
	callCount := len(backend.rangeCalls)
	backend.mu.Unlock()
	if callCount != 2 {
		t.Fatalf("range reader calls = %d, want 2", callCount)
	}
}

func TestDownloaderParallelRetryKeepsOtherPartProgressMonotonic(t *testing.T) {
	data := bytes.Repeat([]byte("x"), int(2*common.MB))
	backend := &retryDownloadBackend{data: data, failRangeOnce: true, failRangeStart: 0}
	destination := filepath.Join(t.TempDir(), "download.bin")
	ctx, events := progressEventsForDownload(t, context.Background())

	if err := (&downloader{Source: backend, RetryStrategy: instantDownloadRetry{}}).downloadParallel(ctx, "object", destination, int64(len(data)), 2, common.MB); err != nil {
		t.Fatalf("downloadParallel returned error: %v", err)
	}
	got, err := os.ReadFile(destination)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, data) {
		t.Fatal("parallel retry changed downloaded bytes")
	}
	assertDownloadProgress(t, *events, int64(len(data)), int64(len(data)))
}

func TestDownloaderCancellationStopsBeforeAnotherFullAttempt(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	backend := &retryDownloadBackend{data: []byte("download"), failFullOnce: true}
	backend.onFailure = cancel
	destination := filepath.Join(t.TempDir(), "download.bin")
	err := (&downloader{Source: backend, RetryStrategy: instantDownloadRetry{}}).downloadSingle(ctx, "object", destination, int64(len(backend.data)))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("downloadSingle error = %v, want context cancellation", err)
	}
	if backend.fullCalls != 1 {
		t.Fatalf("full reader calls = %d, want one before cancellation", backend.fullCalls)
	}
}

func TestDownloaderProgressCallbackErrorDoesNotRetry(t *testing.T) {
	backend := &retryDownloadBackend{data: []byte("download")}
	wantErr := errors.New("progress sink failed")
	ctx := common.WithProgress(context.Background(), func(common.ProgressEvent) error { return wantErr })
	destination := filepath.Join(t.TempDir(), "download.bin")
	err := (&downloader{Source: backend, RetryStrategy: instantDownloadRetry{}}).downloadSingle(ctx, "object", destination, int64(len(backend.data)))
	if !errors.Is(err, wantErr) {
		t.Fatalf("downloadSingle error = %v, want %v", err, wantErr)
	}
	if backend.fullCalls != 1 {
		t.Fatalf("full reader calls = %d, want one", backend.fullCalls)
	}
}
