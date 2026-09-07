package internaldrs

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/calypr/syfon/apigen/server/drs"
	"github.com/calypr/syfon/apigen/server/internalapi"
	"github.com/calypr/syfon/internal/api/routeutil"
	"github.com/calypr/syfon/internal/common"
	"github.com/calypr/syfon/internal/core"
	"github.com/calypr/syfon/internal/models"
	"github.com/calypr/syfon/internal/testutils"
	"github.com/google/uuid"
)

func TestHandleInternalUploadBlank(t *testing.T) {
	guid := "new-guid"
	org := "syfon"
	project := "e2e"
	body, _ := json.Marshal(internalapi.InternalUploadBlankRequest{Guid: &guid, Organization: &org, Project: &project})
	rr := doInternalDRSTestRequest(httptest.NewRequest(http.MethodPost, "/data/upload", bytes.NewBuffer(body)), core.NewObjectManager(&testutils.MockDatabase{
		Objects:     map[string]*drs.DrsObject{},
		Credentials: map[string]models.S3Credential{"b1": {Bucket: "b1"}},
		BucketScopes: map[string]models.BucketScope{
			"syfon|e2e": {Organization: "syfon", ProjectID: "e2e", Bucket: "b1"},
		},
	}, &testutils.MockUrlManager{}))
	if rr.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d", rr.Code)
	}
	var resp internalapi.InternalUploadBlankOutput
	_ = json.NewDecoder(rr.Body).Decode(&resp)
	if _, err := uuid.Parse(common.StringVal(resp.Guid)); err != nil {
		t.Fatalf("expected minted UUID, got %q", common.StringVal(resp.Guid))
	}
}

func TestHandleInternalUploadBlank_ResolvesOrganizationProjectScope(t *testing.T) {
	guid := "00000000-0000-4000-8000-000000000001"
	org := "syfon"
	project := "e2e"
	body, _ := json.Marshal(internalapi.InternalUploadBlankRequest{Guid: &guid, Organization: &org, Project: &project})
	mockUM := &capturingMultipartURLManager{}
	rr := doInternalDRSTestRequest(httptest.NewRequest(http.MethodPost, "/data/upload", bytes.NewBuffer(body)), core.NewObjectManager(&testutils.MockDatabase{
		Objects:     map[string]*drs.DrsObject{},
		Credentials: map[string]models.S3Credential{"b1": {Bucket: "b1"}},
		BucketScopes: map[string]models.BucketScope{
			"syfon|": {
				Organization: "syfon",
				Bucket:       "b1",
				PathPrefix:   "program-root",
			},
			"syfon|e2e": {
				Organization: "syfon",
				ProjectID:    "e2e",
				Bucket:       "b1",
				PathPrefix:   "project-subpath",
			},
		},
	}, mockUM))
	if rr.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d body=%s", rr.Code, rr.Body.String())
	}
	wantURL := "s3://b1/program-root/project-subpath/00000000-0000-4000-8000-000000000001"
	if mockUM.signURL != wantURL {
		t.Fatalf("expected scoped upload URL %q, got %q", wantURL, mockUM.signURL)
	}
	if mockUM.signID != "b1" {
		t.Fatalf("expected signer bucket b1, got %q", mockUM.signID)
	}
}

func TestHandleInternalUploadBlank_RequiresScope(t *testing.T) {
	guid := "new-guid"
	body, _ := json.Marshal(internalapi.InternalUploadBlankRequest{Guid: &guid})
	rr := doInternalDRSTestRequest(httptest.NewRequest(http.MethodPost, "/data/upload", bytes.NewBuffer(body)), core.NewObjectManager(&testutils.MockDatabase{Objects: map[string]*drs.DrsObject{}}, &testutils.MockUrlManager{}))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rr.Code)
	}
}

func TestHandleInternalUploadURL_MissingObjectResolvesOrganizationProjectScope(t *testing.T) {
	mockUM := &capturingMultipartURLManager{}
	req := routeutil.WithPathParams(
		httptest.NewRequest(http.MethodGet, "/data/upload/new-guid?organization=syfon&project=e2e&key=payload.bin", nil),
		map[string]string{"file_id": "new-guid"},
	)
	rr := doInternalDRSTestRequest(req, core.NewObjectManager(&testutils.MockDatabase{
		Objects:     map[string]*drs.DrsObject{},
		Credentials: map[string]models.S3Credential{"b1": {Bucket: "b1"}},
		BucketScopes: map[string]models.BucketScope{
			"syfon|": {
				Organization: "syfon",
				Bucket:       "b1",
				PathPrefix:   "program-root",
			},
			"syfon|e2e": {
				Organization: "syfon",
				ProjectID:    "e2e",
				Bucket:       "b1",
				PathPrefix:   "project-subpath",
			},
		},
	}, mockUM))
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rr.Code, rr.Body.String())
	}
	wantURL := "s3://b1/program-root/project-subpath/payload.bin"
	if mockUM.signURL != wantURL {
		t.Fatalf("expected scoped upload URL %q, got %q", wantURL, mockUM.signURL)
	}
}

