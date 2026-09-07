package services

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"

	"github.com/calypr/syfon/apigen/client/drs"
	internalapi "github.com/calypr/syfon/apigen/client/internalapi"
	syfoncommon "github.com/calypr/syfon/common"
)

func TestIndexServiceOperationsAndUpsert(t *testing.T) {
	t.Parallel()

	var (
		lastListQuery          url.Values
		lastDeleteByQueryQuery url.Values
		lastCreated            internalapi.InternalRecord
		lastUpdated            internalapi.InternalRecord
		lastRemoveControlled   internalapi.ControlledAccessRemoveRequest
		lastBulkCreate         internalapi.BulkCreateRequest
		lastBulkHashes         internalapi.BulkHashesRequest
		lastBulkDelete         internalapi.BulkHashesRequest
		lastBulkValidity       internalapi.BulkSHA256ValidityRequest
	)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/index":
			lastListQuery = r.URL.Query()
			name := "file.txt"
			size := int64(12)
			authz := map[string][]string{"p1": {}}
			rec := testRecordForURL("did-list", "s3://bucket/object", authz)
			rec.Name = &name
			rec.Size = &size
			records := []internalapi.InternalRecord{rec}
			writeJSON(t, w, http.StatusOK, internalapi.ListRecordsResponse{Records: &records})
		case r.Method == http.MethodDelete && r.URL.Path == "/index":
			lastDeleteByQueryQuery = r.URL.Query()
			deleted := 2
			writeJSON(t, w, http.StatusOK, internalapi.DeleteByQueryResponse{Deleted: &deleted})
		case r.Method == http.MethodPost && r.URL.Path == "/index":
			if err := json.NewDecoder(r.Body).Decode(&lastCreated); err != nil {
				t.Fatalf("Decode create body returned error: %v", err)
			}
			writeJSON(t, w, http.StatusCreated, toRecordResponse(lastCreated))
		case r.Method == http.MethodPost && r.URL.Path == "/index/bulk":
			if err := json.NewDecoder(r.Body).Decode(&lastBulkCreate); err != nil {
				t.Fatalf("Decode bulk create body returned error: %v", err)
			}
			records := make([]internalapi.InternalRecord, 0, len(lastBulkCreate.Records))
			for _, rec := range lastBulkCreate.Records {
				records = append(records, rec)
			}
			writeJSON(t, w, http.StatusCreated, internalapi.ListRecordsResponse{Records: &records})
		case r.Method == http.MethodPost && r.URL.Path == "/index/bulk/hashes":
			if err := json.NewDecoder(r.Body).Decode(&lastBulkHashes); err != nil {
				t.Fatalf("Decode bulk hashes body returned error: %v", err)
			}
			writeJSON(t, w, http.StatusOK, internalapi.ListRecordsResponse{})
		case r.Method == http.MethodPost && r.URL.Path == "/index/bulk/delete":
			if err := json.NewDecoder(r.Body).Decode(&lastBulkDelete); err != nil {
				t.Fatalf("Decode bulk delete body returned error: %v", err)
			}
			deleted := len(lastBulkDelete.Hashes)
			writeJSON(t, w, http.StatusOK, internalapi.DeleteByQueryResponse{Deleted: &deleted})
		case r.Method == http.MethodPost && r.URL.Path == "/index/bulk/sha256/validity":
			if err := json.NewDecoder(r.Body).Decode(&lastBulkValidity); err != nil {
				t.Fatalf("Decode bulk validity body returned error: %v", err)
			}
			writeJSON(t, w, http.StatusOK, map[string]bool{"abc": true, "def": false})
		case r.Method == http.MethodPost && r.URL.Path == "/index/bulk/documents":
			authz := map[string][]string{"p1": {}}
			writeJSON(t, w, http.StatusOK, []internalapi.InternalRecordResponse{toRecordResponse(testRecordForURL("did-doc", "s3://bucket/doc", authz))})
		case r.Method == http.MethodGet && r.URL.Path == "/index/did-update":
			fileName := "existing.txt"
			size := int64(42)
			hashes := internalapi.HashInfo{"md5": "md5sum"}
			authz := map[string][]string{"existing": {}}
			rec := toRecordResponse(testRecordForURL("did-update", "s3://bucket/existing", authz))
			rec.Name = &fileName
			rec.Size = &size
			rec.Hashes = &hashes
			writeJSON(t, w, http.StatusOK, internalapi.InternalRecordResponse{
				Did:              rec.Did,
				AccessMethods:    rec.AccessMethods,
				ControlledAccess: rec.ControlledAccess,
				Name:             rec.Name,
				Size:             rec.Size,
				Hashes:           rec.Hashes,
			})
		case r.Method == http.MethodGet && r.URL.Path == "/index/did-no-authz":
			writeJSON(t, w, http.StatusOK, internalapi.InternalRecordResponse{Did: "did-no-authz"})
		case r.Method == http.MethodGet && r.URL.Path == "/index/did-create":
			w.WriteHeader(http.StatusNotFound)
		case r.Method == http.MethodPut && strings.HasPrefix(r.URL.Path, "/index/"):
			if err := json.NewDecoder(r.Body).Decode(&lastUpdated); err != nil {
				t.Fatalf("Decode update body returned error: %v", err)
			}
			writeJSON(t, w, http.StatusOK, toRecordResponse(lastUpdated))
		case r.Method == http.MethodDelete && r.URL.Path == "/index/did-delete":
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodDelete && r.URL.Path == "/index/fail-delete":
			w.WriteHeader(http.StatusTeapot)
		case r.Method == http.MethodPost && r.URL.Path == "/index/did-ca/controlled-access/remove":
			if err := json.NewDecoder(r.Body).Decode(&lastRemoveControlled); err != nil {
				t.Fatalf("Decode remove controlled access body returned error: %v", err)
			}
			resp := toRecordResponse(testRecordForURL("did-ca", "s3://bucket/updated", map[string][]string{"other": {"project"}}))
			writeJSON(t, w, http.StatusOK, resp)
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.String())
		}
	}))
	defer server.Close()

	listName := "file.txt"
	listSize := int64(12)
	listAuthz := map[string][]string{"p1": {}}
	listRec := testRecordForURL("did-list", "s3://bucket/object", listAuthz)
	listRec.Name = &listName
	listRec.Size = &listSize
	listRecords := []internalapi.InternalRecord{listRec}
	listResp, err := json.Marshal(internalapi.ListRecordsResponse{Records: &listRecords})
	if err != nil {
		t.Fatalf("marshal list response: %v", err)
	}
	requester := &fakeRequester{responseJSON: listResp}
	service := NewIndexService(mustInternalClient(t, server.URL), requester)
	ctx := context.Background()

	if got, err := service.Get(ctx, "did-update"); err != nil || got.Did != "did-update" {
		t.Fatalf("Get returned got=%+v err=%v", got, err)
	}
	if _, err := service.GetByHash(ctx, "sha256:abc"); err != nil {
		t.Fatalf("GetByHash returned error: %v", err)
	}
	if lastListQuery.Get("hash") != "sha256:abc" {
		t.Fatalf("expected hash query, got %v", lastListQuery)
	}

	createFile := "created.txt"
	createSize := int64(55)
	createAuthz := map[string][]string{"p1": {}}
	createRec := testRecordForURL("did-new", "s3://bucket/created", createAuthz)
	createRec.Name = &createFile
	createRec.Size = &createSize
	if _, err := service.Create(ctx, createRec); err != nil {
		t.Fatalf("Create returned error: %v", err)
	}
	if lastCreated.Did != "did-new" || lastCreated.Name == nil || *lastCreated.Name != "created.txt" {
		t.Fatalf("unexpected create payload: %+v", lastCreated)
	}

	updateAuthz := map[string][]string{"p2": {}}
	updateRec := testRecordForURL("did-update", "s3://bucket/updated", updateAuthz)
	if _, err := service.Update(ctx, "did-update", updateRec); err != nil {
		t.Fatalf("Update returned error: %v", err)
	}
	if lastUpdated.Did != "did-update" || lastUpdated.AccessMethods == nil || len(*lastUpdated.AccessMethods) != 1 {
		t.Fatalf("unexpected update payload: %+v", lastUpdated)
	}

	if err := service.Delete(ctx, "did-delete"); err != nil {
		t.Fatalf("Delete returned error: %v", err)
	}
	if err := service.Delete(ctx, "fail-delete"); err == nil {
		t.Fatal("expected delete error for non-success status")
	}
	if _, err := service.RemoveControlledAccess(ctx, "did-ca", "/organization/org/project/proj"); err != nil {
		t.Fatalf("RemoveControlledAccess returned error: %v", err)
	}
	if lastRemoveControlled.Resource != "/organization/org/project/proj" {
		t.Fatalf("unexpected remove controlled access payload: %+v", lastRemoveControlled)
	}

	if _, err := service.List(ctx, ListRecordsOptions{Hash: "sha", URL: "s3://bucket/path", Organization: "org", ProjectID: "proj", Limit: 3, Start: "did-100"}); err != nil {
		t.Fatalf("List returned error: %v", err)
	}
	query, err := url.ParseQuery(strings.TrimPrefix(requester.builder.Url, "/index?"))
	if err != nil {
		t.Fatalf("parse list query: %v", err)
	}
	if query.Get("hash") != "sha" || query.Get("url") != "s3://bucket/path" || query.Get("organization") != "org" || query.Get("project") != "proj" || query.Get("limit") != "3" || query.Get("start") != "did-100" || query.Get("page") != "" {
		t.Fatalf("unexpected list query values: %v", query)
	}

	if _, err := service.List(ctx, ListRecordsOptions{Limit: 3, Start: "did-100", Page: 2}); err != nil {
		t.Fatalf("List with start and page returned error: %v", err)
	}
	query, err = url.ParseQuery(strings.TrimPrefix(requester.builder.Url, "/index?"))
	if err != nil {
		t.Fatalf("parse start-priority query: %v", err)
	}
	if query.Get("start") != "did-100" || query.Get("page") != "" {
		t.Fatalf("expected start to take priority over page, got %v", query)
	}

	if _, err := service.DeleteByQuery(ctx, DeleteByQueryOptions{Organization: "org", ProjectID: "proj", Hash: "abc", HashType: "sha256"}); err != nil {
		t.Fatalf("DeleteByQuery returned error: %v", err)
	}
	if lastDeleteByQueryQuery.Get("organization") != "org" || lastDeleteByQueryQuery.Get("project") != "proj" || lastDeleteByQueryQuery.Get("hash") != "abc" || lastDeleteByQueryQuery.Get("hash_type") != "sha256" {
		t.Fatalf("unexpected delete-by-query values: %v", lastDeleteByQueryQuery)
	}

	bulkAuthz := map[string][]string{"p1": {}}
	bulkReq := internalapi.BulkCreateRequest{Records: []internalapi.InternalRecord{testRecordForURL("bulk-1", "s3://bucket/bulk", bulkAuthz)}}
	if _, err := service.CreateBulk(ctx, bulkReq); err != nil {
		t.Fatalf("CreateBulk returned error: %v", err)
	}
	if len(lastBulkCreate.Records) != 1 || lastBulkCreate.Records[0].Did != "bulk-1" {
		t.Fatalf("unexpected bulk create payload: %+v", lastBulkCreate)
	}

	if _, err := service.BulkHashes(ctx, internalapi.BulkHashesRequest{Hashes: []string{"h1", "h2"}}); err != nil {
		t.Fatalf("BulkHashes returned error: %v", err)
	}
	if !reflect.DeepEqual(lastBulkHashes.Hashes, []string{"h1", "h2"}) {
		t.Fatalf("unexpected bulk hashes payload: %+v", lastBulkHashes)
	}

	deleted, err := service.DeleteBulk(ctx, internalapi.BulkHashesRequest{Hashes: []string{"h1", "h2", "h3"}})
	if err != nil || deleted != 3 {
		t.Fatalf("DeleteBulk returned deleted=%d err=%v", deleted, err)
	}

	validity, err := service.BulkSHA256Validity(ctx, internalapi.BulkSHA256ValidityRequest{Sha256: &[]string{"abc", "def"}})
	if err != nil || !validity["abc"] || validity["def"] {
		t.Fatalf("BulkSHA256Validity returned validity=%v err=%v", validity, err)
	}
	if lastBulkValidity.Sha256 == nil || len(*lastBulkValidity.Sha256) != 2 {
		t.Fatalf("unexpected bulk validity payload: %+v", lastBulkValidity)
	}

	docs, err := service.BulkDocuments(ctx, []string{"did-a", "did-b"})
	if err != nil || len(docs) != 1 || docs[0].Did != "did-doc" {
		t.Fatalf("BulkDocuments returned docs=%+v err=%v", docs, err)
	}

	shaValidity, err := service.SHA256Validity(ctx, []string{"abc", "def"})
	if err != nil || !shaValidity["abc"] {
		t.Fatalf("SHA256Validity returned map=%v err=%v", shaValidity, err)
	}

	err = service.Upsert(ctx, "did-update", "s3://bucket/new", "new.txt", 123, "sha256sum", nil)
	if err != nil {
		t.Fatalf("Upsert existing returned error: %v", err)
	}
	if lastUpdated.Name == nil || *lastUpdated.Name != "new.txt" {
		t.Fatalf("expected updated file name, got %+v", lastUpdated)
	}
	if lastUpdated.Size == nil || *lastUpdated.Size != 123 {
		t.Fatalf("expected updated size, got %+v", lastUpdated)
	}
	if lastUpdated.Hashes == nil || (*lastUpdated.Hashes)["sha256"] != "sha256sum" || (*lastUpdated.Hashes)["md5"] != "md5sum" {
		t.Fatalf("expected merged hashes, got %+v", lastUpdated.Hashes)
	}
	if lastUpdated.AccessMethods == nil || len(*lastUpdated.AccessMethods) != 2 {
		t.Fatalf("expected appended access method, got %+v", lastUpdated.AccessMethods)
	}

	err = service.Upsert(ctx, "did-no-authz", "s3://bucket/noauthz", "x.txt", 1, "sha", nil)
	if err == nil || !strings.Contains(err.Error(), "authorizations are required") {
		t.Fatalf("expected missing authz update error, got %v", err)
	}

	newAuthz := map[string][]string{"new": {}}
	err = service.Upsert(ctx, "did-create", "s3://bucket/create", "created.txt", 99, "sha256create", newAuthz)
	if err != nil {
		t.Fatalf("Upsert create returned error: %v", err)
	}
	if lastCreated.Did != "did-create" || lastCreated.ControlledAccess == nil || len(*lastCreated.ControlledAccess) != 1 || lastCreated.AccessMethods == nil || len(*lastCreated.AccessMethods) != 1 {
		t.Fatalf("unexpected create-on-upsert payload: %+v", lastCreated)
	}
	if lastCreated.Name == nil || *lastCreated.Name != "created.txt" || lastCreated.Size == nil || *lastCreated.Size != 99 {
		t.Fatalf("unexpected create-on-upsert sizing: %+v", lastCreated)
	}

	err = service.Upsert(ctx, "did-create", "s3://bucket/create", "created.txt", 99, "sha256create", nil)
	if err == nil || !strings.Contains(err.Error(), "authorizations are required to create") {
		t.Fatalf("expected missing authz create error, got %v", err)
	}
}

