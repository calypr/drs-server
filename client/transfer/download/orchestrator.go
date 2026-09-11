package download

import (
	"context"

	"github.com/calypr/syfon/client/common"
	"github.com/calypr/syfon/client/transfer"
)

// DownloadFile is a high-level orchestrator that downloads a file using the provided backend.
func DownloadFile(ctx context.Context, bk transfer.ReadBackend, guid, destPath string) error {
	opts := DownloadOptions{
		MultipartThreshold: int64(5 * common.GB),
	}
	return DownloadToPathWithOptions(ctx, bk, guid, destPath, opts)
}