func TestHandleInternalMultipartInit(t *testing.T) {
	fileName := "test.bam"
	guid := "multipart-guid"
	org := "syfon"
	project := "e2e"
	body, _ := json.Marshal(internalapi.InternalMultipartInitRequest{Guid: &guid, Key: &fileName, Organization: &org, Project: &project})
	rr := doInternalDRSTestRequest(httptest.NewRequest(http.MethodPost, "/data/multipart/init", bytes.NewBuffer(body)), core.NewObjectManager(&testutils.MockDatabase{
		Objects:     map[string]*drs.DrsObject{},
		Credentials: map[string]models.S3Credential{"b1": {Bucket: "b1"}},
		BucketScopes: map[string]models.BucketScope{
			"syfon|e2e": {Organization: "syfon", ProjectID: "e2e", Bucket: "b1"},
		},
	}, &testutils.MockUrlManager{}))
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
}

func TestHandleInternalMultipartInit_RequiresScopeForNewUpload(t *testing.T) {
	fileName := "test.bam"
	guid := "multipart-guid"
	body, _ := json.Marshal(internalapi.InternalMultipartInitRequest{Guid: &guid, Key: &fileName})
	rr := doInternalDRSTestRequest(httptest.NewRequest(http.MethodPost, "/data/multipart/init", bytes.NewBuffer(body)), core.NewObjectManager(&testutils.MockDatabase{Objects: map[string]*drs.DrsObject{}}, &testutils.MockUrlManager{}))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rr.Code)
	}
}

func TestHandleInternalMultipartInit_ResolvesOrganizationProjectScope(t *testing.T) {
	key := "multipart/new.bin"
	org := "syfon"
	project := "e2e"
	body, _ := json.Marshal(internalapi.InternalMultipartInitRequest{Guid: &key, Organization: &org, Project: &project})
	mockUM := &capturingMultipartURLManager{}
	rr := doInternalDRSTestRequest(httptest.NewRequest(http.MethodPost, "/data/multipart/init", bytes.NewBuffer(body)), core.NewObjectManager(&testutils.MockDatabase{
		Objects:     map[string]*drs.DrsObject{},
		Credentials: map[string]models.S3Credential{"b1": {Bucket: "b1"}},
		BucketScopes: map[string]models.BucketScope{
			"syfon|": {
				Organization: "syfon",
				Bucket:       "b1",
				PathPrefix:   "program-root",
			},
			"syfon|e2e": {
				Organization: "syfon",
				ProjectID:    "e2e",
				Bucket:       "b1",
				PathPrefix:   "project-subpath",
			},
		},
	}, mockUM))
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rr.Code, rr.Body.String())
	}
	if mockUM.bucket != "b1" || mockUM.key != "program-root/project-subpath/multipart/new.bin" {
		t.Fatalf("expected scoped multipart target b1/program-root/project-subpath/multipart/new.bin, got %s/%s", mockUM.bucket, mockUM.key)
	}
}

func TestHandleInternalMultipartInit_PreservesRequestedKey(t *testing.T) {
	key := "programs/programs/projects/e2e/sha256-value"
	org := "syfon"
	project := "e2e"
	body, _ := json.Marshal(internalapi.InternalMultipartInitRequest{Guid: &key, Organization: &org, Project: &project})
	mockUM := &capturingMultipartURLManager{}
	rr := doInternalDRSTestRequest(httptest.NewRequest(http.MethodPost, "/data/multipart/init", bytes.NewBuffer(body)), core.NewObjectManager(&testutils.MockDatabase{
		Objects:     map[string]*drs.DrsObject{},
		Credentials: map[string]models.S3Credential{"b1": {Bucket: "b1"}},
		BucketScopes: map[string]models.BucketScope{
			"syfon|e2e": {Organization: "syfon", ProjectID: "e2e", Bucket: "b1"},
		},
	}, mockUM))
	if rr.Code != http.StatusOK || mockUM.key != key {
		t.Fatalf("expected preserved key, got status=%d key=%q", rr.Code, mockUM.key)
	}
}

func TestHandleInternalMultipartInit_MintsUUIDForChecksumInput(t *testing.T) {
	checksum := strings.Repeat("a", 64)
	body, _ := json.Marshal(internalapi.InternalMultipartInitRequest{Key: &checksum})
	mockDB := &testutils.MockDatabase{Objects: map[string]*drs.DrsObject{}}
	rr := doInternalDRSTestRequest(httptest.NewRequest(http.MethodPost, "/data/multipart/init", bytes.NewBuffer(body)), core.NewObjectManager(mockDB, &testutils.MockUrlManager{}))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rr.Code)
	}
}

