package lfs

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/calypr/syfon/apigen/server/drs"
	"github.com/calypr/syfon/apigen/server/lfsapi"
	"github.com/calypr/syfon/internal/access"
	"github.com/calypr/syfon/internal/core"
	"github.com/calypr/syfon/internal/models"
	"github.com/calypr/syfon/internal/testutils"
	"github.com/gofiber/fiber/v3"
)

func TestLFSBatchDownloadFound(t *testing.T) {
	router, db := newLFSRouter()
	oid := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	db.Objects[oid] = &drs.DrsObject{
		Id:   oid,
		Size: 10,
		Checksums: []drs.Checksum{
			{Type: "sha256", Checksum: oid},
		},
		AccessMethods: &[]drs.AccessMethod{
			{Type: drs.AccessMethodTypeS3, AccessUrl: &struct {
				Headers *[]string `json:"headers,omitempty"`
				Url     string    `json:"url"`
			}{Url: "s3://bucket/" + oid}},
		},
	}

	body := map[string]any{
		"operation": "download",
		"objects":   []map[string]any{{"oid": oid, "size": 10}},
	}
	raw, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/info/lfs/objects/batch", bytes.NewReader(raw))
	req.Header.Set("Accept", "application/vnd.git-lfs+json")
	req.Header.Set("Content-Type", "application/vnd.git-lfs+json")
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rr.Code, rr.Body.String())
	}
	var resp lfsapi.BatchResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	if len(resp.Objects) != 1 || resp.Objects[0].Actions == nil || resp.Objects[0].Actions.Download == nil || resp.Objects[0].Actions.Download.Href == "" {
		t.Fatalf("expected download action, got %+v", resp)
	}
	if len(db.TransferEvents) != 1 {
		t.Fatalf("expected one LFS access-issued event, got %+v", db.TransferEvents)
	}
	ev := db.TransferEvents[0]
	if ev.EventType != models.TransferEventAccessIssued || ev.ObjectID != oid || ev.SHA256 != oid || ev.Provider != "s3" || ev.Bucket != "bucket" || ev.BytesRequested != 10 {
		t.Fatalf("unexpected LFS access-issued event: %+v", ev)
	}
	if ev.AccessGrantID == "" || ev.AccessGrantID == ev.EventID {
		t.Fatalf("expected stable grant id distinct from audit event id: %+v", ev)
	}
}

func TestLFSBatchUploadReturnsActionsWithoutPlaceholder(t *testing.T) {
	router, db := newLFSRouter()
	oid := "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"

	body := map[string]any{
		"operation": "upload",
		"objects":   []map[string]any{{"oid": oid, "size": 123}},
	}
	raw, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/info/lfs/objects/batch", bytes.NewReader(raw))
	req.Header.Set("Accept", "application/vnd.git-lfs+json")
	req.Header.Set("Content-Type", "application/vnd.git-lfs+json")
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rr.Code, rr.Body.String())
	}
	if _, ok := db.Objects[oid]; ok {
		t.Fatalf("did not expect object to be created during batch upload")
	}
	var resp lfsapi.BatchResponse
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)
	if len(resp.Objects) != 1 || resp.Objects[0].Actions == nil || resp.Objects[0].Actions.Upload == nil || resp.Objects[0].Actions.Upload.Href == "" {
		t.Fatalf("expected upload action in response, got %+v", resp)
	}
}

func TestLFSBatchRejectsBadAccept(t *testing.T) {
	router, _ := newLFSRouter()
	body := map[string]any{
		"operation": "download",
		"objects":   []map[string]any{{"oid": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "size": 10}},
	}
	raw, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/info/lfs/objects/batch", bytes.NewReader(raw))
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/vnd.git-lfs+json")
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	if rr.Code != http.StatusNotAcceptable {
		t.Fatalf("expected 406, got %d body=%s", rr.Code, rr.Body.String())
	}
}

func TestLFSBatchRejectsBadContentType(t *testing.T) {
	router, _ := newLFSRouter()
	body := map[string]any{
		"operation": "download",
		"objects":   []map[string]any{{"oid": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "size": 10}},
	}
	raw, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/info/lfs/objects/batch", bytes.NewReader(raw))
	req.Header.Set("Accept", "application/vnd.git-lfs+json")
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422, got %d body=%s", rr.Code, rr.Body.String())
	}
}

