package records

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/calypr/syfon/apigen/server/internalapi"
	"github.com/calypr/syfon/internal/access"
	"github.com/calypr/syfon/internal/persistence/sqlite"

	"github.com/calypr/syfon/internal/objects"
	"github.com/gofiber/fiber/v3"
)

func indexTestAuthContext(base context.Context, mode string, authHeader bool, privileges map[string]map[string]bool) context.Context {
	session := access.NewSession(mode)
	session.AuthHeaderPresent = authHeader
	session.AuthzEnforced = mode == "gen3" || mode == "local"
	session.SetAuthorizations(nil, privileges, session.AuthzEnforced)
	return access.WithSession(base, session)
}

func TestHandleInternalList_ScopeFilteringByReadPrivilege(t *testing.T) {
	now := time.Now().UTC()
	mockDB := &internalRecordStore{
		Objects: map[string]*objects.Record{
			"obj-allow": {Id: "obj-allow", CreatedTime: now, UpdatedTime: &now, Checksums: []objects.Checksum{{Type: "sha256", Checksum: "h1"}}},
			"obj-deny":  {Id: "obj-deny", CreatedTime: now, UpdatedTime: &now, Checksums: []objects.Checksum{{Type: "sha256", Checksum: "h2"}}},
		},
		ObjectAuthz: map[string]map[string][]string{
			"obj-allow": {"org": {"p1"}},
			"obj-deny":  {"org": {"p2"}},
		},
	}

	req := httptest.NewRequest(http.MethodGet, "/?organization=org", nil)
	ctx := indexTestAuthContext(req.Context(), "gen3", true, map[string]map[string]bool{
		"/programs/org/projects/p1": {"read": true},
	})
	req = req.WithContext(ctx)

	om := newInternalDRSObjectManager(mockDB)
	rr := doInternalDRSTestRequest(req, om)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rr.Code, rr.Body.String())
	}
	var payload struct {
		Records []internalapi.InternalRecord `json:"records"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(payload.Records) != 1 {
		t.Fatalf("expected 1 visible record, got %d", len(payload.Records))
	}
	if payload.Records[0].Did != "obj-allow" {
		t.Fatalf("expected obj-allow, got %q", payload.Records[0].Did)
	}
}

func TestHandleInternalList_ExactScopeListingDoesNotDependOnBrowseRows(t *testing.T) {
	now := time.Now().UTC()
	mockDB := &internalRecordStore{
		Objects: map[string]*objects.Record{
			"obj-scoped": {
				Id:          "obj-scoped",
				CreatedTime: now,
				UpdatedTime: &now,
				Name:        stringPtr("file.bin"),
				Checksums:   []objects.Checksum{{Type: "sha256", Checksum: "h1"}},
			},
		},
		ObjectAuthz: map[string]map[string][]string{
			"obj-scoped": {"org": {"p1"}},
		},
	}

	req := httptest.NewRequest(http.MethodGet, "/index?organization=org&project=p1", nil)
	ctx := indexTestAuthContext(req.Context(), "gen3", true, map[string]map[string]bool{
		"/programs/org/projects/p1": {"read": true},
	})
	req = req.WithContext(ctx)

	om := newInternalDRSObjectManager(mockDB)
	rr := doInternalDRSTestRequest(req, om)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rr.Code, rr.Body.String())
	}
	var payload struct {
		Records []internalapi.InternalRecord `json:"records"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(payload.Records) != 1 {
		t.Fatalf("expected 1 visible scoped record, got %d", len(payload.Records))
	}
	if payload.Records[0].Did != "obj-scoped" {
		t.Fatalf("expected obj-scoped, got %q", payload.Records[0].Did)
	}
}

