package internaldrs

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/calypr/syfon/apigen/server/drs"
	"github.com/calypr/syfon/internal/core"
	"github.com/calypr/syfon/internal/models"
	"github.com/calypr/syfon/internal/testutils"
)

func TestHandleInternalInspectObjectScopedSuccess(t *testing.T) {
	body, _ := json.Marshal(internalInspectObjectRequest{Organization: "syfon", Project: "e2e", Key: "nested/file.bin", Scheme: "s3"})
	db := &testutils.MockDatabase{
		Credentials: map[string]models.S3Credential{
			"b1": {Bucket: "b1"},
		},
		BucketScopes: map[string]models.BucketScope{
			"syfon|":    {Organization: "syfon", Bucket: "b1", PathPrefix: "program-root"},
			"syfon|e2e": {Organization: "syfon", ProjectID: "e2e", Bucket: "b1", PathPrefix: "project-root"},
		},
	}
	om := core.NewObjectManager(db, &testutils.MockUrlManager{})
	om.SetS3ObjectInspector(func(ctx context.Context, cred models.S3Credential, bucket string, key string) (*core.StorageObjectMetadata, error) {
		return &core.StorageObjectMetadata{Bucket: bucket, Key: key, Path: "file.bin", SizeBytes: 17, ETag: "etag-1", LastModTime: time.Date(2026, 6, 11, 1, 2, 3, 0, time.UTC)}, nil
	})
	req := withTestAuthzContext(httptest.NewRequest(http.MethodPost, "/data/inspect", bytes.NewBuffer(body)), "gen3", map[string]map[string]bool{"/organization/syfon/project/e2e": {"read": true}})
	rr := doInternalDRSTestRequest(req, om)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rr.Code, rr.Body.String())
	}
	var resp internalInspectObjectResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.ObjectURL != "s3://b1/program-root/project-root/nested/file.bin" {
		t.Fatalf("unexpected object_url: %s", resp.ObjectURL)
	}
	if resp.Bucket != "b1" || resp.Key != "program-root/project-root/nested/file.bin" {
		t.Fatalf("unexpected location: bucket=%s key=%s", resp.Bucket, resp.Key)
	}
	if resp.ETag != "etag-1" || resp.SizeBytes != 17 {
		t.Fatalf("unexpected metadata: %+v", resp)
	}
	if resp.LastModTime != "2026-06-11T01:02:03Z" {
		t.Fatalf("unexpected last_modified: %s", resp.LastModTime)
	}
}

func TestHandleInternalInspectObjectRawSuccess(t *testing.T) {
	body, _ := json.Marshal(internalInspectObjectRequest{ObjectURL: "s3://b1/program-root/raw/file.bin"})
	db := &testutils.MockDatabase{
		Credentials: map[string]models.S3Credential{"b1": {Bucket: "b1"}},
		BucketScopes: map[string]models.BucketScope{
			"syfon|": {Organization: "syfon", Bucket: "b1", PathPrefix: "program-root"},
		},
	}
	om := core.NewObjectManager(db, &testutils.MockUrlManager{})
	om.SetS3ObjectInspector(func(ctx context.Context, cred models.S3Credential, bucket string, key string) (*core.StorageObjectMetadata, error) {
		return &core.StorageObjectMetadata{Bucket: bucket, Key: key, Path: "file.bin", SizeBytes: 99, ETag: "etag-raw"}, nil
	})
	req := withTestAuthzContext(httptest.NewRequest(http.MethodPost, "/data/inspect", bytes.NewBuffer(body)), "gen3", map[string]map[string]bool{"/organization/syfon": {"read": true}})
	rr := doInternalDRSTestRequest(req, om)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "etag-raw") {
		t.Fatalf("expected raw metadata response, got %s", rr.Body.String())
	}
}

func TestHandleInternalInspectObjectMissingScope(t *testing.T) {
	body, _ := json.Marshal(internalInspectObjectRequest{Organization: "syfon", Project: "missing", Key: "nested/file.bin", Scheme: "s3"})
	om := core.NewObjectManager(&testutils.MockDatabase{}, &testutils.MockUrlManager{})
	req := withTestAuthzContext(httptest.NewRequest(http.MethodPost, "/data/inspect", bytes.NewBuffer(body)), "gen3", map[string]map[string]bool{"/organization/syfon/project/missing": {"read": true}})
	rr := doInternalDRSTestRequest(req, om)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d body=%s", rr.Code, rr.Body.String())
	}
}

