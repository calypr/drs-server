package transfers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/calypr/syfon/apigen/server/internalapi"
	"github.com/calypr/syfon/internal/objects"
	"github.com/calypr/syfon/internal/storage"
)

type captureURLManager struct {
	internalDRSStorageFake
	lastOptions storage.AccessOptions
}

func stringPtr(s string) *string { return &s }

func (m *captureURLManager) Access(ctx context.Context, request storage.AccessRequest) (storage.Access, error) {
	m.lastOptions = request.Options
	return m.internalDRSStorageFake.Access(ctx, request)
}

func TestHandleInternalDownload(t *testing.T) {
	mockDB := &transferHTTPFixture{
		Objects: map[string]*objects.Record{
			"test-file-id": {
				Id:   "test-file-id",
				Name: stringPtr("sha/LP6008050-DNA_B01__pv.2.0o__rg.grch38__alleleFrequencies_chr17.txt"),
				AccessMethods: &[]objects.AccessMethod{{
					Type: "s3",
					AccessUrl: &objects.AccessURL{

						Url: "s3://bucket/key"},
				}},
			},
		},
	}
	req := httptest.NewRequest(http.MethodGet, "/data/download/test-file-id", nil)
	um := &captureURLManager{}
	om := newInternalDRSObjectManager(mockDB, um)
	rr := doInternalDRSTestRequest(req, om)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	if got := rr.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("expected download response to disable caching, got %q", got)
	}
	var resp internalapi.InternalSignedURL
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stringValue(resp.Url), "signed=true") {
		t.Fatalf("expected signed url, got %v", stringValue(resp.Url))
	}
	if got, want := um.lastOptions.DownloadFilename, "LP6008050-DNA_B01__pv.2.0o__rg.grch38__alleleFrequencies_chr17.txt"; got != want {
		t.Fatalf("unexpected download filename override: got %q want %q", got, want)
	}
	if len(mockDB.TransferEvents) != 1 {
		t.Fatalf("expected one event, got %+v", mockDB.TransferEvents)
	}
}

func TestHandleInternalDownloadPart(t *testing.T) {
	mockDB := &transferHTTPFixture{
		Objects: map[string]*objects.Record{
			"test-file-id": {
				Id: "test-file-id",
				AccessMethods: &[]objects.AccessMethod{{
					Type: "s3",
					AccessUrl: &objects.AccessURL{

						Url: "s3://bucket/key"},
				}},
			},
		},
	}
	om := newInternalDRSObjectManager(mockDB, &internalDRSStorageFake{})

	t.Run("success", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/data/download/test-file-id/part?start=0&end=1024", nil)
		rr := doInternalDRSTestRequest(req, om)
		if rr.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", rr.Code)
		}
		if got := rr.Header().Get("Cache-Control"); got != "no-store" {
			t.Fatalf("expected ranged download response to disable caching, got %q", got)
		}
	})
	t.Run("missing parameters", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/data/download/test-file-id/part?start=0", nil)
		rr := doInternalDRSTestRequest(req, om)
		if rr.Code != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d", rr.Code)
		}
	})
	t.Run("invalid range", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/data/download/test-file-id/part?start=100&end=50", nil)
		rr := doInternalDRSTestRequest(req, om)
		if rr.Code != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d", rr.Code)
		}
	})
}

func TestHandleInternalDownload_ResolvesByChecksum(t *testing.T) {
	const did = "did-123"
	const oid = "sha256-abc"
	mockDB := &transferHTTPFixture{
		Objects: map[string]*objects.Record{
			did: {
				Id:        did,
				Checksums: []objects.Checksum{{Type: "sha256", Checksum: oid}},
				AccessMethods: &[]objects.AccessMethod{{
					Type: "s3",
					AccessUrl: &objects.AccessURL{

						Url: "s3://bucket/cbds/end_to_end_test/" + did + "/" + oid},
				}},
			},
		},
	}
	req := httptest.NewRequest(http.MethodGet, "/data/download/"+oid, nil)
	rr := doInternalDRSTestRequest(req, newInternalDRSObjectManager(mockDB, &internalDRSStorageFake{}))
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rr.Code, rr.Body.String())
	}
}

