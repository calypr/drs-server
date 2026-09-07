package internaldrs

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/calypr/syfon/apigen/server/bucketapi"
	"github.com/calypr/syfon/apigen/server/drs"
	"github.com/calypr/syfon/internal/common"
	"github.com/calypr/syfon/internal/core"
	"github.com/calypr/syfon/internal/models"
	"github.com/calypr/syfon/internal/testutils"
	"github.com/gofiber/fiber/v3"
)

func TestHandleInternalDeleteProject_RemovesGrantsAndBucketScopes(t *testing.T) {
	mockDB := &testutils.MockDatabase{
		Objects: map[string]*drs.DrsObject{
			"delete-me": {Id: "delete-me", Name: common.Ptr("delete-me")},
			"keep-me":   {Id: "keep-me", Name: common.Ptr("keep-me")},
		},
		ObjectAuthz: map[string]map[string][]string{
			"delete-me": {"org-a": {"proj-a"}},
			"keep-me":   {"org-a": {"proj-b"}},
		},
		BucketScopes: map[string]models.BucketScope{
			"org-a|proj-a": {Organization: "org-a", ProjectID: "proj-a", CredentialID: "bucket-a", Bucket: "bucket-a"},
			"org-a|proj-b": {Organization: "org-a", ProjectID: "proj-b", CredentialID: "bucket-b", Bucket: "bucket-b"},
		},
	}
	req := httptest.NewRequest(http.MethodDelete, "/data/projects/org-a/proj-a", nil)
	req = req.WithContext(dataTestAuthContext(req.Context(), "gen3", true, map[string]map[string]bool{
		"/programs/org-a/projects/proj-a": {"delete": true, "update": true},
	}))

	rr := doInternalDRSTestRequest(req, core.NewObjectManager(mockDB, &testutils.MockUrlManager{}))
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rr.Code, rr.Body.String())
	}
	if _, ok := mockDB.Objects["delete-me"]; !ok {
		t.Fatalf("project removal must retain content")
	}
	if _, ok := mockDB.ObjectAuthz["delete-me"]; ok {
		t.Fatalf("project grant remains after removal")
	}
	if _, ok := mockDB.Objects["keep-me"]; !ok {
		t.Fatalf("expected unrelated object to remain")
	}
	if _, ok := mockDB.BucketScopes["org-a|proj-a"]; ok {
		t.Fatalf("expected scoped bucket mapping to be deleted")
	}
	if _, ok := mockDB.BucketScopes["org-a|proj-b"]; !ok {
		t.Fatalf("expected unrelated bucket mapping to remain")
	}

	var resp projectCleanupResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if resp.DeletedObjects != 1 || resp.DeletedBucketScopes != 1 {
		t.Fatalf("unexpected cleanup counts: %+v", resp)
	}
}

func TestHandleInternalDeleteProject_RequiresGen3Auth(t *testing.T) {
	req := httptest.NewRequest(http.MethodDelete, "/data/projects/org-a/proj-a", nil)
	req = req.WithContext(dataTestAuthContext(req.Context(), "gen3", false, nil))
	rr := doInternalDRSTestRequest(req, core.NewObjectManager(&testutils.MockDatabase{}, &testutils.MockUrlManager{}))
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d body=%s", rr.Code, rr.Body.String())
	}
}

func TestHandleInternalBuckets_Gen3Auth(t *testing.T) {
	mockDB := &testutils.MockDatabase{
		Credentials: map[string]models.S3Credential{"b1": {Bucket: "b1", Region: "us-east-1"}, "b2": {Bucket: "b2", Region: "us-east-1"}},
		Objects: map[string]*drs.DrsObject{
			"obj-1": {Id: "obj-1", Name: common.Ptr("obj-1"), AccessMethods: &[]drs.AccessMethod{
				{Type: drs.AccessMethodTypeS3, AccessUrl: &struct {
					Headers *[]string `json:"headers,omitempty"`
					Url     string    `json:"url"`
				}{Url: "s3://b1/path/obj-1"}},
				{Type: drs.AccessMethodTypeS3, AccessUrl: &struct {
					Headers *[]string `json:"headers,omitempty"`
					Url     string    `json:"url"`
				}{Url: "s3://b2/path/obj-1"}},
			}},
		},
		ObjectAuthz: map[string]map[string][]string{"obj-1": {"cbds": {"proj1"}}},
	}
	req401 := httptest.NewRequest(http.MethodGet, "/data/buckets", nil)
	req401 = req401.WithContext(dataTestAuthContext(req401.Context(), "gen3", false, nil))
	rr401 := doInternalDRSTestRequest(req401, core.NewObjectManager(mockDB, &testutils.MockUrlManager{}))
	if rr401.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rr401.Code)
	}
}