func TestHandleInternalList_CanonicalizesProjectChecksumDuplicates(t *testing.T) {
	database := newInternalDRSInMemoryDB(t)
	om := newInternalDRSObjectManager(database)
	now := time.Now().UTC()
	later := now.Add(time.Minute)
	sha := "dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd"
	oldURL := "s3://ellrottlab/old/" + sha
	newURL := "s3://EllrottLab/new/" + sha

	for _, obj := range []objects.Record{
		{
			Authorizations: map[string][]string{"org": {"p1"}},

			Id:          "did-1",
			Name:        stringPtr("older.tsv"),
			CreatedTime: now,
			UpdatedTime: &now,
			Checksums:   []objects.Checksum{{Type: "sha256", Checksum: sha}},
			AccessMethods: &[]objects.AccessMethod{{
				Type:      "s3",
				AccessUrl: &objects.AccessURL{Url: oldURL},
			}},
		},
		{
			Authorizations: map[string][]string{"org": {"p1"}},

			Id:          "did-2",
			Name:        stringPtr("newer.tsv"),
			CreatedTime: later,
			UpdatedTime: &later,
			Checksums:   []objects.Checksum{{Type: "sha256", Checksum: sha}},
			AccessMethods: &[]objects.AccessMethod{{
				Type:      "s3",
				AccessUrl: &objects.AccessURL{Url: newURL},
			}},
		},
	} {
		if err := om.RegisterObjects(context.Background(), []objects.Record{obj}); err != nil {
			t.Fatalf("RegisterObjects failed: %v", err)
		}
	}

	req := httptest.NewRequest(http.MethodGet, "/index?organization=org&project=p1", nil)
	rr := doInternalDRSTestRequest(req, om)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rr.Code, rr.Body.String())
	}
	var payload struct {
		Records []internalapi.InternalRecord `json:"records"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(payload.Records) != 1 {
		t.Fatalf("expected 1 canonical record, got %d", len(payload.Records))
	}
	if payload.Records[0].Did != "did-1" {
		t.Fatalf("expected canonical did-1, got %q", payload.Records[0].Did)
	}
	if payload.Records[0].Name == nil || *payload.Records[0].Name != "newer.tsv" {
		t.Fatalf("expected latest name newer.tsv, got %+v", payload.Records[0].Name)
	}
	if payload.Records[0].NameAliases == nil || !slices.Equal(*payload.Records[0].NameAliases, []string{"older.tsv"}) {
		t.Fatalf("unexpected name aliases: %#v", payload.Records[0].NameAliases)
	}
	if payload.Records[0].AccessMethods == nil || len(*payload.Records[0].AccessMethods) != 2 {
		t.Fatalf("expected 2 merged access methods, got %#v", payload.Records[0].AccessMethods)
	}
	gotURLs := make([]string, 0, len(*payload.Records[0].AccessMethods))
	for _, method := range *payload.Records[0].AccessMethods {
		if method.AccessUrl != nil {
			gotURLs = append(gotURLs, method.AccessUrl.Url)
		}
	}
	slices.Sort(gotURLs)
	if !slices.Equal(gotURLs, []string{newURL, oldURL}) {
		t.Fatalf("unexpected merged access method urls: %#v", gotURLs)
	}
}

func TestHandleInternalList_FillsLimitAfterCanonicalizingDuplicates(t *testing.T) {
	database := newInternalDRSInMemoryDB(t)
	om := newInternalDRSObjectManager(database)
	now := time.Now().UTC()
	later := now.Add(time.Minute)
	duplicateSHA := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	uniqueSHA := "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"

	for _, obj := range []objects.Record{
		{
			Authorizations: map[string][]string{"org": {"p1"}},

			Id:          "did-1",
			Name:        stringPtr("older.tsv"),
			CreatedTime: now,
			UpdatedTime: &now,
			Checksums:   []objects.Checksum{{Type: "sha256", Checksum: duplicateSHA}},
			AccessMethods: &[]objects.AccessMethod{{
				Type:      "s3",
				AccessUrl: &objects.AccessURL{Url: "s3://bucket/old/" + duplicateSHA},
			}},
		},
		{
			Authorizations: map[string][]string{"org": {"p1"}},

			Id:          "did-2",
			Name:        stringPtr("newer.tsv"),
			CreatedTime: later,
			UpdatedTime: &later,
			Checksums:   []objects.Checksum{{Type: "sha256", Checksum: duplicateSHA}},
			AccessMethods: &[]objects.AccessMethod{{
				Type:      "s3",
				AccessUrl: &objects.AccessURL{Url: "s3://bucket/new/" + duplicateSHA},
			}},
		},
		{
			Authorizations: map[string][]string{"org": {"p1"}},

			Id:          "did-3",
			Name:        stringPtr("unique.tsv"),
			CreatedTime: later,
			UpdatedTime: &later,
			Checksums:   []objects.Checksum{{Type: "sha256", Checksum: uniqueSHA}},
			AccessMethods: &[]objects.AccessMethod{{
				Type:      "s3",
				AccessUrl: &objects.AccessURL{Url: "s3://bucket/unique/" + uniqueSHA},
			}},
		},
	} {
		if err := om.RegisterObjects(context.Background(), []objects.Record{obj}); err != nil {
			t.Fatalf("RegisterObjects failed: %v", err)
		}
	}

	req := httptest.NewRequest(http.MethodGet, "/index?organization=org&project=p1&limit=2", nil)
	rr := doInternalDRSTestRequest(req, om)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rr.Code, rr.Body.String())
	}
	var payload struct {
		Records []internalapi.InternalRecord `json:"records"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(payload.Records) != 2 {
		t.Fatalf("expected filled canonical page of 2 records, got %d: %+v", len(payload.Records), payload.Records)
	}
	if payload.Records[0].Did != "did-1" || payload.Records[1].Did != "did-3" {
		t.Fatalf("unexpected canonical page ids: %+v", []string{payload.Records[0].Did, payload.Records[1].Did})
	}

	req = httptest.NewRequest(http.MethodGet, "/index?organization=org&project=p1&limit=1&start=did-1", nil)
	rr = doInternalDRSTestRequest(req, om)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rr.Code, rr.Body.String())
	}
	payload.Records = nil
	if err := json.NewDecoder(rr.Body).Decode(&payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(payload.Records) != 1 || payload.Records[0].Did != "did-3" {
		t.Fatalf("expected pagination to skip duplicate sibling and return did-3, got %+v", payload.Records)
	}
}

