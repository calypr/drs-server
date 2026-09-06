package middleware

import (
	"context"
	"strings"

	"github.com/calypr/syfon/internal/access"
	"github.com/calypr/syfon/internal/access/authentication"
	"github.com/calypr/syfon/internal/requestmeta"
	"github.com/calypr/syfon/plugin"
	"github.com/gofiber/fiber/v3"
)

func (m *AuthzMiddleware) handleGen3Auth(c fiber.Ctx, ctx context.Context, authHeader string, session *access.Session) error {
	if m.mock.Enabled {
		return m.handleGen3MockAuth(c, ctx, session)
	}
	// If no Authorization header, allow the request through (public endpoint)
	if strings.TrimSpace(authHeader) == "" {
		return m.applySession(c, ctx, session)
	}
	var (
		output *plugin.AuthenticationOutput
		err    error
	)
	if m.authnPluginManager == nil {
		if m.pluginManager != nil {
			output = &plugin.AuthenticationOutput{Authenticated: true}
		} else {
			return c.SendStatus(fiber.StatusUnauthorized)
		}
	} else {
		input := &plugin.AuthenticationInput{
			RequestID:  requestmeta.GetRequestID(ctx),
			AuthHeader: authHeader,
			Metadata:   map[string]interface{}{},
		}
		output, err = m.authnPluginManager.Authenticate(ctx, input)
		if err != nil {
			m.logger.Debug("authentication failed", "error", err)
			return c.SendStatus(fiber.StatusUnauthorized)
		}
		m.logger.Debug("authentication plugin output", "authenticated", output.Authenticated, "subject", output.Subject, "claims", output.Claims, "reason", output.Reason)
	}
	// Always check authentication result
	if output == nil || !output.Authenticated {
		return c.SendStatus(fiber.StatusUnauthorized)
	}
	session.SetSubject(output.Subject)
	session.SetClaims(output.Claims)
	session.SetSource(access.SourceGen3Fence)

	tokenString, err := authentication.ExtractBearerLikeToken(authHeader)
	if err != nil {
		m.logger.Debug("failed to extract bearer token for authorization lookup", "error", err)
	} else {
		authResult := m.tokenResolver.Resolve(ctx, tokenString)
		if authResult.Negative {
			m.logger.Debug("authorization lookup failed or returned no usable privileges")
		} else {
			m.logger.Debug("authorization lookup complete", "resources", len(authResult.Resources))
			session.SetAuthorizations(authResult.Resources, authResult.Privileges, true)
		}
	}

	if err := m.authorizeWithPlugin(ctx, session, c.Method(), c.Path()); err != nil {
		return err
	}
	return m.applySession(c, ctx, session)
}

func (m *AuthzMiddleware) handleGen3MockAuth(c fiber.Ctx, ctx context.Context, session *access.Session) error {
	if m.mock.RequireAuthHeader && !session.AuthHeaderPresent {
		return m.applySession(c, ctx, session)
	}
	session.AuthHeaderPresent = true
	session.AuthzEnforced = true
	session.SetSource(access.SourceGen3Mock)
	resources, privileges := m.mockAuthPrivileges()
	session.SetAuthorizations(resources, privileges, true)
	if err := m.authorizeWithPlugin(ctx, session, c.Method(), c.Path()); err != nil {
		return err
	}
	return m.applySession(c, ctx, session)
}

func (m *AuthzMiddleware) mockAuthPrivileges() ([]string, map[string]map[string]bool) {
	resources := append([]string(nil), m.mock.Resources...)
	privs := make(map[string]map[string]bool, len(resources))
	for _, resource := range resources {
		methods := make(map[string]bool, len(m.mock.Methods))
		for _, method := range m.mock.Methods {
			methods[method] = true
		}
		privs[resource] = methods
	}
	return resources, privs
}
