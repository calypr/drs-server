package storage

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/calypr/syfon/apigen/errorapi"
	clientaccess "github.com/calypr/syfon/client/access"
	"github.com/calypr/syfon/internal/access"
	"github.com/calypr/syfon/internal/buckets"
	"github.com/calypr/syfon/internal/objects"
)

const defaultPageSize = 500

type RepairService struct {
	records   repairRecordService
	buckets   repairBucketService
	inspector *Inspector
}

type repairRecordService interface {
	ListPreparedObjectsPageByScope(context.Context, string, string, string, string, int, int) ([]objects.Record, error)
	UpdateRecord(context.Context, string, objects.Record, *int64, time.Time) (objects.Record, error)
	CollapseProjectChecksumDuplicates(context.Context, string, string) (int, error)
}

type repairBucketService interface {
	ListS3Credentials(context.Context) ([]buckets.Credential, error)
	ListBucketScopes(context.Context) ([]buckets.Scope, error)
}

func NewRepairService(records repairRecordService, buckets repairBucketService, inspector *Inspector) *RepairService {
	return &RepairService{records: records, buckets: buckets, inspector: inspector}
}

type repairScopeTarget struct {
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
	scope          repairScopeTarget
	scopeKnown     bool
	scopeAmbiguous bool
	inferredScope  string
	canonicalURL   string
	findings       []RepairFinding
	updated        *objects.Record
}

type auditState struct {
	report  RepairReport
	objects []*auditedObject
}

// AuditAuthorized checks read access for the requested scope before auditing it.
func (s *RepairService) AuditAuthorized(ctx context.Context, options RepairOptions) (RepairReport, error) {
	options.Organization = strings.TrimSpace(options.Organization)
	options.Project = strings.TrimSpace(options.Project)
	if options.Organization == "" || options.Project == "" {
		return RepairReport{}, fmt.Errorf("audit requires --organization and --project")
	}
	if err := authorizeStorageCleanupScope(ctx, options.Organization, options.Project, "read"); err != nil {
		return RepairReport{}, err
	}
	state, err := s.audit(ctx, options)
	if err != nil {
		return RepairReport{}, err
	}
	return state.report, nil
}

// ApplyAuthorized requires read and update access for the requested scope before applying repairs.
func (s *RepairService) ApplyAuthorized(ctx context.Context, options RepairOptions) (RepairResult, error) {
	options.Organization = strings.TrimSpace(options.Organization)
	options.Project = strings.TrimSpace(options.Project)
	if options.Organization == "" || options.Project == "" {
		return RepairResult{}, fmt.Errorf("apply requires --organization and --project")
	}
	if err := authorizeStorageCleanupScope(ctx, options.Organization, options.Project, "read"); err != nil {
		return RepairResult{}, err
	}
	if err := authorizeStorageCleanupScope(ctx, options.Organization, options.Project, "update"); err != nil {
		return RepairResult{}, err
	}
	return s.apply(ctx, options)
}

func (s *RepairService) apply(ctx context.Context, options RepairOptions) (RepairResult, error) {
	if s.records != nil {
		if _, err := s.records.CollapseProjectChecksumDuplicates(ctx, options.Organization, options.Project); err != nil {
			return RepairResult{}, err
		}
	}
	state, err := s.audit(ctx, options)
	if err != nil {
		return RepairResult{}, err
	}
	result := RepairResult{Report: state.report}
	for _, object := range state.objects {
		if object.updated == nil {
			continue
		}
		result.AutoFixable++
		if s.records == nil {
			result.Skipped++
			continue
		}
		if _, err := s.records.UpdateRecord(ctx, string(object.record.Id), *object.updated, nil, time.Now().UTC()); err != nil {
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

func (s *RepairService) audit(ctx context.Context, options RepairOptions) (*auditState, error) {
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
	state := &auditState{report: RepairReport{Organization: strings.TrimSpace(options.Organization), Project: strings.TrimSpace(options.Project), Scanned: scanned}}
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
		state.report.Objects = append(state.report.Objects, RepairObjectReport{
			ObjectID:             string(object.record.Id),
			SHA256:               object.sha256,
			Organization:         object.scope.Organization,
			Project:              object.scope.Project,
			CurrentAccessURLs:    append([]string(nil), object.currentURLs...),
			ProposedCanonicalURL: object.canonicalURL,
			AutoFixable:          object.updated != nil,
			Findings:             append([]RepairFinding(nil), object.findings...),
		})
	}
	return state, nil
}

func (s *RepairService) listRecords(ctx context.Context, options RepairOptions) ([]objects.Record, int, error) {
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
		page, err := s.records.ListPreparedObjectsPageByScope(ctx, options.Organization, options.Project, "read", start, limit, 0)
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

func (s *RepairService) auditRecord(ctx context.Context, record objects.Record, scopes map[string][]repairScopeTarget, options RepairOptions) (*auditedObject, bool) {
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
