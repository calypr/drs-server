package storage

import "time"

type AccessTarget struct {
	AccessID string
	Location string
}

type ByteRange struct {
	Start int64
	End   int64
}

type AccessOptions struct {
	ExpiresIn        time.Duration
	Method           string
	DownloadFilename string
}

type AccessRequest struct {
	Target  AccessTarget
	Options AccessOptions
	Range   *ByteRange
}

type Access struct {
	Location string
}

type ObjectTarget struct {
	Bucket string
	Key    string
}

type PrefixTarget struct {
	Bucket string
	Prefix string
}

type UploadID string

type CompletedPart struct {
	PartNumber int32
	ETag       string
}

type MultipartPartRequest struct {
	Target     ObjectTarget
	UploadID   UploadID
	PartNumber int32
}

type CompleteMultipartRequest struct {
	Target   ObjectTarget
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
	Target ObjectTarget
}

type ProbeResult struct {
	ID       string
	Target   ObjectTarget
	Metadata ObjectMetadata
	Err      error
}

type InventoryRequest struct {
	Target      PrefixTarget
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
	Provider string
	Bucket   string
	Key      string
	Path     string
}