func TestHandleInternalInspectObjectPermissionDenied(t *testing.T) {
	body, _ := json.Marshal(internalInspectObjectRequest{ObjectURL: "s3://b1/program-root/raw/file.bin"})
	db := &testutils.MockDatabase{
		Credentials: map[string]models.S3Credential{"b1": {Bucket: "b1"}},
		BucketScopes: map[string]models.BucketScope{
			"syfon|": {Organization: "syfon", Bucket: "b1", PathPrefix: "program-root"},
		},
	}
	om := core.NewObjectManager(db, &testutils.MockUrlManager{})
	om.SetS3ObjectInspector(func(ctx context.Context, cred models.S3Credential, bucket string, key string) (*core.StorageObjectMetadata, error) {
		return nil, &core.StorageInspectError{Kind: core.StorageInspectPermissionDenied, Message: "provider denied access to s3://b1/program-root/raw/file.bin"}
	})
	req := withTestAuthzContext(httptest.NewRequest(http.MethodPost, "/data/inspect", bytes.NewBuffer(body)), "gen3", map[string]map[string]bool{"/organization/syfon": {"read": true}})
	rr := doInternalDRSTestRequest(req, om)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d body=%s", rr.Code, rr.Body.String())
	}
}

func TestHandleInternalInspectObjectMalformedURL(t *testing.T) {
	body, _ := json.Marshal(internalInspectObjectRequest{ObjectURL: "https://example.com/file.bin"})
	om := core.NewObjectManager(&testutils.MockDatabase{}, &testutils.MockUrlManager{})
	req := withTestAuthzContext(httptest.NewRequest(http.MethodPost, "/data/inspect", bytes.NewBuffer(body)), "gen3", map[string]map[string]bool{"/organization/syfon": {"read": true}})
	rr := doInternalDRSTestRequest(req, om)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%s", rr.Code, rr.Body.String())
	}
}

func TestHandleInternalInspectProjectRecords(t *testing.T) {
	body, _ := json.Marshal(internalInspectProjectRecordsRequest{Organization: "syfon", Project: "e2e"})
	name := "example.bin"
	created := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	updated := time.Date(2026, 7, 1, 12, 30, 0, 0, time.UTC)
	db := &testutils.MockDatabase{
		Credentials: map[string]models.S3Credential{
			"cred-1": {CredentialID: "cred-1", Bucket: "bucket-a", Provider: "s3"},
		},
		BucketScopes: map[string]models.BucketScope{
			"syfon|e2e": {Organization: "syfon", ProjectID: "e2e", Bucket: "bucket-a", CredentialID: "cred-1", PathPrefix: "project-root"},
		},
		Objects: map[string]*drs.DrsObject{
			"obj-1": {
				Id:   "obj-1",
				Name: &name,
				Checksums: []drs.Checksum{
					{Type: "sha256", Checksum: "abc123"},
				},
				CreatedTime: created,
				UpdatedTime: &updated,
				Size:        17,
				AccessMethods: &[]drs.AccessMethod{
					{
						Type:     "s3",
						AccessId: ptr("acc-1"),
						AccessUrl: &struct {
							Headers *[]string `json:"headers,omitempty"`
							Url     string    `json:"url"`
						}{Url: "s3://bucket-a/prefix/example.bin"},
					},
				},
			},
		},
		ObjectAuthz: map[string]map[string][]string{
			"obj-1": {"syfon": {"e2e"}},
		},
	}
	om := core.NewObjectManager(db, &testutils.MockUrlManager{})
	req := withTestAuthzContext(httptest.NewRequest(http.MethodPost, "/data/inspect/project-records", bytes.NewBuffer(body)), "gen3", map[string]map[string]bool{"/organization/syfon/project/e2e": {"read": true}})
	rr := doInternalDRSTestRequest(req, om)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rr.Code, rr.Body.String())
	}
	var resp internalInspectProjectRecordsResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(resp.Items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(resp.Items))
	}
	item := resp.Items[0]
	if item.ObjectID != "obj-1" || item.Checksum != "abc123" {
		t.Fatalf("unexpected item identity: %+v", item)
	}
	if item.Name != "example.bin" {
		t.Fatalf("expected project record name, got %q", item.Name)
	}
	if item.Organization != "syfon" || item.Project != "e2e" {
		t.Fatalf("unexpected scope: %+v", item)
	}
	if len(item.AccessURLs) != 1 || item.AccessURLs[0] != "s3://bucket-a/prefix/example.bin" {
		t.Fatalf("unexpected access urls: %+v", item.AccessURLs)
	}
}

