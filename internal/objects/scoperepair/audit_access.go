package scoperepair

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/calypr/syfon/apigen/errorapi"
	clientaccess "github.com/calypr/syfon/client/access"
	"github.com/calypr/syfon/internal/objects"
)

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
		if errors.Is(err, errorapi.ErrStorageNotFound) {
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
	return result
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
		organization, project, ok := clientaccess.ResourceScope(resource)
		if ok && organization != "" {
			finding.Organization = organization
			finding.Project = project
			break
		}
	}
	return finding
}
