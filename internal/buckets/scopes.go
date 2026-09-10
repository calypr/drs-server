package buckets

import (
	"context"
	"strings"

	"github.com/calypr/syfon/apigen/errorapi"
)

// ListBucketScopes delegates scope enumeration without changing repository
// order or adapter-owned normalization.
func (s *Service) ListBucketScopes(ctx context.Context) ([]Scope, error) {
	return s.scopeStore.ListBucketScopes(ctx)
}

// CreateBucketScope persists a scope.
func (s *Service) CreateBucketScope(ctx context.Context, scope *Scope) error {
	return s.scopeStore.CreateBucketScope(ctx, scope)
}

// DeleteBucketScope deletes the requested scope and preserves the existing
// last-scope credential cleanup policy.
func (s *Service) DeleteBucketScope(ctx context.Context, organization, projectID, credentialID, pathPrefix string) error {
	if err := s.scopeStore.DeleteBucketScope(ctx, organization, projectID, credentialID, pathPrefix); err != nil {
		return err
	}

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

// LookupBucketScope returns a normalized scope. Not-found errors are represented
// as an absent scope so callers can compose organization and project scopes.
func (s *Service) LookupBucketScope(ctx context.Context, organization, project string) (Scope, bool, error) {
	scope, err := s.scopeStore.GetBucketScope(ctx, organization, project)
	if err != nil {
		if errorapi.IsNotFoundError(err) {
			return Scope{}, false, nil
		}
		return Scope{}, false, err
	}
	if scope == nil {
		return Scope{}, false, nil
	}

	return normalizeScope(scope), true, nil
}

func normalizeScope(scope *Scope) Scope {
	if scope == nil {
		return Scope{}
	}
	return Scope{
		Organization: strings.TrimSpace(scope.Organization),
		ProjectID:    strings.TrimSpace(scope.ProjectID),
		CredentialID: strings.TrimSpace(scope.CredentialID),
		Bucket:       strings.TrimSpace(scope.Bucket),
		PathPrefix:   strings.Trim(strings.TrimSpace(scope.PathPrefix), "/"),
	}
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
