package transfers

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/calypr/syfon/apigen/errorapi"
	"github.com/calypr/syfon/internal/access"
	"github.com/calypr/syfon/internal/storage"
)

type multipartTerminalStorage struct {
	mu           sync.Mutex
	beginIDs     []storage.UploadID
	beginCalls   int
	completeErrs []error
	completeFn   func(int, storage.CompleteMultipartRequest) error
	complete     []storage.CompleteMultipartRequest
	abortErrs    []error
	abort        []storage.AbortMultipartRequest
	parts        []storage.MultipartPartRequest
	started      chan storage.CompleteMultipartRequest
}

func (s *multipartTerminalStorage) Sign(context.Context, storage.SignRequest) (storage.SignedAccess, error) {
	return storage.SignedAccess{}, nil
}

func (s *multipartTerminalStorage) BeginMultipart(_ context.Context, _ storage.BeginMultipartRequest) (storage.UploadID, error) {
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

func (s *multipartTerminalStorage) AbortMultipart(_ context.Context, request storage.AbortMultipartRequest) error {
	s.mu.Lock()
	s.abort = append(s.abort, request)
	call := len(s.abort)
	var err error
	if call <= len(s.abortErrs) {
		err = s.abortErrs[call-1]
	}
	s.mu.Unlock()
	return err
}

func (s *multipartTerminalStorage) completeCalls() []storage.CompleteMultipartRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]storage.CompleteMultipartRequest(nil), s.complete...)
}

func (s *multipartTerminalStorage) abortCalls() []storage.AbortMultipartRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]storage.AbortMultipartRequest(nil), s.abort...)
}

func newMultipartTerminalService(storagePort StoragePort, uploadID string) *Service {
	service := NewService(Dependencies{Storage: storagePort})
	now := time.Now().UTC()
	if err := service.multipartSessions.SaveMultipartSession(context.Background(), MultipartSession{UploadID: uploadID, CompletionID: "completion-" + uploadID, Target: storage.Target{PhysicalBucket: "bucket", LookupKey: "bucket", Key: "key", CanonicalURL: "s3://bucket/key"}, State: MultipartStateActive, CreatedAt: now, UpdatedAt: now}); err != nil {
		panic(err)
	}
	return service
}

var oneCompletedPart = []CompletedPart{{PartNumber: 1, ETag: "etag"}}

func multipartScopeContext(methods ...string) context.Context {
	resource := "/organization/org/project/project"
	privileges := map[string]map[string]bool{resource: {}}
	for _, method := range methods {
		privileges[resource][method] = true
	}
	session := access.NewSession("gen3")
	session.AuthHeaderPresent = true
	session.SetAuthorizations([]string{resource}, privileges, true)
	return access.WithSession(context.Background(), session)
}

func TestMultipartSessionReauthorizesPartAndCompletion(t *testing.T) {
	provider := &multipartTerminalStorage{beginIDs: []storage.UploadID{"upload"}}
	service := NewService(Dependencies{Objects: downloadObjectFake{object: testRecord()}, Storage: provider})
	target := storage.Target{PhysicalBucket: "bucket", Key: "key"}
	authorization := MultipartAuthorization{Scope: &AccessScope{Organization: "org", Project: "project"}, Methods: []string{"file_upload", "create", "update"}}
	if _, err := service.BeginMultipart(multipartScopeContext("file_upload"), MultipartInitRequest{Target: &target, Authorization: &authorization}); err != nil {
		t.Fatal(err)
	}
	readOnly := multipartScopeContext("read")
	if _, err := service.SignMultipartPart(readOnly, "upload", 1); !errors.Is(err, errorapi.ErrAccessDenied) {
		t.Fatalf("read-only part signing error = %v, want access denied", err)
	}
	if _, err := service.CompleteMultipart(readOnly, "upload", oneCompletedPart); !errors.Is(err, errorapi.ErrAccessDenied) {
		t.Fatalf("read-only completion error = %v, want access denied", err)
	}
	if len(provider.parts) != 0 || len(provider.completeCalls()) != 0 {
		t.Fatalf("unauthorized multipart request reached provider: parts=%+v completion=%+v", provider.parts, provider.completeCalls())
	}
}

