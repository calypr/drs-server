package file

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/calypr/syfon/internal/signer"
	"github.com/google/uuid"
	"gocloud.dev/blob"
	_ "gocloud.dev/blob/fileblob"
)

type FileSigner struct {
	rootPath   string
	rootBucket *blob.Bucket
}

// NewFileSigner creates a new FileSigner. rootPath is the base directory for file operations.
func NewFileSigner(rootPath string) (*FileSigner, error) {
	// Ensure the path is absolute or appropriately prefixed for fileblob.
	// Go CDK fileblob expects a path on the local filesystem.
	absPath, err := filepath.Abs(rootPath)
	if err != nil {
		return nil, fmt.Errorf("failed to get absolute path for %s: %w", rootPath, err)
	}

	bucket, err := blob.OpenBucket(context.Background(), "file:"+"//"+filepath.ToSlash(absPath))
	if err != nil {
		return nil, fmt.Errorf("failed to open file bucket at %s: %w", absPath, err)
	}
	return &FileSigner{rootPath: absPath, rootBucket: bucket}, nil
}

func (s *FileSigner) SignURL(ctx context.Context, bucket, key string, opts signer.SignOptions) (string, error) {
	_ = ctx
	_ = bucket
	_ = opts
	return filepath.ToSlash(filepath.Join(s.rootPath, key)), nil
}

func (s *FileSigner) SignDownloadPart(ctx context.Context, bucket, key string, start, end int64, opts signer.SignOptions) (string, error) {
	return s.SignURL(ctx, bucket, key, opts)
}

func (s *FileSigner) InitMultipartUpload(ctx context.Context, bucket, key string) (string, error) {
	return uuid.NewString(), nil
}

func (s *FileSigner) SignMultipartPart(ctx context.Context, bucket, key, uploadID string, partNumber int32) (string, error) {
	partKey := signer.MultipartPartObjectKey(key, uploadID, partNumber)
	expiry := 15 * time.Minute
	signed, err := s.rootBucket.SignedURL(ctx, partKey, &blob.SignedURLOptions{
		Expiry: expiry,
		Method: http.MethodPut,
	})
	if err != nil {
		// Fallback to a direct filesystem path when signing is unavailable.
		return filepath.ToSlash(filepath.Join(s.rootPath, partKey)), nil
	}
	return signed, nil
}

func (s *FileSigner) CompleteMultipartUpload(ctx context.Context, bucket, key, uploadID string, parts []signer.MultipartPart) error {
	if len(parts) == 0 {
		return fmt.Errorf("multipart complete requires at least one part")
	}
	partList := signer.NormalizedMultipartParts(parts)

	writer, err := s.rootBucket.NewWriter(ctx, strings.Trim(strings.TrimSpace(key), "/"), nil)
	if err != nil {
		return fmt.Errorf("failed to open destination writer: %w", err)
	}
	defer writer.Close()

	cleanupKeys := make([]string, 0, len(partList))
	for _, p := range partList {
		partKey := signer.MultipartPartObjectKey(key, uploadID, p.PartNumber)
		reader, err := s.rootBucket.NewReader(ctx, partKey, nil)
		if err != nil {
			return fmt.Errorf("failed to open multipart part %d: %w", p.PartNumber, err)
		}
		if _, err := io.Copy(writer, reader); err != nil {
			if closeErr := reader.Close(); closeErr != nil {
				return fmt.Errorf("failed to copy multipart part %d: %w (close error: %v)", p.PartNumber, err, closeErr)
			}
			return fmt.Errorf("failed to copy multipart part %d: %w", p.PartNumber, err)
		}
		if err := reader.Close(); err != nil {
			return fmt.Errorf("failed to close multipart part %d reader: %w", p.PartNumber, err)
		}
		cleanupKeys = append(cleanupKeys, partKey)
	}

	if err := writer.Close(); err != nil {
		return fmt.Errorf("failed to finalize multipart object: %w", err)
	}
	for _, partKey := range cleanupKeys {
		if err := s.rootBucket.Delete(ctx, partKey); err != nil {
			return fmt.Errorf("failed to delete multipart part %s: %w", partKey, err)
		}
	}
	return nil
}