func TestHandleInternalInspectProjectRecordsPreservesLegacyDuplicatePhysicalRows(t *testing.T) {
	body, _ := json.Marshal(internalInspectProjectRecordsRequest{Organization: "syfon", Project: "e2e"})
	checksum := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	created := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	newer := created.Add(time.Minute)
	urlFor := func(url string) *struct {
		Headers *[]string `json:"headers,omitempty"`
		Url     string    `json:"url"`
	} {
		return &struct {
			Headers *[]string `json:"headers,omitempty"`
			Url     string    `json:"url"`
		}{Url: url}
	}
	db := &testutils.MockDatabase{
		Objects: map[string]*drs.DrsObject{
			"physical-a": {
				Id: "physical-a", Checksums: []drs.Checksum{{Type: "sha256", Checksum: checksum}},
				CreatedTime: created, UpdatedTime: &created,
				AccessMethods: &[]drs.AccessMethod{{Type: drs.AccessMethodTypeS3, AccessUrl: urlFor("s3://bucket/physical-a")}},
			},
			"physical-b": {
				Id: "physical-b", Checksums: []drs.Checksum{{Type: "sha256", Checksum: checksum}},
				CreatedTime: newer, UpdatedTime: &newer,
				AccessMethods: &[]drs.AccessMethod{{Type: drs.AccessMethodTypeS3, AccessUrl: urlFor("s3://bucket/physical-b")}},
			},
		},
		ObjectAuthz: map[string]map[string][]string{
			"physical-a": {"syfon": {"e2e"}},
			"physical-b": {"syfon": {"e2e"}},
		},
	}
	om := core.NewObjectManager(db, &testutils.MockUrlManager{})
	req := withTestAuthzContext(httptest.NewRequest(http.MethodPost, "/data/inspect/project-records", bytes.NewBuffer(body)), "gen3", map[string]map[string]bool{"/organization/syfon/project/e2e": {"read": true}})
	rr := doInternalDRSTestRequest(req, om)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rr.Code, rr.Body.String())
	}
	var resp internalInspectProjectRecordsResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(resp.Items) != 2 {
		t.Fatalf("expected both physical rows, got %d: %+v", len(resp.Items), resp.Items)
	}
	byID := make(map[string]internalInspectProjectRecordItem, len(resp.Items))
	for _, item := range resp.Items {
		byID[item.ObjectID] = item
	}
	for id, wantURL := range map[string]string{
		"physical-a": "s3://bucket/physical-a",
		"physical-b": "s3://bucket/physical-b",
	} {
		item, ok := byID[id]
		if !ok {
			t.Fatalf("missing physical row %q: %+v", id, resp.Items)
		}
		if len(item.AccessURLs) != 1 || item.AccessURLs[0] != wantURL {
			t.Fatalf("physical row %q returned access URLs %v, want %q", id, item.AccessURLs, wantURL)
		}
	}
}

func TestProjectRecordMatchesAnyPathPrefixAvoidsFalsePrefixMatches(t *testing.T) {
	record := internalInspectProjectRecordItem{
		AccessURLs: []string{"s3://bucket-a/project-root/CONFIG/a.json"},
		AccessMethods: []internalProjectAccessMethod{
			{URL: "s3://bucket-a/project-root/CONFIG/a.json"},
		},
	}
	if !projectRecordMatchesAnyPathPrefix(record, "CONFIG", "project-root/CONFIG") {
		t.Fatalf("expected CONFIG path prefix to match CONFIG/a.json")
	}
	if projectRecordMatchesAnyPathPrefix(record, "CONFIGURATION", "project-root/CONFIGURATION") {
		t.Fatalf("expected CONFIGURATION path prefix not to match CONFIG/a.json")
	}
}

func TestHandleInternalInspectProjectScopes(t *testing.T) {
	db := &testutils.MockDatabase{
		Credentials: map[string]models.S3Credential{
			"cred-1": {CredentialID: "cred-1", Bucket: "bucket-a", Provider: "s3"},
		},
		BucketScopes: map[string]models.BucketScope{
			"syfon|":    {Organization: "syfon", Bucket: "bucket-a", CredentialID: "cred-1", PathPrefix: "program-root"},
			"syfon|e2e": {Organization: "syfon", ProjectID: "e2e", Bucket: "bucket-a", CredentialID: "cred-1", PathPrefix: "project-root"},
		},
	}
	om := core.NewObjectManager(db, &testutils.MockUrlManager{})
	authz := map[string]map[string]bool{
		"/organization/syfon":             {"read": true},
		"/organization/syfon/project/e2e": {"read": true},
	}
	cases := []struct {
		name   string
		method string
		path   string
		body   io.Reader
	}{
		{
			name:   "get query",
			method: http.MethodGet,
			path:   "/data/inspect/project-scopes?organization=syfon&project=e2e",
		},
		{
			name:   "post body",
			method: http.MethodPost,
			path:   "/data/inspect/project-scopes",
			body:   bytes.NewBufferString(`{"organization":"syfon","project":"e2e"}`),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := withTestAuthzContext(httptest.NewRequest(tc.method, tc.path, tc.body), "gen3", authz)
			rr := doInternalDRSTestRequest(req, om)
			if rr.Code != http.StatusOK {
				t.Fatalf("expected 200, got %d body=%s", rr.Code, rr.Body.String())
			}
			var resp internalInspectProjectScopesResponse
			if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
				t.Fatalf("decode response: %v", err)
			}
			if len(resp.Items) != 2 {
				t.Fatalf("expected 2 items, got %d", len(resp.Items))
			}
			if resp.Items[0].Bucket != "bucket-a" || !strings.HasPrefix(resp.Items[0].Path, "s3://bucket-a/") {
				t.Fatalf("unexpected first scope: %+v", resp.Items[0])
			}
		})
	}
}

