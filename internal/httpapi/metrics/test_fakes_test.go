package metrics

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/calypr/syfon/internal/faults"
	"github.com/calypr/syfon/internal/objects"
	"github.com/calypr/syfon/internal/usage"
)

func metricsStringPtr(value string) *string {
	return &value
}

type metricsObjectReaderFake struct {
	records map[string]objects.Record
}

var _ usage.ObjectReader = (*metricsObjectReaderFake)(nil)

func newMetricsObjectReader(records map[string]*objects.Record, authorizations map[string]map[string][]string) *metricsObjectReaderFake {
	result := &metricsObjectReaderFake{records: make(map[string]objects.Record, len(records))}
	for id, record := range records {
		copyRecord := *record
		if authz, ok := authorizations[id]; ok {
			copyRecord.Authorizations = cloneMetricsAuthorizations(authz)
		}
		result.records[id] = copyRecord
	}
	return result
}

func (f *metricsObjectReaderFake) ListObjectIDsByScope(_ context.Context, organization, project, _ string) ([]string, error) {
	ids := make([]string, 0, len(f.records))
	for id, record := range f.records {
		if metricsObjectMatchesScope(record, organization, project) {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	return ids, nil
}

func (f *metricsObjectReaderFake) GetObject(_ context.Context, id, _ string) (*objects.Record, error) {
	record, ok := f.records[id]
	if !ok {
		return nil, fmt.Errorf("%w: object not found", faults.ErrNotFound)
	}
	return &record, nil
}

func metricsObjectMatchesScope(record objects.Record, organization, project string) bool {
	organization = strings.TrimSpace(organization)
	project = strings.TrimSpace(project)
	if organization == "" {
		return true
	}
	projects, ok := record.Authorizations[organization]
	if !ok {
		return false
	}
	if project == "" || len(projects) == 0 {
		return true
	}
	for _, candidate := range projects {
		if candidate == project {
			return true
		}
	}
	return false
}

func cloneMetricsAuthorizations(input map[string][]string) map[string][]string {
	if len(input) == 0 {
		return nil
	}
	result := make(map[string][]string, len(input))
	for organization, projects := range input {
		result[organization] = append([]string(nil), projects...)
	}
	return result
}

type metricsTransferState struct {
	events         []usage.Event
	providerEvents []usage.ProviderEvent
}

type metricsIngestFake struct {
	state *metricsTransferState
}

var _ usage.Ingestor = (*metricsIngestFake)(nil)

func (f *metricsIngestFake) RecordFileUpload(context.Context, string) error {
	return nil
}

func (f *metricsIngestFake) RecordFileDownload(context.Context, string) error {
	return nil
}

func (f *metricsIngestFake) RecordTransferAttributionEvents(_ context.Context, events []usage.Event) error {
	for _, event := range events {
		duplicate := false
		for _, existing := range f.state.events {
			if event.EventID != "" && existing.EventID == event.EventID {
				duplicate = true
				break
			}
		}
		if !duplicate {
			f.state.events = append(f.state.events, event)
		}
	}
	return nil
}

func (f *metricsIngestFake) RecordProviderTransferEvents(_ context.Context, events []usage.ProviderEvent) error {
	for _, event := range events {
		duplicate := false
		for _, existing := range f.state.providerEvents {
			if event.ProviderEventID != "" && existing.ProviderEventID == event.ProviderEventID {
				duplicate = true
				break
			}
		}
		if !duplicate {
			f.state.providerEvents = append(f.state.providerEvents, event)
		}
	}
	return nil
}

type metricsReportFake struct {
	objects    *metricsObjectReaderFake
	fileUsage  map[string]usage.FileUsage
	transfers  *metricsTransferState
	totalFiles int64
}

var _ usage.ReportStore = (*metricsReportFake)(nil)

func newMetricsReport(objects *metricsObjectReaderFake, fileUsage map[string]usage.FileUsage, transfers *metricsTransferState) *metricsReportFake {
	return &metricsReportFake{
		objects:    objects,
		fileUsage:  fileUsage,
		transfers:  transfers,
		totalFiles: int64(len(objects.records)),
	}
}

func (f *metricsReportFake) GetFileUsage(_ context.Context, objectID string) (*usage.FileUsage, error) {
	item, ok := f.fileUsage[objectID]
	if !ok {
		return nil, fmt.Errorf("%w: file usage not found", faults.ErrNotFound)
	}
	return &item, nil
}

func (f *metricsReportFake) ListFileUsageByObjectIDs(_ context.Context, ids []string) ([]usage.FileUsage, error) {
	result := make([]usage.FileUsage, 0, len(ids))
	for _, id := range ids {
		if item, ok := f.fileUsage[id]; ok {
			result = append(result, item)
			continue
		}
		if record, ok := f.objects.records[id]; ok {
			item := usage.FileUsage{ObjectID: id, Size: record.Size}
			if record.Name != nil {
				item.Name = *record.Name
			}
			result = append(result, item)
		}
	}
	return result, nil
}

func (f *metricsReportFake) ListFileUsage(_ context.Context, limit, offset int, inactiveSince *time.Time) ([]usage.FileUsage, error) {
	items := make([]usage.FileUsage, 0, len(f.fileUsage))
	for _, item := range f.fileUsage {
		if inactiveSince != nil && item.LastDownloadTime != nil && !item.LastDownloadTime.Before(*inactiveSince) {
			continue
		}
		items = append(items, item)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].ObjectID < items[j].ObjectID })
	if offset >= len(items) {
		return []usage.FileUsage{}, nil
	}
	if offset < 0 {
		offset = 0
	}
	if limit <= 0 || offset+limit > len(items) {
		return items[offset:], nil
	}
	return items[offset : offset+limit], nil
}

