package usage

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/calypr/syfon/apigen/errorapi"
	"github.com/calypr/syfon/apigen/metricsapi"
)

var (
	_ Reporter = (*Service)(nil)
)

func ptr[T any](value T) *T { return &value }

type reportStoreSpy struct {
	files          []metricsapi.FileUsage
	summaries      metricsapi.FileUsageSummary
	transfer       map[string]metricsapi.TransferAttributionSummary
	breakdowns     map[string][]metricsapi.TransferAttributionBreakdown
	listCalls      int
	summaryCalls   int
	transferCalls  int
	breakdownCalls int
}

func (s *reportStoreSpy) GetFileUsage(_ context.Context, objectID string) (*metricsapi.FileUsage, error) {
	for _, item := range s.files {
		if item.ObjectId != nil && *item.ObjectId == objectID {
			copy := item
			return &copy, nil
		}
	}
	return nil, nil
}

func (s *reportStoreSpy) ListFileUsageByObjectIDs(_ context.Context, ids []string) ([]metricsapi.FileUsage, error) {
	if ids == nil {
		return nil, nil
	}
	items := make([]metricsapi.FileUsage, 0, len(ids))
	for _, id := range ids {
		for _, item := range s.files {
			if item.ObjectId != nil && *item.ObjectId == id {
				items = append(items, item)
			}
		}
	}
	return items, nil
}

func (s *reportStoreSpy) ListFileUsage(_ context.Context, _, _ int, _ *time.Time) ([]metricsapi.FileUsage, error) {
	s.listCalls++
	return append([]metricsapi.FileUsage(nil), s.files...), nil
}

func (s *reportStoreSpy) GetFileUsageSummary(_ context.Context, _ *time.Time) (metricsapi.FileUsageSummary, error) {
	s.summaryCalls++
	return s.summaries, nil
}

func (s *reportStoreSpy) ListFileUsagePageByScope(ctx context.Context, _ string, _ string, limit, offset int, inactiveSince *time.Time) ([]metricsapi.FileUsage, error) {
	return s.ListFileUsage(ctx, limit, offset, inactiveSince)
}

func (s *reportStoreSpy) ListFileUsagePageByResources(ctx context.Context, _ []string, _ bool, limit, offset int, inactiveSince *time.Time) ([]metricsapi.FileUsage, error) {
	return s.ListFileUsage(ctx, limit, offset, inactiveSince)
}

func (s *reportStoreSpy) GetFileUsageSummaryByScope(ctx context.Context, _ string, _ string, inactiveSince *time.Time) (metricsapi.FileUsageSummary, error) {
	return s.GetFileUsageSummary(ctx, inactiveSince)
}

func (s *reportStoreSpy) GetFileUsageSummaryByResources(ctx context.Context, _ []string, _ bool, inactiveSince *time.Time) (metricsapi.FileUsageSummary, error) {
	return s.GetFileUsageSummary(ctx, inactiveSince)
}

func (s *reportStoreSpy) GetProjectRecordSummaryByScope(_ context.Context, _ string, _ string) (metricsapi.FileUsageSummary, error) {
	result := s.summaries
	if result.RecordCount == nil {
		result.RecordCount = result.TotalFiles
	}
	return result, nil
}

func (s *reportStoreSpy) QueryTransferSummary(_ context.Context, filter Filter, _ []string) (metricsapi.TransferAttributionSummary, error) {
	s.transferCalls++
	return s.transfer[filter.Organization], nil
}

