package buckets

import (
	"context"
	"fmt"
	"strings"

	"github.com/calypr/syfon/apigen/errorapi"
	clientaccess "github.com/calypr/syfon/client/access"
	"github.com/calypr/syfon/internal/access"
	"github.com/calypr/syfon/internal/storage/address"
)

// PutRequest contains the optional credential fields accepted by the bucket
// write endpoint. Nil fields inherit an existing credential when one is
// selected by its physical bucket or derived credential ID.
type PutRequest struct {
	Bucket       string
	Organization string
	ProjectID    string
	Provider     *string
	Region       *string
	AccessKey    *string
	SecretKey    *string
	Endpoint     *string
	Path         *string
}

// Put applies bucket credential and optional scope policy.
func (s *Service) Put(ctx context.Context, request PutRequest) error {
	bucket := strings.TrimSpace(request.Bucket)
	organization := strings.TrimSpace(request.Organization)
	projectID := strings.TrimSpace(request.ProjectID)
	if bucket == "" {
		return fmt.Errorf("bucket is required")
	}
	if organization == "" && projectID != "" {
		return fmt.Errorf("organization is required when project_id is set")
	}

	providerInput := stringValue(request.Provider)
	provider, err := address.ParseBucketProvider(providerInput)
	if err != nil {
		return invalidInput("provider must be one of: s3, gcs, azure")
	}
	region := stringValue(request.Region)
	accessKey := stringValue(request.AccessKey)
	secretKey := stringValue(request.SecretKey)
	endpoint := stringValue(request.Endpoint)

	if err := access.AuthorizeScopeWrite(ctx, organization, projectID, "create", "update"); err != nil {
		return err
	}
	prefix, err := address.NormalizeStoragePath(stringValue(request.Path), bucket)
	if err != nil {
		return invalidInput(err.Error())
	}

	existing, lookupErr := s.GetS3Credential(ctx, bucket)
	if lookupErr != nil && !isCredentialNotFoundError(lookupErr) {
		return lookupErr
	}
	credentialID := ""
	if lookupErr == nil && existing != nil {
		credentialID = existing.CredentialID
	}
	if credentialID == "" {
		credentialID = DeriveCredentialID(bucket, provider, region, endpoint, accessKey)
	}
	if existing == nil {
		existing, lookupErr = s.GetS3Credential(ctx, credentialID)
		if lookupErr != nil && !isCredentialNotFoundError(lookupErr) {
			return lookupErr
		}
	}
	hasExisting := existing != nil
	if hasExisting && request.Provider == nil {
		provider = address.NormalizeProvider(existing.Provider, address.S3Provider)
	}

	if !hasExisting && provider == address.S3Provider && (accessKey == "" || secretKey == "") {
		return invalidInput("access_key and secret_key are required for new s3 credentials")
	}

	scopeOnly := hasExisting && request.Provider == nil && request.Region == nil &&
		request.AccessKey == nil && request.SecretKey == nil && request.Endpoint == nil &&
		organization != ""
	if scopeOnly {
		return s.CreateBucketScope(ctx, &Scope{
			Organization: organization,
			ProjectID:    projectID,
			CredentialID: credentialID,
			Bucket:       bucket,
			PathPrefix:   prefix,
		})
	}

	if hasExisting {
		if request.Region == nil {
			region = existing.Region
		}
		if request.AccessKey == nil {
			accessKey = existing.AccessKey
		}
		if request.SecretKey == nil {
			secretKey = existing.SecretKey
		}
		if request.Endpoint == nil {
			endpoint = existing.Endpoint
		}
	}
	if err := address.ValidateBucketNameWithEndpoint(provider, bucket, endpoint); err != nil {
		return invalidInput(err.Error())
	}
	if provider == address.S3Provider && (strings.TrimSpace(accessKey) == "" || strings.TrimSpace(secretKey) == "") {
		return invalidInput("access_key and secret_key are required for s3 credentials")
	}

	credential := &Credential{
		CredentialID: credentialID,
		Bucket:       bucket,
		Provider:     provider,
		Region:       region,
		AccessKey:    accessKey,
		SecretKey:    secretKey,
		Endpoint:     endpoint,
	}
	if organization != "" {
		if err := s.credentialAdmin.SaveBucketConfiguration(ctx, BucketConfiguration{
			Credential:   *credential,
			Organization: organization,
			ProjectID:    projectID,
			PathPrefix:   prefix,
		}); err != nil {
			return err
		}
		s.invalidateCredentialAliases(credential)
		return nil
	}
	return s.SaveS3Credential(ctx, credential)
}

