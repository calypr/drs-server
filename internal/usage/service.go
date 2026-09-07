package usage

import (
	"context"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/calypr/syfon/internal/faults"
)

var (
	ErrReportsUnavailable = errors.New("usage report store is unavailable")
	ErrObjectsUnavailable = errors.New("usage object reader is unavailable")
	ErrInvalidGroupBy     = errors.New("invalid transfer breakdown group_by")
)

// Scope identifies one organization/project authorization scope.
type Scope struct {
	Organization string
	Project      string
}

// ScopeQuery describes an already-authorized report scope selection. An empty
// Organization means an unscoped report unless Scopes contains aggregate
// scopes. Resources are supplied explicitly for the optional persistence fast
// path; usage deliberately does not know resource-path encoding.
type ScopeQuery struct {
	Organization    string
	Project         string
	Scopes          []Scope
	Resources       []string
	IncludeUnscoped bool
}

func (q ScopeQuery) isSingle() bool {
	return strings.TrimSpace(q.Organization) != ""
}

func (q ScopeQuery) isAggregate() bool {
	return !q.isSingle() && len(q.Scopes) > 0
}

func (q ScopeQuery) aggregateScopes() []Scope {
	if len(q.Scopes) == 0 {
		return nil
	}
	return append([]Scope(nil), q.Scopes...)
}

func (q ScopeQuery) resources() []string {
	if len(q.Resources) == 0 {
		return nil
	}
	return append([]string(nil), q.Resources...)
}

// FileUsageQuery is the explicit use-case input for a paged file report.
// Limit <= 0 retains the existing convention of returning the complete
// fallback result; public adapters validate pagination bounds.
type FileUsageQuery struct {
	Scope         ScopeQuery
	Limit         int
	Offset        int
	InactiveSince *time.Time
}

// FileUsageSummaryQuery is the explicit use-case input for a file summary.
type FileUsageSummaryQuery struct {
	Scope         ScopeQuery
	InactiveSince *time.Time
}

// TransferSummaryQuery is the explicit use-case input for an attribution
// summary. Aggregate scope selection is applied only when Filter.Organization
// is empty, matching the existing metrics behavior.
type TransferSummaryQuery struct {
	Filter Filter
	Scope  ScopeQuery
}

// TransferBreakdownQuery is the explicit use-case input for an attribution
// breakdown.
type TransferBreakdownQuery struct {
	Filter  Filter
	GroupBy string
	Scope   ScopeQuery
}

type Reporter interface {
	GetFileUsage(ctx context.Context, objectID string) (*FileUsage, error)
	ListFileUsageByObjectIDs(ctx context.Context, ids []string) ([]FileUsage, error)
	ListReadableObjectIDs(ctx context.Context, scope ScopeQuery, requested []string) ([]string, error)
	ListFileUsage(ctx context.Context, query FileUsageQuery) ([]FileUsage, error)
	GetFileUsageSummary(ctx context.Context, query FileUsageSummaryQuery) (FileUsageSummary, error)
	GetTransferAttributionSummary(ctx context.Context, query TransferSummaryQuery) (Summary, error)
	GetTransferAttributionBreakdown(ctx context.Context, query TransferBreakdownQuery) ([]Breakdown, error)
	GetTransferFreshness(ctx context.Context, filter Filter) (Freshness, error)
}

type Dependencies struct {
	Reports ReportStore
	Objects ObjectReader
}

type Service struct {
	reports ReportStore
	objects ObjectReader
}

func NewService(deps Dependencies) *Service {
	return &Service{reports: deps.Reports, objects: deps.Objects}
}

func (s *Service) Reports() Reporter { return s }

func (s *Service) requireReports() error {
	if s == nil || s.reports == nil {
		return ErrReportsUnavailable
	}
	return nil
}

func (s *Service) GetFileUsage(ctx context.Context, objectID string) (*FileUsage, error) {
	if err := s.requireReports(); err != nil {
		return nil, err
	}
	return s.reports.GetFileUsage(ctx, objectID)
}

func (s *Service) ListFileUsageByObjectIDs(ctx context.Context, ids []string) ([]FileUsage, error) {
	if err := s.requireReports(); err != nil {
		return nil, err
	}
	return s.reports.ListFileUsageByObjectIDs(ctx, ids)
}

