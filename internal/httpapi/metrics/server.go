package metrics

import (
	"context"
	"strings"

	"github.com/calypr/syfon/apigen/server/metricsapi"
	"github.com/calypr/syfon/internal/usage"
	"github.com/gofiber/fiber/v3"
)

type metricsQueryContextKey struct{}

type metricsQueryParams struct {
	organization string
	program      string
	project      string
}

type MetricsServer struct {
	reporter usage.Reporter
	ingestor usage.Ingestor
}

func NewMetricsServer(reporter usage.Reporter, ingestor usage.Ingestor) *MetricsServer {
	return &MetricsServer{
		reporter: reporter,
		ingestor: ingestor,
	}
}

func RegisterMetricsRoutes(router fiber.Router, reporter usage.Reporter, ingestor usage.Ingestor) {
	router.Use(func(c fiber.Ctx) error {
		params := metricsQueryParams{
			organization: strings.TrimSpace(c.Query("organization")),
			program:      strings.TrimSpace(c.Query("program")),
			project:      strings.TrimSpace(c.Query("project")),
		}
		c.SetContext(context.WithValue(c.Context(), metricsQueryContextKey{}, params))
		return c.Next()
	})

	server := NewMetricsServer(reporter, ingestor)
	strict := metricsapi.NewStrictHandler(server, nil)
	metricsapi.RegisterHandlers(router, strict)
}
