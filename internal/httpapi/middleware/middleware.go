package middleware

import (
	"context"
	"log/slog"
	"net/http"
	"strings"

	"github.com/calypr/syfon/internal/access"
	"github.com/calypr/syfon/internal/requestmeta"
	"github.com/calypr/syfon/plugin"
	"github.com/gofiber/fiber/v3"
)

type MockOptions struct {
	Enabled           bool
	RequireAuthHeader bool
	Resources         []string
	Methods           []string
}

type Options struct {
	Mode                    string
	Authentication          plugin.AuthenticationPlugin
	Authorization           plugin.AuthorizationPlugin
	LocalAuthzError         error
	LocalAuthzForSubject    func(string) ([]string, map[string]map[string]bool, bool)
	AuthorizationFromClaims func(map[string]interface{}) ([]string, map[string]map[string]bool, bool)
	ExtractToken            func(string) (string, error)
	ResolveToken            func(context.Context, string) ([]string, map[string]map[string]bool, bool)
	Mock                    MockOptions
}

type AuthzMiddleware struct {
	logger                  *slog.Logger
	mode                    string
	mock                    MockOptions
	pluginManager           plugin.AuthorizationPlugin
	authnPluginManager      plugin.AuthenticationPlugin
	localUsersErr           error
	localAuthzForSubject    func(string) ([]string, map[string]map[string]bool, bool)
	authorizationFromClaims func(map[string]interface{}) ([]string, map[string]map[string]bool, bool)
	extractToken            func(string) (string, error)
	resolveToken            func(context.Context, string) ([]string, map[string]map[string]bool, bool)
}

func NewAuthzMiddleware(logger *slog.Logger, options Options) *AuthzMiddleware {
	if logger == nil {
		logger = slog.Default()
	}
	return &AuthzMiddleware{
		logger:                  logger,
		mode:                    strings.ToLower(strings.TrimSpace(options.Mode)),
		mock:                    options.Mock,
		pluginManager:           options.Authorization,
		authnPluginManager:      options.Authentication,
		localUsersErr:           options.LocalAuthzError,
		localAuthzForSubject:    options.LocalAuthzForSubject,
		authorizationFromClaims: options.AuthorizationFromClaims,
		extractToken:            options.ExtractToken,
		resolveToken:            options.ResolveToken,
	}
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
