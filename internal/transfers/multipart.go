package transfers

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/calypr/syfon/internal/faults"
	"github.com/calypr/syfon/internal/objects"
	"github.com/calypr/syfon/internal/storage"
)

// PendingMetadataTTL is the lifetime assigned by StagePendingMetadata when a
// caller has not supplied an explicit expiry.
const PendingMetadataTTL = 20 * time.Minute

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

func (s *Service) SavePendingLFSMeta(ctx context.Context, entries []PendingMetadata) error {
	if s == nil || s.pending == nil {
		return fmt.Errorf("pending LFS metadata store is not configured")
	}
	return s.pending.SavePendingLFSMeta(ctx, entries)
}

func (s *Service) GetPendingLFSMeta(ctx context.Context, oid string) (*PendingMetadata, error) {
	if s == nil || s.pending == nil {
		return nil, fmt.Errorf("pending LFS metadata store is not configured")
	}
	return s.pending.GetPendingLFSMeta(ctx, oid)
}

func (s *Service) PopPendingLFSMeta(ctx context.Context, oid string) (*PendingMetadata, error) {
	if s == nil || s.pending == nil {
		return nil, fmt.Errorf("pending LFS metadata store is not configured")
	}
	return s.pending.PopPendingLFSMeta(ctx, oid)
}

// StagePendingMetadata applies the documented 20-minute default TTL and
// stages one plain candidate. Explicit timestamps are retained so persistence
// characterization and replay callers can control them.
func (s *Service) StagePendingMetadata(ctx context.Context, metadata PendingMetadata) error {
	if strings.TrimSpace(metadata.OID) == "" {
		if oid, ok := objects.CanonicalSHA256(candidateChecksums(metadata.Candidate)); ok {
			metadata.OID = oid
		} else {
			return fmt.Errorf("%w: pending LFS metadata requires an OID", faults.ErrInvalidInput)
		}
	}
	now := time.Now().UTC()
	if s != nil && s.now != nil {
		now = s.now().UTC()
	}
	if metadata.CreatedAt.IsZero() {
		metadata.CreatedAt = now
	} else {
		metadata.CreatedAt = metadata.CreatedAt.UTC()
	}
	if metadata.ExpiresAt.IsZero() {
		metadata.ExpiresAt = metadata.CreatedAt.Add(PendingMetadataTTL)
	} else {
		metadata.ExpiresAt = metadata.ExpiresAt.UTC()
	}
	return s.SavePendingLFSMeta(ctx, []PendingMetadata{metadata})
}

func candidateChecksums(candidate objects.Candidate) []objects.Checksum {
	if candidate.Checksums == nil {
		return nil
	}
	return *candidate.Checksums
}
