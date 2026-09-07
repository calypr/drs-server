package buckets

import (
	"context"
	"strings"

	"github.com/calypr/syfon/internal/faults"
)

// ListBucketScopes delegates scope enumeration without changing repository
// order or adapter-owned normalization.
func (s *Service) ListBucketScopes(ctx context.Context) ([]Scope, error) {
	return s.scopeStore.ListBucketScopes(ctx)
}

// CreateBucketScope persists a scope and only then seeds the positive cache.
func (s *Service) CreateBucketScope(ctx context.Context, scope *Scope) error {
	if err := s.scopeStore.CreateBucketScope(ctx, scope); err != nil {
		return err
	}
	s.scopeCache.set(normalizeScope(scope), true)
	return nil
}

// DeleteBucketScope deletes the requested scope, clears cached scope answers,
// and preserves the existing last-scope credential cleanup policy.
func (s *Service) DeleteBucketScope(ctx context.Context, organization, projectID, credentialID, pathPrefix string) error {
	if err := s.scopeStore.DeleteBucketScope(ctx, organization, projectID, credentialID, pathPrefix); err != nil {
		return err
	}
	s.scopeCache.clear()

	resolvedID := strings.TrimSpace(credentialID)
	resolvedBucket := ""
	if cred, err := s.GetS3Credential(ctx, credentialID); err == nil && cred != nil {
		resolvedID = s.credentialIDForCredential(*cred)
		resolvedBucket = strings.TrimSpace(cred.Bucket)
	}

	scopes, err := s.ListBucketScopes(ctx)
	if err != nil {
		return nil
	}
	for _, scope := range scopes {
		if s.scopeBelongsTo(scope, resolvedID, resolvedBucket) {
			return nil
		}
	}
	_ = s.DeleteS3Credential(ctx, resolvedID)
	return nil
}

// LookupBucketScope returns a normalized scope and caches both hits and
// not-found misses. Other backend errors are never cached.
func (s *Service) LookupBucketScope(ctx context.Context, organization, project string) (Scope, bool, error) {
	if scope, found, cached := s.scopeCache.get(organization, project); cached {
		return scope, found, nil
	}

	scope, err := s.scopeStore.GetBucketScope(ctx, organization, project)
	if err != nil {
		if faults.IsNotFoundError(err) {
			s.scopeCache.set(Scope{Organization: organization, ProjectID: project}, false)
			return Scope{}, false, nil
		}
		return Scope{}, false, err
	}
	if scope == nil {
		s.scopeCache.set(Scope{Organization: organization, ProjectID: project}, false)
		return Scope{}, false, nil
	}

	normalized := normalizeScope(scope)
	s.scopeCache.set(normalized, true)
	return normalized, true, nil
}

func (s *Service) credentialIDForScope(scope Scope) string {
	if credentialID := strings.TrimSpace(scope.CredentialID); credentialID != "" {
		return credentialID
	}
	return strings.TrimSpace(scope.Bucket)
}

func (s *Service) scopeCredentialIDForCredentials(scope Scope, creds []Credential) string {
	candidate := s.credentialIDForScope(scope)
	for _, cred := range creds {
		if strings.EqualFold(candidate, s.credentialIDForCredential(cred)) ||
			strings.EqualFold(candidate, strings.TrimSpace(cred.Bucket)) ||
			strings.EqualFold(candidate, strings.TrimSpace(cred.CredentialID)) {
			return s.credentialIDForCredential(cred)
		}
	}
	return candidate
}

func (s *Service) scopeBelongsTo(scope Scope, credentialID, bucket string) bool {
	for _, candidate := range []string{scope.CredentialID, scope.Bucket} {
		if strings.EqualFold(strings.TrimSpace(candidate), strings.TrimSpace(credentialID)) {
			return true
		}
		if bucket != "" && strings.EqualFold(strings.TrimSpace(candidate), bucket) {
			return true
		}
	}
	return false
}