func TestHandleInternalBuckets_IncludesBucketsWithoutScopes(t *testing.T) {
	mockDB := &testutils.MockDatabase{
		Credentials: map[string]models.S3Credential{
			"b1": {CredentialID: "b1", Bucket: "b1", Region: "us-east-1", Provider: "s3"},
			"b2": {CredentialID: "b2", Bucket: "b2", Region: "us-east-1", Provider: "s3"},
		},
	}
	req := httptest.NewRequest(http.MethodGet, "/data/buckets", nil)
	req = req.WithContext(dataTestAuthContext(req.Context(), "gen3", true, map[string]map[string]bool{
		"/programs": {"read": true},
	}))

	rr := doInternalDRSTestRequest(req, core.NewObjectManager(mockDB, &testutils.MockUrlManager{}))
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rr.Code, rr.Body.String())
	}

	var resp bucketapi.BucketsResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if _, ok := resp.S3BUCKETS["b1"]; !ok {
		t.Fatalf("expected b1 in response: %+v", resp.S3BUCKETS)
	}
	if _, ok := resp.S3BUCKETS["b2"]; !ok {
		t.Fatalf("expected b2 in response: %+v", resp.S3BUCKETS)
	}
}

func TestHandleInternalBuckets_PrefersExplicitScopeOverObjectDerivedDuplicate(t *testing.T) {
	mockDB := &testutils.MockDatabase{
		Credentials: map[string]models.S3Credential{
			"EllrottLab": {CredentialID: "EllrottLab", Bucket: "EllrottLab", Region: "us-east-1", Provider: "s3"},
			"cbds":       {CredentialID: "cbds", Bucket: "cbds", Region: "us-east-1", Provider: "s3"},
		},
		BucketScopes: map[string]models.BucketScope{
			"Ellrott_Lab|hla2vec": {
				Organization: "Ellrott_Lab",
				ProjectID:    "hla2vec",
				CredentialID: "EllrottLab",
				Bucket:       "EllrottLab",
			},
		},
		Objects: map[string]*drs.DrsObject{
			"obj-1": {Id: "obj-1", Name: common.Ptr("obj-1"), AccessMethods: &[]drs.AccessMethod{{
				Type: drs.AccessMethodTypeS3,
				AccessUrl: &struct {
					Headers *[]string `json:"headers,omitempty"`
					Url     string    `json:"url"`
				}{Url: "s3://cbds/path/obj-1"},
			}}},
		},
		ObjectAuthz: map[string]map[string][]string{
			"obj-1": {"Ellrott_Lab": {"hla2vec"}},
		},
	}
	req := httptest.NewRequest(http.MethodGet, "/data/buckets", nil)
	req = req.WithContext(dataTestAuthContext(req.Context(), "gen3", true, map[string]map[string]bool{
		"/programs/Ellrott_Lab/projects/hla2vec": {"read": true},
	}))

	rr := doInternalDRSTestRequest(req, core.NewObjectManager(mockDB, &testutils.MockUrlManager{}))
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rr.Code, rr.Body.String())
	}

	var resp bucketapi.BucketsResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	want := "/organization/Ellrott_Lab/project/hla2vec"
	if got := resp.S3BUCKETS["EllrottLab"].Programs; got == nil || len(*got) != 1 || (*got)[0] != want {
		t.Fatalf("expected explicit bucket to advertise only %q, got %+v", want, got)
	}
	if got := resp.S3BUCKETS["cbds"].Programs; got != nil && len(*got) != 0 {
		t.Fatalf("expected object-derived duplicate to be suppressed, got %+v", *got)
	}
}

func TestHandleInternalPutDeleteBucket_Gen3Auth(t *testing.T) {
	mockDB := &testutils.MockDatabase{Credentials: map[string]models.S3Credential{}}
	region, accessKey, secretKey, endpoint, provider, path := "us-east-1", "ak", "sk", t.TempDir(), "file", "s3://bucket2/cbds/proj1"
	putBody, _ := json.Marshal(bucketapi.PutBucketRequest{Bucket: "bucket2", Provider: &provider, Region: &region, AccessKey: &accessKey, SecretKey: &secretKey, Endpoint: &endpoint, Organization: "cbds", ProjectId: "proj1", Path: &path})
	putReq401 := httptest.NewRequest(http.MethodPut, "/data/buckets", bytes.NewBuffer(putBody))
	putReq401 = putReq401.WithContext(dataTestAuthContext(putReq401.Context(), "gen3", false, nil))
	putRR401 := doInternalDRSTestRequest(putReq401, core.NewObjectManager(mockDB, &testutils.MockUrlManager{}))
	if putRR401.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", putRR401.Code)
	}
}

