package internaldrs

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/calypr/syfon/apigen/server/drs"
	"github.com/calypr/syfon/apigen/server/internalapi"
	"github.com/calypr/syfon/internal/api/routeutil"
	"github.com/calypr/syfon/internal/common"
	"github.com/calypr/syfon/internal/core"
	"github.com/calypr/syfon/internal/testutils"
	"github.com/calypr/syfon/internal/urlmanager"
)

type captureURLManager struct {
	testutils.MockUrlManager
	lastOptions urlmanager.SignOptions
}

func stringPtr(s string) *string { return &s }

func (m *captureURLManager) SignURL(ctx context.Context, accessId string, url string, opts urlmanager.SignOptions) (string, error) {
	m.lastOptions = opts
	return m.MockUrlManager.SignURL(ctx, accessId, url, opts)
}

func TestHandleInternalDownload(t *testing.T) {
	mockDB := &testutils.MockDatabase{
		Objects: map[string]*drs.DrsObject{
			"test-file-id": {
				Id:   "test-file-id",
				Name: stringPtr("sha/LP6008050-DNA_B01__pv.2.0o__rg.grch38__alleleFrequencies_chr17.txt"),
				AccessMethods: &[]drs.AccessMethod{{
					Type: drs.AccessMethodTypeS3,
					AccessUrl: &struct {
						Headers *[]string `json:"headers,omitempty"`
						Url     string    `json:"url"`
					}{Url: "s3://bucket/key"},
				}},
			},
		},
	}
	req := routeutil.WithPathParams(httptest.NewRequest(http.MethodGet, "/data/download/test-file-id", nil), map[string]string{"file_id": "test-file-id"})
	um := &captureURLManager{}
	om := core.NewObjectManager(mockDB, um)
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
	if !strings.Contains(common.StringVal(resp.Url), "signed=true") {
		t.Fatalf("expected signed url, got %v", common.StringVal(resp.Url))
	}
	if got, want := um.lastOptions.DownloadFilename, "LP6008050-DNA_B01__pv.2.0o__rg.grch38__alleleFrequencies_chr17.txt"; got != want {
		t.Fatalf("unexpected download filename override: got %q want %q", got, want)
	}
	if len(mockDB.TransferEvents) != 1 {
		t.Fatalf("expected one event, got %+v", mockDB.TransferEvents)
	}
}

func TestHandleInternalDownloadPart(t *testing.T) {
	mockDB := &testutils.MockDatabase{
		Objects: map[string]*drs.DrsObject{
			"test-file-id": {
				Id: "test-file-id",
				AccessMethods: &[]drs.AccessMethod{{
					Type: drs.AccessMethodTypeS3,
					AccessUrl: &struct {
						Headers *[]string `json:"headers,omitempty"`
						Url     string    `json:"url"`
					}{Url: "s3://bucket/key"},
				}},
			},
		},
	}
	om := core.NewObjectManager(mockDB, &testutils.MockUrlManager{})

	t.Run("success", func(t *testing.T) {
		req := routeutil.WithPathParams(httptest.NewRequest(http.MethodGet, "/data/download/test-file-id/part?start=0&end=1024", nil), map[string]string{"file_id": "test-file-id"})
		rr := doInternalDRSTestRequest(req, om)
		if rr.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", rr.Code)
		}
		if got := rr.Header().Get("Cache-Control"); got != "no-store" {
			t.Fatalf("expected ranged download response to disable caching, got %q", got)
		}
	})
	t.Run("missing parameters", func(t *testing.T) {
		req := routeutil.WithPathParams(httptest.NewRequest(http.MethodGet, "/data/download/test-file-id/part?start=0", nil), map[string]string{"file_id": "test-file-id"})
		rr := doInternalDRSTestRequest(req, om)
		if rr.Code != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d", rr.Code)
		}
	})
	t.Run("invalid range", func(t *testing.T) {
		req := routeutil.WithPathParams(httptest.NewRequest(http.MethodGet, "/data/download/test-file-id/part?start=100&end=50", nil), map[string]string{"file_id": "test-file-id"})
		rr := doInternalDRSTestRequest(req, om)
		if rr.Code != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d", rr.Code)
		}
	})
}

func TestHandleInternalDownload_ResolvesByChecksum(t *testing.T) {
	const did = "did-123"
	const oid = "sha256-abc"
	mockDB := &testutils.MockDatabase{
		Objects: map[string]*drs.DrsObject{
			did: {
				Id:        did,
				Checksums: []drs.Checksum{{Type: "sha256", Checksum: oid}},
				AccessMethods: &[]drs.AccessMethod{{
					Type: drs.AccessMethodTypeS3,
					AccessUrl: &struct {
						Headers *[]string `json:"headers,omitempty"`
						Url     string    `json:"url"`
					}{Url: "s3://bucket/cbds/end_to_end_test/" + did + "/" + oid},
				}},
			},
		},
	}
	req := routeutil.WithPathParams(httptest.NewRequest(http.MethodGet, "/data/download/"+oid, nil), map[string]string{"file_id": oid})
	rr := doInternalDRSTestRequest(req, core.NewObjectManager(mockDB, &testutils.MockUrlManager{}))
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rr.Code, rr.Body.String())
	}
}

