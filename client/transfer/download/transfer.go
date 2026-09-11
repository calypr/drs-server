package download

import (
	"context"

	"github.com/calypr/syfon/client/common"
	"github.com/calypr/syfon/client/transfer"
	"github.com/calypr/syfon/client/transfer/engine"
)

type DownloadOptions struct {
	MultipartThreshold int64
	ChunkSize          int64
	Concurrency        int
	RetryStrategy      transfer.RetryStrategy
}

// DownloadFile downloads an object using the standard client thresholds.
func DownloadFile(ctx context.Context, backend transfer.ReadBackend, guid, destination string) error {
	return DownloadToPathWithOptions(ctx, backend, guid, destination, DownloadOptions{
		MultipartThreshold: 5 * common.GB,
	})
}

func DownloadToPathWithOptions(
	ctx context.Context,
	bk transfer.ReadBackend,
	guid string,
	dstPath string,
	opts DownloadOptions,
) error {
	return engine.Download(ctx, bk, guid, dstPath, engine.DownloadOptions{
		MultipartThreshold: opts.MultipartThreshold,
		ChunkSize:          opts.ChunkSize,
		Concurrency:        opts.Concurrency,
		RetryStrategy:      opts.RetryStrategy,
	})
}
