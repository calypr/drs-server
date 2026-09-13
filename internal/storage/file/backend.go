package file

import (
	"bytes"
	"context"
	"crypto/md5"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/calypr/syfon/internal/storage"
	"github.com/google/uuid"
	"gocloud.dev/blob"
	_ "gocloud.dev/blob/fileblob"
	"gocloud.dev/gcerrors"
)

type backend struct {
	rootPath      string
	rootBucket    *blob.Bucket
	bucketsMu     sync.Mutex
	buckets       map[string]*blob.Bucket
	lifecycleMu   sync.Mutex
	lifecycleCond *sync.Cond
	active        int
	closed        bool
	closeOnce     sync.Once
	closeErr      error
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
	b := &backend{rootPath: absPath, rootBucket: bucket, buckets: map[string]*blob.Bucket{absPath: bucket}}
	b.lifecycleCond = sync.NewCond(&b.lifecycleMu)
	return b, nil
}

func (b *backend) Sign(_ context.Context, binding storage.ProviderBinding, request storage.SignRequest) (storage.SignedAccess, error) {
	if err := b.beginOperation(); err != nil {
		return storage.SignedAccess{}, err
	}
	defer b.endOperation()
	return storage.SignedAccess{Location: b.pathForRoot(b.effectiveRoot(binding), request.Target.Key)}, nil
}

func (b *backend) BeginMultipart(_ context.Context, _ storage.ProviderBinding, _ storage.BeginMultipartRequest) (storage.UploadID, error) {
	if err := b.beginOperation(); err != nil {
		return "", err
	}
	defer b.endOperation()
	return storage.UploadID(uuid.NewString()), nil
}

func (b *backend) SignMultipartPart(ctx context.Context, binding storage.ProviderBinding, request storage.MultipartPartRequest) (storage.SignedAccess, error) {
	if err := b.beginOperation(); err != nil {
		return storage.SignedAccess{}, err
	}
	defer b.endOperation()
	root := b.effectiveRoot(binding)
	bucket, err := b.bucketForRoot(root)
	if err != nil {
		return storage.SignedAccess{}, err
	}
	partKey := storage.MultipartPartObjectKey(request.Target.Key, request.UploadID, request.PartNumber)
	expires := request.ExpiresIn
	if expires <= 0 {
		expires = 15 * time.Minute
	}
	signed, err := bucket.SignedURL(ctx, partKey, &blob.SignedURLOptions{Expiry: expires, Method: http.MethodPut})
	if err != nil {
		return storage.SignedAccess{Location: b.pathForRoot(root, partKey)}, nil
	}
	return storage.SignedAccess{Location: signed}, nil
}

func (b *backend) CompleteMultipart(ctx context.Context, binding storage.ProviderBinding, request storage.CompleteMultipartRequest) error {
	if err := b.beginOperation(); err != nil {
		return err
	}
	defer b.endOperation()
	if len(request.Parts) == 0 {
		return fmt.Errorf("multipart complete requires at least one part")
	}
	root := b.effectiveRoot(binding)
	bucket, err := b.bucketForRoot(root)
	if err != nil {
		return err
	}
	if matched, err := b.multipartCompletionMatchesAt(ctx, bucket, root, request.Target, request.CompletionID); err != nil {
		return err
	} else if matched {
		return b.cleanupMultipartParts(ctx, bucket, request.Target, request.UploadID, request.Parts)
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
	writer, err := bucket.NewWriter(writerContext, destinationKey, writerOptions)
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
		reader, err := bucket.NewReader(ctx, partKey, nil)
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
		matched, reconcileErr := b.multipartCompletionMatchesAt(ctx, bucket, root, request.Target, request.CompletionID)
		if reconcileErr != nil {
			return errors.Join(completionErr, reconcileErr)
		}
		if matched {
			cleanupErr := b.cleanupMultipartParts(ctx, bucket, request.Target, request.UploadID, partList)
			if cleanupErr != nil {
				return cleanupErr
			}
			return nil
		}
		return completionErr
	}
	cleanupErr := b.cleanupKeys(ctx, bucket, cleanupKeys)
	if cleanupErr != nil {
		return cleanupErr
	}
	return nil
}

