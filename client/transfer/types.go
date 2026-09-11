package transfer

import (
	"context"
	"fmt"
	"io"

	internalapi "github.com/calypr/syfon/apigen/internalapi"
	"github.com/calypr/syfon/client/common"
)

var ErrRangeIgnored = fmt.Errorf("server ignored range request and returned full body")

type TransferLogger interface {
	Error(msg string, args ...any)
	Printf(format string, v ...any)
}

// ObjectMetadata carries provider-agnostic information about a storage target.
type ObjectMetadata struct {
	Size         int64
	AcceptRanges bool
	Provider     string
}

// TransferRequest represents a request to move a single file.
type TransferRequest struct {
	SourcePath     string
	ObjectKey      string
	GUID           string
	Bucket         string
	Metadata       common.FileMetadata
	ForceMultipart bool
}

// ReadBackend provides metadata, single-stream reads, and ranged reads.
type ReadBackend interface {
	Logger() TransferLogger
	Stat(ctx context.Context, guid string) (*ObjectMetadata, error)
	GetReader(ctx context.Context, guid string) (io.ReadCloser, error)
	GetRangeReader(ctx context.Context, guid string, offset, length int64) (io.ReadCloser, error)
}

type MultipartPart struct {
	PartNumber int32
	ETag       string
}

type MultipartBackend interface {
	Logger() TransferLogger
	Upload(ctx context.Context, guid string, body io.Reader, size int64) error
	MultipartInit(ctx context.Context, guid string) (string, error)
	MultipartPart(ctx context.Context, guid string, uploadID string, partNum int, body io.Reader) (string, error)
	MultipartComplete(ctx context.Context, guid string, uploadID string, parts []MultipartPart) error
}

type Uploader interface {
	Logger() TransferLogger
	ResolveUploadURL(ctx context.Context, guid string, filename string, metadata common.FileMetadata, bucket string) (string, error)
	InitMultipartUpload(ctx context.Context, guid string, filename string, bucket string) (uploadID string, key string, err error)
	GetMultipartUploadURL(ctx context.Context, key string, uploadID string, partNumber int32, bucket string) (string, error)
	CompleteMultipartUpload(ctx context.Context, key string, uploadID string, parts []internalapi.InternalMultipartPart, bucket string) error
	Upload(ctx context.Context, url string, body io.Reader, size int64) error
	UploadPart(ctx context.Context, url string, body io.Reader, size int64) (string, error)
	DeleteFile(ctx context.Context, guid string) (string, error)
	CanonicalObjectURL(signedURL, bucketHint, fallbackDID string) (string, error)
}

// NoOpLogger satisfies TransferLogger without emitting output.
type NoOpLogger struct{}

func (NoOpLogger) Error(string, ...any)  {}
func (NoOpLogger) Printf(string, ...any) {}
