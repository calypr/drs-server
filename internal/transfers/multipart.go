package transfers

import (
	"context"
	"fmt"

	"github.com/calypr/syfon/internal/storage"
)

// InitMultipartUpload starts a provider multipart upload and returns its
// opaque provider ID unchanged.
func (s *Service) InitMultipartUpload(ctx context.Context, bucket, key string) (string, error) {
	if s == nil || s.multipart == nil {
		return "", fmt.Errorf("storage multipart is not configured")
	}
	uploadID, err := s.multipart.BeginMultipart(ctx, storage.ObjectTarget{Bucket: bucket, Key: key})
	return string(uploadID), err
}

// SignMultipartPart delegates provider part signing without changing the
// part number or opaque upload ID.
func (s *Service) SignMultipartPart(ctx context.Context, bucket, key, uploadID string, partNumber int32) (string, error) {
	if s == nil || s.multipart == nil {
		return "", fmt.Errorf("storage multipart is not configured")
	}
	access, err := s.multipart.AccessMultipartPart(ctx, storage.MultipartPartRequest{
		Target:     storage.ObjectTarget{Bucket: bucket, Key: key},
		UploadID:   storage.UploadID(uploadID),
		PartNumber: partNumber,
	})
	if err != nil {
		return "", err
	}
	return access.Location, nil
}

// CompleteMultipartUpload delegates completion in the caller-provided part
// order. Sorting and session lifecycle remain adapter responsibilities.
func (s *Service) CompleteMultipartUpload(ctx context.Context, bucket, key, uploadID string, parts []storage.CompletedPart) error {
	if s == nil || s.multipart == nil {
		return fmt.Errorf("storage multipart is not configured")
	}
	return s.multipart.CompleteMultipart(ctx, storage.CompleteMultipartRequest{
		Target:   storage.ObjectTarget{Bucket: bucket, Key: key},
		UploadID: storage.UploadID(uploadID),
		Parts:    parts,
	})
}
