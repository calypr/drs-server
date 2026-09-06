package middleware

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	conf "github.com/calypr/syfon/client/config"
	"github.com/calypr/syfon/client/logs"
	"github.com/calypr/syfon/client/request"
	"github.com/calypr/syfon/internal/access"
	"github.com/calypr/syfon/internal/access/authentication"
	"github.com/calypr/syfon/plugin"
	"github.com/gofiber/fiber/v3"
	"github.com/golang-jwt/jwt/v5"
)

func injectDummyPluginManager(m *AuthzMiddleware) {
	m.pluginManager = &DummyPluginManager{}
}

func injectDummyAuthenticationPluginManager(m *AuthzMiddleware, authenticated bool) {
	m.authnPluginManager = &DummyAuthenticationPluginManager{
		Authenticated: authenticated,
	}
}

type DummyAuthenticationPluginManager struct {
	Authenticated bool
}

func (d *DummyAuthenticationPluginManager) Authenticate(ctx context.Context, in *plugin.AuthenticationInput) (*plugin.AuthenticationOutput, error) {
	return &plugin.AuthenticationOutput{
		Authenticated: d.Authenticated,
		Subject:       "dummy",
		Claims:        map[string]interface{}{"role": "test"},
	}, nil
}

type recordingAuthenticationPlugin struct {
	input *plugin.AuthenticationInput
}

func (p *recordingAuthenticationPlugin) Authenticate(_ context.Context, in *plugin.AuthenticationInput) (*plugin.AuthenticationOutput, error) {
	p.input = in
	return &plugin.AuthenticationOutput{Authenticated: true, Subject: "recorded"}, nil
}

type recordingAuthorizationPlugin struct {
	input *plugin.AuthorizationInput
}

func (p *recordingAuthorizationPlugin) Authorize(_ context.Context, in *plugin.AuthorizationInput) (*plugin.AuthorizationOutput, error) {
	p.input = in
	return &plugin.AuthorizationOutput{Allow: true}, nil
}

func TestLocalModeBasicAuthEnforced(t *testing.T) {
	m := NewAuthzMiddleware(slog.Default(), "local", "user", "pass")
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
			m := NewAuthzMiddleware(slog.Default(), "local", "user", "pass")
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

func TestPluginInputsReceiveRequestID(t *testing.T) {
	const requestID = "request-id-for-plugin"
	authn := &recordingAuthenticationPlugin{}
	authz := &recordingAuthorizationPlugin{}
	m := NewAuthzMiddleware(slog.Default(), "gen3", "", "")
	m.authnPluginManager = authn
	m.pluginManager = authz

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
	if authn.input == nil || authn.input.RequestID != requestID {
		t.Fatalf("expected authn plugin request ID %q, got %#v", requestID, authn.input)
	}
	if authz.input == nil || authz.input.RequestID != requestID {
		t.Fatalf("expected authz plugin request ID %q, got %#v", requestID, authz.input)
	}
}

func TestGen3ModeSetsContextWithoutAuthHeader(t *testing.T) {
	m := NewAuthzMiddleware(slog.Default(), "gen3", "", "")
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
	m := NewAuthzMiddleware(slog.Default(), "gen3", "", "")
	injectDummyPluginManager(m)
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

func TestParseToken(t *testing.T) {
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate rsa key: %v", err)
	}

	kid := "test-kid"
	jwks := map[string]any{
		"keys": []map[string]any{
			{
				"kty": "RSA",
				"use": "sig",
				"kid": kid,
				"n":   base64.RawURLEncoding.EncodeToString(privateKey.PublicKey.N.Bytes()),
				"e":   base64.RawURLEncoding.EncodeToString(big.NewInt(int64(privateKey.PublicKey.E)).Bytes()),
			},
		},
	}

	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/.well-known/jwks.json" {
			http.NotFound(w, r)
			return
		}
		if err := json.NewEncoder(w).Encode(jwks); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	}))
	defer server.Close()

	oldTransport := http.DefaultTransport
	http.DefaultTransport = &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}
	defer func() { http.DefaultTransport = oldTransport }()

	issuerOrigin := server.URL
	t.Setenv("DRS_FENCE_URL", issuerOrigin)

	buildToken := func(claims jwt.MapClaims) string {
		tok := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
		tok.Header["kid"] = kid
		s, signErr := tok.SignedString(privateKey)
		if signErr != nil {
			t.Fatalf("sign token: %v", signErr)
		}
		return s
	}

	t.Run("valid token extracts endpoint and exp", func(t *testing.T) {
		const farFutureExp = 4102444800.0 // 2100-01-01T00:00:00Z
		token := buildToken(jwt.MapClaims{
			"iss": issuerOrigin + "/user",
			"exp": farFutureExp,
		})

		endpoint, exp, parseErr := authentication.ParseToken(token)
		if parseErr != nil {
			t.Fatalf("unexpected error: %v", parseErr)
		}
		if endpoint != issuerOrigin {
			t.Fatalf("expected endpoint %s, got %q", issuerOrigin, endpoint)
		}
		if exp != farFutureExp {
			t.Fatalf("expected exp %v, got %v", farFutureExp, exp)
		}
	})

	t.Run("missing iss fails", func(t *testing.T) {
		token := buildToken(jwt.MapClaims{"exp": 42})
		_, _, parseErr := authentication.ParseToken(token)
		if parseErr == nil {
			t.Fatalf("expected parse error for missing iss claim")
		}
		if !strings.Contains(parseErr.Error(), "missing or invalid 'iss' claim") {
			t.Fatalf("unexpected error: %v", parseErr)
		}
	})

	t.Run("invalid token fails", func(t *testing.T) {
		_, _, parseErr := authentication.ParseToken("not-a-token")
		if parseErr == nil {
			t.Fatalf("expected parse error")
		}
	})
}