func TestHandleInternalPutBucket_RejectsInvalidGeneratedPayloads(t *testing.T) {
	mockDB := &testutils.MockDatabase{Credentials: map[string]models.S3Credential{}}
	req := httptest.NewRequest(http.MethodPut, "/data/buckets", bytes.NewBufferString(`{"bucket":"b2","organization":"cbds","unexpected":"boom"}`))
	req = req.WithContext(dataTestAuthContext(req.Context(), "gen3", true, map[string]map[string]bool{"/programs/cbds": {"arborist:create-descendant": true}}))
	rr := doInternalDRSTestRequest(req, core.NewObjectManager(mockDB, &testutils.MockUrlManager{}))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rr.Code)
	}
}

func TestHandleInternalPutBucket_ReusesExistingPhysicalBucketCredential(t *testing.T) {
	mockDB := &testutils.MockDatabase{
		Credentials: map[string]models.S3Credential{
			"cbdscollab_1c102e76761b": {
				CredentialID: "cbdscollab_1c102e76761b",
				Bucket:       "cbdscollab",
				Provider:     "s3",
				Region:       "us-east-1",
				AccessKey:    "old-ak",
				SecretKey:    "old-sk",
				Endpoint:     "https://old-endpoint.example.org",
			},
		},
	}
	provider := "s3"
	region := "us-east-1"
	accessKey := "new-ak"
	secretKey := "new-sk"
	endpoint := "https://fortera-object.ohsu.edu"
	path := "s3://cbdscollab/Lab_Projects/Embedding_Rotation"
	putBody, _ := json.Marshal(bucketapi.PutBucketRequest{
		Bucket:       "cbdscollab",
		Provider:     &provider,
		Region:       &region,
		AccessKey:    &accessKey,
		SecretKey:    &secretKey,
		Endpoint:     &endpoint,
		Organization: "Ellrott_Lab",
		ProjectId:    "embedding_rotation",
		Path:         &path,
	})
	req := httptest.NewRequest(http.MethodPut, "/data/buckets", bytes.NewBuffer(putBody))
	req = req.WithContext(dataTestAuthContext(req.Context(), "gen3", true, map[string]map[string]bool{
		"/programs/Ellrott_Lab/projects/embedding_rotation": {"create": true, "update": true},
	}))

	rr := doInternalDRSTestRequest(req, core.NewObjectManager(mockDB, &testutils.MockUrlManager{}))
	if rr.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d body=%s", rr.Code, rr.Body.String())
	}

	updated, ok := mockDB.Credentials["cbdscollab_1c102e76761b"]
	if !ok {
		t.Fatalf("expected existing credential id to be preserved, got %+v", mockDB.Credentials)
	}
	if updated.AccessKey != accessKey || updated.SecretKey != secretKey || updated.Endpoint != endpoint {
		t.Fatalf("expected existing credential to be updated, got %+v", updated)
	}
	if _, exists := mockDB.Credentials[common.DeriveCredentialID("cbdscollab", provider, region, endpoint, accessKey)]; exists {
		t.Fatalf("expected no replacement credential to be created, got %+v", mockDB.Credentials)
	}
	scope, ok := mockDB.BucketScopes["Ellrott_Lab|embedding_rotation"]
	if !ok {
		t.Fatalf("expected bucket scope to be created")
	}
	if scope.CredentialID != "cbdscollab_1c102e76761b" || scope.Bucket != "cbdscollab" || scope.PathPrefix != "Lab_Projects/Embedding_Rotation" {
		t.Fatalf("unexpected scope saved: %+v", scope)
	}
}

func TestRegisterInternalRoutes_Smoke(t *testing.T) {
	app := fiber.New()
	om := core.NewObjectManager(&testutils.MockDatabase{Objects: map[string]*drs.DrsObject{}, Credentials: map[string]models.S3Credential{"b1": {Bucket: "b1"}}}, &testutils.MockUrlManager{})
	RegisterInternalRoutes(app, om)
	resp, err := app.Test(httptest.NewRequest(http.MethodGet, "/data/upload/abc?bucket=b1", nil))
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusBadRequest {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("unexpected status %d body=%s", resp.StatusCode, string(body))
	}
}

