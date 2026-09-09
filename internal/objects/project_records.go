package objects

import (
	"context"
	"strings"

	"github.com/calypr/syfon/apigen/errorapi"
	clientaccess "github.com/calypr/syfon/client/access"
	"github.com/calypr/syfon/internal/access"
	"github.com/calypr/syfon/internal/storage/address"
)

// ProjectRecordAuditQuery selects the physical record rows exposed by the
// project-record inspection operation.
type ProjectRecordAuditQuery struct {
	Organization string
	Project      string
	PathPrefix   string
}

// PathPrefixResolver is the narrow storage-scope capability required to
// translate a logical request prefix into its configured physical prefix.
// The audit service owns when this capability is consulted after record
// authorization; the resolver only owns scope lookup and composition.
type PathPrefixResolver interface {
	ResolvePathPrefix(context.Context, string, string, string) (string, error)
}

// AuditProjectRecords projects each stored record row in a scope. Unlike
// prepared inventory reads, it keeps same-checksum physical duplicates visible
// and emits only records carrying a primary SHA-256 checksum.
func (s *Service) AuditProjectRecords(ctx context.Context, query ProjectRecordAuditQuery) ([]Record, error) {
	organization := strings.TrimSpace(query.Organization)
	project := strings.TrimSpace(query.Project)
	if organization == "" || project == "" {
		return nil, errorapi.Define(errorapi.ErrorCodeInvalidInput, errorapi.ErrorCategoryInvalidInput, "organization and project are required")
	}
	if s == nil || s.store == nil {
		return nil, errorapi.Define(errorapi.ErrorCodeStorageUnsupported, errorapi.ErrorCategoryInvalidInput, "object store is not configured")
	}
	records, err := s.ListPhysicalObjectsByScope(ctx, organization, project, objectMethodRead)
	if err != nil {
		return nil, err
	}
	prefixes := make([]string, 0, 2)
	if prefix := strings.Trim(strings.TrimSpace(query.PathPrefix), "/"); prefix != "" {
		prefixes = append(prefixes, prefix)
		if s.resolver != nil && canResolvePathPrefix(ctx, organization, project) {
			if resolved, resolveErr := s.resolver.ResolvePathPrefix(ctx, organization, project, prefix); resolveErr == nil && resolved != "" && !strings.EqualFold(resolved, prefix) {
				prefixes = append(prefixes, resolved)
			}
		}
	}
	result := make([]Record, 0, len(records))
	for _, record := range records {
		if _, ok := CanonicalSHA256(record.Checksums); !ok || (len(prefixes) > 0 && !projectRecordMatchesPrefix(record, prefixes...)) {
			continue
		}
		result = append(result, record)
	}
	return result, nil
}

func canResolvePathPrefix(ctx context.Context, organization, project string) bool {
	if !access.IsAuthzEnforced(ctx) {
		return true
	}
	resource, err := clientaccess.ResourcePath(organization, project)
	return err == nil && access.HasMethodAccess(ctx, objectMethodRead, []string{resource})
}

func projectRecordMatchesPrefix(record Record, prefixes ...string) bool {
	for _, rawPrefix := range prefixes {
		prefix := strings.Trim(strings.TrimSpace(rawPrefix), "/")
		if prefix == "" {
			return true
		}
		if record.AccessMethods != nil {
			for _, method := range *record.AccessMethods {
				if method.AccessUrl == nil {
					continue
				}
				_, key, ok := address.ParseS3URL(method.AccessUrl.Url)
				if ok && storageKeyWithinPrefix(key, prefix) {
					return true
				}
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
