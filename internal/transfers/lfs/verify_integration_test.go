package lfs_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/calypr/syfon/apigen/drs"
	"github.com/calypr/syfon/apigen/errorapi"
	"github.com/calypr/syfon/apigen/lfsapi"
	"github.com/calypr/syfon/internal/objects"
	"github.com/calypr/syfon/internal/persistence/sqlite"
	"github.com/calypr/syfon/internal/persistence/store"
	lfs "github.com/calypr/syfon/internal/transfers/lfs"
)

type retryRegistration struct {
	*objects.Service
	err error
}

type uploadRecorder struct{}

func (uploadRecorder) RecordFileUpload(context.Context, string) error { return nil }

type concurrentUploadRecorder struct {
	mu  sync.Mutex
	ids []string
}

func (r *concurrentUploadRecorder) RecordFileUpload(_ context.Context, objectID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.ids = append(r.ids, objectID)
	return nil
}

func (r *concurrentUploadRecorder) recordedIDs() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.ids...)
}

type concurrentPendingStore struct {
	*store.Store
	arrived chan struct{}
	release chan struct{}
}

func newConcurrentPendingStore(database *store.Store) *concurrentPendingStore {
	return &concurrentPendingStore{
		Store:   database,
		arrived: make(chan struct{}, 2),
		release: make(chan struct{}),
	}
}

func (s *concurrentPendingStore) ConsumePendingMetadata(ctx context.Context, expected lfs.PendingMetadata) (bool, error) {
	s.arrived <- struct{}{}
	<-s.release
	return s.Store.ConsumePendingMetadata(ctx, expected)
}

func (r *retryRegistration) RegisterObjects(ctx context.Context, records []drs.DrsObject) ([]drs.DrsObject, error) {
	if r.err != nil {
		return nil, r.err
	}
	return r.Service.RegisterObjects(ctx, records)
}

func TestVerifyRetriesRealPendingMetadataAfterRegistrationFailure(t *testing.T) {
	database, err := sqlite.NewSqliteDB(":memory:", nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })

	ctx := context.Background()
	oid := strings.Repeat("b", 64)
	providerType, location := "s3", "s3://bucket/"+oid
	candidate := lfsapi.DrsObjectCandidate{
		Checksums:     &[]lfsapi.Checksum{{Type: "sha256", Checksum: oid}},
		AccessMethods: &[]lfsapi.AccessMethod{{Type: &providerType, AccessUrl: &lfsapi.AccessMethodAccessUrl{Url: &location}}},
	}
	now := time.Now().UTC()
	pending := lfs.PendingMetadata{OID: oid, Candidate: candidate, CreatedAt: now, ExpiresAt: now.Add(time.Hour)}
	if err := database.SavePendingMetadata(ctx, []lfs.PendingMetadata{pending}); err != nil {
		t.Fatal(err)
	}

	registrationError := errors.New("registration unavailable")
	registrar := &retryRegistration{Service: objects.NewService(database), err: registrationError}
	service := lfs.NewService(nil, registrar, nil, database, uploadRecorder{}, nil)
	if err := service.Verify(ctx, oid); !errors.Is(err, registrationError) {
		t.Fatalf("first Verify() error = %v, want %v", err, registrationError)
	}
	if _, err := database.GetPendingMetadata(ctx, oid); err != nil {
		t.Fatalf("pending metadata after failed registration: %v", err)
	}

	registrar.err = nil
	if err := service.Verify(ctx, oid); err != nil {
		t.Fatalf("retry Verify() error = %v", err)
	}
	if _, err := database.GetPendingMetadata(ctx, oid); !errorapi.IsNotFoundError(err) {
		t.Fatalf("pending metadata after successful retry error = %v, want not found", err)
	}
	if _, err := registrar.GetObject(ctx, oid, "read"); err != nil {
		t.Fatalf("registered object lookup: %v", err)
	}
}

func TestConcurrentVerifyAccountsOnceWithDurableObjectID(t *testing.T) {
	database, err := sqlite.NewSqliteDB(":memory:", nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })

	ctx := context.Background()
	oid := strings.Repeat("c", 64)
	canonicalID := "durable-object-id"
	providerType, location := "s3", "s3://bucket/"+oid
	methods := []drs.AccessMethod{{Type: "s3", AccessUrl: &drs.AccessURL{Url: location}}}
	if err := database.RegisterObjects(ctx, []drs.DrsObject{{
		Id: canonicalID, Checksums: []drs.Checksum{{Type: "sha256", Checksum: oid}}, AccessMethods: &methods,
	}}); err != nil {
		t.Fatalf("seed canonical object: %v", err)
	}
	candidate := lfsapi.DrsObjectCandidate{
		Checksums:     &[]lfsapi.Checksum{{Type: "sha256", Checksum: oid}},
		AccessMethods: &[]lfsapi.AccessMethod{{Type: &providerType, AccessUrl: &lfsapi.AccessMethodAccessUrl{Url: &location}}},
	}
	now := time.Now().UTC()
	pending := lfs.PendingMetadata{OID: oid, Candidate: candidate, CreatedAt: now, ExpiresAt: now.Add(time.Hour)}
	if err := database.SavePendingMetadata(ctx, []lfs.PendingMetadata{pending}); err != nil {
		t.Fatalf("save pending metadata: %v", err)
	}

	pendingStore := newConcurrentPendingStore(database)
	recorder := &concurrentUploadRecorder{}
	service := lfs.NewService(nil, objects.NewService(database), nil, pendingStore, recorder, nil)
	errs := make(chan error, 2)
	for range 2 {
		go func() { errs <- service.Verify(ctx, oid) }()
	}
	for range 2 {
		select {
		case <-pendingStore.arrived:
		case <-time.After(2 * time.Second):
			close(pendingStore.release)
			t.Fatal("concurrent verifiers did not both reach pending consumption")
		}
	}
	close(pendingStore.release)
	for range 2 {
		if err := <-errs; err != nil {
			t.Fatalf("concurrent Verify() error = %v", err)
		}
	}
	if got, want := recorder.recordedIDs(), []string{canonicalID}; len(got) != len(want) || got[0] != want[0] {
		t.Fatalf("upload accounting IDs = %v, want %v", got, want)
	}
	if err := service.Verify(ctx, oid); err != nil {
		t.Fatalf("completed Verify() retry error = %v", err)
	}
	if got := recorder.recordedIDs(); len(got) != 1 {
		t.Fatalf("upload accounting after retry = %v, want one durable event", got)
	}
}
