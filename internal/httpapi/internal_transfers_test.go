package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/calypr/syfon/apigen/drs"
	"github.com/calypr/syfon/apigen/errorapi"
	"github.com/calypr/syfon/apigen/internalapi"
	"github.com/calypr/syfon/internal/buckets"
	httpdrs "github.com/calypr/syfon/internal/httpapi/drs"
	"github.com/calypr/syfon/internal/objects"
	objectrecords "github.com/calypr/syfon/internal/objects/records"
	"github.com/calypr/syfon/internal/persistence/store"
	"github.com/calypr/syfon/internal/storage"
	domaintransfers "github.com/calypr/syfon/internal/transfers"
	"github.com/calypr/syfon/internal/usage"
	"github.com/google/uuid"
)

func TestHandleInternalDownloadAmbiguousScopeSucceedsWithUnattributedEvent(t *testing.T) {
	database := &transferHTTPFixture{
		Objects: map[string]*objects.Record{
			"shared-object": {
				Id: "shared-object",
				ControlledAccess: &[]string{
					"/organization/org/project/project-a",
					"/organization/org/project/project-b",
				},
				AccessMethods: &[]objects.AccessMethod{{
					Type:      "s3",
					AccessUrl: &objects.AccessURL{Url: "s3://bucket/shared-object"},
				}},
			},
		},
		Credentials: map[string]buckets.Credential{"bucket": {Bucket: "bucket"}},
	}

	response := transfersDoInternalDRSTestRequest(
		httptest.NewRequest(http.MethodGet, "/data/download/shared-object", nil),
		transfersNewInternalDRSObjectManager(database, &internalDRSStorageFake{}),
	)
	if response.Code != http.StatusOK {
		t.Fatalf("ambiguous download status = %d, body = %s", response.Code, response.Body.String())
	}
	if len(database.TransferEvents) != 1 {
		t.Fatalf("ambiguous download events = %+v, want one event", database.TransferEvents)
	}
	event := database.TransferEvents[0]
	if event.Organization != "" || event.Project != "" {
		t.Fatalf("ambiguous download was attributed to %q/%q", event.Organization, event.Project)
	}
}

func TestHandleInternalUploadURLUsesAuthorizedExplicitScopeForAttribution(t *testing.T) {
	database := &transferHTTPFixture{
		Objects: map[string]*objects.Record{
			"scoped-object": {
				Id: "scoped-object",
				ControlledAccess: &[]string{
					"/organization/org/project/project",
					"/organization/org/project/other",
				},
				AccessMethods: &[]objects.AccessMethod{{
					Type:      "s3",
					AccessUrl: &objects.AccessURL{Url: "s3://bucket/scoped-object"},
				}},
			},
		},
		Credentials: map[string]buckets.Credential{"bucket": {Bucket: "bucket"}},
		BucketScopes: map[string]buckets.Scope{
			"org|":        {Organization: "org", Bucket: "bucket"},
			"org|project": {Organization: "org", ProjectID: "project", Bucket: "bucket"},
			"org|missing": {Organization: "org", ProjectID: "missing", Bucket: "bucket"},
		},
	}

	response := transfersDoInternalDRSTestRequest(
		httptest.NewRequest(http.MethodGet, "/data/upload/scoped-object?organization=org&project=project&key=scoped-object", nil),
		transfersNewInternalDRSObjectManager(database, &internalDRSStorageFake{}),
	)
	if response.Code != http.StatusOK {
		t.Fatalf("explicit upload status = %d, body = %s", response.Code, response.Body.String())
	}
	if len(database.TransferEvents) != 1 {
		t.Fatalf("explicit upload events = %+v, want one event", database.TransferEvents)
	}
	event := database.TransferEvents[0]
	if event.Organization != "org" || event.Project != "project" {
		t.Fatalf("explicit upload scope = %q/%q, want org/project", event.Organization, event.Project)
	}

	database.TransferEvents = nil
	unsupported := transfersDoInternalDRSTestRequest(
		httptest.NewRequest(http.MethodGet, "/data/upload/scoped-object?organization=org&project=missing&key=scoped-object", nil),
		transfersNewInternalDRSObjectManager(database, &internalDRSStorageFake{}),
	)
	if unsupported.Code != http.StatusOK {
		t.Fatalf("unsupported upload scope status = %d, body = %s", unsupported.Code, unsupported.Body.String())
	}
	if len(database.TransferEvents) != 1 {
		t.Fatalf("unsupported upload events = %+v, want one event", database.TransferEvents)
	}
	if event := database.TransferEvents[0]; event.Organization != "" || event.Project != "" {
		t.Fatalf("unsupported upload scope = %q/%q, want empty scope", event.Organization, event.Project)
	}
}

type transfersInternalDRSTestFixture struct {
	ObjectService   *objectrecords.Service
	TransferService *domaintransfers.Service
	bucketService   *buckets.Service
	objectStore     *transferObjectStoreFake
}

type transferStorageDependency interface {
	domaintransfers.StoragePort
}

func transfersNewInternalDRSObjectManager(store *transferHTTPFixture, storageDependency transferStorageDependency) transfersInternalDRSTestFixture {
	objectStore := &transferObjectStoreFake{fixture: store}
	bucketStore := &transferBucketStoreFake{fixture: store}
	eventStore := &transferEventStoreFake{fixture: store}
	fileCounters := &transferFileCounterFake{fixture: store}
	bucketService := newInternalDRSBucketService(bucketStore)

	objectService := objectrecords.NewService(objectStore)
	transferService := domaintransfers.NewService(domaintransfers.Dependencies{
		Objects:      objectService,
		Storage:      storageDependency,
		FileCounters: fileCounters,
		Scopes:       bucketService,
		Credentials:  bucketService,
		Events:       eventStore,
	})
	return transfersInternalDRSTestFixture{
		ObjectService:   objectService,
		TransferService: transferService,
		bucketService:   bucketService,
		objectStore:     objectStore,
	}
}

func newInternalDRSBucketService(store *transferBucketStoreFake) *buckets.Service {
	service, err := buckets.NewService(buckets.Dependencies{
		Credentials:     store,
		CredentialAdmin: store,
		Scopes:          store,
		Fallback: func(context.Context) ([]buckets.VisibilityRow, error) {
			return nil, nil
		},
	}, nil)
	if err != nil {
		panic(err)
	}
	return service
}

func (f transfersInternalDRSTestFixture) GetObject(ctx context.Context, id, requiredMethod string) (*objects.Record, error) {
	return f.ObjectService.GetObject(ctx, id, requiredMethod)
}

func (f transfersInternalDRSTestFixture) RegisterObjects(ctx context.Context, records []objects.Record) error {
	_ = ctx
	f.objectStore.registerObjects(records)
	return nil
}

func (f transfersInternalDRSTestFixture) SaveS3Credential(ctx context.Context, credential *buckets.Credential) error {
	return f.bucketService.SaveS3Credential(ctx, credential)
}

func (f transfersInternalDRSTestFixture) CreateBucketScope(ctx context.Context, scope *buckets.Scope) error {
	return f.bucketService.CreateBucketScope(ctx, scope)
}