func TestIndexServiceMissingSHA256(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/index/bulk/sha256/missing" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		var req internalapi.BulkMissingSHA256Request
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if req.Organization != "org" || req.Project != "project" || len(req.Sha256) != 2 {
			t.Fatalf("unexpected request body: %+v", req)
		}
		writeJSON(t, w, http.StatusOK, internalapi.BulkMissingSHA256Response{
			Checked:       2,
			MissingSha256: []string{"missing"},
		})
	}))
	defer server.Close()

	service := NewIndexService(mustInternalClient(t, server.URL), &fakeRequester{})
	got, err := service.MissingSHA256(context.Background(), internalapi.BulkMissingSHA256Request{
		Organization: "org",
		Project:      "project",
		Sha256:       []string{"present", "missing"},
	})
	if err != nil {
		t.Fatalf("MissingSHA256 returned error: %v", err)
	}
	if got.Checked != 2 || len(got.MissingSha256) != 1 || got.MissingSha256[0] != "missing" {
		t.Fatalf("unexpected response: %+v", got)
	}
}

func TestIndexServiceRemoveControlledAccessRequiresJSON200(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/index/did-ca/controlled-access/remove" {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		t.Fatalf("unexpected request: %s %s", r.Method, r.URL.String())
	}))
	defer server.Close()

	service := NewIndexService(mustInternalClient(t, server.URL), &fakeRequester{})
	_, err := service.RemoveControlledAccess(context.Background(), "did-ca", "/organization/org/project/proj")
	if err == nil || !strings.Contains(err.Error(), "failed to remove controlled access: 204") {
		t.Fatalf("expected 204 failure, got %v", err)
	}
}

func testRecordForURL(did, rawURL string, authorizations map[string][]string) internalapi.InternalRecord {
	controlled := syfoncommon.AuthzMapToControlledAccess(authorizations)
	methodType := methodTypeForURL(rawURL)
	methods := []drs.AccessMethod{{
		Type:     drs.AccessMethodType(methodType),
		AccessId: &methodType,
		AccessUrl: &struct {
			Headers *[]string `json:"headers,omitempty"`
			Url     string    `json:"url"`
		}{Url: rawURL},
	}}
	return internalapi.InternalRecord{
		Did:              did,
		ControlledAccess: &controlled,
		AccessMethods:    &methods,
	}
}
