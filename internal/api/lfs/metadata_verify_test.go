package lfs

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestLFSMetadataThenVerifyRegistersObject(t *testing.T) {
	router, db := newLFSRouter()
	oid := "dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd"

	meta := map[string]any{
		"candidates": []map[string]any{
			{
				"name": "forge-file",
				"size": 123,
				"checksums": []map[string]any{
					{"type": "sha256", "checksum": oid},
				},
				"access_methods": []map[string]any{
					{
						"type": "s3",
						"access_url": map[string]any{
							"url": "s3://test-bucket-1/path/" + oid,
						},
					},
				},
			},
		},
	}
	metaRaw, _ := json.Marshal(meta)
	metaReq := httptest.NewRequest(http.MethodPost, "/info/lfs/objects/metadata", bytes.NewReader(metaRaw))
	metaReq.Header.Set("Content-Type", "application/vnd.git-lfs+json")
	metaRR := httptest.NewRecorder()
	router.ServeHTTP(metaRR, metaReq)
	if metaRR.Code != http.StatusOK {
		t.Fatalf("expected 200 from metadata, got %d body=%s", metaRR.Code, metaRR.Body.String())
	}

	verifyBody := map[string]any{"oid": oid, "size": 123}
	verifyRaw, _ := json.Marshal(verifyBody)
	verifyReq := httptest.NewRequest(http.MethodPost, "/info/lfs/verify", bytes.NewReader(verifyRaw))
	verifyReq.Header.Set("Accept", "application/vnd.git-lfs+json")
	verifyReq.Header.Set("Content-Type", "application/vnd.git-lfs+json")
	verifyRR := httptest.NewRecorder()
	router.ServeHTTP(verifyRR, verifyReq)
	if verifyRR.Code != http.StatusOK {
		t.Fatalf("expected 200 from verify, got %d body=%s", verifyRR.Code, verifyRR.Body.String())
	}
	matches, err := db.GetObjectsByChecksum(context.Background(), oid)
	if err != nil {
		t.Fatalf("expected checksum lookup to succeed, got error: %v", err)
	}
	if len(matches) == 0 {
		t.Fatalf("expected object with checksum %s to be registered on verify", oid)
	}
}

func TestLFSVerifyNotFound(t *testing.T) {
	router, _ := newLFSRouter()
	oid := "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
	body := map[string]any{"oid": oid, "size": 10}
	raw, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/info/lfs/verify", bytes.NewReader(raw))
	req.Header.Set("Accept", "application/vnd.git-lfs+json")
	req.Header.Set("Content-Type", "application/vnd.git-lfs+json")
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d body=%s", rr.Code, rr.Body.String())
	}
}
