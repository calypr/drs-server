package storage

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/calypr/syfon/internal/storage"
	"github.com/calypr/syfon/internal/storage/address"
)

const deleteMethod = "delete"

// DeleteProjectObjects authorizes each URL against the resolved S3 scope,
// deduplicates in first-seen order, and dispatches exact physical URLs one at
// a time. This preserves per-item provider failures and retry ordering.
func (s *ProjectCleanup) DeleteProjectObjects(ctx context.Context, organization, project string, objectURLs []string) []DeleteResult {
	ctx = withRequestCache(ctx)
	unique := uniqueURLs(objectURLs)
	if len(unique) == 0 {
		return []DeleteResult{}
	}
	target, err := s.inspector.resolveScope(ctx, organization, project, deleteMethod)
	if err != nil {
		results := make([]DeleteResult, 0, len(unique))
		for _, objectURL := range unique {
			results = append(results, DeleteResult{ObjectURL: objectURL, Status: "error", Error: err.Error()})
		}
		return results
	}
	results := make([]DeleteResult, 0, len(unique))
	for _, objectURL := range unique {
		result := DeleteResult{ObjectURL: objectURL, Status: "deleted"}
		candidate, parseStatus, parseErr := parseDeleteURL(ctx, s.inspector, objectURL)
		switch {
		case parseErr != nil:
			result.Status = "error"
			result.Error = parseErr.Error()
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
				result.Error = mapStorageDeleteError(deleteErr).Error()
			}
		}
		results = append(results, result)
	}
	return results
}

// DeleteProjectData performs the project cleanup sequence: catalog objects
// are removed first, then matching bucket scopes are listed and deleted in
// repository order. A scope count includes only successful deletions.
func (s *ProjectCleanup) DeleteProjectData(ctx context.Context, organization, project string) (ProjectCleanupResult, error) {
	result := ProjectCleanupResult{Organization: strings.TrimSpace(organization), ProjectID: strings.TrimSpace(project)}
	if s.cleanupObjects == nil || s.cleanupScopes == nil {
		return result, &Error{Kind: ErrorUnsupported, Message: "project cleanup dependencies are not configured"}
	}
	deletedObjects, err := s.cleanupObjects.DeleteBulkByScope(ctx, result.Organization, result.ProjectID)
	if err != nil {
		return result, err
	}
	result.DeletedObjects = deletedObjects
	scopes, err := s.cleanupScopes.ListBucketScopes(ctx)
	if err != nil {
		return result, err
	}
	for _, scope := range scopes {
		if strings.TrimSpace(scope.Organization) != result.Organization || strings.TrimSpace(scope.ProjectID) != result.ProjectID {
			continue
		}
		credentialID := strings.TrimSpace(scope.CredentialID)
		if credentialID == "" {
			credentialID = strings.TrimSpace(scope.Bucket)
		}
		if credentialID == "" {
			continue
		}
		if err := s.cleanupScopes.DeleteBucketScope(ctx, result.Organization, result.ProjectID, credentialID, scope.PathPrefix); err != nil {
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

func parseDeleteURL(ctx context.Context, service *Inspector, raw string) (deleteCandidate, string, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return deleteCandidate{}, "", fmt.Errorf("parse access url %q: %w", raw, err)
	}
	provider := address.ProviderFromScheme(parsed.Scheme)
	if provider == "" {
		return deleteCandidate{}, "invalid", nil
	}
	bucket := strings.TrimSpace(parsed.Host)
	key := strings.Trim(strings.TrimSpace(parsed.Path), "/")
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

func mapStorageDeleteError(err error) error {
	var operation *storage.OperationError
	if errors.As(err, &operation) && operation.Cause != nil {
		return operation.Cause
	}
	return err
}
