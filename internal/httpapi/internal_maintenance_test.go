package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/calypr/syfon/apigen/errorapi"
	"github.com/calypr/syfon/internal/access"
	"github.com/calypr/syfon/internal/objects/scoperepair"
	projectstorage "github.com/calypr/syfon/internal/projects/storage"
	"github.com/gofiber/fiber/v3"
)

func TestInspectObjectRejectsMalformedURLWithExistingStatusAndBody(t *testing.T) {
	request := authenticatedRequest(http.MethodPost, RouteInspectObject, `{"object_url":"https://example.com/object"}`, nil)
	app := fiber.New()
	app.Use(func(c fiber.Ctx) error {
		c.SetContext(request.Context())
		return c.Next()
	})
	service := projectstorage.NewService(projectstorage.Dependencies{})
	RegisterUndocumentedRoutes(app, nil, service.Inspector, service.ProjectCleanup)

	response, err := app.Test(request)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", response.StatusCode)
	}
	var body errorapi.APIError
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Code != "invalid_input" || body.Message != "object_url must be a valid s3://bucket/key URL" {
		t.Fatalf("unexpected error body: %+v", body)
	}
}

func TestInspectObjectUsesStrictJSONDecoding(t *testing.T) {
	request := authenticatedRequest(http.MethodPost, RouteInspectObject, `{"unexpected":true}`, nil)
	app := fiber.New()
	app.Use(func(c fiber.Ctx) error {
		c.SetContext(request.Context())
		return c.Next()
	})
	service := projectstorage.NewService(projectstorage.Dependencies{})
	RegisterUndocumentedRoutes(app, nil, service.Inspector, service.ProjectCleanup)

	response, err := app.Test(request)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", response.StatusCode)
	}
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	if !strings.Contains(string(body), "Invalid request body: json: unknown field") {
		t.Fatalf("body = %q, want strict JSON error", body)
	}
}

func TestScopeRepairApplyChecksReadBeforeUpdate(t *testing.T) {
	privileges := map[string]map[string]bool{
		"/organization/org/project/project": {"read": true},
	}
	request := authenticatedRequest(http.MethodPost, RouteRepairScopeApply, `{"organization":"org","project":"project"}`, privileges)
	app := fiber.New()
	app.Use(func(c fiber.Ctx) error {
		c.SetContext(request.Context())
		return c.Next()
	})
	RegisterUndocumentedRoutes(app, scoperepair.NewService(nil, nil, nil, nil, nil), nil, nil)

	response, err := app.Test(request)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 for read-only caller", response.StatusCode)
	}
}

func authenticatedRequest(method, path, body string, privileges map[string]map[string]bool) *http.Request {
	session := access.NewSession("gen3")
	session.AuthHeaderPresent = true
	session.AuthzEnforced = true
	session.SetAuthorizations(nil, privileges, true)
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	return request.WithContext(access.WithSession(context.Background(), session))
}