// ListReadableObjectIDs resolves scope membership from the object reader and
// returns only requested IDs in their original order. Unscoped callers retain
// the existing behavior of passing requests through unchanged.
func (s *Service) ListReadableObjectIDs(ctx context.Context, scope ScopeQuery, requested []string) ([]string, error) {
	if !scope.isSingle() && !scope.isAggregate() {
		return append([]string(nil), requested...), nil
	}
	if s == nil || s.objects == nil {
		return nil, ErrObjectsUnavailable
	}

	readable := make(map[string]struct{})
	addScope := func(organization, project string) error {
		ids, err := s.objects.ListObjectIDsByScope(ctx, organization, project, "read")
		if err != nil {
			return err
		}
		for _, id := range ids {
			id = strings.TrimSpace(id)
			if id != "" {
				readable[id] = struct{}{}
			}
		}
		return nil
	}
	if scope.isSingle() {
		if err := addScope(scope.Organization, scope.Project); err != nil {
			return nil, err
		}
	} else {
		for _, selected := range scope.aggregateScopes() {
			if err := addScope(selected.Organization, selected.Project); err != nil {
				return nil, err
			}
		}
	}

	out := make([]string, 0, len(requested))
	for _, id := range requested {
		if _, ok := readable[strings.TrimSpace(id)]; ok {
			out = append(out, id)
		}
	}
	return out, nil
}

// ListFileUsage chooses a scoped persistence optimization when available and
// otherwise runs the object-authorized fallback aggregation used by metrics.
func (s *Service) ListFileUsage(ctx context.Context, query FileUsageQuery) ([]FileUsage, error) {
	if err := s.requireReports(); err != nil {
		return nil, err
	}
	scope := query.Scope
	if scope.isSingle() {
		if scoped, ok := s.reports.(OptionalScopedFileUsageQuery); ok {
			return scoped.ListFileUsagePageByScope(ctx, scope.Organization, scope.Project, query.Limit, query.Offset, query.InactiveSince)
		}
		items, _, err := s.collectScopedUsage(ctx, Scope{Organization: scope.Organization, Project: scope.Project}, query.InactiveSince)
		if err != nil {
			return nil, err
		}
		return pageFileUsage(items, query.Limit, query.Offset), nil
	}
	if scope.isAggregate() {
		if scoped, ok := s.reports.(OptionalScopedFileUsageQuery); ok {
			return scoped.ListFileUsagePageByResources(ctx, scope.resources(), scope.IncludeUnscoped, query.Limit, query.Offset, query.InactiveSince)
		}
		items, _, err := s.collectMultiScopedUsage(ctx, scope.aggregateScopes(), query.InactiveSince)
		if err != nil {
			return nil, err
		}
		return pageFileUsage(items, query.Limit, query.Offset), nil
	}
	return s.reports.ListFileUsage(ctx, query.Limit, query.Offset, query.InactiveSince)
}

// GetFileUsageSummary chooses the scoped optimization when available and
// preserves fallback record-count and inactive-file semantics.
func (s *Service) GetFileUsageSummary(ctx context.Context, query FileUsageSummaryQuery) (FileUsageSummary, error) {
	if err := s.requireReports(); err != nil {
		return FileUsageSummary{}, err
	}
	scope := query.Scope
	if scope.isSingle() {
		if scoped, ok := s.reports.(OptionalScopedFileUsageQuery); ok {
			summary, err := scoped.GetFileUsageSummaryByScope(ctx, scope.Organization, scope.Project, query.InactiveSince)
			if err != nil {
				return FileUsageSummary{}, err
			}
			recordSummary, err := scoped.GetProjectRecordSummaryByScope(ctx, scope.Organization, scope.Project)
			if err != nil {
				return FileUsageSummary{}, err
			}
			summary.RecordCount = recordSummary.RecordCount
			summary.RecordLatestUpdatedTime = recordSummary.RecordLatestUpdatedTime
			return summary, nil
		}
		_, summary, err := s.collectScopedUsage(ctx, Scope{Organization: scope.Organization, Project: scope.Project}, query.InactiveSince)
		if err != nil {
			return FileUsageSummary{}, err
		}
		summary.RecordCount = summary.TotalFiles
		return summary, nil
	}
	if scope.isAggregate() {
		if scoped, ok := s.reports.(OptionalScopedFileUsageQuery); ok {
			return scoped.GetFileUsageSummaryByResources(ctx, scope.resources(), scope.IncludeUnscoped, query.InactiveSince)
		}
		_, summary, err := s.collectMultiScopedUsage(ctx, scope.aggregateScopes(), query.InactiveSince)
		return summary, err
	}
	return s.reports.GetFileUsageSummary(ctx, query.InactiveSince)
}

