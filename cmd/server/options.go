package server

import (
	"github.com/calypr/syfon/apigen/drs"
	"github.com/calypr/syfon/internal/buckets"
	"github.com/calypr/syfon/internal/config"
	"github.com/calypr/syfon/internal/httpapi"
	"github.com/calypr/syfon/internal/httpapi/middleware"
	"github.com/calypr/syfon/internal/objects"
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
	objectService       *objects.Service
	transferService     *transfers.Service
	lfsService          *transferlfs.Service
	usageService        *usage.Service
	usageIngest         usage.Ingestor
	projectStorage      *projectstorage.Service
	bucketService       *buckets.Service
	authzMiddleware     *middleware.AuthzMiddleware
	requestIDMiddleware *middleware.RequestIDMiddleware
}

func registerServerRoutes(rt *serverRuntime) {
	httpapi.RegisterRoutes(rt.app, httpapi.Dependencies{
		ServiceInfo:    rt.serviceInfo,
		Objects:        rt.objectService,
		Transfers:      rt.transferService,
		LFS:            rt.lfsService,
		UsageIngest:    rt.usageIngest,
		UsageReports:   rt.usageService.Reports(),
		Buckets:        rt.bucketService,
		ProjectStorage: rt.projectStorage,
		Authorization:  rt.authzMiddleware,
		RequestIDs:     rt.requestIDMiddleware,
	}, httpapi.Options{
		Docs:     rt.cfg.Routes.Docs,
		GA4GH:    rt.cfg.Routes.Ga4gh,
		Metrics:  rt.cfg.Routes.Metrics,
		Internal: rt.cfg.Routes.Internal,
		LFS:      rt.cfg.Routes.LFS,
		LFSProtocol: httpapi.LFSOptions{
			MaxBatchObjects:              rt.cfg.LFS.MaxBatchObjects,
			MaxBatchBodyBytes:            rt.cfg.LFS.MaxBatchBodyBytes,
			RequestLimitPerMinute:        rt.cfg.LFS.RequestLimitPerMinute,
			BandwidthLimitBytesPerMinute: rt.cfg.LFS.BandwidthLimitBytesPerMinute,
		},
	})
}