func TestCompleteMultipartConcurrentRequestInvokesProviderOnce(t *testing.T) {
	const uploadID = "queued-upload"
	started := make(chan storage.CompleteMultipartRequest, 1)
	release := make(chan struct{})
	provider := &multipartTerminalStorage{started: started, completeFn: func(int, storage.CompleteMultipartRequest) error { <-release; return nil }}
	service := newMultipartTerminalService(provider, uploadID)
	first := make(chan error, 1)
	go func() {
		_, err := service.CompleteMultipart(context.Background(), uploadID, oneCompletedPart)
		first <- err
	}()
	<-started
	if _, err := service.CompleteMultipart(context.Background(), uploadID, oneCompletedPart); !errors.Is(err, errorapi.ErrConflict) {
		t.Fatalf("concurrent completion error = %v, want conflict", err)
	}
	close(release)
	if err := <-first; err != nil {
		t.Fatal(err)
	}
	location, err := service.CompleteMultipart(context.Background(), uploadID, oneCompletedPart)
	if err != nil || location != "s3://bucket/key" {
		t.Fatalf("completion retry = %q, %v", location, err)
	}
	if _, err := service.CompleteMultipart(context.Background(), uploadID, []CompletedPart{{PartNumber: 1, ETag: "different"}}); !errors.Is(err, errorapi.ErrConflict) {
		t.Fatalf("completion with different parts error = %v, want conflict", err)
	}
	if got := len(provider.completeCalls()); got != 1 {
		t.Fatalf("provider completion calls = %d, want 1", got)
	}
}

func TestCompleteMultipartProviderFailureLeavesSessionAvailable(t *testing.T) {
	const uploadID = "retry-upload"
	providerErr := errors.New("provider unavailable")
	provider := &multipartTerminalStorage{completeErrs: []error{providerErr, nil}}
	service := newMultipartTerminalService(provider, uploadID)

	if _, err := service.CompleteMultipart(context.Background(), uploadID, oneCompletedPart); !errors.Is(err, providerErr) {
		t.Fatalf("first completion error = %v, want %v", err, providerErr)
	}
	if _, err := service.CompleteMultipart(context.Background(), uploadID, oneCompletedPart); err != nil {
		t.Fatalf("retry completion error = %v", err)
	}
	if got := len(provider.completeCalls()); got != 2 {
		t.Fatalf("provider completion calls = %d, want 2", got)
	}
	if location, err := service.CompleteMultipart(context.Background(), uploadID, oneCompletedPart); err != nil || location != "s3://bucket/key" {
		t.Fatalf("completion after retry = %q, %v", location, err)
	}
}

func TestCompletedMultipartReplaySurvivesReceiptRetention(t *testing.T) {
	const uploadID = "retained-completion"
	provider := &multipartTerminalStorage{}
	store := newMemoryMultipartSessionStore()
	now := time.Date(2026, time.September, 12, 12, 0, 0, 0, time.UTC)
	if err := store.SaveMultipartSession(context.Background(), MultipartSession{
		UploadID:      uploadID,
		CompletionID:  "completion-id",
		Target:        storage.Target{Key: "key", CanonicalURL: "s3://bucket/key"},
		Authorization: MultipartAuthorization{Scope: &AccessScope{Organization: "org", Project: "project"}, Methods: []string{"update"}},
		State:         MultipartStateActive,
		CreatedAt:     now,
		UpdatedAt:     now,
	}); err != nil {
		t.Fatal(err)
	}
	service := NewService(Dependencies{Storage: provider, MultipartSessions: store, Now: func() time.Time { return now }})
	if location, err := service.CompleteMultipart(multipartScopeContext("update"), uploadID, oneCompletedPart); err != nil || location != "s3://bucket/key" {
		t.Fatalf("initial completion = %q, %v", location, err)
	}

	now = now.Add(2 * time.Hour)
	if err := service.ReconcileMultipartSessions(context.Background(), time.Hour, time.Hour, 10); err != nil {
		t.Fatalf("ReconcileMultipartSessions() error = %v", err)
	}
	if _, err := service.CompleteMultipart(multipartScopeContext("read"), uploadID, oneCompletedPart); !errors.Is(err, errorapi.ErrAccessDenied) {
		t.Fatalf("unauthorized completion replay error = %v, want access denied", err)
	}
	if location, err := service.CompleteMultipart(multipartScopeContext("update"), uploadID, oneCompletedPart); err != nil || location != "s3://bucket/key" {
		t.Fatalf("completion replay after retention = %q, %v", location, err)
	}
	if got := len(provider.completeCalls()); got != 1 {
		t.Fatalf("provider completion calls = %d, want 1", got)
	}
}

