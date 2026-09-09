package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	generated "github.com/calypr/syfon/apigen/drs"
	"github.com/calypr/syfon/internal/objects"
	"github.com/calypr/syfon/internal/storage"
	"github.com/gofiber/fiber/v3"
)

type drsCaptureStorageAccess struct {
	lastOptions storage.SignRequest
	lastURL     string
}

func TestToGeneratedChecksumNilAndEmptySlicesRemainDistinct(t *testing.T) {
	nilValue := drsToGenerated(objects.Record{})
	if nilValue.Checksums != nil {
		t.Fatalf("nil domain checksums became empty generated slice: %#v", nilValue.Checksums)
	}
	emptyValue := drsToGenerated(objects.Record{Checksums: []objects.Checksum{}})
	if emptyValue.Checksums == nil || len(emptyValue.Checksums) != 0 {
		t.Fatalf("empty domain checksums changed: %#v", emptyValue.Checksums)
	}
}

func TestObjectPayloadUsesTypedNestedResponse(t *testing.T) {
	name := "sample"
	aliases := []string{"sample.alias"}
	created := time.Date(2026, time.January, 2, 3, 4, 5, 0, time.UTC)
	childID := "child"
	record := objects.Record{
		Id: "record-1", Name: &name, NameAliases: aliases, CreatedTime: created,
		Contents: &[]objects.Content{{Id: &childID, Contents: &[]objects.Content{{Name: "leaf"}}}},
	}
	data, err := json.Marshal(drsObjectPayload(record))
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatal(err)
	}
	for key, want := range map[string]string{"id": `"record-1"`, "did": `"record-1"`, "name_aliases": `["sample.alias"]`} {
		if string(payload[key]) != want {
			t.Fatalf("%s = %s, want %s", key, payload[key], want)
		}
	}
	var contents []generated.ContentsObject
	if err := json.Unmarshal(payload["contents"], &contents); err != nil {
		t.Fatal(err)
	}
	if len(contents) != 1 || contents[0].Contents == nil || len(*contents[0].Contents) != 1 {
		t.Fatalf("nested contents = %#v", contents)
	}
}

func TestObjectPayloadInvalidTimeFallsBackToIdentity(t *testing.T) {
	record := objects.Record{Id: "record-1", SelfUri: "drs://example/record-1", CreatedTime: time.Date(10000, time.January, 1, 0, 0, 0, 0, time.UTC)}
	data, err := json.Marshal(drsObjectPayload(record))
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload) != 3 || string(payload["id"]) != `"record-1"` || string(payload["did"]) != `"record-1"` || string(payload["self_uri"]) != `"drs://example/record-1"` {
		t.Fatalf("fallback payload = %s", data)
	}
}

func TestAccessMethodsRoundTripPreservesGeneratedWireShape(t *testing.T) {
	accessID := "access"
	available := true
	cloud := "aws"
	region := "us-east-1"
	headers := []string{"authorization", "x-test"}
	supported := []generated.AccessMethodAuthorizationsSupportedTypes{generated.AccessMethodAuthorizationsSupportedTypesBearerAuth}
	want := generated.AccessMethod{
		AccessId: &accessID, Available: &available, Cloud: &cloud, Region: &region, Type: generated.AccessMethodTypeS3,
		AccessUrl: &struct {
			Headers *[]string `json:"headers,omitempty"`
			Url     string    `json:"url"`
		}{Headers: &headers, Url: "s3://bucket/key"},
		Authorizations: &struct {
			BearerAuthIssuers   *[]string                                             `json:"bearer_auth_issuers,omitempty"`
			DrsObjectId         *string                                               `json:"drs_object_id,omitempty"`
			PassportAuthIssuers *[]string                                             `json:"passport_auth_issuers,omitempty"`
			SupportedTypes      *[]generated.AccessMethodAuthorizationsSupportedTypes `json:"supported_types,omitempty"`
		}{SupportedTypes: &supported},
	}

	got := drsToGeneratedAccessMethod(drsFromGeneratedAccessMethod(want))
	wantJSON, err := json.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	gotJSON, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(wantJSON, gotJSON) {
		t.Fatalf("access method wire shape changed: want %s got %s", wantJSON, gotJSON)
	}
}

