package postgres_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/calypr/syfon/apigen/errorapi"
	"github.com/calypr/syfon/apigen/lfsapi"
	transferlfs "github.com/calypr/syfon/internal/transfers/lfs"
)

func TestPostgresPendingLFSConsumption(t *testing.T) {
	database := openPostgresTestStore(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Microsecond)

	t.Run("unchanged", func(t *testing.T) {
		oid := strings.Repeat("d", 64)
		entry := postgresPendingMetadata(oid, "unchanged", now)
		if err := database.SavePendingMetadata(ctx, []transferlfs.PendingMetadata{entry}); err != nil {
			t.Fatal(err)
		}
		loaded, err := database.GetPendingMetadata(ctx, oid)
		if err != nil {
			t.Fatal(err)
		}
		if owned, err := database.ConsumePendingMetadata(ctx, *loaded); err != nil {
			t.Fatal(err)
		} else if !owned {
			t.Fatal("unchanged pending metadata was not reported as consumed")
		}
		if _, err := database.GetPendingMetadata(ctx, oid); !errors.Is(err, errorapi.ErrNotFound) {
			t.Fatalf("GetPendingMetadata() error = %v, want not found", err)
		}
	})

	t.Run("replaced with equal timestamps", func(t *testing.T) {
		oid := strings.Repeat("e", 64)
		oldEntry := postgresPendingMetadata(oid, "old", now)
		if err := database.SavePendingMetadata(ctx, []transferlfs.PendingMetadata{oldEntry}); err != nil {
			t.Fatal(err)
		}
		loaded, err := database.GetPendingMetadata(ctx, oid)
		if err != nil {
			t.Fatal(err)
		}
		replacement := postgresPendingMetadata(oid, "replacement", now)
		if err := database.SavePendingMetadata(ctx, []transferlfs.PendingMetadata{replacement}); err != nil {
			t.Fatal(err)
		}
		if owned, err := database.ConsumePendingMetadata(ctx, *loaded); err != nil {
			t.Fatal(err)
		} else if owned {
			t.Fatal("replaced pending metadata was reported as consumed")
		}
		remaining, err := database.GetPendingMetadata(ctx, oid)
		if err != nil {
			t.Fatalf("replacement GetPendingMetadata() error = %v", err)
		}
		if remaining.Candidate.Name == nil || *remaining.Candidate.Name != "replacement" {
			t.Fatalf("remaining candidate name = %v, want replacement", remaining.Candidate.Name)
		}
	})

	t.Run("jsonb formatting", func(t *testing.T) {
		oid := strings.Repeat("f", 64)
		expires := now.Add(time.Hour)
		raw := `{ "checksums" : [ { "type" : "sha256", "checksum" : "` + oid + `" } ], "name" : "legacy" }`
		if _, err := database.DB().ExecContext(ctx, `
			INSERT INTO lfs_pending_metadata (oid, candidate_json, created_time, expires_time)
			VALUES ($1, $2::jsonb, $3, $4)
			ON CONFLICT (oid) DO UPDATE SET candidate_json = EXCLUDED.candidate_json, created_time = EXCLUDED.created_time, expires_time = EXCLUDED.expires_time
		`, oid, raw, now, expires); err != nil {
			t.Fatal(err)
		}
		loaded, err := database.GetPendingMetadata(ctx, oid)
		if err != nil {
			t.Fatal(err)
		}
		if owned, err := database.ConsumePendingMetadata(ctx, *loaded); err != nil {
			t.Fatal(err)
		} else if !owned {
			t.Fatal("unchanged pending metadata was not reported as consumed")
		}
		if _, err := database.GetPendingMetadata(ctx, oid); !errors.Is(err, errorapi.ErrNotFound) {
			t.Fatalf("GetPendingMetadata() error = %v, want not found", err)
		}
	})
}

func postgresPendingMetadata(oid, name string, createdAt time.Time) transferlfs.PendingMetadata {
	typeName, location := "s3", "s3://bucket/"+oid
	return transferlfs.PendingMetadata{
		OID: oid,
		Candidate: lfsapi.DrsObjectCandidate{
			Name:          &name,
			Checksums:     &[]lfsapi.Checksum{{Type: "sha256", Checksum: oid}},
			AccessMethods: &[]lfsapi.AccessMethod{{Type: &typeName, AccessUrl: &lfsapi.AccessMethodAccessUrl{Url: &location}}},
		},
		CreatedAt: createdAt,
		ExpiresAt: createdAt.Add(time.Hour),
	}
}
