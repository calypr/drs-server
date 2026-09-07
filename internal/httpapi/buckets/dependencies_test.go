package buckets

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/calypr/syfon/internal/access"
	domainbuckets "github.com/calypr/syfon/internal/buckets"
	"github.com/calypr/syfon/internal/faults"
	"github.com/calypr/syfon/internal/objects"
)

type bucketTestStore struct {
	Credentials  map[string]domainbuckets.Credential
	BucketScopes map[string]domainbuckets.Scope
	Objects      map[string]*objects.Record
	ObjectAuthz  map[string]map[string][]string
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
	return nil, errors.New("credential not found")
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
		return fmt.Errorf("%w: bucket scope not found", faults.ErrNotFound)
	}
	delete(f.BucketScopes, key)
	return nil
}

func (f *bucketTestStore) GetBucketScope(_ context.Context, organization, projectID string) (*domainbuckets.Scope, error) {
	scope, ok := f.BucketScopes[bucketTestScopeKey(organization, projectID)]
	if !ok {
		return nil, fmt.Errorf("%w: bucket scope not found", faults.ErrNotFound)
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

func (f *bucketTestStore) GetObject(_ context.Context, id string) (*objects.Record, error) {
	record, ok := f.objectCopy(id)
	if !ok {
		return nil, fmt.Errorf("%w: object not found", faults.ErrNotFound)
	}
	return &record, nil
}

func (f *bucketTestStore) GetBulkObjects(_ context.Context, ids []string) ([]objects.Record, error) {
	records := make([]objects.Record, 0, len(ids))
	for _, id := range ids {
		if record, ok := f.objectCopy(id); ok {
			records = append(records, record)
		}
	}
	return records, nil
}

func (f *bucketTestStore) ListObjectIDsByScope(_ context.Context, organization, project string) ([]string, error) {
	ids := make([]string, 0, len(f.Objects))
	for id := range f.Objects {
		if strings.TrimSpace(organization) == "" {
			ids = append(ids, id)
			continue
		}
		projects := f.ObjectAuthz[id][organization]
		if strings.TrimSpace(project) == "" || len(projects) == 0 {
			if _, ok := f.ObjectAuthz[id][organization]; ok {
				ids = append(ids, id)
			}
			continue
		}
		for _, candidate := range projects {
			if candidate == project {
				ids = append(ids, id)
				break
			}
		}
	}
	sort.Strings(ids)
	return ids, nil
}

func (f *bucketTestStore) objectCopy(id string) (objects.Record, bool) {
	record, ok := f.Objects[id]
	if !ok {
		return objects.Record{}, false
	}
	copy := *record
	if authz, ok := f.ObjectAuthz[id]; ok {
		copy.Authorizations = cloneBucketTestAuthz(authz)
	}
	if record.Name != nil {
		name := *record.Name
		copy.Name = &name
	}
	if record.AccessMethods != nil {
		methods := make([]objects.AccessMethod, len(*record.AccessMethods))
		for i, method := range *record.AccessMethods {
			methods[i] = method
			if method.AccessUrl != nil {
				accessURL := *method.AccessUrl
				methods[i].AccessUrl = &accessURL
			}
		}
		copy.AccessMethods = &methods
	}
	return copy, true
}

func cloneBucketTestAuthz(authz map[string][]string) map[string][]string {
	if authz == nil {
		return nil
	}
	copy := make(map[string][]string, len(authz))
	for organization, projects := range authz {
		copy[organization] = append([]string(nil), projects...)
	}
	return copy
}

func newInternalDRSObjectManager(store *bucketTestStore, storageDependency any) internalDRSTestFixture {
	var invalidator interface{ InvalidateBucket(string) }
	if candidate, ok := storageDependency.(interface{ InvalidateBucket(string) }); ok {
		invalidator = candidate
	}

	service, err := domainbuckets.NewService(domainbuckets.Dependencies{
		Credentials:     store,
		CredentialAdmin: store,
		Scopes:          store,
		Fallback:        newBucketVisibilityFallback(store, store),
	}, invalidator)
	if err != nil {
		panic(err)
	}
	return internalDRSTestFixture{bucketService: service}
}

type internalDRSTestFixture struct {
	bucketService *domainbuckets.Service
}

type internalDRSStorageFake struct{}

func (*internalDRSStorageFake) InvalidateBucket(string) {}

var _ domainbuckets.CredentialReader = (*bucketTestStore)(nil)
var _ domainbuckets.CredentialAdmin = (*bucketTestStore)(nil)
var _ domainbuckets.ScopeStore = (*bucketTestStore)(nil)
var _ objects.RecordReader = (*bucketTestStore)(nil)
var _ objects.ScopeQuery = (*bucketTestStore)(nil)

var (
	errBucketVisibilityScopeQuery   = errors.New("bucket visibility fallback requires an object scope query")
	errBucketVisibilityRecordReader = errors.New("bucket visibility fallback requires an object record reader")
)

func newBucketVisibilityFallback(scope objects.ScopeQuery, reader objects.RecordReader) domainbuckets.VisibilityFallback {
	return func(ctx context.Context) ([]domainbuckets.VisibilityRow, error) {
		if scope == nil {
			return nil, errBucketVisibilityScopeQuery
		}
		if reader == nil {
			return nil, errBucketVisibilityRecordReader
		}

		ids, err := scope.ListObjectIDsByScope(ctx, "", "")
		if err != nil {
			return nil, err
		}
		if len(ids) == 0 {
			return []domainbuckets.VisibilityRow{}, nil
		}

		records, err := reader.GetBulkObjects(ctx, ids)
		if err != nil {
			return nil, err
		}
		rows := make([]domainbuckets.VisibilityRow, 0)
		for i := range records {
			object := &records[i]
			if !bucketVisibilityObjectReadable(ctx, object) || object.AccessMethods == nil {
				continue
			}
			resources := objects.AccessResources(object)
			if len(resources) == 0 {
				continue
			}
			for _, method := range *object.AccessMethods {
				if method.AccessUrl == nil {
					continue
				}
				accessURL := strings.TrimSpace(method.AccessUrl.Url)
				if accessURL == "" {
					continue
				}
				for _, resource := range resources {
					resource = strings.TrimSpace(resource)
					if resource == "" {
						continue
					}
					rows = append(rows, domainbuckets.VisibilityRow{
						AccessURL:  accessURL,
						AccessType: strings.TrimSpace(method.Type),
						Resource:   resource,
					})
				}
			}
		}
		return rows, nil
	}
}

func bucketVisibilityObjectReadable(ctx context.Context, object *objects.Record) bool {
	if !access.IsAuthzEnforced(ctx) ||
		access.HasMethodAccess(ctx, "read", []string{"/programs"}) ||
		access.HasMethodAccess(ctx, "read", []string{"/data_file"}) {
		return true
	}
	if object != nil && object.PublicRead {
		return true
	}
	resources := objects.AccessResources(object)
	if object != nil && object.PublicReadPolicyKnown && len(resources) == 0 {
		return false
	}
	return access.HasObjectMethodAccess(ctx, "read", resources)
}