func TestCompleteMultipartProviderFailureBindsFirstParts(t *testing.T) {
	const uploadID = "bound-parts-upload"
	providerErr := errors.New("provider unavailable")
	provider := &multipartTerminalStorage{completeErrs: []error{providerErr}}
	service := newMultipartTerminalService(provider, uploadID)

	if _, err := service.CompleteMultipart(context.Background(), uploadID, oneCompletedPart); !errors.Is(err, providerErr) {
		t.Fatalf("first completion error = %v, want %v", err, providerErr)
	}
	if _, err := service.CompleteMultipart(context.Background(), uploadID, []CompletedPart{{PartNumber: 1, ETag: "different"}}); !errors.Is(err, errorapi.ErrConflict) {
		t.Fatalf("different-parts retry error = %v, want conflict", err)
	}
	if got := len(provider.completeCalls()); got != 1 {
		t.Fatalf("provider completion calls = %d, want 1", got)
	}
}

func TestIndeterminateMultipartCompletionKeepsLeaseUntilSafeRecovery(t *testing.T) {
	const uploadID = "indeterminate-upload"
	provider := &multipartTerminalStorage{completeErrs: []error{storage.ErrMultipartCompletionIndeterminate, nil}}
	store := newMemoryMultipartSessionStore()
	now := time.Now().UTC()
	if err := store.SaveMultipartSession(context.Background(), MultipartSession{
		UploadID:     uploadID,
		CompletionID: "completion-id",
		Target:       storage.Target{Key: "key", CanonicalURL: "s3://bucket/key"},
		State:        MultipartStateActive,
		CreatedAt:    now,
		UpdatedAt:    now,
	}); err != nil {
		t.Fatal(err)
	}
	service := NewService(Dependencies{Storage: provider, MultipartSessions: store, Now: func() time.Time { return now }})
	if _, err := service.CompleteMultipart(context.Background(), uploadID, oneCompletedPart); !errors.Is(err, storage.ErrMultipartCompletionIndeterminate) {
		t.Fatalf("first completion error = %v, want indeterminate", err)
	}
	if _, err := service.CompleteMultipart(context.Background(), uploadID, oneCompletedPart); !errors.Is(err, errorapi.ErrConflict) {
		t.Fatalf("completion during indeterminate lease error = %v, want conflict", err)
	}
	if got := len(provider.completeCalls()); got != 1 {
		t.Fatalf("provider calls during lease = %d, want 1", got)
	}
	now = now.Add(2 * time.Hour)
	if location, err := service.CompleteMultipart(context.Background(), uploadID, oneCompletedPart); err != nil || location != "s3://bucket/key" {
		t.Fatalf("stale-lease recovery = %q, %v", location, err)
	}
}

func TestCompleteMultipartDoesNotReportSuccessWhenFinishLosesOwnership(t *testing.T) {
	const uploadID = "lost-finish-ownership"
	provider := &multipartTerminalStorage{}
	baseStore := newMemoryMultipartSessionStore()
	now := time.Now().UTC()
	if err := baseStore.SaveMultipartSession(context.Background(), MultipartSession{
		UploadID:     uploadID,
		CompletionID: "completion-id",
		Target:       storage.Target{Key: "key", CanonicalURL: "s3://bucket/key"},
		State:        MultipartStateActive,
		CreatedAt:    now,
		UpdatedAt:    now,
	}); err != nil {
		t.Fatal(err)
	}
	service := NewService(Dependencies{Storage: provider, MultipartSessions: falseFinishMultipartStore{MultipartSessionStore: baseStore}})
	if location, err := service.CompleteMultipart(context.Background(), uploadID, oneCompletedPart); !errors.Is(err, errorapi.ErrConflict) || location != "" {
		t.Fatalf("completion = %q, %v; want conflict without location", location, err)
	}
}

