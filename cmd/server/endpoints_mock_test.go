package server

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"

	"github.com/calypr/syfon/internal/access/authentication"
	"github.com/calypr/syfon/internal/buckets"
	"github.com/calypr/syfon/internal/config"
	"github.com/calypr/syfon/internal/httpapi/middleware"
	"github.com/calypr/syfon/internal/objects"
	"github.com/calypr/syfon/internal/transfers"
	"github.com/calypr/syfon/internal/usage"
)

type endpointCase struct {
	Method   string
	Template string
}

var pathVarPattern = regexp.MustCompile(`:([A-Za-z0-9_]+)`)

func endpointPtr[T any](value T) *T { return &value }

func TestAllRegisteredEndpoints_WithMocks(t *testing.T) {
	app := buildMockServerRouterWithRoutes(config.RoutesConfig{
		Docs:     true,
		Ga4gh:    true,
		Metrics:  true,
		Internal: true,
		LFS:      true,
	})

	endpoints := collectEndpoints(t, app)
	if len(endpoints) == 0 {
		t.Fatal("no endpoints discovered from router")
	}

	seen := make(map[string]struct{}, len(endpoints))
	for _, ep := range endpoints {
		if ep.Method == http.MethodOptions || ep.Method == "CONNECT" || ep.Method == "TRACE" || ep.Method == "PATCH" {
			continue
		}
		if ep.Template == "/" && ep.Method != http.MethodGet && ep.Method != http.MethodDelete {
			continue
		}
		key := ep.Method + " " + ep.Template
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}

		t.Run(key, func(t *testing.T) {
			path := materializePath(ep.Template)
			body, contentType := requestBodyFor(ep.Method, ep.Template)
			req := httptest.NewRequest(ep.Method, path, bytes.NewReader(body))
			if contentType != "" {
				req.Header.Set("Content-Type", contentType)
			}
			if strings.HasPrefix(path, "/info/lfs/") {
				req.Header.Set("Accept", "application/vnd.git-lfs+json")
			}

			resp, err := app.Test(req)
			if err != nil {
				t.Fatalf("test request failed: %v", err)
			}
			if resp.StatusCode <= 0 {
				t.Fatalf("invalid status code for %s %s: %d", ep.Method, path, resp.StatusCode)
			}
			if resp.StatusCode == http.StatusMethodNotAllowed {
				t.Fatalf("unexpected 405 for matched route %s %s", ep.Method, path)
			}
		})
	}

	required := []endpointCase{
		{Method: http.MethodPost, Template: "/ga4gh/drs/v1/objects/register"},
		{Method: http.MethodPost, Template: "/ga4gh/drs/v1/objects"},
		{Method: http.MethodPost, Template: "/ga4gh/drs/v1/objects/access"},
		{Method: http.MethodPost, Template: "/ga4gh/drs/v1/objects/access-methods"},
		{Method: http.MethodPost, Template: "/ga4gh/drs/v1/objects/delete"},
		{Method: http.MethodPost, Template: "/info/lfs/objects/batch"},
		{Method: http.MethodPost, Template: "/index/bulk/sha256/validity"},
		{Method: http.MethodGet, Template: "/index/v1/metrics/summary"},
		{Method: http.MethodGet, Template: "/index/v1/metrics/files"},
	}
	for _, req := range required {
		if _, ok := seen[req.Method+" "+req.Template]; !ok {
			t.Fatalf("required endpoint missing from router: %s %s", req.Method, req.Template)
		}
	}
}

func TestAdminRoutesNotRegistered(t *testing.T) {
	app := buildMockServerRouterWithRoutes(config.RoutesConfig{
		Docs:     true,
		Ga4gh:    true,
		Metrics:  true,
		Internal: true,
		LFS:      true,
	})

	reqSign := httptest.NewRequest(http.MethodPost, "/admin/sign_url", bytes.NewBufferString(`{"url":"s3://b/k","method":"GET"}`))
	reqSign.Header.Set("Content-Type", "application/json")
	respSign, err := app.Test(reqSign)
	if err != nil {
		t.Fatalf("test request failed: %v", err)
	}
	if respSign.StatusCode != http.StatusNotFound {
		t.Fatalf("expected /admin/sign_url to be absent (404), got %d", respSign.StatusCode)
	}

	reqCreds := httptest.NewRequest(http.MethodGet, "/admin/credentials", nil)
	respCreds, err := app.Test(reqCreds)
	if err != nil {
		t.Fatalf("test request failed: %v", err)
	}
	if respCreds.StatusCode != http.StatusNotFound {
		t.Fatalf("expected /admin/credentials to be absent (404), got %d", respCreds.StatusCode)
	}
}