func TestHandleInternalMultipartInit_ResolvesExistingByChecksumGUID(t *testing.T) {
	checksum := strings.Repeat("b", 64)
	existingID := "ee53f5ce-8069-4f99-bd59-0517e6a2f1ea"
	mockDB := &testutils.MockDatabase{
		Objects: map[string]*drs.DrsObject{
			existingID: {
				Id:        existingID,
				Checksums: []drs.Checksum{{Type: "sha256", Checksum: checksum}},
				AccessMethods: &[]drs.AccessMethod{{
					Type: drs.AccessMethodTypeS3,
					AccessUrl: &struct {
						Headers *[]string `json:"headers,omitempty"`
						Url     string    `json:"url"`
					}{Url: "s3://b1/" + existingID},
				}},
			},
		},
	}
	body, _ := json.Marshal(internalapi.InternalMultipartInitRequest{Guid: &checksum})
	rr := doInternalDRSTestRequest(httptest.NewRequest(http.MethodPost, "/data/multipart/init", bytes.NewBuffer(body)), core.NewObjectManager(mockDB, &testutils.MockUrlManager{}))
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
}

func TestHandleInternalMultipartInit_ExistingScopedObjectUsesMappedLocation(t *testing.T) {
	checksum := strings.Repeat("c", 64)
	existingID := "ee53f5ce-8069-4f99-bd59-0517e6a2f1ea"
	mockDB := &testutils.MockDatabase{
		Objects: map[string]*drs.DrsObject{
			existingID: {
				Id:               existingID,
				ControlledAccess: &[]string{"/organization/HTAN_INT/project/BForePC"},
				Checksums:        []drs.Checksum{{Type: "sha256", Checksum: checksum}},
				AccessMethods: &[]drs.AccessMethod{{
					Type: drs.AccessMethodTypeS3,
					AccessUrl: &struct {
						Headers *[]string `json:"headers,omitempty"`
						Url     string    `json:"url"`
					}{Url: "s3://bforepc-prod/OHSU/slide.ome.tiff"},
				}},
			},
		},
		Credentials: map[string]models.S3Credential{
			"bforepc": {Bucket: "bforepc", Provider: "s3", Region: "us-west-2"},
		},
		BucketScopes: map[string]models.BucketScope{
			"HTAN_INT|BForePC": {
				Organization: "HTAN_INT",
				ProjectID:    "BForePC",
				Bucket:       "bforepc",
				PathPrefix:   "bforepc-prod",
			},
		},
	}
	mockUM := &capturingMultipartURLManager{}
	body, _ := json.Marshal(internalapi.InternalMultipartInitRequest{Guid: &checksum})
	rr := doInternalDRSTestRequest(httptest.NewRequest(http.MethodPost, "/data/multipart/init", bytes.NewBuffer(body)), core.NewObjectManager(mockDB, mockUM))
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rr.Code, rr.Body.String())
	}
	if mockUM.bucket != "bforepc" || mockUM.key != "bforepc-prod/"+checksum {
		t.Fatalf("expected mapped multipart target bforepc/bforepc-prod/%s, got %q/%q", checksum, mockUM.bucket, mockUM.key)
	}
}

func TestHandleInternalMultipartUpload(t *testing.T) {
	body, _ := json.Marshal(internalapi.InternalMultipartUploadRequest{Key: "hash-key", UploadId: "mock-upload-id", PartNumber: 1})
	rr := doInternalDRSTestRequest(httptest.NewRequest(http.MethodPost, "/data/multipart/upload", bytes.NewBuffer(body)), core.NewObjectManager(&testutils.MockDatabase{Objects: map[string]*drs.DrsObject{}}, &testutils.MockUrlManager{}))
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
}

func TestHandleInternalMultipartComplete(t *testing.T) {
	body, _ := json.Marshal(internalapi.InternalMultipartCompleteRequest{Key: "hash-key", UploadId: "mock-upload-id", Parts: []internalapi.InternalMultipartPart{{PartNumber: 1, ETag: "etag1"}}})
	rr := doInternalDRSTestRequest(httptest.NewRequest(http.MethodPost, "/data/multipart/complete", bytes.NewBuffer(body)), core.NewObjectManager(&testutils.MockDatabase{Objects: map[string]*drs.DrsObject{}}, &testutils.MockUrlManager{}))
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
}

func TestHandleInternalUploadURL_Gen3Unauthorized(t *testing.T) {
	req := routeutil.WithPathParams(httptest.NewRequest(http.MethodGet, "/data/upload/some-id?organization=syfon&project=e2e", nil), map[string]string{"file_id": "some-id"})
	req = req.WithContext(dataTestAuthContext(req.Context(), "gen3", false, nil))
	rr := doInternalDRSTestRequest(req, core.NewObjectManager(&testutils.MockDatabase{Objects: map[string]*drs.DrsObject{}}, &testutils.MockUrlManager{}))
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rr.Code)
	}
}

