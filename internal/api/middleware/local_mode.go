package middleware

import (
	"context"

	"github.com/calypr/syfon/internal/access"
	"github.com/calypr/syfon/internal/requestmeta"
	"github.com/calypr/syfon/plugin"
	"github.com/gofiber/fiber/v3"
)

func (m *AuthzMiddleware) handleLocalAuth(c fiber.Ctx, ctx context.Context, authHeader string, session *access.Session) error {
	if m.localUsersErr != nil {
		m.logger.Error("local authz csv is configured but could not be loaded", "err", m.localUsersErr)
		return c.SendStatus(fiber.StatusInternalServerError)
	}
	if m.authnPluginManager != nil {
		input := &plugin.AuthenticationInput{
			RequestID:  requestmeta.GetRequestID(ctx),
			AuthHeader: authHeader,
			Metadata:   map[string]interface{}{},
		}
		output, err := m.authnPluginManager.Authenticate(ctx, input)
		if err != nil || !output.Authenticated {
			c.Set(fiber.HeaderWWWAuthenticate, `Basic realm="syfon"`)
			return c.SendStatus(fiber.StatusUnauthorized)
		}
		session.SetSubject(output.Subject)
		session.SetClaims(output.Claims)
		session.SetSource(access.SourceLocalBasic)
		if m.applyLocalAuthzClaims(session, output) {
			session.SetSource(access.SourceLocalCSV)
		} else if m.localUsers != nil && output.Subject != "" {
			if resources, privileges, ok := m.localUsers.authzForSubject(output.Subject); ok {
				session.SetAuthorizations(resources, privileges, true)
				session.SetSource(access.SourceLocalCSV)
			} else {
				return c.SendStatus(fiber.StatusForbidden)
			}
		} else if m.localUsers != nil {
			return c.SendStatus(fiber.StatusForbidden)
		}
		return m.applySession(c, ctx, session)
	}
	return m.applySession(c, ctx, session)
}

func (m *AuthzMiddleware) applyLocalAuthzClaims(session *access.Session, output *plugin.AuthenticationOutput) bool {
	if output == nil || output.Claims == nil {
		return false
	}
	resources, ok := output.Claims[localAuthzResourcesClaim].([]string)
	if !ok {
		return false
	}
	privileges, ok := output.Claims[localAuthzPrivilegesClaim].(map[string]map[string]bool)
	if !ok {
		return false
	}
	session.SetAuthorizations(resources, privileges, true)
	return true
}
