package usage

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/calypr/syfon/apigen/errorapi"
	"github.com/calypr/syfon/internal/objects"
)

var (
	_ Reporter = (*Service)(nil)
)

type reportStoreSpy struct {
	files          []FileUsage
	summaries      FileUsageSummary
	transfer       map[string]Summary
	breakdowns     map[string][]Breakdown
	listCalls      int
	summaryCalls   int
	transferCalls  int
	breakdownCalls int
}

func (s *reportStoreSpy) GetFileUsage(_ context.Context, objectID string) (*FileUsage, error) {
	for _, item := range s.files {
		if item.ObjectID == objectID {
			copy := item
			return &copy, nil
		}
	}
	return nil, nil
}

func (s *reportStoreSpy) ListFileUsageByObjectIDs(_ context.Context, ids []string) ([]FileUsage, error) {
	if ids == nil {
		return nil, nil
	}
	items := make([]FileUsage, 0, len(ids))
	for _, id := range ids {
		for _, item := range s.files {
			if item.ObjectID == id {
				items = append(items, item)
			}
		}
	}
	return items, nil
}

func (s *reportStoreSpy) ListFileUsage(_ context.Context, _, _ int, _ *time.Time) ([]FileUsage, error) {
	s.listCalls++
	return append([]FileUsage(nil), s.files...), nil
}

func (s *reportStoreSpy) GetFileUsageSummary(_ context.Context, _ *time.Time) (FileUsageSummary, error) {
	s.summaryCalls++
	return s.summaries, nil
}

func (s *reportStoreSpy) ListFileUsagePageByScope(ctx context.Context, _ string, _ string, limit, offset int, inactiveSince *time.Time) ([]FileUsage, error) {
	return s.ListFileUsage(ctx, limit, offset, inactiveSince)
}

func (s *reportStoreSpy) ListFileUsagePageByResources(ctx context.Context, _ []string, _ bool, limit, offset int, inactiveSince *time.Time) ([]FileUsage, error) {
	return s.ListFileUsage(ctx, limit, offset, inactiveSince)
}

func (s *reportStoreSpy) GetFileUsageSummaryByScope(ctx context.Context, _ string, _ string, inactiveSince *time.Time) (FileUsageSummary, error) {
	return s.GetFileUsageSummary(ctx, inactiveSince)
}

func (s *reportStoreSpy) GetFileUsageSummaryByResources(ctx context.Context, _ []string, _ bool, inactiveSince *time.Time) (FileUsageSummary, error) {
	return s.GetFileUsageSummary(ctx, inactiveSince)
}

func (s *reportStoreSpy) GetProjectRecordSummaryByScope(_ context.Context, _ string, _ string) (FileUsageSummary, error) {
	result := s.summaries
	if result.RecordCount == 0 {
		result.RecordCount = result.TotalFiles
	}
	return result, nil
}

func (s *reportStoreSpy) GetTransferAttributionSummary(_ context.Context, filter Filter) (Summary, error) {
	s.transferCalls++
	return s.transfer[filter.Organization], nil
}

func (s *reportStoreSpy) GetTransferAttributionBreakdown(_ context.Context, filter Filter, _ string) ([]Breakdown, error) {
	s.breakdownCalls++
	return append([]Breakdown(nil), s.breakdowns[filter.Organization]...), nil
}

func (s *reportStoreSpy) GetTransferAttributionSummaryByResources(ctx context.Context, filter Filter, _ []string) (Summary, error) {
	return s.GetTransferAttributionSummary(ctx, filter)
}

func (s *reportStoreSpy) GetTransferAttributionBreakdownByResources(ctx context.Context, filter Filter, groupBy string, _ []string) ([]Breakdown, error) {
	return s.GetTransferAttributionBreakdown(ctx, filter, groupBy)
}

type optimizedReportStore struct {
	*reportStoreSpy
	pageByScopeCalls       int
	pageByResourcesCalls   int
	summaryByScopeCalls    int
	summaryByResourceCalls int
	recordSummaryCalls     int
	transferByResources    int
	breakdownByResources   int
	lastResources          []string
	lastIncludeUnscoped    bool
}