func TestHandleInternalList_MergesSiblingAccessMethodsFromLegacyDuplicateRows(t *testing.T) {
	database := newInternalDRSInMemoryDB(t)
	om := newInternalDRSObjectManager(database)
	now := time.Now().UTC()
	later := now.Add(time.Minute)
	sha := "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"
	oldURL := "s3://ellrottlab/fa4ee697-f689-5291-a7cc-8ebe2f3ea8b9/" + sha
	newURL := "s3://EllrottLab/" + sha
	controlled := []string{"/organization/org/project/p1"}

	for _, obj := range []objects.Record{
		{

			Id:               "did-legacy-1",
			Name:             stringPtr("legacy.tsv"),
			CreatedTime:      now,
			UpdatedTime:      &now,
			Checksums:        []objects.Checksum{{Type: "sha256", Checksum: sha}},
			ControlledAccess: &controlled,
			AccessMethods: &[]objects.AccessMethod{{
				Type:      "s3",
				AccessUrl: &objects.AccessURL{Url: oldURL},
			}},
			Authorizations: map[string][]string{"org": {"p1"}},
		},
		{

			Id:               "did-legacy-2",
			Name:             stringPtr("canonical.tsv"),
			CreatedTime:      later,
			UpdatedTime:      &later,
			Checksums:        []objects.Checksum{{Type: "sha256", Checksum: sha}},
			ControlledAccess: &controlled,
			AccessMethods: &[]objects.AccessMethod{{
				Type:      "s3",
				AccessUrl: &objects.AccessURL{Url: newURL},
			}},
			Authorizations: map[string][]string{"org": {"p1"}},
		},
	} {
		candidate := obj
		if err := database.CreateObject(context.Background(), &candidate); err != nil {
			t.Fatalf("CreateObject failed: %v", err)
		}
	}

	req := httptest.NewRequest(http.MethodGet, "/index?organization=org&project=p1", nil)
	rr := doInternalDRSTestRequest(req, om)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rr.Code, rr.Body.String())
	}
	var payload struct {
		Records []internalapi.InternalRecord `json:"records"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(payload.Records) != 1 {
		t.Fatalf("expected 1 canonical record, got %d", len(payload.Records))
	}
	if payload.Records[0].AccessMethods == nil || len(*payload.Records[0].AccessMethods) != 2 {
		t.Fatalf("expected 2 merged access methods, got %#v", payload.Records[0].AccessMethods)
	}
	gotURLs := make([]string, 0, len(*payload.Records[0].AccessMethods))
	for _, method := range *payload.Records[0].AccessMethods {
		if method.AccessUrl != nil {
			gotURLs = append(gotURLs, method.AccessUrl.Url)
		}
	}
	slices.Sort(gotURLs)
	if !slices.Equal(gotURLs, []string{newURL, oldURL}) {
		t.Fatalf("unexpected merged access method urls: %#v", gotURLs)
	}
}

func TestHandleInternalList_PaginatesIDs(t *testing.T) {
	now := time.Now().UTC()
	mockDB := &internalRecordStore{
		Objects: map[string]*objects.Record{
			"obj-1": {Id: "obj-1", CreatedTime: now, UpdatedTime: &now, Checksums: []objects.Checksum{{Type: "sha256", Checksum: "h1"}}, Properties: map[string]json.RawMessage{"large": json.RawMessage(`9007199254740993`), "auth": json.RawMessage(`{"retired":true}`)}},
			"obj-2": {Id: "obj-2", CreatedTime: now, UpdatedTime: &now, Checksums: []objects.Checksum{{Type: "sha256", Checksum: "h2"}}},
			"obj-3": {Id: "obj-3", CreatedTime: now, UpdatedTime: &now, Checksums: []objects.Checksum{{Type: "sha256", Checksum: "h3"}}},
		},
	}
	app := fiber.New()
	om := newInternalDRSObjectManager(mockDB)
	RegisterRoutes(app, om.ObjectService)

	req := httptest.NewRequest(http.MethodGet, "/index?limit=1&start=obj-1", nil)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("test request failed: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 200, got %d body=%s", resp.StatusCode, string(body))
	}
	var payload internalapi.ListRecordsResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if payload.Records == nil || len(*payload.Records) != 1 {
		t.Fatalf("expected one paged record, got %+v", payload.Records)
	}
	if (*payload.Records)[0].Did != "obj-2" {
		t.Fatalf("expected obj-2 after start cursor, got %+v", (*payload.Records)[0])
	}
}

func TestHandleInternalList_FiltersByAccessURL(t *testing.T) {
	now := time.Now().UTC()
	offsetsURL := "s3://bucket/path/image.offsets.json"
	sourceURL := "s3://bucket/path/image.ome.tif"
	offsetsAccessID := "offsets-s3"
	sourceAccessID := "source-s3"
	mockDB := &internalRecordStore{
		Objects: map[string]*objects.Record{
			"offsets": {
				Id:          "offsets",
				CreatedTime: now,
				UpdatedTime: &now,
				Checksums:   []objects.Checksum{{Type: "sha256", Checksum: "offsets-hash"}},
				AccessMethods: &[]objects.AccessMethod{{
					Type:     "s3",
					AccessId: &offsetsAccessID,
					AccessUrl: &objects.AccessURL{

						Url: offsetsURL},
				}},
			},
			"source": {
				Id:          "source",
				CreatedTime: now,
				UpdatedTime: &now,
				Checksums:   []objects.Checksum{{Type: "sha256", Checksum: "source-hash"}},
				AccessMethods: &[]objects.AccessMethod{{
					Type:     "s3",
					AccessId: &sourceAccessID,
					AccessUrl: &objects.AccessURL{

						Url: sourceURL},
				}},
			},
		},
	}
	app := fiber.New()
	om := newInternalDRSObjectManager(mockDB)
	RegisterRoutes(app, om.ObjectService)

	req := httptest.NewRequest(http.MethodGet, "/index?url="+url.QueryEscape(offsetsURL), nil)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("test request failed: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 200, got %d body=%s", resp.StatusCode, string(body))
	}
	var payload internalapi.ListRecordsResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if payload.Records == nil || len(*payload.Records) != 1 {
		t.Fatalf("expected one record for access URL, got %+v", payload.Records)
	}
	if (*payload.Records)[0].Did != "offsets" {
		t.Fatalf("expected offsets record, got %+v", (*payload.Records)[0])
	}
}

func TestHandleInternalList_PagePaginatesIDs(t *testing.T) {
	now := time.Now().UTC()
	mockDB := &internalRecordStore{
		Objects: map[string]*objects.Record{
			"obj-1": {Id: "obj-1", CreatedTime: now, UpdatedTime: &now, Checksums: []objects.Checksum{{Type: "sha256", Checksum: "h1"}}},
			"obj-2": {Id: "obj-2", CreatedTime: now, UpdatedTime: &now, Checksums: []objects.Checksum{{Type: "sha256", Checksum: "h2"}}},
			"obj-3": {Id: "obj-3", CreatedTime: now, UpdatedTime: &now, Checksums: []objects.Checksum{{Type: "sha256", Checksum: "h3"}}},
		},
	}
	app := fiber.New()
	om := newInternalDRSObjectManager(mockDB)
	RegisterRoutes(app, om.ObjectService)

	req := httptest.NewRequest(http.MethodGet, "/index?limit=1&page=1", nil)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("test request failed: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 200, got %d body=%s", resp.StatusCode, string(body))
	}
	var payload internalapi.ListRecordsResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if payload.Records == nil || len(*payload.Records) != 1 {
		t.Fatalf("expected one paged record, got %+v", payload.Records)
	}
	if (*payload.Records)[0].Did != "obj-2" {
		t.Fatalf("expected obj-2 on page 1 with zero-based offset, got %+v", (*payload.Records)[0])
	}
}

func TestHandleInternalList_LimitIsCappedAtTenThousand(t *testing.T) {
	now := time.Now().UTC()
	total := maxInternalListLimit + 1
	records := make(map[string]*objects.Record, total)
	for i := 0; i < total; i++ {
		id := fmt.Sprintf("obj-%05d", i)
		records[id] = &objects.Record{
			Id:          objects.RecordID(id),
			CreatedTime: now,
			UpdatedTime: &now,
			Checksums:   []objects.Checksum{{Type: "sha256", Checksum: id}},
		}
	}

	mockDB := &internalRecordStore{Objects: records}
	app := fiber.New()
	om := newInternalDRSObjectManager(mockDB)
	RegisterRoutes(app, om.ObjectService)

	req := httptest.NewRequest(http.MethodGet, "/index?limit=999999", nil)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("test request failed: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 200, got %d body=%s", resp.StatusCode, string(body))
	}
	var payload internalapi.ListRecordsResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if payload.Records == nil || len(*payload.Records) != maxInternalListLimit {
		got := 0
		if payload.Records != nil {
			got = len(*payload.Records)
		}
		t.Fatalf("expected capped limit %d, got %d", maxInternalListLimit, got)
	}
}

func TestHandleInternalList_IgnoresLegacyPathQuery(t *testing.T) {
	now := time.Now().UTC()
	mockDB := &internalRecordStore{
		Objects: map[string]*objects.Record{
			"obj-a": {Id: "obj-a", Name: ptr("nested/a.txt"), CreatedTime: now, UpdatedTime: &now, Checksums: []objects.Checksum{{Type: "sha256", Checksum: "a"}}},
			"obj-b": {Id: "obj-b", Name: ptr("nested/deep/b.txt"), CreatedTime: now, UpdatedTime: &now, Checksums: []objects.Checksum{{Type: "sha256", Checksum: "b"}}},
			"obj-c": {Id: "obj-c", Name: ptr("root.txt"), CreatedTime: now, UpdatedTime: &now, Checksums: []objects.Checksum{{Type: "sha256", Checksum: "c"}}},
			"obj-d": {Id: "obj-d", Name: ptr("nested/z.txt"), CreatedTime: now, UpdatedTime: &now, Checksums: []objects.Checksum{{Type: "sha256", Checksum: "d"}}},
		},
		ObjectAuthz: map[string]map[string][]string{
			"obj-a": {"org-a": {"proj-a"}},
			"obj-b": {"org-a": {"proj-a"}},
			"obj-c": {"org-a": {"proj-a"}},
			"obj-d": {"org-a": {"proj-a"}},
		},
	}
	app := fiber.New()
	om := newInternalDRSObjectManager(mockDB)
	RegisterRoutes(app, om.ObjectService)

	req := httptest.NewRequest(http.MethodGet, "/index?organization=org-a&project=proj-a&path=nested&limit=1", nil)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("test request failed: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 200, got %d body=%s", resp.StatusCode, string(body))
	}
	var payload internalapi.ListRecordsResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if payload.Records == nil || len(*payload.Records) != 1 {
		t.Fatalf("expected one paged record, got %+v", payload.Records)
	}

	req = httptest.NewRequest(http.MethodGet, "/index?organization=org-a&project=proj-a&path=nested&limit=1&start=obj-a", nil)
	resp, err = app.Test(req)
	if err != nil {
		t.Fatalf("test request with start failed: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 200, got %d body=%s", resp.StatusCode, string(body))
	}
	payload = internalapi.ListRecordsResponse{}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("decode paged response: %v", err)
	}
	if payload.Records == nil || len(*payload.Records) != 1 {
		t.Fatalf("expected one paged record with start cursor, got %+v", payload.Records)
	}
}

func TestHandleInternalList_HashTypeFiltering(t *testing.T) {
	now := time.Now().UTC()
	mockDB := &internalRecordStore{
		Objects: map[string]*objects.Record{
			"obj-sha": {
				Id:          "obj-sha",
				CreatedTime: now,
				UpdatedTime: &now,
				Checksums:   []objects.Checksum{{Type: "sha256", Checksum: "samehash"}},
			},
			"obj-md5": {
				Id:          "obj-md5",
				CreatedTime: now,
				UpdatedTime: &now,
				Checksums:   []objects.Checksum{{Type: "md5", Checksum: "samehash"}},
			},
		},
	}

	req := httptest.NewRequest(http.MethodGet, "/?hash=sha256:samehash", nil)
	om := newInternalDRSObjectManager(mockDB)
	rr := doInternalDRSTestRequest(req, om)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rr.Code, rr.Body.String())
	}
	var payload internalapi.ListRecordsResponse
	if err := json.NewDecoder(rr.Body).Decode(&payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if payload.Records == nil || len(*payload.Records) != 1 {
		t.Fatalf("expected 1 record, got %+v", payload.Records)
	}
	if got := (*payload.Records)[0].Did; got != "obj-sha" {
		t.Fatalf("expected obj-sha, got %q", got)
	}

	req = httptest.NewRequest(http.MethodGet, "/?hash=samehash&hash_type=md5", nil)
	om2 := newInternalDRSObjectManager(mockDB)
	rr = doInternalDRSTestRequest(req, om2)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rr.Code, rr.Body.String())
	}
	payload = internalapi.ListRecordsResponse{}
	if err := json.NewDecoder(rr.Body).Decode(&payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if payload.Records == nil || len(*payload.Records) != 1 {
		t.Fatalf("expected 1 record, got %+v", payload.Records)
	}
	if got := (*payload.Records)[0].Did; got != "obj-md5" {
		t.Fatalf("expected obj-md5, got %q", got)
	}
}

func TestHandleInternalList_ScopedFiltersKeepProjectPhysicalRecord(t *testing.T) {
	fixturePath := filepath.Join(t.TempDir(), "legacy.sqlite")
	database, err := sqlite.NewSqliteDB(fixturePath)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := sql.Open("sqlite3", fixturePath)
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	om := newInternalDRSObjectManager(database)
	sha := strings.Repeat("a", 64)
	projectAResource := "/organization/org/project/p1"
	projectBResource := "/organization/org/project/p2"
	projectAURL := "s3://bucket/project-a"
	projectBURL := "s3://bucket/project-b"
	now := time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)
	later := now.Add(time.Minute)
	for _, obj := range []objects.Record{
		{
			Authorizations: map[string][]string{"org": {"p1"}},

			Id:               "project-a-did",
			CreatedTime:      now,
			UpdatedTime:      &now,
			Checksums:        []objects.Checksum{{Type: "sha256", Checksum: sha}},
			ControlledAccess: &[]string{projectAResource},
			AccessMethods: &[]objects.AccessMethod{{
				Type:      "s3",
				AccessUrl: &objects.AccessURL{Url: projectAURL},
			}},
		},
		{
			Authorizations: map[string][]string{"org": {"p2"}},

			Id:               "project-b-did",
			CreatedTime:      later,
			UpdatedTime:      &later,
			Checksums:        []objects.Checksum{{Type: "sha256", Checksum: sha}},
			ControlledAccess: &[]string{projectBResource},
			AccessMethods: &[]objects.AccessMethod{{
				Type:      "s3",
				AccessUrl: &objects.AccessURL{Url: projectBURL},
			}},
		},
	} {
		if _, err := raw.Exec(`INSERT INTO drs_object (id,size,created_time,updated_time,name,version,description) VALUES (?,0,?,?, '', '', '')`, obj.Id, obj.CreatedTime, *obj.UpdatedTime); err != nil {
			t.Fatal(err)
		}
		if _, err := raw.Exec(`INSERT INTO drs_object_checksum (object_id,type,checksum) VALUES (?, 'sha256', ?)`, obj.Id, sha); err != nil {
			t.Fatal(err)
		}
		if _, err := raw.Exec(`INSERT INTO drs_object_controlled_access (object_id,resource) VALUES (?, ?)`, obj.Id, (*obj.ControlledAccess)[0]); err != nil {
			t.Fatal(err)
		}
		if _, err := raw.Exec(`INSERT INTO drs_object_access_method (object_id,type,url) VALUES (?, 's3', ?)`, obj.Id, (*obj.AccessMethods)[0].AccessUrl.Url); err != nil {
			t.Fatal(err)
		}
	}

	queries := []string{
		"/index?hash=sha256:" + sha + "&organization=org&project=p1",
		"/index?url=" + url.QueryEscape(projectAURL) + "&organization=org&project=p1",
	}
	for _, query := range queries {
		t.Run(query, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, query, nil)
			rr := doInternalDRSTestRequest(req, om)
			if rr.Code != http.StatusOK {
				t.Fatalf("expected 200, got %d body=%s", rr.Code, rr.Body.String())
			}
			var payload internalapi.ListRecordsResponse
			if err := json.NewDecoder(rr.Body).Decode(&payload); err != nil {
				t.Fatalf("decode response: %v", err)
			}
			if payload.Records == nil || len(*payload.Records) != 1 {
				t.Fatalf("expected one project record, got %+v", payload.Records)
			}
			got := (*payload.Records)[0]
			if got.Did != "project-a-did" || got.AccessMethods == nil || len(*got.AccessMethods) != 1 || (*got.AccessMethods)[0].AccessUrl == nil || (*got.AccessMethods)[0].AccessUrl.Url != projectAURL {
				t.Fatalf("scoped response included another project's canonical data: %+v", got)
			}
		})
	}
}

func TestFromInternalRecord_NormalizesSHA256(t *testing.T) {
	upper := strings.ToUpper(strings.Repeat("ab", 32))
	hashes := internalapi.HashInfo{"SHA-256": "sha256:" + upper}
	obj, err := FromInternalRecord(internalapi.InternalRecord{
		Did:    "normalized-checksum",
		Hashes: &hashes,
	}, time.Now().UTC())
	if err != nil {
		t.Fatalf("FromInternalRecord failed: %v", err)
	}
	if len(obj.Checksums) != 1 || obj.Checksums[0].Type != "sha256" || obj.Checksums[0].Checksum != strings.ToLower(upper) {
		t.Fatalf("checksum was not canonicalized: %+v", obj.Checksums)
	}
}

func TestHandleInternalList_HashPagination(t *testing.T) {
	now := time.Now().UTC()
	mockDB := &internalRecordStore{
		Objects: map[string]*objects.Record{
			"obj-1": {Id: "obj-1", CreatedTime: now, UpdatedTime: &now, Checksums: []objects.Checksum{{Type: "sha256", Checksum: "samehash"}}},
			"obj-2": {Id: "obj-2", CreatedTime: now, UpdatedTime: &now, Checksums: []objects.Checksum{{Type: "sha256", Checksum: "samehash"}}},
			"obj-3": {Id: "obj-3", CreatedTime: now, UpdatedTime: &now, Checksums: []objects.Checksum{{Type: "sha256", Checksum: "samehash"}}},
		},
	}
	app := fiber.New()
	om := newInternalDRSObjectManager(mockDB)
	RegisterRoutes(app, om.ObjectService)

	req := httptest.NewRequest(http.MethodGet, "/index?hash=sha256:samehash&limit=1&page=1", nil)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("test request failed: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 200, got %d body=%s", resp.StatusCode, string(body))
	}
	var payload internalapi.ListRecordsResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if payload.Records == nil || len(*payload.Records) != 1 {
		t.Fatalf("expected one paged record, got %+v", payload.Records)
	}
	if (*payload.Records)[0].Did != "obj-2" {
		t.Fatalf("expected obj-2 on paged hash lookup, got %+v", (*payload.Records)[0])
	}
}

func TestHandleInternalBulkHashes_HashTypeFiltering(t *testing.T) {
	now := time.Now().UTC()
	mockDB := &internalRecordStore{
		Objects: map[string]*objects.Record{
			"obj-sha": {
				Id:          "obj-sha",
				CreatedTime: now,
				UpdatedTime: &now,
				Checksums:   []objects.Checksum{{Type: "sha256", Checksum: "samehash"}},
			},
			"obj-md5": {
				Id:          "obj-md5",
				CreatedTime: now,
				UpdatedTime: &now,
				Checksums:   []objects.Checksum{{Type: "md5", Checksum: "samehash"}},
			},
		},
	}

	reqBody := `{"hashes":["sha256:samehash"]}`
	req := httptest.NewRequest(http.MethodPost, "/bulk/hashes", strings.NewReader(reqBody))
	req.Header.Set("Content-Type", "application/json")
	om := newInternalDRSObjectManager(mockDB)
	rr := doInternalDRSTestRequestWithAlias(req, om, http.MethodPost, "/bulk/hashes", handleInternalBulkHashesFiber(om.ObjectService))

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rr.Code, rr.Body.String())
	}
	var payload struct {
		Results map[string][]internalapi.InternalRecord `json:"results"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(payload.Results) != 1 {
		t.Fatalf("expected 1 result key, got %d", len(payload.Results))
	}
	objs := payload.Results["sha256:samehash"]
	if len(objs) != 1 {
		t.Fatalf("expected 1 record for hash, got %d", len(objs))
	}
	if objs[0].Did != "obj-sha" {
		t.Fatalf("expected obj-sha, got %q", objs[0].Did)
	}
	if objs[0].Hashes == nil || (*objs[0].Hashes)["sha256"] != "samehash" {
		t.Fatalf("expected sha256 hash, got %+v", objs[0].Hashes)
	}
	raw := rr.Body.String()
	if strings.Contains(raw, `"id"`) || strings.Contains(raw, `"checksums"`) {
		t.Fatalf("expected legacy compatibility fields only, got %s", raw)
	}
}

