package storage

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/calypr/syfon/apigen/drs"
	"github.com/calypr/syfon/apigen/errorapi"
	internalapi "github.com/calypr/syfon/apigen/internalapi"
	clientaccess "github.com/calypr/syfon/client/access"
	"github.com/calypr/syfon/internal/access"
	"github.com/calypr/syfon/internal/objects"
)

const defaultPageSize = 500

const (
	FindingLegacyAccessURLRemovable  = "legacy_access_url_removable"
	FindingLegacyAccessURLRewritable = "legacy_access_url_rewritable"
	FindingNonCanonicalAccessURL     = "non_canonical_access_url"
	FindingMissingControlledAccess   = "missing_controlled_access"
	FindingDuplicateSHA256Sibling    = "duplicate_sha256_sibling"
	FindingStorageObjectMissing      = "storage_object_missing"
	FindingStorageProbeError         = "storage_probe_error"

	SeverityInfo  = "info"
	SeverityWarn  = "warn"
	SeverityError = "error"
)

type repairScopeTarget struct {
	Resource     string
	Organization string
	Project      string
	Bucket       string
	Prefix       string
}

type auditedObject struct {
	record         drs.DrsObject
	sha256         string
	currentURLs    []string
	scope          repairScopeTarget
	scopeKnown     bool
	scopeAmbiguous bool
	inferredScope  string
	canonicalURL   string
	findings       []internalapi.ScopeRepairFinding
	updated        *drs.DrsObject
}

// AuditAuthorized checks read access for the requested scope before auditing it.
func (s *Service) AuditAuthorized(ctx context.Context, options internalapi.ScopeRepairOptions) (internalapi.ScopeRepairReport, error) {
	options.Organization = strings.TrimSpace(options.Organization)
	options.Project = strings.TrimSpace(options.Project)
	if options.Organization == "" || options.Project == "" {
		return internalapi.ScopeRepairReport{}, fmt.Errorf("audit requires --organization and --project")
	}
	if err := authorizeStorageCleanupScope(ctx, options.Organization, options.Project, "read"); err != nil {
		return internalapi.ScopeRepairReport{}, err
	}
	report, _, err := s.audit(ctx, options)
	if err != nil {
		return internalapi.ScopeRepairReport{}, err
	}
	return report, nil
}

// ApplyAuthorized requires read and update access for the requested scope before applying repairs.
func (s *Service) ApplyAuthorized(ctx context.Context, options internalapi.ScopeRepairOptions) (internalapi.ScopeRepairApplyResult, error) {
	options.Organization = strings.TrimSpace(options.Organization)
	options.Project = strings.TrimSpace(options.Project)
	if options.Organization == "" || options.Project == "" {
		return internalapi.ScopeRepairApplyResult{}, fmt.Errorf("apply requires --organization and --project")
	}
	if err := authorizeStorageCleanupScope(ctx, options.Organization, options.Project, "read"); err != nil {
		return internalapi.ScopeRepairApplyResult{}, err
	}
	if err := authorizeStorageCleanupScope(ctx, options.Organization, options.Project, "update"); err != nil {
		return internalapi.ScopeRepairApplyResult{}, err
	}
	return s.apply(ctx, options)
}

