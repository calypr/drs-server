package usage

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/calypr/syfon/apigen/errorapi"
	"github.com/calypr/syfon/apigen/metricsapi"
)

var (
	ErrReportsUnavailable = fmt.Errorf("usage report store is unavailable")
	ErrObjectsUnavailable = fmt.Errorf("usage object reader is unavailable")
	ErrInvalidGroupBy     = fmt.Errorf("invalid transfer breakdown group_by")
)

// Scope identifies one organization/project authorization scope.
type Scope struct {
	Organization string
	Project      string
}

// ScopeQuery describes an already-authorized report scope selection. An empty
// Organization means an unscoped report unless Scopes contains aggregate
// scopes. Resources are supplied by the authorization boundary for scoped
// persistence queries; usage deliberately does not know resource-path encoding.
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
// result; public adapters validate pagination bounds.
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
	GetFileUsage(ctx context.Context, objectID string) (*metricsapi.FileUsage, error)
	ListFileUsageBatch(ctx context.Context, query FileUsageBatchQuery) ([]metricsapi.FileUsage, error)
	GetScopedFileUsage(ctx context.Context, objectID string, scope ScopeQuery) (*metricsapi.FileUsage, error)
	ListFileUsage(ctx context.Context, query FileUsageQuery) ([]metricsapi.FileUsage, error)
	GetFileUsageSummary(ctx context.Context, query FileUsageSummaryQuery) (metricsapi.FileUsageSummary, error)
	GetTransferAttributionSummary(ctx context.Context, query TransferSummaryQuery) (metricsapi.TransferAttributionSummary, error)
	GetTransferAttributionBreakdown(ctx context.Context, query TransferBreakdownQuery) ([]metricsapi.TransferAttributionBreakdown, error)
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

func (s *Service) requireReports() error {
	if s == nil || s.reports == nil {
		return ErrReportsUnavailable
	}
	return nil
}

func (s *Service) GetFileUsage(ctx context.Context, objectID string) (*metricsapi.FileUsage, error) {
	if err := s.requireReports(); err != nil {
		return nil, err
	}
	return s.reports.GetFileUsage(ctx, objectID)
}

