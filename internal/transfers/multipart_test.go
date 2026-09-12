package transfers

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/calypr/syfon/apigen/errorapi"
	"github.com/calypr/syfon/internal/storage"
)

type multipartTerminalStorage struct {
	mu           sync.Mutex
	beginIDs     []storage.UploadID
	beginCalls   int
	completeErrs []error
	completeFn   func(int, storage.CompleteMultipartRequest) error
	complete     []storage.CompleteMultipartRequest
	parts        []storage.MultipartPartRequest
	started      chan storage.CompleteMultipartRequest
}

func (s *multipartTerminalStorage) Sign(context.Context, storage.SignRequest) (storage.SignedAccess, error) {
	return storage.SignedAccess{}, nil
}

func (s *multipartTerminalStorage) BeginMultipart(_ context.Context, _ storage.Target) (storage.UploadID, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	index := s.beginCalls
	s.beginCalls++
	if index >= len(s.beginIDs) {
		return "", fmt.Errorf("unexpected multipart begin call %d", index+1)
	}
	return s.beginIDs[index], nil
}

func (s *multipartTerminalStorage) SignMultipartPart(_ context.Context, request storage.MultipartPartRequest) (storage.SignedAccess, error) {
	s.mu.Lock()
	s.parts = append(s.parts, request)
	s.mu.Unlock()
	return storage.SignedAccess{}, nil
}

func (s *multipartTerminalStorage) CompleteMultipart(_ context.Context, request storage.CompleteMultipartRequest) error {
	s.mu.Lock()
	s.complete = append(s.complete, request)
	call := len(s.complete)
	err := error(nil)
	if call <= len(s.completeErrs) {
		err = s.completeErrs[call-1]
	}
	s.mu.Unlock()
	if s.started != nil {
		s.started <- request
	}
	if s.completeFn != nil {
		return s.completeFn(call, request)
	}
	return err
}

func (s *multipartTerminalStorage) completeCalls() []storage.CompleteMultipartRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]storage.CompleteMultipartRequest(nil), s.complete...)
}

type multipartObservedContext struct {
	context.Context
	entered chan<- struct{}
	seen    atomic.Bool
}

func (c *multipartObservedContext) Err() error {
	if c.seen.CompareAndSwap(false, true) {
		c.entered <- struct{}{}
	}
	return c.Context.Err()
}

func newMultipartTerminalService(storagePort StoragePort, uploadID string) (*Service, *multipartSession) {
	service := NewService(Dependencies{Storage: storagePort})
	session := newMultipartSession(storage.Target{PhysicalBucket: "bucket", LookupKey: "bucket", Key: "key"})
	service.multipartSessions = map[string]*multipartSession{uploadID: session}
	return service, session
}

var oneCompletedPart = []CompletedPart{{PartNumber: 1, ETag: "etag"}}

func TestCompleteMultipartQueuedSuccessInvokesProviderOnce(t *testing.T) {
	const uploadID = "queued-upload"
	provider := &multipartTerminalStorage{}
	service, session := newMultipartTerminalService(provider, uploadID)
	if err := session.acquire(context.Background()); err != nil {
		t.Fatalf("reserve multipart session: %v", err)
	}

	entered := make(chan struct{}, 2)
	results := make(chan error, 2)
	for range 2 {
		go func() {
			results <- service.CompleteMultipart(&multipartObservedContext{Context: context.Background(), entered: entered}, uploadID, oneCompletedPart)
		}()
	}
	for range 2 {
		<-entered
	}
	session.release()

	var notFound error
	for range 2 {
		if err := <-results; err != nil {
			if notFound != nil {
				t.Fatalf("both queued completions failed: %v and %v", notFound, err)
			}
			notFound = err
		}
	}
	if got := len(provider.completeCalls()); got != 1 {
		t.Fatalf("provider completion calls = %d, want 1", got)
	}
	want := fmt.Errorf("%w: %s", errorapi.ErrMultipartUploadNotFound, uploadID)
	if notFound == nil || !errors.Is(notFound, errorapi.ErrMultipartUploadNotFound) || notFound.Error() != want.Error() {
		t.Fatalf("queued completion error = %v, want %v", notFound, want)
	}
	if err := service.CompleteMultipart(context.Background(), uploadID, oneCompletedPart); err == nil || err.Error() != want.Error() {
		t.Fatalf("sequential completion error = %v, want %v", err, want)
	}
}

