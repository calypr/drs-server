package address

import (
	"fmt"
	"net/url"
	"strings"
)

// ParsedLocation is the syntax and provider information extracted from a
// storage location. Path retains the parsed URL path, while Key is the object
// key with its leading slash removed.
type ParsedLocation struct {
	URL      string
	Scheme   string
	Provider string
	Bucket   string
	Key      string
	Path     string
}

// ParseLocation parses a storage URL or filesystem path once for callers
// that need both provider-neutral and provider-specific location fields.
func ParseLocation(raw string) (ParsedLocation, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ParsedLocation{}, nil
	}

	u, err := url.Parse(trimmed)
	if err != nil {
		return ParsedLocation{}, fmt.Errorf("invalid storage location: %w", err)
	}

	scheme := strings.ToLower(strings.TrimSpace(u.Scheme))
	return ParsedLocation{
		URL:      trimmed,
		Scheme:   scheme,
		Provider: ProviderFromScheme(scheme),
		Bucket:   strings.TrimSpace(u.Host),
		Key:      strings.TrimPrefix(strings.TrimSpace(u.Path), "/"),
		Path:     u.Path,
	}, nil
}

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

// TrimLeadingStoragePrefix removes a complete storage prefix from a key.
func TrimLeadingStoragePrefix(key, prefix string) string {
	key = strings.Trim(strings.TrimSpace(key), "/")
	prefix = strings.Trim(strings.TrimSpace(prefix), "/")
	if key == prefix {
		return ""
	}
	return strings.TrimPrefix(key, prefix+"/")
}