func TestLFSBatchGen3MissingAuthReturns401(t *testing.T) {
	oid := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	db := &testutils.MockDatabase{
		Objects: map[string]*drs.DrsObject{
			oid: &drs.DrsObject{
				Id: oid,
				AccessMethods: &[]drs.AccessMethod{
					{
						Type: drs.AccessMethodTypeS3,
						AccessUrl: &struct {
							Headers *[]string `json:"headers,omitempty"`
							Url     string    `json:"url"`
						}{Url: "s3://bucket/" + oid},
					},
				},
			},
		},
		ObjectAuthz: map[string]map[string][]string{
			oid: {"syfon": {"e2e"}},
		},
	}
	uM := &testutils.MockUrlManager{}
	app := fiber.New()
	app.Use(func(c fiber.Ctx) error {
		session := access.NewSession("gen3")
		session.AuthHeaderPresent = false
		session.AuthzEnforced = true
		ctx := access.WithSession(c.Context(), session)
		c.SetContext(ctx)
		return c.Next()
	})
	om := core.NewObjectManager(db, uM)
	RegisterLFSRoutes(app, om, DefaultOptions())
	router := &fiberTestRouter{app: app}
	body := map[string]any{
		"operation": "download",
		"objects":   []map[string]any{{"oid": oid, "size": 10}},
	}
	raw, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/info/lfs/objects/batch", bytes.NewReader(raw))
	req.Header.Set("Accept", "application/vnd.git-lfs+json")
	req.Header.Set("Content-Type", "application/vnd.git-lfs+json")
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rr.Code, rr.Body.String())
	}
	var resp lfsapi.BatchResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	if len(resp.Objects) != 1 || resp.Objects[0].Error == nil || resp.Objects[0].Error.Code != int32(http.StatusUnauthorized) {
		t.Fatalf("expected embedded unauthorized object error, got %+v", resp)
	}
}

func TestLFSBatchTooManyObjects413(t *testing.T) {
	ResetLFSLimitersForTest()
	opts := DefaultOptions()
	opts.MaxBatchObjects = 1
	router, _ := newLFSRouterWithOptions(opts)
	body := map[string]any{
		"operation": "download",
		"objects": []map[string]any{
			{"oid": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "size": 10},
			{"oid": "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", "size": 20},
		},
	}
	raw, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/info/lfs/objects/batch", bytes.NewReader(raw))
	req.Header.Set("Accept", "application/vnd.git-lfs+json")
	req.Header.Set("Content-Type", "application/vnd.git-lfs+json")
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	if rr.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("expected 413, got %d body=%s", rr.Code, rr.Body.String())
	}
}

func TestLFSBatchRateLimit429(t *testing.T) {
	ResetLFSLimitersForTest()
	opts := DefaultOptions()
	opts.RequestLimitPerMinute = 1
	router, _ := newLFSRouterWithOptions(opts)
	body := map[string]any{
		"operation": "download",
		"objects":   []map[string]any{{"oid": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "size": 10}},
	}
	raw, _ := json.Marshal(body)
	req1 := httptest.NewRequest(http.MethodPost, "/info/lfs/objects/batch", bytes.NewReader(raw))
	req1.RemoteAddr = "test-client:1"
	req1.Header.Set("Accept", "application/vnd.git-lfs+json")
	req1.Header.Set("Content-Type", "application/vnd.git-lfs+json")
	rr1 := httptest.NewRecorder()
	router.ServeHTTP(rr1, req1)
	if rr1.Code != http.StatusOK {
		t.Fatalf("expected first request 200, got %d body=%s", rr1.Code, rr1.Body.String())
	}

	req2 := httptest.NewRequest(http.MethodPost, "/info/lfs/objects/batch", bytes.NewReader(raw))
	req2.RemoteAddr = "test-client:1"
	req2.Header.Set("Accept", "application/vnd.git-lfs+json")
	req2.Header.Set("Content-Type", "application/vnd.git-lfs+json")
	rr2 := httptest.NewRecorder()
	router.ServeHTTP(rr2, req2)
	if rr2.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429, got %d body=%s", rr2.Code, rr2.Body.String())
	}
}

func TestLFSBatchBandwidthLimit509(t *testing.T) {
	ResetLFSLimitersForTest()
	opts := DefaultOptions()
	opts.BandwidthLimitBytesPerMinute = 5
	router, _ := newLFSRouterWithOptions(opts)
	body := map[string]any{
		"operation": "download",
		"objects":   []map[string]any{{"oid": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "size": 10}},
	}
	raw, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/info/lfs/objects/batch", bytes.NewReader(raw))
	req.RemoteAddr = "test-client:2"
	req.Header.Set("Accept", "application/vnd.git-lfs+json")
	req.Header.Set("Content-Type", "application/vnd.git-lfs+json")
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	if rr.Code != 509 {
		t.Fatalf("expected 509, got %d body=%s", rr.Code, rr.Body.String())
	}
}

func TestLFSBatchPayloadLimit413(t *testing.T) {
	ResetLFSLimitersForTest()
	opts := DefaultOptions()
	opts.MaxBatchBodyBytes = 20
	router, _ := newLFSRouterWithOptions(opts)
	body := map[string]any{
		"operation": "download",
		"objects":   []map[string]any{{"oid": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "size": 10}},
	}
	raw, _ := json.Marshal(body)
	if len(raw) <= 20 {
		t.Fatalf("test setup invalid: payload unexpectedly <= 20 bytes")
	}
	req := httptest.NewRequest(http.MethodPost, "/info/lfs/objects/batch", bytes.NewReader(raw))
	req.ContentLength = int64(len(raw))
	req.Header.Set("Accept", "application/vnd.git-lfs+json")
	req.Header.Set("Content-Type", "application/vnd.git-lfs+json")
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	if rr.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("expected 413, got %d body=%s", rr.Code, rr.Body.String())
	}
}
