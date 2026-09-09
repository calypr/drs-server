package metrics

import (
	"context"
	"fmt"

	"github.com/calypr/syfon/apigen/errorapi"
	"github.com/calypr/syfon/internal/usage"
)

type metricsIngestFake struct {
	events []usage.ProviderEvent
	err    error
}

func (f *metricsIngestFake) RecordProviderTransferEvents(_ context.Context, events []usage.ProviderEvent) error {
	if f.err != nil {
		return f.err
	}
	f.events = append(f.events, events...)
	return nil
}

type metricsReporterFake struct {
	files               []usage.FileUsage
	fileUsage           map[string]usage.FileUsage
	scopedFileUsage     map[string]usage.FileUsage
	batch               []usage.FileUsage
	summary             usage.FileUsageSummary
	transferSummary     usage.Summary
	transferBreakdown   []usage.Breakdown
	freshness           usage.Freshness
	getFileUsageErr     error
	listFileUsageErr    error
	batchErr            error
	summaryErr          error
	scopedFileUsageErr  error
	freshnessErr        error
	transferSummaryFn   func(usage.TransferSummaryQuery) (usage.Summary, error)
	transferBreakdownFn func(usage.TransferBreakdownQuery) ([]usage.Breakdown, error)
	lastFileQuery       usage.FileUsageQuery
	lastBatchQuery      usage.FileUsageBatchQuery
	lastSummaryQuery    usage.FileUsageSummaryQuery
	lastTransferQuery   usage.TransferSummaryQuery
	lastBreakdownQuery  usage.TransferBreakdownQuery
}

func (f *metricsReporterFake) GetFileUsage(_ context.Context, objectID string) (*usage.FileUsage, error) {
	if f.getFileUsageErr != nil {
		return nil, f.getFileUsageErr
	}
	item, ok := f.fileUsage[objectID]
	if !ok {
		return nil, fmt.Errorf("%w: file usage not found", errorapi.ErrNotFound)
	}
	return &item, nil
}

func (f *metricsReporterFake) ListFileUsageByObjectIDs(_ context.Context, ids []string) ([]usage.FileUsage, error) {
	items := make([]usage.FileUsage, 0, len(ids))
	for _, id := range ids {
		if item, ok := f.fileUsage[id]; ok {
			items = append(items, item)
		}
	}
	return items, nil
}

func (f *metricsReporterFake) ListReadableObjectIDs(context.Context, usage.ScopeQuery, []string) ([]string, error) {
	return nil, nil
}

func (f *metricsReporterFake) ListFileUsageBatch(_ context.Context, query usage.FileUsageBatchQuery) ([]usage.FileUsage, error) {
	f.lastBatchQuery = query
	if f.batchErr != nil {
		return nil, f.batchErr
	}
	return append([]usage.FileUsage(nil), f.batch...), nil
}

func (f *metricsReporterFake) GetScopedFileUsage(_ context.Context, objectID string, _ usage.ScopeQuery) (*usage.FileUsage, error) {
	if f.scopedFileUsageErr != nil {
		return nil, f.scopedFileUsageErr
	}
	item, ok := f.scopedFileUsage[objectID]
	if !ok {
		return nil, fmt.Errorf("%w: scoped file usage not found", errorapi.ErrNotFound)
	}
	return &item, nil
}

func (f *metricsReporterFake) ListFileUsage(_ context.Context, query usage.FileUsageQuery) ([]usage.FileUsage, error) {
	f.lastFileQuery = query
	if f.listFileUsageErr != nil {
		return nil, f.listFileUsageErr
	}
	return append([]usage.FileUsage(nil), f.files...), nil
}

func (f *metricsReporterFake) GetFileUsageSummary(_ context.Context, query usage.FileUsageSummaryQuery) (usage.FileUsageSummary, error) {
	f.lastSummaryQuery = query
	if f.summaryErr != nil {
		return usage.FileUsageSummary{}, f.summaryErr
	}
	return f.summary, nil
}

func (f *metricsReporterFake) GetTransferAttributionSummary(_ context.Context, query usage.TransferSummaryQuery) (usage.Summary, error) {
	f.lastTransferQuery = query
	if f.transferSummaryFn != nil {
		return f.transferSummaryFn(query)
	}
	return f.transferSummary, nil
}

func (f *metricsReporterFake) GetTransferAttributionBreakdown(_ context.Context, query usage.TransferBreakdownQuery) ([]usage.Breakdown, error) {
	f.lastBreakdownQuery = query
	if f.transferBreakdownFn != nil {
		return f.transferBreakdownFn(query)
	}
	return append([]usage.Breakdown(nil), f.transferBreakdown...), nil
}

func (f *metricsReporterFake) GetTransferFreshness(_ context.Context, _ usage.Filter) (usage.Freshness, error) {
	if f.freshnessErr != nil {
		return usage.Freshness{}, f.freshnessErr
	}
	return f.freshness, nil
}

var _ usage.Reporter = (*metricsReporterFake)(nil)
var _ usage.ProviderEventRecorder = (*metricsIngestFake)(nil)