func TestGen3ModePopulatesUserPrivilegesFromFence(t *testing.T) {
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate rsa key: %v", err)
	}

	kid := "test-kid"
	jwks := map[string]any{
		"keys": []map[string]any{
			{
				"kty": "RSA",
				"use": "sig",
				"kid": kid,
				"n":   base64.RawURLEncoding.EncodeToString(privateKey.PublicKey.N.Bytes()),
				"e":   base64.RawURLEncoding.EncodeToString(big.NewInt(int64(privateKey.PublicKey.E)).Bytes()),
			},
		},
	}
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/jwks.json":
			if err := json.NewEncoder(w).Encode(jwks); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
			}
		case "/user/user":
			if got := r.Header.Get("Authorization"); !strings.HasPrefix(got, "Bearer ") {
				t.Fatalf("expected bearer auth forwarded to user lookup, got %q", got)
			}
			resp := map[string]any{
				"authz": map[string]any{
					"/programs/test/projects/p1": []any{
						map[string]any{"service": "indexd", "method": "read"},
						map[string]any{"service": "drs", "method": "create"},
					},
				},
			}
			if err := json.NewEncoder(w).Encode(resp); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
			}
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	oldTransport := http.DefaultTransport
	http.DefaultTransport = &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}
	defer func() { http.DefaultTransport = oldTransport }()
	t.Setenv("DRS_FENCE_URL", server.URL)
	oldRequestor := authentication.NewBearerTokenRequestor
	authentication.NewBearerTokenRequestor = func(logger *logs.Gen3Logger, cred *conf.Credential, mgr conf.ManagerInterface, baseURL string, userAgent string, _ *http.Client) request.Requester {
		return request.NewBearerTokenRequestor(logger, cred, mgr, baseURL, userAgent, server.Client())
	}
	t.Cleanup(func() { authentication.NewBearerTokenRequestor = oldRequestor })

	token := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{
		"iss": server.URL,
		"exp": float64(time.Now().Add(time.Hour).Unix()),
	})
	token.Header["kid"] = kid
	tokenString, err := token.SignedString(privateKey)
	if err != nil {
		t.Fatalf("sign token: %v", err)
	}

	m := NewAuthzMiddleware(slog.Default(), "gen3", "", "")
	injectDummyAuthenticationPluginManager(m, true)
	app := fiber.New()
	app.Use(m.FiberMiddleware())
	app.Get("/", func(c fiber.Ctx) error {
		resources := access.GetUserAuthz(c.Context())
		if len(resources) != 1 || resources[0] != "/programs/test/projects/p1" {
			t.Fatalf("expected populated user resources, got %+v", resources)
		}
		if !access.HasMethodAccess(c.Context(), "read", []string{"/programs/test/projects/p1"}) {
			t.Fatalf("expected read access from Fence authz")
		}
		if !access.HasMethodAccess(c.Context(), "create", []string{"/programs/test/projects/p1"}) {
			t.Fatalf("expected create access from Fence authz")
		}
		return c.SendStatus(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer "+tokenString)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("test request failed: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
}

func TestExtractPrivileges(t *testing.T) {
	privs := map[string]any{
		"/programs/a/projects/b": []any{
			map[string]any{"service": "drs", "method": "read"},
			map[string]any{"service": "indexd", "method": "create"},
			map[string]any{"service": "*", "method": "delete"},
			map[string]any{"service": "arborist", "method": "create-descendant"},
			map[string]any{"service": "drs"}, // missing method
			"bad-entry",
		},
		"/programs/a": "not-a-list",
	}

	resources, out := authentication.ExtractPrivileges(privs)
	if len(resources) != 2 {
		t.Fatalf("expected 2 resources, got %d", len(resources))
	}
	methods := out["/programs/a/projects/b"]
	if !methods["read"] || !methods["create"] || !methods["delete"] {
		t.Fatalf("expected read/create/delete methods from accepted services, got %v", methods)
	}
	if !methods["arborist:create-descendant"] {
		t.Fatalf("expected arborist create-descendant to be preserved as a qualified privilege, got %v", methods)
	}
	if methods["create-descendant"] {
		t.Fatalf("did not expect arborist create-descendant to be promoted to an unqualified method")
	}
	if len(out["/programs/a"]) != 0 {
		t.Fatalf("expected empty method map for malformed privilege list")
	}
}

func TestExtractBearerLikeToken(t *testing.T) {
	t.Run("bearer token", func(t *testing.T) {
		token, err := authentication.ExtractBearerLikeToken("Bearer abc.def.ghi")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if token != "abc.def.ghi" {
			t.Fatalf("unexpected token: %q", token)
		}
	})

	t.Run("basic token in password", func(t *testing.T) {
		encoded := base64.StdEncoding.EncodeToString([]byte("oauth2:abc.def.ghi"))
		token, err := authentication.ExtractBearerLikeToken(fmt.Sprintf("Basic %s", encoded))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if token != "abc.def.ghi" {
			t.Fatalf("unexpected token: %q", token)
		}
	})

	t.Run("unsupported scheme", func(t *testing.T) {
		_, err := authentication.ExtractBearerLikeToken("Digest abc")
		if err == nil {
			t.Fatalf("expected error for unsupported scheme")
		}
	})
}

func TestGen3MockAuthInjectsPrivileges(t *testing.T) {
	t.Setenv("DRS_AUTH_MOCK_ENABLED", "true")
	t.Setenv("DRS_AUTH_MOCK_RESOURCES", "/data_file,/programs/cbds/projects/end_to_end_test")
	t.Setenv("DRS_AUTH_MOCK_METHODS", "read,file_upload,create,update,delete")

	m := NewAuthzMiddleware(slog.Default(), "gen3", "", "")
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

	m := NewAuthzMiddleware(slog.Default(), "gen3", "", "")
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

	m := NewAuthzMiddleware(slog.Default(), "local", "admin", "admin-pass")
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

	m := NewAuthzMiddleware(slog.Default(), "local", "admin", "admin-pass")
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

	m := NewAuthzMiddleware(slog.Default(), "local", "", "")
	injectDummyAuthenticationPluginManager(m, true)
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

			m := NewAuthzMiddleware(slog.Default(), tc.mode, tc.basicUser, tc.basicPass)
			// Always inject dummy plugin manager for malformed bearer scenario
			if tc.name == "gen3 malformed bearer" {
				injectDummyPluginManager(m)
			}
			// Always inject dummy authn plugin manager for authn plugin scenarios
			if tc.name == "local authn plugin allows access" {
				injectDummyAuthenticationPluginManager(m, true)
			}
			if tc.name == "local authn plugin denies access" {
				injectDummyAuthenticationPluginManager(m, false)
			}
			if tc.name == "gen3 authn plugin allows access" {
				injectDummyAuthenticationPluginManager(m, true)
			}
			if tc.name == "gen3 authn plugin denies access" {
				injectDummyAuthenticationPluginManager(m, false)
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

func TestJWKSDiscovery_OpenIDConfigPreferred(t *testing.T) {
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate rsa key: %v", err)
	}
	kid := "test-kid"
	jwks := map[string]any{
		"keys": []map[string]any{{
			"kty": "RSA",
			"use": "sig",
			"kid": kid,
			"n":   base64.RawURLEncoding.EncodeToString(privateKey.PublicKey.N.Bytes()),
			"e":   base64.RawURLEncoding.EncodeToString(big.NewInt(int64(privateKey.PublicKey.E)).Bytes()),
		}},
	}
	jwksServer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewEncoder(w).Encode(jwks); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	}))
	defer jwksServer.Close()
	openidConfig := map[string]any{"jwks_uri": jwksServer.URL}
	openidServer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/.well-known/openid-configuration" {
			_ = json.NewEncoder(w).Encode(openidConfig)
			return
		}
		http.NotFound(w, r)
	}))
	defer openidServer.Close()
	oldTransport := http.DefaultTransport
	http.DefaultTransport = &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}
	defer func() { http.DefaultTransport = oldTransport }()
	issuer := openidServer.URL
	t.Setenv("DRS_FENCE_URL", issuer)
	tok := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{"iss": issuer, "exp": time.Now().Add(time.Hour).Unix()})
	tok.Header["kid"] = kid
	s, err := tok.SignedString(privateKey)
	if err != nil {
		t.Fatalf("sign token: %v", err)
	}
	_, _, parseErr := authentication.ParseToken(s)
	if parseErr != nil {
		t.Fatalf("unexpected error: %v", parseErr)
	}
}