func TestHandleInternalInspectProjectBucketModesUsePrefixList(t *testing.T) {
	db := &testutils.MockDatabase{
		Credentials: map[string]models.S3Credential{
			"cred-1": {CredentialID: "cred-1", Bucket: "bucket-a", Provider: "s3"},
		},
		BucketScopes: map[string]models.BucketScope{
			"syfon|":    {Organization: "syfon", Bucket: "bucket-a", CredentialID: "cred-1", PathPrefix: "program-root"},
			"syfon|e2e": {Organization: "syfon", ProjectID: "e2e", Bucket: "bucket-a", CredentialID: "cred-1", PathPrefix: "project-root"},
		},
	}
	om := core.NewObjectManager(db, &testutils.MockUrlManager{})
	inspectCalls := 0
	om.SetS3ObjectInspector(func(ctx context.Context, cred models.S3Credential, bucket string, key string) (*core.StorageObjectMetadata, error) {
		inspectCalls++
		return nil, nil
	})
	var listCalls []core.StoragePrefixListOptions
	var listedPrefixes []string
	om.SetS3PrefixListerWithOptions(func(ctx context.Context, cred models.S3Credential, bucket string, prefix string, options core.StoragePrefixListOptions) ([]core.StorageBucketObject, error) {
		if bucket != "bucket-a" {
			t.Fatalf("unexpected list target bucket=%q prefix=%q", bucket, prefix)
		}
		listedPrefixes = append(listedPrefixes, prefix)
		listCalls = append(listCalls, options)
		if prefix == "program-root/project-root/CONFIG" {
			return []core.StorageBucketObject{
				{Provider: "s3", Bucket: bucket, Key: prefix + "/a.bin", Path: "CONFIG/a.bin", SizeBytes: 10},
				{Provider: "s3", Bucket: bucket, Key: prefix + "/nested/b.bin", Path: "CONFIG/nested/b.bin", SizeBytes: 15},
			}, nil
		}
		if prefix != "program-root/project-root" {
			t.Fatalf("unexpected list prefix=%q", prefix)
		}
		return []core.StorageBucketObject{
			{Provider: "s3", Bucket: bucket, Key: prefix + "/a.bin", Path: "a.bin", SizeBytes: 10},
			{Provider: "s3", Bucket: bucket, Key: prefix + "/b.bin", Path: "b.bin", SizeBytes: 15},
		}, nil
	})

	existsBody, _ := json.Marshal(internalInspectProjectBucketRequest{Organization: "syfon", Project: "e2e", Mode: "exists"})
	req := withTestAuthzContext(httptest.NewRequest(http.MethodPost, "/data/inspect/project-bucket", bytes.NewBuffer(existsBody)), "gen3", map[string]map[string]bool{
		"/organization/syfon/project/e2e": {"read": true},
	})
	rr := doInternalDRSTestRequest(req, om)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rr.Code, rr.Body.String())
	}
	var existsResp internalInspectProjectBucketResponse
	if err := json.NewDecoder(rr.Body).Decode(&existsResp); err != nil {
		t.Fatalf("decode exists response: %v", err)
	}
	if existsResp.Summary == nil || !existsResp.Summary.Exists || existsResp.Summary.Mode != "exists" {
		t.Fatalf("unexpected exists summary: %+v", existsResp.Summary)
	}
	if len(existsResp.Items) != 0 {
		t.Fatalf("exists mode should not return item rows, got %+v", existsResp.Items)
	}
	if len(listCalls) != 1 || listCalls[0].MaxKeys != 1 || listCalls[0].IncludeHead {
		t.Fatalf("exists mode should use MaxKeys=1 without HEAD, got %+v", listCalls)
	}

	summaryBody, _ := json.Marshal(internalInspectProjectBucketRequest{Organization: "syfon", Project: "e2e", Mode: "summary"})
	req = withTestAuthzContext(httptest.NewRequest(http.MethodPost, "/data/inspect/project-bucket", bytes.NewBuffer(summaryBody)), "gen3", map[string]map[string]bool{
		"/organization/syfon/project/e2e": {"read": true},
	})
	rr = doInternalDRSTestRequest(req, om)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rr.Code, rr.Body.String())
	}
	var summaryResp internalInspectProjectBucketResponse
	if err := json.NewDecoder(rr.Body).Decode(&summaryResp); err != nil {
		t.Fatalf("decode summary response: %v", err)
	}
	if summaryResp.Summary == nil || summaryResp.Summary.Mode != "summary" || summaryResp.Summary.ObjectCount != 2 || summaryResp.Summary.TotalBytes != 25 {
		t.Fatalf("unexpected summary response: %+v", summaryResp.Summary)
	}
	if len(summaryResp.Items) != 0 {
		t.Fatalf("summary mode should not return item rows, got %+v", summaryResp.Items)
	}
	if len(listCalls) != 2 || listCalls[1].MaxKeys != 0 || listCalls[1].IncludeHead {
		t.Fatalf("summary mode should list recursively without HEAD, got %+v", listCalls)
	}

	itemsBody, _ := json.Marshal(internalInspectProjectBucketRequest{Organization: "syfon", Project: "e2e", Mode: "items", PathPrefix: "CONFIG"})
	req = withTestAuthzContext(httptest.NewRequest(http.MethodPost, "/data/inspect/project-bucket", bytes.NewBuffer(itemsBody)), "gen3", map[string]map[string]bool{
		"/organization/syfon/project/e2e": {"read": true},
	})
	rr = doInternalDRSTestRequest(req, om)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rr.Code, rr.Body.String())
	}
	var itemsResp internalInspectProjectBucketResponse
	if err := json.NewDecoder(rr.Body).Decode(&itemsResp); err != nil {
		t.Fatalf("decode items response: %v", err)
	}
	if itemsResp.Summary == nil || itemsResp.Summary.Mode != "items" || itemsResp.Summary.Prefix != "program-root/project-root/CONFIG" {
		t.Fatalf("unexpected items summary: %+v", itemsResp.Summary)
	}
	if len(itemsResp.Items) != 2 {
		t.Fatalf("items mode should return recursive rows, got %+v", itemsResp.Items)
	}
	if len(listedPrefixes) != 3 || listedPrefixes[2] != "program-root/project-root/CONFIG" {
		t.Fatalf("expected path_prefix to scope recursive LIST, got %+v", listedPrefixes)
	}
	if inspectCalls != 0 {
		t.Fatalf("expected project-bucket modes not to HEAD objects, got %d inspector calls", inspectCalls)
	}
}