func (s *optimizedReportStore) ListFileUsagePageByScope(_ context.Context, organization, project string, limit, offset int, _ *time.Time) ([]FileUsage, error) {
	s.pageByScopeCalls++
	return []FileUsage{{ObjectID: organization + "/" + project, Size: int64(limit + offset)}}, nil
}

func (s *optimizedReportStore) ListFileUsagePageByResources(_ context.Context, resources []string, includeUnscoped bool, _, _ int, _ *time.Time) ([]FileUsage, error) {
	s.pageByResourcesCalls++
	s.lastResources = append([]string(nil), resources...)
	s.lastIncludeUnscoped = includeUnscoped
	return []FileUsage{{ObjectID: "resource-fast-path"}}, nil
}

func (s *optimizedReportStore) GetFileUsageSummaryByScope(context.Context, string, string, *time.Time) (FileUsageSummary, error) {
	s.summaryByScopeCalls++
	return FileUsageSummary{TotalFiles: 2}, nil
}

func (s *optimizedReportStore) GetFileUsageSummaryByResources(_ context.Context, resources []string, includeUnscoped bool, _ *time.Time) (FileUsageSummary, error) {
	s.summaryByResourceCalls++
	s.lastResources = append([]string(nil), resources...)
	s.lastIncludeUnscoped = includeUnscoped
	return FileUsageSummary{TotalFiles: 3}, nil
}

func (s *optimizedReportStore) GetProjectRecordSummaryByScope(context.Context, string, string) (FileUsageSummary, error) {
	s.recordSummaryCalls++
	return FileUsageSummary{RecordCount: 7}, nil
}

func (s *optimizedReportStore) GetTransferAttributionSummaryByResources(_ context.Context, _ Filter, resources []string) (Summary, error) {
	s.transferByResources++
	s.lastResources = append([]string(nil), resources...)
	return Summary{EventCount: 9}, nil
}

func (s *optimizedReportStore) GetTransferAttributionBreakdownByResources(_ context.Context, _ Filter, _ string, resources []string) ([]Breakdown, error) {
	s.breakdownByResources++
	s.lastResources = append([]string(nil), resources...)
	return []Breakdown{{Key: "resource-fast-path"}}, nil
}

type objectReaderSpy struct {
	ids     map[string][]string
	objects map[string]*objects.Record
}

func (s *objectReaderSpy) GetObject(_ context.Context, ident, _ string) (*objects.Record, error) {
	obj, ok := s.objects[ident]
	if !ok {
		return nil, errors.New("missing object")
	}
	return obj, nil
}

func (s *objectReaderSpy) ListObjectIDsByScope(_ context.Context, organization, project, _ string) ([]string, error) {
	return append([]string(nil), s.ids[organization+"/"+project]...), nil
}

func TestServiceUsesScopedReportCapabilities(t *testing.T) {
	store := &optimizedReportStore{reportStoreSpy: &reportStoreSpy{}}
	service := NewService(Dependencies{Reports: store})
	ctx := context.Background()

	items, err := service.ListFileUsage(ctx, FileUsageQuery{Scope: ScopeQuery{Organization: "org", Project: "project"}, Limit: 5})
	if err != nil || len(items) != 1 || store.pageByScopeCalls != 1 {
		t.Fatalf("single scope optimization: items=%+v err=%v calls=%d", items, err, store.pageByScopeCalls)
	}
	query := ScopeQuery{Scopes: []Scope{{Organization: "org", Project: "project"}}, Resources: []string{"/programs/org/projects/project"}, IncludeUnscoped: true}
	items, err = service.ListFileUsage(ctx, FileUsageQuery{Scope: query, Limit: 2})
	if err != nil || len(items) != 1 || store.pageByResourcesCalls != 1 || !reflect.DeepEqual(store.lastResources, query.Resources) || !store.lastIncludeUnscoped {
		t.Fatalf("aggregate optimization: items=%+v err=%v resources=%v include=%t", items, err, store.lastResources, store.lastIncludeUnscoped)
	}
	summary, err := service.GetFileUsageSummary(ctx, FileUsageSummaryQuery{Scope: ScopeQuery{Organization: "org", Project: "project"}})
	if err != nil || summary.RecordCount != 7 || store.summaryByScopeCalls != 1 || store.recordSummaryCalls != 1 {
		t.Fatalf("single summary optimization: summary=%+v err=%v", summary, err)
	}
	if _, err := service.GetTransferAttributionSummary(ctx, TransferSummaryQuery{Scope: query}); err != nil || store.transferByResources != 1 {
		t.Fatalf("transfer summary optimization: err=%v calls=%d", err, store.transferByResources)
	}
	if _, err := service.GetTransferAttributionBreakdown(ctx, TransferBreakdownQuery{Scope: query, GroupBy: "provider"}); err != nil || store.breakdownByResources != 1 {
		t.Fatalf("transfer breakdown optimization: err=%v calls=%d", err, store.breakdownByResources)
	}
}