// DeleteBucket authorizes deletion against the physical bucket name before
// delegating alias resolution and credential cleanup to the credential policy.
func (s *Service) DeleteBucket(ctx context.Context, bucket string) error {
	scopes, err := s.ListBucketScopes(ctx)
	if err != nil {
		return err
	}
	for _, scope := range scopes {
		if scope.Bucket != bucket {
			continue
		}
		resource, resourceErr := clientaccess.ResourcePath(scope.Organization, scope.ProjectID)
		if resourceErr == nil && resource != "" && access.HasAnyMethodAccess(ctx, []string{resource}, "delete", "update") {
			return s.DeleteS3Credential(ctx, bucket)
		}
	}
	return errorapi.ErrAccessDenied
}

// CreateScopeForBucket resolves a physical or credential alias, normalizes its
// path against the physical bucket, applies scope-write authorization, and
// persists the canonical scope.
func (s *Service) CreateScopeForBucket(ctx context.Context, credentialID, organization, projectID, path string) error {
	credentialID = strings.TrimSpace(credentialID)
	organization = strings.TrimSpace(organization)
	projectID = strings.TrimSpace(projectID)
	credential, err := s.GetS3Credential(ctx, credentialID)
	if err != nil {
		return err
	}
	if credential == nil {
		return errorapi.ErrStorageCredentialMissing
	}
	if err := access.AuthorizeScopeWrite(ctx, organization, projectID, "create", "update"); err != nil {
		return err
	}
	prefix, err := address.NormalizeStoragePath(strings.TrimSpace(path), credential.Bucket)
	if err != nil {
		return invalidInput(err.Error())
	}
	return s.CreateBucketScope(ctx, &Scope{
		Organization: organization,
		ProjectID:    projectID,
		CredentialID: credential.CredentialID,
		Bucket:       credential.Bucket,
		PathPrefix:   prefix,
	})
}

// DeleteScope selects exactly one matching scope before delegating the
// destructive repository operation. Alias matching is case-insensitive while
// organization, project, and normalized path retain their exact semantics.
func (s *Service) DeleteScope(ctx context.Context, routeCredentialID, organization, projectID, pathPrefix string) error {
	routeCredentialID = strings.TrimSpace(routeCredentialID)
	organization = strings.TrimSpace(organization)
	projectID = strings.TrimSpace(projectID)
	pathPrefix = strings.TrimSpace(pathPrefix)
	if err := access.AuthorizeScopeWrite(ctx, organization, projectID, "delete", "update"); err != nil {
		return err
	}
	if pathPrefix != "" {
		credential, err := s.GetS3Credential(ctx, routeCredentialID)
		if err != nil {
			return err
		}
		if credential == nil {
			return errorapi.ErrStorageCredentialMissing
		}
		pathPrefix, err = address.NormalizeStoragePath(pathPrefix, credential.Bucket)
		if err != nil {
			return err
		}
	}

	scopes, err := s.ListBucketScopes(ctx)
	if err != nil {
		return err
	}
	matchCount := 0
	var matched Scope
	for _, scope := range scopes {
		if !(strings.EqualFold(strings.TrimSpace(scope.Bucket), routeCredentialID) || strings.EqualFold(strings.TrimSpace(scope.CredentialID), routeCredentialID)) {
			continue
		}
		if strings.TrimSpace(scope.Organization) != organization || strings.TrimSpace(scope.ProjectID) != projectID {
			continue
		}
		if strings.Trim(strings.TrimSpace(scope.PathPrefix), "/") != pathPrefix {
			continue
		}
		matchCount++
		matched = scope
	}
	if matchCount == 0 {
		return errorapi.ErrBucketScopeNotFound
	}
	if matchCount > 1 {
		return fmt.Errorf("%w: bucket scope delete matched multiple rows", errorapi.ErrConflict)
	}
	deleteCredentialID := strings.TrimSpace(matched.CredentialID)
	if deleteCredentialID == "" {
		deleteCredentialID = strings.TrimSpace(matched.Bucket)
	}
	return s.DeleteBucketScope(ctx, organization, projectID, deleteCredentialID, pathPrefix)
}

