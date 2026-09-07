package server

import (
	"github.com/calypr/syfon/apigen/server/drs"
	"github.com/calypr/syfon/internal/buckets"
	"github.com/calypr/syfon/internal/config"
	"github.com/calypr/syfon/internal/httpapi"
	"github.com/calypr/syfon/internal/httpapi/lfs"
	"github.com/calypr/syfon/internal/httpapi/middleware"
	"github.com/calypr/syfon/internal/maintenance/projectstorage"
	"github.com/calypr/syfon/internal/maintenance/scoperepair"
	"github.com/calypr/syfon/internal/objects"
	"github.com/calypr/syfon/internal/transfers"
	"github.com/calypr/syfon/internal/usage"
	"github.com/gofiber/fiber/v3"
)

type serverRuntime struct {
	app                 *fiber.App
	cfg                 *config.Config
	serviceInfo         drs.Service
	objectService       *objects.Service
	transferService     *transfers.Service
	usageService        *usage.Service
	usageIngest         usage.Ingestor
	projectInspector    *projectstorage.Inspector
	projectCleanup      *projectstorage.ProjectCleanup
	scopeRepairService  *scoperepair.Service
	bucketService       *buckets.Service
	authzMiddleware     *middleware.AuthzMiddleware
	requestIDMiddleware *middleware.RequestIDMiddleware
}

func registerServerRoutes(rt *serverRuntime) {
	httpapi.RegisterRoutes(rt.app, httpapi.Dependencies{
		ServiceInfo:      rt.serviceInfo,
		Objects:          rt.objectService,
		Transfers:        rt.transferService,
		UsageIngest:      rt.usageIngest,
		UsageReports:     rt.usageService.Reports(),
		Buckets:          rt.bucketService,
		ProjectInspector: rt.projectInspector,
		ProjectCleanup:   rt.projectCleanup,
		ScopeRepair:      rt.scopeRepairService,
		Authorization:    rt.authzMiddleware,
		RequestIDs:       rt.requestIDMiddleware,
	}, httpapi.Options{
		Docs:     rt.cfg.Routes.Docs,
		GA4GH:    rt.cfg.Routes.Ga4gh,
		Metrics:  rt.cfg.Routes.Metrics,
		Internal: rt.cfg.Routes.Internal,
		LFS:      rt.cfg.Routes.LFS,
		LFSProtocol: lfs.Options{
			MaxBatchObjects:              rt.cfg.LFS.MaxBatchObjects,
			MaxBatchBodyBytes:            rt.cfg.LFS.MaxBatchBodyBytes,
			RequestLimitPerMinute:        rt.cfg.LFS.RequestLimitPerMinute,
			BandwidthLimitBytesPerMinute: rt.cfg.LFS.BandwidthLimitBytesPerMinute,
		},
	})
}
