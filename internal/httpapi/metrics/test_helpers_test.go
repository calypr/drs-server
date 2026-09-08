package metrics

import (
	"context"
	"sort"
	"time"

	clientaccess "github.com/calypr/syfon/client/access"
	"github.com/calypr/syfon/internal/access"
	"github.com/calypr/syfon/internal/objects"
	"github.com/calypr/syfon/internal/usage"
	"github.com/gofiber/fiber/v3"
)

func metricsTestContext(base context.Context, mode string, headerSet bool, headerValue bool, privileges map[string]map[string]bool) context.Context {
	session := access.NewSession(mode)
	if headerSet {
		session.AuthHeaderPresent = headerValue
	}
	session.AuthzEnforced = mode == "gen3" || mode == "local"
	session.SetAuthorizations(nil, privileges, session.AuthzEnforced)
	return access.WithSession(base, session)
}

func registerMetricsRoutesForTest(app *fiber.App, ingest usage.Ingestor, reports usage.ReportStore, objects usage.ObjectReader) {
	service := usage.NewService(usage.Dependencies{
		Reports: reports,
		Objects: objects,
	})
	RegisterMetricsRoutes(app, service.Reports(), ingest)
}

func (f *metricsReportFake) ListFileUsagePageByScope(ctx context.Context, organization, project string, limit, offset int, inactiveSince *time.Time) ([]usage.FileUsage, error) {
	return f.listScopedFileUsage(ctx, []usage.Scope{{Organization: organization, Project: project}}, false, limit, offset, inactiveSince)
}

func (f *metricsReportFake) ListFileUsagePageByResources(ctx context.Context, resources []string, includeUnscoped bool, limit, offset int, inactiveSince *time.Time) ([]usage.FileUsage, error) {
	if len(resources) == 0 && !includeUnscoped {
		return f.ListFileUsage(ctx, limit, offset, inactiveSince)
	}
	ids := f.objectIDsForResources(resources, includeUnscoped)
	return f.listFileUsageIDs(ids, limit, offset, inactiveSince), nil
}

func (f *metricsReportFake) GetFileUsageSummaryByScope(_ context.Context, organization, project string, inactiveSince *time.Time) (usage.FileUsageSummary, error) {
	ids, _ := f.objects.ListObjectIDsByScope(context.Background(), organization, project, "read")
	return f.fileUsageSummary(ids, inactiveSince), nil
}

func (f *metricsReportFake) GetFileUsageSummaryByResources(_ context.Context, resources []string, includeUnscoped bool, inactiveSince *time.Time) (usage.FileUsageSummary, error) {
	return f.fileUsageSummary(f.objectIDsForResources(resources, includeUnscoped), inactiveSince), nil
}

func (f *metricsReportFake) GetProjectRecordSummaryByScope(_ context.Context, organization, project string) (usage.FileUsageSummary, error) {
	ids, _ := f.objects.ListObjectIDsByScope(context.Background(), organization, project, "read")
	return usage.FileUsageSummary{RecordCount: int64(len(ids))}, nil
}

func (f *metricsReportFake) GetTransferAttributionSummaryByResources(ctx context.Context, filter usage.Filter, resources []string) (usage.Summary, error) {
	return f.scopedTransfer(resources).GetTransferAttributionSummary(ctx, filter)
}

func (f *metricsReportFake) GetTransferAttributionBreakdownByResources(ctx context.Context, filter usage.Filter, groupBy string, resources []string) ([]usage.Breakdown, error) {
	return f.scopedTransfer(resources).GetTransferAttributionBreakdown(ctx, filter, groupBy)
}

func (f *metricsReportFake) listScopedFileUsage(_ context.Context, scopes []usage.Scope, includeUnscoped bool, limit, offset int, inactiveSince *time.Time) ([]usage.FileUsage, error) {
	ids := make([]string, 0)
	for id, record := range f.objects.records {
		matched := includeUnscoped && len(objects.AccessResources(&record)) == 0
		for _, scope := range scopes {
			if metricsObjectMatchesScope(record, scope.Organization, scope.Project) {
				matched = true
			}
		}
		if matched {
			ids = append(ids, id)
		}
	}
	return f.listFileUsageIDs(ids, limit, offset, inactiveSince), nil
}

func (f *metricsReportFake) listFileUsageIDs(ids []string, limit, offset int, inactiveSince *time.Time) []usage.FileUsage {
	items := make([]usage.FileUsage, 0, len(ids))
	for _, id := range ids {
		item, ok := f.fileUsage[id]
		if !ok {
			continue
		}
		if inactiveSince != nil && item.LastDownloadTime != nil && !item.LastDownloadTime.Before(*inactiveSince) {
			continue
		}
		items = append(items, item)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].ObjectID < items[j].ObjectID })
	if offset < 0 {
		offset = 0
	}
	if offset >= len(items) {
		return []usage.FileUsage{}
	}
	if limit <= 0 || offset+limit > len(items) {
		return items[offset:]
	}
	return items[offset : offset+limit]
}

func (f *metricsReportFake) fileUsageSummary(ids []string, inactiveSince *time.Time) usage.FileUsageSummary {
	summary := usage.FileUsageSummary{TotalFiles: int64(len(ids)), RecordCount: int64(len(ids))}
	for _, id := range ids {
		item, ok := f.fileUsage[id]
		if !ok {
			if inactiveSince != nil {
				summary.InactiveFileCount++
			}
			continue
		}
		summary.TotalUploads += item.UploadCount
		summary.TotalDownloads += item.DownloadCount
		if inactiveSince != nil && (item.LastDownloadTime == nil || item.LastDownloadTime.Before(*inactiveSince)) {
			summary.InactiveFileCount++
		}
	}
	return summary
}

func (f *metricsReportFake) objectIDsForResources(resources []string, includeUnscoped bool) []string {
	ids := make([]string, 0)
	for id, record := range f.objects.records {
		matched := includeUnscoped && len(objects.AccessResources(&record)) == 0
		for _, resource := range resources {
			organization, project, ok := clientaccess.ResourceScope(resource)
			if ok && metricsObjectMatchesScope(record, organization, project) {
				matched = true
			}
		}
		if matched {
			ids = append(ids, id)
		}
	}
	return ids
}

func (f *metricsReportFake) scopedTransfer(resources []string) *metricsReportFake {
	selected := make([]usage.Event, 0, len(f.transfers.events))
	for _, event := range f.transfers.events {
		for _, resource := range resources {
			organization, project, ok := clientaccess.ResourceScope(resource)
			if ok && event.Organization == organization && (project == "" || event.Project == project) {
				selected = append(selected, event)
				break
			}
		}
	}
	copy := *f
	state := *f.transfers
	state.events = selected
	copy.transfers = &state
	return &copy
}