func (b *backend) multipartCompletionMatchesAt(ctx context.Context, bucket *blob.Bucket, root string, target storage.Target, completionID string) (bool, error) {
	if strings.TrimSpace(completionID) == "" {
		return false, nil
	}
	destinationKey := strings.Trim(strings.TrimSpace(target.Key), "/")
	attrs, err := bucket.Attributes(ctx, destinationKey)
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

	file, err := os.Open(b.pathForRoot(root, destinationKey))
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
	if err := b.beginOperation(); err != nil {
		return err
	}
	defer b.endOperation()
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

func (b *backend) pathForRoot(root, key string) string {
	return filepath.ToSlash(filepath.Join(root, key))
}

func (b *backend) effectiveRoot(binding storage.ProviderBinding) string {
	endpoint := strings.TrimSpace(credentialEndpoint(binding))
	if endpoint == "" {
		return b.rootPath
	}
	if strings.HasPrefix(strings.ToLower(endpoint), "file://") {
		parsed, err := url.Parse(endpoint)
		if err != nil || parsed.Path == "" || (parsed.Host != "" && parsed.Host != "localhost") {
			return b.rootPath
		}
		endpoint = parsed.Path
	}
	if !filepath.IsAbs(endpoint) {
		return b.rootPath
	}
	absPath, err := filepath.Abs(endpoint)
	if err != nil {
		return b.rootPath
	}
	return filepath.Clean(absPath)
}

func credentialEndpoint(binding storage.ProviderBinding) string {
	if binding.Credential == nil {
		return ""
	}
	return binding.Credential.Endpoint
}

func (b *backend) bucketForRoot(root string) (*blob.Bucket, error) {
	root = filepath.Clean(root)
	b.bucketsMu.Lock()
	defer b.bucketsMu.Unlock()
	if b.buckets == nil && b.rootBucket != nil && root == filepath.Clean(b.rootPath) {
		b.buckets = map[string]*blob.Bucket{root: b.rootBucket}
	}
	if bucket := b.buckets[root]; bucket != nil {
		return bucket, nil
	}
	bucket, err := blob.OpenBucket(context.Background(), "file:"+"//"+filepath.ToSlash(root))
	if err != nil {
		return nil, fmt.Errorf("failed to open file bucket at %s: %w", root, err)
	}
	if b.buckets == nil {
		b.buckets = make(map[string]*blob.Bucket)
	}
	b.buckets[root] = bucket
	return bucket, nil
}

func (b *backend) cleanupMultipartParts(ctx context.Context, bucket *blob.Bucket, target storage.Target, uploadID storage.UploadID, parts []storage.CompletedPart) error {
	keys := make([]string, 0, len(parts))
	for _, part := range parts {
		keys = append(keys, storage.MultipartPartObjectKey(target.Key, uploadID, part.PartNumber))
	}
	return b.cleanupKeys(ctx, bucket, keys)
}

func (b *backend) cleanupKeys(ctx context.Context, bucket *blob.Bucket, keys []string) error {
	var cleanupErrs []error
	for _, key := range keys {
		if err := bucket.Delete(ctx, key); err != nil && !isNotFound(err) {
			cleanupErrs = append(cleanupErrs, fmt.Errorf("failed to delete multipart component %s: %w", key, err))
		}
	}
	return errors.Join(cleanupErrs...)
}

func isNotFound(err error) bool {
	return os.IsNotExist(err) || gcerrors.Code(err) == gcerrors.NotFound
}

func (b *backend) beginOperation() error {
	b.lifecycleMu.Lock()
	defer b.lifecycleMu.Unlock()
	if b.lifecycleCond == nil {
		b.lifecycleCond = sync.NewCond(&b.lifecycleMu)
	}
	if b.closed {
		return errors.New("file storage backend is closed")
	}
	b.active++
	return nil
}

func (b *backend) endOperation() {
	b.lifecycleMu.Lock()
	b.active--
	if b.active == 0 {
		b.lifecycleCond.Broadcast()
	}
	b.lifecycleMu.Unlock()
}

func (b *backend) Close() error {
	b.closeOnce.Do(func() {
		b.lifecycleMu.Lock()
		if b.lifecycleCond == nil {
			b.lifecycleCond = sync.NewCond(&b.lifecycleMu)
		}
		b.closed = true
		for b.active > 0 {
			b.lifecycleCond.Wait()
		}
		b.lifecycleMu.Unlock()

		b.bucketsMu.Lock()
		if b.buckets == nil && b.rootBucket != nil {
			b.buckets = map[string]*blob.Bucket{b.rootPath: b.rootBucket}
		}
		roots := make([]string, 0, len(b.buckets))
		buckets := make(map[string]*blob.Bucket, len(b.buckets))
		for root, bucket := range b.buckets {
			roots = append(roots, root)
			buckets[root] = bucket
		}
		b.bucketsMu.Unlock()
		sort.Strings(roots)
		var closeErrs []error
		for _, root := range roots {
			if bucket := buckets[root]; bucket != nil {
				if err := bucket.Close(); err != nil {
					closeErrs = append(closeErrs, fmt.Errorf("close file bucket %s: %w", root, err))
				}
			}
		}
		b.closeErr = errors.Join(closeErrs...)
	})
	return b.closeErr
}
