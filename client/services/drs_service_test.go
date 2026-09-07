package services

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	drsapi "github.com/calypr/syfon/apigen/drs"
	"github.com/calypr/syfon/apigen/errorapi"
	internalapi "github.com/calypr/syfon/apigen/internalapi"
	"github.com/calypr/syfon/client/apierror"
)

func TestDRSServiceResolveAndList(t *testing.T) {
	t.Parallel()

	var deleteRequest drsapi.DeleteRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/objects/obj-1":
			name := "object.bin"
			accessID := "acc-1"
			accessMethods := []drsapi.AccessMethod{{
				AccessId: &accessID,
				AccessUrl: &struct {
					Headers *[]string `json:"headers,omitempty"`
					Url     string    `json:"url"`
				}{Url: "https://signed.example/object.bin"},
				Type: drsapi.AccessMethodType("https"),
			}}
			writeJSON(t, w, http.StatusOK, drsapi.DrsObject{Id: "obj-1", Name: &name, Size: 99, Checksums: []drsapi.Checksum{{Type: "sha256", Checksum: "abc"}}, CreatedTime: time.Now(), AccessMethods: &accessMethods})
		case r.Method == http.MethodGet && r.URL.Path == "/objects/no-access":
			writeJSON(t, w, http.StatusOK, drsapi.DrsObject{Id: "no-access", Checksums: []drsapi.Checksum{{Type: "sha256", Checksum: "abc"}}, CreatedTime: time.Now()})
		case r.Method == http.MethodGet && r.URL.Path == "/objects/missing":
			http.Error(w, "missing", http.StatusNotFound)
		case r.Method == http.MethodGet && r.URL.Path == "/objects/broken":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = io.WriteString(w, `{"error":{"message":"schema is stale"}}`)
		case r.Method == http.MethodGet && r.URL.Path == "/objects/obj-1/access/acc-1":
			writeJSON(t, w, http.StatusOK, drsapi.AccessURL{Url: "https://signed.example/access"})
		case r.Method == http.MethodGet && r.URL.Path == "/objects/checksum/abc":
			name := "hash.bin"
			resolved := []drsapi.DrsObject{{
				Id:        "did-hash",
				Name:      &name,
				Size:      12,
				Checksums: []drsapi.Checksum{{Type: "sha256", Checksum: "abc"}},
			}}
			writeJSON(t, w, http.StatusOK, drsapi.N200OkDrsObjects{ResolvedDrsObject: &resolved})
		case r.Method == http.MethodGet && r.URL.Path == "/objects/checksum/def":
			resolved := []drsapi.DrsObject{}
			writeJSON(t, w, http.StatusOK, drsapi.N200OkDrsObjects{ResolvedDrsObject: &resolved})
		case r.Method == http.MethodGet && r.URL.Path == "/objects/checksum/fail":
			http.Error(w, "backend unavailable", http.StatusBadGateway)
		case r.Method == http.MethodPost && r.URL.Path == "/objects/register":
			name := "registered.bin"
			writeJSON(t, w, http.StatusCreated, drsapi.N201ObjectsCreated{Objects: []drsapi.DrsObject{{Id: "obj-created", Name: &name, Checksums: []drsapi.Checksum{{Type: "sha256", Checksum: "abc"}}, CreatedTime: time.Now()}}})
		case r.Method == http.MethodPut && r.URL.Path == "/objects/obj-1/access-methods":
			var body drsapi.AccessMethodUpdateRequest
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatalf("decode access method update body: %v", err)
			}
			name := "object.bin"
			writeJSON(t, w, http.StatusOK, drsapi.DrsObject{Id: "obj-1", Name: &name, Size: 99, Checksums: []drsapi.Checksum{{Type: "sha256", Checksum: "abc"}}, CreatedTime: time.Now(), AccessMethods: &body.AccessMethods})
		case r.Method == http.MethodPut && r.URL.Path == "/objects/obj-delete/delete":
			if err := json.NewDecoder(r.Body).Decode(&deleteRequest); err != nil {
				t.Fatalf("decode delete body: %v", err)
			}
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodPut && r.URL.Path == "/objects/obj-delete-fail/delete":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = io.WriteString(w, `{"error":{"message":"delete failed because schema is stale"}}`)
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.String())
		}
	}))
	defer server.Close()

	recordName := "record.bin"
	recordSize := int64(77)
	recordHashes := internalapi.HashInfo{"sha256": "abc123"}
	recordAuthz := map[string][]string{"org1": {"p1"}}
	record := testRecordForURL("did-record", "https://storage.example/record.bin", recordAuthz)
	record.Name = &recordName
	record.Size = &recordSize
	record.Hashes = &recordHashes
	records := []internalapi.InternalRecord{record}
	listResp, err := json.Marshal(internalapi.ListRecordsResponse{Records: &records})
	if err != nil {
		t.Fatalf("marshal list response: %v", err)
	}
	requester := &fakeRequester{responseJSON: listResp}
	index := NewIndexService(mustInternalClient(t, server.URL), requester)
	service := NewDRSService(mustDRSClient(t, server.URL), index)
	ctx := context.Background()

	resolved, err := service.Resolve(ctx, "obj-1")
	if err != nil {
		t.Fatalf("Resolve returned error: %v", err)
	}
	if resolved.Id != "obj-1" || resolved.Name != "object.bin" || resolved.ProviderURL != "https://signed.example/object.bin" || resolved.AccessMethod != "https" {
		t.Fatalf("unexpected resolved object: %+v", resolved)
	}

	if _, err := service.Resolve(ctx, "no-access"); err == nil || !strings.Contains(err.Error(), "no access methods found") {
		t.Fatalf("expected missing access methods error, got %v", err)
	}

	obj, err := service.GetObject(ctx, "obj-1")
	if err != nil || obj.Id != "obj-1" {
		t.Fatalf("GetObject returned obj=%+v err=%v", obj, err)
	}
	if _, err := service.GetObject(ctx, "missing"); !errors.Is(err, errorapi.ErrNotFound) {
		t.Fatalf("expected errorapi.ErrNotFound, got %v", err)
	}
	if _, err := service.GetObject(ctx, "broken"); err == nil || !strings.Contains(err.Error(), "schema is stale") {
		t.Fatalf("expected server error detail for broken object lookup, got %v", err)
	}

	page, err := service.ListObjects(ctx, 5, 2)
	if err != nil || len(page.DrsObjects) != 1 || page.DrsObjects[0].Id != "did-record" {
		t.Fatalf("ListObjects returned page=%+v err=%v", page, err)
	}
	query, err := url.ParseQuery(strings.TrimPrefix(requester.builder.Url, "/index?"))
	if err != nil {
		t.Fatalf("parse list query: %v", err)
	}
	if query.Get("limit") != "5" || query.Get("page") != "2" {
		t.Fatalf("unexpected list query values: %v", query)
	}
	page, err = service.ListObjectsAfter(ctx, 7, "did-record")
	if err != nil || len(page.DrsObjects) != 1 || page.DrsObjects[0].Id != "did-record" {
		t.Fatalf("ListObjectsAfter returned page=%+v err=%v", page, err)
	}
	query, err = url.ParseQuery(strings.TrimPrefix(requester.builder.Url, "/index?"))
	if err != nil {
		t.Fatalf("parse cursor list query: %v", err)
	}
	if query.Get("limit") != "7" || query.Get("start") != "did-record" || query.Get("page") != "" {
		t.Fatalf("unexpected cursor list query values: %v", query)
	}
	if page.DrsObjects[0].AccessMethods == nil || len(*page.DrsObjects[0].AccessMethods) != 1 {
		t.Fatalf("expected mapped access methods, got %+v", page.DrsObjects[0])
	}
	method := (*page.DrsObjects[0].AccessMethods)[0]
	if method.Authorizations != nil {
		t.Fatalf("did not expect per-method authz mapping, got %+v", method)
	}
	if page.DrsObjects[0].ControlledAccess == nil || len(*page.DrsObjects[0].ControlledAccess) == 0 {
		t.Fatalf("expected controlled access mapping, got %+v", page.DrsObjects[0])
	}

	projectPage, err := service.ListObjectsByProject(ctx, "proj-1", 10, 3)
	if err != nil || len(projectPage.DrsObjects) != 1 {
		t.Fatalf("ListObjectsByProject returned page=%+v err=%v", projectPage, err)
	}
	query, err = url.ParseQuery(strings.TrimPrefix(requester.builder.Url, "/index?"))
	if err != nil {
		t.Fatalf("parse project list query: %v", err)
	}
	if query.Get("project") != "proj-1" || query.Get("limit") != "10" || query.Get("page") != "3" {
		t.Fatalf("unexpected project list query values: %v", query)
	}
	projectPage, err = service.ListObjectsByProjectAfter(ctx, "proj-1", 9, "did-record")
	if err != nil || len(projectPage.DrsObjects) != 1 {
		t.Fatalf("ListObjectsByProjectAfter returned page=%+v err=%v", projectPage, err)
	}
	query, err = url.ParseQuery(strings.TrimPrefix(requester.builder.Url, "/index?"))
	if err != nil {
		t.Fatalf("parse project cursor query: %v", err)
	}
	if query.Get("project") != "proj-1" || query.Get("limit") != "9" || query.Get("start") != "did-record" || query.Get("page") != "" {
		t.Fatalf("unexpected project cursor query values: %v", query)
	}

	sample, err := service.GetProjectSample(ctx, "proj-2", 4)
	if err != nil || len(sample.DrsObjects) != 1 {
		t.Fatalf("GetProjectSample returned page=%+v err=%v", sample, err)
	}
	query, err = url.ParseQuery(strings.TrimPrefix(requester.builder.Url, "/index?"))
	if err != nil {
		t.Fatalf("parse sample list query: %v", err)
	}
	if query.Get("project") != "proj-2" || query.Get("page") != "1" {
		t.Fatalf("unexpected sample query values: %v", query)
	}

	hashPage, err := service.BatchGetObjectsByHash(ctx, []string{"abc", "def"})
	if err != nil || len(hashPage.DrsObjects) != 1 || hashPage.DrsObjects[0].Id != "did-hash" {
		t.Fatalf("BatchGetObjectsByHash returned page=%+v err=%v", hashPage, err)
	}
	if _, err := service.BatchGetObjectsByHash(ctx, []string{"abc", "fail"}); !errors.Is(err, errorapi.ErrUnavailable) {
		t.Fatalf("expected batch checksum lookup to fail on backend error, got %v", err)
	}

	accessURL, err := service.GetAccessURL(ctx, "obj-1", "acc-1")
	if err != nil || accessURL.Url != "https://signed.example/access" {
		t.Fatalf("GetAccessURL returned accessURL=%+v err=%v", accessURL, err)
	}

	registered, err := service.RegisterObjects(ctx, drsapi.RegisterObjectsJSONRequestBody{Candidates: []drsapi.DrsObjectCandidate{{Checksums: []drsapi.Checksum{{Type: "sha256", Checksum: "abc"}}}}})
	if err != nil || len(registered.Objects) != 1 || registered.Objects[0].Id != "obj-created" {
		t.Fatalf("RegisterObjects returned registered=%+v err=%v", registered, err)
	}
	updatedAccessID := "acc-2"
	updated, err := service.UpdateObjectAccessMethods(ctx, "obj-1", []drsapi.AccessMethod{{
		AccessId: &updatedAccessID,
		Type:     drsapi.AccessMethodType("https"),
		AccessUrl: &struct {
			Headers *[]string `json:"headers,omitempty"`
			Url     string    `json:"url"`
		}{Url: "https://signed.example/updated.bin"},
	}})
	if err != nil || updated.AccessMethods == nil || len(*updated.AccessMethods) != 1 || (*updated.AccessMethods)[0].AccessUrl == nil || (*updated.AccessMethods)[0].AccessUrl.Url != "https://signed.example/updated.bin" {
		t.Fatalf("UpdateObjectAccessMethods returned updated=%+v err=%v", updated, err)
	}

	if err := service.DeleteObject(ctx, "obj-delete", true); err != nil {
		t.Fatalf("DeleteObject returned error: %v", err)
	}
	if err := service.DeleteObject(ctx, "obj-delete-fail", true); err == nil || !strings.Contains(err.Error(), "schema is stale") {
		t.Fatalf("expected delete server error detail, got %v", err)
	}
	if deleteRequest.DeleteStorageData == nil || !*deleteRequest.DeleteStorageData {
		t.Fatalf("expected delete_storage_data=true, got %+v", deleteRequest)
	}
	if deleteRequest.DeleteObjectMetadata == nil || !*deleteRequest.DeleteObjectMetadata {
		t.Fatalf("expected delete_object_metadata=true, got %+v", deleteRequest)
	}
}