func (f *metricsReportFake) GetFileUsageSummary(_ context.Context, inactiveSince *time.Time) (usage.FileUsageSummary, error) {
	summary := usage.FileUsageSummary{TotalFiles: f.totalFiles}
	for _, item := range f.fileUsage {
		summary.TotalUploads += item.UploadCount
		summary.TotalDownloads += item.DownloadCount
		if inactiveSince != nil && (item.LastDownloadTime == nil || item.LastDownloadTime.Before(*inactiveSince)) {
			summary.InactiveFileCount++
		}
	}
	return summary, nil
}

func (f *metricsReportFake) GetTransferAttributionSummary(_ context.Context, filter usage.Filter) (usage.Summary, error) {
	var summary usage.Summary
	for _, event := range f.transfers.events {
		if !metricsTransferEventMatchesFilter(event, filter) {
			continue
		}
		summary.EventCount++
		if event.EventType == usage.TransferEventAccessIssued {
			summary.AccessIssuedCount++
		}
		summary.BytesRequested += event.BytesRequested
		switch event.Direction {
		case usage.ProviderTransferDirectionDownload:
			summary.DownloadEventCount++
			summary.BytesDownloaded += event.BytesRequested
		case usage.ProviderTransferDirectionUpload:
			summary.UploadEventCount++
			summary.BytesUploaded += event.BytesRequested
		}
	}
	return summary, nil
}

func (f *metricsReportFake) GetTransferAttributionBreakdown(_ context.Context, filter usage.Filter, groupBy string) ([]usage.Breakdown, error) {
	items := make(map[string]*usage.Breakdown)
	for _, event := range f.transfers.events {
		if !metricsTransferEventMatchesFilter(event, filter) {
			continue
		}
		key := metricsTransferBreakdownKey(event, groupBy)
		item := items[key]
		if item == nil {
			item = &usage.Breakdown{
				Key:          key,
				Organization: event.Organization,
				Project:      event.Project,
				Provider:     event.Provider,
				Bucket:       event.Bucket,
				SHA256:       event.SHA256,
				ActorEmail:   event.ActorEmail,
				ActorSubject: event.ActorSubject,
			}
			items[key] = item
		}
		item.EventCount++
		item.BytesRequested += event.BytesRequested
		if event.Direction == usage.ProviderTransferDirectionDownload {
			item.BytesDownloaded += event.BytesRequested
		}
		if event.Direction == usage.ProviderTransferDirectionUpload {
			item.BytesUploaded += event.BytesRequested
		}
		if item.LastTransferTime == nil || (!event.EventTime.IsZero() && event.EventTime.After(*item.LastTransferTime)) {
			eventTime := event.EventTime
			item.LastTransferTime = &eventTime
		}
	}
	result := make([]usage.Breakdown, 0, len(items))
	for _, item := range items {
		result = append(result, *item)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Key < result[j].Key })
	return result, nil
}

func metricsTransferEventMatchesFilter(event usage.Event, filter usage.Filter) bool {
	if filter.Organization != "" && event.Organization != filter.Organization {
		return false
	}
	if filter.Project != "" && event.Project != filter.Project {
		return false
	}
	if filter.EventType != "" && filter.EventType != "all" && event.EventType != filter.EventType {
		return false
	}
	direction := filter.Direction
	if direction == "" {
		switch filter.EventType {
		case usage.ProviderTransferDirectionDownload, usage.ProviderTransferDirectionUpload:
			direction = filter.EventType
		}
	}
	if direction != "" && direction != "all" && event.Direction != direction {
		return false
	}
	if filter.From != nil && event.EventTime.Before(*filter.From) {
		return false
	}
	if filter.To != nil && event.EventTime.After(*filter.To) {
		return false
	}
	if filter.Provider != "" && event.Provider != filter.Provider {
		return false
	}
	if filter.Bucket != "" && event.Bucket != filter.Bucket {
		return false
	}
	if filter.SHA256 != "" && event.SHA256 != filter.SHA256 {
		return false
	}
	if filter.User != "" && event.ActorEmail != filter.User && event.ActorSubject != filter.User {
		return false
	}
	return true
}

func metricsTransferBreakdownKey(event usage.Event, groupBy string) string {
	switch groupBy {
	case "user":
		if event.ActorEmail != "" {
			return event.ActorEmail
		}
		return event.ActorSubject
	case "provider":
		return event.Provider + ":" + event.Bucket
	case "object":
		return event.SHA256
	default:
		return event.Organization + "/" + event.Project
	}
}