func TestRegisteredRoutesByWorkflow(t *testing.T) {
	db := &testutils.MockDatabase{
		Objects: map[string]*drs.DrsObject{
			"obj-1": {Id: "obj-1", Name: ptr("file"), Checksums: []drs.Checksum{{Type: "sha256", Checksum: "sha-1"}}, AccessMethods: &[]drs.AccessMethod{{
				AccessId: ptr("s3"),
				Type:     drs.AccessMethodTypeS3,
				AccessUrl: &struct {
					Headers *[]string `json:"headers,omitempty"`
					Url     string    `json:"url"`
				}{Url: "s3://bucket-a/key"},
			}}},
		},
		Credentials: map[string]models.S3Credential{"bucket-a": {Bucket: "bucket-a", Provider: "s3"}},
	}
	om := core.NewObjectManager(db, &testutils.MockUrlManager{})
	for _, tc := range []struct {
		name string
		req  *http.Request
	}{
		{name: "index", req: httptest.NewRequest(http.MethodGet, "/", nil)},
		{name: "transfer", req: httptest.NewRequest(http.MethodGet, "/data/download/obj-1", nil)},
		{name: "bucket", req: httptest.NewRequest(http.MethodGet, "/data/buckets", nil)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rr := doInternalDRSTestRequest(tc.req, om)
			if rr.Code != http.StatusOK {
				t.Fatalf("expected 200, got %d", rr.Code)
			}
		})
	}
}

func TestHandleInternalListBucketScopes_Success(t *testing.T) {
	mockDB := &testutils.MockDatabase{
		Credentials: map[string]models.S3Credential{
			"bucket-a": {CredentialID: "bucket-a", Bucket: "bucket-a", Provider: "s3"},
		},
		BucketScopes: map[string]models.BucketScope{
			"org-a|proj-a": {Organization: "org-a", ProjectID: "proj-a", CredentialID: "bucket-a", Bucket: "bucket-a", PathPrefix: "path/to/a"},
			"org-a|proj-b": {Organization: "org-a", ProjectID: "proj-b", CredentialID: "bucket-b", Bucket: "bucket-b"},
		},
	}
	req := httptest.NewRequest(http.MethodGet, "/data/buckets/bucket-a/scopes", nil)
	req = req.WithContext(dataTestAuthContext(req.Context(), "gen3", true, map[string]map[string]bool{
		"/programs/org-a/projects/proj-a": {"read": true},
	}))

	rr := doInternalDRSTestRequest(req, core.NewObjectManager(mockDB, &testutils.MockUrlManager{}))
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rr.Code, rr.Body.String())
	}

	var resp []bucketapi.BucketScopeResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if len(resp) != 1 {
		t.Fatalf("expected 1 scope, got %d", len(resp))
	}
	if resp[0].Organization != "org-a" || resp[0].ProjectId != "proj-a" {
		t.Fatalf("unexpected scope: %+v", resp[0])
	}
	if resp[0].Path == nil || *resp[0].Path != "s3://bucket-a/path/to/a" {
		t.Fatalf("unexpected path: %+v", resp[0].Path)
	}
}

func TestHandleInternalListBucketScopes_FiltersUnauthorizedScopesOnSharedBucket(t *testing.T) {
	mockDB := &testutils.MockDatabase{
		Credentials: map[string]models.S3Credential{
			"bucket-a": {CredentialID: "bucket-a", Bucket: "bucket-a", Provider: "s3"},
		},
		BucketScopes: map[string]models.BucketScope{
			"org-a|proj-a": {Organization: "org-a", ProjectID: "proj-a", CredentialID: "bucket-a", Bucket: "bucket-a", PathPrefix: "path/to/a"},
			"org-b|proj-b": {Organization: "org-b", ProjectID: "proj-b", CredentialID: "bucket-a", Bucket: "bucket-a", PathPrefix: "secret/path"},
		},
	}
	req := httptest.NewRequest(http.MethodGet, "/data/buckets/bucket-a/scopes", nil)
	req = req.WithContext(dataTestAuthContext(req.Context(), "gen3", true, map[string]map[string]bool{
		"/programs/org-a/projects/proj-a": {"read": true},
	}))

	rr := doInternalDRSTestRequest(req, core.NewObjectManager(mockDB, &testutils.MockUrlManager{}))
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rr.Code, rr.Body.String())
	}

	var resp []bucketapi.BucketScopeResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if len(resp) != 1 {
		t.Fatalf("expected exactly 1 visible scope, got %d: %+v", len(resp), resp)
	}
	if resp[0].Organization != "org-a" || resp[0].ProjectId != "proj-a" {
		t.Fatalf("unexpected visible scope: %+v", resp[0])
	}
	if resp[0].Path == nil || *resp[0].Path != "s3://bucket-a/path/to/a" {
		t.Fatalf("unexpected visible scope path: %+v", resp[0].Path)
	}
}

