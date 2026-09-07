package sqlite

import (
	"context"
	"testing"
	"time"
)

func TestPendingMetaLegacyJSON(t *testing.T) {
	db, err := NewSqliteDB(":memory:")
	if err != nil {
		t.Fatalf("NewSqliteDB failed: %v", err)
	}

	const oid = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	now := time.Now().UTC().Truncate(time.Second)
	legacyJSON := `{"id":"legacy-id","did":"legacy-did","hashes":{"sha256":"ignored-legacy-alias"},"checksums":[{"type":"sha256","checksum":"` + oid + `"}],"name":"legacy-lfs.bin","size":17,"access_methods":[{"type":"s3","access_url":{"url":"s3://bucket/legacy-lfs.bin"}}]}`
	if _, err := db.db.Exec(`INSERT INTO lfs_pending_metadata (oid, candidate_json, created_time, expires_time) VALUES (?, ?, ?, ?)`, oid, legacyJSON, now, now.Add(time.Hour)); err != nil {
		t.Fatalf("insert legacy pending row: %v", err)
	}

	got, err := db.GetPendingLFSMeta(context.Background(), oid)
	if err != nil {
		t.Fatalf("GetPendingLFSMeta failed: %v", err)
	}
	if got.OID != oid || !got.CreatedAt.Equal(now) || !got.ExpiresAt.Equal(now.Add(time.Hour)) {
		t.Fatalf("unexpected pending row envelope: %+v", got)
	}
	if got.Candidate.Name == nil || *got.Candidate.Name != "legacy-lfs.bin" {
		t.Fatalf("legacy candidate name = %#v, want legacy-lfs.bin", got.Candidate.Name)
	}
	if len(got.Candidate.Checksums) != 1 || got.Candidate.Checksums[0].Checksum != oid {
		t.Fatalf("legacy candidate checksums = %+v, want canonical checksum", got.Candidate.Checksums)
	}
	if got.Candidate.AccessMethods == nil || len(*got.Candidate.AccessMethods) != 1 {
		t.Fatalf("legacy candidate access methods = %+v, want one method", got.Candidate.AccessMethods)
	}
}
