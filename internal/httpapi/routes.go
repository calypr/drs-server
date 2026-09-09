package httpapi

import (
	"encoding/json"
	"io"
	"strings"

	generated "github.com/calypr/syfon/apigen/drs"
	internalapi "github.com/calypr/syfon/apigen/internalapi"
	"github.com/calypr/syfon/internal/buckets"
	"github.com/calypr/syfon/internal/httpapi/apidocs"
	"github.com/calypr/syfon/internal/httpapi/middleware"
	"github.com/calypr/syfon/internal/objects"
	projectstorage "github.com/calypr/syfon/internal/projects/storage"
	"github.com/calypr/syfon/internal/transfers"
	transferlfs "github.com/calypr/syfon/internal/transfers/lfs"
	"github.com/calypr/syfon/internal/usage"
	"github.com/gofiber/fiber/v3"
)

const RouteHealthz = "/healthz"

type Dependencies struct {
	ServiceInfo    generated.Service
	Objects        *objects.Service
	Transfers      *transfers.Service
	LFS            *transferlfs.Service
	UsageIngest    usage.Ingestor
	UsageReports   usage.Reporter
	Buckets        *buckets.Service
	ProjectStorage *projectstorage.Service
	Authorization  *middleware.AuthzMiddleware
	RequestIDs     *middleware.RequestIDMiddleware
}

type Options struct {
	Docs        bool
	GA4GH       bool
	Metrics     bool
	Internal    bool
	LFS         bool
	LFSProtocol LFSOptions
}

type internalServer struct {
	objects        *objects.Service
	transfers      *transfers.Service
	projectStorage *projectstorage.Service
	buckets        *buckets.Service
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
		registerDRSRoutes(api.Group("/ga4gh/drs/v1"), deps.Objects, deps.Transfers, deps.ServiceInfo)
	}
	if options.Metrics {
		registerMetricsRoutes(api, deps.UsageReports, deps.UsageIngest)
	}
	if options.Internal {
		server := newInternalServer(deps)
		internalapi.RegisterHandlers(api, server)
		registerBucketRoutes(api, deps.Buckets, projectCleanupHandler(server))
	}
	if options.LFS {
		registerLFSRoutes(api, deps.LFS, options.LFSProtocol)
	}
}

func newInternalServer(deps Dependencies) *internalServer {
	return &internalServer{
		objects:        deps.Objects,
		transfers:      deps.Transfers,
		projectStorage: deps.ProjectStorage,
		buckets:        deps.Buckets,
	}
}

func decodeStrictJSON(body []byte, dst any) error {
	dec := json.NewDecoder(strings.NewReader(string(body)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return err
	}
	var extra any
	if err := dec.Decode(&extra); err == nil {
		return io.ErrUnexpectedEOF
	}
	return nil
}
