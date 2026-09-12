package apidocs_test

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/calypr/syfon/apigen/errorapi"
	"github.com/calypr/syfon/internal/httpapi"
	"github.com/calypr/syfon/internal/httpapi/apidocs"
	"github.com/gofiber/fiber/v3"
	"gopkg.in/yaml.v3"
)

func TestSwaggerUIRoutesServed(t *testing.T) {
	app := fiber.New()
	apidocs.RegisterSwaggerRoutes(app)

	for _, path := range []string{apidocs.RouteSwaggerUI, apidocs.RouteSwaggerUIAlt} {
		resp, err := app.Test(httptest.NewRequest(http.MethodGet, path, nil))
		if err != nil {
			t.Fatalf("test request failed for %s: %v", path, err)
		}
		body, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("expected 200 for %s, got %d body=%s", path, resp.StatusCode, string(body))
		}
		if !strings.Contains(string(body), "SwaggerUIBundle") {
			t.Fatalf("expected swagger html for %s, got: %s", path, string(body))
		}
		if got := resp.Header.Get("Content-Type"); got != "text/html; charset=utf-8" {
			t.Fatalf("expected html content type for %s, got %q", path, got)
		}
	}
}

func TestOpenAPISpecFailureUsesAPIErrorContract(t *testing.T) {
	app := fiber.New(fiber.Config{ErrorHandler: httpapi.FiberErrorHandler})
	app.Get("/", func(c fiber.Ctx) error {
		return errors.New("private filesystem detail")
	})

	resp, err := app.Test(httptest.NewRequest(http.MethodGet, "/", nil))
	if err != nil {
		t.Fatalf("test request failed: %v", err)
	}
	var body errorapi.APIError
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode error response: %v", err)
	}
	if resp.StatusCode != http.StatusInternalServerError || body.Status != http.StatusInternalServerError || body.Code != errorapi.ErrorCodeInternalError || body.Category != errorapi.ErrorCategoryInternalError || body.Message != http.StatusText(http.StatusInternalServerError) {
		t.Fatalf("unexpected error response: status=%d body=%+v", resp.StatusCode, body)
	}
}

func TestOpenAPIRoutesServedInRegistrationOrder(t *testing.T) {
	app := fiber.New()
	apidocs.RegisterSwaggerRoutes(app)

	want := []string{
		apidocs.RouteSwaggerUI,
		apidocs.RouteSwaggerUIAlt,
		apidocs.RouteOpenAPISpec,
		apidocs.RouteLFSSpec,
		apidocs.RouteBucketSpec,
		apidocs.RouteInternalSpec,
		apidocs.RouteErrorSpec,
	}
	var got []string
	for _, routes := range app.Stack() {
		for _, route := range routes {
			if route.Method == http.MethodGet {
				got = append(got, route.Path)
			}
		}
	}
	if len(got) < len(want) {
		t.Fatalf("expected at least %d GET routes, got %d: %v", len(want), len(got), got)
	}
	got = got[len(got)-len(want):]
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("GET route %d: expected %q, got %q", i, want[i], got[i])
		}
	}
}

func TestOpenAPISpecRoutesServed(t *testing.T) {
	app := fiber.New()
	apidocs.RegisterSwaggerRoutes(app)

	paths := []string{
		apidocs.RouteOpenAPISpec,
		apidocs.RouteLFSSpec,
		apidocs.RouteBucketSpec,
		apidocs.RouteInternalSpec,
		apidocs.RouteErrorSpec,
	}
	for _, path := range paths {
		resp, err := app.Test(httptest.NewRequest(http.MethodGet, path, nil))
		if err != nil {
			t.Fatalf("test request failed for %s: %v", path, err)
		}
		body, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("expected 200 for %s, got %d body=%s", path, resp.StatusCode, string(body))
		}
		if got := resp.Header.Get("Content-Type"); got != "application/yaml" {
			t.Fatalf("expected yaml content type for %s, got %q", path, got)
		}
		if path == apidocs.RouteOpenAPISpec && !strings.Contains(string(body), "openapi: 3.0.3") {
			t.Fatalf("expected openapi spec body, got: %s", string(body))
		}
	}
}

func TestProjectDeleteOperationHasOneCanonicalOwner(t *testing.T) {
	app := fiber.New()
	apidocs.RegisterSwaggerRoutes(app)

	load := func(path string) map[string]any {
		t.Helper()
		response, err := app.Test(httptest.NewRequest(http.MethodGet, path, nil))
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		defer response.Body.Close()
		var document map[string]any
		if err := yaml.NewDecoder(response.Body).Decode(&document); err != nil {
			t.Fatalf("decode %s: %v", path, err)
		}
		return document
	}
	operation := func(document map[string]any) (map[string]any, bool) {
		paths, ok := document["paths"].(map[string]any)
		if !ok {
			return nil, false
		}
		path, ok := paths["/data/projects/{organization}/{project_id}"].(map[string]any)
		if !ok {
			return nil, false
		}
		deleteOperation, ok := path["delete"].(map[string]any)
		return deleteOperation, ok
	}

	if _, ok := operation(load(apidocs.RouteInternalSpec)); ok {
		t.Fatal("internal OpenAPI spec still owns the project-delete operation")
	}
	if _, ok := operation(load(apidocs.RouteBucketSpec)); !ok {
		t.Fatal("bucket OpenAPI spec does not own the project-delete operation")
	}

	deleteOperation, ok := operation(load(apidocs.RouteOpenAPISpec))
	if !ok {
		t.Fatal("merged OpenAPI spec does not contain the project-delete operation")
	}
	if got := deleteOperation["operationId"]; got != "deleteProjectData" {
		t.Fatalf("operationId = %v, want deleteProjectData", got)
	}
	tags, _ := deleteOperation["tags"].([]any)
	if len(tags) != 1 || tags[0] != "bucket" {
		t.Fatalf("tags = %v, want [bucket]", tags)
	}
	responses, _ := deleteOperation["responses"].(map[string]any)
	for _, status := range []string{"200", "400", "401", "403", "404", "500"} {
		if _, ok := responses[status]; !ok {
			t.Fatalf("project-delete response %s is missing: %v", status, responses)
		}
	}
}