type captureURLManager struct {
	internalDRSStorageFake
	lastOptions storage.SignRequest
}

func transfersStringPtr(s string) *string { return &s }

func (m *captureURLManager) Sign(ctx context.Context, request storage.SignRequest) (storage.SignedAccess, error) {
	m.lastOptions = request
	return m.internalDRSStorageFake.Sign(ctx, request)
}

func TestHandleInternalDownload(t *testing.T) {
	mockDB := &transferHTTPFixture{
		Objects: map[string]*objects.Record{
			"test-file-id": {
				Id:   "test-file-id",
				Name: transfersStringPtr("sha/LP6008050-DNA_B01__pv.2.0o__rg.grch38__alleleFrequencies_chr17.txt"),
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
	om := transfersNewInternalDRSObjectManager(mockDB, um)
	rr := transfersDoInternalDRSTestRequest(req, om)
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
	om := transfersNewInternalDRSObjectManager(mockDB, &internalDRSStorageFake{})

	t.Run("success", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/data/download/test-file-id/part?start=0&end=1024", nil)
		rr := transfersDoInternalDRSTestRequest(req, om)
		if rr.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", rr.Code)
		}
		if got := rr.Header().Get("Cache-Control"); got != "no-store" {
			t.Fatalf("expected ranged download response to disable caching, got %q", got)
		}
	})
	t.Run("missing parameters", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/data/download/test-file-id/part?start=0", nil)
		rr := transfersDoInternalDRSTestRequest(req, om)
		if rr.Code != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d", rr.Code)
		}
	})
	t.Run("invalid range", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/data/download/test-file-id/part?start=100&end=50", nil)
		rr := transfersDoInternalDRSTestRequest(req, om)
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
	rr := transfersDoInternalDRSTestRequest(req, transfersNewInternalDRSObjectManager(mockDB, &internalDRSStorageFake{}))
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
	rr := transfersDoInternalDRSTestRequest(req, transfersNewInternalDRSObjectManager(mockDB, &internalDRSStorageFake{}))
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
		rr := transfersDoInternalDRSTestRequest(req, transfersNewInternalDRSObjectManager(mockDB, &internalDRSStorageFake{}))
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
	om := transfersNewInternalDRSObjectManager(mockDB, &internalDRSStorageFake{})
	req401 := httptest.NewRequest(http.MethodGet, "/data/download/secure-id", nil)
	req401 = req401.WithContext(transfersDataTestAuthContext(req401.Context(), "gen3", false, nil))
	rr401 := transfersDoInternalDRSTestRequest(req401, om)
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
			om := transfersNewInternalDRSObjectManager(mockDB, &internalDRSStorageFake{})
			req200 := httptest.NewRequest(http.MethodGet, "/data/download/secure-id", nil)
			req200 = transfersWithTestAuthzContext(req200, mode, map[string]map[string]bool{"/programs/p/projects/q": {"read": true}})
			rr200 := transfersDoInternalDRSTestRequest(req200, om)
			if rr200.Code != http.StatusOK {
				t.Fatalf("expected 200, got %d", rr200.Code)
			}
		})
	}
}

func TestHandleInternalMultipartUpload_NotFound(t *testing.T) {
	mockDB := &transferHTTPFixture{}
	mockUM := &internalDRSStorageFake{}
	om := transfersNewInternalDRSObjectManager(mockDB, mockUM)
	reqBody := internalapi.InternalMultipartUploadRequest{
		UploadId:   "non-existent",
		PartNumber: 1,
	}
	body, _ := json.Marshal(reqBody)
	req := httptest.NewRequest("POST", "/data/multipart/upload", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")

	rr := transfersDoInternalDRSTestRequest(req, om)
	if rr.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", rr.Code)
	}
	var responseBody internalapi.APIError
	if err := json.NewDecoder(rr.Body).Decode(&responseBody); err != nil {
		t.Fatalf("decode error response: %v", err)
	}
	if responseBody.Code != errorapi.ErrorCodeMultipartUploadNotFound || responseBody.Message != "Upload ID not found" {
		t.Errorf("unexpected not-found body: %+v", responseBody)
	}
}

func TestHandleInternalMultipartComplete_NotFound(t *testing.T) {
	mockDB := &transferHTTPFixture{}
	mockUM := &internalDRSStorageFake{}
	om := transfersNewInternalDRSObjectManager(mockDB, mockUM)
	reqBody := internalapi.InternalMultipartCompleteRequest{
		UploadId: "non-existent",
		Parts:    []internalapi.InternalMultipartPart{},
	}
	body, _ := json.Marshal(reqBody)
	req := httptest.NewRequest("POST", "/data/multipart/complete", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")

	rr := transfersDoInternalDRSTestRequest(req, om)
	if rr.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", rr.Code)
	}
	var responseBody internalapi.APIError
	if err := json.NewDecoder(rr.Body).Decode(&responseBody); err != nil {
		t.Fatalf("decode error response: %v", err)
	}
	if responseBody.Code != errorapi.ErrorCodeMultipartUploadNotFound || responseBody.Message != "Upload ID not found" {
		t.Errorf("unexpected not-found body: %+v", responseBody)
	}
}

func TestHandleInternalMultipartCompletePreservesPartOrderAndOpaqueETags(t *testing.T) {
	fake := &internalDRSStorageFake{}
	om := transfersNewInternalDRSObjectManager(&transferHTTPFixture{}, fake)
	target := storage.Target{PhysicalBucket: "bucket-a", Key: "path/object.bin"}
	upload, err := om.TransferService.BeginMultipart(t.Context(), domaintransfers.MultipartInitRequest{Target: &target})
	uploadID := upload.UploadID
	if err != nil || uploadID != "mock-upload-id" {
		t.Fatalf("begin multipart upload = (%q, %v)", uploadID, err)
	}

	body, _ := json.Marshal(internalapi.InternalMultipartCompleteRequest{
		UploadId: uploadID,
		Parts: []internalapi.InternalMultipartPart{
			{PartNumber: 7, ETag: `"opaque-seven"`},
			{PartNumber: 2, ETag: `"opaque-two"`},
		},
	})
	rr := transfersDoInternalDRSTestRequest(httptest.NewRequest(http.MethodPost, "/data/multipart/complete", bytes.NewBuffer(body)), om)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	if len(fake.completeParts) != 2 || fake.completeParts[0].PartNumber != 7 || fake.completeParts[0].ETag != `"opaque-seven"` || fake.completeParts[1].PartNumber != 2 || fake.completeParts[1].ETag != `"opaque-two"` {
		t.Fatalf("completed parts were not preserved in caller order: %+v", fake.completeParts)
	}
}