func TestHandleInternalUploadURL_Branches(t *testing.T) {
	db := &testutils.MockDatabase{
		Objects:     map[string]*drs.DrsObject{},
		Credentials: map[string]models.S3Credential{"b1": {Bucket: "b1"}},
		BucketScopes: map[string]models.BucketScope{
			"syfon|e2e": {Organization: "syfon", ProjectID: "e2e", Bucket: "b1"},
		},
	}
	req := routeutil.WithPathParams(httptest.NewRequest(http.MethodGet, "/data/upload/abc?organization=syfon&project=e2e&filename=f1", nil), map[string]string{"file_id": "abc"})
	rr := doInternalDRSTestRequest(req, core.NewObjectManager(db, &testutils.MockUrlManager{}))
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), "upload=true") {
		t.Fatalf("expected signed upload URL, got status=%d body=%s", rr.Code, rr.Body.String())
	}
}

func TestHandleInternalUploadURL_MissingObjectRequiresScope(t *testing.T) {
	db := &testutils.MockDatabase{Objects: map[string]*drs.DrsObject{}, Credentials: map[string]models.S3Credential{"b1": {Bucket: "b1"}}}
	req := routeutil.WithPathParams(httptest.NewRequest(http.MethodGet, "/data/upload/abc", nil), map[string]string{"file_id": "abc"})
	rr := doInternalDRSTestRequest(req, core.NewObjectManager(db, &testutils.MockUrlManager{}))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%s", rr.Code, rr.Body.String())
	}
}

func TestHandleInternalUploadURL_RewritesScopedObjectURL(t *testing.T) {
	db := &testutils.MockDatabase{
		Objects: map[string]*drs.DrsObject{
			"scoped-obj": {
				Id:               "scoped-obj",
				ControlledAccess: &[]string{"/organization/HTAN_INT/project/BForePC"},
				AccessMethods: &[]drs.AccessMethod{{
					Type: drs.AccessMethodTypeS3,
					AccessUrl: &struct {
						Headers *[]string `json:"headers,omitempty"`
						Url     string    `json:"url"`
					}{Url: "s3://bforepc-prod/OHSU/slide.ome.tiff"},
				}},
			},
		},
		Credentials: map[string]models.S3Credential{
			"bforepc": {Bucket: "bforepc", Provider: "s3", Region: "us-west-2"},
		},
		BucketScopes: map[string]models.BucketScope{
			"HTAN_INT|BForePC": {
				Organization: "HTAN_INT",
				ProjectID:    "BForePC",
				Bucket:       "bforepc",
				PathPrefix:   "bforepc-prod",
			},
		},
	}
	mockUM := &capturingMultipartURLManager{}
	req := routeutil.WithPathParams(httptest.NewRequest(http.MethodGet, "/data/upload/scoped-obj", nil), map[string]string{"file_id": "scoped-obj"})
	rr := doInternalDRSTestRequest(req, core.NewObjectManager(db, mockUM))
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rr.Code, rr.Body.String())
	}
	wantURL := "s3://bforepc/bforepc-prod/OHSU/slide.ome.tiff"
	if mockUM.signURL != wantURL {
		t.Fatalf("expected scoped upload URL %q, got %q", wantURL, mockUM.signURL)
	}
	if mockUM.signID != "bforepc" {
		t.Fatalf("expected signer credential bucket bforepc, got %q", mockUM.signID)
	}
}

func TestHandleInternalUploadURL_ResolvesRegisteredScopedObjectID(t *testing.T) {
	ctx := t.Context()
	database := testutils.NewInMemoryDB()
	om := core.NewObjectManager(database, &capturingMultipartURLManager{})
	if err := om.SaveS3Credential(ctx, &models.S3Credential{Bucket: "syfon-e2e-bucket", Provider: "s3", Region: "us-east-1"}); err != nil {
		t.Fatalf("SaveS3Credential failed: %v", err)
	}
	if err := om.CreateBucketScope(ctx, &models.BucketScope{
		Organization: "syfon",
		ProjectID:    "",
		Bucket:       "syfon-e2e-bucket",
		PathPrefix:   "program-root",
	}); err != nil {
		t.Fatalf("CreateBucketScope failed: %v", err)
	}

	oid := "3d71f043937a09b77826109db4f2b47c46f19923ef823f6a777a15fde0b2c9c7"
	name := "program-root.bin"
	obj, err := core.CandidateToInternalObject(drs.DrsObjectCandidate{
		Name:             &name,
		Size:             20,
		Checksums:        []drs.Checksum{{Type: "sha256", Checksum: oid}},
		ControlledAccess: &[]string{"/organization/syfon/project/e2e"},
		AccessMethods: &[]drs.AccessMethod{{
			Type: drs.AccessMethodTypeS3,
			AccessUrl: &struct {
				Headers *[]string `json:"headers,omitempty"`
				Url     string    `json:"url"`
			}{Url: "s3://syfon-e2e-bucket/program-root/" + oid},
		}},
	}, time.Now().UTC())
	if err != nil {
		t.Fatalf("CandidateToInternalObject failed: %v", err)
	}
	if err := om.RegisterObjects(ctx, []models.InternalObject{obj}); err != nil {
		t.Fatalf("RegisterObjects failed: %v", err)
	}
	registered, err := om.GetObject(ctx, obj.Id, "read")
	if err != nil {
		t.Fatalf("GetObject failed: %v", err)
	}

	mockUM := &capturingMultipartURLManager{}
	om = core.NewObjectManager(database, mockUM)
	req := routeutil.WithPathParams(
		httptest.NewRequest(http.MethodGet, "/data/upload/"+registered.Id+"?key=program-root/"+oid, nil),
		map[string]string{"file_id": registered.Id},
	)
	rr := doInternalDRSTestRequest(req, om)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rr.Code, rr.Body.String())
	}
	if want := "s3://syfon-e2e-bucket/program-root/" + oid; mockUM.signURL != want {
		t.Fatalf("signed URL target = %q, want %q", mockUM.signURL, want)
	}
}

