package lfs

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/calypr/syfon/internal/faults"
	"github.com/calypr/syfon/internal/objects"
)

type pendingFake struct {
	saved []PendingMetadata
}

func (f *pendingFake) SavePendingMetadata(_ context.Context, entries []PendingMetadata) error {
	f.saved = append([]PendingMetadata(nil), entries...)
	return nil
}

func (f *pendingFake) GetPendingMetadata(context.Context, string) (*PendingMetadata, error) {
	return nil, nil
}

func (f *pendingFake) PopPendingMetadata(context.Context, string) (*PendingMetadata, error) {
	return nil, nil
}

func TestStagePendingMetadataDefaultsCanonicalOIDAndTwentyMinuteTTL(t *testing.T) {
	pending := &pendingFake{}
	workflow := NewMetadataWorkflow(pending, nil, nil)
	now := time.Date(2026, 9, 6, 12, 0, 0, 0, time.FixedZone("PDT", -7*60*60))
	workflow.now = func() time.Time { return now }
	sha := "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	candidate := objects.Candidate{Checksums: &[]objects.Checksum{{Type: "sha256", Checksum: sha}}}
	if err := workflow.StagePendingMetadata(context.Background(), PendingMetadata{Candidate: candidate}); err != nil {
		t.Fatalf("StagePendingMetadata() error = %v", err)
	}
	if len(pending.saved) != 1 {
		t.Fatalf("saved %d entries, want 1", len(pending.saved))
	}
	got := pending.saved[0]
	if got.OID != sha || !got.CreatedAt.Equal(now.UTC()) || !got.ExpiresAt.Equal(now.UTC().Add(PendingMetadataTTL)) {
		t.Fatalf("unexpected staged metadata: %+v", got)
	}
}

func TestStagePendingMetadataRejectsMissingOIDAndChecksum(t *testing.T) {
	workflow := NewMetadataWorkflow(&pendingFake{}, nil, nil)
	if err := workflow.StagePendingMetadata(context.Background(), PendingMetadata{}); !errors.Is(err, faults.ErrInvalidInput) {
		t.Fatalf("StagePendingMetadata() error = %v, want invalid input", err)
	}
}
