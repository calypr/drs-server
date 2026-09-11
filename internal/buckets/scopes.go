package buckets

import (
	"context"
	"errors"
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

// DeleteBucketScope deletes the requested scope and removes its credential
// after the last scope is gone.
func (s *Service) DeleteBucketScope(ctx context.Context, organization, projectID, credentialID, pathPrefix string) error {
	organization = strings.TrimSpace(organization)
	projectID = strings.TrimSpace(projectID)
	credentialID = strings.TrimSpace(credentialID)
	pathPrefix = strings.Trim(strings.TrimSpace(pathPrefix), "/")
	resolvedID := strings.TrimSpace(credentialID)
	resolvedBucket := ""
	cred, lookupErr := s.GetS3Credential(ctx, credentialID)
	if lookupErr != nil && !errors.Is(lookupErr, errorapi.ErrStorageCredentialMissing) {
		return lookupErr
	}
	if lookupErr == nil && cred != nil {
		resolvedID = s.credentialIDForCredential(*cred)
		resolvedBucket = strings.TrimSpace(cred.Bucket)
	}

	scopes, err := s.ListBucketScopes(ctx)
	if err != nil {
		return err
	}
	remaining := false
	for _, scope := range scopes {
		if strings.TrimSpace(scope.Organization) == organization &&
			strings.TrimSpace(scope.ProjectID) == projectID &&
			strings.Trim(strings.TrimSpace(scope.PathPrefix), "/") == pathPrefix &&
			s.scopeBelongsTo(scope, resolvedID, resolvedBucket) {
			continue
		}
		if s.scopeBelongsTo(scope, resolvedID, resolvedBucket) {
			remaining = true
			break
		}
	}
	if err := s.scopeStore.DeleteBucketScope(ctx, organization, projectID, credentialID, pathPrefix); err != nil {
		return err
	}
	if remaining {
		return nil
	}
	if err := s.deleteS3Credential(ctx, resolvedID, cred); err != nil && !errors.Is(err, errorapi.ErrStorageCredentialMissing) {
		return err
	}
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