func TestHandleInternalUploadURL_ResolvesRegisteredProjectScopedObjectWithoutQueryHints(t *testing.T) {
	ctx := t.Context()
	database := testutils.NewInMemoryDB()
	om := core.NewObjectManager(database, &capturingMultipartURLManager{})
	if err := om.SaveS3Credential(ctx, &models.S3Credential{Bucket: "syfon-e2e-bucket", Provider: "s3", Region: "us-east-1"}); err != nil {
		t.Fatalf("SaveS3Credential failed: %v", err)
	}
	if err := om.CreateBucketScope(ctx, &models.BucketScope{
		Organization: "syfon",
		Bucket:       "syfon-e2e-bucket",
		PathPrefix:   "program-root",
	}); err != nil {
		t.Fatalf("CreateBucketScope(org) failed: %v", err)
	}
	if err := om.CreateBucketScope(ctx, &models.BucketScope{
		Organization: "syfon",
		ProjectID:    "e2e",
		Bucket:       "syfon-e2e-bucket",
		PathPrefix:   "project-subpath",
	}); err != nil {
		t.Fatalf("CreateBucketScope(project) failed: %v", err)
	}

	oid := "412f8568bfb0e62937ee40c6fcdeaa1cf55910c558c0152250340356c8829a47"
	did := "f781273b-52eb-5ac2-a484-775235eef303"
	name := "project-subpath.bin"
	aliases := []string{"id:" + did}
	obj, err := core.CandidateToInternalObject(drs.DrsObjectCandidate{
		Name:             &name,
		Size:             23,
		Checksums:        []drs.Checksum{{Type: "sha256", Checksum: oid}},
		Aliases:          &aliases,
		ControlledAccess: &[]string{"/organization/syfon/project/e2e"},
		AccessMethods: &[]drs.AccessMethod{{
			Type: drs.AccessMethodTypeS3,
			AccessUrl: &struct {
				Headers *[]string `json:"headers,omitempty"`
				Url     string    `json:"url"`
			}{Url: "s3://syfon-e2e-bucket/program-root/project-subpath/" + oid},
		}},
	}, time.Now().UTC())
	if err != nil {
		t.Fatalf("CandidateToInternalObject failed: %v", err)
	}
	if err := om.RegisterObjects(ctx, []models.InternalObject{obj}); err != nil {
		t.Fatalf("RegisterObjects failed: %v", err)
	}

	mockUM := &capturingMultipartURLManager{}
	req := routeutil.WithPathParams(httptest.NewRequest(http.MethodGet, "/data/upload/"+did, nil), map[string]string{"file_id": did})
	rr := doInternalDRSTestRequest(req, core.NewObjectManager(database, mockUM))
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rr.Code, rr.Body.String())
	}
	wantURL := "s3://syfon-e2e-bucket/program-root/project-subpath/" + oid
	if mockUM.signURL != wantURL {
		t.Fatalf("signed URL target = %q, want %q", mockUM.signURL, wantURL)
	}
	if mockUM.signID != "syfon-e2e-bucket" {
		t.Fatalf("signer credential bucket = %q, want syfon-e2e-bucket", mockUM.signID)
	}
}

