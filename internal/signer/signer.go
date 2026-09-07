package signer

import (
	"context"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"
)

// SignOptions contains optional parameters for signing.
type SignOptions struct {
	ExpiresIn time.Duration
	Method    string
	// DownloadFilename, when set for GET-style URLs, asks the backend to serve
	// the object with this presentation filename.
	DownloadFilename string
}

// MultipartPart represents a part in a multipart upload.
type MultipartPart struct {
	PartNumber int32
	ETag       string
}

// Signer is the interface for cloud-specific URL signing and multipart operations.
type Signer interface {
	// SignURL signs a URL for the given resource (usually for Get or Put).
	SignURL(ctx context.Context, bucket, key string, opts SignOptions) (string, error)

	// SignDownloadPart signs a URL for downloading a specific byte range.
	SignDownloadPart(ctx context.Context, bucket, key string, start int64, end int64, opts SignOptions) (string, error)

	// InitMultipartUpload initializes a multipart upload and returns an upload ID.
	InitMultipartUpload(ctx context.Context, bucket, key string) (string, error)

	// SignMultipartPart signs a URL for uploading a specific part of a multipart upload.
	SignMultipartPart(ctx context.Context, bucket, key, uploadID string, partNumber int32) (string, error)

	// CompleteMultipartUpload finalizes a multipart upload.
	CompleteMultipartUpload(ctx context.Context, bucket, key, uploadID string, parts []MultipartPart) error
}

// NormalizedMultipartParts sorts parts by their part number.
func NormalizedMultipartParts(parts []MultipartPart) []MultipartPart {
	partList := append([]MultipartPart(nil), parts...)
	sort.Slice(partList, func(i, j int) bool {
		return partList[i].PartNumber < partList[j].PartNumber
	})
	return partList
}

// MultipartPartObjectKey returns the storage key for a specific part of a multipart upload.
func MultipartPartObjectKey(key, uploadID string, partNumber int32) string {
	cleanKey := strings.Trim(strings.TrimSpace(key), "/")
	return path.Join(".syfon-multipart", strings.TrimSpace(uploadID), cleanKey, "parts", strconv.Itoa(int(partNumber)))
}