func TestHandleInternalBulkSHA256Validity(t *testing.T) {
	now := time.Now().UTC()
	mockDB := &internalRecordStore{
		Objects: map[string]*objects.Record{
			"obj-sha": {
				Id:          "obj-sha",
				CreatedTime: now,
				UpdatedTime: &now,
				Checksums:   []objects.Checksum{{Type: "sha256", Checksum: "present"}},
			},
			"obj-md5": {
				Id:          "obj-md5",
				CreatedTime: now,
				UpdatedTime: &now,
				Checksums:   []objects.Checksum{{Type: "md5", Checksum: "md5-only"}},
			},
		},
	}

	req := httptest.NewRequest(http.MethodPost, "/index/bulk/sha256/validity", strings.NewReader(`{"sha256":["present","md5-only","missing"]}`))
	req.Header.Set("Content-Type", "application/json")
	app := fiber.New()
	om := newInternalDRSObjectManager(mockDB)
	app.Post("/index/bulk/sha256/validity", handleInternalBulkSHA256ValidityFiber(om.ObjectService))
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 200, got %d body=%s", resp.StatusCode, string(body))
	}
	var payload map[string]bool
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !payload["present"] {
		t.Fatalf("expected present=true, got %+v", payload)
	}
	if payload["md5-only"] {
		t.Fatalf("expected md5-only=false, got %+v", payload)
	}
	if payload["missing"] {
		t.Fatalf("expected missing=false, got %+v", payload)
	}
}

