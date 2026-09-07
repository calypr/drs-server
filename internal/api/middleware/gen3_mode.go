package middleware

import (
	"context"
	"strings"

	conf "github.com/calypr/syfon/client/config"
	"github.com/calypr/syfon/client/logs"
	"github.com/calypr/syfon/internal/access"
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
	// Authenticate first
	var (
		output *plugin.AuthenticationOutput
		err    error
	)
	if m.authnPluginManager == nil {
		// TEST MODE: If pluginManager is set but no authnPluginManager, treat as authenticated (for plugin integration tests)
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

	tokenString, err := extractBearerLikeToken(authHeader)
	if err != nil {
		m.logger.Debug("failed to extract bearer token for authorization lookup", "error", err)
	} else {
		authResult := m.resolveTokenAuth(ctx, tokenString)
		if authResult.negative {
			m.logger.Debug("authorization lookup failed or returned no usable privileges")
		} else {
			m.logger.Debug("authorization lookup complete", "resources", len(authResult.resources))
			session.SetAuthorizations(authResult.resources, authResult.privileges, true)
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

type authFetchResult struct {
	resources  []string
	privileges map[string]map[string]bool
	negative   bool
}

func (m *AuthzMiddleware) resolveTokenAuth(ctx context.Context, tokenString string) authFetchResult {
	v, err := m.fetchTokenAuth(ctx, tokenString)
	if err != nil {
		m.logger.Debug("failed to resolve token auth", "error", err)
		return authFetchResult{negative: true}
	}
	res, ok := v.(authFetchResult)
	if !ok {
		m.logger.Debug("unexpected token auth result type")
		return authFetchResult{negative: true}
	}
	return res
}

func (m *AuthzMiddleware) fetchTokenAuth(ctx context.Context, tokenString string) (interface{}, error) {
	// 1. Discover Fence API endpoint from token 'iss' claim
	apiEndpoint, _, err := m.parseToken(tokenString)
	if err != nil {
		m.logger.Debug("failed to parse token", "error", err)
		return authFetchResult{negative: true}, nil
	}

	// 2. Initialize request client for authz lookup.
	cred := &conf.Credential{
		AccessToken: tokenString,
		APIEndpoint: apiEndpoint,
	}

	// We use a no-op gen3 logger for the request client to avoid unnecessary side effects in middleware
	gen3Logger := logs.NewGen3Logger(m.logger, "", "syfon")
	reqClient := newBearerTokenRequestor(gen3Logger, cred, nil, apiEndpoint, "syfon-server", nil)

	// 3. Fetch user info (privileges)
	privs, err := fetchPrivileges(ctx, reqClient, cred)
	if err != nil {
		m.logger.Debug("failed to check privileges with internal auth", "error", err)
		return authFetchResult{negative: true}, nil
	}

	// 4. Map privileges to authorized resources + methods
	authorizedResources, privileges := m.extractPrivileges(privs)
	return authFetchResult{
		resources:  authorizedResources,
		privileges: privileges,
		negative:   false,
	}, nil
}

func (m *AuthzMiddleware) extractPrivileges(privs map[string]any) ([]string, map[string]map[string]bool) {
	resources := make([]string, 0, len(privs))
	out := make(map[string]map[string]bool, len(privs))
	for path, raw := range privs {
		resources = append(resources, path)
		methods := map[string]bool{}
		entries, ok := raw.([]any)
		if !ok {
			out[path] = methods
			continue
		}
		for _, entry := range entries {
			mm, ok := entry.(map[string]any)
			if !ok {
				continue
			}
			service, _ := mm["service"].(string)
			method, _ := mm["method"].(string)
			if method == "" {
				continue
			}
			service = strings.ToLower(strings.TrimSpace(service))
			method = strings.ToLower(strings.TrimSpace(method))
			switch service {
			case "", "*", "indexd", "drs":
				methods[method] = true
				if service != "" {
					methods[service+":"+method] = true
				}
			default:
				methods[service+":"+method] = true
			}
		}
		out[path] = methods
	}
	return resources, out
}
