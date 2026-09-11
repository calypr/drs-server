package httpapi

import (
	"encoding/json"
	"io"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/calypr/syfon/internal/objects"
	"github.com/calypr/syfon/internal/persistence/sqlite"
	"github.com/gofiber/fiber/v3"
)

func TestRecordBoundary(t *testing.T) {
	database, err := sqlite.NewSqliteDB(":memory:", nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	app := fiber.New(fiber.Config{ErrorHandler: FiberErrorHandler})
	RegisterRoutes(app, Dependencies{Objects: objects.NewService(database)}, Options{Internal: true, GA4GH: true})
	const sha = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	tests := []struct {
		name, method, path, body string
		status                   int
		shape                    string
		ids                      []string
		fields                   map[string]string
		absent                   []string
		message                  string
		raw                      string
	}{
		{name: "single create defaults and permissive fields", method: "POST", path: "/index", body: `{"did":"single","name":"/ignored/file.txt","organization":"org","project":"p1","unknown":true}`, status: 201, shape: "object", ids: []string{"single"}, fields: map[string]string{"size": "0", "version": `"1"`, "name_aliases": "[]", "controlled_access": `["/organization/org/project/p1"]`}, absent: []string{"name"}},
		{name: "bulk create order", method: "POST", path: "/index/bulk", body: `{"records":[{"did":"bulk-a","organization":"org","project":"p1","unknown":true},{"did":"bulk-b","organization":"org","project":"p1"}],"unknown":true}`, status: 201, shape: "list", ids: []string{"bulk-a", "bulk-b"}, fields: map[string]string{"size": "0", "version": `"1"`}},
		{name: "single create validation label", method: "POST", path: "/index", body: `{"did":" "}`, status: 400, message: "record invalid"},
		{name: "bulk create validation index", method: "POST", path: "/index/bulk", body: `{"records":[{"did":" "}]}`, status: 400, message: "record[0] invalid"},
		{name: "GET legacy omissions", method: "GET", path: "/index/single", status: 200, shape: "object", ids: []string{"single"}, fields: map[string]string{"id": `"single"`}, absent: []string{"size", "version", "name_aliases"}},
		{name: "list zero and empty values", method: "GET", path: "/index?organization=org&project=p1&limit=1", status: 200, shape: "list", ids: []string{"bulk-a"}, fields: map[string]string{"size": "0", "version": `"1"`, "name_aliases": "[]"}, absent: []string{"id"}},
		{name: "bulk documents array input", method: "POST", path: "/index/bulk/documents", body: `["single"]`, status: 200, shape: "array", ids: []string{"single"}, fields: map[string]string{"size": "0", "version": `"1"`, "name_aliases": "[]"}},
		{name: "bulk documents object input", method: "POST", path: "/index/bulk/documents", body: `{"ids":["single"]}`, status: 200, shape: "array", ids: []string{"single"}},
		{name: "single input at bulk route", method: "POST", path: "/index/bulk", body: `{"did":"single-at-bulk"}`, status: 201, shape: "list", ids: []string{"single-at-bulk"}},
		{name: "bulk hash delete accepts object IDs", method: "POST", path: "/index/bulk/delete", body: `{"hashes":["single-at-bulk"]}`, status: 200, raw: `{"deleted":1}`},
		{name: "bulk input at single route returns first record", method: "POST", path: "/index", body: `{"records":[{"did":"bulk-at-single-a"},{"did":"bulk-at-single-b"}]}`, status: 201, shape: "object", ids: []string{"bulk-at-single-a"}},
		{name: "query fixtures", method: "POST", path: "/index/bulk", body: `{"records":[{"did":"url-a","organization":"org","project":"p1","access_methods":[{"type":"s3","access_url":{"url":"s3://bucket/shared"}}]},{"did":"url-b","organization":"org","project":"p1","access_methods":[{"type":"s3","access_url":{"url":"s3://bucket/shared"}}]},{"did":"other-scope","organization":"other","project":"p2"},{"did":"hash-record","organization":"org","project":"p1","hashes":{"sha256":"` + sha + `"}}]}`, status: 201, shape: "list", ids: []string{"url-a", "url-b", "other-scope", "hash-record"}},
		{name: "checksum duplicate returns durable ID", method: "POST", path: "/index", body: `{"did":"hash-alias","organization":"org","project":"p1","hashes":{"sha256":"` + sha + `"}}`, status: 201, shape: "object", ids: []string{"hash-record"}},
		{name: "URL page", method: "GET", path: "/index?url=s3%3A%2F%2Fbucket%2Fshared&limit=1&page=0", status: 200, shape: "list", ids: []string{"url-a"}},
		{name: "URL cursor overrides page", method: "GET", path: "/index?url=s3%3A%2F%2Fbucket%2Fshared&limit=1&start=url-a&page=99", status: 200, shape: "list", ids: []string{"url-b"}},
		{name: "scoped checksum", method: "GET", path: "/index?organization=org&project=p1&hash=sha256:" + sha + "&limit=1", status: 200, shape: "list", ids: []string{"hash-record"}},
		{name: "scope excludes other projects", method: "GET", path: "/index?organization=org&project=p1", status: 200, shape: "list", ids: []string{"bulk-a", "bulk-b", "hash-record", "single", "url-a", "url-b"}},
		{name: "malformed integer precedes scope validation", method: "GET", path: "/index?limit=bad&project=project-without-org", status: 400, message: "limit"},
		{name: "project requires organization", method: "GET", path: "/index?project=project-without-org", status: 400, message: "organization is required"},
		{name: "update fixture", method: "POST", path: "/index", body: `{"did":"immutable-size","size":7,"organization":"org","project":"p1"}`, status: 201, shape: "object", ids: []string{"immutable-size"}},
		{name: "size is immutable", method: "PUT", path: "/index/immutable-size", body: `{"size":8}`, status: 409, message: "immutable"},
		{name: "explicit zero size is not omission", method: "PUT", path: "/index/immutable-size", body: `{"size":0}`, status: 409, message: "immutable"},
		{name: "omitted size preserves stored size", method: "PUT", path: "/index/immutable-size", body: `{"description":"changed"}`, status: 200, shape: "object", ids: []string{"immutable-size"}, fields: map[string]string{"size": "7", "description": `"changed"`}},
		{name: "update rejects unknown fields", method: "PUT", path: "/index/immutable-size", body: `{"size":7,"unknown_update_field":true}`, status: 400, message: "unknown_update_field"},
	}
	for _, test := range tests {
		request := httptest.NewRequest(test.method, test.path, strings.NewReader(test.body))
		if test.body != "" {
			request.Header.Set("Content-Type", "application/json")
		}
		response, err := app.Test(request)
		if err != nil {
			t.Fatalf("%s: %v", test.name, err)
		}
		payload, err := io.ReadAll(response.Body)
		response.Body.Close()
		if err != nil {
			t.Fatal(err)
		}
		if response.StatusCode != test.status {
			t.Fatalf("%s: status %d, want %d: %s", test.name, response.StatusCode, test.status, payload)
		}
		if test.message != "" {
			var failure struct {
				Message string `json:"message"`
			}
			if err := json.Unmarshal(payload, &failure); err != nil || !strings.Contains(failure.Message, test.message) {
				t.Fatalf("%s: unexpected error: %s", test.name, payload)
			}
			continue
		}
		if test.raw != "" {
			if string(payload) != test.raw {
				t.Fatalf("%s: response %s, want %s", test.name, payload, test.raw)
			}
			continue
		}
		var records []map[string]json.RawMessage
		switch test.shape {
		case "object":
			var record map[string]json.RawMessage
			err = json.Unmarshal(payload, &record)
			records = []map[string]json.RawMessage{record}
		case "array":
			err = json.Unmarshal(payload, &records)
		case "list":
			var body struct {
				Records []map[string]json.RawMessage `json:"records"`
			}
			err = json.Unmarshal(payload, &body)
			records = body.Records
		}
		if err != nil {
			t.Fatalf("%s: invalid %s response: %s", test.name, test.shape, payload)
		}
		ids := make([]string, len(records))
		for i, record := range records {
			if err := json.Unmarshal(record["did"], &ids[i]); err != nil {
				t.Fatalf("%s: missing record ID: %s", test.name, payload)
			}
			for key, want := range test.fields {
				if string(record[key]) != want {
					t.Fatalf("%s: %s=%s, want %s", test.name, key, record[key], want)
				}
			}
			for _, key := range test.absent {
				if _, found := record[key]; found {
					t.Fatalf("%s: unexpected %s in %s", test.name, key, payload)
				}
			}
		}
		if !reflect.DeepEqual(ids, test.ids) {
			t.Fatalf("%s: IDs %v, want %v", test.name, ids, test.ids)
		}
	}
}
