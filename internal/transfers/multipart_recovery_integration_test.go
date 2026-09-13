package transfers_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/calypr/syfon/apigen/drs"
	"github.com/calypr/syfon/internal/buckets"
	"github.com/calypr/syfon/internal/persistence/sqlite"
	"github.com/calypr/syfon/internal/storage"
	filestorage "github.com/calypr/syfon/internal/storage/file"
	"github.com/calypr/syfon/internal/transfers"
)

func TestMultipartCompletionRecoversAfterProviderCommitBeforeDatabaseFinish(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	registration, err := filestorage.New(root)
	if err != nil {
		t.Fatal(err)
	}
	manager, err := storage.NewManager(multipartRecoveryCredentials{}, registration)
	if err != nil {
		t.Fatal(err)
	}
	databasePath := filepath.Join(t.TempDir(), "multipart.db")
	firstDatabase, err := sqlite.NewSqliteDB(databasePath, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = firstDatabase.Close() })

	now := time.Now().UTC()
	finishErr := errors.New("injected finish failure")
	firstStore := &failFirstMultipartFinishStore{MultipartSessionStore: firstDatabase, err: finishErr}
	first := transfers.NewService(transfers.Dependencies{
		Objects:           multipartRecoveryObjects{},
		Storage:           manager,
		MultipartSessions: firstStore,
		Now:               func() time.Time { return now },
	})
	target := storage.Target{
		Provider:     "file",
		Key:          "nested/object.bin",
		Path:         filepath.Join(root, "nested", "object.bin"),
		CanonicalURL: "file://nested/object.bin",
	}
	started, err := first.BeginMultipart(ctx, transfers.MultipartInitRequest{Target: &target})
	if err != nil {
		t.Fatal(err)
	}
	partLocation, err := first.SignMultipartPart(ctx, started.UploadID, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(partLocation), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(partLocation, []byte("provider committed bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	parts := []transfers.CompletedPart{{PartNumber: 1, ETag: "part-one"}}
	if _, err := first.CompleteMultipart(ctx, started.UploadID, parts); !errors.Is(err, finishErr) {
		t.Fatalf("first completion error = %v, want injected database failure", err)
	}

	secondDatabase, err := sqlite.NewSqliteDB(databasePath, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = secondDatabase.Close() })
	now = now.Add(2 * time.Hour)
	second := transfers.NewService(transfers.Dependencies{
		Objects:           multipartRecoveryObjects{},
		Storage:           manager,
		MultipartSessions: secondDatabase,
		Now:               func() time.Time { return now },
	})
	location, err := second.CompleteMultipart(ctx, started.UploadID, parts)
	if err != nil {
		t.Fatalf("completion recovery failed after provider commit: %v", err)
	}
	if location != target.CanonicalURL {
		t.Fatalf("recovered location = %q, want %q", location, target.CanonicalURL)
	}
	contents, err := os.ReadFile(target.Path)
	if err != nil {
		t.Fatal(err)
	}
	if string(contents) != "provider committed bytes" {
		t.Fatalf("recovered contents = %q", contents)
	}
}

type failFirstMultipartFinishStore struct {
	transfers.MultipartSessionStore
	err error
}

func (s *failFirstMultipartFinishStore) FinishMultipartCompletion(context.Context, string, string, string, time.Time) (bool, error) {
	if s.err != nil {
		err := s.err
		s.err = nil
		return false, err
	}
	panic("unexpected second finish through first store")
}

type multipartRecoveryCredentials struct{}

func (multipartRecoveryCredentials) GetS3Credential(context.Context, string) (*buckets.Credential, error) {
	return nil, nil
}

type multipartRecoveryObjects struct{}

func (multipartRecoveryObjects) GetObject(context.Context, string, string) (*drs.DrsObject, error) {
	return nil, errors.New("unexpected object lookup")
}

func (multipartRecoveryObjects) GetObjectsByChecksums(context.Context, []string, string) (map[string][]drs.DrsObject, error) {
	return nil, errors.New("unexpected checksum lookup")
}