func TestServiceListsReadableObjectIDsByScopeInRequestOrder(t *testing.T) {
	objects := &objectReaderSpy{ids: map[string][]string{
		"org-1/p-1": {"a", "b"},
		"org-2/p-2": {"b", "c"},
	}}
	service := NewService(Dependencies{Objects: objects})
	requested := []string{"c", "missing", "a", "b"}
	got, err := service.ListReadableObjectIDs(context.Background(), ScopeQuery{
		Scopes: []Scope{{Organization: "org-1", Project: "p-1"}, {Organization: "org-2", Project: "p-2"}},
	}, requested)
	if err != nil {
		t.Fatalf("ListReadableObjectIDs error: %v", err)
	}
	if want := []string{"c", "a", "b"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("readable IDs = %v, want %v", got, want)
	}

	unscoped, err := service.ListReadableObjectIDs(context.Background(), ScopeQuery{}, requested)
	if err != nil {
		t.Fatalf("unscoped ListReadableObjectIDs error: %v", err)
	}
	if !reflect.DeepEqual(unscoped, requested) {
		t.Fatalf("unscoped readable IDs = %v, want %v", unscoped, requested)
	}
}

func TestScopedFileUsageBatchPreservesOrderMembershipAndInactiveCutoff(t *testing.T) {
	old := time.Now().UTC().Add(-48 * time.Hour)
	recent := time.Now().UTC().Add(-2 * time.Hour)
	objects := &objectReaderSpy{ids: map[string][]string{"org/project": {"a", "b"}}}
	store := &reportStoreSpy{files: []FileUsage{
		{ObjectID: "a", LastDownloadTime: &old},
		{ObjectID: "b", LastDownloadTime: &recent},
	}}
	service := NewService(Dependencies{Reports: store, Objects: objects})
	cutoff := time.Now().UTC().Add(-24 * time.Hour)
	items, err := service.ListFileUsageBatch(context.Background(), FileUsageBatchQuery{
		Scope:         ScopeQuery{Organization: "org", Project: "project"},
		ObjectIDs:     []string{" b ", "missing", "a", "b"},
		InactiveSince: &cutoff,
	})
	if err != nil {
		t.Fatalf("ListFileUsageBatch error: %v", err)
	}
	if !reflect.DeepEqual(items, []FileUsage{{ObjectID: "a", LastDownloadTime: &old}}) {
		t.Fatalf("items = %+v", items)
	}
	if _, err := service.GetScopedFileUsage(context.Background(), "missing", ScopeQuery{Organization: "org", Project: "project"}); !errors.Is(err, errorapi.ErrNotFound) {
		t.Fatalf("missing scoped file error = %v", err)
	}
}

