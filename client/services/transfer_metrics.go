package services

import "time"

// TransferAttributionSummary is the client-facing transfer metrics summary.
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

// TransferMetricsFreshness describes the persisted metrics coverage.
type TransferMetricsFreshness struct {
	IsStale             bool       `json:"is_stale"`
	MissingBuckets      []string   `json:"missing_buckets,omitempty"`
	LatestCompletedSync *time.Time `json:"latest_completed_sync,omitempty"`
	RequiredFrom        *time.Time `json:"required_from,omitempty"`
	RequiredTo          *time.Time `json:"required_to,omitempty"`
}

// TransferAttributionBreakdown is one grouped transfer metrics row.
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

// TransferBreakdownResponse is the client-facing grouped transfer response.
type TransferBreakdownResponse struct {
	GroupBy   string                         `json:"group_by"`
	Data      []TransferAttributionBreakdown `json:"data"`
	Freshness *TransferMetricsFreshness      `json:"freshness,omitempty"`
}
