package server

import (
	"github.com/calypr/syfon/apigen/server/drs"
	"github.com/calypr/syfon/internal/buckets"
	"github.com/calypr/syfon/internal/config"
	"github.com/calypr/syfon/internal/httpapi"
	"github.com/calypr/syfon/internal/httpapi/lfs"
	"github.com/calypr/syfon/internal/httpapi/middleware"
	objectrecords "github.com/calypr/syfon/internal/objects/records"
	"github.com/calypr/syfon/internal/objects/scoperepair"
	projectstorage "github.com/calypr/syfon/internal/projects/storage"
	"github.com/calypr/syfon/internal/transfers"
	transferlfs "github.com/calypr/syfon/internal/transfers/lfs"
	"github.com/calypr/syfon/internal/usage"
	"github.com/gofiber/fiber/v3"
)

type serverRuntime struct {
	app                 *fiber.App
	cfg                 *config.Config
	serviceInfo         drs.Service
	objectService       *objectrecords.Service
	transferService     *transfers.Service
	lfsPending          transferlfs.PendingStore
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
		LFSPending:       rt.lfsPending,
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