func TestHandleInternalUploadURL_RepairsMalformedScopedObjectURL(t *testing.T) {
	db := &testutils.MockDatabase{
		Objects: map[string]*drs.DrsObject{
			"scoped-obj": {
				Id: "scoped-obj",
				Checksums: []drs.Checksum{{
					Type:     "sha256",
					Checksum: "412f8568bfb0e62937ee40c6fcdeaa1cf55910c558c0152250340356c8829a47",
				}},
				ControlledAccess: &[]string{"/organization/syfon/project/e2e"},
				AccessMethods: &[]drs.AccessMethod{{
					Type: drs.AccessMethodTypeS3,
					AccessUrl: &struct {
						Headers *[]string `json:"headers,omitempty"`
						Url     string    `json:"url"`
					}{Url: "s3://objects/f781273b-52eb-5ac2-a484-775235eef303"},
				}},
			},
		},
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
	}
	mockUM := &capturingMultipartURLManager{}
	req := routeutil.WithPathParams(httptest.NewRequest(http.MethodGet, "/data/upload/scoped-obj", nil), map[string]string{"file_id": "scoped-obj"})
	rr := doInternalDRSTestRequest(req, core.NewObjectManager(db, mockUM))
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rr.Code, rr.Body.String())
	}
	wantURL := "s3://syfon-e2e-bucket/program-root/project-subpath/412f8568bfb0e62937ee40c6fcdeaa1cf55910c558c0152250340356c8829a47"
	if mockUM.signURL != wantURL {
		t.Fatalf("expected repaired scoped upload URL %q, got %q", wantURL, mockUM.signURL)
	}
	if mockUM.signID != "syfon-e2e-bucket" {
		t.Fatalf("expected signer credential bucket syfon-e2e-bucket, got %q", mockUM.signID)
	}
}

func TestHandleInternalUploadURL_UsesScopedPathForMalformedObjectURL(t *testing.T) {
	db := &testutils.MockDatabase{
		Objects: map[string]*drs.DrsObject{
			"scoped-obj": {
				Id: "scoped-obj",
				Checksums: []drs.Checksum{{
					Type:     "sha256",
					Checksum: "3d71f043937a09b77826109db4f2b47c46f19923ef823f6a777a15fde0b2c9c7",
				}},
				ControlledAccess: &[]string{"/organization/syfon/project/e2e"},
				AccessMethods: &[]drs.AccessMethod{{
					Type: drs.AccessMethodTypeS3,
					AccessUrl: &struct {
						Headers *[]string `json:"headers,omitempty"`
						Url     string    `json:"url"`
					}{Url: "s3://7b9de5b9-19b2-536f-abcc-fe2a146c4eb5"},
				}},
			},
		},
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
			},
		},
	}
	mockUM := &capturingMultipartURLManager{}
	req := routeutil.WithPathParams(httptest.NewRequest(http.MethodGet, "/data/upload/scoped-obj", nil), map[string]string{"file_id": "scoped-obj"})
	rr := doInternalDRSTestRequest(req, core.NewObjectManager(db, mockUM))
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rr.Code, rr.Body.String())
	}
	wantURL := "s3://syfon-e2e-bucket/program-root/3d71f043937a09b77826109db4f2b47c46f19923ef823f6a777a15fde0b2c9c7"
	if mockUM.signURL != wantURL {
		t.Fatalf("expected scoped upload URL %q, got %q", wantURL, mockUM.signURL)
	}
	if mockUM.signID != "syfon-e2e-bucket" {
		t.Fatalf("expected signer credential bucket syfon-e2e-bucket, got %q", mockUM.signID)
	}
}

func TestHandleInternalUploadURL_UsesExplicitObjectKeyForExistingObject(t *testing.T) {
	const checksum = "3d71f043937a09b77826109db4f2b47c46f19923ef823f6a777a15fde0b2c9c7"
	db := &testutils.MockDatabase{
		Objects: map[string]*drs.DrsObject{
			"7b9de5b9-19b2-536f-abcc-fe2a146c4eb5": {
				Id: "7b9de5b9-19b2-536f-abcc-fe2a146c4eb5",
				Checksums: []drs.Checksum{{
					Type:     "sha256",
					Checksum: checksum,
				}},
				ControlledAccess: &[]string{"/organization/syfon/project/e2e"},
				AccessMethods: &[]drs.AccessMethod{{
					Type: drs.AccessMethodTypeS3,
					AccessUrl: &struct {
						Headers *[]string `json:"headers,omitempty"`
						Url     string    `json:"url"`
					}{Url: "s3://7b9de5b9-19b2-536f-abcc-fe2a146c4eb5"},
				}},
			},
		},
		Credentials: map[string]models.S3Credential{
			"syfon-e2e-bucket": {Bucket: "syfon-e2e-bucket", Provider: "s3", Region: "us-west-2"},
		},
		BucketScopes: map[string]models.BucketScope{
			"syfon|": {
				Organization: "syfon",
				Bucket:       "syfon-e2e-bucket",
				PathPrefix:   "program-root",
			},
		},
	}
	mockUM := &capturingMultipartURLManager{}
	req := routeutil.WithPathParams(
		httptest.NewRequest(http.MethodGet, "/data/upload/7b9de5b9-19b2-536f-abcc-fe2a146c4eb5?key=program-root/"+checksum, nil),
		map[string]string{"file_id": "7b9de5b9-19b2-536f-abcc-fe2a146c4eb5"},
	)
	rr := doInternalDRSTestRequest(req, core.NewObjectManager(db, mockUM))
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rr.Code, rr.Body.String())
	}
	wantURL := "s3://syfon-e2e-bucket/program-root/" + checksum
	if mockUM.signURL != wantURL {
		t.Fatalf("expected explicit object-key upload URL %q, got %q", wantURL, mockUM.signURL)
	}
	if mockUM.signID != "syfon-e2e-bucket" {
		t.Fatalf("expected signer credential bucket syfon-e2e-bucket, got %q", mockUM.signID)
	}
}

