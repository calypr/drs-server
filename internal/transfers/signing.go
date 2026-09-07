package transfers

import (
	"context"
	"fmt"
	"strings"

	"github.com/calypr/syfon/internal/objects"
	"github.com/calypr/syfon/internal/storage"
	"github.com/calypr/syfon/internal/storage/address"
)

// SignURL signs an already-resolved storage URL. S3 bucket names are passed
// as the storage access ID; arbitrary provider URLs retain an empty access ID.
func (s *Service) SignURL(ctx context.Context, accessURL string, options storage.AccessOptions) (string, error) {
	if s == nil || s.access == nil {
		return "", fmt.Errorf("storage access is not configured")
	}
	access, err := s.access.Access(ctx, storage.AccessRequest{
		Target: storage.AccessTarget{
			AccessID: resolveSigningBucket(accessURL),
			Location: accessURL,
		},
		Options: options,
	})
	if err != nil {
		return "", err
	}
	return access.Location, nil
}

// SignObjectURL resolves an object URL before signing. PUT requests use the
// canonical target path; reads retain legacy S3 and checksum-only URL repair.
func (s *Service) SignObjectURL(ctx context.Context, obj *objects.Record, accessURL string, options storage.AccessOptions) (string, error) {
	targetURL := strings.TrimSpace(accessURL)
	if strings.EqualFold(strings.TrimSpace(options.Method), "PUT") {
		target, err := s.ResolveCanonicalStorageTarget(ctx, CanonicalStorageTargetRequest{Object: obj, AccessURL: targetURL})
		if err != nil {
			return "", err
		}
		targetURL = target.URL
	} else {
		var err error
		targetURL, err = s.resolveObjectDownloadURL(ctx, obj, targetURL)
		if err != nil {
			return "", err
		}
	}
	return s.SignURL(ctx, targetURL, options)
}

// SignDownloadPart signs an inclusive byte range against an already-resolved
// URL. Range validation remains at the HTTP boundary.
func (s *Service) SignDownloadPart(ctx context.Context, bucket, accessURL string, start, end int64, options storage.AccessOptions) (string, error) {
	if s == nil || s.access == nil {
		return "", fmt.Errorf("storage access is not configured")
	}
	access, err := s.access.Access(ctx, storage.AccessRequest{
		Target:  storage.AccessTarget{AccessID: bucket, Location: accessURL},
		Options: options,
		Range:   &storage.ByteRange{Start: start, End: end},
	})
	if err != nil {
		return "", err
	}
	return access.Location, nil
}

// SignObjectDownloadPart repairs an object URL before signing its inclusive
// byte range. A parsed S3 bucket overrides the caller's legacy bucket value.
func (s *Service) SignObjectDownloadPart(ctx context.Context, obj *objects.Record, bucket, accessURL string, start, end int64, options storage.AccessOptions) (string, error) {
	resolved, err := s.resolveObjectDownloadURL(ctx, obj, accessURL)
	if err != nil {
		return "", err
	}
	if parsedBucket, _, ok := address.ParseS3URL(resolved); ok {
		bucket = parsedBucket
	}
	return s.SignDownloadPart(ctx, bucket, resolved, start, end, options)
}

func resolveSigningBucket(accessURL string) string {
	if bucket, _, ok := address.ParseS3URL(accessURL); ok {
		return bucket
	}
	return ""
}
