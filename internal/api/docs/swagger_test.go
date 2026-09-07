package docs

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"
)

func TestSwaggerUIRouteRootServed(t *testing.T) {
	app := fiber.New()
	RegisterSwaggerRoutes(app)

	req := httptest.NewRequest(http.MethodGet, "/index/swagger", nil)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("test request failed: %v", err)
	}

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", resp.StatusCode, string(body))
	}
}

func TestSwaggerUIRouteTrailingSlash(t *testing.T) {
	app := fiber.New()
	RegisterSwaggerRoutes(app)

	req := httptest.NewRequest(http.MethodGet, "/index/swagger/", nil)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("test request failed: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	if !strings.Contains(string(body), "SwaggerUIBundle") {
		t.Fatalf("expected swagger html, got: %s", string(body))
	}
}

func TestOpenAPIRouteRootServed(t *testing.T) {
	app := fiber.New()
	RegisterSwaggerRoutes(app)

	req := httptest.NewRequest(http.MethodGet, "/index/openapi.yaml", nil)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("test request failed: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", resp.StatusCode, string(body))
	}
}

func TestOpenAPIRouteServed(t *testing.T) {
	app := fiber.New()
	RegisterSwaggerRoutes(app)

	req := httptest.NewRequest(http.MethodGet, "/index/openapi.yaml", nil)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("test request failed: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", resp.StatusCode, string(body))
	}
	if !strings.Contains(string(body), "openapi: 3.0.3") {
		t.Fatalf("expected openapi spec body, got: %s", string(body))
	}
}

func TestAuxOpenAPIRoutesServed(t *testing.T) {
	app := fiber.New()
	RegisterSwaggerRoutes(app)

	paths := []string{
		"/index/openapi-lfs.yaml",
		"/index/openapi-bucket.yaml",
		"/index/openapi-internal.yaml",
	}
	for _, p := range paths {
		req := httptest.NewRequest(http.MethodGet, p, nil)
		resp, err := app.Test(req)
		if err != nil {
			t.Fatalf("test request failed: %v", err)
		}
		body, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("expected 200 for %s, got %d body=%s", p, resp.StatusCode, string(body))
		}
	}
}
