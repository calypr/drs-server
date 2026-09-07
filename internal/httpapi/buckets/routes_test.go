package buckets

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v3"

	"github.com/calypr/syfon/apigen/server/bucketapi"
	"github.com/calypr/syfon/internal/buckets"
	"github.com/calypr/syfon/internal/objects"
)

type recordingBucketCredentialStore struct {
	events *[]string
}

func (s *recordingBucketCredentialStore) GetS3Credential(_ context.Context, bucket string) (*buckets.Credential, error) {
	*s.events = append(*s.events, "get:"+bucket)
	return nil, errors.New("credential not found")
}

func (s *recordingBucketCredentialStore) ListS3Credentials(context.Context) ([]buckets.Credential, error) {
	return nil, nil
}

func (s *recordingBucketCredentialStore) SaveS3Credential(_ context.Context, _ *buckets.Credential) error {
	*s.events = append(*s.events, "save")
	return nil
}

func (s *recordingBucketCredentialStore) DeleteS3Credential(context.Context, string) error {
	return nil
}

type recordingBucketScopeStore struct {
	events *[]string
}

func (s *recordingBucketScopeStore) CreateBucketScope(context.Context, *buckets.Scope) error {
	*s.events = append(*s.events, "scope")
	return nil
}

func (s *recordingBucketScopeStore) DeleteBucketScope(context.Context, string, string, string, string) error {
	return nil
}

func (s *recordingBucketScopeStore) GetBucketScope(context.Context, string, string) (*buckets.Scope, error) {
	return nil, nil
}

func (s *recordingBucketScopeStore) ListBucketScopes(context.Context) ([]buckets.Scope, error) {
	return nil, nil
}

func TestHandleInternalPutBucket_CreatesScopeBeforeSavingCredential(t *testing.T) {
	events := []string{}
	credentials := &recordingBucketCredentialStore{events: &events}
	scopes := &recordingBucketScopeStore{events: &events}
	bucketService, err := buckets.NewService(buckets.Dependencies{
		Credentials:     credentials,
		CredentialAdmin: credentials,
		Scopes:          scopes,
		Fallback: func(context.Context) ([]buckets.VisibilityRow, error) {
			return nil, nil
		},
	}, nil)
	if err != nil {
		t.Fatalf("construct bucket service: %v", err)
	}

	provider := "file"
	body, err := json.Marshal(bucketapi.PutBucketRequest{
		Bucket:       "bucket-order",
		Provider:     &provider,
		Organization: "org",
		ProjectId:    "project",
	})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	req := httptest.NewRequest(http.MethodPut, "/data/buckets", bytes.NewReader(body))
	req = req.WithContext(dataTestAuthContext(req.Context(), "gen3", true, map[string]map[string]bool{
		"/programs/org/projects/project": {"create": true, "update": true},
	}))

	app := fiber.New()
	app.Put("/data/buckets", func(c fiber.Ctx) error {
		return handleInternalPutBucketFiber(c, bucketService)
	})
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 201, got %d body=%s", resp.StatusCode, body)
	}

	var scopeIndex, saveIndex = -1, -1
	for i, event := range events {
		switch event {
		case "scope":
			scopeIndex = i
		case "save":
			saveIndex = i
		}
	}
	if scopeIndex == -1 || saveIndex == -1 || scopeIndex >= saveIndex {
		t.Fatalf("expected scope creation before credential save, events=%v", events)
	}
}

func TestHandleInternalBuckets_Gen3Auth(t *testing.T) {
	mockDB := &bucketTestStore{
		Credentials: map[string]buckets.Credential{"b1": {Bucket: "b1", Region: "us-east-1"}, "b2": {Bucket: "b2", Region: "us-east-1"}},
		Objects: map[string]*objects.Record{
			"obj-1": {Id: "obj-1", Name: ptr("obj-1"), AccessMethods: &[]objects.AccessMethod{
				{Type: "s3", AccessUrl: &objects.AccessURL{
					Url: "s3://b1/path/obj-1"}},
				{Type: "s3", AccessUrl: &objects.AccessURL{
					Url: "s3://b2/path/obj-1"}},
			}},
		},
		ObjectAuthz: map[string]map[string][]string{"obj-1": {"cbds": {"proj1"}}},
	}
	req401 := httptest.NewRequest(http.MethodGet, "/data/buckets", nil)
	req401 = req401.WithContext(dataTestAuthContext(req401.Context(), "gen3", false, nil))
	rr401 := doInternalDRSTestRequest(req401, newInternalDRSObjectManager(mockDB, &internalDRSStorageFake{}))
	if rr401.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rr401.Code)
	}
}

