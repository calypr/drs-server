package middleware

import (
	"encoding/base64"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/calypr/syfon/internal/access"
	"github.com/calypr/syfon/internal/access/authentication"
	"github.com/gofiber/fiber/v3"
)

func newTestAuthzMiddleware(logger *slog.Logger, mode, basicUser, basicPass string) *AuthzMiddleware {
	authRuntime := authentication.NewRuntime(logger, mode, basicUser, basicPass)
	return NewAuthzMiddleware(logger, Options{Mode: mode, Evaluator: authRuntime})
}

func injectDummyAuthorizationEvaluator(m *AuthzMiddleware) {
	m.evaluator = &fixedEvaluator{decision: authentication.DecisionContinue}
}

func injectDummyAuthenticationEvaluator(m *AuthzMiddleware, authenticated bool) {
	decision := authentication.DecisionUnauthorized
	if authenticated {
		decision = authentication.DecisionContinue
	}
	m.evaluator = &fixedEvaluator{decision: decision, basicChallenge: m.mode == "local"}
}

type fixedEvaluator struct {
	decision       authentication.Decision
	basicChallenge bool
}

func (e *fixedEvaluator) Evaluate(req authentication.EvaluationRequest) authentication.EvaluationResult {
	session := access.NewSession(req.Mode)
	if strings.EqualFold(req.Mode, "gen3") {
		session.AuthHeaderPresent = strings.TrimSpace(req.AuthHeader) != ""
		session.AuthzEnforced = true
	}
	return authentication.EvaluationResult{
		Session:        session,
		Decision:       e.decision,
		BasicChallenge: e.basicChallenge,
	}
}

func TestLocalModeBasicAuthEnforced(t *testing.T) {
	m := newTestAuthzMiddleware(slog.Default(), "local", "user", "pass")
	app := fiber.New()
	app.Use(m.FiberMiddleware())
	app.Get("/", func(c fiber.Ctx) error {
		return c.SendStatus(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("test request failed: %v", err)
	}
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", resp.StatusCode)
	}

	req2 := httptest.NewRequest(http.MethodGet, "/", nil)
	req2.SetBasicAuth("user", "pass")
	resp2, err := app.Test(req2)
	if err != nil {
		t.Fatalf("test request failed: %v", err)
	}
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp2.StatusCode)
	}
}

func TestPublicMetadataBypassExcludesReservedObjectNames(t *testing.T) {
	paths := []struct {
		name       string
		path       string
		wantStatus int
	}{
		{name: "object metadata", path: "/ga4gh/drs/v1/objects/object-id", wantStatus: http.StatusOK},
		{name: "checksum metadata", path: "/ga4gh/drs/v1/objects/checksum/sha256:abc", wantStatus: http.StatusOK},
		{name: "register mutation", path: "/ga4gh/drs/v1/objects/register", wantStatus: http.StatusUnauthorized},
		{name: "access mutation", path: "/ga4gh/drs/v1/objects/access", wantStatus: http.StatusUnauthorized},
		{name: "delete mutation", path: "/ga4gh/drs/v1/objects/delete", wantStatus: http.StatusUnauthorized},
		{name: "access methods mutation", path: "/ga4gh/drs/v1/objects/access-methods", wantStatus: http.StatusUnauthorized},
		{name: "checksum reserved name", path: "/ga4gh/drs/v1/objects/checksum", wantStatus: http.StatusUnauthorized},
	}

	for _, tc := range paths {
		t.Run(tc.name, func(t *testing.T) {
			m := newTestAuthzMiddleware(slog.Default(), "local", "user", "pass")
			app := fiber.New()
			app.Use(m.FiberMiddleware())
			app.Get(tc.path, func(c fiber.Ctx) error { return c.SendStatus(http.StatusOK) })

			resp, err := app.Test(httptest.NewRequest(http.MethodGet, tc.path, nil))
			if err != nil {
				t.Fatalf("test request failed: %v", err)
			}
			if resp.StatusCode != tc.wantStatus {
				t.Fatalf("expected status %d, got %d", tc.wantStatus, resp.StatusCode)
			}
		})
	}
}

