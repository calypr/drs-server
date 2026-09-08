package lfs

import (
	"github.com/calypr/syfon/apigen/lfsapi"
	transferlfs "github.com/calypr/syfon/internal/transfers/lfs"
	"github.com/gofiber/fiber/v3"
)

type Options struct {
	MaxBatchObjects              int
	MaxBatchBodyBytes            int64
	RequestLimitPerMinute        int
	BandwidthLimitBytesPerMinute int64
}

type Dependencies struct {
	Service *transferlfs.Service
}

// DefaultOptions returns the historical Git LFS limits.
func DefaultOptions() Options {
	return Options{
		MaxBatchObjects:              1000,
		MaxBatchBodyBytes:            10 * 1024 * 1024,
		RequestLimitPerMinute:        1200,
		BandwidthLimitBytesPerMinute: 0,
	}
}

func RegisterLFSRoutes(router fiber.Router, deps Dependencies, opts ...Options) {
	effective := DefaultOptions()
	if len(opts) > 0 {
		effective = opts[0]
	}
	server := NewLFSServer(deps.Service, effective)
	strict := lfsapi.NewStrictHandler(server, []lfsapi.StrictMiddlewareFunc{
		LFSRequestMiddleware(effective),
	})
	router.Use(func(c fiber.Ctx) error {
		c.SetContext(WithBaseURL(c.Context(), c.BaseURL()))
		return c.Next()
	})
	lfsapi.RegisterHandlers(router, strict)
}