func TestCompleteMultipartProviderFailureLeavesSessionAvailable(t *testing.T) {
	const uploadID = "retry-upload"
	providerErr := errors.New("provider unavailable")
	provider := &multipartTerminalStorage{completeErrs: []error{providerErr, nil}}
	service, _ := newMultipartTerminalService(provider, uploadID)

	if err := service.CompleteMultipart(context.Background(), uploadID, oneCompletedPart); !errors.Is(err, providerErr) {
		t.Fatalf("first completion error = %v, want %v", err, providerErr)
	}
	if err := service.CompleteMultipart(context.Background(), uploadID, oneCompletedPart); err != nil {
		t.Fatalf("retry completion error = %v", err)
	}
	if got := len(provider.completeCalls()); got != 2 {
		t.Fatalf("provider completion calls = %d, want 2", got)
	}
	if err := service.CompleteMultipart(context.Background(), uploadID, oneCompletedPart); !errors.Is(err, errorapi.ErrMultipartUploadNotFound) {
		t.Fatalf("completion after retry error = %v, want multipart not found", err)
	}
}

func TestCompleteMultipartWaitingCancellationDoesNotInvokeProvider(t *testing.T) {
	const uploadID = "cancel-upload"
	firstStarted := make(chan storage.CompleteMultipartRequest, 1)
	releaseFirst := make(chan struct{})
	provider := &multipartTerminalStorage{
		started: firstStarted,
		completeFn: func(call int, _ storage.CompleteMultipartRequest) error {
			if call == 1 {
				<-releaseFirst
			}
			return nil
		},
	}
	service, session := newMultipartTerminalService(provider, uploadID)
	if err := session.acquire(context.Background()); err != nil {
		t.Fatalf("reserve multipart session: %v", err)
	}

	firstResult := make(chan error, 1)
	firstEntered := make(chan struct{}, 1)
	firstContext := &multipartObservedContext{Context: context.Background(), entered: firstEntered}
	go func() { firstResult <- service.CompleteMultipart(firstContext, uploadID, oneCompletedPart) }()
	<-firstEntered
	session.release()
	<-firstStarted

	ctx, cancel := context.WithCancel(context.Background())
	entered := make(chan struct{}, 1)
	secondResult := make(chan error, 1)
	go func() {
		secondResult <- service.CompleteMultipart(&multipartObservedContext{Context: ctx, entered: entered}, uploadID, oneCompletedPart)
	}()
	<-entered
	cancel()
	if err := <-secondResult; !errors.Is(err, context.Canceled) {
		t.Fatalf("waiting completion error = %v, want context canceled", err)
	}
	close(releaseFirst)
	if err := <-firstResult; err != nil {
		t.Fatalf("first completion error = %v", err)
	}
	if got := len(provider.completeCalls()); got != 1 {
		t.Fatalf("provider completion calls = %d, want 1", got)
	}
}

func TestCompleteMultipartDifferentSessionsCanRunConcurrently(t *testing.T) {
	started := make(chan storage.CompleteMultipartRequest, 2)
	release := make(chan struct{})
	provider := &multipartTerminalStorage{
		started: started,
		completeFn: func(int, storage.CompleteMultipartRequest) error {
			<-release
			return nil
		},
	}
	service := NewService(Dependencies{Storage: provider})
	service.multipartSessions = map[string]*multipartSession{
		"upload-one": newMultipartSession(storage.Target{Key: "one"}),
		"upload-two": newMultipartSession(storage.Target{Key: "two"}),
	}
	results := make(chan error, 2)
	go func() { results <- service.CompleteMultipart(context.Background(), "upload-one", oneCompletedPart) }()
	go func() { results <- service.CompleteMultipart(context.Background(), "upload-two", oneCompletedPart) }()
	<-started
	<-started
	if got := len(provider.completeCalls()); got != 2 {
		t.Fatalf("provider calls before release = %d, want 2", got)
	}
	close(release)
	for range 2 {
		if err := <-results; err != nil {
			t.Fatalf("concurrent completion error = %v", err)
		}
	}
}

