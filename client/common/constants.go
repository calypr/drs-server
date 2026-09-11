package common

import (
	"time"
)

const (
	// B is bytes
	B int64 = 1
	// KB is kilobytes
	KB int64 = 1024 * B
	// MB is megabytes
	MB int64 = 1024 * KB
	// GB is gigabytes
	GB int64 = 1024 * MB
	// TB is terabytes
	TB int64 = 1024 * GB
)
const (
	// DataAccessTokenEndpoint is the endpoint postfix for FENCE access token
	DataAccessTokenEndpoint = "/user/credentials/api/access_token"

	// DataMultipartInitEndpoint is the endpoint postfix for multipart init
	DataMultipartInitEndpoint = "/data/multipart/init"

	// DataMultipartUploadEndpoint is the endpoint postfix for multipart upload
	DataMultipartUploadEndpoint = "/data/multipart/upload"

	// DataMultipartCompleteEndpoint is the endpoint postfix for multipart complete
	DataMultipartCompleteEndpoint = "/data/multipart/complete"

	// DataTimeout is used specifically for large data transfers (uploads/downloads)
	DataTimeout = 5 * time.Minute

	HeaderContentType   = "Content-Type"
	MIMEApplicationJSON = "application/json"

	// FileSizeLimit is the maximum single file size for non-multipart upload (5GB)
	FileSizeLimit = 5 * GB

	// MaxRetryCount is the maximum retry number per record
	MaxRetryCount = 5
	MaxWaitTime   = 300

	MaxConcurrentUploads = 10

	OnProgressThreshold = 1 * MB

	HealthzEndpoint = "/healthz"
)
