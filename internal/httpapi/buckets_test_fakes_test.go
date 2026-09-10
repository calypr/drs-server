package httpapi

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/calypr/syfon/apigen/errorapi"
	domainbuckets "github.com/calypr/syfon/internal/buckets"
)

type bucketTestStore struct {
	Credentials    map[string]domainbuckets.Credential
	BucketScopes   map[string]domainbuckets.Scope
	VisibilityRows []domainbuckets.VisibilityRow
}

func (f *bucketTestStore) GetS3Credential(_ context.Context, bucket string) (*domainbuckets.Credential, error) {
	if credential, ok := f.Credentials[bucket]; ok {
		copy := credential
		return &copy, nil
	}
	requested := strings.TrimSpace(bucket)
	for _, credential := range f.Credentials {
		if strings.EqualFold(strings.TrimSpace(credential.Bucket), requested) ||
			strings.EqualFold(strings.TrimSpace(credential.CredentialID), requested) {
			copy := credential
			return &copy, nil
		}
	}
	return nil, errorapi.ErrStorageCredentialMissing
}

func (f *bucketTestStore) ListS3Credentials(context.Context) ([]domainbuckets.Credential, error) {
	keys := make([]string, 0, len(f.Credentials))
	for key := range f.Credentials {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	credentials := make([]domainbuckets.Credential, 0, len(keys))
	for _, key := range keys {
		credentials = append(credentials, f.Credentials[key])
	}
	return credentials, nil
}

func (f *bucketTestStore) SaveS3Credential(_ context.Context, credential *domainbuckets.Credential) error {
	if credential == nil {
		return errors.New("credential is required")
	}
	if f.Credentials == nil {
		f.Credentials = make(map[string]domainbuckets.Credential)
	}
	key := strings.TrimSpace(credential.CredentialID)
	if key == "" {
		key = strings.TrimSpace(credential.Bucket)
	}
	f.Credentials[key] = *credential
	return nil
}

func (f *bucketTestStore) DeleteS3Credential(_ context.Context, bucket string) error {
	delete(f.Credentials, bucket)
	return nil
}

func (f *bucketTestStore) CreateBucketScope(_ context.Context, scope *domainbuckets.Scope) error {
	if scope == nil {
		return errors.New("scope is required")
	}
	if f.BucketScopes == nil {
		f.BucketScopes = make(map[string]domainbuckets.Scope)
	}
	f.BucketScopes[bucketTestScopeKey(scope.Organization, scope.ProjectID)] = *scope
	return nil
}

func (f *bucketTestStore) DeleteBucketScope(_ context.Context, organization, projectID, credentialID, pathPrefix string) error {
	key := bucketTestScopeKey(organization, projectID)
	scope, ok := f.BucketScopes[key]
	if !ok || (scope.CredentialID != credentialID && scope.Bucket != credentialID) ||
		strings.Trim(strings.TrimSpace(scope.PathPrefix), "/") != strings.Trim(strings.TrimSpace(pathPrefix), "/") {
		return fmt.Errorf("%w: bucket scope not found", errorapi.ErrNotFound)
	}
	delete(f.BucketScopes, key)
	return nil
}

func (f *bucketTestStore) GetBucketScope(_ context.Context, organization, projectID string) (*domainbuckets.Scope, error) {
	scope, ok := f.BucketScopes[bucketTestScopeKey(organization, projectID)]
	if !ok {
		return nil, fmt.Errorf("%w: bucket scope not found", errorapi.ErrNotFound)
	}
	copy := scope
	return &copy, nil
}

func (f *bucketTestStore) ListBucketScopes(context.Context) ([]domainbuckets.Scope, error) {
	keys := make([]string, 0, len(f.BucketScopes))
	for key := range f.BucketScopes {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	scopes := make([]domainbuckets.Scope, 0, len(keys))
	for _, key := range keys {
		scopes = append(scopes, f.BucketScopes[key])
	}
	return scopes, nil
}

func bucketTestScopeKey(organization, project string) string {
	return strings.TrimSpace(organization) + "|" + strings.TrimSpace(project)
}

func (f *bucketTestStore) ListBucketVisibilityRows(context.Context, []string, bool, bool) ([]domainbuckets.VisibilityRow, error) {
	return append([]domainbuckets.VisibilityRow(nil), f.VisibilityRows...), nil
}

func newInternalDRSObjectManager(store *bucketTestStore) internalDRSTestFixture {
	service, err := domainbuckets.NewService(domainbuckets.Dependencies{
		Credentials:     store,
		CredentialAdmin: store,
		Scopes:          store,
		Visibility:      store,
	}, nil)
	if err != nil {
		panic(err)
	}
	return internalDRSTestFixture{bucketService: service}
}

type internalDRSTestFixture struct {
	bucketService *domainbuckets.Service
}

var _ domainbuckets.CredentialReader = (*bucketTestStore)(nil)
var _ domainbuckets.CredentialAdmin = (*bucketTestStore)(nil)
var _ domainbuckets.ScopeStore = (*bucketTestStore)(nil)
var _ domainbuckets.VisibilityQuery = (*bucketTestStore)(nil)