// GetTransferAttributionSummary chooses the scoped optimization only for an
// aggregate selection without an explicit organization filter.
func (s *Service) GetTransferAttributionSummary(ctx context.Context, query TransferSummaryQuery) (Summary, error) {
	if err := s.requireReports(); err != nil {
		return Summary{}, err
	}
	if query.Scope.isAggregate() && strings.TrimSpace(query.Filter.Organization) == "" {
		if scoped, ok := s.reports.(OptionalScopedTransferQuery); ok {
			return scoped.GetTransferAttributionSummaryByResources(ctx, query.Filter, query.Scope.resources())
		}
		return s.aggregateTransferSummary(ctx, query.Filter, query.Scope.aggregateScopes())
	}
	return s.reports.GetTransferAttributionSummary(ctx, query.Filter)
}

// GetTransferAttributionBreakdown preserves group validation, scoped
// optimization, fallback merging, and latest-transfer ordering.
func (s *Service) GetTransferAttributionBreakdown(ctx context.Context, query TransferBreakdownQuery) ([]Breakdown, error) {
	if err := s.requireReports(); err != nil {
		return nil, err
	}
	if !validBreakdownGroup(query.GroupBy) {
		return nil, ErrInvalidGroupBy
	}
	if query.Scope.isAggregate() && strings.TrimSpace(query.Filter.Organization) == "" {
		if scoped, ok := s.reports.(OptionalScopedTransferQuery); ok {
			return scoped.GetTransferAttributionBreakdownByResources(ctx, query.Filter, query.GroupBy, query.Scope.resources())
		}
		return s.aggregateTransferBreakdown(ctx, query.Filter, query.GroupBy, query.Scope.aggregateScopes())
	}
	return s.reports.GetTransferAttributionBreakdown(ctx, query.Filter, query.GroupBy)
}

func (s *Service) GetTransferFreshness(_ context.Context, filter Filter) (Freshness, error) {
	return Freshness{
		IsStale:             false,
		MissingBuckets:      []string{},
		RequiredFrom:        filter.From,
		RequiredTo:          filter.To,
		LatestCompletedSync: nil,
	}, nil
}

func (s *Service) collectScopedUsage(ctx context.Context, scope Scope, inactiveSince *time.Time) ([]FileUsage, FileUsageSummary, error) {
	if s.objects == nil {
		return nil, FileUsageSummary{}, ErrObjectsUnavailable
	}
	ids, err := s.objects.ListObjectIDsByScope(ctx, scope.Organization, scope.Project, "read")
	if err != nil {
		return nil, FileUsageSummary{}, err
	}
	sort.Strings(ids)

	summary := FileUsageSummary{TotalFiles: int64(len(ids))}
	bulkUsage, err := s.reports.ListFileUsageByObjectIDs(ctx, ids)
	if err != nil {
		return nil, FileUsageSummary{}, err
	}
	usageByID := make(map[string]FileUsage, len(bulkUsage))
	for _, fileUsage := range bulkUsage {
		usageByID[fileUsage.ObjectID] = fileUsage
	}
	items := make([]FileUsage, 0, len(ids))
	for _, id := range ids {
		fileUsage, ok := usageByID[id]
		if !ok {
			if inactiveSince != nil {
				summary.InactiveFileCount++
			}
			obj, objErr := s.objects.GetObject(ctx, id, "read")
			if objErr != nil {
				if errors.Is(objErr, faults.ErrNotFound) || errors.Is(objErr, faults.ErrUnauthorized) {
					continue
				}
				return nil, FileUsageSummary{}, objErr
			}
			item := FileUsage{ObjectID: id}
			if obj != nil {
				if obj.Name != nil {
					item.Name = *obj.Name
				}
				item.Size = obj.Size
			}
			items = append(items, item)
			continue
		}
		summary.TotalUploads += fileUsage.UploadCount
		summary.TotalDownloads += fileUsage.DownloadCount
		if inactiveSince != nil && (fileUsage.LastDownloadTime == nil || fileUsage.LastDownloadTime.Before(*inactiveSince)) {
			summary.InactiveFileCount++
		}
		if inactiveSince != nil && fileUsage.LastDownloadTime != nil && !fileUsage.LastDownloadTime.Before(*inactiveSince) {
			continue
		}
		items = append(items, fileUsage)
	}
	return items, summary, nil
}