func newDRSTestApp(services *testDRSServicesFixture) *fiber.App {
	app := fiber.New()
	registerDRSRoutes(app, services.objectService, services.transferService, generated.Service{})
	return app
}

func TestRegisterObjects(t *testing.T) {
	db := newDRSObjectStore(map[string]*objects.Record{})
	om := testDRSServices(db, nil)
	app := newDRSTestApp(om)

	candidate := generated.DrsObjectCandidate{
		Size: 50,
		Checksums: []generated.Checksum{{
			Type: "sha256", Checksum: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		}},
		ControlledAccess: drsPtr([]string{"/organization/org1/project/proj1"}),
		AccessMethods: &[]generated.AccessMethod{{
			Type: generated.AccessMethodTypeS3,
			AccessUrl: &struct {
				Headers *[]string `json:"headers,omitempty"`
				Url     string    `json:"url"`
			}{Url: "s3://bucket/org1/proj1/object"},
		}},
	}
	body, err := json.Marshal(candidate)
	if err != nil {
		t.Fatalf("marshal candidate: %v", err)
	}
	resp, err := app.Test(httptest.NewRequest(http.MethodPost, "/objects/register", bytes.NewReader(body)))
	if err != nil {
		t.Fatalf("register request failed: %v", err)
	}
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("register status = %d, want %d", resp.StatusCode, http.StatusCreated)
	}
	var created struct {
		Objects []map[string]json.RawMessage `json:"objects"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&created); err != nil {
		t.Fatalf("decode register response: %v", err)
	}
	if len(created.Objects) != 1 || string(created.Objects[0]["id"]) == "" {
		t.Fatalf("unexpected register response: %+v", created)
	}
	if string(created.Objects[0]["did"]) != string(created.Objects[0]["id"]) {
		t.Fatalf("expected id and did in response: %+v", created.Objects[0])
	}
}

func TestRegisterObjectsRejectsMissingAccessMethods(t *testing.T) {
	db := newDRSObjectStore(map[string]*objects.Record{})
	om := testDRSServices(db, nil)
	app := newDRSTestApp(om)

	body, err := json.Marshal(generated.DrsObjectCandidate{
		Size:             100,
		Checksums:        []generated.Checksum{{Type: "sha256", Checksum: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}},
		ControlledAccess: drsPtr([]string{"/organization/org1/project/proj1"}),
	})
	if err != nil {
		t.Fatalf("marshal candidate: %v", err)
	}
	resp, err := app.Test(httptest.NewRequest(http.MethodPost, "/objects/register", bytes.NewReader(body)))
	if err != nil {
		t.Fatalf("register request failed: %v", err)
	}
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("register status = %d, want %d", resp.StatusCode, http.StatusBadRequest)
	}
}

func (m *drsCaptureStorageAccess) Sign(_ context.Context, request storage.SignRequest) (storage.SignedAccess, error) {
	m.lastOptions = request
	m.lastURL = request.Target.OriginalURL
	return storage.SignedAccess{Location: request.Target.OriginalURL + "?signed=true"}, nil
}

func (m *drsCaptureStorageAccess) BeginMultipart(context.Context, storage.Target) (storage.UploadID, error) {
	return "", nil
}
func (m *drsCaptureStorageAccess) SignMultipartPart(context.Context, storage.MultipartPartRequest) (storage.SignedAccess, error) {
	return storage.SignedAccess{}, nil
}
func (m *drsCaptureStorageAccess) CompleteMultipart(context.Context, storage.CompleteMultipartRequest) error {
	return nil
}

func TestGetObjectAndAccessURLAliases(t *testing.T) {
	db := newDRSObjectStore(map[string]*objects.Record{
		"object-1": {
			Id:   "object-1",
			Name: drsPtr("test-file"),
			AccessMethods: &[]objects.AccessMethod{{
				AccessId:  drsPtr("s3-access"),
				Type:      "s3",
				AccessUrl: &objects.AccessURL{Url: "s3://bucket/object-1"},
			}},
		},
	})
	storageAccess := &drsCaptureStorageAccess{}
	om := testDRSServices(db, storageAccess)
	app := newDRSTestApp(om)

	for _, method := range []string{http.MethodGet, http.MethodPost} {
		resp, err := app.Test(httptest.NewRequest(method, "/objects/object-1", nil))
		if err != nil {
			t.Fatalf("%s object request failed: %v", method, err)
		}
		if resp.StatusCode != http.StatusOK {
			t.Errorf("%s object status = %d, want %d", method, resp.StatusCode, http.StatusOK)
		}
		var body map[string]json.RawMessage
		if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
			t.Fatalf("decode %s object response: %v", method, err)
		}
		if string(body["id"]) != `"object-1"` {
			t.Errorf("%s object id = %s", method, body["id"])
		}
	}

	for _, method := range []string{http.MethodGet, http.MethodPost} {
		resp, err := app.Test(httptest.NewRequest(method, "/objects/object-1/access/s3-access", nil))
		if err != nil {
			t.Fatalf("%s access request failed: %v", method, err)
		}
		if resp.StatusCode != http.StatusOK {
			t.Errorf("%s access status = %d, want %d", method, resp.StatusCode, http.StatusOK)
		}
		var body generated.AccessURL
		if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
			t.Fatalf("decode %s access response: %v", method, err)
		}
		if body.Url != "s3://bucket/object-1?signed=true" {
			t.Errorf("%s access URL = %q", method, body.Url)
		}
	}
	if got, want := storageAccess.lastOptions.DownloadFilename, "test-file"; got != want {
		t.Fatalf("download filename = %q, want %q", got, want)
	}
	if got, want := storageAccess.lastURL, "s3://bucket/object-1"; got != want {
		t.Fatalf("storage URL = %q, want %q", got, want)
	}
}

func TestBulkAccessResponsePreservesResolutionContract(t *testing.T) {
	db := newDRSObjectStore(map[string]*objects.Record{
		"object-1": {
			Id: "object-1",
			AccessMethods: &[]objects.AccessMethod{
				{AccessId: drsPtr("a"), Type: "s3", AccessUrl: &objects.AccessURL{Url: "s3://bucket/a"}},
			},
		},
	})
	om := testDRSServices(db, &drsCaptureStorageAccess{})
	app := newDRSTestApp(om)

	request := []byte(`{"bulk_object_access_ids":[{"bulk_object_id":"object-1","bulk_access_ids":["a","missing"," a "]},{"bulk_object_id":"missing","bulk_access_ids":["a","b"]},{"bulk_object_id":"empty"}]}`)
	resp, err := app.Test(httptest.NewRequest(http.MethodPost, "/objects/access", bytes.NewReader(request)))
	if err != nil {
		t.Fatalf("bulk access request failed: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("bulk access status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
	var payload struct {
		Resolved []generated.BulkAccessURL `json:"resolved_drs_object_access_urls"`
		Summary  struct {
			Requested  int `json:"requested"`
			Resolved   int `json:"resolved"`
			Unresolved int `json:"unresolved"`
		} `json:"summary"`
		Unresolved []struct {
			ErrorCode int      `json:"error_code"`
			ObjectIDs []string `json:"object_ids"`
		} `json:"unresolved_drs_objects"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("decode bulk response: %v", err)
	}
	if payload.Summary.Requested != 6 || payload.Summary.Resolved != 2 || payload.Summary.Unresolved != 4 {
		t.Fatalf("summary = %+v", payload.Summary)
	}
	if len(payload.Resolved) != 2 || *payload.Resolved[0].DrsAccessId != "a" || *payload.Resolved[1].DrsAccessId != "a" {
		t.Fatalf("resolved = %+v", payload.Resolved)
	}
	if len(payload.Unresolved) != 1 || payload.Unresolved[0].ErrorCode != http.StatusNotFound || !reflect.DeepEqual(payload.Unresolved[0].ObjectIDs, []string{"object-1", "missing", "empty"}) {
		t.Fatalf("unresolved = %+v", payload.Unresolved)
	}
}