func (s *Service) listReadableObjectIDs(ctx context.Context, scope ScopeQuery, requested []string) ([]string, error) {
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

// ListFileUsageBatch normalizes requested IDs, filters them through the
// authorized scope, queries persistence once, and applies inactivity filtering
// without changing persistence order.
func (s *Service) ListFileUsageBatch(ctx context.Context, query FileUsageBatchQuery) ([]metricsapi.FileUsage, error) {
	requested := uniqueNonEmptyStrings(query.ObjectIDs)
	readable, err := s.listReadableObjectIDs(ctx, query.Scope, requested)
	if err != nil {
		return nil, err
	}
	if err := s.requireReports(); err != nil {
		return nil, err
	}
	items, err := s.reports.ListFileUsageByObjectIDs(ctx, readable)
	if err != nil {
		return nil, err
	}
	if query.InactiveSince == nil {
		return items, nil
	}
	filtered := make([]metricsapi.FileUsage, 0, len(items))
	for _, item := range items {
		if item.LastDownloadTime == nil || item.LastDownloadTime.Before(*query.InactiveSince) {
			filtered = append(filtered, item)
		}
	}
	return filtered, nil
}

// GetScopedFileUsage enforces object membership before exposing a single
// report. Inaccessible objects intentionally look absent at the HTTP boundary.
func (s *Service) GetScopedFileUsage(ctx context.Context, objectID string, scope ScopeQuery) (*metricsapi.FileUsage, error) {
	if scope.isSingle() || scope.isAggregate() {
		readable, err := s.listReadableObjectIDs(ctx, scope, []string{objectID})
		if err != nil {
			if errorsIsNotFoundOrDenied(err) {
				return nil, errorapi.ErrNotFound
			}
			return nil, err
		}
		if len(readable) == 0 {
			return nil, errorapi.ErrNotFound
		}
	}
	return s.GetFileUsage(ctx, objectID)
}

func uniqueNonEmptyStrings(values []string) []string {
	out := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func errorsIsNotFoundOrDenied(err error) bool {
	return errors.Is(err, errorapi.ErrNotFound) || errors.Is(err, errorapi.ErrAccessDenied)
}

// ListFileUsage routes scoped reports through the required Store capability.
func (s *Service) ListFileUsage(ctx context.Context, query FileUsageQuery) ([]metricsapi.FileUsage, error) {
	if err := s.requireReports(); err != nil {
		return nil, err
	}
	scope := query.Scope
	if scope.isSingle() {
		return s.reports.ListFileUsagePageByScope(ctx, scope.Organization, scope.Project, query.Limit, query.Offset, query.InactiveSince)
	}
	if scope.isAggregate() {
		return s.reports.ListFileUsagePageByResources(ctx, scope.resources(), scope.IncludeUnscoped, query.Limit, query.Offset, query.InactiveSince)
	}
	return s.reports.ListFileUsage(ctx, query.Limit, query.Offset, query.InactiveSince)
}

// GetFileUsageSummary routes scoped summaries through the required Store
// capability and supplements single-scope reports with record metadata.
func (s *Service) GetFileUsageSummary(ctx context.Context, query FileUsageSummaryQuery) (metricsapi.FileUsageSummary, error) {
	if err := s.requireReports(); err != nil {
		return metricsapi.FileUsageSummary{}, err
	}
	scope := query.Scope
	if scope.isSingle() {
		summary, err := s.reports.GetFileUsageSummaryByScope(ctx, scope.Organization, scope.Project, query.InactiveSince)
		if err != nil {
			return metricsapi.FileUsageSummary{}, err
		}
		recordSummary, err := s.reports.GetProjectRecordSummaryByScope(ctx, scope.Organization, scope.Project)
		if err != nil {
			return metricsapi.FileUsageSummary{}, err
		}
		summary.RecordCount = recordSummary.RecordCount
		summary.RecordLatestUpdatedTime = recordSummary.RecordLatestUpdatedTime
		return summary, nil
	}
	if scope.isAggregate() {
		return s.reports.GetFileUsageSummaryByResources(ctx, scope.resources(), scope.IncludeUnscoped, query.InactiveSince)
	}
	return s.reports.GetFileUsageSummary(ctx, query.InactiveSince)
}

// GetTransferAttributionSummary routes aggregate scope reports directly to the
// resource-scoped Store capability when no explicit organization filter exists.
func (s *Service) GetTransferAttributionSummary(ctx context.Context, query TransferSummaryQuery) (metricsapi.TransferAttributionSummary, error) {
	if err := s.requireReports(); err != nil {
		return metricsapi.TransferAttributionSummary{}, err
	}
	var resources []string
	if query.Scope.isAggregate() && strings.TrimSpace(query.Filter.Organization) == "" {
		resources = query.Scope.resources()
	}
	return s.reports.QueryTransferSummary(ctx, query.Filter, resources)
}

// GetTransferAttributionBreakdown preserves group validation and routes
// aggregate scope reports directly to the resource-scoped Store capability.
func (s *Service) GetTransferAttributionBreakdown(ctx context.Context, query TransferBreakdownQuery) ([]metricsapi.TransferAttributionBreakdown, error) {
	if err := s.requireReports(); err != nil {
		return nil, err
	}
	if !validBreakdownGroup(query.GroupBy) {
		return nil, ErrInvalidGroupBy
	}
	var resources []string
	if query.Scope.isAggregate() && strings.TrimSpace(query.Filter.Organization) == "" {
		resources = query.Scope.resources()
	}
	return s.reports.QueryTransferBreakdown(ctx, query.Filter, query.GroupBy, resources)
}

func validBreakdownGroup(groupBy string) bool {
	switch groupBy {
	case "scope", "user", "provider", "object":
		return true
	default:
		return false
	}
}
