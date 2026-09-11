package download

import (
	"context"

	"github.com/calypr/syfon/client/transfer"
	"github.com/calypr/syfon/client/transfer/engine"
)

type DownloadOptions struct {
	MultipartThreshold int64
	ChunkSize          int64
	Concurrency        int
	RetryStrategy      transfer.RetryStrategy
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
