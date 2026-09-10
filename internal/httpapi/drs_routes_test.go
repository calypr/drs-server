package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/calypr/syfon/apigen/drs"
	generated "github.com/calypr/syfon/apigen/drs"
	"github.com/calypr/syfon/internal/objects"
	"github.com/calypr/syfon/internal/storage"
	"github.com/gofiber/fiber/v3"
)

type drsCaptureStorageAccess struct {
	lastOptions storage.SignRequest
	lastURL     string
}

func TestGeneratedChecksumNilAndEmptySlicesRemainDistinct(t *testing.T) {
	nilValue := drs.DrsObject{}
	if nilValue.Checksums != nil {
		t.Fatalf("nil domain checksums became empty generated slice: %#v", nilValue.Checksums)
	}
	emptyValue := drs.DrsObject{Checksums: []drs.Checksum{}}
	if emptyValue.Checksums == nil || len(emptyValue.Checksums) != 0 {
		t.Fatalf("empty domain checksums changed: %#v", emptyValue.Checksums)
	}
}

func TestObjectPayloadIncludesLegacyIdentityAliases(t *testing.T) {
	name := "sample"
	aliases := []string{"sample.alias"}
	created := time.Date(2026, time.January, 2, 3, 4, 5, 0, time.UTC)
	record := drs.DrsObject{
		Id: "record-1", Did: valuePointer("record-1"), Name: &name, NameAliases: &aliases, CreatedTime: created,
	}
	data, err := json.Marshal(record)
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
}