func TestHandleInternalInspectProjectBucketInventoryListsProjectScope(t *testing.T) {
	db := &testutils.MockDatabase{
		Credentials: map[string]models.S3Credential{
			"cred-1": {CredentialID: "cred-1", Bucket: "bucket-a", Provider: "s3"},
		},
		BucketScopes: map[string]models.BucketScope{
			"syfon|":    {Organization: "syfon", Bucket: "bucket-a", CredentialID: "cred-1", PathPrefix: "program-root"},
			"syfon|e2e": {Organization: "syfon", ProjectID: "e2e", Bucket: "bucket-a", CredentialID: "cred-1", PathPrefix: "project-root"},
		},
	}
	om := core.NewObjectManager(db, &testutils.MockUrlManager{})
	inspectCalls := 0
	om.SetS3ObjectInspector(func(ctx context.Context, cred models.S3Credential, bucket string, key string) (*core.StorageObjectMetadata, error) {
		inspectCalls++
		return nil, nil
	})
	var listCalls []core.StoragePrefixListOptions
	var listedPrefixes []string
	om.SetS3PrefixListerWithOptions(func(ctx context.Context, cred models.S3Credential, bucket string, prefix string, options core.StoragePrefixListOptions) ([]core.StorageBucketObject, error) {
		if bucket != "bucket-a" {
			t.Fatalf("unexpected list target bucket=%q prefix=%q", bucket, prefix)
		}
		listedPrefixes = append(listedPrefixes, prefix)
		listCalls = append(listCalls, options)
		return []core.StorageBucketObject{
			{Provider: "s3", Bucket: bucket, Key: prefix + "/a.bin", Path: "CONFIG/a.bin", SizeBytes: 10},
			{Provider: "s3", Bucket: bucket, Key: prefix + "/nested/b.bin", Path: "CONFIG/nested/b.bin", SizeBytes: 15},
		}, nil
	})

	body, _ := json.Marshal(internalInspectProjectBucketRequest{Organization: "syfon", Project: "e2e", PathPrefix: "CONFIG"})
	req := withTestAuthzContext(httptest.NewRequest(http.MethodPost, "/data/inspect/project-bucket/inventory", bytes.NewBuffer(body)), "gen3", map[string]map[string]bool{
		"/organization/syfon/project/e2e": {"read": true},
	})
	rr := doInternalDRSTestRequest(req, om)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rr.Code, rr.Body.String())
	}
	var resp internalInspectProjectBucketResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Summary == nil || resp.Summary.Mode != "items" || resp.Summary.Prefix != "program-root/project-root/CONFIG" {
		t.Fatalf("unexpected inventory summary: %+v", resp.Summary)
	}
	if resp.Summary.ObjectCount != 2 || resp.Summary.TotalBytes != 25 || len(resp.Items) != 2 {
		t.Fatalf("unexpected inventory response summary=%+v items=%+v", resp.Summary, resp.Items)
	}
	for _, item := range resp.Items {
		if !item.InventoryComplete {
			t.Fatalf("expected successful inventory item to be marked complete, got %+v", item)
		}
	}
	if len(listCalls) != 1 || listCalls[0].IncludeHead || listCalls[0].MaxKeys != 0 || listCalls[0].ExactPrefix {
		t.Fatalf("expected one recursive LIST without HEAD, got %+v", listCalls)
	}
	if len(listedPrefixes) != 1 || listedPrefixes[0] != "program-root/project-root/CONFIG" {
		t.Fatalf("expected path_prefix to scope recursive inventory, got %+v", listedPrefixes)
	}
	if inspectCalls != 0 {
		t.Fatalf("expected inventory route not to HEAD objects, got %d inspector calls", inspectCalls)
	}
}

