package middleware

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"

	conf "github.com/calypr/syfon/client/config"
	"github.com/calypr/syfon/client/request"
	"github.com/calypr/syfon/internal/access"
	"github.com/calypr/syfon/internal/requestmeta"
	"github.com/calypr/syfon/plugin"
	"github.com/gofiber/fiber/v3"
)

type authenticationPluginManagerInterface interface {
	Authenticate(ctx context.Context, in *plugin.AuthenticationInput) (*plugin.AuthenticationOutput, error)
}

var newBearerTokenRequestor = request.NewBearerTokenRequestor

type AuthzMiddleware struct {
	logger             *slog.Logger
	mode               string
	basicUser          string
	basicPass          string
	mock               mockAuthConfig
	pluginManager      pluginManagerInterface               // interface for testability
	authnPluginManager authenticationPluginManagerInterface // authentication plugin (interface)
	localUsers         *localAuthzStore
	localUsersErr      error
}

type mockAuthConfig struct {
	Enabled           bool
	RequireAuthHeader bool
	Resources         []string
	Methods           []string
}

func NewAuthzMiddleware(logger *slog.Logger, mode, basicUser, basicPass string) *AuthzMiddleware {
	m := &AuthzMiddleware{
		logger:    logger,
		mode:      strings.ToLower(strings.TrimSpace(mode)),
		basicUser: basicUser,
		basicPass: basicPass,
		mock:      loadMockAuthConfigFromEnv(),
	}
	if m.mode == "local" {
		localCSV := strings.TrimSpace(os.Getenv("DRS_LOCAL_AUTHZ_CSV"))
		if localCSV != "" {
			users, err := loadLocalAuthzCSV(localCSV)
			if err != nil {
				m.localUsersErr = err
				logger.Error("failed to load local authz csv", "path", localCSV, "err", err)
			} else {
				m.localUsers = users
			}
		}
	}
	// Config loading maps auth.plugin_paths.authz to this environment variable.
	pluginPath := os.Getenv("SYFON_AUTHZ_PLUGIN_PATH")
	if pluginPath != "" {
		pm, err := NewPluginManager(pluginPath)
		if err == nil {
			m.pluginManager = pm
		}
	}
	// Authentication plugin
	authnPluginPath := os.Getenv("SYFON_AUTHN_PLUGIN_PATH")
	if authnPluginPath != "" {
		apm, err := NewAuthenticationPluginManager(authnPluginPath)
		if err == nil {
			m.authnPluginManager = apm
		}
	}
	// Built-in plugins fallback
	if m.authnPluginManager == nil {
		if m.mode == "local" {
			m.authnPluginManager = &LocalAuthPlugin{BasicUser: basicUser, BasicPass: basicPass, Users: m.localUsers}
		} else if m.mode == "gen3" && !m.mock.Enabled {
			m.authnPluginManager = &Gen3AuthPlugin{MockConfig: m.mock, Logger: logger}
		}
	}
	return m
}

func clonePrivMap(in map[string]map[string]bool) map[string]map[string]bool {
	if len(in) == 0 {
		return map[string]map[string]bool{}
	}
	out := make(map[string]map[string]bool, len(in))
	for k, methods := range in {
		if methods == nil {
			out[k] = map[string]bool{}
			continue
		}
		mm := make(map[string]bool, len(methods))
		for mk, mv := range methods {
			mm[mk] = mv
		}
		out[k] = mm
	}
	return out
}

// FiberMiddleware returns a fiber middleware that extracts the token and fetches user info.

func (m *AuthzMiddleware) FiberMiddleware() fiber.Handler {
	return func(c fiber.Ctx) error {
		ctx, authHeader, session := m.prepareRequestContext(c)
		if isPublicDRSMetadataRequest(c) && strings.TrimSpace(authHeader) == "" {
			return m.applySession(c, ctx, session)
		}
		if m.mode != "gen3" {
			return m.handleLocalAuth(c, ctx, authHeader, session)
		}
		return m.handleGen3Auth(c, ctx, authHeader, session)
	}
}

// isPublicDRSMetadataRequest identifies the metadata endpoints that may be
// queried anonymously. Static mutation and access routes are excluded even
// when their final path segment resembles an object ID.
func isPublicDRSMetadataRequest(c fiber.Ctx) bool {
	if c == nil {
		return false
	}
	method := strings.ToUpper(strings.TrimSpace(c.Method()))
	path := strings.TrimSuffix(strings.TrimSpace(c.Path()), "/")
	const drsPrefix = "/ga4gh/drs/v1"
	if strings.HasPrefix(path, drsPrefix) {
		path = strings.TrimPrefix(path, drsPrefix)
	}
	if !strings.HasPrefix(path, "/objects") {
		return false
	}
	if method == http.MethodPost && path == "/objects" {
		return true
	}
	if method == http.MethodGet && strings.HasPrefix(path, "/objects/checksum/") {
		return strings.TrimPrefix(path, "/objects/checksum/") != ""
	}
	if method != http.MethodGet && method != http.MethodPost {
		return false
	}
	objectID := strings.TrimPrefix(path, "/objects/")
	if objectID == "" || strings.Contains(objectID, "/") {
		return false
	}
	switch objectID {
	case "register", "access", "delete", "access-methods", "checksum":
		return false
	default:
		return true
	}
}

func (m *AuthzMiddleware) prepareRequestContext(c fiber.Ctx) (context.Context, string, *access.Session) {
	authHeader := c.Get(fiber.HeaderAuthorization)
	session := access.NewSession(m.mode)
	if m.mode == "gen3" {
		session.AuthHeaderPresent = strings.TrimSpace(authHeader) != ""
		session.AuthzEnforced = true
	}
	ctx := access.WithSession(c.Context(), session)
	return ctx, authHeader, session
}

func (m *AuthzMiddleware) applySession(c fiber.Ctx, ctx context.Context, session *access.Session) error {
	ctx = access.WithSession(ctx, session)
	c.SetContext(ctx)
	return c.Next()
}

func (m *AuthzMiddleware) authorizeWithPlugin(ctx context.Context, session *access.Session, action, resource string) error {
	if m.pluginManager == nil {
		return nil
	}
	authzInput := &plugin.AuthorizationInput{
		RequestID: requestmeta.GetRequestID(ctx),
		Subject:   session.Subject,
		Action:    action,
		Resource:  resource,
		Claims:    session.Claims,
		Metadata:  map[string]interface{}{},
	}
	authzOutput, err := m.pluginManager.Authorize(ctx, authzInput)
	if err != nil {
		return fiber.NewError(fiber.StatusUnauthorized)
	}
	if !authzOutput.Allow {
		return fiber.NewError(fiber.StatusForbidden)
	}
	return nil
}

func fetchPrivileges(ctx context.Context, reqClient request.Requester, cred *conf.Credential) (map[string]any, error) {
	var data map[string]any
	err := reqClient.Do(ctx, http.MethodGet, "/user/user", nil, &data)
	if err != nil {
		return nil, fmt.Errorf("request user info: %w", err)
	}

	resourceAccess, ok := data["authz"].(map[string]any)
	if !ok || len(resourceAccess) == 0 {
		resourceAccess, ok = data["project_access"].(map[string]any)
		if !ok {
			return nil, fmt.Errorf("no authz/project_access found in user response")
		}
	}
	return resourceAccess, nil
}

type pluginManagerInterface interface {
	Authorize(ctx context.Context, in *plugin.AuthorizationInput) (*plugin.AuthorizationOutput, error)
}