func TestHandleInternalListBucketScopes_RendersRootScopeAsBucketURL(t *testing.T) {
	mockDB := &testutils.MockDatabase{
		Credentials: map[string]models.S3Credential{
			"gdcdata": {CredentialID: "gdcdata", Bucket: "gdcdata", Provider: "s3"},
		},
		BucketScopes: map[string]models.BucketScope{
			"gdc|": {Organization: "gdc", ProjectID: "", CredentialID: "gdcdata", Bucket: "gdcdata", PathPrefix: ""},
		},
	}
	req := httptest.NewRequest(http.MethodGet, "/data/buckets/gdcdata/scopes", nil)
	req = req.WithContext(dataTestAuthContext(req.Context(), "gen3", true, map[string]map[string]bool{
		"/programs/gdc": {"read": true},
	}))

	rr := doInternalDRSTestRequest(req, core.NewObjectManager(mockDB, &testutils.MockUrlManager{}))
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rr.Code, rr.Body.String())
	}

	var resp []bucketapi.BucketScopeResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if len(resp) != 1 {
		t.Fatalf("expected 1 scope, got %d: %+v", len(resp), resp)
	}
	if resp[0].Path == nil || *resp[0].Path != "s3://gdcdata" {
		t.Fatalf("expected root scope path s3://gdcdata, got %+v", resp[0].Path)
	}
}

func TestHandleInternalListBucketScopes_RequiresGen3Auth(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/data/buckets/bucket-a/scopes", nil)
	req = req.WithContext(dataTestAuthContext(req.Context(), "gen3", false, nil))
	rr := doInternalDRSTestRequest(req, core.NewObjectManager(&testutils.MockDatabase{}, &testutils.MockUrlManager{}))
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d body=%s", rr.Code, rr.Body.String())
	}
}

func TestHandleInternalDeleteBucketScope_RequiresExactPathMatch(t *testing.T) {
	mockDB := &testutils.MockDatabase{
		Credentials: map[string]models.S3Credential{
			"bucket-a": {CredentialID: "bucket-a", Bucket: "bucket-a", Provider: "s3"},
		},
		BucketScopes: map[string]models.BucketScope{
			"org-a|":       {Organization: "org-a", ProjectID: "", CredentialID: "bucket-a", Bucket: "bucket-a", PathPrefix: "lab"},
			"org-a|proj-a": {Organization: "org-a", ProjectID: "proj-a", CredentialID: "bucket-a", Bucket: "bucket-a", PathPrefix: "lab/project-a"},
		},
	}
	req := httptest.NewRequest(http.MethodDelete, "/data/buckets/bucket-a/scopes?organization=org-a&path=s3://bucket-a/lab", nil)
	req = req.WithContext(dataTestAuthContext(req.Context(), "gen3", true, map[string]map[string]bool{
		"/programs/org-a": {"delete": true, "update": true},
	}))

	rr := doInternalDRSTestRequest(req, core.NewObjectManager(mockDB, &testutils.MockUrlManager{}))
	if rr.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d body=%s", rr.Code, rr.Body.String())
	}
	if _, ok := mockDB.BucketScopes["org-a|"]; ok {
		t.Fatalf("expected exact path org-only scope to be deleted")
	}
	if _, ok := mockDB.BucketScopes["org-a|proj-a"]; !ok {
		t.Fatalf("expected project scope to remain")
	}
}

func TestHandleInternalDeleteBucketScope_AllowsEmptyRootPath(t *testing.T) {
	mockDB := &testutils.MockDatabase{
		Credentials: map[string]models.S3Credential{
			"bucket-a": {CredentialID: "bucket-a", Bucket: "bucket-a", Provider: "s3"},
		},
		BucketScopes: map[string]models.BucketScope{
			"org-a|": {Organization: "org-a", ProjectID: "", CredentialID: "bucket-a", Bucket: "bucket-a", PathPrefix: ""},
		},
	}
	req := httptest.NewRequest(http.MethodDelete, "/data/buckets/bucket-a/scopes?organization=org-a&path=", nil)
	req = req.WithContext(dataTestAuthContext(req.Context(), "gen3", true, map[string]map[string]bool{
		"/programs/org-a": {"delete": true, "update": true},
	}))

	rr := doInternalDRSTestRequest(req, core.NewObjectManager(mockDB, &testutils.MockUrlManager{}))
	if rr.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d body=%s", rr.Code, rr.Body.String())
	}
	if _, ok := mockDB.BucketScopes["org-a|"]; ok {
		t.Fatalf("expected root scope to be deleted")
	}
}
