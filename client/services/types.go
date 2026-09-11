package services

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
