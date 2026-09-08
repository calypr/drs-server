package storage

import (
	"time"

	"github.com/calypr/syfon/internal/buckets"
)

type Target struct {
	Provider         string
	LookupKey        string
	PhysicalBucket   string
	Key              string
	Path             string
	OriginalURL      string
	CanonicalURL     string
	LookupCandidates []string
}

type ProviderBinding struct {
	Provider       string
	LookupKey      string
	PhysicalBucket string
	Credential     *buckets.Credential
}

type SignRequest struct {
	Target           Target
	Method           string
	ExpiresIn        time.Duration
	DownloadFilename string
	Range            *ByteRange
}

type SignedAccess struct {
	Location string
}

type ByteRange struct {
	Start int64
	End   int64
}

type UploadID string

type CompletedPart struct {
	PartNumber int32
	ETag       string
}

type MultipartPartRequest struct {
	Target     Target
	UploadID   UploadID
	PartNumber int32
}

type CompleteMultipartRequest struct {
	Target   Target
	UploadID UploadID
	Parts    []CompletedPart
}

type ObjectMetadata struct {
	Provider     string
	Bucket       string
	Key          string
	Path         string
	SizeBytes    int64
	MetaSHA256   string
	ETag         string
	LastModified time.Time
}

type ProbeTarget struct {
	ID     string
	Target Target
}

type ProbeResult struct {
	ID       string
	Target   Target
	Metadata ObjectMetadata
	Err      error
}

type InventoryRequest struct {
	Target      Target
	Prefix      string
	IncludeHead bool
	ExactPrefix bool
	MaxKeys     int32
}

type InventoryResult struct {
	Items    []ObjectMetadata
	Complete bool
}

type DeleteTarget struct {
	Location string
}

type PhysicalTarget struct {
	Provider       string
	LookupKey      string
	PhysicalBucket string
	Key            string
	Path           string
}
