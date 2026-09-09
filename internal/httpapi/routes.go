package httpapi

import (
	generated "github.com/calypr/syfon/apigen/drs"
	internalapi "github.com/calypr/syfon/apigen/internalapi"
	"github.com/calypr/syfon/internal/buckets"
	"github.com/calypr/syfon/internal/httpapi/apidocs"
	httpbuckets "github.com/calypr/syfon/internal/httpapi/buckets"
	httpdrs "github.com/calypr/syfon/internal/httpapi/drs"
	"github.com/calypr/syfon/internal/httpapi/lfs"
	"github.com/calypr/syfon/internal/httpapi/metrics"
	"github.com/calypr/syfon/internal/httpapi/middleware"
	"github.com/calypr/syfon/internal/objects"
	"github.com/calypr/syfon/internal/objects/scoperepair"
	projectstorage "github.com/calypr/syfon/internal/projects/storage"
	"github.com/calypr/syfon/internal/transfers"
	transferlfs "github.com/calypr/syfon/internal/transfers/lfs"
	"github.com/calypr/syfon/internal/usage"
	"github.com/gofiber/fiber/v3"
)

const RouteHealthz = "/healthz"

type Dependencies struct {
	ServiceInfo      generated.Service
	Objects          *objects.Service
	Transfers        *transfers.Service
	LFS              *transferlfs.Service
	UsageIngest      usage.Ingestor
	UsageReports     usage.Reporter
	Buckets          *buckets.Service
	ProjectInspector *projectstorage.Inspector
	ProjectCleanup   *projectstorage.ProjectCleanup
	ScopeRepair      *scoperepair.Service
	Authorization    *middleware.AuthzMiddleware
	RequestIDs       *middleware.RequestIDMiddleware
}

type Options struct {
	Docs        bool
	GA4GH       bool
	Metrics     bool
	Internal    bool
	LFS         bool
	LFSProtocol lfs.Options
}

type internalServer struct {
	objects   *objects.Service
	transfers *transfers.Service
	inspector *projectstorage.Inspector
	cleanup   *projectstorage.ProjectCleanup
	buckets   *buckets.Service
	repair    *scoperepair.Service
}

var _ internalapi.ServerInterface = (*internalServer)(nil)

func projectCleanupHandler(server *internalServer) fiber.Handler {
	return func(c fiber.Ctx) error {
		return server.InternalDeleteProject(c, c.Params("organization"), c.Params("project_id"))
	}
}

func RegisterRoutes(app fiber.Router, deps Dependencies, options Options) {
	app.Get(RouteHealthz, func(c fiber.Ctx) error {
		return c.SendString("OK")
	})

	if !options.Docs && !options.GA4GH && !options.Metrics && !options.Internal && !options.LFS {
		return
	}

	api := app.Group("/")
	var middlewares []any
	if deps.RequestIDs != nil {
		middlewares = append(middlewares, deps.RequestIDs.FiberMiddleware())
	}
	if deps.Authorization != nil {
		middlewares = append(middlewares, deps.Authorization.FiberMiddleware())
	}
	if len(middlewares) > 0 {
		api.Use(middlewares...)
	}

	if options.Docs {
		apidocs.RegisterSwaggerRoutes(api)
	}
	if options.GA4GH {
		httpdrs.RegisterDRSRoutes(api.Group("/ga4gh/drs/v1"), deps.Objects, deps.Transfers, deps.ServiceInfo)
	}
	if options.Metrics {
		metrics.RegisterMetricsRoutes(api, deps.UsageReports, deps.UsageIngest)
	}
	if options.Internal {
		server := newInternalServer(deps)
		internalapi.RegisterHandlers(api, server)
		httpbuckets.RegisterRoutes(api, deps.Buckets, projectCleanupHandler(server))
	}
	if options.LFS {
		lfs.RegisterLFSRoutes(api, lfs.Dependencies{
			Service: deps.LFS,
		}, options.LFSProtocol)
	}
}

func newInternalServer(deps Dependencies) *internalServer {
	return &internalServer{
		objects:   deps.Objects,
		transfers: deps.Transfers,
		inspector: deps.ProjectInspector,
		cleanup:   deps.ProjectCleanup,
		buckets:   deps.Buckets,
		repair:    deps.ScopeRepair,
	}
}