type falseFinishMultipartStore struct {
	MultipartSessionStore
}

func (falseFinishMultipartStore) FinishMultipartCompletion(context.Context, string, string, string, time.Time) (bool, error) {
	return false, nil
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
	now := time.Now().UTC()
	for uploadID, target := range map[string]storage.Target{
		"upload-one": {Key: "one"},
		"upload-two": {Key: "two"},
	} {
		if err := service.multipartSessions.SaveMultipartSession(context.Background(), MultipartSession{UploadID: uploadID, CompletionID: "completion-" + uploadID, Target: target, State: MultipartStateActive, CreatedAt: now, UpdatedAt: now}); err != nil {
			t.Fatal(err)
		}
	}
	results := make(chan error, 2)
	go func() {
		_, err := service.CompleteMultipart(context.Background(), "upload-one", oneCompletedPart)
		results <- err
	}()
	go func() {
		_, err := service.CompleteMultipart(context.Background(), "upload-two", oneCompletedPart)
		results <- err
	}()
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

func TestBeginMultipartRejectsOpaqueProviderIDCollision(t *testing.T) {
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
	go func() {
		_, err := service.CompleteMultipart(context.Background(), uploadID, oneCompletedPart)
		oldResult <- err
	}()
	<-firstStarted
	if _, err := service.BeginMultipart(context.Background(), MultipartInitRequest{Target: &newTarget}); !errors.Is(err, errorapi.ErrConflict) {
		t.Fatalf("begin replacement multipart error = %v, want conflict", err)
	}
	close(releaseFirst)
	if err := <-oldResult; err != nil {
		t.Fatalf("old completion error = %v", err)
	}

	calls := provider.completeCalls()
	if len(calls) != 1 {
		t.Fatalf("provider completion calls = %d, want 1", len(calls))
	}
	if calls[0].Target.Key != oldTarget.Key {
		t.Fatalf("completion target = %q, want %q", calls[0].Target.Key, oldTarget.Key)
	}
}

func TestMultipartRejectsInvalidInputsBeforeProviderDispatch(t *testing.T) {
	provider := &multipartTerminalStorage{}
	service := newMultipartTerminalService(provider, "upload")

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
			if _, err := service.CompleteMultipart(context.Background(), "upload", testCase.parts); !errors.Is(err, errorapi.ErrInvalidInput) {
				t.Fatalf("completion error = %v, want invalid input", err)
			}
		})
	}
	if len(provider.completeCalls()) != 0 {
		t.Fatalf("invalid completion reached provider: %+v", provider.completeCalls())
	}
}

