package usage

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

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

func (s *reportStoreSpy) GetTransferAttributionSummary(_ context.Context, filter Filter) (Summary, error) {
	s.transferCalls++
	return s.transfer[filter.Organization], nil
}

func (s *reportStoreSpy) GetTransferAttributionBreakdown(_ context.Context, filter Filter, _ string) ([]Breakdown, error) {
	s.breakdownCalls++
	return append([]Breakdown(nil), s.breakdowns[filter.Organization]...), nil
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

func TestServiceUsesScopedReportOptimizations(t *testing.T) {
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

func TestServiceFallbackAggregatesScopedFileUsage(t *testing.T) {
	name := "B"
	store := &reportStoreSpy{files: []FileUsage{{ObjectID: "a", UploadCount: 2, DownloadCount: 3}}}
	objects := &objectReaderSpy{
		ids: map[string][]string{
			"org-1/p-1": {"b", "a"},
			"org-2/p-2": {"a", "c"},
		},
		objects: map[string]*objects.Record{
			"b": {Id: "b", Name: &name, Size: 12},
			"c": {Id: "c", Size: 13},
		},
	}
	service := NewService(Dependencies{Reports: store, Objects: objects})
	query := FileUsageQuery{Scope: ScopeQuery{Scopes: []Scope{{Organization: "org-1", Project: "p-1"}, {Organization: "org-2", Project: "p-2"}}}, Limit: 2}
	items, err := service.ListFileUsage(context.Background(), query)
	if err != nil {
		t.Fatal(err)
	}
	if got := []string{items[0].ObjectID, items[1].ObjectID}; !reflect.DeepEqual(got, []string{"a", "b"}) {
		t.Fatalf("unexpected sorted/deduplicated page: %v", got)
	}
	summary, err := service.GetFileUsageSummary(context.Background(), FileUsageSummaryQuery{Scope: query.Scope})
	if err != nil {
		t.Fatal(err)
	}
	if summary.TotalFiles != 4 || summary.TotalUploads != 4 || summary.TotalDownloads != 6 {
		t.Fatalf("unexpected aggregate summary: %+v", summary)
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

func TestServiceFallbackMergesTransferBreakdownAndFreshness(t *testing.T) {
	first := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	second := first.Add(time.Hour)
	store := &reportStoreSpy{
		transfer: map[string]Summary{
			"org-1": {EventCount: 2, BytesDownloaded: 5},
			"org-2": {EventCount: 3, BytesUploaded: 7},
		},
		breakdowns: map[string][]Breakdown{
			"org-1": {{Key: "same", EventCount: 1, BytesDownloaded: 5, LastTransferTime: &first}},
			"org-2": {{Key: "same", EventCount: 2, BytesUploaded: 7, LastTransferTime: &second}},
		},
	}
	service := NewService(Dependencies{Reports: store})
	query := ScopeQuery{Scopes: []Scope{{Organization: "org-1"}, {Organization: "org-2"}}}
	summary, err := service.GetTransferAttributionSummary(context.Background(), TransferSummaryQuery{Scope: query})
	if err != nil || summary.EventCount != 5 || summary.BytesDownloaded != 5 || summary.BytesUploaded != 7 {
		t.Fatalf("unexpected merged summary: %+v err=%v", summary, err)
	}
	items, err := service.GetTransferAttributionBreakdown(context.Background(), TransferBreakdownQuery{Scope: query, GroupBy: "user"})
	if err != nil || len(items) != 1 || items[0].EventCount != 3 || items[0].BytesDownloaded != 5 || items[0].BytesUploaded != 7 || !items[0].LastTransferTime.Equal(second) {
		t.Fatalf("unexpected merged breakdown: %+v err=%v", items, err)
	}
	if _, err := service.GetTransferAttributionBreakdown(context.Background(), TransferBreakdownQuery{GroupBy: "invalid"}); !errors.Is(err, ErrInvalidGroupBy) {
		t.Fatalf("invalid group_by error = %v", err)
	}
	from := first.Add(-time.Hour)
	to := second.Add(time.Hour)
	freshness, err := service.GetTransferFreshness(context.Background(), Filter{From: &from, To: &to})
	if err != nil || freshness.IsStale || len(freshness.MissingBuckets) != 0 || !freshness.RequiredFrom.Equal(from) || !freshness.RequiredTo.Equal(to) {
		t.Fatalf("unexpected placeholder freshness: %+v err=%v", freshness, err)
	}
}
