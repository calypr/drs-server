package storage

import (
	"context"
	"fmt"
	"strings"

	internalapi "github.com/calypr/syfon/apigen/internalapi"
	"github.com/calypr/syfon/internal/access"
	"github.com/calypr/syfon/internal/storage"
	"github.com/calypr/syfon/internal/storage/address"
)

const deleteMethod = "delete"

// DeleteProjectObjects authorizes each URL against the resolved S3 scope,
// deduplicates in first-seen order, and dispatches exact physical URLs one at
// a time. This preserves per-item provider failures and retry ordering.
func (s *Service) DeleteProjectObjects(ctx context.Context, organization, project string, objectURLs []string) []internalapi.InternalDeleteProjectBucketObjectsItem {
	ctx = withRequestCache(ctx)
	unique := uniqueURLs(objectURLs)
	if len(unique) == 0 {
		return []internalapi.InternalDeleteProjectBucketObjectsItem{}
	}
	target, err := s.resolveScope(ctx, organization, project, deleteMethod)
	if err != nil {
		results := make([]internalapi.InternalDeleteProjectBucketObjectsItem, 0, len(unique))
		logStorageDiagnostic(ctx, err, "delete")
		message := safeStorageErrorMessage(err, "delete")
		for _, objectURL := range unique {
			results = append(results, internalapi.InternalDeleteProjectBucketObjectsItem{ObjectUrl: objectURL, Status: "error", Error: message})
		}
		return results
	}
	results := make([]internalapi.InternalDeleteProjectBucketObjectsItem, 0, len(unique))
	for _, objectURL := range unique {
		result := internalapi.InternalDeleteProjectBucketObjectsItem{ObjectUrl: objectURL, Status: "deleted"}
		candidate, parseStatus, parseErr := parseDeleteURL(ctx, s, objectURL)
		switch {
		case parseErr != nil:
			result.Status = "error"
			logStorageDiagnostic(ctx, parseErr, "delete")
			result.Error = safeStorageErrorMessage(parseErr, "delete")
		case parseStatus == "invalid":
			result.Status = "invalid"
			result.Error = "object_url must resolve to a deletable storage target"
		case !targetAllowed(candidate, target):
			result.Status = "forbidden"
			result.Error = fmt.Sprintf("object_url %q is outside configured project bucket scope", objectURL)
		default:
			if s.delete == nil {
				result.Status = "error"
				result.Error = "storage deletion is not configured"
			} else if deleteErr := s.delete.DeleteExact(ctx, []storage.DeleteTarget{{Location: objectURL}}); deleteErr != nil {
				result.Status = "error"
				logStorageDiagnostic(ctx, deleteErr, "delete")
				result.Error = safeStorageErrorMessage(deleteErr, "delete")
			}
		}
		results = append(results, result)
	}
	return results
}

// DeleteProjectDataAuthorized applies the maintenance write policy before
// entering the trusted cleanup sequence.
func (s *Service) DeleteProjectDataAuthorized(ctx context.Context, organization, project string) (internalapi.ProjectCleanupResponse, error) {
	result := internalapi.ProjectCleanupResponse{Organization: strings.TrimSpace(organization), ProjectId: strings.TrimSpace(project)}
	if err := access.AuthorizeScopeWrite(ctx, result.Organization, result.ProjectId, "delete", "update"); err != nil {
		return result, err
	}
	return s.deleteProjectData(ctx, result.Organization, result.ProjectId)
}

func (s *Service) deleteProjectData(ctx context.Context, organization, project string) (internalapi.ProjectCleanupResponse, error) {
	result := internalapi.ProjectCleanupResponse{Organization: organization, ProjectId: project}
	if s.cleanupObjects == nil || s.cleanupScopes == nil {
		return result, &Error{Kind: ErrorUnsupported, Message: "project cleanup dependencies are not configured"}
	}
	deletedObjects, err := s.cleanupObjects.DeleteBulkByScope(ctx, result.Organization, result.ProjectId)
	if err != nil {
		return result, err
	}
	result.DeletedObjects = deletedObjects
	scopes, err := s.cleanupScopes.ListBucketScopes(ctx)
	if err != nil {
		return result, err
	}
	for _, scope := range scopes {
		if strings.TrimSpace(scope.Organization) != result.Organization || strings.TrimSpace(scope.ProjectID) != result.ProjectId {
			continue
		}
		credentialID := strings.TrimSpace(scope.CredentialID)
		if credentialID == "" {
			credentialID = strings.TrimSpace(scope.Bucket)
		}
		if credentialID == "" {
			continue
		}
		if err := s.cleanupScopes.DeleteBucketScope(ctx, result.Organization, result.ProjectId, credentialID, scope.PathPrefix); err != nil {
			return result, err
		}
		result.DeletedBucketScopes++
	}
	return result, nil
}

type deleteCandidate struct {
	provider string
	bucket   string
	key      string
}

func parseDeleteURL(ctx context.Context, service *Service, raw string) (deleteCandidate, string, error) {
	parsed, err := address.ParseLocation(strings.TrimSpace(raw))
	if err != nil {
		return deleteCandidate{}, "", fmt.Errorf("parse access url %q: %w", raw, err)
	}
	provider := parsed.Provider
	if provider == "" {
		return deleteCandidate{}, "invalid", nil
	}
	bucket := strings.TrimSpace(parsed.Bucket)
	key := strings.Trim(strings.TrimSpace(parsed.Key), "/")
	if bucket == "" || key == "" {
		return deleteCandidate{}, "invalid", nil
	}
	credential, err := service.credentialForBucket(ctx, bucket)
	if err != nil {
		return deleteCandidate{}, "", fmt.Errorf("lookup credential for bucket %s: %w", bucket, err)
	}
	if provider == address.S3Provider {
		provider = address.NormalizeProvider(credential.Provider, provider)
	}
	return deleteCandidate{provider: provider, bucket: bucket, key: key}, "valid", nil
}

func targetAllowed(candidate deleteCandidate, target scopeTarget) bool {
	if address.NormalizeProvider(candidate.provider, address.S3Provider) != address.S3Provider || !strings.EqualFold(candidate.bucket, target.Bucket) {
		return false
	}
	key := strings.Trim(strings.TrimSpace(candidate.key), "/")
	prefix := strings.Trim(strings.TrimSpace(target.Prefix), "/")
	if prefix == "" {
		return key != ""
	}
	return key == prefix || strings.HasPrefix(key, prefix+"/")
}

func uniqueURLs(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}
