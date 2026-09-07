package httpapi

import (
	generated "github.com/calypr/syfon/apigen/server/drs"
	"github.com/calypr/syfon/internal/buckets"
	"github.com/calypr/syfon/internal/httpapi/apidocs"
	httpbuckets "github.com/calypr/syfon/internal/httpapi/buckets"
	httpdrs "github.com/calypr/syfon/internal/httpapi/drs"
	"github.com/calypr/syfon/internal/httpapi/lfs"
	"github.com/calypr/syfon/internal/httpapi/maintenance"
	"github.com/calypr/syfon/internal/httpapi/metrics"
	"github.com/calypr/syfon/internal/httpapi/middleware"
	"github.com/calypr/syfon/internal/httpapi/records"
	httptransfers "github.com/calypr/syfon/internal/httpapi/transfers"
	"github.com/calypr/syfon/internal/maintenance/projectstorage"
	"github.com/calypr/syfon/internal/maintenance/scoperepair"
	"github.com/calypr/syfon/internal/objects"
	"github.com/calypr/syfon/internal/transfers"
	"github.com/calypr/syfon/internal/usage"
	"github.com/gofiber/fiber/v3"
)

const RouteHealthz = "/healthz"

type Dependencies struct {
	ServiceInfo      generated.Service
	Objects          *objects.Service
	Transfers        *transfers.Service
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
		records.RegisterRoutes(api, deps.Objects)
		maintenance.RegisterRepairRoutes(api, deps.ScopeRepair)
		httptransfers.RegisterObjectRoutes(api, deps.Objects, deps.Transfers, deps.UsageIngest)
		maintenance.RegisterInspectionRoutes(api, deps.ProjectInspector, deps.ProjectCleanup, deps.Buckets)
		httptransfers.RegisterBulkAndMultipartRoutes(api, deps.Objects, deps.Transfers)
		httpbuckets.RegisterRoutes(api, deps.Buckets)
		maintenance.RegisterProjectCleanupRoute(api, deps.ProjectCleanup)
	}
	if options.LFS {
		lfs.RegisterLFSRoutes(api, lfs.Dependencies{
			ObjectService:   deps.Objects,
			TransferService: deps.Transfers,
			FileCounters:    deps.UsageIngest,
			Credentials:     deps.Buckets,
		}, options.LFSProtocol)
	}
}