func TestObjectPayloadInvalidTimeFallsBackToIdentity(t *testing.T) {
	record := drs.DrsObject{Id: "record-1", Did: valuePointer("record-1"), SelfUri: "drs://example/record-1", CreatedTime: time.Date(10000, time.January, 1, 0, 0, 0, 0, time.UTC)}
	data, err := json.Marshal(record)
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

func TestGeneratedAccessMethodsPreserveDurableWireShape(t *testing.T) {
	accessID := "access"
	headers := []string{"authorization", "x-test"}
	want := generated.AccessMethod{
		AccessId: &accessID, Type: generated.AccessMethodTypeS3,
		AccessUrl: &generated.AccessURL{Headers: &headers, Url: "s3://bucket/key"},
	}

	got := generated.AccessMethod{AccessId: want.AccessId, Type: want.Type, AccessUrl: want.AccessUrl}
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

	empty := generated.AccessMethod{Type: generated.AccessMethodTypeS3}
	if empty.AccessId != nil || empty.AccessUrl != nil || empty.Type != generated.AccessMethodTypeS3 {
		t.Fatalf("empty optional access fields changed: %#v", empty)
	}
}

func newDRSTestApp(services *testDRSServicesFixture) *fiber.App {
	app := fiber.New()
	registerDRSRoutes(app, services.objectService, services.transferService, generated.Service{})
	return app
}

func TestRegisterObjects(t *testing.T) {
	db := newDRSObjectStore(t, map[string]*drs.DrsObject{})
	om := testDRSServices(db, nil)
	app := newDRSTestApp(om)

	candidate := generated.DrsObjectCandidate{
		Size: 50,
		Checksums: []generated.Checksum{{
			Type: "sha256", Checksum: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		}},
		ControlledAccess: valuePointer([]string{"/organization/org1/project/proj1"}),
		AccessMethods: &[]generated.AccessMethod{{
			Type:      generated.AccessMethodTypeS3,
			AccessUrl: &generated.AccessURL{Url: "s3://bucket/org1/proj1/object"},
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
	db := newDRSObjectStore(t, map[string]*drs.DrsObject{})
	om := testDRSServices(db, nil)
	app := newDRSTestApp(om)

	body, err := json.Marshal(generated.DrsObjectCandidate{
		Size:             100,
		Checksums:        []generated.Checksum{{Type: "sha256", Checksum: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}},
		ControlledAccess: valuePointer([]string{"/organization/org1/project/proj1"}),
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
	accessID := objects.AccessMethodID("s3", "s3://bucket/object-1")
	db := newDRSObjectStore(t, map[string]*drs.DrsObject{
		"object-1": {
			Id:   "object-1",
			Name: valuePointer("test-file"),
			AccessMethods: &[]drs.AccessMethod{{
				Type:      "s3",
				AccessUrl: &drs.AccessURL{Url: "s3://bucket/object-1"},
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
		resp, err := app.Test(httptest.NewRequest(method, "/objects/object-1/access/"+accessID, nil))
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
	accessID := objects.AccessMethodID("s3", "s3://bucket/a")
	db := newDRSObjectStore(t, map[string]*drs.DrsObject{
		"object-1": {
			Id: "object-1",
			AccessMethods: &[]drs.AccessMethod{
				{Type: "s3", AccessUrl: &drs.AccessURL{Url: "s3://bucket/a"}},
			},
		},
	})
	om := testDRSServices(db, &drsCaptureStorageAccess{})
	app := newDRSTestApp(om)

	request := []byte(`{"bulk_object_access_ids":[{"bulk_object_id":"object-1","bulk_access_ids":["` + accessID + `","missing"," ` + accessID + ` "]},{"bulk_object_id":"missing","bulk_access_ids":["` + accessID + `","b"]},{"bulk_object_id":"empty"}]}`)
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
	if len(payload.Resolved) != 2 || *payload.Resolved[0].DrsAccessId != accessID || *payload.Resolved[1].DrsAccessId != accessID {
		t.Fatalf("resolved = %+v", payload.Resolved)
	}
	if len(payload.Unresolved) != 1 || payload.Unresolved[0].ErrorCode != http.StatusNotFound || !reflect.DeepEqual(payload.Unresolved[0].ObjectIDs, []string{"object-1", "missing", "empty"}) {
		t.Fatalf("unresolved = %+v", payload.Unresolved)
	}
}

func TestBulkObjectAndChecksumHandlers(t *testing.T) {
	checksum := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	db := newDRSObjectStore(t, map[string]*drs.DrsObject{
		"object-1": {Id: "object-1", Checksums: []drs.Checksum{{Type: "sha256", Checksum: checksum}}},
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

	for _, path := range []string{"/objects/checksum/" + checksum, "/objects/checksum/%20" + checksum + "%20"} {
		resp, err = app.Test(httptest.NewRequest(http.MethodGet, path, nil))
		if err != nil {
			t.Fatalf("checksum request %q failed: %v", path, err)
		}
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("checksum status for %q = %d, want %d", path, resp.StatusCode, http.StatusOK)
		}
		var byChecksum generated.N200OkDrsObjectsJSONResponse
		if err := json.NewDecoder(resp.Body).Decode(&byChecksum); err != nil {
			t.Fatalf("decode checksum response for %q: %v", path, err)
		}
		if byChecksum.Summary == nil || byChecksum.Summary.Resolved == nil || *byChecksum.Summary.Resolved != 1 {
			t.Fatalf("checksum summary for %q = %+v", path, byChecksum.Summary)
		}
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
		db := newDRSObjectStore(t, map[string]*drs.DrsObject{
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

	for _, request := range []struct {
		path string
		body string
	}{
		{path: "/objects/object-1/delete", body: `{"delete_storage_data":true}`},
		{path: "/objects/delete", body: `{"bulk_object_ids":["object-1"],"delete_storage_data":true}`},
	} {
		db := newDRSObjectStore(t, map[string]*drs.DrsObject{"object-1": {Id: "object-1"}})
		app := newDRSTestApp(testDRSServices(db, nil))
		resp, err := app.Test(httptest.NewRequest(http.MethodPut, request.path, strings.NewReader(request.body)))
		if err != nil {
			t.Fatalf("%s request failed: %v", request.path, err)
		}
		if resp.StatusCode != http.StatusConflict {
			t.Errorf("%s status = %d, want %d", request.path, resp.StatusCode, http.StatusConflict)
		}
		if _, err := db.GetObject(context.Background(), "object-1"); err != nil {
			t.Errorf("%s mutated the catalog: %v", request.path, err)
		}
	}

	db := newDRSObjectStore(t, map[string]*drs.DrsObject{
		"object-1": {Id: "object-1"},
	})
	om := testDRSServices(db, nil)
	app := newDRSTestApp(om)
	body, err := json.Marshal(generated.AccessMethodUpdateRequest{AccessMethods: []generated.AccessMethod{{
		Type:      generated.AccessMethodTypeS3,
		AccessUrl: &generated.AccessURL{Url: "s3://bucket/object-1"},
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