func TestMiddlewareInstallsEvaluatorSession(t *testing.T) {
	const requestID = "request-id-for-evaluator"
	evaluator := &recordingEvaluator{}
	m := NewAuthzMiddleware(slog.Default(), Options{Mode: "gen3", Evaluator: evaluator})

	app := fiber.New()
	app.Use(NewRequestIDMiddleware(nil).FiberMiddleware())
	app.Use(m.FiberMiddleware())
	app.Get("/objects/object-id", func(c fiber.Ctx) error { return c.SendStatus(http.StatusOK) })

	req := httptest.NewRequest(http.MethodGet, "/objects/object-id", nil)
	req.Header.Set(requestIDHeader, requestID)
	req.Header.Set(fiber.HeaderAuthorization, "Bearer malformed.token")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("test request failed: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected status 200, got %d", resp.StatusCode)
	}
	if evaluator.request.RequestID != requestID || evaluator.request.AuthHeader == "" {
		t.Fatalf("expected middleware to pass request metadata to evaluator: %+v", evaluator.request)
	}
}

type recordingEvaluator struct {
	request authentication.EvaluationRequest
}

func (e *recordingEvaluator) Evaluate(request authentication.EvaluationRequest) authentication.EvaluationResult {
	e.request = request
	return authentication.EvaluationResult{Session: access.NewSession(request.Mode), Decision: authentication.DecisionContinue}
}

