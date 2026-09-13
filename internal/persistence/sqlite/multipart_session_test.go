package sqlite

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/calypr/syfon/apigen/errorapi"
	"github.com/calypr/syfon/internal/storage"
	"github.com/calypr/syfon/internal/transfers"
)

func TestMultipartSessionPersistsAcrossDatabaseHandles(t *testing.T) {
	path := filepath.Join(t.TempDir(), "multipart.db")
	first, err := NewSqliteDB(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = first.Close() })
	second, err := NewSqliteDB(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = second.Close() })

	now := time.Now().UTC().Truncate(time.Microsecond)
	session := transfers.MultipartSession{
		UploadID:      "provider-upload-id",
		CompletionID:  "completion-id",
		Target:        storage.Target{Provider: "s3", PhysicalBucket: "bucket", Key: "project/file", CanonicalURL: "s3://bucket/project/file"},
		Authorization: transfers.MultipartAuthorization{Scope: &transfers.AccessScope{Organization: "org", Project: "project"}, Methods: []string{"file_upload", "create"}},
		State:         transfers.MultipartStateActive,
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	ctx := context.Background()
	if err := first.SaveMultipartSession(ctx, session); err != nil {
		t.Fatal(err)
	}
	replacement := session
	replacement.CompletionID = "replacement-completion"
	replacement.Target.Key = "replacement"
	if err := second.SaveMultipartSession(ctx, replacement); !errors.Is(err, errorapi.ErrConflict) {
		t.Fatalf("duplicate upload ID error = %v, want conflict", err)
	}
	loaded, err := second.GetMultipartSession(ctx, session.UploadID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.CompletionID != session.CompletionID || loaded.Target.CanonicalURL != "s3://bucket/project/file" || loaded.Authorization.Scope == nil || loaded.Authorization.Scope.Project != "project" {
		t.Fatalf("loaded multipart session = %+v", loaded)
	}
	claimed, ok, err := second.ClaimMultipartCompletion(ctx, session.UploadID, "claim", "parts", now.Add(time.Minute), now.Add(-time.Hour))
	if err != nil || !ok || claimed.State != transfers.MultipartStateCompleting {
		t.Fatalf("completion claim = %+v, %t, %v", claimed, ok, err)
	}
	if claimed.PartsFingerprint != "parts" {
		t.Fatalf("completion parts fingerprint = %q, want parts", claimed.PartsFingerprint)
	}
	if stale, ok, err := second.ClaimMultipartCompletion(ctx, session.UploadID, "wrong-claim", "different-parts", now.Add(2*time.Hour), now.Add(time.Hour)); err != nil || ok || stale.PartsFingerprint != "parts" {
		t.Fatalf("different-parts stale claim = %+v, %t, %v", stale, ok, err)
	}
	if reclaimed, ok, err := second.ClaimMultipartCompletion(ctx, session.UploadID, "reclaim", "parts", now.Add(2*time.Hour), now.Add(time.Hour)); err != nil || !ok || reclaimed.PartsFingerprint != "parts" {
		t.Fatalf("same-parts stale claim = %+v, %t, %v", reclaimed, ok, err)
	}
	if ok, err := second.FinishMultipartCompletion(ctx, session.UploadID, "reclaim", session.Target.CanonicalURL, now.Add(2*time.Hour+time.Minute)); err != nil || !ok {
		t.Fatalf("finish completion = %t, %v", ok, err)
	}
	completed, err := first.GetMultipartSession(ctx, session.UploadID)
	if err != nil {
		t.Fatal(err)
	}
	if completed.State != transfers.MultipartStateCompleted || completed.CompletedLocation != session.Target.CanonicalURL {
		t.Fatalf("completed multipart session = %+v", completed)
	}
	if err := second.CompactCompletedMultipartSessions(ctx, now.Add(3*time.Hour), 10); err != nil {
		t.Fatalf("compact completed multipart session: %v", err)
	}
	if _, err := first.GetMultipartSession(ctx, session.UploadID); !errors.Is(err, errorapi.ErrMultipartUploadNotFound) {
		t.Fatalf("compacted session lookup error = %v, want multipart upload not found", err)
	}
	receipt, err := first.GetMultipartCompletionReceipt(ctx, session.UploadID)
	if err != nil {
		t.Fatalf("load multipart completion receipt: %v", err)
	}
	if receipt.CompletedLocation != session.Target.CanonicalURL || receipt.PartsFingerprint != "parts" || receipt.Authorization.Scope == nil || receipt.Authorization.Scope.Project != "project" {
		t.Fatalf("multipart completion receipt = %+v", receipt)
	}
	if err := first.SaveMultipartSession(ctx, replacement); !errors.Is(err, errorapi.ErrConflict) {
		t.Fatalf("compacted upload ID reuse error = %v, want conflict", err)
	}
}
