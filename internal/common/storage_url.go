package common

import (
	"fmt"
	"net/url"
	"strings"
)

func NormalizeStoragePath(rawPath, bucket string) (string, error) {
	p := strings.TrimSpace(rawPath)
	if p == "" {
		return "", nil
	}
	u, err := url.Parse(p)
	if err != nil {
		return "", fmt.Errorf("invalid storage path: %w", err)
	}

	targetBucket := strings.TrimSpace(bucket)
	if targetBucket != "" && !strings.EqualFold(strings.TrimSpace(u.Host), targetBucket) {
		return "", fmt.Errorf("path bucket %q does not match expected bucket %q", u.Host, targetBucket)
	}

	if ProviderFromScheme(u.Scheme) == "" {
		return "", fmt.Errorf("unsupported storage scheme: %s", u.Scheme)
	}

	return strings.Trim(strings.TrimSpace(u.Path), "/"), nil
}

// SchemeFromURL extracts the scheme from a URL string.
func SchemeFromURL(raw string) string {
	if i := strings.Index(raw, "://"); i != -1 {
		return strings.ToLower(raw[:i])
	}
	return ""
}

// BucketToURL converts a bucket and key to an s3:// URL.
func BucketToURL(bucket, key string) string {
	return fmt.Sprintf("s3://%s/%s", strings.TrimPrefix(bucket, "s3://"), strings.TrimPrefix(key, "/"))
}

// ParseS3URL extracts bucket/key pairs from an s3:// URL.
func ParseS3URL(raw string) (bucket string, key string, ok bool) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return "", "", false
	}
	if !strings.EqualFold(u.Scheme, "s3") {
		return "", "", false
	}
	bucket = strings.TrimSpace(u.Host)
	key = strings.TrimSpace(strings.TrimPrefix(u.Path, "/"))
	if bucket == "" || key == "" {
		return "", "", false
	}
	return bucket, key, true
}
