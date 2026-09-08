package scoperepair

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/calypr/syfon/apigen/errorapi"
	clientaccess "github.com/calypr/syfon/client/access"
	"github.com/calypr/syfon/internal/access"
	"github.com/calypr/syfon/internal/objects"
)

const defaultPageSize = 500

type Service struct {
	records   PreparedRecordReader
	writer    ReferenceWriter
	scopes    ScopeReader
	probe     StorageProbe
	collapser DuplicateCollapser
}

func NewService(records PreparedRecordReader, writer ReferenceWriter, scopes ScopeReader, probe StorageProbe, collapser DuplicateCollapser) *Service {
	return &Service{records: records, writer: writer, scopes: scopes, probe: probe, collapser: collapser}
}

type scopeTarget struct {
	Resource     string
	Organization string
	Project      string
	Bucket       string
	Prefix       string
}

type auditedObject struct {
	record         objects.Record
	sha256         string
	currentURLs    []string
	scope          scopeTarget
	scopeKnown     bool
	scopeAmbiguous bool
	inferredScope  string
	canonicalURL   string
	findings       []Finding
	updated        *objects.Record
}

type auditState struct {
	report  Report
	objects []*auditedObject
}

func (s *Service) Audit(ctx context.Context, options Options) (Report, error) {
	state, err := s.audit(ctx, options)
	if err != nil {
		return Report{}, err
	}
	return state.report, nil
}

// AuditAuthorized applies the HTTP maintenance read policy before running the
// trusted audit state machine. Trusted callers should continue using Audit.
func (s *Service) AuditAuthorized(ctx context.Context, options Options) (Report, error) {
	options.Organization = strings.TrimSpace(options.Organization)
	options.Project = strings.TrimSpace(options.Project)
	if options.Organization == "" || options.Project == "" {
		return Report{}, fmt.Errorf("audit requires --organization and --project")
	}
	if err := authorizeStorageCleanupScope(ctx, options.Organization, options.Project, "read"); err != nil {
		return Report{}, err
	}
	state, err := s.audit(ctx, options)
	if err != nil {
		return Report{}, err
	}
	return state.report, nil
}

func (s *Service) Apply(ctx context.Context, options Options) (ApplyResult, error) {
	options.Organization = strings.TrimSpace(options.Organization)
	options.Project = strings.TrimSpace(options.Project)
	if options.Organization == "" || options.Project == "" {
		return ApplyResult{}, fmt.Errorf("apply requires --organization and --project")
	}
	return s.apply(ctx, options)
}

// ApplyAuthorized performs read authorization before update authorization and
// before entering the trusted collapse/audit/write state machine. Trusted
// callers should continue using Apply.
func (s *Service) ApplyAuthorized(ctx context.Context, options Options) (ApplyResult, error) {
	options.Organization = strings.TrimSpace(options.Organization)
	options.Project = strings.TrimSpace(options.Project)
	if options.Organization == "" || options.Project == "" {
		return ApplyResult{}, fmt.Errorf("apply requires --organization and --project")
	}
	if err := authorizeStorageCleanupScope(ctx, options.Organization, options.Project, "read"); err != nil {
		return ApplyResult{}, err
	}
	if err := authorizeStorageCleanupScope(ctx, options.Organization, options.Project, "update"); err != nil {
		return ApplyResult{}, err
	}
	return s.apply(ctx, options)
}

func (s *Service) apply(ctx context.Context, options Options) (ApplyResult, error) {
	if s.collapser != nil {
		if _, err := s.collapser.Collapse(ctx, options.Organization, options.Project); err != nil {
			return ApplyResult{}, err
		}
	}
	state, err := s.audit(ctx, options)
	if err != nil {
		return ApplyResult{}, err
	}
	result := ApplyResult{Report: state.report}
	for _, object := range state.objects {
		if object.updated == nil {
			continue
		}
		result.AutoFixable++
		if s.writer == nil {
			result.Skipped++
			continue
		}
		if err := s.writer.Update(ctx, object.record.Id, *object.updated); err != nil {
			result.Skipped++
			continue
		}
		result.Mutated++
	}
	return result, nil
}

func authorizeStorageCleanupScope(ctx context.Context, organization, project string, methods ...string) error {
	if !access.IsAuthzEnforced(ctx) {
		return nil
	}
	resource, err := clientaccess.ResourcePath(organization, project)
	if err != nil {
		return err
	}
	if access.HasMethodAccess(ctx, methods[0], []string{"/programs", "/data_file"}) || access.HasAnyMethodAccess(ctx, []string{resource}, methods...) {
		return nil
	}
	return errorapi.ErrAccessDenied
}

