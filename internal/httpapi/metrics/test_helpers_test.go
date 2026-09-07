package metrics

import (
	"context"

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

func registerMetricsRoutesForTest(app *fiber.App, ingest usage.Ingestor, reports usage.ReportStore, objects usage.ObjectReader) {
	service := usage.NewService(usage.Dependencies{
		Reports: reports,
		Objects: objects,
	})
	RegisterMetricsRoutes(app, service.Reports(), ingest)
}