func TestHandleInternalMultipartCompleteRetainsSessionAfterProviderError(t *testing.T) {
	fake := &internalDRSStorageFake{completeErr: errors.New("provider completion failed")}
	om := transfersNewInternalDRSObjectManager(&transferHTTPFixture{}, fake)
	target := storage.Target{PhysicalBucket: "bucket-a", Key: "path/object.bin"}
	upload, err := om.TransferService.BeginMultipart(t.Context(), domaintransfers.MultipartInitRequest{Target: &target})
	uploadID := upload.UploadID
	if err != nil || uploadID != "mock-upload-id" {
		t.Fatalf("begin multipart upload = (%q, %v)", uploadID, err)
	}

	body, _ := json.Marshal(internalapi.InternalMultipartCompleteRequest{UploadId: uploadID})
	rr := transfersDoInternalDRSTestRequest(httptest.NewRequest(http.MethodPost, "/data/multipart/complete", bytes.NewBuffer(body)), om)
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("expected provider failure to map to 500, got %d", rr.Code)
	}
	fake.completeErr = nil
	rr = transfersDoInternalDRSTestRequest(httptest.NewRequest(http.MethodPost, "/data/multipart/complete", bytes.NewBuffer(body)), om)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected successful retry after provider recovery, got %d", rr.Code)
	}
	if err := om.TransferService.CompleteMultipart(t.Context(), uploadID, nil); !errors.Is(err, errorapi.ErrMultipartUploadNotFound) {
		t.Fatalf("expected consumed upload ID after successful completion, got %v", err)
	}
}

func TestHandleInternalUploadMatrix(t *testing.T) {
	cases := []struct {
		name string
		test func(*testing.T)
	}{
		{"TestHandleInternalUploadBlank", uploadCaseBlank},
		{"TestHandleInternalUploadBlank_ResolvesOrganizationProjectScope", uploadCaseBlankResolvesOrganizationProjectScope},
		{"TestHandleInternalUploadBlank_RequiresScope", uploadCaseBlankRequiresScope},
		{"TestHandleInternalUploadURL_MissingObjectResolvesOrganizationProjectScope", uploadCaseURLMissingObjectResolvesOrganizationProjectScope},
		{"TestHandleInternalMultipartInit", uploadCaseMultipartInit},
		{"TestHandleInternalMultipartInit_RequiresScopeForNewUpload", uploadCaseMultipartInitRequiresScopeForNewUpload},
		{"TestHandleInternalMultipartInit_ResolvesOrganizationProjectScope", uploadCaseMultipartInitResolvesOrganizationProjectScope},
		{"TestHandleInternalMultipartInit_PreservesRequestedKey", uploadCaseMultipartInitPreservesRequestedKey},
		{"TestHandleInternalMultipartInit_MintsUUIDForChecksumInput", uploadCaseMultipartInitMintsUUIDForChecksumInput},
		{"TestHandleInternalMultipartInit_ResolvesExistingByChecksumGUID", uploadCaseMultipartInitResolvesExistingByChecksumGUID},
		{"TestHandleInternalMultipartInit_ExistingScopedObjectUsesMappedLocation", uploadCaseMultipartInitExistingScopedObjectUsesMappedLocation},
		{"TestHandleInternalMultipartUpload", uploadCaseMultipartUpload},
		{"TestHandleInternalMultipartComplete", uploadCaseMultipartComplete},
		{"TestHandleInternalUploadURL_Gen3Unauthorized", uploadCaseURLGen3Unauthorized},
		{"TestHandleInternalUploadURL_Branches", uploadCaseURLBranches},
		{"TestHandleInternalUploadURL_MissingObjectRequiresScope", uploadCaseURLMissingObjectRequiresScope},
		{"TestHandleInternalUploadURL_RewritesScopedObjectURL", uploadCaseURLRewritesScopedObjectURL},
		{"TestHandleInternalUploadURL_ResolvesRegisteredScopedObjectID", uploadCaseURLResolvesRegisteredScopedObjectID},
		{"TestHandleInternalUploadURL_ResolvesRegisteredProjectScopedObjectWithoutQueryHints", uploadCaseURLResolvesRegisteredProjectScopedObjectWithoutQueryHints},
		{"TestHandleInternalUploadURL_RepairsMalformedScopedObjectURL", uploadCaseURLRepairsMalformedScopedObjectURL},
		{"TestHandleInternalUploadURL_UsesScopedPathForMalformedObjectURL", uploadCaseURLUsesScopedPathForMalformedObjectURL},
		{"TestHandleInternalUploadURL_UsesExplicitObjectKeyForExistingObject", uploadCaseURLUsesExplicitObjectKeyForExistingObject},
		{"TestHandleInternalUploadURL_ExplicitScopeOverridesMalformedExistingObjectURL", uploadCaseURLExplicitScopeOverridesMalformedExistingObjectURL},
		{"TestHandleInternalUploadURL_ExplicitScopeIgnoresConflictingObjectMetadata", uploadCaseURLExplicitScopeIgnoresConflictingObjectMetadata},
		{"TestHandleInternalUploadURL_RejectsMalformedUnscopedObjectURL", uploadCaseURLRejectsMalformedUnscopedObjectURL},
		{"TestHandleInternalUploadBulk_MixedResults", uploadCaseBulkMixedResults},
		{"TestHandleInternalUploadBulk_Gen3UnauthorizedPerItem", uploadCaseBulkGen3UnauthorizedPerItem},
		{"TestHandleInternalMultipartValidationErrors", uploadCaseMultipartValidationErrors},
	}
	for _, tc := range cases {
		t.Run(tc.name, tc.test)
	}
}

func uploadCaseBlank(t *testing.T) {
	guid := "new-guid"
	org := "syfon"
	project := "e2e"
	body, _ := json.Marshal(internalapi.InternalUploadBlankRequest{Guid: &guid, Organization: &org, Project: &project})
	rr := transfersDoInternalDRSTestRequest(httptest.NewRequest(http.MethodPost, "/data/upload", bytes.NewBuffer(body)), transfersNewInternalDRSObjectManager(&transferHTTPFixture{
		Objects:     map[string]*objects.Record{},
		Credentials: map[string]buckets.Credential{"b1": {Bucket: "b1"}},
		BucketScopes: map[string]buckets.Scope{
			"syfon|e2e": {Organization: "syfon", ProjectID: "e2e", Bucket: "b1"},
		},
	}, &internalDRSStorageFake{}))
	if rr.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d", rr.Code)
	}
	var resp internalapi.InternalUploadBlankOutput
	_ = json.NewDecoder(rr.Body).Decode(&resp)
	if _, err := uuid.Parse(stringValue(resp.Guid)); err != nil {
		t.Fatalf("expected minted UUID, got %q", stringValue(resp.Guid))
	}
}