func TestBulkObjectAndChecksumHandlers(t *testing.T) {
	checksum := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	db := newDRSObjectStore(map[string]*objects.Record{
		"object-1": {Id: "object-1", Checksums: []objects.Checksum{{Type: "sha256", Checksum: checksum}}},
	})
	om := testDRSServices(db, nil)
	app := newDRSTestApp(om)

	body, err := json.Marshal(struct {
		BulkObjectIds []string `json:"bulk_object_ids"`
	}{BulkObjectIds: []string{"object-1", "missing"}})
	if err != nil {
		t.Fatalf("marshal bulk request: %v", err)
	}
	resp, err := app.Test(httptest.NewRequest(http.MethodPost, "/objects", bytes.NewReader(body)))
	if err != nil {
		t.Fatalf("bulk request failed: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("bulk status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
	var bulk generated.N200OkDrsObjectsJSONResponse
	if err := json.NewDecoder(resp.Body).Decode(&bulk); err != nil {
		t.Fatalf("decode bulk response: %v", err)
	}
	if bulk.Summary == nil || bulk.Summary.Requested == nil || *bulk.Summary.Requested != 2 {
		t.Fatalf("bulk summary = %+v", bulk.Summary)
	}

	resp, err = app.Test(httptest.NewRequest(http.MethodGet, "/objects/checksum/"+checksum, nil))
	if err != nil {
		t.Fatalf("checksum request failed: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("checksum status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
	var byChecksum generated.N200OkDrsObjectsJSONResponse
	if err := json.NewDecoder(resp.Body).Decode(&byChecksum); err != nil {
		t.Fatalf("decode checksum response: %v", err)
	}
	if byChecksum.Summary == nil || byChecksum.Summary.Resolved == nil || *byChecksum.Summary.Resolved != 1 {
		t.Fatalf("checksum summary = %+v", byChecksum.Summary)
	}
}

func TestDeleteAndAccessMethodRoutes(t *testing.T) {
	for _, methodPath := range []struct {
		method string
		path   string
	}{
		{method: http.MethodPut, path: "/objects/object-1/delete"},
		{method: http.MethodPut, path: "/objects/delete"},
	} {
		db := newDRSObjectStore(map[string]*objects.Record{
			"object-1": {Id: "object-1"},
		})
		om := testDRSServices(db, nil)
		app := newDRSTestApp(om)
		var body []byte
		if methodPath.path == "/objects/delete" {
			body, _ = json.Marshal(generated.BulkDeleteRequest{BulkObjectIds: []string{"object-1"}})
		}
		resp, err := app.Test(httptest.NewRequest(methodPath.method, methodPath.path, bytes.NewReader(body)))
		if err != nil {
			t.Fatalf("%s %s request failed: %v", methodPath.method, methodPath.path, err)
		}
		if resp.StatusCode != http.StatusNoContent {
			t.Errorf("%s %s status = %d, want %d", methodPath.method, methodPath.path, resp.StatusCode, http.StatusNoContent)
		}
	}

	db := newDRSObjectStore(map[string]*objects.Record{
		"object-1": {Id: "object-1"},
	})
	om := testDRSServices(db, nil)
	app := newDRSTestApp(om)
	body, err := json.Marshal(generated.AccessMethodUpdateRequest{AccessMethods: []generated.AccessMethod{{
		Type: generated.AccessMethodTypeS3,
		AccessUrl: &struct {
			Headers *[]string `json:"headers,omitempty"`
			Url     string    `json:"url"`
		}{Url: "s3://bucket/object-1"},
	}}})
	if err != nil {
		t.Fatalf("marshal access method request: %v", err)
	}
	resp, err := app.Test(httptest.NewRequest(http.MethodPut, "/objects/object-1/access-methods", bytes.NewReader(body)))
	if err != nil {
		t.Fatalf("access method request failed: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Errorf("access method status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
}

func TestRegisterDRSRoutesKeepsStaticRoutesBeforeDynamicRoutes(t *testing.T) {
	app := fiber.New()
	registerDRSRoutes(app, nil, nil, generated.Service{})

	var getPaths []string
	for _, routes := range app.Stack() {
		for _, route := range routes {
			if route.Method == http.MethodGet {
				getPaths = append(getPaths, route.Path)
			}
		}
	}
	checksumIndex := drsIndexOf(getPaths, "/objects/checksum/:checksum")
	objectIndex := drsIndexOf(getPaths, "/objects/:object_id")
	if checksumIndex < 0 || objectIndex < 0 || checksumIndex > objectIndex {
		t.Fatalf("GET route order = %v", getPaths)
	}
}

func TestRegisterDRSRoutesRegistersCanonicalRoutesAndOptions(t *testing.T) {
	app := fiber.New()
	registerDRSRoutes(app, nil, nil, generated.Service{})

	want := map[string]bool{
		"POST /objects/register":                     false,
		"POST /objects/access":                       false,
		"PUT /objects/delete":                        false,
		"PUT /objects/access-methods":                false,
		"POST /objects":                              false,
		"GET /objects/:object_id":                    false,
		"POST /objects/:object_id":                   false,
		"PUT /objects/:object_id/delete":             false,
		"GET /objects/:object_id/access/:access_id":  false,
		"POST /objects/:object_id/access/:access_id": false,
		"PUT /objects/:object_id/access-methods":     false,
		"OPTIONS /objects":                           false,
		"OPTIONS /objects/:object_id":                false,
	}
	for _, routes := range app.Stack() {
		for _, route := range routes {
			key := route.Method + " " + route.Path
			if _, ok := want[key]; ok {
				want[key] = true
			}
		}
	}
	for route, found := range want {
		if !found {
			t.Errorf("missing route %s", route)
		}
	}

	for _, route := range []string{
		"POST /objects/delete",
		"POST /objects/access-methods",
		"DELETE /objects/:object_id",
		"POST /objects/:object_id/delete",
		"POST /objects/:object_id/access-methods",
	} {
		for _, routes := range app.Stack() {
			for _, registered := range routes {
				if registered.Method+" "+registered.Path == route {
					t.Errorf("legacy route %s is still registered", route)
				}
			}
		}
	}

	for _, methodPath := range []struct {
		method string
		path   string
	}{
		{method: http.MethodOptions, path: "/objects"},
		{method: http.MethodOptions, path: "/objects/object-1"},
	} {
		resp, err := app.Test(httptest.NewRequest(methodPath.method, methodPath.path, nil))
		if err != nil {
			t.Fatalf("request %s %s failed: %v", methodPath.method, methodPath.path, err)
		}
		if resp.StatusCode != http.StatusNoContent {
			t.Errorf("%s %s status = %d, want %d", methodPath.method, methodPath.path, resp.StatusCode, http.StatusNoContent)
		}
	}
}

func TestUnsupportedChecksumRoutesReturnDRSError(t *testing.T) {
	app := fiber.New()
	registerDRSRoutes(app, nil, nil, generated.Service{})

	for _, path := range []string{"/objects/checksums", "/objects/object-1/checksums"} {
		resp, err := app.Test(httptest.NewRequest(http.MethodPut, path, nil))
		if err != nil {
			t.Fatalf("request %s failed: %v", path, err)
		}
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("%s status = %d, want %d", path, resp.StatusCode, http.StatusNotFound)
		}
		var body generated.Error
		if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
			t.Fatalf("decode %s response: %v", path, err)
		}
		if body.Msg == nil || *body.Msg != "Checksum addition is not supported" {
			t.Errorf("%s body = %+v", path, body)
		}
	}
}

func drsIndexOf(values []string, want string) int {
	for i, value := range values {
		if value == want {
			return i
		}
	}
	return -1
}
