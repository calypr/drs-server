package services

import (
	drsapi "github.com/calypr/syfon/apigen/drs"
)

// Options and request types for client services.

type DRSPage struct {
	DrsObjects []drsapi.DrsObject `json:"drs_objects"`
}

type DeleteByQueryOptions struct {
	Organization string
	ProjectID    string
	Hash         string
	HashType     string
}

type ListRecordsOptions struct {
	Hash         string
	URL          string
	Organization string
	ProjectID    string
	Limit        int
	Start        string
	Page         int
}

type UploadURLRequest struct {
	FileID       string
	Key          string
	ExpiresIn    int
	Organization string
	Project      string
}

type MetricsFilesOptions struct {
	Limit        int
	Offset       int
	InactiveDays int
	Organization string
	ProjectID    string
}

type MetricsSummaryOptions struct {
	InactiveDays int
	Organization string
	ProjectID    string
}

type StorageSummaryOptions struct {
	Organization string
	ProjectID    string
	Path         string
}

type StorageChildrenOptions struct {
	Organization string
	ProjectID    string
	Path         string
	Limit        int
	Offset       int
	SortBy       string
	SortOrder    string
}

type TransferMetricsOptions struct {
	Organization         string
	ProjectID            string
	Direction            string
	From                 string
	To                   string
	Provider             string
	Bucket               string
	SHA256               string
	User                 string
	GroupBy              string
	ReconciliationStatus string
	AllowStale           bool
}
