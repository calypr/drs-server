package transfers

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/calypr/syfon/apigen/errorapi"
	"github.com/calypr/syfon/internal/storage"
)

func TestMultipartLifecycleSessionsAreInstanceLocal(t *testing.T) {
	service := NewService(Dependencies{Multipart: &multipartFake{beginID: "opaque-upload"}})
	assertMultipartLifecycleIsolated(t, NewMultipartLifecycle(service), NewMultipartLifecycle(service))
}

func TestMultipartLifecycleSessionsAreIsolatedAcrossServices(t *testing.T) {
	firstService := NewService(Dependencies{Multipart: &multipartFake{beginID: "opaque-upload"}})
	secondService := NewService(Dependencies{Multipart: &multipartFake{beginID: "opaque-upload"}})
	assertMultipartLifecycleIsolated(t, NewMultipartLifecycle(firstService), NewMultipartLifecycle(secondService))
}

func assertMultipartLifecycleIsolated(t *testing.T, first, second *MultipartLifecycle) {
	t.Helper()
	uploadID, err := first.Begin(context.Background(), "bucket", "key")
	if err != nil {
		t.Fatalf("Begin() error = %v", err)
	}
	if _, err := second.SignPart(context.Background(), uploadID, 1); !errors.Is(err, errorapi.ErrMultipartUploadNotFound) {
		t.Fatalf("SignPart() error = %v, want ErrMultipartUploadNotFound", err)
	}
}

func TestMultipartLifecycleBeginDoesNotStoreFailedProviderUpload(t *testing.T) {
	providerErr := errors.New("provider begin failed")
	lifecycle := NewMultipartLifecycle(NewService(Dependencies{Multipart: &multipartFake{
		beginID:  "opaque-upload",
		beginErr: providerErr,
	}}))

	if _, err := lifecycle.Begin(context.Background(), "bucket", "key"); !errors.Is(err, providerErr) {
		t.Fatalf("Begin() error = %v, want provider error", err)
	}
	if _, err := lifecycle.SignPart(context.Background(), "opaque-upload", 1); !errors.Is(err, errorapi.ErrMultipartUploadNotFound) {
		t.Fatalf("SignPart() error = %v, want ErrMultipartUploadNotFound", err)
	}
}

func TestMultipartLifecycleCompleteRetainsSessionAfterProviderFailure(t *testing.T) {
	providerErr := errors.New("provider completion failed")
	provider := &lifecycleMultipartFake{beginIDs: []storage.UploadID{"opaque-upload"}, completeErrs: []error{providerErr, nil}}
	lifecycle := NewMultipartLifecycle(NewService(Dependencies{Multipart: provider}))
	uploadID, err := lifecycle.Begin(context.Background(), "bucket", "key")
	if err != nil {
		t.Fatalf("Begin() error = %v", err)
	}

	if err := lifecycle.Complete(context.Background(), uploadID, nil); !errors.Is(err, providerErr) {
		t.Fatalf("Complete() error = %v, want provider error", err)
	}
	if err := lifecycle.Complete(context.Background(), uploadID, nil); err != nil {
		t.Fatalf("retry Complete() error = %v, want nil", err)
	}
	if err := lifecycle.Complete(context.Background(), uploadID, nil); !errors.Is(err, errorapi.ErrMultipartUploadNotFound) {
		t.Fatalf("post-success Complete() error = %v, want ErrMultipartUploadNotFound", err)
	}
}

type lifecycleMultipartFake struct {
	mu            sync.Mutex
	beginIDs      []storage.UploadID
	completeErrs  []error
	completeCalls []storage.CompleteMultipartRequest
	active        int
	maxActive     int
	started       chan<- struct{}
	release       <-chan struct{}
}

func (f *lifecycleMultipartFake) BeginMultipart(context.Context, storage.Target) (storage.UploadID, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.beginIDs) == 0 {
		return "", fmt.Errorf("no upload ID configured")
	}
	id := f.beginIDs[0]
	f.beginIDs = f.beginIDs[1:]
	return id, nil
}

func (f *lifecycleMultipartFake) SignMultipartPart(context.Context, storage.MultipartPartRequest) (storage.SignedAccess, error) {
	return storage.SignedAccess{}, nil
}

func (f *lifecycleMultipartFake) CompleteMultipart(ctx context.Context, request storage.CompleteMultipartRequest) error {
	f.mu.Lock()
	call := len(f.completeCalls)
	f.completeCalls = append(f.completeCalls, request)
	f.active++
	if f.active > f.maxActive {
		f.maxActive = f.active
	}
	var providerErr error
	if call < len(f.completeErrs) {
		providerErr = f.completeErrs[call]
	}
	f.mu.Unlock()

	if f.started != nil {
		f.started <- struct{}{}
	}
	if f.release != nil {
		select {
		case <-f.release:
		case <-ctx.Done():
			f.mu.Lock()
			f.active--
			f.mu.Unlock()
			return ctx.Err()
		}
	}
	f.mu.Lock()
	f.active--
	f.mu.Unlock()
	return providerErr
}