func (s *Service) apply(ctx context.Context, options internalapi.ScopeRepairOptions) (internalapi.ScopeRepairApplyResult, error) {
	if s.records != nil {
		if _, err := s.records.CollapseProjectChecksumDuplicates(ctx, options.Organization, options.Project); err != nil {
			return internalapi.ScopeRepairApplyResult{}, err
		}
	}
	report, audited, err := s.audit(ctx, options)
	if err != nil {
		return internalapi.ScopeRepairApplyResult{}, err
	}
	result := internalapi.ScopeRepairApplyResult{Report: report}
	for _, object := range audited {
		if object.updated == nil {
			continue
		}
		result.AutoFixable++
		if s.records == nil {
			result.Skipped++
			continue
		}
		if _, err := s.records.UpdateObjectMetadata(ctx, object.record.Id, *object.updated, objects.Scope{}, nil); err != nil {
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

func (s *Service) audit(ctx context.Context, options internalapi.ScopeRepairOptions) (internalapi.ScopeRepairReport, []*auditedObject, error) {
	if s.records == nil {
		return internalapi.ScopeRepairReport{}, nil, fmt.Errorf("prepared record reader is not configured")
	}
	scopes, err := s.loadScopeTargets(ctx)
	if err != nil {
		return internalapi.ScopeRepairReport{}, nil, err
	}
	pageSize := options.PageSize
	if pageSize <= 0 {
		pageSize = defaultPageSize
	}
	records := make([]drs.DrsObject, 0)
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
		page, err := s.records.ListObjects(ctx, objects.RecordListQuery{Scope: objects.Scope{Organization: options.Organization, Project: options.Project}, RequiredMethod: "read", StartAfter: start, Limit: limit})
		if err != nil {
			return internalapi.ScopeRepairReport{}, nil, err
		}
		if len(page) == 0 {
			break
		}
		records = append(records, page...)
		scanned += len(page)
		start = strings.TrimSpace(page[len(page)-1].Id)
		if len(page) < limit || start == "" {
			break
		}
	}
	report := internalapi.ScopeRepairReport{Organization: strings.TrimSpace(options.Organization), Project: strings.TrimSpace(options.Project), Scanned: scanned}
	audited := make([]*auditedObject, 0, len(records))
	for _, record := range records {
		object, include := s.auditRecord(ctx, record, scopes, options)
		if include {
			audited = append(audited, object)
		}
	}
	s.addDuplicateFindings(audited)
	sort.Slice(audited, func(i, j int) bool { return audited[i].record.Id < audited[j].record.Id })
	for _, object := range audited {
		if len(object.findings) == 0 {
			continue
		}
		report.Objects = append(report.Objects, internalapi.ScopeRepairObjectReport{
			ObjectId:             object.record.Id,
			Sha256:               object.sha256,
			Organization:         object.scope.Organization,
			Project:              object.scope.Project,
			CurrentAccessUrls:    append([]string(nil), object.currentURLs...),
			ProposedCanonicalUrl: object.canonicalURL,
			AutoFixable:          object.updated != nil,
			Findings:             append([]internalapi.ScopeRepairFinding(nil), object.findings...),
		})
	}
	return report, audited, nil
}

func (s *Service) auditRecord(ctx context.Context, record drs.DrsObject, scopes map[string][]repairScopeTarget, options internalapi.ScopeRepairOptions) (*auditedObject, bool) {
	sha, _ := objects.CanonicalSHA256(record.Checksums)
	object := &auditedObject{record: record, sha256: sha, currentURLs: accessMethodURLs(record.AccessMethods)}
	resource, known, ambiguous := inferRecordResource(record, sha, scopes)
	object.scopeKnown = known
	object.scopeAmbiguous = ambiguous
	object.inferredScope = resource
	if known && len(scopes[resource]) > 0 {
		object.scope = scopes[resource][0]
		object.canonicalURL = canonicalAccessURL(object.scope, record.Id, sha)
	}
	targetResource := ""
	if strings.TrimSpace(options.Organization) != "" && strings.TrimSpace(options.Project) != "" {
		targetResource, _ = clientaccess.ResourcePath(options.Organization, options.Project)
	}
	hasTargetResource := false
	for _, resource := range objects.AccessResources(&record) {
		if resource == targetResource {
			hasTargetResource = true
			break
		}
	}
	if targetResource != "" && !hasTargetResource && object.inferredScope != targetResource {
		return object, false
	}
	if targetResource != "" && !hasTargetResource && object.inferredScope == targetResource && sha != "" {
		object.findings = append(object.findings, newFinding(FindingMissingControlledAccess, SeverityWarn, record, sha, object.currentURLs, object.canonicalURL, true, "missing controlled_access row recoverable from deterministic scope"))
		updated := cloneRecord(record)
		controlled := make([]string, 0)
		if updated.ControlledAccess != nil {
			controlled = append(controlled, (*updated.ControlledAccess)...)
		}
		controlled = append(controlled, targetResource)
		normalized := clientaccess.NormalizeAccessResources(controlled)
		updated.ControlledAccess = &normalized
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
