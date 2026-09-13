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

// DeleteBucketScope deletes the requested scope and removes its credential
// after the last scope is gone.
func (s *Service) DeleteBucketScope(ctx context.Context, organization, projectID, credentialID, pathPrefix string) error {
	aliases, err := s.credentialAdmin.DeleteBucketScopeConfiguration(ctx, Scope{
		Organization: strings.TrimSpace(organization),
		ProjectID:    strings.TrimSpace(projectID),
		CredentialID: strings.TrimSpace(credentialID),
		PathPrefix:   strings.Trim(strings.TrimSpace(pathPrefix), "/"),
	})
	if err != nil {
		return err
	}
	s.invalidateAliases(aliases...)
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