func TestHandleInternalBulkMissingSHA256(t *testing.T) {
	present := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	missing := "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	now := time.Now().UTC()
	mockDB := &internalRecordStore{Objects: map[string]*objects.Record{
		"obj-sha": {
			Id:          "obj-sha",
			CreatedTime: now,
			UpdatedTime: &now,
			Checksums:   []objects.Checksum{{Type: "sha256", Checksum: present}},
		},
	}, ObjectAuthz: map[string]map[string][]string{
		"obj-sha": {"org": {"project"}},
	}}
	om := newInternalDRSObjectManager(mockDB)
	app := fiber.New()
	app.Post("/index/bulk/sha256/missing", handleInternalBulkMissingSHA256Fiber(om.ObjectService))
	req := httptest.NewRequest(http.MethodPost, "/index/bulk/sha256/missing", strings.NewReader(`{"organization":"org","project":"project","sha256":["SHA256:`+present+`","`+missing+`","`+missing+`"]}`))
	req.Header.Set("Content-Type", "application/json")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 200, got %d body=%s", resp.StatusCode, string(body))
	}
	var payload internalapi.BulkMissingSHA256Response
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if payload.Checked != 2 || !slices.Equal(payload.MissingSha256, []string{missing}) {
		t.Fatalf("unexpected response: %+v", payload)
	}
}