func uploadCaseBlankResolvesOrganizationProjectScope(t *testing.T) {
	guid := "00000000-0000-4000-8000-000000000001"
	org := "syfon"
	project := "e2e"
	body, _ := json.Marshal(internalapi.InternalUploadBlankRequest{Guid: &guid, Organization: &org, Project: &project})
	mockUM := &internalDRSStorageFake{}
	rr := transfersDoInternalDRSTestRequest(httptest.NewRequest(http.MethodPost, "/data/upload", bytes.NewBuffer(body)), transfersNewInternalDRSObjectManager(&transferHTTPFixture{
		Objects:     map[string]*objects.Record{},
		Credentials: map[string]buckets.Credential{"b1": {Bucket: "b1"}},
		BucketScopes: map[string]buckets.Scope{
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

func uploadCaseBlankRequiresScope(t *testing.T) {
	guid := "new-guid"
	body, _ := json.Marshal(internalapi.InternalUploadBlankRequest{Guid: &guid})
	rr := transfersDoInternalDRSTestRequest(httptest.NewRequest(http.MethodPost, "/data/upload", bytes.NewBuffer(body)), transfersNewInternalDRSObjectManager(&transferHTTPFixture{Objects: map[string]*objects.Record{}}, &internalDRSStorageFake{}))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rr.Code)
	}
}

func uploadCaseURLMissingObjectResolvesOrganizationProjectScope(t *testing.T) {
	mockUM := &internalDRSStorageFake{}
	req := httptest.NewRequest(http.MethodGet, "/data/upload/new-guid?organization=syfon&project=e2e&key=payload.bin", nil)
	rr := transfersDoInternalDRSTestRequest(req, transfersNewInternalDRSObjectManager(&transferHTTPFixture{
		Objects:     map[string]*objects.Record{},
		Credentials: map[string]buckets.Credential{"b1": {Bucket: "b1"}},
		BucketScopes: map[string]buckets.Scope{
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

func uploadCaseMultipartInit(t *testing.T) {
	fileName := "test.bam"
	guid := "multipart-guid"
	org := "syfon"
	project := "e2e"
	body, _ := json.Marshal(internalapi.InternalMultipartInitRequest{Guid: &guid, Key: &fileName, Organization: &org, Project: &project})
	rr := transfersDoInternalDRSTestRequest(httptest.NewRequest(http.MethodPost, "/data/multipart/init", bytes.NewBuffer(body)), transfersNewInternalDRSObjectManager(&transferHTTPFixture{
		Objects:     map[string]*objects.Record{},
		Credentials: map[string]buckets.Credential{"b1": {Bucket: "b1"}},
		BucketScopes: map[string]buckets.Scope{
			"syfon|e2e": {Organization: "syfon", ProjectID: "e2e", Bucket: "b1"},
		},
	}, &internalDRSStorageFake{}))
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
}

func uploadCaseMultipartInitRequiresScopeForNewUpload(t *testing.T) {
	fileName := "test.bam"
	guid := "multipart-guid"
	body, _ := json.Marshal(internalapi.InternalMultipartInitRequest{Guid: &guid, Key: &fileName})
	rr := transfersDoInternalDRSTestRequest(httptest.NewRequest(http.MethodPost, "/data/multipart/init", bytes.NewBuffer(body)), transfersNewInternalDRSObjectManager(&transferHTTPFixture{Objects: map[string]*objects.Record{}}, &internalDRSStorageFake{}))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rr.Code)
	}
}

func uploadCaseMultipartInitResolvesOrganizationProjectScope(t *testing.T) {
	key := "multipart/new.bin"
	org := "syfon"
	project := "e2e"
	body, _ := json.Marshal(internalapi.InternalMultipartInitRequest{Guid: &key, Organization: &org, Project: &project})
	mockUM := &internalDRSStorageFake{}
	rr := transfersDoInternalDRSTestRequest(httptest.NewRequest(http.MethodPost, "/data/multipart/init", bytes.NewBuffer(body)), transfersNewInternalDRSObjectManager(&transferHTTPFixture{
		Objects:     map[string]*objects.Record{},
		Credentials: map[string]buckets.Credential{"b1": {Bucket: "b1"}},
		BucketScopes: map[string]buckets.Scope{
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

func uploadCaseMultipartInitPreservesRequestedKey(t *testing.T) {
	key := "programs/programs/projects/e2e/sha256-value"
	org := "syfon"
	project := "e2e"
	body, _ := json.Marshal(internalapi.InternalMultipartInitRequest{Guid: &key, Organization: &org, Project: &project})
	mockUM := &internalDRSStorageFake{}
	rr := transfersDoInternalDRSTestRequest(httptest.NewRequest(http.MethodPost, "/data/multipart/init", bytes.NewBuffer(body)), transfersNewInternalDRSObjectManager(&transferHTTPFixture{
		Objects:     map[string]*objects.Record{},
		Credentials: map[string]buckets.Credential{"b1": {Bucket: "b1"}},
		BucketScopes: map[string]buckets.Scope{
			"syfon|e2e": {Organization: "syfon", ProjectID: "e2e", Bucket: "b1"},
		},
	}, mockUM))
	if rr.Code != http.StatusOK || mockUM.key != key {
		t.Fatalf("expected preserved key, got status=%d key=%q", rr.Code, mockUM.key)
	}
}

func uploadCaseMultipartInitMintsUUIDForChecksumInput(t *testing.T) {
	checksum := strings.Repeat("a", 64)
	body, _ := json.Marshal(internalapi.InternalMultipartInitRequest{Key: &checksum})
	mockDB := &transferHTTPFixture{Objects: map[string]*objects.Record{}}
	rr := transfersDoInternalDRSTestRequest(httptest.NewRequest(http.MethodPost, "/data/multipart/init", bytes.NewBuffer(body)), transfersNewInternalDRSObjectManager(mockDB, &internalDRSStorageFake{}))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rr.Code)
	}
}

func uploadCaseMultipartInitResolvesExistingByChecksumGUID(t *testing.T) {
	checksum := strings.Repeat("b", 64)
	existingID := "ee53f5ce-8069-4f99-bd59-0517e6a2f1ea"
	mockDB := &transferHTTPFixture{
		Objects: map[string]*objects.Record{
			existingID: {
				Id:        objects.RecordID(existingID),
				Checksums: []objects.Checksum{{Type: "sha256", Checksum: checksum}},
				AccessMethods: &[]objects.AccessMethod{{
					Type:      "s3",
					AccessUrl: &objects.AccessURL{Url: "s3://b1/" + existingID},
				}},
			},
		},
	}
	body, _ := json.Marshal(internalapi.InternalMultipartInitRequest{Guid: &checksum})
	rr := transfersDoInternalDRSTestRequest(httptest.NewRequest(http.MethodPost, "/data/multipart/init", bytes.NewBuffer(body)), transfersNewInternalDRSObjectManager(mockDB, &internalDRSStorageFake{}))
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
}

func uploadCaseMultipartInitExistingScopedObjectUsesMappedLocation(t *testing.T) {
	checksum := strings.Repeat("c", 64)
	existingID := "ee53f5ce-8069-4f99-bd59-0517e6a2f1ea"
	mockDB := &transferHTTPFixture{
		Objects: map[string]*objects.Record{
			existingID: {
				Id:               objects.RecordID(existingID),
				ControlledAccess: &[]string{"/organization/HTAN_INT/project/BForePC"},
				Checksums:        []objects.Checksum{{Type: "sha256", Checksum: checksum}},
				AccessMethods: &[]objects.AccessMethod{{
					Type:      "s3",
					AccessUrl: &objects.AccessURL{Url: "s3://bforepc-prod/OHSU/slide.ome.tiff"},
				}},
			},
		},
		Credentials: map[string]buckets.Credential{
			"bforepc": {Bucket: "bforepc", Provider: "s3", Region: "us-west-2"},
		},
		BucketScopes: map[string]buckets.Scope{
			"HTAN_INT|BForePC": {
				Organization: "HTAN_INT",
				ProjectID:    "BForePC",
				Bucket:       "bforepc",
				PathPrefix:   "bforepc-prod",
			},
		},
	}
	mockUM := &internalDRSStorageFake{}
	body, _ := json.Marshal(internalapi.InternalMultipartInitRequest{Guid: &checksum})
	rr := transfersDoInternalDRSTestRequest(httptest.NewRequest(http.MethodPost, "/data/multipart/init", bytes.NewBuffer(body)), transfersNewInternalDRSObjectManager(mockDB, mockUM))
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rr.Code, rr.Body.String())
	}
	if mockUM.bucket != "bforepc" || mockUM.key != "bforepc-prod/"+checksum {
		t.Fatalf("expected mapped multipart target bforepc/bforepc-prod/%s, got %q/%q", checksum, mockUM.bucket, mockUM.key)
	}
}

func uploadCaseMultipartUpload(t *testing.T) {
	fake := &internalDRSStorageFake{}
	om := transfersNewInternalDRSObjectManager(&transferHTTPFixture{Objects: map[string]*objects.Record{}}, fake)
	target := storage.Target{PhysicalBucket: "bucket", Key: "key"}
	if _, err := om.TransferService.BeginMultipart(context.Background(), domaintransfers.MultipartInitRequest{Target: &target}); err != nil {
		t.Fatalf("begin multipart upload: %v", err)
	}
	body, _ := json.Marshal(internalapi.InternalMultipartUploadRequest{Key: "hash-key", UploadId: "mock-upload-id", PartNumber: 1})
	rr := transfersDoInternalDRSTestRequest(httptest.NewRequest(http.MethodPost, "/data/multipart/upload", bytes.NewBuffer(body)), om)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
}

func uploadCaseMultipartComplete(t *testing.T) {
	fake := &internalDRSStorageFake{}
	om := transfersNewInternalDRSObjectManager(&transferHTTPFixture{Objects: map[string]*objects.Record{}}, fake)
	target := storage.Target{PhysicalBucket: "bucket", Key: "key"}
	if _, err := om.TransferService.BeginMultipart(context.Background(), domaintransfers.MultipartInitRequest{Target: &target}); err != nil {
		t.Fatalf("begin multipart upload: %v", err)
	}
	body, _ := json.Marshal(internalapi.InternalMultipartCompleteRequest{Key: "hash-key", UploadId: "mock-upload-id", Parts: []internalapi.InternalMultipartPart{{PartNumber: 1, ETag: "etag1"}}})
	rr := transfersDoInternalDRSTestRequest(httptest.NewRequest(http.MethodPost, "/data/multipart/complete", bytes.NewBuffer(body)), om)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
}

func uploadCaseURLGen3Unauthorized(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/data/upload/some-id?organization=syfon&project=e2e", nil)
	req = req.WithContext(transfersDataTestAuthContext(req.Context(), "gen3", false, nil))
	rr := transfersDoInternalDRSTestRequest(req, transfersNewInternalDRSObjectManager(&transferHTTPFixture{Objects: map[string]*objects.Record{}}, &internalDRSStorageFake{}))
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rr.Code)
	}
}

func uploadCaseURLBranches(t *testing.T) {
	db := &transferHTTPFixture{
		Objects:     map[string]*objects.Record{},
		Credentials: map[string]buckets.Credential{"b1": {Bucket: "b1"}},
		BucketScopes: map[string]buckets.Scope{
			"syfon|e2e": {Organization: "syfon", ProjectID: "e2e", Bucket: "b1"},
		},
	}
	req := httptest.NewRequest(http.MethodGet, "/data/upload/abc?organization=syfon&project=e2e&filename=f1", nil)
	rr := transfersDoInternalDRSTestRequest(req, transfersNewInternalDRSObjectManager(db, &internalDRSStorageFake{}))
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), "upload=true") {
		t.Fatalf("expected signed upload URL, got status=%d body=%s", rr.Code, rr.Body.String())
	}
}

func uploadCaseURLMissingObjectRequiresScope(t *testing.T) {
	db := &transferHTTPFixture{Objects: map[string]*objects.Record{}, Credentials: map[string]buckets.Credential{"b1": {Bucket: "b1"}}}
	req := httptest.NewRequest(http.MethodGet, "/data/upload/abc", nil)
	rr := transfersDoInternalDRSTestRequest(req, transfersNewInternalDRSObjectManager(db, &internalDRSStorageFake{}))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%s", rr.Code, rr.Body.String())
	}
}

func uploadCaseURLRewritesScopedObjectURL(t *testing.T) {
	db := &transferHTTPFixture{
		Objects: map[string]*objects.Record{
			"scoped-obj": {
				Id:               "scoped-obj",
				ControlledAccess: &[]string{"/organization/HTAN_INT/project/BForePC"},
				AccessMethods: &[]objects.AccessMethod{{
					Type:      "s3",
					AccessUrl: &objects.AccessURL{Url: "s3://bforepc-prod/OHSU/slide.ome.tiff"},
				}},
			},
		},
		Credentials: map[string]buckets.Credential{
			"bforepc": {Bucket: "bforepc", Provider: "s3", Region: "us-west-2"},
		},
		BucketScopes: map[string]buckets.Scope{
			"HTAN_INT|BForePC": {
				Organization: "HTAN_INT",
				ProjectID:    "BForePC",
				Bucket:       "bforepc",
				PathPrefix:   "bforepc-prod",
			},
		},
	}
	mockUM := &internalDRSStorageFake{}
	req := httptest.NewRequest(http.MethodGet, "/data/upload/scoped-obj", nil)
	rr := transfersDoInternalDRSTestRequest(req, transfersNewInternalDRSObjectManager(db, mockUM))
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

func uploadCaseURLResolvesRegisteredScopedObjectID(t *testing.T) {
	ctx := t.Context()
	database := &transferHTTPFixture{}
	om := transfersNewInternalDRSObjectManager(database, &internalDRSStorageFake{})
	if err := om.SaveS3Credential(ctx, &buckets.Credential{Bucket: "syfon-e2e-bucket", Provider: "s3", Region: "us-east-1"}); err != nil {
		t.Fatalf("SaveS3Credential failed: %v", err)
	}
	if err := om.CreateBucketScope(ctx, &buckets.Scope{
		Organization: "syfon",
		ProjectID:    "",
		Bucket:       "syfon-e2e-bucket",
		PathPrefix:   "program-root",
	}); err != nil {
		t.Fatalf("CreateBucketScope failed: %v", err)
	}

	oid := "3d71f043937a09b77826109db4f2b47c46f19923ef823f6a777a15fde0b2c9c7"
	name := "program-root.bin"
	obj, err := objects.CandidateToRecord(httpdrs.FromGeneratedCandidate(drs.DrsObjectCandidate{
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
	}), time.Now().UTC())
	if err != nil {
		t.Fatalf("CandidateToRecord failed: %v", err)
	}
	if err := om.RegisterObjects(ctx, []objects.Record{obj}); err != nil {
		t.Fatalf("RegisterObjects failed: %v", err)
	}
	registered, err := om.GetObject(ctx, string(obj.Id), "read")
	if err != nil {
		t.Fatalf("GetObject failed: %v", err)
	}

	mockUM := &internalDRSStorageFake{}
	om = transfersNewInternalDRSObjectManager(database, mockUM)
	req := httptest.NewRequest(http.MethodGet, "/data/upload/"+string(registered.Id)+"?key=program-root/"+oid, nil)
	rr := transfersDoInternalDRSTestRequest(req, om)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rr.Code, rr.Body.String())
	}
	if want := "s3://syfon-e2e-bucket/program-root/" + oid; mockUM.signURL != want {
		t.Fatalf("signed URL target = %q, want %q", mockUM.signURL, want)
	}
}

func uploadCaseURLResolvesRegisteredProjectScopedObjectWithoutQueryHints(t *testing.T) {
	ctx := t.Context()
	database := &transferHTTPFixture{}
	om := transfersNewInternalDRSObjectManager(database, &internalDRSStorageFake{})
	if err := om.SaveS3Credential(ctx, &buckets.Credential{Bucket: "syfon-e2e-bucket", Provider: "s3", Region: "us-east-1"}); err != nil {
		t.Fatalf("SaveS3Credential failed: %v", err)
	}
	if err := om.CreateBucketScope(ctx, &buckets.Scope{
		Organization: "syfon",
		Bucket:       "syfon-e2e-bucket",
		PathPrefix:   "program-root",
	}); err != nil {
		t.Fatalf("CreateBucketScope(org) failed: %v", err)
	}
	if err := om.CreateBucketScope(ctx, &buckets.Scope{
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
	obj, err := objects.CandidateToRecord(httpdrs.FromGeneratedCandidate(drs.DrsObjectCandidate{
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
	}), time.Now().UTC())
	if err != nil {
		t.Fatalf("CandidateToRecord failed: %v", err)
	}
	if err := om.RegisterObjects(ctx, []objects.Record{obj}); err != nil {
		t.Fatalf("RegisterObjects failed: %v", err)
	}

	mockUM := &internalDRSStorageFake{}
	req := httptest.NewRequest(http.MethodGet, "/data/upload/"+did, nil)
	rr := transfersDoInternalDRSTestRequest(req, transfersNewInternalDRSObjectManager(database, mockUM))
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

func uploadCaseURLRepairsMalformedScopedObjectURL(t *testing.T) {
	db := &transferHTTPFixture{
		Objects: map[string]*objects.Record{
			"scoped-obj": {
				Id: "scoped-obj",
				Checksums: []objects.Checksum{{
					Type:     "sha256",
					Checksum: "412f8568bfb0e62937ee40c6fcdeaa1cf55910c558c0152250340356c8829a47",
				}},
				ControlledAccess: &[]string{"/organization/syfon/project/e2e"},
				AccessMethods: &[]objects.AccessMethod{{
					Type: "s3",
					AccessUrl: &objects.AccessURL{

						Url: "s3://objects/f781273b-52eb-5ac2-a484-775235eef303"},
				}},
			},
		},
		Credentials: map[string]buckets.Credential{
			"syfon-e2e-bucket": {Bucket: "syfon-e2e-bucket", Provider: "s3", Region: "us-west-2"},
		},
		BucketScopes: map[string]buckets.Scope{
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
	mockUM := &internalDRSStorageFake{}
	req := httptest.NewRequest(http.MethodGet, "/data/upload/scoped-obj", nil)
	rr := transfersDoInternalDRSTestRequest(req, transfersNewInternalDRSObjectManager(db, mockUM))
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

func uploadCaseURLUsesScopedPathForMalformedObjectURL(t *testing.T) {
	db := &transferHTTPFixture{
		Objects: map[string]*objects.Record{
			"scoped-obj": {
				Id: "scoped-obj",
				Checksums: []objects.Checksum{{
					Type:     "sha256",
					Checksum: "3d71f043937a09b77826109db4f2b47c46f19923ef823f6a777a15fde0b2c9c7",
				}},
				ControlledAccess: &[]string{"/organization/syfon/project/e2e"},
				AccessMethods: &[]objects.AccessMethod{{
					Type: "s3",
					AccessUrl: &objects.AccessURL{

						Url: "s3://7b9de5b9-19b2-536f-abcc-fe2a146c4eb5"},
				}},
			},
		},
		Credentials: map[string]buckets.Credential{
			"syfon-e2e-bucket": {Bucket: "syfon-e2e-bucket", Provider: "s3", Region: "us-west-2"},
		},
		BucketScopes: map[string]buckets.Scope{
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
	mockUM := &internalDRSStorageFake{}
	req := httptest.NewRequest(http.MethodGet, "/data/upload/scoped-obj", nil)
	rr := transfersDoInternalDRSTestRequest(req, transfersNewInternalDRSObjectManager(db, mockUM))
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

func uploadCaseURLUsesExplicitObjectKeyForExistingObject(t *testing.T) {
	const checksum = "3d71f043937a09b77826109db4f2b47c46f19923ef823f6a777a15fde0b2c9c7"
	db := &transferHTTPFixture{
		Objects: map[string]*objects.Record{
			"7b9de5b9-19b2-536f-abcc-fe2a146c4eb5": {
				Id: "7b9de5b9-19b2-536f-abcc-fe2a146c4eb5",
				Checksums: []objects.Checksum{{
					Type:     "sha256",
					Checksum: checksum,
				}},
				ControlledAccess: &[]string{"/organization/syfon/project/e2e"},
				AccessMethods: &[]objects.AccessMethod{{
					Type: "s3",
					AccessUrl: &objects.AccessURL{

						Url: "s3://7b9de5b9-19b2-536f-abcc-fe2a146c4eb5"},
				}},
			},
		},
		Credentials: map[string]buckets.Credential{
			"syfon-e2e-bucket": {Bucket: "syfon-e2e-bucket", Provider: "s3", Region: "us-west-2"},
		},
		BucketScopes: map[string]buckets.Scope{
			"syfon|": {
				Organization: "syfon",
				Bucket:       "syfon-e2e-bucket",
				PathPrefix:   "program-root",
			},
		},
	}
	mockUM := &internalDRSStorageFake{}
	req := httptest.NewRequest(http.MethodGet, "/data/upload/7b9de5b9-19b2-536f-abcc-fe2a146c4eb5?key=program-root/"+checksum, nil)
	rr := transfersDoInternalDRSTestRequest(req, transfersNewInternalDRSObjectManager(db, mockUM))
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

func uploadCaseURLExplicitScopeOverridesMalformedExistingObjectURL(t *testing.T) {
	const checksum = "412f8568bfb0e62937ee40c6fcdeaa1cf55910c558c0152250340356c8829a47"
	db := &transferHTTPFixture{
		Objects: map[string]*objects.Record{
			"f781273b-52eb-5ac2-a484-775235eef303": {
				Id: "f781273b-52eb-5ac2-a484-775235eef303",
				Checksums: []objects.Checksum{{
					Type:     "sha256",
					Checksum: checksum,
				}},
				AccessMethods: &[]objects.AccessMethod{{
					Type: "s3",
					AccessUrl: &objects.AccessURL{

						Url: "s3://f781273b-52eb-5ac2-a484-775235eef303"},
				}},
			},
		},
		Credentials: map[string]buckets.Credential{
			"syfon-e2e-bucket": {Bucket: "syfon-e2e-bucket", Provider: "s3", Region: "us-west-2"},
		},
		BucketScopes: map[string]buckets.Scope{
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
	mockUM := &internalDRSStorageFake{}
	req := httptest.NewRequest(http.MethodGet, "/data/upload/f781273b-52eb-5ac2-a484-775235eef303?organization=syfon&project=e2e&key=project-subpath/"+checksum, nil)
	rr := transfersDoInternalDRSTestRequest(req, transfersNewInternalDRSObjectManager(db, mockUM))
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

func uploadCaseURLExplicitScopeIgnoresConflictingObjectMetadata(t *testing.T) {
	const checksum = "45be10b3fe5163b6f11155fb46027878d23e3dc99d525d7079180b9dd9b832e9"
	db := &transferHTTPFixture{
		Objects: map[string]*objects.Record{
			"4f74e0c2-3c80-5c19-b47c-061b300ae270": {
				Id:               "4f74e0c2-3c80-5c19-b47c-061b300ae270",
				ControlledAccess: &[]string{"/organization/other/project/wrong"},
				Checksums: []objects.Checksum{{
					Type:     "sha256",
					Checksum: checksum,
				}},
				AccessMethods: &[]objects.AccessMethod{{
					Type: "s3",
					AccessUrl: &objects.AccessURL{

						Url: "s3://objects/4f74e0c2-3c80-5c19-b47c-061b300ae270"},
				}},
			},
		},
		Credentials: map[string]buckets.Credential{
			"syfon-e2e-bucket": {Bucket: "syfon-e2e-bucket", Provider: "s3", Region: "us-west-2"},
		},
		BucketScopes: map[string]buckets.Scope{
			"syfon|e2e": {
				Organization: "syfon",
				ProjectID:    "e2e",
				Bucket:       "syfon-e2e-bucket",
			},
		},
	}
	mockUM := &internalDRSStorageFake{}
	req := httptest.NewRequest(http.MethodGet, "/data/upload/4f74e0c2-3c80-5c19-b47c-061b300ae270?organization=syfon&project=e2e&key="+checksum, nil)
	rr := transfersDoInternalDRSTestRequest(req, transfersNewInternalDRSObjectManager(db, mockUM))
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

func uploadCaseURLRejectsMalformedUnscopedObjectURL(t *testing.T) {
	const checksum = "412f8568bfb0e62937ee40c6fcdeaa1cf55910c558c0152250340356c8829a47"
	db := &transferHTTPFixture{
		Objects: map[string]*objects.Record{
			"f781273b-52eb-5ac2-a484-775235eef303": {
				Id: "f781273b-52eb-5ac2-a484-775235eef303",
				Checksums: []objects.Checksum{{
					Type:     "sha256",
					Checksum: checksum,
				}},
				AccessMethods: &[]objects.AccessMethod{{
					Type: "s3",
					AccessUrl: &objects.AccessURL{

						Url: "s3://f781273b-52eb-5ac2-a484-775235eef303"},
				}},
			},
		},
		Credentials: map[string]buckets.Credential{
			"syfon-e2e-bucket": {Bucket: "syfon-e2e-bucket", Provider: "s3", Region: "us-west-2"},
		},
	}
	mockUM := &internalDRSStorageFake{}
	req := httptest.NewRequest(http.MethodGet, "/data/upload/f781273b-52eb-5ac2-a484-775235eef303", nil)
	rr := transfersDoInternalDRSTestRequest(req, transfersNewInternalDRSObjectManager(db, mockUM))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%s", rr.Code, rr.Body.String())
	}
}

func uploadCaseBulkMixedResults(t *testing.T) {
	db := &transferHTTPFixture{
		Objects: map[string]*objects.Record{"obj-1": {Id: "obj-1", AccessMethods: &[]objects.AccessMethod{{Type: "s3", AccessUrl: &objects.AccessURL{
			Url: "s3://b1/prefix/from-existing.bin"}}}}},
		Credentials: map[string]buckets.Credential{"b1": {Bucket: "b1", Provider: "s3", Region: "us-east-1"}},
	}
	body, _ := json.Marshal(internalapi.InternalUploadBulkRequest{Requests: []internalapi.InternalUploadBulkItem{{FileId: "obj-1"}, {FileId: ""}}})
	rr := transfersDoInternalDRSTestRequest(httptest.NewRequest(http.MethodPost, "/data/upload/bulk", bytes.NewBuffer(body)), transfersNewInternalDRSObjectManager(db, &internalDRSStorageFake{}))
	if rr.Code != http.StatusMultiStatus {
		t.Fatalf("expected 207, got %d", rr.Code)
	}
}

func uploadCaseBulkGen3UnauthorizedPerItem(t *testing.T) {
	db := &transferHTTPFixture{
		Objects:     map[string]*objects.Record{"secure-id": {Id: "secure-id"}},
		ObjectAuthz: map[string]map[string][]string{"secure-id": {"p": {"q"}}},
	}
	body, _ := json.Marshal(internalapi.InternalUploadBulkRequest{Requests: []internalapi.InternalUploadBulkItem{{FileId: "secure-id"}}})
	req := httptest.NewRequest(http.MethodPost, "/data/upload/bulk", bytes.NewBuffer(body))
	req = req.WithContext(transfersDataTestAuthContext(req.Context(), "gen3", false, nil))
	rr := transfersDoInternalDRSTestRequest(req, transfersNewInternalDRSObjectManager(db, &internalDRSStorageFake{}))
	if rr.Code != http.StatusMultiStatus {
		t.Fatalf("expected 207, got %d", rr.Code)
	}
}

func uploadCaseMultipartValidationErrors(t *testing.T) {
	om := transfersNewInternalDRSObjectManager(&transferHTTPFixture{Objects: map[string]*objects.Record{}}, &internalDRSStorageFake{})
	rrUpload := transfersDoInternalDRSTestRequest(httptest.NewRequest(http.MethodPost, "/data/multipart/upload", strings.NewReader(`{}`)), om)
	if rrUpload.Code != http.StatusBadRequest {
		t.Fatalf("expected upload 400, got %d", rrUpload.Code)
	}
	rrComplete := transfersDoInternalDRSTestRequest(httptest.NewRequest(http.MethodPost, "/data/multipart/complete", strings.NewReader(`{}`)), om)
	if rrComplete.Code != http.StatusBadRequest {
		t.Fatalf("expected complete 400, got %d", rrComplete.Code)
	}
}

type failedUploadReader struct{ *store.Store }

func (failedUploadReader) GetObject(context.Context, string) (*objects.Record, error) {
	return nil, errors.New("database lookup failed: QA_PRIVATE_PROVIDER_DETAIL")
}

func (failedUploadReader) GetBulkObjects(context.Context, []string) ([]objects.Record, error) {
	return nil, errors.New("database lookup failed: QA_PRIVATE_PROVIDER_DETAIL")
}

func TestBulkUploadRedactsServerCause(t *testing.T) {
	service := objectrecords.NewService(failedUploadReader{})
	rr := transfersDoInternalDRSTestRequest(httptest.NewRequest(http.MethodPost, "/data/upload/bulk", strings.NewReader(`{"requests":[{"file_id":"record-id"}]}`)), transfersInternalDRSTestFixture{ObjectService: service})
	body := rr.Body.Bytes()
	if rr.Code != 207 {
		t.Fatalf("expected partial success, got %d body=%s", rr.Code, body)
	}
	var output internalapi.InternalUploadBulkOutput
	if err := json.Unmarshal(body, &output); err != nil {
		t.Fatal(err)
	}
	if output.Results == nil || len(*output.Results) != 1 || (*output.Results)[0].Error == nil || *(*output.Results)[0].Error == "" {
		t.Fatalf("expected one failed result, got %+v", output.Results)
	}
	if strings.Contains(*(*output.Results)[0].Error, "QA_PRIVATE_PROVIDER_DETAIL") {
		t.Fatal("internal provider detail leaked into partial-success response")
	}
}

type bulkProviderReader struct {
	*store.Store
	objects map[string]*objects.Record
	errID   string
	err     error
}

func (r *bulkProviderReader) GetObject(_ context.Context, id string) (*objects.Record, error) {
	if id == r.errID {
		return nil, r.err
	}
	obj, ok := r.objects[id]
	if !ok {
		return nil, errors.New("object missing")
	}
	return obj, nil
}

func (r *bulkProviderReader) GetBulkObjects(_ context.Context, ids []string) ([]objects.Record, error) {
	out := make([]objects.Record, 0, len(ids))
	for _, id := range ids {
		obj, err := r.GetObject(context.Background(), id)
		if err != nil {
			return nil, err
		}
		out = append(out, *obj)
	}
	return out, nil
}

type bulkScopeFailure struct{ err error }

func (s bulkScopeFailure) LookupBucketScope(context.Context, string, string) (buckets.Scope, bool, error) {
	return buckets.Scope{}, false, s.err
}

type bulkAccessFailure struct {
	failID string
	err    error
}

func (a bulkAccessFailure) Sign(_ context.Context, request storage.SignRequest) (storage.SignedAccess, error) {
	if strings.Contains(request.Target.OriginalURL, "/"+a.failID) {
		return storage.SignedAccess{}, a.err
	}
	return storage.SignedAccess{Location: request.Target.OriginalURL + "?signed=true"}, nil
}

func (a bulkAccessFailure) BeginMultipart(context.Context, storage.Target) (storage.UploadID, error) {
	return "", nil
}
func (a bulkAccessFailure) SignMultipartPart(context.Context, storage.MultipartPartRequest) (storage.SignedAccess, error) {
	return storage.SignedAccess{}, nil
}
func (a bulkAccessFailure) CompleteMultipart(context.Context, storage.CompleteMultipartRequest) error {
	return nil
}

type bulkEventFailure struct {
	failID string
	err    error
}

func (e bulkEventFailure) RecordTransferAttributionEvents(_ context.Context, events []usage.Event) error {
	for _, event := range events {
		if event.ObjectID == e.failID {
			return e.err
		}
	}
	return nil
}

func bulkUploadRecord(id string, scoped bool) *objects.Record {
	obj := &objects.Record{
		Id: objects.RecordID(id),
		AccessMethods: &[]objects.AccessMethod{{
			Type:      "s3",
			AccessUrl: &objects.AccessURL{Url: "s3://bucket/" + id},
		}},
	}
	if scoped {
		controlled := []string{"/organization/org/project/project"}
		obj.ControlledAccess = &controlled
	}
	return obj
}

func TestBulkUploadProviderFailuresRedactCauseAndKeepSuccess(t *testing.T) {
	providerError := func(capability string) error {
		return &storage.OperationError{
			Kind:       storage.ErrorInvalid,
			Provider:   "s3",
			Capability: capability,
			Cause:      errors.New("QA_PRIVATE_PROVIDER_DETAIL"),
		}
	}
	tests := []struct {
		name       string
		failure    string
		readerErr  error
		scopeErr   error
		accessErr  error
		eventErr   error
		scopedFail bool
	}{
		{name: "lookup", failure: "lookup", readerErr: providerError("lookup")},
		{name: "target", failure: "target", scopeErr: providerError("target"), scopedFail: true},
		{name: "sign", failure: "sign", accessErr: providerError("sign")},
		{name: "record", failure: "record", eventErr: providerError("record")},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			const failID = "fail"
			reader := &bulkProviderReader{objects: map[string]*objects.Record{
				failID:    bulkUploadRecord(failID, tc.scopedFail),
				"success": bulkUploadRecord("success", false),
			}, errID: "", err: tc.readerErr}
			if tc.readerErr != nil {
				reader.errID = failID
			}
			var scopes domaintransfers.ScopeReader
			if tc.scopeErr != nil {
				scopes = bulkScopeFailure{err: tc.scopeErr}
			}
			var access domaintransfers.StoragePort
			if tc.accessErr != nil {
				access = bulkAccessFailure{failID: failID, err: tc.accessErr}
			} else {
				access = bulkAccessFailure{failID: "never", err: errors.New("unused")}
			}
			var events domaintransfers.EventRecorder
			if tc.eventErr != nil {
				events = bulkEventFailure{failID: failID, err: tc.eventErr}
			} else {
				events = bulkEventFailure{failID: "never", err: errors.New("unused")}
			}
			objectService := objectrecords.NewService(reader)
			transferService := domaintransfers.NewService(domaintransfers.Dependencies{Objects: objectService, Storage: access, Scopes: scopes, Events: events})
			body := strings.NewReader(`{"requests":[{"file_id":"` + failID + `"},{"file_id":"success"}]}`)
			rr := transfersDoInternalDRSTestRequest(httptest.NewRequest(http.MethodPost, "/data/upload/bulk", body), transfersInternalDRSTestFixture{ObjectService: objectService, TransferService: transferService})
			var output internalapi.InternalUploadBulkOutput
			if err := json.NewDecoder(rr.Body).Decode(&output); err != nil {
				t.Fatal(err)
			}
			if rr.Code != 207 || output.Results == nil || len(*output.Results) != 2 {
				t.Fatalf("unexpected bulk response: status=%d output=%+v", rr.Code, output)
			}
			failed, succeeded := (*output.Results)[0], (*output.Results)[1]
			if failed.Error == nil || *failed.Error != "storage request is invalid" || strings.Contains(*failed.Error, "QA_PRIVATE_PROVIDER_DETAIL") || failed.Status != 400 {
				t.Fatalf("unsafe failed result: %+v", failed)
			}
			if succeeded.Error != nil || succeeded.Url == nil || succeeded.Status != 200 {
				t.Fatalf("successful sibling was not retained: %+v", succeeded)
			}
		})
	}
}
