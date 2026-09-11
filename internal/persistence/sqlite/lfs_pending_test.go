package sqlite

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/calypr/syfon/apigen/errorapi"
	"github.com/calypr/syfon/apigen/lfsapi"
	transferlfs "github.com/calypr/syfon/internal/transfers/lfs"
)

func TestSqlitePendingLFSConsumptionPreservesRestageWithEqualTimestamps(t *testing.T) {
	db, err := NewSqliteDB(":memory:", nil)
	if err != nil {
		t.Fatalf("failed to create db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	ctx := context.Background()
	oid := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	now := time.Now().UTC().Truncate(time.Microsecond)
	typeName := "s3"
	url := "s3://bucket/" + oid
	oldCandidate := lfsapi.DrsObjectCandidate{
		Name:          sqliteTestPtr("old"),
		Checksums:     &[]lfsapi.Checksum{{Type: "sha256", Checksum: oid}},
		AccessMethods: &[]lfsapi.AccessMethod{{Type: &typeName, AccessUrl: &lfsapi.AccessMethodAccessUrl{Url: &url}}},
	}
	old := transferlfs.PendingMetadata{OID: oid, Candidate: oldCandidate, CreatedAt: now, ExpiresAt: now.Add(time.Hour)}
	if err := db.SavePendingMetadata(ctx, []transferlfs.PendingMetadata{old}); err != nil {
		t.Fatalf("save old pending metadata: %v", err)
	}
	loaded, err := db.GetPendingMetadata(ctx, oid)
	if err != nil {
		t.Fatalf("load old pending metadata: %v", err)
	}

	newCandidate := oldCandidate
	newCandidate.Name = sqliteTestPtr("new")
	replacement := old
	replacement.Candidate = newCandidate
	if err := db.SavePendingMetadata(ctx, []transferlfs.PendingMetadata{replacement}); err != nil {
		t.Fatalf("save replacement pending metadata: %v", err)
	}
	if err := db.ConsumePendingMetadata(ctx, *loaded); err != nil {
		t.Fatalf("consume old pending metadata: %v", err)
	}

	got, err := db.GetPendingMetadata(ctx, oid)
	if err != nil {
		t.Fatalf("replacement was removed: %v", err)
	}
	if sqliteTestStringVal(got.Candidate.Name) != "new" {
		t.Fatalf("replacement candidate = %q, want new", sqliteTestStringVal(got.Candidate.Name))
	}
}

func TestSqlitePendingLFSConsumptionAcceptsLegacyJSONFormatting(t *testing.T) {
	db, err := NewSqliteDB(":memory:", nil)
	if err != nil {
		t.Fatalf("failed to create db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	ctx := context.Background()
	oid := "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	now := time.Now().UTC().Truncate(time.Microsecond)
	legacyJSON := `{"checksums" : [ { "checksum" : "` + oid + `", "type" : "sha256" } ], "name" : "legacy"}`
	if _, err := db.DB().ExecContext(ctx, `INSERT INTO lfs_pending_metadata (oid, candidate_json, created_time, expires_time) VALUES (?, ?, ?, ?)`, oid, legacyJSON, now, now.Add(time.Hour)); err != nil {
		t.Fatalf("insert legacy pending metadata: %v", err)
	}

	entry, err := db.GetPendingMetadata(ctx, oid)
	if err != nil {
		t.Fatalf("load legacy pending metadata: %v", err)
	}
	if err := db.ConsumePendingMetadata(ctx, *entry); err != nil {
		t.Fatalf("consume legacy pending metadata: %v", err)
	}
	if _, err := db.GetPendingMetadata(ctx, oid); !errors.Is(err, errorapi.ErrNotFound) {
		t.Fatalf("legacy pending metadata still exists, error = %v", err)
	}
}

func TestSqlitePendingLFSConsumptionMissingEntryIsNoOp(t *testing.T) {
	db, err := NewSqliteDB(":memory:", nil)
	if err != nil {
		t.Fatalf("failed to create db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	entry := transferlfs.PendingMetadata{
		OID:       "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc",
		CreatedAt: time.Now().UTC(),
		ExpiresAt: time.Now().UTC().Add(time.Hour),
	}
	if err := db.ConsumePendingMetadata(context.Background(), entry); err != nil {
		t.Fatalf("missing entry consumption = %v, want nil", err)
	}
}
