package storage

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/calypr/syfon/apigen/drs"
	"github.com/calypr/syfon/apigen/errorapi"
	internalapi "github.com/calypr/syfon/apigen/internalapi"
	clientaccess "github.com/calypr/syfon/client/access"
	"github.com/calypr/syfon/internal/objects"
)

func (s *Service) classifyAccessMethods(ctx context.Context, object *auditedObject, checkStorage bool) {
	if object.record.AccessMethods == nil {
		return
	}
	methods := cloneAccessMethods(*object.record.AccessMethods)
	name := ""
	if object.record.Name != nil {
		name = strings.Trim(strings.TrimSpace(*object.record.Name), "/")
	}
	pathStyleURL := pathStyleAccessURL(object.scope, name)
	targetURL := object.canonicalURL
	if checkStorage {
		canonicalExists := strings.TrimSpace(object.canonicalURL) != "" && s.inspectStorageURL(ctx, object.canonicalURL) == nil
		pathStyleExists := strings.TrimSpace(pathStyleURL) != "" && s.inspectStorageURL(ctx, pathStyleURL) == nil
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
		if methods[index].AccessUrl == nil {
			methods[index].AccessUrl = &drs.AccessURL{}
		}
		methods[index].AccessUrl.Url = targetURL
		changed = true
		hasTarget = true
	}
	if !changed {
		return
	}
	filtered := make([]drs.AccessMethod, 0, len(methods))
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
	}
	object.updated = &updated
}

func (s *Service) addStorageFindings(ctx context.Context, object *auditedObject) {
	if s.probe == nil {
		return
	}
	for _, raw := range object.currentURLs {
		err := s.inspectStorageURL(ctx, raw)
		if err == nil {
			continue
		}
		kind := FindingStorageProbeError
		severity := SeverityWarn
		message := err.Error()
		if errors.Is(err, errorapi.ErrStorageNotFound) {
			kind = FindingStorageObjectMissing
			severity = SeverityError
			message = "storage object not found"
		}
		object.findings = append(object.findings, newFinding(kind, severity, object.record, object.sha256, []string{raw}, object.canonicalURL, false, message))
	}
}

func (s *Service) inspectStorageURL(ctx context.Context, rawURL string) error {
	if s.probe == nil {
		return fmt.Errorf("storage inspector is not configured")
	}
	_, err := s.probeObject(ctx, InspectRequest{ObjectURL: strings.TrimSpace(rawURL)})
	if err == nil {
		return nil
	}
	var storageErr *Error
	if errors.As(err, &storageErr) && storageErr.Kind == ErrorObjectNotFound {
		return errorapi.ErrStorageNotFound
	}
	return err
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
		organization, project, _ := clientaccess.ResourceScope(resource)
		for _, object := range group {
			object.findings = append(object.findings, newFinding(FindingDuplicateSHA256Sibling, SeverityWarn, object.record, object.sha256, object.currentURLs, object.canonicalURL, false, "same sha256 appears in multiple DIDs for this scope"))
			if object.scope.Organization == "" {
				object.scope.Organization = organization
				object.scope.Project = project
			}
		}
	}
}

func accessMethodURLs(methods *[]drs.AccessMethod) []string {
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

func accessMethodURL(method drs.AccessMethod) string {
	if method.AccessUrl == nil {
		return ""
	}
	return strings.TrimSpace(method.AccessUrl.Url)
}

func cloneAccessMethods(input []drs.AccessMethod) []drs.AccessMethod {
	output := make([]drs.AccessMethod, len(input))
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

func cloneRecord(record drs.DrsObject) drs.DrsObject {
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
	result.Checksums = append([]drs.Checksum(nil), record.Checksums...)
	if record.NameAliases != nil {
		nameAliases := append([]string(nil), (*record.NameAliases)...)
		result.NameAliases = &nameAliases
	}
	return result
}

func canonicalAccessURL(target repairScopeTarget, did, sha string) string {
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

func pathStyleAccessURL(target repairScopeTarget, name string) string {
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

func newFinding(kind, severity string, record drs.DrsObject, sha string, currentURLs []string, canonical string, autoFixable bool, message string) internalapi.ScopeRepairFinding {
	finding := internalapi.ScopeRepairFinding{Kind: kind, Severity: severity, ObjectId: record.Id, Sha256: sha, CurrentAccessUrls: append([]string(nil), currentURLs...), ProposedCanonicalUrl: canonical, AutoFixable: autoFixable, Message: message}
	for _, resource := range objects.AccessResources(&record) {
		organization, project, ok := clientaccess.ResourceScope(resource)
		if ok && organization != "" {
			finding.Organization = organization
			finding.Project = project
			break
		}
	}
	return finding
}
