package file

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/calypr/syfon/internal/storage"
	"github.com/google/uuid"
	"gocloud.dev/blob"
	_ "gocloud.dev/blob/fileblob"
)

type backend struct {
	rootPath   string
	rootBucket *blob.Bucket
}

func New(root string) (storage.Registration, error) {
	b, err := newBackend(root)
	if err != nil {
		return storage.Registration{}, err
	}
	return storage.NewRegistration("file", b), nil
}

func newBackend(root string) (*backend, error) {
	absPath, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("failed to get absolute path for %s: %w", root, err)
	}
	bucket, err := blob.OpenBucket(context.Background(), "file:"+"//"+filepath.ToSlash(absPath))
	if err != nil {
		return nil, fmt.Errorf("failed to open file bucket at %s: %w", absPath, err)
	}
	return &backend{rootPath: absPath, rootBucket: bucket}, nil
}

func (b *backend) Sign(_ context.Context, _ storage.ProviderBinding, request storage.SignRequest) (storage.SignedAccess, error) {
	return storage.SignedAccess{Location: filepath.ToSlash(filepath.Join(b.rootPath, request.Target.Key))}, nil
}

func (b *backend) BeginMultipart(context.Context, storage.ProviderBinding, storage.Target) (storage.UploadID, error) {
	return storage.UploadID(uuid.NewString()), nil
}

func (b *backend) SignMultipartPart(ctx context.Context, _ storage.ProviderBinding, request storage.MultipartPartRequest) (storage.SignedAccess, error) {
	partKey := storage.MultipartPartObjectKey(request.Target.Key, request.UploadID, request.PartNumber)
	expires := request.ExpiresIn
	if expires <= 0 {
		expires = 15 * time.Minute
	}
	signed, err := b.rootBucket.SignedURL(ctx, partKey, &blob.SignedURLOptions{Expiry: expires, Method: http.MethodPut})
	if err != nil {
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
	writerContext, cancelWriter := context.WithCancel(ctx)
	defer cancelWriter()
	writer, err := b.rootBucket.NewWriter(writerContext, destinationKey, nil)
	if err != nil {
		return fmt.Errorf("failed to open destination writer: %w", err)
	}
	abort := func(err error) error {
		cancelWriter()
		_ = writer.Close()
		return err
	}

	cleanupKeys := make([]string, 0, len(partList))
	for _, part := range partList {
		partKey := storage.MultipartPartObjectKey(request.Target.Key, request.UploadID, part.PartNumber)
		reader, err := b.rootBucket.NewReader(ctx, partKey, nil)
		if err != nil {
			return abort(fmt.Errorf("failed to open multipart part %d: %w", part.PartNumber, err))
		}
		if _, err := io.Copy(writer, reader); err != nil {
			if closeErr := reader.Close(); closeErr != nil {
				return abort(fmt.Errorf("failed to copy multipart part %d: %w (close error: %v)", part.PartNumber, err, closeErr))
			}
			return abort(fmt.Errorf("failed to copy multipart part %d: %w", part.PartNumber, err))
		}
		if err := reader.Close(); err != nil {
			return abort(fmt.Errorf("failed to close multipart part %d reader: %w", part.PartNumber, err))
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

func (b *backend) Delete(_ context.Context, _ storage.ProviderBinding, targets []storage.PhysicalTarget) error {
	for _, target := range targets {
		if strings.TrimSpace(target.Path) == "" {
			continue
		}
		if err := os.Remove(target.Path); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("delete file %s: %w", target.Path, err)
		}
	}
	return nil
}

func (b *backend) pathForKey(key string) string {
	return filepath.ToSlash(filepath.Join(b.rootPath, key))
}