func (f *lifecycleMultipartFake) completionStats() (calls, maxActive int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.completeCalls), f.maxActive
}

func waitForCompletions(t *testing.T, started <-chan struct{}, count int) {
	t.Helper()
	for range count {
		select {
		case <-started:
		case <-time.After(2 * time.Second):
			t.Fatalf("provider completion %d did not start", count)
		}
	}
}

func TestMultipartLifecycleCompleteSerializesSameUpload(t *testing.T) {
	providerErr := errors.New("provider completion failed")
	started := make(chan struct{}, 2)
	release := make(chan struct{})
	provider := &lifecycleMultipartFake{
		beginIDs:     []storage.UploadID{"opaque-upload"},
		completeErrs: []error{providerErr, nil},
		started:      started,
		release:      release,
	}
	lifecycle := NewMultipartLifecycle(NewService(Dependencies{Multipart: provider}))
	uploadID, err := lifecycle.Begin(context.Background(), "bucket", "key")
	if err != nil {
		t.Fatalf("Begin() error = %v", err)
	}

	firstDone := make(chan error, 1)
	go func() { firstDone <- lifecycle.Complete(context.Background(), uploadID, nil) }()
	waitForCompletions(t, started, 1)
	secondDone := make(chan error, 1)
	go func() { secondDone <- lifecycle.Complete(context.Background(), uploadID, nil) }()
	select {
	case <-started:
		t.Fatal("same-upload completion reached provider concurrently")
	case <-time.After(50 * time.Millisecond):
	}
	close(release)
	if err := <-firstDone; !errors.Is(err, providerErr) {
		t.Fatalf("first Complete() error = %v, want provider error", err)
	}
	if err := <-secondDone; err != nil {
		t.Fatalf("second Complete() error = %v, want nil", err)
	}
	calls, maxActive := provider.completionStats()
	if calls != 2 || maxActive != 1 {
		t.Fatalf("provider completion stats = calls %d, max active %d, want 2, 1", calls, maxActive)
	}
}

func TestMultipartLifecycleCompleteAllowsDistinctUploadsConcurrently(t *testing.T) {
	started := make(chan struct{}, 2)
	release := make(chan struct{})
	provider := &lifecycleMultipartFake{
		beginIDs: []storage.UploadID{"opaque-upload-1", "opaque-upload-2"},
		started:  started,
		release:  release,
	}
	lifecycle := NewMultipartLifecycle(NewService(Dependencies{Multipart: provider}))
	firstID, err := lifecycle.Begin(context.Background(), "bucket", "key-1")
	if err != nil {
		t.Fatalf("first Begin() error = %v", err)
	}
	secondID, err := lifecycle.Begin(context.Background(), "bucket", "key-2")
	if err != nil {
		t.Fatalf("second Begin() error = %v", err)
	}

	results := make(chan error, 2)
	go func() { results <- lifecycle.Complete(context.Background(), firstID, nil) }()
	go func() { results <- lifecycle.Complete(context.Background(), secondID, nil) }()
	waitForCompletions(t, started, 2)
	close(release)
	for range 2 {
		if err := <-results; err != nil {
			t.Fatalf("Complete() error = %v", err)
		}
	}
	calls, maxActive := provider.completionStats()
	if calls != 2 || maxActive != 2 {
		t.Fatalf("provider completion stats = calls %d, max active %d, want 2, 2", calls, maxActive)
	}
}

func TestMultipartLifecycleCompleteWaitsPerUploadAndAllowsCancellation(t *testing.T) {
	started := make(chan struct{}, 2)
	release := make(chan struct{})
	provider := &lifecycleMultipartFake{beginIDs: []storage.UploadID{"opaque-upload"}, started: started, release: release}
	lifecycle := NewMultipartLifecycle(NewService(Dependencies{Multipart: provider}))
	uploadID, err := lifecycle.Begin(context.Background(), "bucket", "key")
	if err != nil {
		t.Fatalf("Begin() error = %v", err)
	}

	firstDone := make(chan error, 1)
	go func() { firstDone <- lifecycle.Complete(context.Background(), uploadID, nil) }()
	waitForCompletions(t, started, 1)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := lifecycle.Complete(ctx, uploadID, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("waiting Complete() error = %v, want context.Canceled", err)
	}
	close(release)
	if err := <-firstDone; err != nil {
		t.Fatalf("first Complete() error = %v", err)
	}
	calls, maxActive := provider.completionStats()
	if calls != 1 || maxActive != 1 {
		t.Fatalf("provider completion stats = calls %d, max active %d, want 1, 1", calls, maxActive)
	}
}