func TestHandleInternalInspectProjectBucketInventoryReturnsPartialListing(t *testing.T) {
	db := &testutils.MockDatabase{
		Credentials: map[string]models.S3Credential{
			"cred-1": {CredentialID: "cred-1", Bucket: "bucket-a", Provider: "s3"},
		},
		BucketScopes: map[string]models.BucketScope{
			"syfon|":    {Organization: "syfon", Bucket: "bucket-a", CredentialID: "cred-1", PathPrefix: "program-root"},
			"syfon|e2e": {Organization: "syfon", ProjectID: "e2e", Bucket: "bucket-a", CredentialID: "cred-1", PathPrefix: "project-root"},
		},
	}
	om := core.NewObjectManager(db, &testutils.MockUrlManager{})
	om.SetS3PrefixListerWithOptions(func(context.Context, models.S3Credential, string, string, core.StoragePrefixListOptions) ([]core.StorageBucketObject, error) {
		return []core.StorageBucketObject{{
				Provider:  "s3",
				Bucket:    "bucket-a",
				Key:       "program-root/project-root/observed.bin",
				Path:      "observed.bin",
				SizeBytes: 17,
			}}, &core.StorageInspectError{
				Kind:    core.StorageInspectListingIncomplete,
				Message: "terminal replay returned different page content",
			}
	})

	body, _ := json.Marshal(internalInspectProjectBucketRequest{Organization: "syfon", Project: "e2e"})
	req := withTestAuthzContext(httptest.NewRequest(http.MethodPost, "/data/inspect/project-bucket/inventory", bytes.NewBuffer(body)), "gen3", map[string]map[string]bool{
		"/organization/syfon/project/e2e": {"read": true},
	})
	rr := doInternalDRSTestRequest(req, om)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected partial inventory to return 200, got %d body=%s", rr.Code, rr.Body.String())
	}
	var resp internalInspectProjectBucketResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Summary == nil || resp.Summary.InventoryComplete {
		t.Fatalf("expected incomplete inventory summary, got %+v", resp.Summary)
	}
	if !strings.Contains(resp.Summary.InventoryWarning, "terminal replay") {
		t.Fatalf("expected listing warning to be preserved, got %+v", resp.Summary)
	}
	if len(resp.Items) != 1 || resp.Items[0].InventoryComplete {
		t.Fatalf("expected observed partial item, got %+v", resp.Items)
	}
}