func TestHandleInternalDownload_ResolvesByUUID(t *testing.T) {
	const did = "2eb7a53c-1309-4be6-b6aa-8ed9249e23a9"
	mockDB := &transferHTTPFixture{
		Objects: map[string]*objects.Record{
			did: {
				Id: did,
				AccessMethods: &[]objects.AccessMethod{{
					Type: "s3",
					AccessUrl: &objects.AccessURL{

						Url: "s3://bucket/cbds/end_to_end_test/" + did},
				}},
			},
		},
	}
	req := httptest.NewRequest(http.MethodGet, "/data/download/"+did, nil)
	rr := doInternalDRSTestRequest(req, newInternalDRSObjectManager(mockDB, &internalDRSStorageFake{}))
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rr.Code, rr.Body.String())
	}
}

func TestHandleInternalDownload_MultiCloud(t *testing.T) {
	mockDB := &transferHTTPFixture{
		Objects: map[string]*objects.Record{
			"gcs-file": {Id: "gcs-file", AccessMethods: &[]objects.AccessMethod{{Type: "gs", AccessUrl: &objects.AccessURL{

				Url: "gs://gcs-bucket/obj"}}}},
			"azure-file": {Id: "azure-file", AccessMethods: &[]objects.AccessMethod{{Type: "azblob", AccessUrl: &objects.AccessURL{

				Url: "azblob://azure-bucket/obj"}}}},
		},
	}
	for _, id := range []string{"gcs-file", "azure-file"} {
		req := httptest.NewRequest(http.MethodGet, "/data/download/"+id, nil)
		rr := doInternalDRSTestRequest(req, newInternalDRSObjectManager(mockDB, &internalDRSStorageFake{}))
		if rr.Code != http.StatusOK {
			t.Fatalf("expected 200 for %s, got %d", id, rr.Code)
		}
	}
}

func TestHandleInternalDownload_Gen3Auth(t *testing.T) {
	mockDB := &transferHTTPFixture{
		Objects: map[string]*objects.Record{
			"secure-id": {Id: "secure-id", AccessMethods: &[]objects.AccessMethod{{Type: "s3", AccessUrl: &objects.AccessURL{

				Url: "s3://bucket/key"}}}},
		},
		ObjectAuthz: map[string]map[string][]string{"secure-id": {"p": {"q"}}},
	}
	om := newInternalDRSObjectManager(mockDB, &internalDRSStorageFake{})
	req401 := httptest.NewRequest(http.MethodGet, "/data/download/secure-id", nil)
	req401 = req401.WithContext(dataTestAuthContext(req401.Context(), "gen3", false, nil))
	rr401 := doInternalDRSTestRequest(req401, om)
	if rr401.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rr401.Code)
	}
}

func TestHandleInternalDownload_AuthzParity(t *testing.T) {
	for _, mode := range []string{"gen3", "local-authz"} {
		t.Run(mode, func(t *testing.T) {
			mockDB := &transferHTTPFixture{
				Objects: map[string]*objects.Record{
					"secure-id": {Id: "secure-id", AccessMethods: &[]objects.AccessMethod{{Type: "s3", AccessUrl: &objects.AccessURL{

						Url: "s3://bucket/key"}}}},
				},
				ObjectAuthz: map[string]map[string][]string{"secure-id": {"p": {"q"}}},
			}
			om := newInternalDRSObjectManager(mockDB, &internalDRSStorageFake{})
			req200 := httptest.NewRequest(http.MethodGet, "/data/download/secure-id", nil)
			req200 = withTestAuthzContext(req200, mode, map[string]map[string]bool{"/programs/p/projects/q": {"read": true}})
			rr200 := doInternalDRSTestRequest(req200, om)
			if rr200.Code != http.StatusOK {
				t.Fatalf("expected 200, got %d", rr200.Code)
			}
		})
	}
}
