package middleware

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"strings"

	"github.com/calypr/syfon/internal/access"
	"github.com/calypr/syfon/internal/access/authentication"
	"github.com/calypr/syfon/internal/requestmeta"
	"github.com/calypr/syfon/plugin"
	"github.com/gofiber/fiber/v3"
)

type AuthzMiddleware struct {
	logger             *slog.Logger
	mode               string
	basicUser          string
	basicPass          string
	mock               authentication.MockConfig
	pluginManager      plugin.AuthorizationPlugin
	authnPluginManager plugin.AuthenticationPlugin
	localUsers         *authentication.LocalAuthzStore
	localUsersErr      error
	tokenResolver      *authentication.TokenAuthResolver
}

func NewAuthzMiddleware(logger *slog.Logger, mode, basicUser, basicPass string) *AuthzMiddleware {
	m := &AuthzMiddleware{
		logger:        logger,
		mode:          strings.ToLower(strings.TrimSpace(mode)),
		basicUser:     basicUser,
		basicPass:     basicPass,
		mock:          authentication.LoadMockAuthConfigFromEnv(),
		tokenResolver: authentication.NewTokenAuthResolver(logger),
	}
	if m.mode == "local" {
		localCSV := strings.TrimSpace(os.Getenv("DRS_LOCAL_AUTHZ_CSV"))
		if localCSV != "" {
			users, err := authentication.LoadLocalAuthzCSV(localCSV)
			if err != nil {
				m.localUsersErr = err
				logger.Error("failed to load local authz csv", "path", localCSV, "err", err)
			} else {
				m.localUsers = users
			}
		}
	}
	pluginPath := os.Getenv("SYFON_AUTHZ_PLUGIN_PATH")
	if pluginPath != "" {
		pm, err := authentication.NewAuthorizationPluginManager(pluginPath)
		if err == nil {
			m.pluginManager = pm
		}
	}
	authnPluginPath := os.Getenv("SYFON_AUTHN_PLUGIN_PATH")
	if authnPluginPath != "" {
		apm, err := authentication.NewAuthenticationPluginManager(authnPluginPath)
		if err == nil {
			m.authnPluginManager = apm
		}
	}
	if m.authnPluginManager == nil {
		if m.mode == "local" {
			m.authnPluginManager = &authentication.LocalAuthPlugin{BasicUser: basicUser, BasicPass: basicPass, Users: m.localUsers}
		} else if m.mode == "gen3" && !m.mock.Enabled {
			m.authnPluginManager = &authentication.Gen3AuthPlugin{MockConfig: m.mock}
		}
	}
	return m
}

// FiberMiddleware returns the request-wiring handler for access context and decisions.

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