func TestHandleInternalBulkMissingSHA256RejectsInvalidChecksum(t *testing.T) {
	om := newInternalDRSObjectManager(&internalRecordStore{})
	app := fiber.New()
	app.Post("/index/bulk/sha256/missing", handleInternalBulkMissingSHA256Fiber(om.ObjectService))
	req := httptest.NewRequest(http.MethodPost, "/index/bulk/sha256/missing", strings.NewReader(`{"organization":"org","project":"project","sha256":["not-a-sha256"]}`))
	req.Header.Set("Content-Type", "application/json")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}
}

func TestHandleInternalCreate_PersistsControlledAccess(t *testing.T) {
	mockDB := &internalRecordStore{Objects: map[string]*objects.Record{}}
	reqBody := `{"records":[{"did":"obj-1","size":42,"controlled_access":["https://calypr.org/program/test/project/p1"],"access_methods":[{"type":"s3","access_url":{"url":"s3://bucket/path/obj-1"}}]}]}`
	req := httptest.NewRequest(http.MethodPost, "/index", strings.NewReader(reqBody))
	req.Header.Set("Content-Type", "application/json")
	om := newInternalDRSObjectManager(mockDB)
	rr := doInternalDRSTestRequest(req, om)

	if rr.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d body=%s", rr.Code, rr.Body.String())
	}

	if got := mockDB.ObjectAuthz["obj-1"]; len(got["test"]) != 1 || got["test"][0] != "p1" {
		t.Fatalf("expected persisted authz, got %v", got)
	}
}

func TestHandleInternalCreate_RequiredFieldsFailAtDecode(t *testing.T) {
	t.Run("missing records", func(t *testing.T) {
		mockDB := &internalRecordStore{Objects: map[string]*objects.Record{}}
		reqBody := `{"size":42,"auth":{"test":{"p1":["s3://bucket/path/obj"]}}}`
		req := httptest.NewRequest(http.MethodPost, "/index", strings.NewReader(reqBody))
		req.Header.Set("Content-Type", "application/json")
		om := newInternalDRSObjectManager(mockDB)
		rr := doInternalDRSTestRequest(req, om)

		if rr.Code != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d body=%s", rr.Code, rr.Body.String())
		}
	})
}

func TestHandleInternalBulkCreate_PersistsControlledAccess(t *testing.T) {
	mockDB := &internalRecordStore{Objects: map[string]*objects.Record{}}
	reqBody := `{"records":[{"did":"obj-bulk-1","size":7,"controlled_access":["/programs/test/projects/p1"],"access_methods":[{"type":"s3","access_url":{"url":"s3://bucket/path/obj-bulk-1"}}]}]}`
	req := httptest.NewRequest(http.MethodPost, "/bulk/create", strings.NewReader(reqBody))
	req.Header.Set("Content-Type", "application/json")
	om := newInternalDRSObjectManager(mockDB)
	rr := doInternalDRSTestRequestWithAlias(req, om, http.MethodPost, "/bulk/create", handleInternalBulkCreateFiber(om.ObjectService))

	if rr.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d body=%s", rr.Code, rr.Body.String())
	}

	if got := mockDB.ObjectAuthz["obj-bulk-1"]; len(got["test"]) != 1 || got["test"][0] != "p1" {
		t.Fatalf("expected persisted authz, got %v", got)
	}
}

func TestHandleInternalBulkCreate_OrganizationProjectAddsCanonicalControlledAccess(t *testing.T) {
	mockDB := &internalRecordStore{Objects: map[string]*objects.Record{}}
	reqBody := `{"records":[{"did":"obj-bulk-2","organization":"test","project":"p2","size":7,"access_methods":[{"type":"s3","access_url":{"url":"s3://bucket/path/obj-bulk-2"}}]}]}`
	req := httptest.NewRequest(http.MethodPost, "/index/bulk", strings.NewReader(reqBody))
	req.Header.Set("Content-Type", "application/json")
	om := newInternalDRSObjectManager(mockDB)
	rr := doInternalDRSTestRequest(req, om)

	if rr.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d body=%s", rr.Code, rr.Body.String())
	}
	if got := mockDB.ObjectAuthz["obj-bulk-2"]; len(got["test"]) != 1 || got["test"][0] != "p2" {
		t.Fatalf("expected canonical project authz, got %v", got)
	}
}

func TestHandleInternalUpdate_OrganizationProjectAddsCanonicalControlledAccess(t *testing.T) {
	now := time.Now().UTC()
	mockDB := &internalRecordStore{
		Objects: map[string]*objects.Record{
			"obj-update": {Id: "obj-update", CreatedTime: now, UpdatedTime: &now},
		},
	}
	reqBody := `{"did":"obj-update","organization":"test","project":"p3"}`
	req := httptest.NewRequest(http.MethodPut, "/index/obj-update", strings.NewReader(reqBody))
	req.Header.Set("Content-Type", "application/json")
	om := newInternalDRSObjectManager(mockDB)
	rr := doInternalDRSTestRequest(req, om)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rr.Code, rr.Body.String())
	}
	if got := mockDB.ObjectAuthz["obj-update"]; len(got["test"]) != 1 || got["test"][0] != "p3" {
		t.Fatalf("expected canonical project authz after update, got %v", got)
	}
}

func TestHandleInternalBulkCreate_AllowsCreateAccessForAnyControlledAccessScope(t *testing.T) {
	ctx := indexTestAuthContext(context.Background(), "gen3", true, map[string]map[string]bool{
		"/programs/test/projects/p1": {"create": true},
	})
	obj, err := FromInternalRecord(internalapi.InternalRecord{
		Did:              "obj-bulk-denied",
		Size:             ptr(int64(7)),
		ControlledAccess: &[]string{"/programs/test/projects/p1", "/programs/test/projects/p2"},
	}, time.Now().UTC())
	if err != nil {
		t.Fatalf("FromInternalRecord failed: %v", err)
	}

	mockDB := &internalRecordStore{}
	om := newInternalDRSObjectManager(mockDB)
	if err := om.RegisterObjects(ctx, []objects.Record{obj}); err != nil {
		t.Fatalf("expected object manager create policy to allow when one controlled_access scope matches: %v", err)
	}
	if _, ok := mockDB.Objects[string(obj.Id)]; !ok {
		t.Fatal("expected object to be registered")
	}
}

