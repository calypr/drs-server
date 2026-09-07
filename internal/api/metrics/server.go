package metrics

import (
	"context"
	"strings"

	"github.com/calypr/syfon/apigen/server/metricsapi"
	"github.com/calypr/syfon/internal/core"
	"github.com/calypr/syfon/internal/db"
	"github.com/calypr/syfon/internal/models"
	"github.com/gofiber/fiber/v3"
)

type metricsQueryContextKey struct{}

type metricsQueryParams struct {
	organization string
	program      string
	project      string
}

type MetricsServer struct {
	database db.MetricsStore
	objects  metricsObjectReader
}

type metricsObjectReader interface {
	GetObject(ctx context.Context, ident string, requiredMethod string) (*models.InternalObject, error)
	ListObjectIDsByScope(ctx context.Context, organization, project string, requiredMethod string) ([]string, error)
}

func NewMetricsServer(database db.DatabaseInterface) *MetricsServer {
	return &MetricsServer{
		database: database,
		objects:  core.NewObjectManager(database, nil),
	}
}

func RegisterMetricsRoutes(router fiber.Router, database db.DatabaseInterface) {
	router.Use(func(c fiber.Ctx) error {
		params := metricsQueryParams{
			organization: strings.TrimSpace(c.Query("organization")),
			program:      strings.TrimSpace(c.Query("program")),
			project:      strings.TrimSpace(c.Query("project")),
		}
		c.SetContext(context.WithValue(c.Context(), metricsQueryContextKey{}, params))
		return c.Next()
	})

	server := NewMetricsServer(database)
	strict := metricsapi.NewStrictHandler(server, nil)
	metricsapi.RegisterHandlers(router, strict)
}
