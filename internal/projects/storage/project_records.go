package storage

import (
	"context"
	"strings"

	"github.com/calypr/syfon/internal/objects"
	"github.com/calypr/syfon/internal/storage/address"
)

// AuditProjectRecords projects physical records for the project-record
// inspection route. Unlike prepared inventory reads, it intentionally keeps
// same-checksum physical duplicates visible and only emits records carrying a
// primary SHA-256 checksum.
func (s *Inspector) AuditProjectRecords(ctx context.Context, organization, project, requestPrefix string) ([]ProjectRecordAudit, error) {
	organization = strings.TrimSpace(organization)
	project = strings.TrimSpace(project)
	if organization == "" || project == "" {
		return nil, &Error{Kind: ErrorInvalidInput, Message: "organization and project are required"}
	}
	if s.physical == nil {
		return nil, &Error{Kind: ErrorUnsupported, Message: "physical scope reader is not configured"}
	}
	records, err := s.physical.ListPhysicalObjectsByScope(ctx, organization, project, readMethod)
	if err != nil {
		return nil, err
	}
	prefixes := make([]string, 0, 2)
	if prefix := strings.Trim(strings.TrimSpace(requestPrefix), "/"); prefix != "" {
		prefixes = append(prefixes, prefix)
		if resolved, resolveErr := s.ResolvePathPrefix(ctx, organization, project, prefix); resolveErr == nil && resolved != "" && !strings.EqualFold(resolved, prefix) {
			prefixes = append(prefixes, resolved)
		}
	}
	result := make([]ProjectRecordAudit, 0, len(records))
	for _, record := range records {
		item, ok := projectRecordFromRecord(record, organization, project)
		if !ok || (len(prefixes) > 0 && !projectRecordMatchesPrefix(item, prefixes...)) {
			continue
		}
		result = append(result, item)
	}
	return result, nil
}

func projectRecordFromRecord(record objects.Record, organization, project string) (ProjectRecordAudit, bool) {
	checksum, ok := objects.CanonicalSHA256(record.Checksums)
	if !ok || strings.TrimSpace(checksum) == "" {
		return ProjectRecordAudit{}, false
	}
	item := ProjectRecordAudit{ObjectID: string(record.Id), Checksum: checksum, Organization: organization, Project: project, Size: record.Size, CreatedTime: record.CreatedTime}
	if record.Name != nil {
		item.Name = strings.TrimSpace(*record.Name)
	}
	if record.UpdatedTime != nil {
		updated := *record.UpdatedTime
		item.UpdatedTime = &updated
	}
	if record.AccessMethods != nil {
		item.AccessMethods = make([]ProjectAccessMethod, 0, len(*record.AccessMethods))
		for _, method := range *record.AccessMethods {
			access := ProjectAccessMethod{Type: strings.TrimSpace(method.Type)}
			if method.AccessId != nil {
				access.AccessID = strings.TrimSpace(*method.AccessId)
			}
			if method.AccessUrl != nil {
				access.URL = strings.TrimSpace(method.AccessUrl.Url)
				if access.URL != "" {
					item.AccessURLs = append(item.AccessURLs, access.URL)
				}
				if method.AccessUrl.Headers != nil {
					access.Headers = append([]string(nil), (*method.AccessUrl.Headers)...)
				}
			}
			item.AccessMethods = append(item.AccessMethods, access)
		}
	}
	return item, true
}

func projectRecordMatchesPrefix(record ProjectRecordAudit, prefixes ...string) bool {
	for _, rawPrefix := range prefixes {
		prefix := strings.Trim(strings.TrimSpace(rawPrefix), "/")
		if prefix == "" {
			return true
		}
		for _, rawURL := range record.AccessURLs {
			_, key, ok := address.ParseS3URL(rawURL)
			if ok && storageKeyWithinPrefix(key, prefix) {
				return true
			}
		}
		for _, method := range record.AccessMethods {
			_, key, ok := address.ParseS3URL(method.URL)
			if ok && storageKeyWithinPrefix(key, prefix) {
				return true
			}
		}
	}
	return false
}

func storageKeyWithinPrefix(key, prefix string) bool {
	key = strings.Trim(strings.TrimSpace(key), "/")
	prefix = strings.Trim(strings.TrimSpace(prefix), "/")
	return key == prefix || strings.HasPrefix(key, prefix+"/")
}
