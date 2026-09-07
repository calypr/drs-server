package scoperepair

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strings"

	syfoncommon "github.com/calypr/syfon/common"
	"github.com/calypr/syfon/internal/buckets"
	"github.com/calypr/syfon/internal/objects"
	"github.com/calypr/syfon/internal/storage/address"
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

func (s *Service) Apply(ctx context.Context, options Options) (ApplyResult, error) {
	options.Organization = strings.TrimSpace(options.Organization)
	options.Project = strings.TrimSpace(options.Project)
	if options.Organization == "" || options.Project == "" {
		return ApplyResult{}, fmt.Errorf("apply requires --organization and --project")
	}
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
		targetResource, _ = syfoncommon.ResourcePath(options.Organization, options.Project)
	}
	if targetResource != "" && !recordMatchesResource(record, targetResource) && object.inferredScope != targetResource {
		return object, false
	}
	if targetResource != "" && !recordMatchesResource(record, targetResource) && object.inferredScope == targetResource && sha != "" {
		object.findings = append(object.findings, newFinding(FindingMissingControlledAccess, SeverityWarn, record, sha, object.currentURLs, object.canonicalURL, true, "missing controlled_access row recoverable from deterministic scope"))
		updated := cloneRecord(record)
		updated.ControlledAccess = addControlledAccess(updated.ControlledAccess, targetResource)
		updated.Authorizations = syfoncommon.ControlledAccessToAuthzMap(*updated.ControlledAccess)
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

func (s *Service) classifyAccessMethods(ctx context.Context, object *auditedObject, checkStorage bool) {
	if object.record.AccessMethods == nil {
		return
	}
	methods := cloneAccessMethods(*object.record.AccessMethods)
	pathStyleURL := pathStyleAccessURL(object.scope, objectName(object.record))
	targetURL := object.canonicalURL
	if checkStorage {
		canonicalExists := s.checkURLExists(ctx, object, object.canonicalURL)
		pathStyleExists := s.checkURLExists(ctx, object, pathStyleURL)
		if !canonicalExists && pathStyleExists {
			targetURL = pathStyleURL
		}
	}
	hasTarget := false
	for _, raw := range object.currentURLs {
		if raw == targetURL {
			hasTarget = true
			break
		}
	}
	changed := false
	for index := range methods {
		raw := accessMethodURL(methods[index])
		if raw == "" || raw == targetURL {
			continue
		}
		if hasTarget {
			object.findings = append(object.findings, newFinding(FindingLegacyAccessURLRemovable, SeverityWarn, object.record, object.sha256, object.currentURLs, targetURL, true, fmt.Sprintf("redundant URL %q has target sibling %q", raw, targetURL)))
			methods[index].AccessUrl = nil
			changed = true
			continue
		}
		object.findings = append(object.findings, newFinding(FindingLegacyAccessURLRewritable, SeverityWarn, object.record, object.sha256, object.currentURLs, targetURL, true, fmt.Sprintf("URL %q can be rewritten to target URL %q", raw, targetURL)))
		setAccessMethodURL(&methods[index], targetURL)
		changed = true
		hasTarget = true
	}
	if !changed {
		return
	}
	filtered := make([]objects.AccessMethod, 0, len(methods))
	for _, method := range methods {
		if accessMethodURL(method) != "" {
			filtered = append(filtered, method)
		}
	}
	updated := cloneRecord(object.record)
	updated.AccessMethods = &filtered
	if object.updated != nil && object.updated.ControlledAccess != nil {
		controlled := append([]string(nil), (*object.updated.ControlledAccess)...)
		updated.ControlledAccess = &controlled
		updated.Authorizations = syfoncommon.ControlledAccessToAuthzMap(*updated.ControlledAccess)
	}
	object.updated = &updated
}

func (s *Service) addStorageFindings(ctx context.Context, object *auditedObject) {
	if s.probe == nil {
		return
	}
	for _, raw := range object.currentURLs {
		_, err := s.probe.Inspect(ctx, StorageInspectRequest{Organization: object.scope.Organization, Project: object.scope.Project, ObjectURL: raw})
		if err == nil {
			continue
		}
		kind := FindingStorageProbeError
		severity := SeverityWarn
		message := err.Error()
		if errors.Is(err, ErrStorageObjectNotFound) {
			kind = FindingStorageObjectMissing
			severity = SeverityError
			message = "storage object not found"
		}
		object.findings = append(object.findings, newFinding(kind, severity, object.record, object.sha256, []string{raw}, object.canonicalURL, false, message))
	}
}

func (s *Service) checkURLExists(ctx context.Context, object *auditedObject, raw string) bool {
	if s.probe == nil || strings.TrimSpace(raw) == "" {
		return false
	}
	_, err := s.probe.Inspect(ctx, StorageInspectRequest{Organization: object.scope.Organization, Project: object.scope.Project, ObjectURL: raw})
	return err == nil
}

func (s *Service) loadScopeTargets(ctx context.Context) (map[string][]scopeTarget, error) {
	if s.scopes == nil {
		return nil, fmt.Errorf("scope reader is not configured")
	}
	credentials, err := s.scopes.ListCredentials(ctx)
	if err != nil {
		return nil, fmt.Errorf("list buckets: %w", err)
	}
	out := make(map[string][]scopeTarget)
	for _, credential := range credentials {
		if address.NormalizeProvider(credential.Provider, address.S3Provider) != address.S3Provider {
			continue
		}
		bucket := strings.TrimSpace(credential.Bucket)
		if bucket == "" {
			continue
		}
		scopes, err := s.scopes.ListScopes(ctx, bucket)
		if err != nil {
			return nil, fmt.Errorf("list bucket scopes for %s: %w", bucket, err)
		}
		for _, scope := range scopes {
			if !scopeBelongsToCredential(scope, credential) {
				continue
			}
			resource, err := syfoncommon.ResourcePath(strings.TrimSpace(scope.Organization), strings.TrimSpace(scope.ProjectID))
			if err != nil || resource == "" {
				continue
			}
			target := scopeTarget{Resource: resource, Organization: strings.TrimSpace(scope.Organization), Project: strings.TrimSpace(scope.ProjectID), Bucket: bucket}
			if scopeBucket, prefix, ok := parseScopePath(scope.PathPrefix); ok {
				if scopeBucket != "" {
					target.Bucket = scopeBucket
				}
				target.Prefix = prefix
			} else {
				target.Prefix = strings.Trim(strings.TrimSpace(scope.PathPrefix), "/")
			}
			out[resource] = append(out[resource], target)
		}
	}
	return out, nil
}

func scopeBelongsToCredential(scope buckets.Scope, credential buckets.Credential) bool {
	for _, value := range []string{scope.CredentialID, scope.Bucket} {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if strings.EqualFold(value, strings.TrimSpace(credential.CredentialID)) || strings.EqualFold(value, strings.TrimSpace(credential.Bucket)) {
			return true
		}
	}
	return strings.TrimSpace(scope.CredentialID) == "" && strings.TrimSpace(scope.Bucket) == ""
}

func parseScopePath(raw string) (string, string, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", "", false
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme == "" {
		return "", "", false
	}
	if address.ProviderFromScheme(parsed.Scheme) != address.S3Provider {
		return strings.TrimSpace(parsed.Host), strings.Trim(strings.TrimSpace(parsed.Path), "/"), true
	}
	return strings.TrimSpace(parsed.Host), strings.Trim(strings.TrimSpace(parsed.Path), "/"), true
}

func inferRecordResource(record objects.Record, sha string, scopes map[string][]scopeTarget) (string, bool, bool) {
	resources := recordProjectResources(record, "")
	if len(resources) == 1 {
		resource := resources[0]
		if len(scopes[resource]) == 0 {
			return resource, false, false
		}
		return resource, true, false
	}
	if len(resources) > 1 {
		return "", false, true
	}
	if strings.TrimSpace(sha) == "" {
		return "", false, false
	}
	matches := make([]string, 0)
	for resource, targets := range scopes {
		if len(targets) == 0 || strings.TrimSpace(targets[0].Project) == "" {
			continue
		}
		minted, err := objects.MintRecordIDFromChecksum(sha, []string{resource})
		if err == nil && minted == record.Id {
			matches = append(matches, resource)
		}
	}
	if len(matches) == 1 {
		return matches[0], true, false
	}
	if len(matches) > 1 {
		return "", false, true
	}
	return "", false, false
}

func recordProjectResources(record objects.Record, inferred string) []string {
	seen := make(map[string]struct{})
	result := make([]string, 0)
	for _, resource := range objects.AccessResources(&record) {
		org, project, ok := syfoncommon.ResourceScope(resource)
		if !ok || org == "" || project == "" {
			continue
		}
		if _, exists := seen[resource]; exists {
			continue
		}
		seen[resource] = struct{}{}
		result = append(result, resource)
	}
	if inferred != "" {
		if _, exists := seen[inferred]; !exists {
			result = append(result, inferred)
		}
	}
	sort.Strings(result)
	return result
}

func recordMatchesResource(record objects.Record, resource string) bool {
	for _, candidate := range objects.AccessResources(&record) {
		if candidate == resource {
			return true
		}
	}
	return false
}

func (s *Service) addDuplicateFindings(objectsToAudit []*auditedObject) {
	byKey := make(map[string][]*auditedObject)
	for _, object := range objectsToAudit {
		if object.sha256 == "" {
			continue
		}
		for _, resource := range recordProjectResources(object.record, object.inferredScope) {
			key := resource + "|" + object.sha256
			byKey[key] = append(byKey[key], object)
		}
	}
	for key, group := range byKey {
		if len(group) < 2 {
			continue
		}
		resource := strings.SplitN(key, "|", 2)[0]
		organization, project, _ := syfoncommon.ResourceScope(resource)
		for _, object := range group {
			object.findings = append(object.findings, newFinding(FindingDuplicateSHA256Sibling, SeverityWarn, object.record, object.sha256, object.currentURLs, object.canonicalURL, false, "same sha256 appears in multiple DIDs for this scope"))
			if object.scope.Organization == "" {
				object.scope.Organization = organization
				object.scope.Project = project
			}
		}
	}
}

func accessMethodURLs(methods *[]objects.AccessMethod) []string {
	if methods == nil {
		return nil
	}
	result := make([]string, 0, len(*methods))
	for _, method := range *methods {
		if raw := accessMethodURL(method); raw != "" {
			result = append(result, raw)
		}
	}
	return result
}

func accessMethodURL(method objects.AccessMethod) string {
	if method.AccessUrl == nil {
		return ""
	}
	return strings.TrimSpace(method.AccessUrl.Url)
}

func setAccessMethodURL(method *objects.AccessMethod, raw string) {
	if method.AccessUrl == nil {
		method.AccessUrl = &objects.AccessURL{}
	}
	method.AccessUrl.Url = raw
}

func cloneAccessMethods(input []objects.AccessMethod) []objects.AccessMethod {
	output := make([]objects.AccessMethod, len(input))
	for index, method := range input {
		output[index] = method
		if method.AccessUrl != nil {
			accessURL := *method.AccessUrl
			if method.AccessUrl.Headers != nil {
				headers := append([]string(nil), (*method.AccessUrl.Headers)...)
				accessURL.Headers = &headers
			}
			output[index].AccessUrl = &accessURL
		}
	}
	return output
}

func cloneRecord(record objects.Record) objects.Record {
	result := record
	if record.AccessMethods != nil {
		methods := cloneAccessMethods(*record.AccessMethods)
		result.AccessMethods = &methods
	}
	if record.ControlledAccess != nil {
		controlled := append([]string(nil), (*record.ControlledAccess)...)
		result.ControlledAccess = &controlled
	}
	if record.Aliases != nil {
		aliases := append([]string(nil), (*record.Aliases)...)
		result.Aliases = &aliases
	}
	result.Checksums = append([]objects.Checksum(nil), record.Checksums...)
	result.NameAliases = append([]string(nil), record.NameAliases...)
	if record.Authorizations != nil {
		result.Authorizations = make(map[string][]string, len(record.Authorizations))
		for key, values := range record.Authorizations {
			result.Authorizations[key] = append([]string(nil), values...)
		}
	}
	if record.Properties != nil {
		result.Properties = make(map[string]json.RawMessage, len(record.Properties))
		for key, value := range record.Properties {
			result.Properties[key] = append(json.RawMessage(nil), value...)
		}
	}
	return result
}

func addControlledAccess(controlled *[]string, resource string) *[]string {
	values := make([]string, 0)
	if controlled != nil {
		values = append(values, (*controlled)...)
	}
	values = append(values, resource)
	normalized := syfoncommon.NormalizeAccessResources(values)
	return &normalized
}

func objectName(record objects.Record) string {
	if record.Name == nil {
		return ""
	}
	return strings.Trim(strings.TrimSpace(*record.Name), "/")
}

func canonicalAccessURL(target scopeTarget, did, sha string) string {
	if strings.TrimSpace(target.Bucket) == "" || strings.TrimSpace(did) == "" || strings.TrimSpace(sha) == "" {
		return ""
	}
	parts := make([]string, 0, 3)
	if prefix := strings.Trim(target.Prefix, "/"); prefix != "" {
		parts = append(parts, prefix)
	}
	parts = append(parts, strings.TrimSpace(did), strings.TrimSpace(sha))
	return "s3://" + strings.TrimSpace(target.Bucket) + "/" + strings.Join(parts, "/")
}

func pathStyleAccessURL(target scopeTarget, name string) string {
	if strings.TrimSpace(target.Bucket) == "" || strings.TrimSpace(name) == "" {
		return ""
	}
	parts := make([]string, 0, 2)
	if prefix := strings.Trim(target.Prefix, "/"); prefix != "" {
		parts = append(parts, prefix)
	}
	parts = append(parts, strings.Trim(name, "/"))
	return "s3://" + strings.TrimSpace(target.Bucket) + "/" + strings.Join(parts, "/")
}

func newFinding(kind FindingKind, severity Severity, record objects.Record, sha string, currentURLs []string, canonical string, autoFixable bool, message string) Finding {
	finding := Finding{Kind: kind, Severity: severity, ObjectID: string(record.Id), SHA256: sha, CurrentAccessURLs: append([]string(nil), currentURLs...), ProposedCanonicalURL: canonical, AutoFixable: autoFixable, Message: message}
	for _, resource := range objects.AccessResources(&record) {
		organization, project, ok := syfoncommon.ResourceScope(resource)
		if ok && organization != "" {
			finding.Organization = organization
			finding.Project = project
			break
		}
	}
	return finding
}
