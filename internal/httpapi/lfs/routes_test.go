package lfs

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/calypr/syfon/apigen/server/lfsapi"
	"github.com/calypr/syfon/internal/buckets"
	"github.com/calypr/syfon/internal/objects"
	"github.com/calypr/syfon/internal/storage"
	"github.com/calypr/syfon/internal/transfers"
	"github.com/calypr/syfon/internal/usage"
)

func TestLFSBatchDownloadUsesTransferAndUsagePorts(t *testing.T) {
	oid := strings.Repeat("a", 64)
	ports := newLFSTestPorts(
		map[string]*objects.Record{
			oid: {
				Id:        objects.RecordID(oid),
				Size:      10,
				Checksums: []objects.Checksum{{Type: "sha256", Checksum: oid}},
				AccessMethods: &[]objects.AccessMethod{{
					Type:      "s3",
					AccessUrl: &objects.AccessURL{Url: "s3://bucket/" + oid},
				}},
			},
		},
		map[string]buckets.Credential{"bucket": {Bucket: "bucket"}},
	)
	storageFake := &lfsTestStorage{}
	router := newLFSTestRouter(ports, storageFake, DefaultOptions())
	body, _ := json.Marshal(map[string]any{
		"operation": "download",
		"objects":   []map[string]any{{"oid": oid, "size": 10}},
	})
	request := httptest.NewRequest(http.MethodPost, "/info/lfs/objects/batch", bytes.NewReader(body))
	request.Header.Set("Accept", "application/vnd.git-lfs+json")
	request.Header.Set("Content-Type", "application/vnd.git-lfs+json")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("batch status = %d body=%s", response.Code, response.Body.String())
	}
	var payload lfsapi.BatchResponse
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode batch response: %v", err)
	}
	if len(payload.Objects) != 1 || payload.Objects[0].Actions == nil || payload.Objects[0].Actions.Download == nil {
		t.Fatalf("download actions = %+v", payload.Objects)
	}
	if len(ports.fileCounters.downloads) != 1 || ports.fileCounters.downloads[0] != oid {
		t.Fatalf("download counters = %v", ports.fileCounters.downloads)
	}
	if len(ports.events.events) != 1 || ports.events.events[0].EventType != usage.TransferEventAccessIssued {
		t.Fatalf("transfer events = %+v", ports.events.events)
	}
}

func TestLFSMetadataVerifyPreservesPendingPopBeforeRegister(t *testing.T) {
	oid := strings.Repeat("b", 64)
	ports := newLFSTestPorts(map[string]*objects.Record{}, map[string]buckets.Credential{"bucket": {Bucket: "bucket"}})
	router := newLFSTestRouter(ports, &lfsTestStorage{}, DefaultOptions())
	metadata, _ := json.Marshal(map[string]any{"candidates": []map[string]any{{
		"name": "object.bin", "size": 12,
		"checksums":      []map[string]any{{"type": "sha256", "checksum": oid}},
		"access_methods": []map[string]any{{"type": "s3", "access_url": map[string]any{"url": "s3://bucket/" + oid}}},
	}}})
	request := httptest.NewRequest(http.MethodPost, "/info/lfs/objects/metadata", bytes.NewReader(metadata))
	request.Header.Set("Content-Type", "application/vnd.git-lfs+json")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("metadata status = %d body=%s", response.Code, response.Body.String())
	}
	entry, ok := ports.pending.entries[oid]
	if !ok || entry.CreatedAt.IsZero() || entry.ExpiresAt.Sub(entry.CreatedAt) != transfers.PendingMetadataTTL {
		t.Fatalf("pending metadata timestamps = %+v", entry)
	}
	verify, _ := json.Marshal(map[string]any{"oid": oid, "size": 12})
	request = httptest.NewRequest(http.MethodPost, "/info/lfs/verify", bytes.NewReader(verify))
	request.Header.Set("Accept", "application/vnd.git-lfs+json")
	request.Header.Set("Content-Type", "application/vnd.git-lfs+json")
	response = httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("verify status = %d body=%s", response.Code, response.Body.String())
	}
	if _, ok := ports.pending.entries[oid]; ok {
		t.Fatal("pending metadata was not consumed")
	}
}

