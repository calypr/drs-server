package sqlite

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/calypr/syfon/internal/objects"
	transferlfs "github.com/calypr/syfon/internal/transfers/lfs"
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

	got, err := db.GetPendingMetadata(context.Background(), oid)
	if err != nil {
		t.Fatalf("GetPendingLFSMeta failed: %v", err)
	}
	if got.OID != oid || !got.CreatedAt.Equal(now) || !got.ExpiresAt.Equal(now.Add(time.Hour)) {
		t.Fatalf("unexpected pending row envelope: %+v", got)
	}
	if got.Candidate.Name == nil || *got.Candidate.Name != "legacy-lfs.bin" {
		t.Fatalf("legacy candidate name = %#v, want legacy-lfs.bin", got.Candidate.Name)
	}
	if got.Candidate.Checksums == nil || len(*got.Candidate.Checksums) != 1 || (*got.Candidate.Checksums)[0].Checksum != oid {
		t.Fatalf("legacy candidate checksums = %+v, want canonical checksum", got.Candidate.Checksums)
	}
	if got.Candidate.AccessMethods == nil || len(*got.Candidate.AccessMethods) != 1 {
		t.Fatalf("legacy candidate access methods = %+v, want one method", got.Candidate.AccessMethods)
	}
}

func TestPendingMetaCandidateJSONPreservesLegacyLFSShape(t *testing.T) {
	db, err := NewSqliteDB(":memory:")
	if err != nil {
		t.Fatalf("NewSqliteDB failed: %v", err)
	}

	const oid = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	size := int64(17)
	id := "explicit-lfs-id"
	typ := "s3"
	region := "legacy-cloud"
	url := "s3://bucket/legacy-lfs.bin"
	candidate := objects.Candidate{
		Aliases: &[]string{"id:" + id},
		Name:    stringPtr("legacy-lfs.bin"),
		Size:    &size,
		Checksums: &[]objects.Checksum{{
			Type: "sha256", Checksum: oid,
		}},
		AccessMethods: &[]objects.AccessMethod{{
			AccessId:  stringPtr("s3"),
			Type:      typ,
			Cloud:     &region,
			AccessUrl: &objects.AccessURL{Url: url},
		}},
	}
	now := time.Now().UTC().Truncate(time.Second)
	if err := db.SavePendingMetadata(context.Background(), []transferlfs.PendingMetadata{{OID: oid, Candidate: candidate, CreatedAt: now, ExpiresAt: now.Add(time.Hour)}}); err != nil {
		t.Fatalf("SavePendingLFSMeta failed: %v", err)
	}

	var raw string
	if err := db.db.QueryRow(`SELECT candidate_json FROM lfs_pending_metadata WHERE oid = ?`, oid).Scan(&raw); err != nil {
		t.Fatalf("read candidate_json: %v", err)
	}
	var payload map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		t.Fatalf("decode candidate_json: %v", err)
	}
	if _, ok := payload["id"]; ok {
		t.Fatalf("candidate_json gained a top-level id: %s", raw)
	}
	var aliases []string
	if err := json.Unmarshal(payload["aliases"], &aliases); err != nil || len(aliases) != 1 || aliases[0] != "id:"+id {
		t.Fatalf("legacy id alias = %s", payload["aliases"])
	}
	var methods []map[string]json.RawMessage
	if err := json.Unmarshal(payload["access_methods"], &methods); err != nil || len(methods) != 1 {
		t.Fatalf("legacy access methods = %s", payload["access_methods"])
	}
	method := methods[0]
	if string(method["cloud"]) != `"`+region+`"` || method["region"] != nil || method["authorizations"] != nil {
		t.Fatalf("legacy region/cloud fields changed: %s", payload["access_methods"])
	}
	var accessURL map[string]json.RawMessage
	if err := json.Unmarshal(method["access_url"], &accessURL); err != nil || string(accessURL["url"]) != `"`+url+`"` {
		t.Fatalf("legacy access URL fields changed: %s", method["access_url"])
	}
}

func TestPendingMetaCandidateJSONPreservesExplicitZeroSize(t *testing.T) {
	db, err := NewSqliteDB(":memory:")
	if err != nil {
		t.Fatalf("NewSqliteDB failed: %v", err)
	}

	const oid = "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
	size := int64(0)
	candidate := objects.Candidate{Size: &size}
	now := time.Now().UTC().Truncate(time.Second)
	if err := db.SavePendingMetadata(context.Background(), []transferlfs.PendingMetadata{{OID: oid, Candidate: candidate, CreatedAt: now, ExpiresAt: now.Add(time.Hour)}}); err != nil {
		t.Fatalf("SavePendingLFSMeta failed: %v", err)
	}

	var raw string
	if err := db.db.QueryRow(`SELECT candidate_json FROM lfs_pending_metadata WHERE oid = ?`, oid).Scan(&raw); err != nil {
		t.Fatalf("read candidate_json: %v", err)
	}
	var payload map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		t.Fatalf("decode candidate_json: %v", err)
	}
	if sizeJSON, ok := payload["size"]; !ok || string(sizeJSON) != "0" {
		t.Fatalf("candidate_json size = %s, want explicit zero", sizeJSON)
	}
}

func stringPtr(value string) *string { return &value }
