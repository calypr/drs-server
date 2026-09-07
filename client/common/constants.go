package common

import (
	"os"
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
	// IndexdIndexEndpoint is the endpoint postfix for INDEXD index
	IndexdIndexEndpoint = "/index"

	// DataUserEndpoint is the endpoint postfix for FENCE user
	DataUserEndpoint = "/user/user"

	// DataEndpoint is the canonical endpoint prefix for upload/delete flows
	DataEndpoint = "/data/upload"

	// DataAccessTokenEndpoint is the endpoint postfix for FENCE access token
	DataAccessTokenEndpoint = "/user/credentials/api/access_token"

	// DataDownloadEndpoint is the endpoint postfix for download presigned-url
	DataDownloadEndpoint = "/data/download"

	// DataDownloadPartEndpoint is the endpoint postfix for download part presigned-url
	DataDownloadPartEndpoint = "/data/download/%s/part"

	// DataMultipartInitEndpoint is the endpoint postfix for multipart init
	DataMultipartInitEndpoint = "/data/multipart/init"

	// DataMultipartUploadEndpoint is the endpoint postfix for multipart upload
	DataMultipartUploadEndpoint = "/data/multipart/upload"

	// DataMultipartCompleteEndpoint is the endpoint postfix for multipart complete
	DataMultipartCompleteEndpoint = "/data/multipart/complete"

	// PathSeparator is os dependent path separator char
	PathSeparator = string(os.PathSeparator)

	// DefaultTimeout is used for standard metadata/API requests
	DefaultTimeout = 60 * time.Second
	// DataTimeout is used specifically for large data transfers (uploads/downloads)
	DataTimeout = 5 * time.Minute

	HeaderContentType   = "Content-Type"
	MIMEApplicationJSON = "application/json"

	// FileSizeLimit is the maximum single file size for non-multipart upload (5GB)
	FileSizeLimit = 5 * GB

	// MultipartFileSizeLimit is the maximum single file size for multipart upload (5TB)
	MultipartFileSizeLimit = 5 * TB
	MinMultipartChunkSize  = 10 * MB

	// MaxRetryCount is the maximum retry number per record
	MaxRetryCount = 5
	MaxWaitTime   = 300

	MaxMultipartParts    = 10000
	MaxConcurrentUploads = 10
	MaxRetries           = 5

	OnProgressThreshold = 1 * MB

	// API Endpoints
	HealthzEndpoint = "/healthz"

	IndexdIndexBulkEndpoint               = "/index/bulk"
	IndexdIndexBulkHashesEndpoint         = "/index/bulk/hashes"
	IndexdIndexBulkDeleteEndpoint         = "/index/bulk/delete"
	IndexdIndexBulkSHA256ValidityEndpoint = "/index/bulk/sha256/validity"
	IndexdIndexBulkDocumentsEndpoint      = "/index/bulk/documents"
	IndexdIndexRecordEndpointTemplate     = "/index/%s"

	DataUploadBulkEndpoint             = "/data/upload/bulk"
	DataRecordEndpointTemplate         = "/data/upload/%s"
	DataDownloadRecordEndpointTemplate = "/data/download/%s"

	DataBucketsEndpoint                = "/data/buckets"
	DataBucketsRecordsEndpointTemplate = "/data/buckets/%s"
	DataBucketsScopesEndpointTemplate  = "/data/buckets/%s/scopes"

	MetricsSummaryEndpoint      = "/index/v1/metrics/summary"
	MetricsFilesEndpoint        = "/index/v1/metrics/files"
	MetricsFileEndpointTemplate = "/index/v1/metrics/files/%s"

	GA4GHDRSObjectAccessEndpointTemplate = "/ga4gh/drs/v1/objects/%s/access/%s"

	// Query Parameters
	QueryParamHash         = "hash"
	QueryParamOrganization = "organization"
	QueryParamProject      = "project"
	QueryParamLimit        = "limit"
	QueryParamOffset       = "offset"
	QueryParamPage         = "page"
	QueryParamURL          = "url"
	QueryParamHashType     = "hash_type"
	QueryParamBucket       = "bucket"
	QueryParamFileName     = "filename"
	QueryParamExpiresIn    = "expires_in"
	QueryParamRedirect     = "redirect"
	QueryParamInactiveDays = "inactive_days"
	QueryParamStart        = "start"
	QueryParamEnd          = "end"
)

var (
	// MinChunkSize is the lower bound for multipart chunk sizing.
	MinChunkSize = 10 * MB
)
