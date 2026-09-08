package usage

import (
	"context"
	"fmt"
	"strings"
	"time"
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

// ListFileUsage routes scoped reports through the required Store capability.
func (s *Service) ListFileUsage(ctx context.Context, query FileUsageQuery) ([]FileUsage, error) {
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
func (s *Service) GetFileUsageSummary(ctx context.Context, query FileUsageSummaryQuery) (FileUsageSummary, error) {
	if err := s.requireReports(); err != nil {
		return FileUsageSummary{}, err
	}
	scope := query.Scope
	if scope.isSingle() {
		summary, err := s.reports.GetFileUsageSummaryByScope(ctx, scope.Organization, scope.Project, query.InactiveSince)
		if err != nil {
			return FileUsageSummary{}, err
		}
		recordSummary, err := s.reports.GetProjectRecordSummaryByScope(ctx, scope.Organization, scope.Project)
		if err != nil {
			return FileUsageSummary{}, err
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
func (s *Service) GetTransferAttributionSummary(ctx context.Context, query TransferSummaryQuery) (Summary, error) {
	if err := s.requireReports(); err != nil {
		return Summary{}, err
	}
	if query.Scope.isAggregate() && strings.TrimSpace(query.Filter.Organization) == "" {
		return s.reports.GetTransferAttributionSummaryByResources(ctx, query.Filter, query.Scope.resources())
	}
	return s.reports.GetTransferAttributionSummary(ctx, query.Filter)
}

// GetTransferAttributionBreakdown preserves group validation and routes
// aggregate scope reports directly to the resource-scoped Store capability.
func (s *Service) GetTransferAttributionBreakdown(ctx context.Context, query TransferBreakdownQuery) ([]Breakdown, error) {
	if err := s.requireReports(); err != nil {
		return nil, err
	}
	if !validBreakdownGroup(query.GroupBy) {
		return nil, ErrInvalidGroupBy
	}
	if query.Scope.isAggregate() && strings.TrimSpace(query.Filter.Organization) == "" {
		return s.reports.GetTransferAttributionBreakdownByResources(ctx, query.Filter, query.GroupBy, query.Scope.resources())
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

func validBreakdownGroup(groupBy string) bool {
	switch groupBy {
	case "scope", "user", "provider", "object":
		return true
	default:
		return false
	}
}