func (s *Service) audit(ctx context.Context, options Options) (*auditState, error) {
	if s.records == nil {
		return nil, fmt.Errorf("prepared record reader is not configured")
	}
	scopes, err := s.loadScopeTargets(ctx)
	if err != nil {
		return nil, err
	}
	records, scanned, err := s.listRecords(ctx, options)
	if err != nil {
		return nil, err
	}
	state := &auditState{report: Report{Organization: strings.TrimSpace(options.Organization), Project: strings.TrimSpace(options.Project), Scanned: scanned}}
	for _, record := range records {
		object, include := s.auditRecord(ctx, record, scopes, options)
		if include {
			state.objects = append(state.objects, object)
		}
	}
	s.addDuplicateFindings(state.objects)
	sort.Slice(state.objects, func(i, j int) bool { return string(state.objects[i].record.Id) < string(state.objects[j].record.Id) })
	for _, object := range state.objects {
		if len(object.findings) == 0 {
			continue
		}
		state.report.Objects = append(state.report.Objects, ObjectReport{
			ObjectID:             string(object.record.Id),
			SHA256:               object.sha256,
			Organization:         object.scope.Organization,
			Project:              object.scope.Project,
			CurrentAccessURLs:    append([]string(nil), object.currentURLs...),
			ProposedCanonicalURL: object.canonicalURL,
			AutoFixable:          object.updated != nil,
			Findings:             append([]Finding(nil), object.findings...),
		})
	}
	return state, nil
}

func (s *Service) listRecords(ctx context.Context, options Options) ([]objects.Record, int, error) {
	pageSize := options.PageSize
	if pageSize <= 0 {
		pageSize = defaultPageSize
	}
	result := make([]objects.Record, 0)
	start := ""
	scanned := 0
	for {
		limit := pageSize
		if options.Limit > 0 && options.Limit-scanned < limit {
			limit = options.Limit - scanned
		}
		if limit <= 0 && options.Limit > 0 {
			break
		}
		page, err := s.records.ListPrepared(ctx, PreparedRecordQuery{Limit: limit, Start: start, Organization: options.Organization, Project: options.Project})
		if err != nil {
			return nil, scanned, err
		}
		if len(page) == 0 {
			break
		}
		result = append(result, page...)
		scanned += len(page)
		start = strings.TrimSpace(string(page[len(page)-1].Id))
		if len(page) < limit || start == "" {
			break
		}
	}
	return result, scanned, nil
}

func (s *Service) auditRecord(ctx context.Context, record objects.Record, scopes map[string][]scopeTarget, options Options) (*auditedObject, bool) {
	sha, _ := objects.CanonicalSHA256(record.Checksums)
	object := &auditedObject{record: record, sha256: sha, currentURLs: accessMethodURLs(record.AccessMethods)}
	resource, known, ambiguous := inferRecordResource(record, sha, scopes)
	object.scopeKnown = known
	object.scopeAmbiguous = ambiguous
	object.inferredScope = resource
	if known && len(scopes[resource]) > 0 {
		object.scope = scopes[resource][0]
		object.canonicalURL = canonicalAccessURL(object.scope, string(record.Id), sha)
	}
	targetResource := ""
	if strings.TrimSpace(options.Organization) != "" && strings.TrimSpace(options.Project) != "" {
		targetResource, _ = clientaccess.ResourcePath(options.Organization, options.Project)
	}
	if targetResource != "" && !recordMatchesResource(record, targetResource) && object.inferredScope != targetResource {
		return object, false
	}
	if targetResource != "" && !recordMatchesResource(record, targetResource) && object.inferredScope == targetResource && sha != "" {
		object.findings = append(object.findings, newFinding(FindingMissingControlledAccess, SeverityWarn, record, sha, object.currentURLs, object.canonicalURL, true, "missing controlled_access row recoverable from deterministic scope"))
		updated := cloneRecord(record)
		updated.ControlledAccess = addControlledAccess(updated.ControlledAccess, targetResource)
		object.updated = &updated
	}
	if object.scopeKnown && object.canonicalURL != "" {
		s.classifyAccessMethods(ctx, object, options.CheckStorage)
	}
	if options.CheckStorage {
		s.addStorageFindings(ctx, object)
	}
	return object, true
}
