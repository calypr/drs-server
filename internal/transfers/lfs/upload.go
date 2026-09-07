package lfs

import (
	"context"
	"fmt"
	"io"

	"github.com/calypr/syfon/internal/storage"
	"github.com/calypr/syfon/internal/transfers"
)

const multipartPartSize = 64 * 1024 * 1024

type PartUploader func(context.Context, string, []byte) (string, error)

type UploadAccounting interface {
	RecordFileUpload(context.Context, string) error
}

type UploadWorkflow struct {
	transfer   *transfers.Service
	uploader   PartUploader
	accounting UploadAccounting
}

func NewUploadWorkflow(transfer *transfers.Service, uploader PartUploader, accounting UploadAccounting) *UploadWorkflow {
	return &UploadWorkflow{transfer: transfer, uploader: uploader, accounting: accounting}
}

func (w *UploadWorkflow) Upload(ctx context.Context, body io.Reader, bucket, key, objectID string) error {
	uploadID, err := w.transfer.InitMultipartUpload(ctx, bucket, key)
	if err != nil {
		return fmt.Errorf("failed to initialize multipart upload: %w", err)
	}
	if w.uploader == nil {
		return fmt.Errorf("failed uploading multipart part: multipart part uploader is not configured")
	}

	parts := make([]storage.CompletedPart, 0, 16)
	partNumber := int32(1)
	buffer := make([]byte, multipartPartSize)
	for {
		readCount, readErr := io.ReadFull(body, buffer)
		if readErr == io.EOF {
			break
		}
		if readErr == io.ErrUnexpectedEOF {
			if readCount == 0 {
				break
			}
		} else if readErr != nil {
			return fmt.Errorf("failed reading upload stream: %w", readErr)
		}

		partURL, err := w.transfer.SignMultipartPart(ctx, bucket, key, uploadID, partNumber)
		if err != nil {
			return fmt.Errorf("failed to sign multipart part: %w", err)
		}
		etag, err := w.uploader(ctx, partURL, buffer[:readCount])
		if err != nil {
			return fmt.Errorf("failed uploading multipart part: %w", err)
		}
		parts = append(parts, storage.CompletedPart{PartNumber: partNumber, ETag: etag})
		partNumber++
		if readErr == io.ErrUnexpectedEOF {
			break
		}
	}

	if err := w.transfer.CompleteMultipartUpload(ctx, bucket, key, uploadID, parts); err != nil {
		return fmt.Errorf("failed to complete multipart upload: %w", err)
	}
	if w.accounting == nil {
		return fmt.Errorf("failed to record upload usage: file counters are not configured")
	}
	if err := w.accounting.RecordFileUpload(ctx, objectID); err != nil {
		return fmt.Errorf("failed to record upload usage: %w", err)
	}
	return nil
}
