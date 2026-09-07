package usage

import "time"

type FileUsage struct {
	ObjectID         string
	Name             string
	Size             int64
	UploadCount      int64
	DownloadCount    int64
	LastUploadTime   *time.Time
	LastDownloadTime *time.Time
	LastAccessTime   *time.Time
}

type FileUsageSummary struct {
	TotalFiles              int64
	TotalUploads            int64
	TotalDownloads          int64
	InactiveFileCount       int64
	RecordCount             int64
	RecordLatestUpdatedTime *time.Time
}

type Filter struct {
	Organization         string
	Project              string
	EventType            string
	Direction            string
	From                 *time.Time
	To                   *time.Time
	Provider             string
	Bucket               string
	SHA256               string
	User                 string
	ReconciliationStatus string
}

type Summary struct {
	EventCount         int64      `json:"event_count"`
	AccessIssuedCount  int64      `json:"access_issued_count"`
	DownloadEventCount int64      `json:"download_event_count"`
	UploadEventCount   int64      `json:"upload_event_count"`
	BytesRequested     int64      `json:"bytes_requested"`
	BytesDownloaded    int64      `json:"bytes_downloaded"`
	BytesUploaded      int64      `json:"bytes_uploaded"`
	Freshness          *Freshness `json:"freshness,omitempty"`
}

type Freshness struct {
	IsStale             bool       `json:"is_stale"`
	MissingBuckets      []string   `json:"missing_buckets,omitempty"`
	LatestCompletedSync *time.Time `json:"latest_completed_sync,omitempty"`
	RequiredFrom        *time.Time `json:"required_from,omitempty"`
	RequiredTo          *time.Time `json:"required_to,omitempty"`
}

type Breakdown struct {
	Key              string     `json:"key"`
	Organization     string     `json:"organization"`
	Project          string     `json:"project"`
	Provider         string     `json:"provider"`
	Bucket           string     `json:"bucket"`
	SHA256           string     `json:"sha256"`
	ActorEmail       string     `json:"actor_email"`
	ActorSubject     string     `json:"actor_subject"`
	EventCount       int64      `json:"event_count"`
	BytesRequested   int64      `json:"bytes_requested"`
	BytesDownloaded  int64      `json:"bytes_downloaded"`
	BytesUploaded    int64      `json:"bytes_uploaded"`
	LastTransferTime *time.Time `json:"last_transfer_time"`
}
