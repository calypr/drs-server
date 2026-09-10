package usage

import (
	"context"
	"time"

	"github.com/calypr/syfon/apigen/drs"
	"github.com/calypr/syfon/apigen/metricsapi"
)

// FileCounterRecorder records object upload and download counters.
type FileCounterRecorder interface {
	RecordFileUpload(ctx context.Context, objectID string) error
	RecordFileDownload(ctx context.Context, objectID string) error
}

type TransferEventWriter interface {
	RecordTransferAttributionEvents(ctx context.Context, events []Event) error
}

type Ingestor interface {
	FileCounterRecorder
	TransferEventWriter
	ProviderEventRecorder
}

// FileUsageReader reads per-object usage and unscoped reports.
type FileUsageReader interface {
	GetFileUsage(ctx context.Context, objectID string) (*metricsapi.FileUsage, error)
	ListFileUsageByObjectIDs(ctx context.Context, ids []string) ([]metricsapi.FileUsage, error)
	ListFileUsage(ctx context.Context, limit, offset int, inactiveSince *time.Time) ([]metricsapi.FileUsage, error)
	GetFileUsageSummary(ctx context.Context, inactiveSince *time.Time) (metricsapi.FileUsageSummary, error)
}

// ObjectReader reads objects for metrics authorization.
// requiredMethod is supplied by callers so the object service can enforce the
// same access method as the existing metrics paths.
type ObjectReader interface {
	GetObject(ctx context.Context, ident, requiredMethod string) (*drs.DrsObject, error)
	ListObjectIDsByScope(ctx context.Context, organization, project, requiredMethod string) ([]string, error)
}

// ProviderEventRecorder records provider-reported transfer events.
type ProviderEventRecorder interface {
	RecordProviderTransferEvents(ctx context.Context, events []metricsapi.ProviderTransferEvent) error
}

// TransferQuery reads transfer attribution reports.
type TransferQuery interface {
	QueryTransferSummary(ctx context.Context, filter Filter, resources []string) (metricsapi.TransferAttributionSummary, error)
	QueryTransferBreakdown(ctx context.Context, filter Filter, groupBy string, resources []string) ([]metricsapi.TransferAttributionBreakdown, error)
}

type ReportStore interface {
	FileUsageReader
	TransferQuery
	ListFileUsagePageByScope(ctx context.Context, organization, project string, limit, offset int, inactiveSince *time.Time) ([]metricsapi.FileUsage, error)
	ListFileUsagePageByResources(ctx context.Context, resources []string, includeUnscoped bool, limit, offset int, inactiveSince *time.Time) ([]metricsapi.FileUsage, error)
	GetFileUsageSummaryByScope(ctx context.Context, organization, project string, inactiveSince *time.Time) (metricsapi.FileUsageSummary, error)
	GetFileUsageSummaryByResources(ctx context.Context, resources []string, includeUnscoped bool, inactiveSince *time.Time) (metricsapi.FileUsageSummary, error)
	GetProjectRecordSummaryByScope(ctx context.Context, organization, project string) (metricsapi.FileUsageSummary, error)
}