func TestDRSServiceDeleteObjectRejectsUnexpectedStatus(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut && r.URL.Path == "/objects/obj-delete/delete" {
			w.WriteHeader(http.StatusTeapot)
			return
		}
		t.Fatalf("unexpected request: %s %s", r.Method, r.URL.String())
	}))
	defer server.Close()

	service := NewDRSService(mustDRSClient(t, server.URL), nil)
	err := service.DeleteObject(context.Background(), "obj-delete", true)
	var apiErr *apierror.APIError
	if !errors.As(err, &apiErr) || apiErr.Status != http.StatusTeapot {
		t.Fatalf("expected unexpected-status error, got %v", err)
	}
}

func TestDRSServiceDeleteRecordsByHash(t *testing.T) {
	t.Parallel()

	recordName := "object.bin"
	recordSize := int64(77)
	recordURLs := []string{"https://storage.example/object.bin"}
	recordHashes := internalapi.HashInfo{"sha256": "abc"}
	drsRecords := []drsapi.DrsObject{
		{
			Id:        "obj-1",
			Name:      &recordName,
			Size:      recordSize,
			Checksums: []drsapi.Checksum{{Type: "sha256", Checksum: "abc"}},
		},
		{
			Id:        "obj-2",
			Name:      &recordName,
			Size:      recordSize,
			Checksums: []drsapi.Checksum{{Type: "sha256", Checksum: "abc"}},
		},
	}
	records := []internalapi.InternalRecord{
		testRecordForURL("obj-1", recordURLs[0], map[string][]string{"org1": {"proj1"}}),
		testRecordForURL("obj-2", recordURLs[0], map[string][]string{"org1": {}}),
	}
	for i := range records {
		records[i].Name = &recordName
		records[i].Size = &recordSize
		records[i].Hashes = &recordHashes
	}
	listResp, err := json.Marshal(internalapi.ListRecordsResponse{Records: &records})
	if err != nil {
		t.Fatalf("marshal list response: %v", err)
	}
	requester := &fakeRequester{responseJSON: listResp}
	httpClient := &http.Client{Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/objects/checksum/abc":
			return jsonResponse(http.StatusOK, drsapi.N200OkDrsObjects{ResolvedDrsObject: &drsRecords})
		case r.Method == http.MethodDelete && (r.URL.Path == "/index/obj-1" || r.URL.Path == "/index/obj-2"):
			return emptyResponse(http.StatusNoContent), nil
		default:
			return nil, fmt.Errorf("unexpected request: %s %s", r.Method, r.URL.String())
		}
	})}
	index := NewIndexService(mustInternalClientWithHTTPClient(httpClient), requester)
	service := NewDRSService(mustDRSClientWithHTTPClient(httpClient), index)
	ctx := context.Background()

	if err := service.DeleteRecordsByHash(ctx, "sha256:abc"); err != nil {
		t.Fatalf("DeleteRecordsByHash returned error: %v", err)
	}

	emptyHTTPClient := &http.Client{Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		if r.Method == http.MethodGet && r.URL.Path == "/objects/checksum/abc" {
			empty := []drsapi.DrsObject{}
			return jsonResponse(http.StatusOK, drsapi.N200OkDrsObjects{ResolvedDrsObject: &empty})
		}
		return nil, fmt.Errorf("unexpected request: %s %s", r.Method, r.URL.String())
	})}
	emptyRequester := &fakeRequester{responseJSON: []byte(`{"records":[]}`)}
	emptyIndex := NewIndexService(mustInternalClientWithHTTPClient(emptyHTTPClient), emptyRequester)
	emptyService := NewDRSService(mustDRSClientWithHTTPClient(emptyHTTPClient), emptyIndex)
	if err := emptyService.DeleteRecordsByHash(ctx, "sha256:abc"); err == nil {
		t.Fatal("expected no-records error from DeleteRecordsByHash")
	} else if !errors.Is(err, ErrNoRecordsForHash) {
		t.Fatalf("expected ErrNoRecordsForHash, got %v", err)
	} else if errors.Is(err, errorapi.ErrObjectNotFound) {
		t.Fatalf("no-records error must not match errorapi.ErrObjectNotFound: %v", err)
	}
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func jsonResponse(status int, v any) (*http.Response, error) {
	body, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return &http.Response{
		StatusCode: status,
		Body:       io.NopCloser(strings.NewReader(string(body))),
		Header:     http.Header{"Content-Type": []string{"application/json"}},
	}, nil
}

func emptyResponse(status int) *http.Response {
	return &http.Response{
		StatusCode: status,
		Body:       io.NopCloser(strings.NewReader("")),
		Header:     http.Header{"Content-Type": []string{"application/json"}},
	}
}

func mustInternalClientWithHTTPClient(client *http.Client) *internalapi.ClientWithResponses {
	c, err := internalapi.NewClientWithResponses("http://example.invalid", internalapi.WithHTTPClient(client))
	if err != nil {
		panic(err)
	}
	return c
}

func mustDRSClientWithHTTPClient(client *http.Client) *drsapi.ClientWithResponses {
	c, err := drsapi.NewClientWithResponses("http://example.invalid", drsapi.WithHTTPClient(client))
	if err != nil {
		panic(err)
	}
	return c
}