func TestJWKSDiscovery_FallbackJWKS(t *testing.T) {
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate rsa key: %v", err)
	}
	kid := "test-kid"
	jwks := map[string]any{
		"keys": []map[string]any{{
			"kty": "RSA",
			"use": "sig",
			"kid": kid,
			"n":   base64.RawURLEncoding.EncodeToString(privateKey.PublicKey.N.Bytes()),
			"e":   base64.RawURLEncoding.EncodeToString(big.NewInt(int64(privateKey.PublicKey.E)).Bytes()),
		}},
	}
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/.well-known/jwks.json" {
			_ = json.NewEncoder(w).Encode(jwks)
			return
		}
		if r.URL.Path == "/.well-known/openid-configuration" {
			http.NotFound(w, r)
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()
	oldTransport := http.DefaultTransport
	http.DefaultTransport = &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}
	defer func() { http.DefaultTransport = oldTransport }()
	issuer := server.URL
	t.Setenv("DRS_FENCE_URL", issuer)
	tok := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{"iss": issuer, "exp": time.Now().Add(time.Hour).Unix()})
	tok.Header["kid"] = kid
	s, err := tok.SignedString(privateKey)
	if err != nil {
		t.Fatalf("sign token: %v", err)
	}
	_, _, parseErr := authentication.ParseToken(s)
	if parseErr != nil {
		t.Fatalf("unexpected error: %v", parseErr)
	}
}