func TestHandleInternalBuckets_IncludesBucketsWithoutScopes(t *testing.T) {
	mockDB := &bucketTestStore{
		Credentials: map[string]buckets.Credential{
			"b1": {CredentialID: "b1", Bucket: "b1", Region: "us-east-1", Provider: "s3"},
			"b2": {CredentialID: "b2", Bucket: "b2", Region: "us-east-1", Provider: "s3"},
		},
	}
	req := httptest.NewRequest(http.MethodGet, "/data/buckets", nil)
	req = req.WithContext(dataTestAuthContext(req.Context(), "gen3", true, map[string]map[string]bool{
		"/programs": {"read": true},
	}))

	rr := doInternalDRSTestRequest(req, newInternalDRSObjectManager(mockDB, &internalDRSStorageFake{}))
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
	mockDB := &bucketTestStore{
		Credentials: map[string]buckets.Credential{
			"EllrottLab": {CredentialID: "EllrottLab", Bucket: "EllrottLab", Region: "us-east-1", Provider: "s3"},
			"cbds":       {CredentialID: "cbds", Bucket: "cbds", Region: "us-east-1", Provider: "s3"},
		},
		BucketScopes: map[string]buckets.Scope{
			"Ellrott_Lab|hla2vec": {
				Organization: "Ellrott_Lab",
				ProjectID:    "hla2vec",
				CredentialID: "EllrottLab",
				Bucket:       "EllrottLab",
			},
		},
		Objects: map[string]*objects.Record{
			"obj-1": {Id: "obj-1", Name: ptr("obj-1"), AccessMethods: &[]objects.AccessMethod{{
				Type: "s3",
				AccessUrl: &objects.AccessURL{

					Url: "s3://cbds/path/obj-1"},
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

	rr := doInternalDRSTestRequest(req, newInternalDRSObjectManager(mockDB, &internalDRSStorageFake{}))
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
	mockDB := &bucketTestStore{Credentials: map[string]buckets.Credential{}}
	region, accessKey, secretKey, endpoint, provider, path := "us-east-1", "ak", "sk", t.TempDir(), "file", "s3://bucket2/cbds/proj1"
	putBody, _ := json.Marshal(bucketapi.PutBucketRequest{Bucket: "bucket2", Provider: &provider, Region: &region, AccessKey: &accessKey, SecretKey: &secretKey, Endpoint: &endpoint, Organization: "cbds", ProjectId: "proj1", Path: &path})
	putReq401 := httptest.NewRequest(http.MethodPut, "/data/buckets", bytes.NewBuffer(putBody))
	putReq401 = putReq401.WithContext(dataTestAuthContext(putReq401.Context(), "gen3", false, nil))
	putRR401 := doInternalDRSTestRequest(putReq401, newInternalDRSObjectManager(mockDB, &internalDRSStorageFake{}))
	if putRR401.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", putRR401.Code)
	}
}

func TestHandleInternalPutBucket_RejectsInvalidGeneratedPayloads(t *testing.T) {
	mockDB := &bucketTestStore{Credentials: map[string]buckets.Credential{}}
	req := httptest.NewRequest(http.MethodPut, "/data/buckets", bytes.NewBufferString(`{"bucket":"b2","organization":"cbds","unexpected":"boom"}`))
	req = req.WithContext(dataTestAuthContext(req.Context(), "gen3", true, map[string]map[string]bool{"/programs/cbds": {"arborist:create-descendant": true}}))
	rr := doInternalDRSTestRequest(req, newInternalDRSObjectManager(mockDB, &internalDRSStorageFake{}))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rr.Code)
	}
}

func TestHandleInternalPutBucket_ReusesExistingPhysicalBucketCredential(t *testing.T) {
	mockDB := &bucketTestStore{
		Credentials: map[string]buckets.Credential{
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

	rr := doInternalDRSTestRequest(req, newInternalDRSObjectManager(mockDB, &internalDRSStorageFake{}))
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
	if _, exists := mockDB.Credentials[buckets.DeriveCredentialID("cbdscollab", provider, region, endpoint, accessKey)]; exists {
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

func TestHandleInternalListBucketScopes_Success(t *testing.T) {
	mockDB := &bucketTestStore{
		Credentials: map[string]buckets.Credential{
			"bucket-a": {CredentialID: "bucket-a", Bucket: "bucket-a", Provider: "s3"},
		},
		BucketScopes: map[string]buckets.Scope{
			"org-a|proj-a": {Organization: "org-a", ProjectID: "proj-a", CredentialID: "bucket-a", Bucket: "bucket-a", PathPrefix: "path/to/a"},
			"org-a|proj-b": {Organization: "org-a", ProjectID: "proj-b", CredentialID: "bucket-b", Bucket: "bucket-b"},
		},
	}
	req := httptest.NewRequest(http.MethodGet, "/data/buckets/bucket-a/scopes", nil)
	req = req.WithContext(dataTestAuthContext(req.Context(), "gen3", true, map[string]map[string]bool{
		"/programs/org-a/projects/proj-a": {"read": true},
	}))

	rr := doInternalDRSTestRequest(req, newInternalDRSObjectManager(mockDB, &internalDRSStorageFake{}))
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
	mockDB := &bucketTestStore{
		Credentials: map[string]buckets.Credential{
			"bucket-a": {CredentialID: "bucket-a", Bucket: "bucket-a", Provider: "s3"},
		},
		BucketScopes: map[string]buckets.Scope{
			"org-a|proj-a": {Organization: "org-a", ProjectID: "proj-a", CredentialID: "bucket-a", Bucket: "bucket-a", PathPrefix: "path/to/a"},
			"org-b|proj-b": {Organization: "org-b", ProjectID: "proj-b", CredentialID: "bucket-a", Bucket: "bucket-a", PathPrefix: "secret/path"},
		},
	}
	req := httptest.NewRequest(http.MethodGet, "/data/buckets/bucket-a/scopes", nil)
	req = req.WithContext(dataTestAuthContext(req.Context(), "gen3", true, map[string]map[string]bool{
		"/programs/org-a/projects/proj-a": {"read": true},
	}))

	rr := doInternalDRSTestRequest(req, newInternalDRSObjectManager(mockDB, &internalDRSStorageFake{}))
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
	mockDB := &bucketTestStore{
		Credentials: map[string]buckets.Credential{
			"gdcdata": {CredentialID: "gdcdata", Bucket: "gdcdata", Provider: "s3"},
		},
		BucketScopes: map[string]buckets.Scope{
			"gdc|": {Organization: "gdc", ProjectID: "", CredentialID: "gdcdata", Bucket: "gdcdata", PathPrefix: ""},
		},
	}
	req := httptest.NewRequest(http.MethodGet, "/data/buckets/gdcdata/scopes", nil)
	req = req.WithContext(dataTestAuthContext(req.Context(), "gen3", true, map[string]map[string]bool{
		"/programs/gdc": {"read": true},
	}))

	rr := doInternalDRSTestRequest(req, newInternalDRSObjectManager(mockDB, &internalDRSStorageFake{}))
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
	rr := doInternalDRSTestRequest(req, newInternalDRSObjectManager(&bucketTestStore{}, &internalDRSStorageFake{}))
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d body=%s", rr.Code, rr.Body.String())
	}
}

func TestHandleInternalDeleteBucketScope_RequiresExactPathMatch(t *testing.T) {
	mockDB := &bucketTestStore{
		Credentials: map[string]buckets.Credential{
			"bucket-a": {CredentialID: "bucket-a", Bucket: "bucket-a", Provider: "s3"},
		},
		BucketScopes: map[string]buckets.Scope{
			"org-a|":       {Organization: "org-a", ProjectID: "", CredentialID: "bucket-a", Bucket: "bucket-a", PathPrefix: "lab"},
			"org-a|proj-a": {Organization: "org-a", ProjectID: "proj-a", CredentialID: "bucket-a", Bucket: "bucket-a", PathPrefix: "lab/project-a"},
		},
	}
	req := httptest.NewRequest(http.MethodDelete, "/data/buckets/bucket-a/scopes?organization=org-a&path=s3://bucket-a/lab", nil)
	req = req.WithContext(dataTestAuthContext(req.Context(), "gen3", true, map[string]map[string]bool{
		"/programs/org-a": {"delete": true, "update": true},
	}))

	rr := doInternalDRSTestRequest(req, newInternalDRSObjectManager(mockDB, &internalDRSStorageFake{}))
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
	mockDB := &bucketTestStore{
		Credentials: map[string]buckets.Credential{
			"bucket-a": {CredentialID: "bucket-a", Bucket: "bucket-a", Provider: "s3"},
		},
		BucketScopes: map[string]buckets.Scope{
			"org-a|": {Organization: "org-a", ProjectID: "", CredentialID: "bucket-a", Bucket: "bucket-a", PathPrefix: ""},
		},
	}
	req := httptest.NewRequest(http.MethodDelete, "/data/buckets/bucket-a/scopes?organization=org-a&path=", nil)
	req = req.WithContext(dataTestAuthContext(req.Context(), "gen3", true, map[string]map[string]bool{
		"/programs/org-a": {"delete": true, "update": true},
	}))

	rr := doInternalDRSTestRequest(req, newInternalDRSObjectManager(mockDB, &internalDRSStorageFake{}))
	if rr.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d body=%s", rr.Code, rr.Body.String())
	}
	if _, ok := mockDB.BucketScopes["org-a|"]; ok {
		t.Fatalf("expected root scope to be deleted")
	}
}