func buildMockServerRouter() *fiber.App {
	return buildMockServerRouterWithRoutes(config.RoutesConfig{
		Docs:     true,
		Ga4gh:    true,
		Metrics:  true,
		Internal: true,
		LFS:      true,
	})
}

func TestHealthOnlyServerExposesNoOptionalRoutes(t *testing.T) {
	app := buildMockServerRouterWithRoutes(config.RoutesConfig{})

	endpoints := collectEndpoints(t, app)
	foundHealth := false
	for _, ep := range endpoints {
		if ep.Template == "/healthz" {
			foundHealth = true
			continue
		}
		if ep.Template == "/" {
			continue
		}
		t.Fatalf("expected only healthz route when no modules are enabled, found %s %s", ep.Method, ep.Template)
	}
	if !foundHealth {
		t.Fatal("expected /healthz route when no modules are enabled")
	}
}

func buildMockServerRouterWithRoutes(routes config.RoutesConfig) *fiber.App {
	objectStore := newServerObjectStore(map[string]*objects.Record{
		"sha-1": {
			Id:          "sha-1",
			Name:        endpointPtr("mock-object"),
			Size:        1,
			Version:     endpointPtr("1"),
			Description: endpointPtr("mock"),
			Checksums:   []objects.Checksum{{Type: "sha256", Checksum: "sha-1"}},
			AccessMethods: &[]objects.AccessMethod{
				{
					Type:      "s3",
					AccessId:  endpointPtr("s3"),
					AccessUrl: &objects.AccessURL{Url: "s3://test-bucket-1/sha-1"},
				},
			},
			ControlledAccess: &[]string{"/programs/data_file"},
		},
	})
	bucketStore := &serverBucketStore{credentials: map[string]buckets.Credential{
		"test-bucket-1": {
			Bucket:    "test-bucket-1",
			Region:    "us-east-1",
			AccessKey: "mock-key",
			SecretKey: "mock-secret",
		},
	}}
	app := fiber.New()

	logger := slog.New(slog.NewTextHandler(bytes.NewBuffer(nil), nil))
	authRuntime := authentication.NewRuntime(logger, "local", "", "")
	authzMiddleware := middleware.NewAuthzMiddleware(logger, middleware.Options{Mode: "local", Evaluator: authRuntime})
	requestIDMiddleware := middleware.NewRequestIDMiddleware(logger)
	cfg := &config.Config{Routes: routes}
	dependencies := mockServerDependencies(objectStore, bucketStore)
	objectService := objects.NewService(dependencies.objects)
	usageService := usage.NewService(usage.Dependencies{Reports: dependencies.usageReports, Objects: objectService})
	transferService := transfers.NewService(transfers.Dependencies{
		Scopes: dependencies.bucketService, Credentials: dependencies.bucketService,
		Pending: dependencies.pending, Events: dependencies.usageIngest,
	})
	rt := &serverRuntime{
		app:                 app,
		cfg:                 cfg,
		serviceInfo:         serviceInfoForBackend(true),
		objectService:       objectService,
		transferService:     transferService,
		usageService:        usageService,
		usageIngest:         dependencies.usageIngest,
		bucketService:       dependencies.bucketService,
		authzMiddleware:     authzMiddleware,
		requestIDMiddleware: requestIDMiddleware,
	}
	registerServerRoutes(rt)
	return app
}

func collectEndpoints(t *testing.T, app *fiber.App) []endpointCase {
	t.Helper()
	routes := app.GetRoutes(false)
	out := make([]endpointCase, 0, len(routes))
	for _, route := range routes {
		if route.Method == http.MethodHead {
			continue
		}
		out = append(out, endpointCase{Method: route.Method, Template: route.Path})
	}
	return out
}

func materializePath(template string) string {
	return pathVarPattern.ReplaceAllStringFunc(template, func(v string) string {
		name := strings.TrimPrefix(v, ":")
		switch name {
		case "object_id", "id", "did", "guid", "file_id", "oid":
			return "sha-1"
		case "access_id":
			return "s3"
		case "bucket":
			return "test-bucket-1"
		default:
			return "value"
		}
	})
}