func TestLFSUploadProxyPreservesOpaqueMultipartAndPartOrder(t *testing.T) {
	oid := strings.Repeat("c", 64)
	ports := newLFSTestPorts(map[string]*objects.Record{}, map[string]buckets.Credential{"bucket": {Bucket: "bucket"}})
	storageFake := &lfsTestStorage{}
	deps := newLFSTestDependencies(ports, storageFake)
	deps.PartUploader = func(_ context.Context, _ string, content []byte) (string, error) {
		if string(content) != "payload" {
			t.Fatalf("multipart content = %q", content)
		}
		return "etag", nil
	}
	server := NewLFSServer(deps, DefaultOptions())
	response, err := server.LfsUploadProxy(context.Background(), lfsapi.LfsUploadProxyRequestObject{
		Oid:  oid,
		Body: bytes.NewReader([]byte("payload")),
	})
	if err != nil {
		t.Fatalf("upload proxy error: %v", err)
	}
	if _, ok := response.(lfsapi.LfsUploadProxy200Response); !ok {
		t.Fatalf("upload proxy response = %T, want 200", response)
	}
	if storageFake.initTarget != (storage.ObjectTarget{Bucket: "bucket", Key: oid}) {
		t.Fatalf("multipart init target = %+v", storageFake.initTarget)
	}
	if storageFake.partRequest.UploadID != "opaque-upload-id" || storageFake.partRequest.PartNumber != 1 {
		t.Fatalf("multipart part request = %+v", storageFake.partRequest)
	}
	if storageFake.complete.UploadID != "opaque-upload-id" || len(storageFake.complete.Parts) != 1 || storageFake.complete.Parts[0].ETag != "etag" {
		t.Fatalf("multipart completion = %+v", storageFake.complete)
	}
}

type lfsTestScopeReader struct {
	scopes map[string]buckets.Scope
}

func (r lfsTestScopeReader) LookupBucketScope(_ context.Context, organization, project string) (buckets.Scope, bool, error) {
	scope, ok := r.scopes[organization+"|"+project]
	return scope, ok, nil
}

func TestLFSUploadProxyUsesCanonicalOIDForScopedTargets(t *testing.T) {
	oid := strings.Repeat("d", 64)
	newTransferService := func(ports *lfsTestServicePorts, storageFake *lfsTestStorage) *transfers.Service {
		return transfers.NewService(transfers.Dependencies{
			Access:      storageFake,
			Multipart:   storageFake,
			Scopes:      lfsTestScopeReader{scopes: map[string]buckets.Scope{"org|project": {Organization: "org", ProjectID: "project", Bucket: "physical", PathPrefix: "project-prefix"}}},
			Credentials: ports.credentials,
			Pending:     ports.pending,
			Events:      ports.events,
		})
	}

	tests := []struct {
		name     string
		populate func(*lfsTestServicePorts)
	}{
		{
			name: "existing object",
			populate: func(ports *lfsTestServicePorts) {
				resources := []string{"/programs/org/projects/project"}
				methods := []objects.AccessMethod{{Type: "s3", AccessUrl: &objects.AccessURL{Url: "s3://legacy/stale-key"}}}
				ports.objectReader.records["record-existing"] = &objects.Record{
					Id:               "record-existing",
					Checksums:        []objects.Checksum{{Type: "sha256", Checksum: oid}},
					AccessMethods:    &methods,
					ControlledAccess: &resources,
				}
			},
		},
		{
			name: "pending metadata",
			populate: func(ports *lfsTestServicePorts) {
				resources := []string{"/programs/org/projects/project"}
				methods := []objects.AccessMethod{{Type: "s3", AccessUrl: &objects.AccessURL{Url: "s3://legacy/stale-key"}}}
				ports.pending.entries[oid] = transfers.PendingMetadata{
					OID: oid,
					Candidate: objects.Candidate{
						Checksums:        &[]objects.Checksum{{Type: "sha256", Checksum: oid}},
						AccessMethods:    &methods,
						ControlledAccess: &resources,
					},
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ports := newLFSTestPorts(map[string]*objects.Record{}, map[string]buckets.Credential{"physical": {Bucket: "physical"}})
			tt.populate(ports)
			storageFake := &lfsTestStorage{}
			deps := newLFSTestDependencies(ports, storageFake)
			deps.TransferService = newTransferService(ports, storageFake)
			deps.PartUploader = func(context.Context, string, []byte) (string, error) { return "etag", nil }
			server := NewLFSServer(deps, DefaultOptions())

			response, err := server.LfsUploadProxy(context.Background(), lfsapi.LfsUploadProxyRequestObject{
				Oid:  oid,
				Body: strings.NewReader("payload"),
			})
			if err != nil {
				t.Fatalf("upload proxy error: %v", err)
			}
			if _, ok := response.(lfsapi.LfsUploadProxy200Response); !ok {
				t.Fatalf("upload proxy response = %T (%+v)", response, response)
			}
			want := storage.ObjectTarget{Bucket: "physical", Key: "project-prefix/" + oid}
			if storageFake.initTarget != want {
				t.Fatalf("multipart init target = %+v, want %+v", storageFake.initTarget, want)
			}
		})
	}
}
