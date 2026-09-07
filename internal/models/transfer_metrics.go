package models

import "time"

// FileUsage captures per-object transfer activity that can drive lifecycle policies.
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

// FileUsageSummary aggregates transfer activity for a set of objects.
type FileUsageSummary struct {
	TotalFiles              int64
	TotalUploads            int64
	TotalDownloads          int64
	InactiveFileCount       int64
	RecordCount             int64
	RecordLatestUpdatedTime *time.Time
}

const (
	TransferEventAccessIssued = "access_issued"

	ProviderTransferDirectionDownload = "download"
	ProviderTransferDirectionUpload   = "upload"

	ProviderTransferMatched   = "matched"
	ProviderTransferAmbiguous = "ambiguous"
	ProviderTransferUnmatched = "unmatched"
)

// AccessGrant captures the canonical billing identity for a signed/direct
// storage access authorization. Repeated access issuance events for the same
// object/scope/storage URL should point at one grant.
type AccessGrant struct {
	AccessGrantID string
	FirstIssuedAt time.Time
	LastIssuedAt  time.Time
	IssueCount    int64
	ObjectID      string
	SHA256        string
	ObjectSize    int64
	Organization  string
	Project       string
	AccessID      string
	Provider      string
	Bucket        string
	StorageURL    string
	ActorEmail    string
	ActorSubject  string
	AuthMode      string
}

// TransferAttributionEvent is the append-only signed-access audit log. Billing
// metrics assume each signed access is used and are based on these events.
type TransferAttributionEvent struct {
	EventID           string
	AccessGrantID     string
	EventType         string
	Direction         string
	EventTime         time.Time
	RequestID         string
	ObjectID          string
	SHA256            string
	ObjectSize        int64
	Organization      string
	Project           string
	AccessID          string
	Provider          string
	Bucket            string
	StorageURL        string
	RangeStart        *int64
	RangeEnd          *int64
	BytesRequested    int64
	BytesCompleted    int64
	ActorEmail        string
	ActorSubject      string
	AuthMode          string
	ClientName        string
	ClientVersion     string
	TransferSessionID string
}

// ProviderTransferEvent captures provider-observed transfer bytes for billing.
type ProviderTransferEvent struct {
	ProviderEventID      string
	AccessGrantID        string
	Direction            string
	EventTime            time.Time
	RequestID            string
	ProviderRequestID    string
	ObjectID             string
	SHA256               string
	ObjectSize           int64
	Organization         string
	Project              string
	AccessID             string
	Provider             string
	Bucket               string
	ObjectKey            string
	StorageURL           string
	RangeStart           *int64
	RangeEnd             *int64
	BytesTransferred     int64
	HTTPMethod           string
	HTTPStatus           int
	RequesterPrincipal   string
	SourceIP             string
	UserAgent            string
	RawEventRef          string
	ActorEmail           string
	ActorSubject         string
	AuthMode             string
	ReconciliationStatus string
}

type TransferAttributionFilter struct {
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

type TransferAttributionSummary struct {
	EventCount         int64                     `json:"event_count"`
	AccessIssuedCount  int64                     `json:"access_issued_count"`
	DownloadEventCount int64                     `json:"download_event_count"`
	UploadEventCount   int64                     `json:"upload_event_count"`
	BytesRequested     int64                     `json:"bytes_requested"`
	BytesDownloaded    int64                     `json:"bytes_downloaded"`
	BytesUploaded      int64                     `json:"bytes_uploaded"`
	Freshness          *TransferMetricsFreshness `json:"freshness,omitempty"`
}

type TransferMetricsFreshness struct {
	IsStale             bool       `json:"is_stale"`
	MissingBuckets      []string   `json:"missing_buckets,omitempty"`
	LatestCompletedSync *time.Time `json:"latest_completed_sync,omitempty"`
	RequiredFrom        *time.Time `json:"required_from,omitempty"`
	RequiredTo          *time.Time `json:"required_to,omitempty"`
}

type TransferAttributionBreakdown struct {
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