func TestAbortMultipartIsIdempotentAndAuthorizesBeforeProvider(t *testing.T) {
	const uploadID = "abort-upload"
	provider := &multipartTerminalStorage{}
	service := newMultipartTerminalService(provider, uploadID)

	if err := service.AbortMultipart(context.Background(), uploadID); err != nil {
		t.Fatalf("first abort error = %v", err)
	}
	if err := service.AbortMultipart(context.Background(), uploadID); err != nil {
		t.Fatalf("repeated abort error = %v", err)
	}
	if got := len(provider.abortCalls()); got != 1 {
		t.Fatalf("provider abort calls = %d, want 1", got)
	}
	if err := service.AbortMultipart(context.Background(), "missing-upload"); err != nil {
		t.Fatalf("missing abort error = %v, want nil", err)
	}

	protected := NewService(Dependencies{Storage: provider})
	authorized := MultipartAuthorization{Scope: &AccessScope{Organization: "org", Project: "project"}, Methods: []string{"update"}}
	if err := protected.multipartSessions.SaveMultipartSession(context.Background(), MultipartSession{UploadID: "protected-upload", CompletionID: "completion", Target: storage.Target{Key: "key"}, Authorization: authorized, State: MultipartStateActive, CreatedAt: time.Now(), UpdatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if err := protected.AbortMultipart(multipartScopeContext("read"), "protected-upload"); !errors.Is(err, errorapi.ErrAccessDenied) {
		t.Fatalf("unauthorized abort error = %v, want access denied", err)
	}
	if got := len(provider.abortCalls()); got != 1 {
		t.Fatalf("unauthorized abort reached provider, calls = %d", got)
	}
}

func TestCompletionFinalizersCannotConsumeAbortLease(t *testing.T) {
	store := newMemoryMultipartSessionStore()
	now := time.Date(2026, time.September, 12, 12, 0, 0, 0, time.UTC)
	if err := store.SaveMultipartSession(context.Background(), MultipartSession{
		UploadID:        "upload",
		State:           MultipartStateCompleting,
		Operation:       MultipartOperationAbort,
		CompletionToken: "token",
		CreatedAt:       now,
		UpdatedAt:       now,
	}); err != nil {
		t.Fatalf("SaveMultipartSession failed: %v", err)
	}

	if err := store.ReleaseMultipartCompletion(context.Background(), "upload", "token", now.Add(time.Minute)); err != nil {
		t.Fatalf("ReleaseMultipartCompletion failed: %v", err)
	}
	if finished, err := store.FinishMultipartCompletion(context.Background(), "upload", "token", "location", now.Add(time.Minute)); err != nil || finished {
		t.Fatalf("FinishMultipartCompletion = %v, %v; want false, nil", finished, err)
	}
	session, err := store.GetMultipartSession(context.Background(), "upload")
	if err != nil {
		t.Fatalf("GetMultipartSession failed: %v", err)
	}
	if session.State != MultipartStateCompleting || session.Operation != MultipartOperationAbort || session.CompletionToken != "token" {
		t.Fatalf("abort lease changed by completion finalizer: %+v", session)
	}
}

func TestCompleteMultipartSortsPartsAndPreservesETags(t *testing.T) {
	provider := &multipartTerminalStorage{}
	service := newMultipartTerminalService(provider, "upload")
	parts := []CompletedPart{{PartNumber: 7, ETag: "seven"}, {PartNumber: 2, ETag: "two"}}
	if _, err := service.CompleteMultipart(context.Background(), "upload", parts); err != nil {
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

func TestSignMultipartPartTouchesSessionActivity(t *testing.T) {
	provider := &multipartTerminalStorage{}
	store := newMemoryMultipartSessionStore()
	created := time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)
	service := NewService(Dependencies{Storage: provider, MultipartSessions: store, Now: func() time.Time { return created.Add(time.Minute) }})
	if err := store.SaveMultipartSession(context.Background(), MultipartSession{UploadID: "active-upload", CompletionID: "completion", Target: storage.Target{Key: "key"}, State: MultipartStateActive, CreatedAt: created, UpdatedAt: created}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.SignMultipartPart(context.Background(), "active-upload", 1); err != nil {
		t.Fatalf("SignMultipartPart() error = %v", err)
	}
	session, err := store.GetMultipartSession(context.Background(), "active-upload")
	if err != nil {
		t.Fatal(err)
	}
	if !session.UpdatedAt.Equal(created.Add(time.Minute)) {
		t.Fatalf("updated_at = %s, want %s", session.UpdatedAt, created.Add(time.Minute))
	}
}

func TestBeginMultipartRejectsEmptyProviderUploadID(t *testing.T) {
	provider := &multipartTerminalStorage{beginIDs: []storage.UploadID{""}}
	service := NewService(Dependencies{Objects: downloadObjectFake{object: testRecord()}, Storage: provider})
	target := storage.Target{PhysicalBucket: "bucket", Key: "key"}
	if _, err := service.BeginMultipart(context.Background(), MultipartInitRequest{Target: &target}); err == nil {
		t.Fatal("BeginMultipart accepted an empty provider upload ID")
	}
	if _, err := service.multipartSessions.GetMultipartSession(context.Background(), ""); !errors.Is(err, errorapi.ErrMultipartUploadNotFound) {
		t.Fatalf("empty upload ID session lookup error = %v, want not found", err)
	}
}
