package lfs

import (
	"context"

	"github.com/calypr/syfon/apigen/server/lfsapi"
	"github.com/calypr/syfon/internal/buckets"
	"github.com/calypr/syfon/internal/objects"
	"github.com/calypr/syfon/internal/transfers"
	"github.com/calypr/syfon/internal/usage"
	"github.com/gofiber/fiber/v3"
)

type Options struct {
	MaxBatchObjects              int
	MaxBatchBodyBytes            int64
	RequestLimitPerMinute        int
	BandwidthLimitBytesPerMinute int64
}

// PartUploader performs one provider PUT for a signed multipart part and
// returns the provider's opaque ETag. The default is
// storage.UploadSignedMultipartPart. Focused tests may inject another function.
type PartUploader func(context.Context, string, []byte) (string, error)

type Dependencies struct {
	ObjectService   *objects.Service
	TransferService *transfers.Service
	FileCounters    usage.FileCounterRecorder
	Credentials     buckets.CredentialReader
	PartUploader    PartUploader
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
	server := NewLFSServer(deps, effective)
	strict := lfsapi.NewStrictHandler(server, []lfsapi.StrictMiddlewareFunc{
		LFSRequestMiddleware(effective),
	})
	router.Use(func(c fiber.Ctx) error {
		c.SetContext(WithBaseURL(c.Context(), c.BaseURL()))
		return c.Next()
	})
	lfsapi.RegisterHandlers(router, strict)
}
