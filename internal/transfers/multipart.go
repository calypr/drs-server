package transfers

import (
	"context"
	"fmt"

	"github.com/calypr/syfon/internal/storage"
)

// InitMultipartUpload starts a provider multipart upload and returns its
// opaque provider ID unchanged.
func (s *Service) InitMultipartUpload(ctx context.Context, bucket, key string) (string, error) {
	if s == nil || (s.storage == nil && s.multipart == nil) {
		return "", fmt.Errorf("storage multipart is not configured")
	}
	target := storage.Target{Provider: "s3", PhysicalBucket: bucket, LookupKey: bucket, LookupCandidates: []string{bucket}, Key: key}
	if s.storage == nil {
		uploadID, err := s.multipart.BeginMultipart(ctx, target)
		return string(uploadID), err
	}
	uploadID, err := s.storage.BeginMultipart(ctx, target)
	return string(uploadID), err
}

// SignMultipartPart delegates provider part signing without changing the
// part number or opaque upload ID.
func (s *Service) SignMultipartPart(ctx context.Context, bucket, key, uploadID string, partNumber int32) (string, error) {
	if s == nil || (s.storage == nil && s.multipart == nil) {
		return "", fmt.Errorf("storage multipart is not configured")
	}
	request := storage.MultipartPartRequest{
		Target:     storage.Target{PhysicalBucket: bucket, LookupKey: bucket, LookupCandidates: []string{bucket}, Key: key},
		UploadID:   storage.UploadID(uploadID),
		PartNumber: partNumber,
	}
	var access storage.SignedAccess
	var err error
	if s.storage == nil {
		access, err = s.multipart.SignMultipartPart(ctx, request)
	} else {
		access, err = s.storage.SignMultipartPart(ctx, request)
	}
	if err != nil {
		return "", err
	}
	return access.Location, nil
}

// CompleteMultipartUpload delegates completion in the caller-provided part
// order. Sorting and session lifecycle remain adapter responsibilities.
func (s *Service) CompleteMultipartUpload(ctx context.Context, bucket, key, uploadID string, parts []storage.CompletedPart) error {
	if s == nil || (s.storage == nil && s.multipart == nil) {
		return fmt.Errorf("storage multipart is not configured")
	}
	request := storage.CompleteMultipartRequest{
		Target:   storage.Target{PhysicalBucket: bucket, LookupKey: bucket, LookupCandidates: []string{bucket}, Key: key},
		UploadID: storage.UploadID(uploadID),
		Parts:    parts,
	}
	if s.storage == nil {
		return s.multipart.CompleteMultipart(ctx, request)
	}
	return s.storage.CompleteMultipart(ctx, request)
}
