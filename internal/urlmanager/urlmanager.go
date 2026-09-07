package urlmanager

import (
	"context"

	"github.com/calypr/syfon/internal/signer"
)

// SignOptions is a type alias for backward compatibility or convenience.
type SignOptions = signer.SignOptions

// MultipartPart is a type alias for backward compatibility or convenience.
type MultipartPart = signer.MultipartPart

// SignedURLManager signs download/upload URLs for a storage backend.
type SignedURLManager interface {
	// SignURL signs a URL for the given resource (Download).
	SignURL(ctx context.Context, accessId string, url string, opts SignOptions) (string, error)

	// SignUploadURL signs a URL for uploading a resource.
	SignUploadURL(ctx context.Context, accessId string, url string, opts SignOptions) (string, error)

	// SignDownloadPart signs a URL for downloading a specific byte range.
	SignDownloadPart(ctx context.Context, accessId string, url string, start int64, end int64, opts SignOptions) (string, error)
}

// MultipartManager handles multipart lifecycle for a storage backend.
type MultipartManager interface {
	InitMultipartUpload(ctx context.Context, bucket string, key string) (string, error)
	SignMultipartPart(ctx context.Context, bucket string, key string, uploadId string, partNumber int32) (string, error)
	CompleteMultipartUpload(ctx context.Context, bucket string, key string, uploadId string, parts []MultipartPart) error
}

// UrlManager composes signed URL operations and multipart operations.
type UrlManager interface {
	SignedURLManager
	MultipartManager
}

// BucketCacheInvalidator allows callers to evict any cached signer state for a bucket.
// Implementations should treat missing buckets as a no-op.
type BucketCacheInvalidator interface {
	InvalidateBucket(bucket string)
}