func TestHandleInternalBulkCreate_ReportsDeniedCreateResources(t *testing.T) {
	reqBody := `{"records":[{"did":"obj-denied","size":7,"controlled_access":["/programs/test/projects/p2"],"access_methods":[{"type":"s3","access_url":{"url":"s3://bucket/path/obj-denied"}}]}]}`
	req := httptest.NewRequest(http.MethodPost, "/index/bulk", strings.NewReader(reqBody))
	req.Header.Set("Content-Type", "application/json")
	ctx := indexTestAuthContext(req.Context(), "gen3", true, map[string]map[string]bool{
		"/programs/test/projects/p1": {"create": true},
	})
	req = req.WithContext(ctx)

	mockDB := &internalRecordStore{Objects: map[string]*objects.Record{}}
	om := newInternalDRSObjectManager(mockDB)
	rr := doInternalDRSTestRequest(req, om)

	if rr.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d body=%s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	if !strings.Contains(body, "obj-denied") || !strings.Contains(body, "test/p2") {
		t.Fatalf("expected denied resource detail in response body, got %q", body)
	}
	if strings.Contains(body, "/programs/test/projects/p2") {
		t.Fatalf("expected user-facing organization/project scope, got raw resource path in %q", body)
	}
	if _, ok := mockDB.Objects["obj-denied"]; ok {
		t.Fatal("unauthorized bulk create wrote object")
	}
}

func TestHandleInternalBulkOverwrite_ReplacesProjectChecksumSibling(t *testing.T) {
	sha := "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
	oldName := "old"
	mockDB := &internalRecordStore{
		Objects: map[string]*objects.Record{
			"target-did": {Id: "target-did", Name: &oldName, Checksums: []objects.Checksum{{Type: "sha256", Checksum: sha}}},
		},
		ObjectAuthz: map[string]map[string][]string{"target-did": {"test": {"p1"}}},
	}
	body := `{"organization":"test","project":"p1","records":[{"did":"source-did","name":"new","hashes":{"sha256":"` + sha + `"},"controlled_access":["/programs/test/projects/p1"]}]}`
	req := httptest.NewRequest(http.MethodPut, "/index/bulk/overwrite", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(indexTestAuthContext(req.Context(), "gen3", true, map[string]map[string]bool{
		"/programs/test/projects/p1": {"update": true},
	}))
	om := newInternalDRSObjectManager(mockDB)
	rr := doInternalDRSTestRequest(req, om)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rr.Code, rr.Body.String())
	}
	if _, ok := mockDB.Objects["source-did"]; ok {
		t.Fatal("expected checksum sibling to preserve target DID")
	}
	if got := mockDB.Objects["target-did"]; got == nil || got.Name == nil || *got.Name != "new" {
		t.Fatalf("expected target metadata replacement, got %+v", got)
	}
}

func TestHandleInternalBulkOverwrite_AppliesTopLevelScope(t *testing.T) {
	body := `{"organization":"test","project":"p1","records":[{"did":"source-did","name":"new"}]}`
	req := httptest.NewRequest(http.MethodPut, "/index/bulk/overwrite", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(indexTestAuthContext(req.Context(), "gen3", true, map[string]map[string]bool{
		"/programs/test/projects/p1": {"create": true},
	}))
	mockDB := &internalRecordStore{Objects: map[string]*objects.Record{}}
	om := newInternalDRSObjectManager(mockDB)
	rr := doInternalDRSTestRequest(req, om)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rr.Code, rr.Body.String())
	}
	if got := mockDB.ObjectAuthz["source-did"]; !slices.Equal(got["test"], []string{"p1"}) {
		t.Fatalf("expected top-level scope on stored record, got %+v", got)
	}
	var response bulkOverwriteResponse
	if err := json.NewDecoder(rr.Body).Decode(&response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.Processed != 1 || response.Created != 1 {
		t.Fatalf("unexpected response: %+v", response)
	}
}

func TestHandleInternalBulkOverwrite_ValidatesRequest(t *testing.T) {
	tests := []struct {
		name   string
		body   string
		status int
	}{
		{name: "malformed json", body: `{`, status: http.StatusBadRequest},
		{name: "missing scope", body: `{"records":[{"did":"did-1"}]}`, status: http.StatusBadRequest},
		{name: "invalid record", body: `{"organization":"test","project":"p1","records":[{"did":""}]}`, status: http.StatusBadRequest},
	}
	tooMany := make([]internalapi.InternalRecord, maxInternalBulkOverwrite+1)
	for i := range tooMany {
		tooMany[i].Did = fmt.Sprintf("did-%d", i)
	}
	payload, err := json.Marshal(bulkOverwriteRequest{Organization: "test", Project: "p1", Records: tooMany})
	if err != nil {
		t.Fatal(err)
	}
	tests = append(tests, struct {
		name   string
		body   string
		status int
	}{name: "too many records", body: string(payload), status: http.StatusRequestEntityTooLarge})

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPut, "/index/bulk/overwrite", strings.NewReader(tc.body))
			req.Header.Set("Content-Type", "application/json")
			rr := doInternalDRSTestRequest(req, newInternalDRSObjectManager(&internalRecordStore{}))
			if rr.Code != tc.status {
				t.Fatalf("expected %d, got %d body=%s", tc.status, rr.Code, rr.Body.String())
			}
		})
	}
}

func TestHandleInternalDeleteByQuery(t *testing.T) {
	t.Run("requires scope query", func(t *testing.T) {
		mockDB := &internalRecordStore{}
		req := httptest.NewRequest(http.MethodDelete, "/", nil)
		om := newInternalDRSObjectManager(mockDB)
		rr := doInternalDRSTestRequest(req, om)

		if rr.Code != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d", rr.Code)
		}
	})

	t.Run("requires auth header in gen3 mode", func(t *testing.T) {
		mockDB := &internalRecordStore{}
		req := httptest.NewRequest(http.MethodDelete, "/?organization=org", nil)
		ctx := indexTestAuthContext(req.Context(), "gen3", false, nil)
		req = req.WithContext(ctx)
		om := newInternalDRSObjectManager(mockDB)
		rr := doInternalDRSTestRequest(req, om)

		if rr.Code != http.StatusUnauthorized {
			t.Fatalf("expected 401, got %d body=%s", rr.Code, rr.Body.String())
		}
	})

	t.Run("deletes only authorized scoped records", func(t *testing.T) {
		now := time.Now().UTC()
		mockDB := &internalRecordStore{
			Objects: map[string]*objects.Record{
				"obj-1": {Id: "obj-1", CreatedTime: now, UpdatedTime: &now},
				"obj-2": {Id: "obj-2", CreatedTime: now, UpdatedTime: &now},
			},
			ObjectAuthz: map[string]map[string][]string{
				"obj-1": {"org": {"a"}},
				"obj-2": {"org": {"a"}},
			},
		}
		req := httptest.NewRequest(http.MethodDelete, "/?organization=org&project=a", nil)
		ctx := indexTestAuthContext(req.Context(), "gen3", true, map[string]map[string]bool{
			"/programs/org/projects/a": {"delete": true},
		})
		req = req.WithContext(ctx)

		om := newInternalDRSObjectManager(mockDB)
		rr := doInternalDRSTestRequest(req, om)

		if rr.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d body=%s", rr.Code, rr.Body.String())
		}
		if _, ok := mockDB.Objects["obj-1"]; !ok {
			t.Fatal("grant removal must retain obj-1 content")
		}
		if _, ok := mockDB.Objects["obj-2"]; !ok {
			t.Fatal("grant removal must retain obj-2 content")
		}
		if len(mockDB.ObjectAuthz) != 0 {
			t.Fatalf("project grants remain: %+v", mockDB.ObjectAuthz)
		}
		if !strings.Contains(rr.Body.String(), `"deleted":2`) {
			t.Fatalf("expected deleted count in response, got %s", rr.Body.String())
		}
	})
}