type VisibleScope struct {
	Organization string
	ProjectID    string
	Path         string
}

type VisibleProjectScope struct {
	Bucket       string
	Organization string
	ProjectID    string
	Path         string
}

// ListVisibleScopes returns authorized scopes matching a bucket or credential
// alias. Provider lookup intentionally uses the canonical credential ID only;
// an absent credential preserves the historical s3 URL fallback.
func (s *Service) ListVisibleScopes(ctx context.Context, routeCredentialID string) ([]VisibleScope, error) {
	scopes, err := s.ListBucketScopes(ctx)
	if err != nil {
		return nil, err
	}
	visible := make([]VisibleScope, 0)
	for _, scope := range scopes {
		if !strings.EqualFold(strings.TrimSpace(scope.Bucket), strings.TrimSpace(routeCredentialID)) &&
			!strings.EqualFold(strings.TrimSpace(scope.CredentialID), strings.TrimSpace(routeCredentialID)) {
			continue
		}
		resource, resourceErr := clientaccess.ResourcePath(scope.Organization, scope.ProjectID)
		if resourceErr != nil || resource == "" || !access.HasAnyMethodAccess(ctx, []string{resource}, "read") {
			continue
		}
		visible = append(visible, VisibleScope{
			Organization: scope.Organization,
			ProjectID:    scope.ProjectID,
			Path:         s.scopeURL(ctx, scope),
		})
	}
	return visible, nil
}

// ListVisibleProjectScopes returns authorized scopes for a requested
// organization/project pair. Organization output follows the request, even
// when the stored scope uses different casing.
func (s *Service) ListVisibleProjectScopes(ctx context.Context, organization, project string) ([]VisibleProjectScope, error) {
	organization = strings.TrimSpace(organization)
	project = strings.TrimSpace(project)
	scopes, err := s.ListBucketScopes(ctx)
	if err != nil {
		return nil, err
	}
	visible := make([]VisibleProjectScope, 0)
	for _, scope := range scopes {
		if !strings.EqualFold(strings.TrimSpace(scope.Organization), organization) {
			continue
		}
		scopeProject := strings.TrimSpace(scope.ProjectID)
		if scopeProject != "" && !strings.EqualFold(scopeProject, project) {
			continue
		}
		resource, resourceErr := clientaccess.ResourcePath(scope.Organization, scope.ProjectID)
		if resourceErr != nil || resource == "" || !access.HasAnyMethodAccess(ctx, []string{resource}, "read") {
			continue
		}
		visible = append(visible, VisibleProjectScope{
			Bucket:       strings.TrimSpace(scope.Bucket),
			Organization: organization,
			ProjectID:    scopeProject,
			Path:         s.scopeURL(ctx, scope),
		})
	}
	return visible, nil
}

func (s *Service) scopeURL(ctx context.Context, scope Scope) string {
	scheme := "s3"
	if credential, err := s.credentialReader.GetS3Credential(ctx, scope.CredentialID); err == nil && credential != nil {
		scheme = address.ProviderToScheme(credential.Provider)
	}
	path := fmt.Sprintf("%s://%s", scheme, strings.TrimSpace(scope.Bucket))
	if prefix := strings.Trim(strings.TrimSpace(scope.PathPrefix), "/"); prefix != "" {
		path += "/" + prefix
	}
	return path
}

func invalidInput(message string) error {
	return fmt.Errorf("%w: %s", errorapi.ErrInvalidInput, message)
}

func stringValue(value *string) string {
	if value == nil {
		return ""
	}
	return strings.TrimSpace(*value)
}