func TestHandleInternalUploadURL_ExplicitScopeOverridesMalformedExistingObjectURL(t *testing.T) {
	const checksum = "412f8568bfb0e62937ee40c6fcdeaa1cf55910c558c0152250340356c8829a47"
	db := &testutils.MockDatabase{
		Objects: map[string]*drs.DrsObject{
			"f781273b-52eb-5ac2-a484-775235eef303": {
				Id: "f781273b-52eb-5ac2-a484-775235eef303",
				Checksums: []drs.Checksum{{
					Type:     "sha256",
					Checksum: checksum,
				}},
				AccessMethods: &[]drs.AccessMethod{{
					Type: drs.AccessMethodTypeS3,
					AccessUrl: &struct {
						Headers *[]string `json:"headers,omitempty"`
						Url     string    `json:"url"`
					}{Url: "s3://f781273b-52eb-5ac2-a484-775235eef303"},
				}},
			},
		},
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
	}
	mockUM := &capturingMultipartURLManager{}
	req := routeutil.WithPathParams(
		httptest.NewRequest(http.MethodGet, "/data/upload/f781273b-52eb-5ac2-a484-775235eef303?organization=syfon&project=e2e&key=project-subpath/"+checksum, nil),
		map[string]string{"file_id": "f781273b-52eb-5ac2-a484-775235eef303"},
	)
	rr := doInternalDRSTestRequest(req, core.NewObjectManager(db, mockUM))
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rr.Code, rr.Body.String())
	}
	wantURL := "s3://syfon-e2e-bucket/program-root/project-subpath/" + checksum
	if mockUM.signURL != wantURL {
		t.Fatalf("expected explicit scoped upload URL %q, got %q", wantURL, mockUM.signURL)
	}
	if mockUM.signID != "syfon-e2e-bucket" {
		t.Fatalf("expected signer credential bucket syfon-e2e-bucket, got %q", mockUM.signID)
	}
}

func TestHandleInternalUploadURL_ExplicitScopeIgnoresConflictingObjectMetadata(t *testing.T) {
	const checksum = "45be10b3fe5163b6f11155fb46027878d23e3dc99d525d7079180b9dd9b832e9"
	db := &testutils.MockDatabase{
		Objects: map[string]*drs.DrsObject{
			"4f74e0c2-3c80-5c19-b47c-061b300ae270": {
				Id:               "4f74e0c2-3c80-5c19-b47c-061b300ae270",
				ControlledAccess: &[]string{"/organization/other/project/wrong"},
				Checksums: []drs.Checksum{{
					Type:     "sha256",
					Checksum: checksum,
				}},
				AccessMethods: &[]drs.AccessMethod{{
					Type: drs.AccessMethodTypeS3,
					AccessUrl: &struct {
						Headers *[]string `json:"headers,omitempty"`
						Url     string    `json:"url"`
					}{Url: "s3://objects/4f74e0c2-3c80-5c19-b47c-061b300ae270"},
				}},
			},
		},
		Credentials: map[string]models.S3Credential{
			"syfon-e2e-bucket": {Bucket: "syfon-e2e-bucket", Provider: "s3", Region: "us-west-2"},
		},
		BucketScopes: map[string]models.BucketScope{
			"syfon|e2e": {
				Organization: "syfon",
				ProjectID:    "e2e",
				Bucket:       "syfon-e2e-bucket",
			},
		},
	}
	mockUM := &capturingMultipartURLManager{}
	req := routeutil.WithPathParams(
		httptest.NewRequest(http.MethodGet, "/data/upload/4f74e0c2-3c80-5c19-b47c-061b300ae270?organization=syfon&project=e2e&key="+checksum, nil),
		map[string]string{"file_id": "4f74e0c2-3c80-5c19-b47c-061b300ae270"},
	)
	rr := doInternalDRSTestRequest(req, core.NewObjectManager(db, mockUM))
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rr.Code, rr.Body.String())
	}
	wantURL := "s3://syfon-e2e-bucket/" + checksum
	if mockUM.signURL != wantURL {
		t.Fatalf("expected explicit scoped upload URL %q, got %q", wantURL, mockUM.signURL)
	}
	if mockUM.signID != "syfon-e2e-bucket" {
		t.Fatalf("expected signer credential bucket syfon-e2e-bucket, got %q", mockUM.signID)
	}
}

