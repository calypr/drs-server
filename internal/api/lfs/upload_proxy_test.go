package lfs

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/calypr/syfon/apigen/server/drs"
	"github.com/calypr/syfon/internal/core"
	"github.com/calypr/syfon/internal/models"
	"github.com/calypr/syfon/internal/testutils"
	"github.com/gofiber/fiber/v3"
)

func TestLFSUploadProxyNoBucket507(t *testing.T) {
	ResetLFSLimitersForTest()
	router, db := newLFSRouter()
	db.Credentials = map[string]models.S3Credential{}
	db.NoDefaultCreds = true
	req := httptest.NewRequest(http.MethodPut, "/info/lfs/objects/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", bytes.NewReader([]byte("x")))
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	if rr.Code != http.StatusInsufficientStorage {
		t.Fatalf("expected 507, got %d body=%s", rr.Code, rr.Body.String())
	}
}

func TestLFSUploadProxySuccess(t *testing.T) {
	// 1. Setup mock upload server
	uploadServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("ETag", "mock-etag")
		w.WriteHeader(http.StatusOK)
	}))
	defer uploadServer.Close()

	// 2. Setup Syfon router with mocked dependencies
	db := &testutils.MockDatabase{
		Objects: map[string]*drs.DrsObject{},
	}
	uM := &customMockUrlManager{uploadURL: uploadServer.URL}
	app := fiber.New()
	om := core.NewObjectManager(db, uM)
	RegisterLFSRoutes(app, om, DefaultOptions())
	router := &fiberTestRouter{app: app}

	// 3. Perform upload proxy request
	oid := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	req := httptest.NewRequest(http.MethodPut, "/info/lfs/objects/"+oid, bytes.NewReader([]byte("small content")))
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	// 4. Verify results
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rr.Code, rr.Body.String())
	}
	if uM.initCalled != 1 {
		t.Errorf("expected 1 InitMultipartUpload call, got %d", uM.initCalled)
	}
	if uM.completeCalled != 1 {
		t.Errorf("expected 1 CompleteMultipartUpload call, got %d", uM.completeCalled)
	}
}

func TestLFSUploadProxyUsesPendingScopedCanonicalLocation(t *testing.T) {
	uploadServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("ETag", "mock-etag")
		w.WriteHeader(http.StatusOK)
	}))
	defer uploadServer.Close()

	oid := strings.Repeat("b", 64)
	db := &testutils.MockDatabase{
		Objects: map[string]*drs.DrsObject{},
		Credentials: map[string]models.S3Credential{
			"syfon-e2e-bucket": {Bucket: "syfon-e2e-bucket", Provider: "s3", Region: "us-west-2"},
		},
		BucketScopes: map[string]models.BucketScope{
			"syfon|": {
				Organization: "syfon",
				Bucket:       "syfon-e2e-bucket",
				PathPrefix:   "program-root",
			},
			"syfon|e2e": {
				Organization: "syfon",
				ProjectID:    "e2e",
				Bucket:       "syfon-e2e-bucket",
				PathPrefix:   "project-subpath",
			},
		},
		PendingMeta: map[string]models.PendingLFSMeta{
			oid: {
				OID: oid,
				Candidate: drs.DrsObjectCandidate{
					Name:             ptr("scoped-lfs.bin"),
					Size:             13,
					Checksums:        []drs.Checksum{{Type: "sha256", Checksum: oid}},
					ControlledAccess: &[]string{"/organization/syfon/project/e2e"},
					AccessMethods: &[]drs.AccessMethod{{
						Type: drs.AccessMethodTypeS3,
						AccessUrl: &struct {
							Headers *[]string `json:"headers,omitempty"`
							Url     string    `json:"url"`
						}{Url: "s3://objects/stale-object-id"},
					}},
				},
				CreatedAt: time.Now().UTC(),
				ExpiresAt: time.Now().UTC().Add(time.Minute),
			},
		},
	}
	uM := &customMockUrlManager{uploadURL: uploadServer.URL}
	app := fiber.New()
	om := core.NewObjectManager(db, uM)
	RegisterLFSRoutes(app, om, DefaultOptions())
	router := &fiberTestRouter{app: app}

	req := httptest.NewRequest(http.MethodPut, "/info/lfs/objects/"+oid, bytes.NewReader([]byte("small content")))
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rr.Code, rr.Body.String())
	}
	if uM.initBucket != "syfon-e2e-bucket" {
		t.Fatalf("expected scoped bucket syfon-e2e-bucket, got %q", uM.initBucket)
	}
	if want := "program-root/project-subpath/" + oid; uM.initKey != want {
		t.Fatalf("expected scoped key %q, got %q", want, uM.initKey)
	}
}
