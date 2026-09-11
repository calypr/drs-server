package lfs_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/calypr/syfon/apigen/drs"
	"github.com/calypr/syfon/apigen/errorapi"
	"github.com/calypr/syfon/apigen/lfsapi"
	"github.com/calypr/syfon/internal/objects"
	"github.com/calypr/syfon/internal/persistence/sqlite"
	lfs "github.com/calypr/syfon/internal/transfers/lfs"
)

type retryRegistration struct {
	*objects.Service
	err error
}

type uploadRecorder struct{}

func (uploadRecorder) RecordFileUpload(context.Context, string) error { return nil }

func (r *retryRegistration) RegisterObjects(ctx context.Context, records []drs.DrsObject) error {
	if r.err != nil {
		return r.err
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
