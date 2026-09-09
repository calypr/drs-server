package drs

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	generated "github.com/calypr/syfon/apigen/drs"
	"github.com/calypr/syfon/internal/objects"
	"github.com/calypr/syfon/internal/storage"
)

type captureStorageAccess struct {
	lastOptions storage.SignRequest
	lastURL     string
}

func (m *captureStorageAccess) Sign(_ context.Context, request storage.SignRequest) (storage.SignedAccess, error) {
	m.lastOptions = request
	m.lastURL = request.Target.OriginalURL
	return storage.SignedAccess{Location: request.Target.OriginalURL + "?signed=true"}, nil
}

func (m *captureStorageAccess) BeginMultipart(context.Context, storage.Target) (storage.UploadID, error) {
	return "", nil
}
func (m *captureStorageAccess) SignMultipartPart(context.Context, storage.MultipartPartRequest) (storage.SignedAccess, error) {
	return storage.SignedAccess{}, nil
}
func (m *captureStorageAccess) CompleteMultipart(context.Context, storage.CompleteMultipartRequest) error {
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
	storageAccess := &captureStorageAccess{}
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
	om := testDRSServices(db, &captureStorageAccess{})
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
