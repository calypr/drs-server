package server

import (
	"encoding/json"
	"io"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRouterMigrationBoundaryParity(t *testing.T) {
	for _, tc := range []struct {
		method   string
		target   string
		body     string
		status   int
		code     string
		contains string
	}{
		{"PUT", "/ga4gh/drs/v1/objects/delete", `{}`, 400, "invalid_input", "bulk_object_ids cannot be empty"},
		{"PUT", "/ga4gh/drs/v1/objects/access-methods", `{}`, 400, "invalid_input", "Invalid request body"},
		{"PUT", "/ga4gh/drs/v1/objects/checksums", `{}`, 404, "not_found", "Checksum addition is not supported"},
		{"GET", "/ga4gh/drs/v1/objects/sha-1", "", 200, "", `"id":"sha-1"`},
		{"POST", "/ga4gh/drs/v1/objects/sha-1", `{}`, 200, "", `"id":"sha-1"`},
		{"GET", "/ga4gh/drs/v1/objects/sha-1?expand=true", "", 200, "", `"id":"sha-1"`},
		{"GET", "/index?limit=bad", "", 400, "invalid_input", ""},
		{"GET", "/index?limit=-1", "", 400, "invalid_input", ""},
		{"POST", "/index/bulk/sha256/validity", `{}`, 400, "invalid_input", "sha256 values are required"},
		{"POST", "/index/bulk/sha256/missing", `{}`, 400, "invalid_input", "organization, project, and sha256 values are required"},
		{"POST", "/index/bulk/hashes", `{}`, 200, "", `"Results":{}`},
		{"PUT", "/index/bulk/overwrite", `{}`, 400, "invalid_input", "organization, project, and records are required"},
		{"GET", "/data/buckets", "", 200, "", `"S3_BUCKETS"`},
		{"PUT", "/data/buckets", `{}`, 400, "invalid_input", "bucket is required"},
		{"POST", "/data/buckets/test-bucket-1/scopes", `{}`, 400, "invalid_input", "organization is required"},
		{"GET", "/data/download/sha-1?redirect=bad", "", 400, "invalid_input", ""},
		{"GET", "/data/download/sha-1/part?part_number=bad", "", 400, "invalid_input", ""},
		{"POST", "/data/multipart/init", `{}`, 400, "invalid_input", "key/guid is required"},
		{"POST", "/data/multipart/upload", `{}`, 400, "invalid_input", "uploadId is required"},
		{"POST", "/data/multipart/complete", `{}`, 400, "invalid_input", "uploadId is required"},
		{"POST", "/data/inspect/project-scopes", `{}`, 400, "invalid_input", "organization and project are required"},
	} {
		t.Run(tc.method+" "+tc.target, func(t *testing.T) {
			app := buildMockServerRouter()
			req := httptest.NewRequest(tc.method, tc.target, strings.NewReader(tc.body))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("X-Request-Id", "router-parity")
			resp, err := app.Test(req)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			body, err := io.ReadAll(resp.Body)
			if err != nil {
				t.Fatal(err)
			}
			var payload map[string]any
			if err := json.Unmarshal(body, &payload); err != nil {
				t.Fatalf("non-JSON response: %s: %v", body, err)
			}
			if resp.StatusCode != tc.status || !strings.Contains(string(body), tc.contains) {
				t.Fatalf("status=%d body=%s, want status=%d containing %q", resp.StatusCode, body, tc.status, tc.contains)
			}
			if tc.code != "" {
				if payload["code"] != tc.code || payload["request_id"] != "router-parity" || payload["status"] != float64(tc.status) {
					t.Fatalf("error contract changed: %s", body)
				}
			} else if payload["code"] != nil {
				t.Fatalf("success became an error: %s", body)
			}
		})
	}
}