func TestHandleInternalInspectObjectBulkListValidatesExactKeyWithoutHead(t *testing.T) {
	db := &testutils.MockDatabase{
		Credentials: map[string]models.S3Credential{
			"cred-1": {CredentialID: "cred-1", Bucket: "bucket-a", Provider: "s3"},
		},
		BucketScopes: map[string]models.BucketScope{
			"syfon|e2e": {Organization: "syfon", ProjectID: "e2e", Bucket: "bucket-a", CredentialID: "cred-1", PathPrefix: "project-root"},
		},
	}
	om := core.NewObjectManager(db, &testutils.MockUrlManager{})
	inspectCalls := 0
	om.SetS3ObjectInspector(func(ctx context.Context, cred models.S3Credential, bucket string, key string) (*core.StorageObjectMetadata, error) {
		inspectCalls++
		return nil, nil
	})
	var listedPrefixesMu sync.Mutex
	var listedPrefixes []string
	om.SetS3PrefixListerWithOptions(func(ctx context.Context, cred models.S3Credential, bucket string, prefix string, options core.StoragePrefixListOptions) ([]core.StorageBucketObject, error) {
		if bucket != "bucket-a" || !options.ExactPrefix || options.MaxKeys != 1 || options.IncludeHead {
			t.Fatalf("unexpected bulk-list options bucket=%q options=%+v", bucket, options)
		}
		listedPrefixesMu.Lock()
		listedPrefixes = append(listedPrefixes, prefix)
		listedPrefixesMu.Unlock()
		switch prefix {
		case "project-root/file.bin":
			return []core.StorageBucketObject{{Provider: "s3", Bucket: bucket, Key: "project-root/file.bin", Path: "file.bin", SizeBytes: 17, ETag: "etag-1", LastModTime: time.Date(2026, 7, 1, 1, 2, 3, 0, time.UTC)}}, nil
		case "project-root/dir":
			return []core.StorageBucketObject{{Provider: "s3", Bucket: bucket, Key: "project-root/dir/child.bin", Path: "dir/child.bin", SizeBytes: 19}}, nil
		default:
			return nil, nil
		}
	})
	expectedSize := int64(17)
	body, _ := json.Marshal(internalInspectObjectsBulkRequest{Items: []internalInspectObjectRequest{
		{ID: "present", ObjectURL: "s3://bucket-a/project-root/file.bin", ExpectedSizeBytes: &expectedSize, ExpectedName: "file.bin"},
		{ID: "prefix-child", ObjectURL: "s3://bucket-a/project-root/dir", ExpectedSizeBytes: &expectedSize, ExpectedName: "dir"},
	}})
	req := withTestAuthzContext(httptest.NewRequest(http.MethodPost, "/data/inspect/bulk-list", bytes.NewBuffer(body)), "gen3", map[string]map[string]bool{
		"/organization/syfon/project/e2e": {"read": true},
	})
	rr := doInternalDRSTestRequest(req, om)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rr.Code, rr.Body.String())
	}
	var resp internalInspectObjectBulkResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(resp.Items) != 2 {
		t.Fatalf("expected 2 items, got %+v", resp.Items)
	}
	if !resp.Items[0].Exists || resp.Items[0].Status != "present" || resp.Items[0].ValidationStatus != "matched" {
		t.Fatalf("unexpected present item: %+v", resp.Items[0])
	}
	if resp.Items[1].Exists || resp.Items[1].Status != "not_found" {
		t.Fatalf("expected prefix child not to count as exact key, got %+v", resp.Items[1])
	}
	listedPrefixesMu.Lock()
	listedPrefixCount := len(listedPrefixes)
	listedPrefixesMu.Unlock()
	if listedPrefixCount != 2 {
		t.Fatalf("expected two LIST calls, got %+v", listedPrefixes)
	}
	if inspectCalls != 0 {
		t.Fatalf("expected bulk-list not to HEAD objects, got %d inspector calls", inspectCalls)
	}
}

func TestHandleInternalInspectObjectBulkListDeduplicatesExactTargets(t *testing.T) {
	db := &testutils.MockDatabase{
		Credentials: map[string]models.S3Credential{
			"cred-1": {CredentialID: "cred-1", Bucket: "bucket-a", Provider: "s3"},
		},
		BucketScopes: map[string]models.BucketScope{
			"syfon|e2e": {Organization: "syfon", ProjectID: "e2e", Bucket: "bucket-a", CredentialID: "cred-1", PathPrefix: "project-root"},
		},
	}
	om := core.NewObjectManager(db, &testutils.MockUrlManager{})
	listCalls := 0
	om.SetS3PrefixListerWithOptions(func(ctx context.Context, cred models.S3Credential, bucket string, prefix string, options core.StoragePrefixListOptions) ([]core.StorageBucketObject, error) {
		listCalls++
		if bucket != "bucket-a" || prefix != "project-root/file.bin" || !options.ExactPrefix || options.MaxKeys != 1 {
			t.Fatalf("unexpected bulk-list request bucket=%q prefix=%q options=%+v", bucket, prefix, options)
		}
		return []core.StorageBucketObject{{Provider: "s3", Bucket: bucket, Key: "project-root/file.bin", Path: "file.bin", SizeBytes: 17}}, nil
	})
	expectedSize := int64(17)
	body, _ := json.Marshal(internalInspectObjectsBulkRequest{Items: []internalInspectObjectRequest{
		{ID: "one", ObjectURL: "s3://bucket-a/project-root/file.bin", ExpectedSizeBytes: &expectedSize, ExpectedName: "file.bin"},
		{ID: "two", ObjectURL: "s3://bucket-a/project-root/file.bin", ExpectedSizeBytes: &expectedSize, ExpectedName: "file.bin"},
	}})
	req := withTestAuthzContext(httptest.NewRequest(http.MethodPost, "/data/inspect/bulk-list", bytes.NewBuffer(body)), "gen3", map[string]map[string]bool{
		"/organization/syfon/project/e2e": {"read": true},
	})
	rr := doInternalDRSTestRequest(req, om)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rr.Code, rr.Body.String())
	}
	var resp internalInspectObjectBulkResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if listCalls != 1 {
		t.Fatalf("expected duplicate exact keys to share one LIST call, got %d", listCalls)
	}
	if len(resp.Items) != 2 || resp.Items[0].ID != "one" || resp.Items[1].ID != "two" {
		t.Fatalf("expected one response per original request, got %+v", resp.Items)
	}
}

