package services

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/calypr/syfon/apigen/drs"
	internalapi "github.com/calypr/syfon/apigen/internalapi"

	clientaccess "github.com/calypr/syfon/client/access"
	"github.com/calypr/syfon/client/apierror"
)

func TestIndexServiceOperationsAndUpsert(t *testing.T) {
	t.Parallel()

	var (
		lastListQuery        url.Values
		lastDeleteQuery      url.Values
		lastCreated          internalapi.InternalRecord
		lastUpdated          internalapi.InternalRecord
		lastRemoveControlled internalapi.ControlledAccessRemoveRequest
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
		case r.Method == http.MethodPost && r.URL.Path == "/index":
			if err := json.NewDecoder(r.Body).Decode(&lastCreated); err != nil {
				t.Fatalf("Decode create body returned error: %v", err)
			}
			writeJSON(t, w, http.StatusCreated, toRecordResponse(lastCreated))
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
		case r.Method == http.MethodDelete && r.URL.Path == "/index":
			lastDeleteQuery = r.URL.Query()
			deleted := 3
			writeJSON(t, w, http.StatusOK, internalapi.DeleteByQueryResponse{Deleted: &deleted})
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

	service := NewIndexService(mustInternalClient(t, server.URL))
	ctx := context.Background()

	if got, err := service.Get(ctx, "did-update"); err != nil || got.Did != "did-update" {
		t.Fatalf("Get returned got=%+v err=%v", got, err)
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
	if deleted, err := service.DeleteByQuery(ctx, DeleteByQueryOptions{Organization: "org", ProjectID: "project", Hash: "abc", HashType: "sha256"}); err != nil || deleted.Deleted == nil || *deleted.Deleted != 3 {
		t.Fatalf("DeleteByQuery returned response=%+v err=%v", deleted, err)
	}
	if lastDeleteQuery.Get("organization") != "org" || lastDeleteQuery.Get("project") != "project" || lastDeleteQuery.Get("hash") != "abc" || lastDeleteQuery.Get("hash_type") != "sha256" {
		t.Fatalf("unexpected delete query: %v", lastDeleteQuery)
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
	if lastListQuery.Get("hash") != "sha" || lastListQuery.Get("url") != "s3://bucket/path" || lastListQuery.Get("organization") != "org" || lastListQuery.Get("project") != "proj" || lastListQuery.Get("limit") != "3" || lastListQuery.Get("start") != "did-100" || lastListQuery.Get("page") != "" {
		t.Fatalf("unexpected list query values: %v", lastListQuery)
	}

	if _, err := service.List(ctx, ListRecordsOptions{Limit: 3, Start: "did-100", Page: 2}); err != nil {
		t.Fatalf("List with start and page returned error: %v", err)
	}
	if lastListQuery.Get("start") != "did-100" || lastListQuery.Get("page") != "" {
		t.Fatalf("expected start to take priority over page, got %v", lastListQuery)
	}

	err := service.Upsert(ctx, "did-update", "s3://bucket/new", "new.txt", 123, "sha256sum", nil)
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

	service := NewIndexService(mustInternalClient(t, server.URL))
	_, err := service.RemoveControlledAccess(context.Background(), "did-ca", "/organization/org/project/proj")
	var apiErr *apierror.APIError
	if !errors.As(err, &apiErr) || apiErr.Status != http.StatusNoContent {
		t.Fatalf("expected 204 failure, got %v", err)
	}
}

func testRecordForURL(did, rawURL string, authorizations map[string][]string) internalapi.InternalRecord {
	controlled := clientaccess.AuthzMapToControlledAccess(authorizations)
	methodType := methodTypeForURL(rawURL)
	methods := []drs.AccessMethod{{
		Type:      drs.AccessMethodType(methodType),
		AccessId:  &methodType,
		AccessUrl: &drs.AccessURL{Url: rawURL},
	}}
	return internalapi.InternalRecord{
		Did:              did,
		ControlledAccess: &controlled,
		AccessMethods:    &methods,
	}
}

func TestUpsertCreatesOnlyAfterNotFound(t *testing.T) {
	for _, status := range []int{http.StatusNotFound, http.StatusUnauthorized, http.StatusForbidden, http.StatusInternalServerError, http.StatusServiceUnavailable} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			creates := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				if req.Method == http.MethodGet {
					w.WriteHeader(status)
					return
				}
				creates++
				writeJSON(t, w, http.StatusCreated, internalapi.InternalRecordResponse{Did: "id"})
			}))
			defer server.Close()
			service := NewIndexService(mustInternalClient(t, server.URL))
			err := service.Upsert(context.Background(), "id", "s3://bucket/key", "key", 1, "", map[string][]string{"org": {"project"}})
			if status == http.StatusNotFound {
				if err != nil || creates != 1 {
					t.Fatalf("not-found upsert: creates=%d err=%v", creates, err)
				}
			} else if err == nil || creates != 0 {
				t.Fatalf("failed lookup created a record: creates=%d err=%v", creates, err)
			}
		})
	}
}

func TestUpsertReturnsLookupTransportError(t *testing.T) {
	t.Parallel()
	lookupErr := errors.New("lookup disconnected")
	creates := 0
	httpClient := &http.Client{Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		if req.Method != http.MethodGet {
			creates++
		}
		return nil, lookupErr
	})}
	gen, err := internalapi.NewClientWithResponses("http://example.test", internalapi.WithHTTPClient(httpClient))
	if err != nil {
		t.Fatal(err)
	}
	err = NewIndexService(gen).Upsert(context.Background(), "id", "s3://bucket/key", "key", 1, "", map[string][]string{"org": {"project"}})
	if !errors.Is(err, lookupErr) || creates != 0 {
		t.Fatalf("lookup failure lost: err=%v creates=%d", err, creates)
	}
}