func TestHandleInternalDeleteByQuery_AuthzParity(t *testing.T) {
	for _, mode := range []string{"gen3", "local-authz"} {
		t.Run(mode, func(t *testing.T) {
			now := time.Now().UTC()
			mockDB := &internalRecordStore{
				Objects: map[string]*objects.Record{
					"obj-1": {Id: "obj-1", CreatedTime: now, UpdatedTime: &now},
					"obj-2": {Id: "obj-2", CreatedTime: now, UpdatedTime: &now},
				},
				ObjectAuthz: map[string]map[string][]string{
					"obj-1": {"org": {"a"}},
					"obj-2": {"org": {"a"}},
				},
			}

			req := httptest.NewRequest(http.MethodDelete, "/?organization=org&project=a", nil)
			req = withTestAuthzContext(req, mode, map[string]map[string]bool{
				"/programs/org/projects/a": {"delete": true},
			})
			om := newInternalDRSObjectManager(mockDB)
			rr := doInternalDRSTestRequest(req, om)

			if rr.Code != http.StatusOK {
				t.Fatalf("expected 200, got %d body=%s", rr.Code, rr.Body.String())
			}
			if _, ok := mockDB.Objects["obj-1"]; !ok {
				t.Fatal("grant removal must retain obj-1 content")
			}
			if _, ok := mockDB.Objects["obj-2"]; !ok {
				t.Fatal("grant removal must retain obj-2 content")
			}
			if len(mockDB.ObjectAuthz) != 0 {
				t.Fatalf("project grants remain: %+v", mockDB.ObjectAuthz)
			}
			if !strings.Contains(rr.Body.String(), `"deleted":2`) {
				t.Fatalf("expected deleted count in response, got %s", rr.Body.String())
			}
		})
	}
}

func TestHandleInternalRemoveControlledAccess(t *testing.T) {
	now := time.Now().UTC()
	controlled := []string{"/organization/org/project/a", "/organization/org/project/b"}
	mockDB := &internalRecordStore{
		Objects: map[string]*objects.Record{
			"obj-1": {Id: "obj-1", CreatedTime: now, UpdatedTime: &now, ControlledAccess: &controlled},
		},
		ObjectAuthz: map[string]map[string][]string{
			"obj-1": {"org": {"a", "b"}},
		},
	}

	body := `{"resource":"/programs/org/projects/a"}`
	req := httptest.NewRequest(http.MethodPost, "/index/obj-1/controlled-access/remove", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req = withTestAuthzContext(req, "local-authz", map[string]map[string]bool{
		"/programs/org/projects/a": {"update": true},
		"/programs/org/projects/b": {"read": true},
	})

	om := newInternalDRSObjectManager(mockDB)
	rr := doInternalDRSTestRequest(req, om)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "/organization/org/project/b") {
		t.Fatalf("expected remaining controlled access in response, got %s", rr.Body.String())
	}
	if strings.Contains(rr.Body.String(), "/organization/org/project/a") {
		t.Fatalf("expected removed resource to be absent, got %s", rr.Body.String())
	}
}

func TestRegisterInternalIndexRoutes_LegacyAliases(t *testing.T) {
	now := time.Now().UTC()
	mockDB := &internalRecordStore{
		Objects: map[string]*objects.Record{
			"obj-1": {
				Id: "obj-1", CreatedTime: now, UpdatedTime: &now,
				Checksums: []objects.Checksum{{Type: "sha256", Checksum: "h1"}},
				Properties: map[string]json.RawMessage{
					"large": json.RawMessage(`9007199254740993`),
					"auth":  json.RawMessage(`{"retired":true}`),
				},
			},
		},
	}

	app := fiber.New()
	om := newInternalDRSObjectManager(mockDB)
	RegisterRoutes(app, om.ObjectService)

	t.Run("collection alias /index", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/index?organization=org", nil)
		resp, err := app.Test(req)
		if err != nil {
			t.Fatalf("test request failed: %v", err)
		}
		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(resp.Body)
			t.Fatalf("expected 200, got %d body=%s", resp.StatusCode, string(body))
		}
	})

	t.Run("detail alias /index/{id}", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/index/obj-1", nil)
		resp, err := app.Test(req)
		if err != nil {
			t.Fatalf("test request failed: %v", err)
		}
		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(resp.Body)
			t.Fatalf("expected 200, got %d body=%s", resp.StatusCode, string(body))
		}
		if got := resp.Header.Get("Cache-Control"); got != "no-store" {
			t.Fatalf("expected object detail response to disable caching, got %q", got)
		}
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(body), `"did":"obj-1"`) || !strings.Contains(string(body), `"large":9007199254740993`) {
			t.Fatalf("detail response lost compatibility/raw fields: %s", body)
		}
		if strings.Contains(string(body), `"auth"`) {
			t.Fatalf("detail response emitted retired auth field: %s", body)
		}
	})

	t.Run("bulk alias /index/bulk/hashes", func(t *testing.T) {
		reqBody := `{"hashes":["sha256:h1"]}`
		req := httptest.NewRequest(http.MethodPost, "/index/bulk/hashes", strings.NewReader(reqBody))
		req.Header.Set("Content-Type", "application/json")
		resp, err := app.Test(req)
		if err != nil {
			t.Fatalf("test request failed: %v", err)
		}
		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(resp.Body)
			t.Fatalf("expected 200, got %d body=%s", resp.StatusCode, string(body))
		}
	})

	t.Run("bulk alias /index/bulk/delete", func(t *testing.T) {
		reqBody := `{"hashes":["sha256:h1"]}`
		req := httptest.NewRequest(http.MethodPost, "/index/bulk/delete", strings.NewReader(reqBody))
		req.Header.Set("Content-Type", "application/json")
		resp, err := app.Test(req)
		if err != nil {
			t.Fatalf("test request failed: %v", err)
		}
		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(resp.Body)
			t.Fatalf("expected 200, got %d body=%s", resp.StatusCode, string(body))
		}
		body, _ := io.ReadAll(resp.Body)
		if !strings.Contains(string(body), `"deleted":1`) {
			t.Fatalf("expected deleted count in response, got %s", string(body))
		}
	})
}
