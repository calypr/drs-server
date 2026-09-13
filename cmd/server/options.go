package server

import (
	"context"
	"errors"
	"net"
	"sync"

	generated "github.com/calypr/syfon/apigen/drs"
	"github.com/calypr/syfon/internal/access/authentication"
	"github.com/calypr/syfon/internal/buckets"
	"github.com/calypr/syfon/internal/config"
	"github.com/calypr/syfon/internal/httpapi"
	"github.com/calypr/syfon/internal/objects"
	"github.com/calypr/syfon/internal/persistence/store"
	projectstorage "github.com/calypr/syfon/internal/projects/storage"
	"github.com/calypr/syfon/internal/storage"
	"github.com/calypr/syfon/internal/transfers"
	transferlfs "github.com/calypr/syfon/internal/transfers/lfs"
	"github.com/calypr/syfon/internal/usage"
	"github.com/gofiber/fiber/v3"
)

type serverRuntime struct {
	app              *fiber.App
	cfg              *config.Config
	database         *store.Store
	storageManager   *storage.Manager
	authRuntime      *authentication.Runtime
	listener         net.Listener
	closeOnce        sync.Once
	closeErr         error
	serviceInfo      generated.N200ServiceInfo
	health           *httpapi.Health
	objectService    *objects.Service
	transferService  *transfers.Service
	lfsService       *transferlfs.Service
	usageService     *usage.Service
	usageIngest      usage.Ingestor
	projectStorage   *projectstorage.Service
	bucketService    *buckets.Service
	authzHandler     fiber.Handler
	requestIDHandler fiber.Handler
	multipartCancel  context.CancelFunc
	multipartWG      sync.WaitGroup
}

// firstAcceptListener reports that the Fiber server has reached its serving
// loop. Accept is intentionally notified before delegating to the owned
// listener so startup cannot race an immediate shutdown.
type firstAcceptListener struct {
	net.Listener
	ready chan<- struct{}
	once  sync.Once
}

func (l *firstAcceptListener) Accept() (net.Conn, error) {
	l.once.Do(func() { close(l.ready) })
	return l.Listener.Accept()
}

func (rt *serverRuntime) Close(ctx context.Context) error {
	if rt == nil {
		return nil
	}
	rt.closeOnce.Do(func() {
		if ctx == nil {
			ctx = context.Background()
		}

		var cleanupErrors []error
		if rt.health != nil {
			rt.health.StopServing()
		}
		if rt.multipartCancel != nil {
			rt.multipartCancel()
			rt.multipartWG.Wait()
		}
		if rt.app != nil {
			if err := rt.app.ShutdownWithContext(ctx); err != nil && !errors.Is(err, fiber.ErrNotRunning) {
				cleanupErrors = append(cleanupErrors, err)
			}
		}
		if rt.listener != nil {
			if err := rt.listener.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
				cleanupErrors = append(cleanupErrors, err)
			}
		}
		if rt.authRuntime != nil {
			rt.authRuntime.Close()
		}
		if rt.storageManager != nil {
			if err := rt.storageManager.Close(); err != nil {
				cleanupErrors = append(cleanupErrors, err)
			}
		}
		if rt.database != nil {
			if err := rt.database.Close(); err != nil {
				cleanupErrors = append(cleanupErrors, err)
			}
		}
		rt.closeErr = errors.Join(cleanupErrors...)
	})
	return rt.closeErr
}

func registerServerRoutes(rt *serverRuntime) {
	httpapi.RegisterRoutes(rt.app, httpapi.Dependencies{
		ServiceInfo:    rt.serviceInfo,
		Objects:        rt.objectService,
		Transfers:      rt.transferService,
		LFS:            rt.lfsService,
		UsageIngest:    rt.usageIngest,
		UsageReports:   rt.usageService,
		Buckets:        rt.bucketService,
		ProjectStorage: rt.projectStorage,
		Authorization:  rt.authzHandler,
		RequestIDs:     rt.requestIDHandler,
		Health:         rt.health,
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
		MaxBulkRequestLength: rt.cfg.DRS.MaxBulkRequestLength,
	})
}
