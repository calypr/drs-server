package file

import (
	"bytes"
	"context"
	"crypto/md5"
	"errors"
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
	"gocloud.dev/gcerrors"
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

func (b *backend) BeginMultipart(context.Context, storage.ProviderBinding, storage.BeginMultipartRequest) (storage.UploadID, error) {
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
	if matched, err := b.multipartCompletionMatches(ctx, request.Target, request.CompletionID); err != nil {
		return err
	} else if matched {
		return nil
	}
	partList := append([]storage.CompletedPart(nil), request.Parts...)
	sort.Slice(partList, func(i, j int) bool { return partList[i].PartNumber < partList[j].PartNumber })

	destinationKey := strings.Trim(strings.TrimSpace(request.Target.Key), "/")
	writerContext, cancelWriter := context.WithCancel(ctx)
	defer cancelWriter()
	writerOptions := (*blob.WriterOptions)(nil)
	if strings.TrimSpace(request.CompletionID) != "" {
		writerOptions = &blob.WriterOptions{Metadata: map[string]string{
			storage.MultipartCompletionMarkerMetadataKey: request.CompletionID,
		}}
	}
	writer, err := b.rootBucket.NewWriter(writerContext, destinationKey, writerOptions)
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
		completionErr := fmt.Errorf("failed to finalize multipart object: %w", err)
		matched, reconcileErr := b.multipartCompletionMatches(ctx, request.Target, request.CompletionID)
		if reconcileErr != nil {
			return errors.Join(completionErr, reconcileErr)
		}
		if matched {
			return nil
		}
		return completionErr
	}
	for _, partKey := range cleanupKeys {
		if err := b.rootBucket.Delete(ctx, partKey); err != nil {
			if strings.TrimSpace(request.CompletionID) == "" {
				return fmt.Errorf("failed to delete multipart part %s: %w", partKey, err)
			}
			break
		}
	}
	return nil
}

func (b *backend) multipartCompletionMatches(ctx context.Context, target storage.Target, completionID string) (bool, error) {
	if strings.TrimSpace(completionID) == "" {
		return false, nil
	}
	destinationKey := strings.Trim(strings.TrimSpace(target.Key), "/")
	attrs, err := b.rootBucket.Attributes(ctx, destinationKey)
	if err != nil {
		if os.IsNotExist(err) || gcerrors.Code(err) == gcerrors.NotFound {
			return false, nil
		}
		return false, errors.Join(storage.ErrMultipartCompletionIndeterminate, fmt.Errorf("inspect file multipart completion marker for %s: %w", destinationKey, err))
	}
	if attrs == nil {
		return false, errors.Join(storage.ErrMultipartCompletionIndeterminate, fmt.Errorf("inspect file multipart completion marker for %s: provider returned an empty response", destinationKey))
	}
	if attrs.Metadata[storage.MultipartCompletionMarkerMetadataKey] != completionID || len(attrs.MD5) == 0 {
		return false, nil
	}

	file, err := os.Open(b.pathForKey(destinationKey))
	if err != nil {
		if os.IsNotExist(err) || gcerrors.Code(err) == gcerrors.NotFound {
			return false, nil
		}
		return false, errors.Join(storage.ErrMultipartCompletionIndeterminate, fmt.Errorf("open file multipart destination %s: %w", destinationKey, err))
	}
	hash := md5.New()
	_, copyErr := io.Copy(hash, file)
	closeErr := file.Close()
	if copyErr != nil {
		return false, errors.Join(storage.ErrMultipartCompletionIndeterminate, fmt.Errorf("read file multipart destination %s: %w", destinationKey, copyErr))
	}
	if closeErr != nil {
		return false, errors.Join(storage.ErrMultipartCompletionIndeterminate, fmt.Errorf("close file multipart destination %s: %w", destinationKey, closeErr))
	}
	return bytes.Equal(attrs.MD5, hash.Sum(nil)), nil
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
