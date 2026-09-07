package upload

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/calypr/syfon/client/common"
	"github.com/calypr/syfon/client/transfer"
)

type singleUploadBackendAdapter struct {
	transfer.Uploader
}

func (a singleUploadBackendAdapter) MultipartInit(context.Context, string) (string, error) {
	return "", fmt.Errorf("multipart upload not supported by UploadSingle compatibility adapter")
}

func (a singleUploadBackendAdapter) MultipartPart(context.Context, string, string, int, io.Reader) (string, error) {
	return "", fmt.Errorf("multipart upload not supported by UploadSingle compatibility adapter")
}

func (a singleUploadBackendAdapter) MultipartComplete(context.Context, string, string, []transfer.MultipartPart) error {
	return fmt.Errorf("multipart upload not supported by UploadSingle compatibility adapter")
}

// UploadSingle is a compatibility shim that preserves the old entrypoint while
// routing the actual byte transfer through the shared engine uploader.
func UploadSingle(ctx context.Context, bk transfer.Uploader, logger transfer.TransferLogger, sourcePath, objectKey, guid, bucket string, metadata common.FileMetadata, showProgress bool) error {
	info, err := os.Stat(sourcePath)
	if err != nil {
		logger.Failed(sourcePath, objectKey, common.FileMetadata{}, "", 0, false)
		return fmt.Errorf("error opening file %s: %w", sourcePath, err)
	}
	if info.Size() > common.FileSizeLimit {
		err := fmt.Errorf("file size exceeds limit")
		logger.Failed(sourcePath, objectKey, metadata, guid, 0, false)
		return err
	}

	err = UploadWithOptions(ctx, singleUploadBackendAdapter{Uploader: bk}, sourcePath, objectKey, guid, bucket, metadata, showProgress, false)
	if err != nil {
		logger.Failed(sourcePath, objectKey, metadata, guid, 0, false)
		return err
	}

	logger.Succeeded(sourcePath, guid)
	return nil
}