func (s *reportStoreSpy) QueryTransferBreakdown(_ context.Context, filter Filter, _ string, _ []string) ([]metricsapi.TransferAttributionBreakdown, error) {
	s.breakdownCalls++
	return append([]metricsapi.TransferAttributionBreakdown(nil), s.breakdowns[filter.Organization]...), nil
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

func (s *optimizedReportStore) ListFileUsagePageByScope(_ context.Context, organization, project string, limit, offset int, _ *time.Time) ([]metricsapi.FileUsage, error) {
	s.pageByScopeCalls++
	return []metricsapi.FileUsage{{ObjectId: ptr(organization + "/" + project), Size: ptr(int64(limit + offset))}}, nil
}

func (s *optimizedReportStore) ListFileUsagePageByResources(_ context.Context, resources []string, includeUnscoped bool, _, _ int, _ *time.Time) ([]metricsapi.FileUsage, error) {
	s.pageByResourcesCalls++
	s.lastResources = append([]string(nil), resources...)
	s.lastIncludeUnscoped = includeUnscoped
	return []metricsapi.FileUsage{{ObjectId: ptr("resource-fast-path")}}, nil
}

func (s *optimizedReportStore) GetFileUsageSummaryByScope(context.Context, string, string, *time.Time) (metricsapi.FileUsageSummary, error) {
	s.summaryByScopeCalls++
	return metricsapi.FileUsageSummary{TotalFiles: ptr(int64(2))}, nil
}

func (s *optimizedReportStore) GetFileUsageSummaryByResources(_ context.Context, resources []string, includeUnscoped bool, _ *time.Time) (metricsapi.FileUsageSummary, error) {
	s.summaryByResourceCalls++
	s.lastResources = append([]string(nil), resources...)
	s.lastIncludeUnscoped = includeUnscoped
	return metricsapi.FileUsageSummary{TotalFiles: ptr(int64(3))}, nil
}

func (s *optimizedReportStore) GetProjectRecordSummaryByScope(context.Context, string, string) (metricsapi.FileUsageSummary, error) {
	s.recordSummaryCalls++
	return metricsapi.FileUsageSummary{RecordCount: ptr(int64(7))}, nil
}

func (s *optimizedReportStore) QueryTransferSummary(_ context.Context, _ Filter, resources []string) (metricsapi.TransferAttributionSummary, error) {
	s.transferByResources++
	s.lastResources = append([]string(nil), resources...)
	return metricsapi.TransferAttributionSummary{EventCount: ptr(int64(9))}, nil
}

func (s *optimizedReportStore) QueryTransferBreakdown(_ context.Context, _ Filter, _ string, resources []string) ([]metricsapi.TransferAttributionBreakdown, error) {
	s.breakdownByResources++
	s.lastResources = append([]string(nil), resources...)
	return []metricsapi.TransferAttributionBreakdown{{Key: ptr("resource-fast-path")}}, nil
}

type objectReaderSpy struct {
	ids map[string][]string
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
	if err != nil || summary.RecordCount == nil || *summary.RecordCount != 7 || store.summaryByScopeCalls != 1 || store.recordSummaryCalls != 1 {
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
	got, err := service.listReadableObjectIDs(context.Background(), ScopeQuery{
		Scopes: []Scope{{Organization: "org-1", Project: "p-1"}, {Organization: "org-2", Project: "p-2"}},
	}, requested)
	if err != nil {
		t.Fatalf("ListReadableObjectIDs error: %v", err)
	}
	if want := []string{"c", "a", "b"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("readable IDs = %v, want %v", got, want)
	}

	unscoped, err := service.listReadableObjectIDs(context.Background(), ScopeQuery{}, requested)
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
	store := &reportStoreSpy{files: []metricsapi.FileUsage{
		{ObjectId: ptr("a"), LastDownloadTime: &old},
		{ObjectId: ptr("b"), LastDownloadTime: &recent},
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
	if !reflect.DeepEqual(items, []metricsapi.FileUsage{{ObjectId: ptr("a"), LastDownloadTime: &old}}) {
		t.Fatalf("items = %+v", items)
	}
	if _, err := service.GetScopedFileUsage(context.Background(), "missing", ScopeQuery{Organization: "org", Project: "project"}); !errors.Is(err, errorapi.ErrNotFound) {
		t.Fatalf("missing scoped file error = %v", err)
	}
}

func TestProviderEventNormalizationPreservesValidationAndCounts(t *testing.T) {
	direction := metricsapi.ProviderTransferDirection(" DOWNLOAD ")
	objectKey := "///key"
	httpMethod := " get "
	reconciliation := metricsapi.ProviderTransferReconciliationStatus(" matched ")
	eventTime := time.Date(2026, 9, 8, 1, 2, 3, 0, time.FixedZone("PDT", -7*60*60))
	event, err := NormalizeProviderEvent(metricsapi.ProviderTransferEvent{
		ProviderEventId:      " event-1 ",
		Direction:            direction,
		Provider:             " s3 ",
		Bucket:               " bucket ",
		ObjectKey:            &objectKey,
		HttpMethod:           &httpMethod,
		BytesTransferred:     4,
		ReconciliationStatus: &reconciliation,
		EventTime:            &eventTime,
	})
	if err != nil {
		t.Fatalf("NormalizeProviderEvent error: %v", err)
	}
	if event.ProviderEventId != "event-1" || event.Direction != metricsapi.ProviderTransferDirection(ProviderTransferDirectionDownload) || event.ObjectKey == nil || *event.ObjectKey != "key" || event.HttpMethod == nil || *event.HttpMethod != "GET" || event.ReconciliationStatus == nil || string(*event.ReconciliationStatus) != ProviderTransferMatched || event.EventTime == nil || !event.EventTime.Equal(event.EventTime.UTC()) {
		t.Fatalf("normalized event = %+v", event)
	}
	for _, invalid := range []metricsapi.ProviderTransferEvent{
		{ProviderEventId: "id", Direction: "bad", Provider: "s3", Bucket: "b"},
		{Direction: metricsapi.Download, Provider: "s3", Bucket: "b"},
		{ProviderEventId: "id", Direction: metricsapi.Download, Provider: "s3", Bucket: "b", BytesTransferred: -1},
		{ProviderEventId: "id", Direction: metricsapi.Download, Provider: "s3", Bucket: "b", ReconciliationStatus: ptr(metricsapi.ProviderTransferReconciliationStatus("bad"))},
	} {
		if _, err := NormalizeProviderEvent(invalid); err == nil {
			t.Fatalf("NormalizeProviderEvent(%+v) unexpectedly succeeded", invalid)
		}
	}
}

func TestServiceDelegatesUnscopedQueriesAndAvailabilityErrors(t *testing.T) {
	store := &reportStoreSpy{
		files:      []metricsapi.FileUsage{{ObjectId: ptr("object-1"), Size: ptr(int64(17))}},
		summaries:  metricsapi.FileUsageSummary{TotalFiles: ptr(int64(4))},
		transfer:   map[string]metricsapi.TransferAttributionSummary{"": {EventCount: ptr(int64(3))}},
		breakdowns: map[string][]metricsapi.TransferAttributionBreakdown{"": {{Key: ptr("provider"), EventCount: ptr(int64(2))}}},
	}
	service := NewService(Dependencies{Reports: store})
	ctx := context.Background()

	got, err := service.GetFileUsage(ctx, "object-1")
	if err != nil || got == nil || got.Size == nil || *got.Size != 17 {
		t.Fatalf("GetFileUsage() = %+v, %v", got, err)
	}
	items, err := service.ListFileUsage(ctx, FileUsageQuery{Limit: 1})
	if err != nil || len(items) != 1 || store.listCalls != 1 {
		t.Fatalf("ListFileUsage() = %+v, %v (calls=%d)", items, err, store.listCalls)
	}
	summary, err := service.GetFileUsageSummary(ctx, FileUsageSummaryQuery{})
	if err != nil || summary.TotalFiles == nil || *summary.TotalFiles != 4 || store.summaryCalls != 1 {
		t.Fatalf("GetFileUsageSummary() = %+v, %v (calls=%d)", summary, err, store.summaryCalls)
	}
	transfer, err := service.GetTransferAttributionSummary(ctx, TransferSummaryQuery{})
	if err != nil || transfer.EventCount == nil || *transfer.EventCount != 3 || store.transferCalls != 1 {
		t.Fatalf("GetTransferAttributionSummary() = %+v, %v (calls=%d)", transfer, err, store.transferCalls)
	}
	breakdown, err := service.GetTransferAttributionBreakdown(ctx, TransferBreakdownQuery{GroupBy: "scope"})
	if err != nil || len(breakdown) != 1 || breakdown[0].Key == nil || *breakdown[0].Key != "provider" || store.breakdownCalls != 1 {
		t.Fatalf("GetTransferAttributionBreakdown() = %+v, %v (calls=%d)", breakdown, err, store.breakdownCalls)
	}
	var unavailable *Service
	if _, err := unavailable.GetFileUsage(ctx, "object-1"); !errors.Is(err, ErrReportsUnavailable) {
		t.Fatalf("nil GetFileUsage() error = %v", err)
	}
	if _, err := NewService(Dependencies{}).GetFileUsageSummary(ctx, FileUsageSummaryQuery{}); !errors.Is(err, ErrReportsUnavailable) {
		t.Fatalf("missing reports summary error = %v", err)
	}
}
