package metrics

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/calypr/syfon/internal/access"
	"github.com/calypr/syfon/internal/usage"
	"github.com/gofiber/fiber/v3"
)

func metricsTestContext(base context.Context, mode string, headerSet bool, headerValue bool, privileges map[string]map[string]bool) context.Context {
	session := access.NewSession(mode)
	if headerSet {
		session.AuthHeaderPresent = headerValue
	}
	session.AuthzEnforced = mode == "gen3" || mode == "local"
	session.SetAuthorizations(nil, privileges, session.AuthzEnforced)
	return access.WithSession(base, session)
}

func registerMetricsRoutesForTest(app *fiber.App, reporter usage.Reporter, ingest usage.ProviderEventRecorder) {
	RegisterMetricsRoutes(app, reporter, ingest)
}

func newMetricsTestApp(reporter usage.Reporter, ingest usage.ProviderEventRecorder) *fiber.App {
	app := fiber.New()
	app.Use(func(c fiber.Ctx) error {
		mode := c.Get("X-Test-Auth-Mode")
		if mode == "" {
			return c.Next()
		}
		var privileges map[string]map[string]bool
		if raw := c.Get("X-Test-Privileges"); raw != "" {
			_ = json.Unmarshal([]byte(raw), &privileges)
		}
		header := c.Get("X-Test-Auth-Header")
		c.SetContext(metricsTestContext(c.Context(), mode, header != "", header == "true", privileges))
		return c.Next()
	})
	RegisterMetricsRoutes(app, reporter, ingest)
	return app
}

func setMetricsAuthHeaders(request *http.Request, mode string, header bool, privileges map[string]map[string]bool) {
	request.Header.Set("X-Test-Auth-Mode", mode)
	request.Header.Set("X-Test-Auth-Header", fmt.Sprintf("%t", header))
	if privileges != nil {
		encoded, _ := json.Marshal(privileges)
		request.Header.Set("X-Test-Privileges", string(encoded))
	}
}