func TestCompleteMultipartOpaqueIDReusePreservesReplacementSession(t *testing.T) {
	const uploadID = "opaque-upload-id"
	firstStarted := make(chan storage.CompleteMultipartRequest, 1)
	releaseFirst := make(chan struct{})
	provider := &multipartTerminalStorage{
		beginIDs: []storage.UploadID{uploadID, uploadID},
		started:  firstStarted,
		completeFn: func(call int, _ storage.CompleteMultipartRequest) error {
			if call == 1 {
				<-releaseFirst
			}
			return nil
		},
	}
	service := NewService(Dependencies{Objects: downloadObjectFake{object: testRecord()}, Storage: provider})
	oldTarget := storage.Target{PhysicalBucket: "old-bucket", LookupKey: "old-bucket", Key: "old-key"}
	newTarget := storage.Target{PhysicalBucket: "new-bucket", LookupKey: "new-bucket", Key: "new-key"}
	if _, err := service.BeginMultipart(context.Background(), MultipartInitRequest{Target: &oldTarget}); err != nil {
		t.Fatalf("begin old multipart: %v", err)
	}
	oldResult := make(chan error, 1)
	go func() { oldResult <- service.CompleteMultipart(context.Background(), uploadID, oneCompletedPart) }()
	<-firstStarted
	if _, err := service.BeginMultipart(context.Background(), MultipartInitRequest{Target: &newTarget}); err != nil {
		t.Fatalf("begin replacement multipart: %v", err)
	}
	close(releaseFirst)
	if err := <-oldResult; err != nil {
		t.Fatalf("old completion error = %v", err)
	}
	if err := service.CompleteMultipart(context.Background(), uploadID, oneCompletedPart); err != nil {
		t.Fatalf("replacement completion error = %v", err)
	}

	calls := provider.completeCalls()
	if len(calls) != 2 {
		t.Fatalf("provider completion calls = %d, want 2", len(calls))
	}
	if calls[0].Target.Key != oldTarget.Key || calls[1].Target.Key != newTarget.Key {
		t.Fatalf("completion targets = %q, %q; want %q, %q", calls[0].Target.Key, calls[1].Target.Key, oldTarget.Key, newTarget.Key)
	}
}

func TestMultipartRejectsInvalidInputsBeforeProviderDispatch(t *testing.T) {
	provider := &multipartTerminalStorage{}
	service, _ := newMultipartTerminalService(provider, "upload")

	if _, err := service.SignMultipartPart(context.Background(), "upload", 0); !errors.Is(err, errorapi.ErrInvalidInput) {
		t.Fatalf("zero part signing error = %v, want invalid input", err)
	}
	if len(provider.parts) != 0 {
		t.Fatalf("invalid part signing reached provider: %+v", provider.parts)
	}

	tests := []struct {
		name  string
		parts []CompletedPart
	}{
		{name: "empty"},
		{name: "zero", parts: []CompletedPart{{PartNumber: 0}}},
		{name: "negative", parts: []CompletedPart{{PartNumber: -1}}},
		{name: "duplicate", parts: []CompletedPart{{PartNumber: 2}, {PartNumber: 2}}},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			if err := service.CompleteMultipart(context.Background(), "upload", testCase.parts); !errors.Is(err, errorapi.ErrInvalidInput) {
				t.Fatalf("completion error = %v, want invalid input", err)
			}
		})
	}
	if len(provider.completeCalls()) != 0 {
		t.Fatalf("invalid completion reached provider: %+v", provider.completeCalls())
	}
}

func TestCompleteMultipartSortsPartsAndPreservesETags(t *testing.T) {
	provider := &multipartTerminalStorage{}
	service, _ := newMultipartTerminalService(provider, "upload")
	parts := []CompletedPart{{PartNumber: 7, ETag: "seven"}, {PartNumber: 2, ETag: "two"}}
	if err := service.CompleteMultipart(context.Background(), "upload", parts); err != nil {
		t.Fatal(err)
	}
	got := provider.completeCalls()[0].Parts
	if len(got) != 2 || got[0].PartNumber != 2 || got[0].ETag != "two" || got[1].PartNumber != 7 || got[1].ETag != "seven" {
		t.Fatalf("provider parts = %+v", got)
	}
	if parts[0].PartNumber != 7 {
		t.Fatalf("caller parts mutated: %+v", parts)
	}
}

func TestBeginMultipartRejectsEmptyProviderUploadID(t *testing.T) {
	provider := &multipartTerminalStorage{beginIDs: []storage.UploadID{""}}
	service := NewService(Dependencies{Objects: downloadObjectFake{object: testRecord()}, Storage: provider})
	target := storage.Target{PhysicalBucket: "bucket", Key: "key"}
	if _, err := service.BeginMultipart(context.Background(), MultipartInitRequest{Target: &target}); err == nil {
		t.Fatal("BeginMultipart accepted an empty provider upload ID")
	}
	if len(service.multipartSessions) != 0 {
		t.Fatalf("empty upload ID created session: %+v", service.multipartSessions)
	}
}