func TestProviderEventNormalizationPreservesValidationAndCounts(t *testing.T) {
	event, err := NormalizeProviderEvent(ProviderEvent{
		ProviderEventID:      " event-1 ",
		Direction:            " DOWNLOAD ",
		Provider:             " s3 ",
		Bucket:               " bucket ",
		ObjectKey:            "///key",
		HTTPMethod:           " get ",
		BytesTransferred:     4,
		ReconciliationStatus: " matched ",
		EventTime:            time.Date(2026, 9, 8, 1, 2, 3, 0, time.FixedZone("PDT", -7*60*60)),
	})
	if err != nil {
		t.Fatalf("NormalizeProviderEvent error: %v", err)
	}
	if event.ProviderEventID != "event-1" || event.Direction != ProviderTransferDirectionDownload || event.ObjectKey != "key" || event.HTTPMethod != "GET" || event.ReconciliationStatus != ProviderTransferMatched || !event.EventTime.Equal(event.EventTime.UTC()) {
		t.Fatalf("normalized event = %+v", event)
	}
	for _, invalid := range []ProviderEvent{
		{ProviderEventID: "id", Direction: "bad", Provider: "s3", Bucket: "b"},
		{Direction: ProviderTransferDirectionDownload, Provider: "s3", Bucket: "b"},
		{ProviderEventID: "id", Direction: ProviderTransferDirectionDownload, Provider: "s3", Bucket: "b", BytesTransferred: -1},
		{ProviderEventID: "id", Direction: ProviderTransferDirectionDownload, Provider: "s3", Bucket: "b", ReconciliationStatus: "bad"},
	} {
		if _, err := NormalizeProviderEvent(invalid); err == nil {
			t.Fatalf("NormalizeProviderEvent(%+v) unexpectedly succeeded", invalid)
		}
	}
}

func TestServiceDelegatesUnscopedQueriesAndAvailabilityErrors(t *testing.T) {
	store := &reportStoreSpy{
		files:      []FileUsage{{ObjectID: "object-1", Size: 17}},
		summaries:  FileUsageSummary{TotalFiles: 4},
		transfer:   map[string]Summary{"": {EventCount: 3}},
		breakdowns: map[string][]Breakdown{"": {{Key: "provider", EventCount: 2}}},
	}
	service := NewService(Dependencies{Reports: store})
	ctx := context.Background()

	got, err := service.GetFileUsage(ctx, "object-1")
	if err != nil || got == nil || got.Size != 17 {
		t.Fatalf("GetFileUsage() = %+v, %v", got, err)
	}
	items, err := service.ListFileUsageByObjectIDs(ctx, []string{"object-1"})
	if err != nil || len(items) != 1 || items[0].ObjectID != "object-1" {
		t.Fatalf("ListFileUsageByObjectIDs() = %+v, %v", items, err)
	}
	items, err = service.ListFileUsage(ctx, FileUsageQuery{Limit: 1})
	if err != nil || len(items) != 1 || store.listCalls != 1 {
		t.Fatalf("ListFileUsage() = %+v, %v (calls=%d)", items, err, store.listCalls)
	}
	summary, err := service.GetFileUsageSummary(ctx, FileUsageSummaryQuery{})
	if err != nil || summary.TotalFiles != 4 || store.summaryCalls != 1 {
		t.Fatalf("GetFileUsageSummary() = %+v, %v (calls=%d)", summary, err, store.summaryCalls)
	}
	transfer, err := service.GetTransferAttributionSummary(ctx, TransferSummaryQuery{})
	if err != nil || transfer.EventCount != 3 || store.transferCalls != 1 {
		t.Fatalf("GetTransferAttributionSummary() = %+v, %v (calls=%d)", transfer, err, store.transferCalls)
	}
	breakdown, err := service.GetTransferAttributionBreakdown(ctx, TransferBreakdownQuery{GroupBy: "scope"})
	if err != nil || len(breakdown) != 1 || breakdown[0].Key != "provider" || store.breakdownCalls != 1 {
		t.Fatalf("GetTransferAttributionBreakdown() = %+v, %v (calls=%d)", breakdown, err, store.breakdownCalls)
	}
	var unavailable *Service
	if _, err := unavailable.GetFileUsage(ctx, "object-1"); !errors.Is(err, ErrReportsUnavailable) {
		t.Fatalf("nil GetFileUsage() error = %v", err)
	}
	if _, err := unavailable.ListFileUsageByObjectIDs(ctx, nil); !errors.Is(err, ErrReportsUnavailable) {
		t.Fatalf("nil ListFileUsageByObjectIDs() error = %v", err)
	}
	if _, err := NewService(Dependencies{}).GetFileUsageSummary(ctx, FileUsageSummaryQuery{}); !errors.Is(err, ErrReportsUnavailable) {
		t.Fatalf("missing reports summary error = %v", err)
	}
}