func TestGen3ModeSetsContextWithoutAuthHeader(t *testing.T) {
	m := newTestAuthzMiddleware(slog.Default(), "gen3", "", "")
	app := fiber.New()
	app.Use(m.FiberMiddleware())
	app.Get("/", func(c fiber.Ctx) error {
		if !access.IsGen3Mode(c.Context()) {
			t.Fatalf("expected gen3 mode in context")
		}
		if access.HasAuthHeader(c.Context()) {
			t.Fatalf("did not expect auth header presence")
		}
		return c.SendStatus(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("test request failed: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
}

func TestGen3ModeMalformedBearerStillPassesToNext(t *testing.T) {
	m := newTestAuthzMiddleware(slog.Default(), "gen3", "", "")
	injectDummyAuthorizationEvaluator(m)
	app := fiber.New()
	app.Use(m.FiberMiddleware())
	app.Get("/", func(c fiber.Ctx) error {
		if !access.HasAuthHeader(c.Context()) {
			t.Fatalf("expected auth header presence to be true")
		}
		return c.SendStatus(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer malformed.token")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("test request failed: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
}

func TestGen3MockAuthInjectsPrivileges(t *testing.T) {
	t.Setenv("DRS_AUTH_MOCK_ENABLED", "true")
	t.Setenv("DRS_AUTH_MOCK_RESOURCES", "/data_file,/programs/cbds/projects/end_to_end_test")
	t.Setenv("DRS_AUTH_MOCK_METHODS", "read,file_upload,create,update,delete")

	m := newTestAuthzMiddleware(slog.Default(), "gen3", "", "")
	app := fiber.New()
	app.Use(m.FiberMiddleware())
	app.Get("/", func(c fiber.Ctx) error {
		if !access.IsGen3Mode(c.Context()) {
			t.Fatalf("expected gen3 mode")
		}
		if !access.HasMethodAccess(c.Context(), "read", []string{"/data_file"}) {
			t.Fatalf("expected read on /data_file")
		}
		if !access.HasMethodAccess(c.Context(), "create", []string{"/programs/cbds/projects/end_to_end_test"}) {
			t.Fatalf("expected create on project resource")
		}
		return c.SendStatus(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("test request failed: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
}

func TestGen3MockAuthRequireHeader(t *testing.T) {
	t.Setenv("DRS_AUTH_MOCK_ENABLED", "true")
	t.Setenv("DRS_AUTH_MOCK_REQUIRE_AUTH_HEADER", "true")
	t.Setenv("DRS_AUTH_MOCK_RESOURCES", "/data_file")
	t.Setenv("DRS_AUTH_MOCK_METHODS", "read")

	m := newTestAuthzMiddleware(slog.Default(), "gen3", "", "")
	app := fiber.New()
	app.Use(m.FiberMiddleware())
	app.Get("/", func(c fiber.Ctx) error {
		// Without header, mock privileges should not be injected.
		if access.HasMethodAccess(c.Context(), "read", []string{"/data_file"}) {
			t.Fatalf("did not expect read access without auth header")
		}
		return c.SendStatus(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("test request failed: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
}

func TestLocalAuthzCSVInjectsMethodAwarePrivileges(t *testing.T) {
	csvPath := filepath.Join(t.TempDir(), "local-authz.csv")
	content := strings.Join([]string{
		"username,password,organization,project,methods",
		"alice,alice-pass,cbds,end_to_end_test,read|write",
		"bob,bob-pass,cbds,end_to_end_test,read",
	}, "\n")
	if err := os.WriteFile(csvPath, []byte(content), 0o600); err != nil {
		t.Fatalf("write csv: %v", err)
	}
	t.Setenv("DRS_LOCAL_AUTHZ_CSV", csvPath)

	m := newTestAuthzMiddleware(slog.Default(), "local", "admin", "admin-pass")
	app := fiber.New()
	app.Use(m.FiberMiddleware())
	app.Get("/", func(c fiber.Ctx) error {
		if access.IsGen3Mode(c.Context()) {
			t.Fatalf("did not expect gen3 mode")
		}
		if !access.IsAuthzEnforced(c.Context()) {
			t.Fatalf("expected local authz enforcement")
		}
		resource := []string{"/programs/cbds/projects/end_to_end_test"}
		if !access.HasMethodAccess(c.Context(), "read", resource) {
			t.Fatalf("expected read access")
		}
		if access.GetUserPrivileges(c.Context())[resource[0]]["write"] {
			t.Fatalf("did not expect write to persist in normalized privileges")
		}
		if !access.HasMethodAccess(c.Context(), "file_upload", resource) {
			t.Fatalf("expected write alias to grant file_upload access")
		}
		if access.HasMethodAccess(c.Context(), "read", []string{"/programs/other/projects/nope"}) {
			t.Fatalf("did not expect access to another project")
		}
		return c.SendStatus(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.SetBasicAuth("alice", "alice-pass")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("test request failed: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
}

func TestLocalAuthzCSVReplacesSingleAdminCredentials(t *testing.T) {
	csvPath := filepath.Join(t.TempDir(), "local-authz.csv")
	if err := os.WriteFile(csvPath, []byte("username,password,resource,methods\nalice,alice-pass,/data_file,read\n"), 0o600); err != nil {
		t.Fatalf("write csv: %v", err)
	}
	t.Setenv("DRS_LOCAL_AUTHZ_CSV", csvPath)

	m := newTestAuthzMiddleware(slog.Default(), "local", "admin", "admin-pass")
	app := fiber.New()
	app.Use(m.FiberMiddleware())
	app.Get("/", func(c fiber.Ctx) error {
		return c.SendStatus(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.SetBasicAuth("admin", "admin-pass")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("test request failed: %v", err)
	}
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected csv users to replace admin basic credentials, got %d", resp.StatusCode)
	}
}

func TestLocalAuthzCSVDeniesAuthenticatedSubjectMissingFromCSV(t *testing.T) {
	csvPath := filepath.Join(t.TempDir(), "local-authz.csv")
	if err := os.WriteFile(csvPath, []byte("username,password,resource,methods\nalice,alice-pass,/data_file,read\n"), 0o600); err != nil {
		t.Fatalf("write csv: %v", err)
	}
	t.Setenv("DRS_LOCAL_AUTHZ_CSV", csvPath)

	m := newTestAuthzMiddleware(slog.Default(), "local", "", "")
	m.evaluator = &fixedEvaluator{decision: authentication.DecisionForbidden}
	app := fiber.New()
	app.Use(m.FiberMiddleware())
	app.Get("/", func(c fiber.Ctx) error {
		return c.SendStatus(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("test request failed: %v", err)
	}
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected subject missing from csv to be forbidden, got %d", resp.StatusCode)
	}
}

func TestAuthzMiddlewareScenarios(t *testing.T) {
	cases := []struct {
		name       string
		mode       string
		basicUser  string
		basicPass  string
		env        map[string]string
		authHeader string
		wantStatus int
		assert     func(*testing.T, fiber.Ctx)
	}{
		{
			name:       "local basic auth missing",
			mode:       "local",
			basicUser:  "user",
			basicPass:  "pass",
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:       "local basic auth valid",
			mode:       "local",
			basicUser:  "user",
			basicPass:  "pass",
			authHeader: "Basic " + base64.StdEncoding.EncodeToString([]byte("user:pass")),
			wantStatus: http.StatusOK,
			assert: func(t *testing.T, c fiber.Ctx) {
				if access.IsGen3Mode(c.Context()) {
					t.Fatalf("did not expect gen3 mode in local auth")
				}
				if access.HasAuthHeader(c.Context()) {
					t.Fatalf("did not expect auth header presence in local auth")
				}
			},
		},
		{
			name:       "gen3 no auth header",
			mode:       "gen3",
			wantStatus: http.StatusOK,
			assert: func(t *testing.T, c fiber.Ctx) {
				if !access.IsGen3Mode(c.Context()) {
					t.Fatalf("expected gen3 mode")
				}
				if access.HasAuthHeader(c.Context()) {
					t.Fatalf("did not expect auth header presence")
				}
			},
		},
		{
			name:       "gen3 malformed bearer",
			mode:       "gen3",
			authHeader: "Bearer malformed.token",
			wantStatus: http.StatusOK,
			assert: func(t *testing.T, c fiber.Ctx) {
				if !access.IsGen3Mode(c.Context()) {
					t.Fatalf("expected gen3 mode")
				}
				if !access.HasAuthHeader(c.Context()) {
					t.Fatalf("expected auth header presence")
				}
			},
		},
		{
			name: "gen3 mock auth injects privileges",
			mode: "gen3",
			env: map[string]string{
				"DRS_AUTH_MOCK_ENABLED":   "true",
				"DRS_AUTH_MOCK_RESOURCES": "/data_file,/programs/cbds/projects/end_to_end_test",
				"DRS_AUTH_MOCK_METHODS":   "read,create",
			},
			wantStatus: http.StatusOK,
			assert: func(t *testing.T, c fiber.Ctx) {
				if !access.HasMethodAccess(c.Context(), "read", []string{"/data_file"}) {
					t.Fatalf("expected read access on /data_file")
				}
				if !access.HasMethodAccess(c.Context(), "create", []string{"/programs/cbds/projects/end_to_end_test"}) {
					t.Fatalf("expected create access on scoped resource")
				}
			},
		},
		{
			name: "local authn plugin allows access",
			mode: "local",
			env: map[string]string{
				"DRS_AUTHN_PLUGIN_ENABLED": "true",
			},
			wantStatus: http.StatusOK,
			assert: func(t *testing.T, c fiber.Ctx) {
				if access.IsGen3Mode(c.Context()) {
					t.Fatalf("did not expect gen3 mode in local auth")
				}
				if access.HasAuthHeader(c.Context()) {
					t.Fatalf("did not expect auth header presence in local auth")
				}
			},
		},
		{
			name: "local authn plugin denies access",
			mode: "local",
			env: map[string]string{
				"DRS_AUTHN_PLUGIN_ENABLED": "true",
			},
			wantStatus: http.StatusUnauthorized,
		},
		{
			name: "gen3 authn plugin allows access",
			mode: "gen3",
			env: map[string]string{
				"DRS_AUTHN_PLUGIN_ENABLED": "true",
			},
			wantStatus: http.StatusOK,
		},
		{
			name: "gen3 authn plugin denies access",
			mode: "gen3",
			env: map[string]string{
				"DRS_AUTHN_PLUGIN_ENABLED": "true",
			},
			wantStatus: http.StatusUnauthorized,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for k, v := range tc.env {
				t.Setenv(k, v)
			}

			m := newTestAuthzMiddleware(slog.Default(), tc.mode, tc.basicUser, tc.basicPass)
			// Always inject dummy plugin manager for malformed bearer scenario
			if tc.name == "gen3 malformed bearer" {
				injectDummyAuthorizationEvaluator(m)
			}
			// Always inject dummy authn plugin manager for authn plugin scenarios
			if tc.name == "local authn plugin allows access" {
				injectDummyAuthenticationEvaluator(m, true)
			}
			if tc.name == "local authn plugin denies access" {
				injectDummyAuthenticationEvaluator(m, false)
			}
			if tc.name == "gen3 authn plugin allows access" {
				injectDummyAuthenticationEvaluator(m, true)
			}
			if tc.name == "gen3 authn plugin denies access" {
				injectDummyAuthenticationEvaluator(m, false)
			}
			// For gen3 authn plugin denies access, ensure an Authorization header is set
			if tc.name == "gen3 authn plugin denies access" && tc.authHeader == "" {
				tc.authHeader = "Bearer dummy-deny-token"
			}
			app := fiber.New()
			handlerCalled := false
			app.Use(m.FiberMiddleware())
			app.Get("/", func(c fiber.Ctx) error {
				handlerCalled = true
				if tc.assert != nil {
					tc.assert(t, c)
				}
				return c.SendStatus(http.StatusOK)
			})

			req := httptest.NewRequest(http.MethodGet, "/", nil)
			if tc.authHeader != "" {
				req.Header.Set("Authorization", tc.authHeader)
			}

			resp, err := app.Test(req)
			if err != nil {
				t.Fatalf("test request failed: %v", err)
			}
			if resp.StatusCode != tc.wantStatus {
				t.Fatalf("expected status %d, got %d", tc.wantStatus, resp.StatusCode)
			}
			if tc.wantStatus == http.StatusOK && !handlerCalled {
				t.Fatalf("expected handler to be called")
			}
			if tc.wantStatus != http.StatusOK && handlerCalled {
				t.Fatalf("did not expect handler to be called")
			}
		})
	}
}