func (s *Service) collectMultiScopedUsage(ctx context.Context, scopes []Scope, inactiveSince *time.Time) ([]FileUsage, FileUsageSummary, error) {
	byID := make(map[string]FileUsage)
	var summary FileUsageSummary
	for _, scope := range scopes {
		items, scopedSummary, err := s.collectScopedUsage(ctx, scope, inactiveSince)
		if err != nil {
			return nil, FileUsageSummary{}, err
		}
		summary.TotalFiles += scopedSummary.TotalFiles
		summary.TotalUploads += scopedSummary.TotalUploads
		summary.TotalDownloads += scopedSummary.TotalDownloads
		summary.InactiveFileCount += scopedSummary.InactiveFileCount
		for _, item := range items {
			byID[item.ObjectID] = item
		}
	}
	items := make([]FileUsage, 0, len(byID))
	for _, item := range byID {
		items = append(items, item)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].ObjectID < items[j].ObjectID })
	return items, summary, nil
}

func pageFileUsage(items []FileUsage, limit, offset int) []FileUsage {
	if limit <= 0 {
		return items
	}
	if offset >= len(items) {
		return []FileUsage{}
	}
	end := offset + limit
	if end > len(items) {
		end = len(items)
	}
	return items[offset:end]
}

func (s *Service) aggregateTransferSummary(ctx context.Context, filter Filter, scopes []Scope) (Summary, error) {
	var out Summary
	for _, scope := range scopes {
		scoped := filter
		scoped.Organization = scope.Organization
		scoped.Project = scope.Project
		item, err := s.reports.GetTransferAttributionSummary(ctx, scoped)
		if err != nil {
			return Summary{}, err
		}
		out.EventCount += item.EventCount
		out.AccessIssuedCount += item.AccessIssuedCount
		out.DownloadEventCount += item.DownloadEventCount
		out.UploadEventCount += item.UploadEventCount
		out.BytesRequested += item.BytesRequested
		out.BytesDownloaded += item.BytesDownloaded
		out.BytesUploaded += item.BytesUploaded
	}
	return out, nil
}

func (s *Service) aggregateTransferBreakdown(ctx context.Context, filter Filter, groupBy string, scopes []Scope) ([]Breakdown, error) {
	byKey := map[string]*Breakdown{}
	for _, scope := range scopes {
		scoped := filter
		scoped.Organization = scope.Organization
		scoped.Project = scope.Project
		items, err := s.reports.GetTransferAttributionBreakdown(ctx, scoped, groupBy)
		if err != nil {
			return nil, err
		}
		for _, item := range items {
			key := item.Key
			if key == "" {
				key = item.Organization + "/" + item.Project + "/" + item.Provider + "/" + item.Bucket + "/" + item.SHA256 + "/" + item.ActorEmail + "/" + item.ActorSubject
			}
			merged := byKey[key]
			if merged == nil {
				copy := item
				byKey[key] = &copy
				continue
			}
			merged.EventCount += item.EventCount
			merged.BytesRequested += item.BytesRequested
			merged.BytesDownloaded += item.BytesDownloaded
			merged.BytesUploaded += item.BytesUploaded
			if item.LastTransferTime != nil && (merged.LastTransferTime == nil || item.LastTransferTime.After(*merged.LastTransferTime)) {
				t := *item.LastTransferTime
				merged.LastTransferTime = &t
			}
		}
	}
	out := make([]Breakdown, 0, len(byKey))
	for _, item := range byKey {
		out = append(out, *item)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].LastTransferTime == nil || out[j].LastTransferTime == nil {
			return out[i].Key < out[j].Key
		}
		if out[i].LastTransferTime.Equal(*out[j].LastTransferTime) {
			return out[i].Key < out[j].Key
		}
		return out[i].LastTransferTime.After(*out[j].LastTransferTime)
	})
	return out, nil
}

func validBreakdownGroup(groupBy string) bool {
	switch groupBy {
	case "scope", "user", "provider", "object":
		return true
	default:
		return false
	}
}