func TestHandleInternalUploadURL_RejectsMalformedUnscopedObjectURL(t *testing.T) {
	const checksum = "412f8568bfb0e62937ee40c6fcdeaa1cf55910c558c0152250340356c8829a47"
	db := &testutils.MockDatabase{
		Objects: map[string]*drs.DrsObject{
			"f781273b-52eb-5ac2-a484-775235eef303": {
				Id: "f781273b-52eb-5ac2-a484-775235eef303",
				Checksums: []drs.Checksum{{
					Type:     "sha256",
					Checksum: checksum,
				}},
				AccessMethods: &[]drs.AccessMethod{{
					Type: drs.AccessMethodTypeS3,
					AccessUrl: &struct {
						Headers *[]string `json:"headers,omitempty"`
						Url     string    `json:"url"`
					}{Url: "s3://f781273b-52eb-5ac2-a484-775235eef303"},
				}},
			},
		},
		Credentials: map[string]models.S3Credential{
			"syfon-e2e-bucket": {Bucket: "syfon-e2e-bucket", Provider: "s3", Region: "us-west-2"},
		},
	}
	mockUM := &capturingMultipartURLManager{}
	req := routeutil.WithPathParams(
		httptest.NewRequest(http.MethodGet, "/data/upload/f781273b-52eb-5ac2-a484-775235eef303", nil),
		map[string]string{"file_id": "f781273b-52eb-5ac2-a484-775235eef303"},
	)
	rr := doInternalDRSTestRequest(req, core.NewObjectManager(db, mockUM))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%s", rr.Code, rr.Body.String())
	}
}

func TestHandleInternalUploadBulk_MixedResults(t *testing.T) {
	db := &testutils.MockDatabase{
		Objects: map[string]*drs.DrsObject{"obj-1": {Id: "obj-1", AccessMethods: &[]drs.AccessMethod{{Type: drs.AccessMethodTypeS3, AccessUrl: &struct {
			Headers *[]string `json:"headers,omitempty"`
			Url     string    `json:"url"`
		}{Url: "s3://b1/prefix/from-existing.bin"}}}}},
		Credentials: map[string]models.S3Credential{"b1": {Bucket: "b1", Provider: "s3", Region: "us-east-1"}},
	}
	body, _ := json.Marshal(internalapi.InternalUploadBulkRequest{Requests: []internalapi.InternalUploadBulkItem{{FileId: "obj-1"}, {FileId: ""}}})
	rr := doInternalDRSTestRequest(httptest.NewRequest(http.MethodPost, "/data/upload/bulk", bytes.NewBuffer(body)), core.NewObjectManager(db, &testutils.MockUrlManager{}))
	if rr.Code != http.StatusMultiStatus {
		t.Fatalf("expected 207, got %d", rr.Code)
	}
}

func TestHandleInternalUploadBulk_Gen3UnauthorizedPerItem(t *testing.T) {
	db := &testutils.MockDatabase{
		Objects:     map[string]*drs.DrsObject{"secure-id": {Id: "secure-id"}},
		ObjectAuthz: map[string]map[string][]string{"secure-id": {"p": {"q"}}},
	}
	body, _ := json.Marshal(internalapi.InternalUploadBulkRequest{Requests: []internalapi.InternalUploadBulkItem{{FileId: "secure-id"}}})
	req := httptest.NewRequest(http.MethodPost, "/data/upload/bulk", bytes.NewBuffer(body))
	req = req.WithContext(dataTestAuthContext(req.Context(), "gen3", false, nil))
	rr := doInternalDRSTestRequest(req, core.NewObjectManager(db, &testutils.MockUrlManager{}))
	if rr.Code != http.StatusMultiStatus {
		t.Fatalf("expected 207, got %d", rr.Code)
	}
}

func TestHandleInternalMultipartValidationErrors(t *testing.T) {
	om := core.NewObjectManager(&testutils.MockDatabase{Objects: map[string]*drs.DrsObject{}}, &testutils.MockUrlManager{})
	rrUpload := doInternalDRSTestRequest(httptest.NewRequest(http.MethodPost, "/data/multipart/upload", strings.NewReader(`{}`)), om)
	if rrUpload.Code != http.StatusBadRequest {
		t.Fatalf("expected upload 400, got %d", rrUpload.Code)
	}
	rrComplete := doInternalDRSTestRequest(httptest.NewRequest(http.MethodPost, "/data/multipart/complete", strings.NewReader(`{}`)), om)
	if rrComplete.Code != http.StatusBadRequest {
		t.Fatalf("expected complete 400, got %d", rrComplete.Code)
	}
}
