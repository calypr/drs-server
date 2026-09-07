package apidocs

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"
)

func TestSwaggerUIRoutesServed(t *testing.T) {
	app := fiber.New()
	RegisterSwaggerRoutes(app)

	for _, path := range []string{RouteSwaggerUI, RouteSwaggerUIAlt} {
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

func TestOpenAPIRoutesServedInRegistrationOrder(t *testing.T) {
	app := fiber.New()
	RegisterSwaggerRoutes(app)

	want := []string{
		RouteSwaggerUI,
		RouteSwaggerUIAlt,
		RouteOpenAPISpec,
		RouteLFSSpec,
		RouteBucketSpec,
		RouteInternalSpec,
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
	RegisterSwaggerRoutes(app)

	paths := []string{
		RouteOpenAPISpec,
		RouteLFSSpec,
		RouteBucketSpec,
		RouteInternalSpec,
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
		if path == RouteOpenAPISpec && !strings.Contains(string(body), "openapi: 3.0.3") {
			t.Fatalf("expected openapi spec body, got: %s", string(body))
		}
	}
}
