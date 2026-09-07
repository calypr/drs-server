package maintenance

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/calypr/syfon/internal/access"
	apiresponse "github.com/calypr/syfon/internal/httpapi/response"
	"github.com/calypr/syfon/internal/objects/scoperepair"
	projectstorage "github.com/calypr/syfon/internal/projects/storage"
	"github.com/gofiber/fiber/v3"
)

func TestRegisterRoutesUsesDirectFiberCleanupParams(t *testing.T) {
	app := fiber.New()
	RegisterProjectCleanupRoute(app, nil)

	request := httptest.NewRequest(http.MethodDelete, "/data/projects/org/project", nil)
	response, err := app.Test(request)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", response.StatusCode)
	}
	var body apiresponse.APIError
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Code != "storage_unavailable" || body.Category != "unavailable" || body.Message != http.StatusText(http.StatusServiceUnavailable) {
		t.Fatalf("unexpected error body: %+v", body)
	}
}

func TestInspectObjectRejectsMalformedURLWithExistingStatusAndBody(t *testing.T) {
	request := authenticatedRequest(http.MethodPost, RouteInspectObject, `{"object_url":"https://example.com/object"}`, nil)
	app := fiber.New()
	app.Use(func(c fiber.Ctx) error {
		c.SetContext(request.Context())
		return c.Next()
	})
	service := projectstorage.NewService(projectstorage.Dependencies{})
	RegisterInspectionRoutes(app, service.Inspector, service.ProjectCleanup, nil)

	response, err := app.Test(request)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", response.StatusCode)
	}
	var body apiresponse.APIError
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
	RegisterInspectionRoutes(app, service.Inspector, service.ProjectCleanup, nil)

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
	RegisterRepairRoutes(app, scoperepair.NewService(nil, nil, nil, nil, nil))

	response, err := app.Test(request)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 for read-only caller", response.StatusCode)
	}
}

func TestRegisterRepairRoutesPreservesOrder(t *testing.T) {
	app := fiber.New()
	RegisterRepairRoutes(app, nil)

	want := []string{RouteRepairScopeAudit, RouteRepairScopeApply}
	assertRegisteredPOSTPaths(t, app, want)
}

func TestRegisterInspectionRoutesPreservesOrder(t *testing.T) {
	app := fiber.New()
	RegisterInspectionRoutes(app, nil, nil, nil)

	want := []string{
		RouteInspectObject,
		RouteInspectObjectBulk,
		RouteInspectObjectBulkList,
		RouteInspectProjectBucket,
		RouteInspectProjectBucketInventory,
		RouteInspectProjectRecords,
		RouteInspectProjectScopes,
		RouteDeleteProjectBucketObjects,
	}
	assertRegisteredPOSTPaths(t, app, want)
	assertRegisteredGETPaths(t, app, []string{RouteInspectProjectScopes})
}

func TestRegisterProjectCleanupRoutePreservesOrder(t *testing.T) {
	app := fiber.New()
	RegisterProjectCleanupRoute(app, nil)

	want := []string{RouteProjectCleanup}
	var got []string
	for _, routes := range app.Stack() {
		for _, route := range routes {
			if route.Method == http.MethodDelete {
				got = append(got, route.Path)
			}
		}
	}
	if len(got) != len(want) {
		t.Fatalf("DELETE routes = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("DELETE route %d = %q, want %q", i, got[i], want[i])
		}
	}
}

func assertRegisteredPOSTPaths(t *testing.T, app *fiber.App, want []string) {
	t.Helper()
	var got []string
	for _, routes := range app.Stack() {
		for _, route := range routes {
			if route.Method == http.MethodPost {
				got = append(got, route.Path)
			}
		}
	}
	if len(got) != len(want) {
		t.Fatalf("POST routes = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("POST route %d = %q, want %q", i, got[i], want[i])
		}
	}
}

func assertRegisteredGETPaths(t *testing.T, app *fiber.App, want []string) {
	t.Helper()
	var got []string
	for _, routes := range app.Stack() {
		for _, route := range routes {
			if route.Method == http.MethodGet {
				got = append(got, route.Path)
			}
		}
	}
	if len(got) != len(want) {
		t.Fatalf("inspection routes = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("inspection route %d = %q, want %q", i, got[i], want[i])
		}
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
