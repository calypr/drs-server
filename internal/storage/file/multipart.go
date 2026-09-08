package file

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/calypr/syfon/internal/storage"
	"gocloud.dev/blob"
)

func (b *backend) SignMultipartPart(ctx context.Context, _ storage.ProviderBinding, request storage.MultipartPartRequest) (storage.SignedAccess, error) {
	partKey := storage.MultipartPartObjectKey(request.Target.Key, request.UploadID, request.PartNumber)
	signed, err := b.rootBucket.SignedURL(ctx, partKey, &blob.SignedURLOptions{
		Expiry: 15 * time.Minute,
		Method: http.MethodPut,
	})
	if err != nil {
		// Preserve the local direct-path fallback when fileblob signing is unavailable.
		return storage.SignedAccess{Location: b.pathForKey(partKey)}, nil
	}
	return storage.SignedAccess{Location: signed}, nil
}

func (b *backend) CompleteMultipart(ctx context.Context, _ storage.ProviderBinding, request storage.CompleteMultipartRequest) error {
	if len(request.Parts) == 0 {
		return fmt.Errorf("multipart complete requires at least one part")
	}
	partList := append([]storage.CompletedPart(nil), request.Parts...)
	sort.Slice(partList, func(i, j int) bool { return partList[i].PartNumber < partList[j].PartNumber })

	destinationKey := strings.Trim(strings.TrimSpace(request.Target.Key), "/")
	writer, err := b.rootBucket.NewWriter(ctx, destinationKey, nil)
	if err != nil {
		return fmt.Errorf("failed to open destination writer: %w", err)
	}
	defer writer.Close()

	cleanupKeys := make([]string, 0, len(partList))
	for _, part := range partList {
		partKey := storage.MultipartPartObjectKey(request.Target.Key, request.UploadID, part.PartNumber)
		reader, err := b.rootBucket.NewReader(ctx, partKey, nil)
		if err != nil {
			return fmt.Errorf("failed to open multipart part %d: %w", part.PartNumber, err)
		}
		if _, err := io.Copy(writer, reader); err != nil {
			if closeErr := reader.Close(); closeErr != nil {
				return fmt.Errorf("failed to copy multipart part %d: %w (close error: %v)", part.PartNumber, err, closeErr)
			}
			return fmt.Errorf("failed to copy multipart part %d: %w", part.PartNumber, err)
		}
		if err := reader.Close(); err != nil {
			return fmt.Errorf("failed to close multipart part %d reader: %w", part.PartNumber, err)
		}
		cleanupKeys = append(cleanupKeys, partKey)
	}

	if err := writer.Close(); err != nil {
		return fmt.Errorf("failed to finalize multipart object: %w", err)
	}
	for _, partKey := range cleanupKeys {
		if err := b.rootBucket.Delete(ctx, partKey); err != nil {
			return fmt.Errorf("failed to delete multipart part %s: %w", partKey, err)
		}
	}
	return nil
}

func (b *backend) pathForKey(key string) string {
	return filepath.ToSlash(filepath.Join(b.rootPath, key))
}
