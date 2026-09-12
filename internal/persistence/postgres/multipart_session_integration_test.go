package postgres_test

import (
	"context"
	"testing"
	"time"

	"github.com/calypr/syfon/internal/storage"
	"github.com/calypr/syfon/internal/transfers"
)

func TestPostgresMultipartCompletionClaimAndRetryState(t *testing.T) {
	database := openPostgresTestStore(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Microsecond)
	session := transfers.MultipartSession{
		UploadID:     "postgres-multipart-session",
		CompletionID: "postgres-completion-id",
		Target:       storage.Target{Provider: "s3", PhysicalBucket: "bucket", Key: "project/file", CanonicalURL: "s3://bucket/project/file"},
		State:        transfers.MultipartStateActive,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	if err := database.SaveMultipartSession(ctx, session); err != nil {
		t.Fatal(err)
	}
	claimed, ok, err := database.ClaimMultipartCompletion(ctx, session.UploadID, "claim", "parts", now.Add(time.Minute), now.Add(-time.Hour))
	if err != nil || !ok || claimed.PartsFingerprint != "parts" {
		t.Fatalf("completion claim = %+v, %t, %v", claimed, ok, err)
	}
	if _, ok, err := database.ClaimMultipartCompletion(ctx, session.UploadID, "other", "different", now.Add(2*time.Minute), now.Add(-time.Hour)); err != nil || ok {
		t.Fatalf("competing completion claim = %t, %v", ok, err)
	}
	if stale, ok, err := database.ClaimMultipartCompletion(ctx, session.UploadID, "wrong-stale", "different", now.Add(2*time.Hour), now.Add(time.Hour)); err != nil || ok || stale.PartsFingerprint != "parts" {
		t.Fatalf("different-parts stale claim = %+v, %t, %v", stale, ok, err)
	}
	if _, ok, err := database.ClaimMultipartCompletion(ctx, session.UploadID, "same-stale", "parts", now.Add(2*time.Hour), now.Add(time.Hour)); err != nil || !ok {
		t.Fatalf("same-parts stale claim = %t, %v", ok, err)
	}
	if ok, err := database.FinishMultipartCompletion(ctx, session.UploadID, "same-stale", session.Target.CanonicalURL, now.Add(2*time.Hour+time.Minute)); err != nil || !ok {
		t.Fatalf("finish completion = %t, %v", ok, err)
	}
	completed, err := database.GetMultipartSession(ctx, session.UploadID)
	if err != nil {
		t.Fatal(err)
	}
	if completed.State != transfers.MultipartStateCompleted || completed.CompletedLocation != session.Target.CanonicalURL || completed.PartsFingerprint != "parts" {
		t.Fatalf("completed multipart session = %+v", completed)
	}
}