func requestBodyFor(method, template string) ([]byte, string) {
	if method != http.MethodPost && method != http.MethodPut && method != http.MethodDelete {
		return nil, ""
	}

	switch template {
	case "/ga4gh/drs/v1/objects/register":
		return []byte(`{"candidates":[{"name":"obj","size":1,"checksums":[{"type":"sha256","checksum":"sha-1"}],"access_methods":[{"type":"s3","access_url":{"url":"s3://test-bucket-1/sha-1"},"authorizations":{"data":[]}}]}]}`), "application/json"
	case "/ga4gh/drs/v1/objects":
		return []byte(`{"bulk_object_ids":["sha-1"]}`), "application/json"
	case "/ga4gh/drs/v1/objects/access":
		return []byte(`{"bulk_object_access_ids":[{"bulk_object_id":"sha-1","bulk_access_ids":["s3"]}]}`), "application/json"
	case "/ga4gh/drs/v1/objects/access-methods":
		return []byte(`{"updates":[{"object_id":"sha-1","access_methods":[{"type":"s3","access_id":"s3","access_url":{"url":"s3://test-bucket-1/sha-1"}}]}]}`), "application/json"
	case "/ga4gh/drs/v1/objects/:object_id/checksums":
		return []byte(`{"checksums":[{"type":"md5","checksum":"md5sum"}]}`), "application/json"
	case "/ga4gh/drs/v1/objects/delete":
		return []byte(`{"bulk_object_ids":["sha-1"],"delete_storage_data":false}`), "application/json"
	case "/ga4gh/drs/v1/objects/:object_id/delete":
		return []byte(`{"delete_storage_data":false}`), "application/json"
	case "/ga4gh/drs/v1/upload-request":
		return []byte(`{"requests":[{"size":1,"checksums":[{"type":"sha256","checksum":"sha-1"}],"name":"obj.bin"}]}`), "application/json"
	case "/index/bulk/sha256/validity":
		return []byte(`{"sha256":["sha-1"]}`), "application/json"
	case "/index/bulk/hashes":
		return []byte(`{"hashes":["sha-1"]}`), "application/json"
	case "/index/bulk":
		return []byte(`{"records":[{"did":"sha-1","hashes":{"sha256":"sha-1"},"size":1,"auth":{"data_file":{"":["s3://test-bucket-1/sha-1"]}}}]}`), "application/json"
	case "/index/bulk/documents":
		return []byte(`["sha-1"]`), "application/json"
	case "/data/upload", "/data/upload/:file_id":
		if template == "/data/upload" {
			return []byte(`{"guid":"sha-1","authorizations":{"data_file":[]}}`), "application/json"
		}
		return []byte(`{}`), "application/json"
	case "/data/upload/bulk":
		return []byte(`{"requests":[{"file_id":"sha-1","bucket":"test-bucket-1","name":"sha-1"}]}`), "application/json"
	case "/data/multipart/init":
		return []byte(`{"guid":"sha-1","name":"sha-1","bucket":"test-bucket-1"}`), "application/json"
	case "/data/multipart/upload":
		return []byte(`{"key":"sha-1","bucket":"test-bucket-1","uploadId":"mock-upload-id","partNumber":1}`), "application/json"
	case "/data/multipart/complete":
		return []byte(`{"key":"sha-1","bucket":"test-bucket-1","uploadId":"mock-upload-id","parts":[{"PartNumber":1,"ETag":"etag-1"}]}`), "application/json"
	case "/data/buckets":
		if method == http.MethodPut {
			return []byte(`{"bucket":"test-bucket-3","region":"us-east-1","access_key":"k","secret_key":"s","endpoint":""}`), "application/json"
		}
		return []byte(`{}`), "application/json"
	case "/data/buckets/:bucket/scopes":
		return []byte(`{"organization":"cbds","project_id":"proj2"}`), "application/json"
	case "/info/lfs/objects/batch":
		return []byte(`{"operation":"download","objects":[{"oid":"sha-1","size":1}]}`), "application/json"
	case "/info/lfs/objects/metadata":
		return []byte(`{"candidates":[{"name":"obj","size":1,"checksums":[{"type":"sha256","checksum":"sha-1"}],"access_methods":[{"type":"s3","access_url":{"url":"s3://test-bucket-1/sha-1"},"authorizations":{"data":[]}}]}]}`), "application/json"
	case "/info/lfs/verify":
		return []byte(`{"oid":"sha-1","size":1}`), "application/json"
	case "/info/lfs/objects/:oid":
		return []byte(`{}`), "application/octet-stream"
	default:
		return []byte(`{}`), "application/json"
	}
}
