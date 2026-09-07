package transfers

import (
	"context"
	"fmt"
	"strings"

	"github.com/calypr/syfon/internal/faults"
	"github.com/calypr/syfon/internal/objects"
	"github.com/calypr/syfon/internal/storage/address"
)

func (s *Service) resolveObjectDownloadURL(ctx context.Context, obj *objects.Record, accessURL string) (string, error) {
	accessURL = strings.TrimSpace(accessURL)
	legacyURL, err := s.resolveLegacyS3DownloadURL(ctx, obj, accessURL)
	if err != nil {
		return "", err
	}
	if legacyURL != accessURL || !isUnscopedCanonicalSHA256(obj, accessURL) {
		return legacyURL, nil
	}
	target, err := s.ResolveCanonicalStorageTarget(ctx, CanonicalStorageTargetRequest{Object: obj, AccessURL: accessURL})
	if err != nil {
		return "", err
	}
	return target.URL, nil
}

func (s *Service) resolveLegacyS3DownloadURL(ctx context.Context, obj *objects.Record, accessURL string) (string, error) {
	accessURL = strings.TrimSpace(accessURL)
	bucket, key, ok := parseS3Location(accessURL)
	if !ok || strings.TrimSpace(bucket) == "" || strings.TrimSpace(key) == "" {
		return accessURL, nil
	}
	scopes, err := s.bucketScopesForObject(ctx, obj)
	if err != nil {
		return "", err
	}
	mappedURLs := make([]string, 0, 1)
	for _, scope := range scopes {
		prefix := strings.Trim(strings.TrimSpace(scope.PathPrefix), "/")
		if prefix == "" || bucket != prefix {
			continue
		}
		targetBucket := strings.TrimSpace(scope.Bucket)
		if targetBucket == "" {
			continue
		}
		mappedKey := prefix + "/" + strings.TrimLeft(key, "/")
		candidate := address.BucketToURL(targetBucket, mappedKey)
		if len(mappedURLs) == 0 || mappedURLs[len(mappedURLs)-1] != candidate {
			mappedURLs = append(mappedURLs, candidate)
		}
	}
	if len(mappedURLs) == 0 {
		return accessURL, nil
	}
	if s.credentials != nil {
		credentials, err := s.credentials.ListS3Credentials(ctx)
		if err != nil {
			return "", err
		}
		for _, credential := range credentials {
			if strings.TrimSpace(credential.Bucket) == bucket {
				return accessURL, nil
			}
		}
	}
	if len(mappedURLs) > 1 {
		return "", fmt.Errorf("%w: legacy S3 URL %q maps to conflicting physical locations %q and %q", faults.ErrConflict, accessURL, mappedURLs[0], mappedURLs[1])
	}
	return mappedURLs[0], nil
}

func isUnscopedCanonicalSHA256(obj *objects.Record, accessURL string) bool {
	if obj == nil {
		return false
	}
	_, key, ok := parseS3Location(accessURL)
	if !ok || strings.Contains(strings.Trim(key, "/"), "/") {
		return false
	}
	sha, ok := objects.CanonicalSHA256(obj.Checksums)
	return ok && strings.EqualFold(strings.Trim(key, "/"), strings.TrimSpace(sha))
}