func TestHandleInternalInspectObjectBulkListSharesRemoteEvidenceAcrossValidationExpectations(t *testing.T) {
	db := &testutils.MockDatabase{
		Credentials: map[string]models.S3Credential{
			"cred-1": {CredentialID: "cred-1", Bucket: "bucket-a", Provider: "s3"},
		},
		BucketScopes: map[string]models.BucketScope{
			"syfon|e2e": {Organization: "syfon", ProjectID: "e2e", Bucket: "bucket-a", CredentialID: "cred-1", PathPrefix: "project-root"},
		},
	}
	om := core.NewObjectManager(db, &testutils.MockUrlManager{})
	listCalls := 0
	om.SetS3PrefixListerWithOptions(func(ctx context.Context, cred models.S3Credential, bucket string, prefix string, options core.StoragePrefixListOptions) ([]core.StorageBucketObject, error) {
		listCalls++
		return []core.StorageBucketObject{{Provider: "s3", Bucket: bucket, Key: "project-root/file.bin", Path: "file.bin", SizeBytes: 17}}, nil
	})
	size17 := int64(17)
	size99 := int64(99)
	body, _ := json.Marshal(internalInspectObjectsBulkRequest{Items: []internalInspectObjectRequest{
		{ID: "matched", ObjectURL: "s3://bucket-a/project-root/file.bin", ExpectedSizeBytes: &size17, ExpectedName: "file.bin"},
		{ID: "mismatched", ObjectURL: "s3://bucket-a/project-root/file.bin", ExpectedSizeBytes: &size99, ExpectedName: "file.bin"},
	}})
	req := withTestAuthzContext(httptest.NewRequest(http.MethodPost, "/data/inspect/bulk-list", bytes.NewBuffer(body)), "gen3", map[string]map[string]bool{
		"/organization/syfon/project/e2e": {"read": true},
	})
	rr := doInternalDRSTestRequest(req, om)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rr.Code, rr.Body.String())
	}
	var resp internalInspectObjectBulkResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if listCalls != 1 {
		t.Fatalf("expected one shared LIST call, got %d", listCalls)
	}
	if len(resp.Items) != 2 || resp.Items[0].ValidationStatus != "matched" || resp.Items[1].ValidationStatus != "mismatched" {
		t.Fatalf("expected per-request validation after shared evidence, got %+v", resp.Items)
	}
}

func TestHandleInternalInspectObjectBulkListCoalescesDensePrefixes(t *testing.T) {
	db := &testutils.MockDatabase{
		Credentials: map[string]models.S3Credential{
			"cred-1": {CredentialID: "cred-1", Bucket: "bucket-a", Provider: "s3"},
		},
		BucketScopes: map[string]models.BucketScope{
			"syfon|e2e": {Organization: "syfon", ProjectID: "e2e", Bucket: "bucket-a", CredentialID: "cred-1", PathPrefix: "project-root"},
		},
	}
	om := core.NewObjectManager(db, &testutils.MockUrlManager{})
	listCalls := 0
	om.SetS3PrefixListerWithOptions(func(ctx context.Context, cred models.S3Credential, bucket string, prefix string, options core.StoragePrefixListOptions) ([]core.StorageBucketObject, error) {
		listCalls++
		if bucket != "bucket-a" || prefix != "project-root/dense/" || !options.ExactPrefix || options.MaxKeys != 0 || options.IncludeHead {
			t.Fatalf("unexpected coalesced LIST request bucket=%q prefix=%q options=%+v", bucket, prefix, options)
		}
		out := make([]core.StorageBucketObject, 0, 25)
		for i := 0; i < 25; i++ {
			key := fmt.Sprintf("project-root/dense/file-%02d.bin", i)
			out = append(out, core.StorageBucketObject{Provider: "s3", Bucket: bucket, Key: key, Path: fmt.Sprintf("file-%02d.bin", i), SizeBytes: int64(i + 1)})
		}
		return out, nil
	})
	items := make([]internalInspectObjectRequest, 0, 25)
	for i := 0; i < 25; i++ {
		size := int64(i + 1)
		items = append(items, internalInspectObjectRequest{
			ID:                fmt.Sprintf("item-%02d", i),
			ObjectURL:         fmt.Sprintf("s3://bucket-a/project-root/dense/file-%02d.bin", i),
			ExpectedSizeBytes: &size,
			ExpectedName:      fmt.Sprintf("file-%02d.bin", i),
		})
	}
	body, _ := json.Marshal(internalInspectObjectsBulkRequest{Items: items})
	req := withTestAuthzContext(httptest.NewRequest(http.MethodPost, "/data/inspect/bulk-list", bytes.NewBuffer(body)), "gen3", map[string]map[string]bool{
		"/organization/syfon/project/e2e": {"read": true},
	})
	rr := doInternalDRSTestRequest(req, om)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rr.Code, rr.Body.String())
	}
	var resp internalInspectObjectBulkResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if listCalls != 1 {
		t.Fatalf("expected dense prefix to coalesce into one LIST call, got %d", listCalls)
	}
	if len(resp.Items) != 25 {
		t.Fatalf("expected 25 items, got %d", len(resp.Items))
	}
}