func TestHandleInternalDownload_ResolvesByUUID(t *testing.T) {
	const did = "2eb7a53c-1309-4be6-b6aa-8ed9249e23a9"
	mockDB := &testutils.MockDatabase{
		Objects: map[string]*drs.DrsObject{
			did: {
				Id: did,
				AccessMethods: &[]drs.AccessMethod{{
					Type: drs.AccessMethodTypeS3,
					AccessUrl: &struct {
						Headers *[]string `json:"headers,omitempty"`
						Url     string    `json:"url"`
					}{Url: "s3://bucket/cbds/end_to_end_test/" + did},
				}},
			},
		},
	}
	req := routeutil.WithPathParams(httptest.NewRequest(http.MethodGet, "/data/download/"+did, nil), map[string]string{"file_id": did})
	rr := doInternalDRSTestRequest(req, core.NewObjectManager(mockDB, &testutils.MockUrlManager{}))
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rr.Code, rr.Body.String())
	}
}

func TestHandleInternalDownload_MultiCloud(t *testing.T) {
	mockDB := &testutils.MockDatabase{
		Objects: map[string]*drs.DrsObject{
			"gcs-file": {Id: "gcs-file", AccessMethods: &[]drs.AccessMethod{{Type: drs.AccessMethodTypeGs, AccessUrl: &struct {
				Headers *[]string `json:"headers,omitempty"`
				Url     string    `json:"url"`
			}{Url: "gs://gcs-bucket/obj"}}}},
			"azure-file": {Id: "azure-file", AccessMethods: &[]drs.AccessMethod{{Type: drs.AccessMethodType("azblob"), AccessUrl: &struct {
				Headers *[]string `json:"headers,omitempty"`
				Url     string    `json:"url"`
			}{Url: "azblob://azure-bucket/obj"}}}},
		},
	}
	for _, id := range []string{"gcs-file", "azure-file"} {
		req := routeutil.WithPathParams(httptest.NewRequest(http.MethodGet, "/data/download/"+id, nil), map[string]string{"file_id": id})
		rr := doInternalDRSTestRequest(req, core.NewObjectManager(mockDB, &testutils.MockUrlManager{}))
		if rr.Code != http.StatusOK {
			t.Fatalf("expected 200 for %s, got %d", id, rr.Code)
		}
	}
}

func TestHandleInternalDownload_Gen3Auth(t *testing.T) {
	mockDB := &testutils.MockDatabase{
		Objects: map[string]*drs.DrsObject{
			"secure-id": {Id: "secure-id", AccessMethods: &[]drs.AccessMethod{{Type: drs.AccessMethodTypeS3, AccessUrl: &struct {
				Headers *[]string `json:"headers,omitempty"`
				Url     string    `json:"url"`
			}{Url: "s3://bucket/key"}}}},
		},
		ObjectAuthz: map[string]map[string][]string{"secure-id": {"p": {"q"}}},
	}
	om := core.NewObjectManager(mockDB, &testutils.MockUrlManager{})
	req401 := routeutil.WithPathParams(httptest.NewRequest(http.MethodGet, "/data/download/secure-id", nil), map[string]string{"file_id": "secure-id"})
	req401 = req401.WithContext(dataTestAuthContext(req401.Context(), "gen3", false, nil))
	rr401 := doInternalDRSTestRequest(req401, om)
	if rr401.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rr401.Code)
	}
}

func TestHandleInternalDownload_AuthzParity(t *testing.T) {
	for _, mode := range []string{"gen3", "local-authz"} {
		t.Run(mode, func(t *testing.T) {
			mockDB := &testutils.MockDatabase{
				Objects: map[string]*drs.DrsObject{
					"secure-id": {Id: "secure-id", AccessMethods: &[]drs.AccessMethod{{Type: drs.AccessMethodTypeS3, AccessUrl: &struct {
						Headers *[]string `json:"headers,omitempty"`
						Url     string    `json:"url"`
					}{Url: "s3://bucket/key"}}}},
				},
				ObjectAuthz: map[string]map[string][]string{"secure-id": {"p": {"q"}}},
			}
			om := core.NewObjectManager(mockDB, &testutils.MockUrlManager{})
			req200 := routeutil.WithPathParams(httptest.NewRequest(http.MethodGet, "/data/download/secure-id", nil), map[string]string{"file_id": "secure-id"})
			req200 = withTestAuthzContext(req200, mode, map[string]map[string]bool{"/programs/p/projects/q": {"read": true}})
			rr200 := doInternalDRSTestRequest(req200, om)
			if rr200.Code != http.StatusOK {
				t.Fatalf("expected 200, got %d", rr200.Code)
			}
		})
	}
}
