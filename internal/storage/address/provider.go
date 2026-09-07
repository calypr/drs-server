package address

import (
	"fmt"
	"net"
	"regexp"
	"strings"
)

const (
	S3Provider    = "s3"
	GCSProvider   = "gcs"
	AzureProvider = "azure"
	FileProvider  = "file"

	S3Prefix    = "s3://"
	GCSPrefix   = "gs://"
	AzurePrefix = "azblob://"
	DRSPrefix   = "drs://"
)

var (
	s3BucketNameRE           = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,61}[a-z0-9]$`)
	s3CompatibleBucketNameRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,253}[A-Za-z0-9]$`)
	azureBucketNameRE        = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,61}[a-z0-9]$`)
	gcsBucketNameRE          = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{1,220}[a-z0-9]$`)
)

func NormalizeProvider(p string, fallback string) string {
	p = strings.ToLower(strings.TrimSpace(p))
	switch p {
	case S3Provider, GCSProvider, AzureProvider, FileProvider:
		return p
	case "gs":
		return GCSProvider
	case "azblob":
		return AzureProvider
	default:
		if fallback != "" {
			return NormalizeProvider(fallback, "")
		}
		return S3Provider
	}
}

func ProviderFromScheme(scheme string) string {
	switch strings.ToLower(strings.TrimSuffix(strings.TrimSpace(scheme), "://")) {
	case "s3":
		return S3Provider
	case "gs", "gcs":
		return GCSProvider
	case "az", "azblob":
		return AzureProvider
	case "file":
		return FileProvider
	default:
		return ""
	}
}

func ProviderToScheme(p string) string {
	switch NormalizeProvider(p, "") {
	case S3Provider:
		return "s3"
	case GCSProvider:
		return "gs"
	case AzureProvider:
		return "azblob"
	case FileProvider:
		return "file"
	default:
		return "s3"
	}
}

// ParseBucketProvider returns a canonical bucket provider name or an error for
// unsupported values.
func ParseBucketProvider(raw string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", S3Provider:
		return S3Provider, nil
	case GCSProvider, "gs":
		return GCSProvider, nil
	case AzureProvider, "azblob":
		return AzureProvider, nil
	case FileProvider:
		return FileProvider, nil
	default:
		return "", fmt.Errorf("unsupported provider %q", raw)
	}
}

// ValidateBucketNameWithEndpoint validates a bucket/container name for the
// given provider and endpoint. S3-compatible backends with a custom endpoint
// may allow names that would be invalid for AWS virtual-hosted DNS names.
func ValidateBucketNameWithEndpoint(providerName, bucketName, endpoint string) error {
	bucketName = strings.TrimSpace(bucketName)
	if bucketName == "" {
		return fmt.Errorf("bucket name is required")
	}

	switch NormalizeProvider(providerName, "") {
	case S3Provider:
		if strings.TrimSpace(endpoint) != "" {
			return validateS3CompatibleBucketName(bucketName)
		}
		return validateS3BucketName(bucketName)
	case GCSProvider:
		return validateGCSBucketName(bucketName)
	case AzureProvider:
		return validateAzureBucketName(bucketName)
	case FileProvider:
		return nil
	default:
		return fmt.Errorf("unsupported provider %q", providerName)
	}
}

func validateS3CompatibleBucketName(bucketName string) error {
	if len(bucketName) < 1 || len(bucketName) > 255 {
		return fmt.Errorf("bucket name %q must be 1-255 characters", bucketName)
	}
	if strings.ContainsAny(bucketName, " \t\n\r/\\") {
		return fmt.Errorf("bucket name %q is invalid (spaces and path separators are not allowed)", bucketName)
	}
	if !s3CompatibleBucketNameRE.MatchString(bucketName) {
		return fmt.Errorf("bucket name %q is invalid (letters, numbers, hyphens, underscores, and dots only; must start and end with letter or number)", bucketName)
	}
	return nil
}

func validateS3BucketName(bucketName string) error {
	if len(bucketName) < 3 || len(bucketName) > 63 {
		return fmt.Errorf("bucket name %q must be 3-63 characters", bucketName)
	}
	if !s3BucketNameRE.MatchString(bucketName) {
		return fmt.Errorf("bucket name %q is invalid (lowercase letters, numbers, hyphens only; must start and end with letter or number)", bucketName)
	}
	return nil
}

func validateAzureBucketName(bucketName string) error {
	if len(bucketName) < 3 || len(bucketName) > 63 {
		return fmt.Errorf("bucket name %q must be 3-63 characters", bucketName)
	}
	if strings.Contains(bucketName, "--") {
		return fmt.Errorf("bucket name %q is invalid (consecutive hyphens are not allowed)", bucketName)
	}
	if !azureBucketNameRE.MatchString(bucketName) {
		return fmt.Errorf("bucket name %q is invalid (lowercase letters, numbers, hyphens only; must start and end with letter or number)", bucketName)
	}
	return nil
}

func validateGCSBucketName(bucketName string) error {
	if len(bucketName) < 3 {
		return fmt.Errorf("bucket name %q must be at least 3 characters", bucketName)
	}
	if strings.ContainsAny(bucketName, " \t\n\r") {
		return fmt.Errorf("bucket name %q is invalid (spaces are not allowed)", bucketName)
	}
	if !gcsBucketNameRE.MatchString(bucketName) {
		return fmt.Errorf("bucket name %q is invalid (lowercase letters, numbers, hyphens, underscores, and dots only; must start and end with letter or number)", bucketName)
	}
	if strings.HasPrefix(bucketName, "goog") {
		return fmt.Errorf("bucket name %q is invalid (cannot begin with \"goog\")", bucketName)
	}
	if net.ParseIP(bucketName) != nil {
		return fmt.Errorf("bucket name %q is invalid (cannot be an IP address)", bucketName)
	}
	if strings.Contains(bucketName, ".") {
		if len(bucketName) > 222 {
			return fmt.Errorf("bucket name %q must be 222 characters or fewer when dots are present", bucketName)
		}
		for _, segment := range strings.Split(bucketName, ".") {
			if segment == "" || len(segment) > 63 {
				return fmt.Errorf("bucket name %q is invalid (each dot-separated component must be 1-63 characters)", bucketName)
			}
		}
	} else if len(bucketName) > 63 {
		return fmt.Errorf("bucket name %q must be 3-63 characters", bucketName)
	}
	return nil
}
