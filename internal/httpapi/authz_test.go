package httpapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/calypr/syfon/internal/access"
	"github.com/gofiber/fiber/v3"
)

type fixedEvaluator struct {
	decision       access.Decision
	basicChallenge bool
}

func (e *fixedEvaluator) Evaluate(req access.EvaluationRequest) access.EvaluationResult {
	session := access.NewSession(req.Mode)
	if strings.EqualFold(req.Mode, "gen3") {
		session.AuthHeaderPresent = strings.TrimSpace(req.AuthHeader) != ""
		session.AuthzEnforced = true
	}
	return access.EvaluationResult{
		Session:        session,
		Decision:       e.decision,
		BasicChallenge: e.basicChallenge,
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
			app := fiber.New()
			app.Use(AuthorizationHandler(AuthzOptions{
				Mode:      "local",
				Evaluator: &fixedEvaluator{decision: access.DecisionUnauthorized},
			}))
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

func TestAuthorizationHandlerInstallsEvaluatorSession(t *testing.T) {
	const requestID = "request-id-for-evaluator"
	evaluator := &recordingEvaluator{}
	app := fiber.New()
	app.Use(RequestIDHandler(nil))
	app.Use(AuthorizationHandler(AuthzOptions{Mode: "gen3", Evaluator: evaluator}))
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
	request access.EvaluationRequest
}

func (e *recordingEvaluator) Evaluate(request access.EvaluationRequest) access.EvaluationResult {
	e.request = request
	return access.EvaluationResult{Session: access.NewSession(request.Mode), Decision: access.DecisionContinue}
}

func TestAuthorizationHandlerInstallsGen3ContextWithoutAuthHeader(t *testing.T) {
	app := fiber.New()
	app.Use(AuthorizationHandler(AuthzOptions{Mode: "gen3"}))
	app.Get("/", func(c fiber.Ctx) error {
		if !access.IsGen3Mode(c.Context()) {
			t.Fatalf("expected gen3 mode in context")
		}
		if access.HasAuthHeader(c.Context()) {
			t.Fatalf("did not expect auth header presence")
		}
		return c.SendStatus(http.StatusOK)
	})

	resp, err := app.Test(httptest.NewRequest(http.MethodGet, "/", nil))
	if err != nil {
		t.Fatalf("test request failed: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
}

func TestAuthorizationHandlerContinuesWithEvaluatorSession(t *testing.T) {
	app := fiber.New()
	app.Use(AuthorizationHandler(AuthzOptions{
		Mode:      "gen3",
		Evaluator: &fixedEvaluator{decision: access.DecisionContinue},
	}))
	app.Get("/", func(c fiber.Ctx) error {
		if !access.HasAuthHeader(c.Context()) {
			t.Fatalf("expected auth header presence")
		}
		return c.SendStatus(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set(fiber.HeaderAuthorization, "Bearer malformed.token")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("test request failed: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
}

func TestAuthorizationHandlerAppliesEvaluatorDecisions(t *testing.T) {
	tests := []struct {
		name           string
		decision       access.Decision
		basicChallenge bool
		wantStatus     int
		wantChallenge  string
	}{
		{name: "unauthorized", decision: access.DecisionUnauthorized, wantStatus: http.StatusUnauthorized},
		{name: "basic challenge", decision: access.DecisionUnauthorized, basicChallenge: true, wantStatus: http.StatusUnauthorized, wantChallenge: `Basic realm="syfon"`},
		{name: "forbidden", decision: access.DecisionForbidden, wantStatus: http.StatusForbidden},
		{name: "internal error", decision: access.DecisionInternalError, wantStatus: http.StatusInternalServerError},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			app := fiber.New()
			app.Use(AuthorizationHandler(AuthzOptions{
				Mode: "local",
				Evaluator: &fixedEvaluator{
					decision:       tc.decision,
					basicChallenge: tc.basicChallenge,
				},
			}))
			app.Get("/", func(c fiber.Ctx) error { return c.SendStatus(http.StatusOK) })

			resp, err := app.Test(httptest.NewRequest(http.MethodGet, "/", nil))
			if err != nil {
				t.Fatalf("test request failed: %v", err)
			}
			if resp.StatusCode != tc.wantStatus {
				t.Fatalf("expected %d, got %d", tc.wantStatus, resp.StatusCode)
			}
			if got := resp.Header.Get(fiber.HeaderWWWAuthenticate); got != tc.wantChallenge {
				t.Fatalf("WWW-Authenticate = %q, want %q", got, tc.wantChallenge)
			}
		})
	}
}
