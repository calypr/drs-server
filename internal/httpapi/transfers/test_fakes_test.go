package transfers

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/calypr/syfon/internal/buckets"
	"github.com/calypr/syfon/internal/faults"
	"github.com/calypr/syfon/internal/objects"
	objectrecords "github.com/calypr/syfon/internal/objects/records"
	domaintransfers "github.com/calypr/syfon/internal/transfers"
	"github.com/calypr/syfon/internal/usage"
)

type transferHTTPFixture struct {
	Objects             map[string]*objects.Record
	ObjectAuthz         map[string]map[string][]string
	Credentials         map[string]buckets.Credential
	BucketScopes        map[string]buckets.Scope
	Usage               map[string]usage.FileUsage
	TransferEvents      []usage.Event
	NoDefaultCreds      bool
	GetBucketScopeCalls int
}

type transferObjectStoreFake struct {
	fixture *transferHTTPFixture
}

var (
	_ objectrecords.RecordReader       = (*transferObjectStoreFake)(nil)
	_ objectrecords.ContentReader      = (*transferObjectStoreFake)(nil)
	_ objectrecords.ChecksumScopeQuery = (*transferObjectStoreFake)(nil)
	_ objectrecords.ScopeQuery         = (*transferObjectStoreFake)(nil)
)

func (f *transferObjectStoreFake) GetObject(_ context.Context, id string) (*objects.Record, error) {
	obj, ok := f.fixture.Objects[id]
	if !ok {
		return nil, fmt.Errorf("%w: object not found", faults.ErrNotFound)
	}
	return f.copyObject(id, obj), nil
}

func (f *transferObjectStoreFake) GetBulkObjects(_ context.Context, ids []string) ([]objects.Record, error) {
	out := make([]objects.Record, 0, len(ids))
	for _, id := range ids {
		if obj, ok := f.fixture.Objects[id]; ok {
			out = append(out, *f.copyObject(id, obj))
		}
	}
	return out, nil
}

type transferAliasStoreFake struct {
	fixture *transferHTTPFixture
}

var _ objectrecords.AliasStore = (*transferAliasStoreFake)(nil)

func (f *transferAliasStoreFake) DeleteObjectAlias(_ context.Context, aliasID string) error {
	delete(f.fixture.Objects, aliasID)
	return nil
}

func (f *transferAliasStoreFake) CreateObjectAlias(_ context.Context, aliasID, canonicalObjectID string) error {
	obj, ok := f.fixture.Objects[canonicalObjectID]
	if !ok {
		return faults.ErrNotFound
	}
	copyObj := *obj
	copyObj.Id = objects.RecordID(aliasID)
	f.fixture.Objects[aliasID] = &copyObj
	return nil
}

func (f *transferAliasStoreFake) ResolveObjectAlias(_ context.Context, aliasID string) (string, error) {
	if _, ok := f.fixture.Objects[aliasID]; ok {
		return aliasID, nil
	}
	return "", faults.ErrNotFound
}

func (f *transferObjectStoreFake) GetObjectsByChecksum(_ context.Context, checksum string) ([]objects.Record, error) {
	out := make([]objects.Record, 0)
	for id, obj := range f.fixture.Objects {
		if id == checksum || string(obj.Id) == checksum || transferHasChecksum(obj, checksum) {
			out = append(out, *f.copyObject(id, obj))
		}
	}
	return out, nil
}

func (f *transferObjectStoreFake) GetObjectsByChecksums(ctx context.Context, checksums []string) (map[string][]objects.Record, error) {
	out := make(map[string][]objects.Record, len(checksums))
	for _, checksum := range checksums {
		matches, err := f.GetObjectsByChecksum(ctx, checksum)
		if err != nil {
			return nil, err
		}
		out[checksum] = matches
	}
	return out, nil
}

func (f *transferObjectStoreFake) ListScopedObjectIDsByChecksums(ctx context.Context, organization, project string, checksums []string) (map[string][]string, error) {
	out := make(map[string][]string, len(checksums))
	for _, checksum := range checksums {
		matches, err := f.GetObjectsByChecksum(ctx, checksum)
		if err != nil {
			return nil, err
		}
		for _, obj := range matches {
			if transferObjectMatchesScope(obj.Authorizations, organization, project) {
				out[checksum] = append(out[checksum], string(obj.Id))
			}
		}
	}
	return out, nil
}

func (f *transferObjectStoreFake) ListObjectIDsByScope(_ context.Context, organization, project string) ([]string, error) {
	ids := make([]string, 0)
	for id, obj := range f.fixture.Objects {
		if transferObjectMatchesScope(f.fixture.ObjectAuthz[id], organization, project) || transferObjectMatchesScope(obj.Authorizations, organization, project) {
			ids = append(ids, id)
		}
	}
	return ids, nil
}

func (f *transferObjectStoreFake) registerObjects(records []objects.Record) {
	if f.fixture.Objects == nil {
		f.fixture.Objects = map[string]*objects.Record{}
	}
	if f.fixture.ObjectAuthz == nil {
		f.fixture.ObjectAuthz = map[string]map[string][]string{}
	}
	for _, obj := range records {
		copyObj := obj
		f.fixture.Objects[string(obj.Id)] = &copyObj
		f.fixture.ObjectAuthz[string(obj.Id)] = transferCloneAuthzMap(obj.Authorizations)
	}
}

func (f *transferObjectStoreFake) copyObject(id string, obj *objects.Record) *objects.Record {
	copyObj := *obj
	if authz, ok := f.fixture.ObjectAuthz[id]; ok {
		copyObj.Authorizations = transferCloneAuthzMap(authz)
	}
	return &copyObj
}

type transferBucketStoreFake struct {
	fixture *transferHTTPFixture
}

var (
	_ buckets.CredentialReader = (*transferBucketStoreFake)(nil)
	_ buckets.CredentialAdmin  = (*transferBucketStoreFake)(nil)
	_ buckets.ScopeStore       = (*transferBucketStoreFake)(nil)
)

func (f *transferBucketStoreFake) GetS3Credential(_ context.Context, bucket string) (*buckets.Credential, error) {
	for key, credential := range f.fixture.Credentials {
		if key == bucket || strings.EqualFold(credential.Bucket, bucket) || strings.EqualFold(credential.CredentialID, bucket) {
			copyCredential := credential
			return &copyCredential, nil
		}
	}
	if f.fixture.NoDefaultCreds {
		return nil, nil
	}
	return &buckets.Credential{CredentialID: bucket, Bucket: bucket, Provider: "s3", Region: "us-east-1", AccessKey: "test-key", SecretKey: "test-secret"}, nil
}

func (f *transferBucketStoreFake) ListS3Credentials(_ context.Context) ([]buckets.Credential, error) {
	if len(f.fixture.Credentials) > 0 {
		out := make([]buckets.Credential, 0, len(f.fixture.Credentials))
		for _, credential := range f.fixture.Credentials {
			out = append(out, credential)
		}
		return out, nil
	}
	if f.fixture.NoDefaultCreds {
		return []buckets.Credential{}, nil
	}
	return []buckets.Credential{{CredentialID: "test-bucket-1", Bucket: "test-bucket-1", Provider: "s3", Region: "us-east-1"}}, nil
}

func (f *transferBucketStoreFake) SaveS3Credential(_ context.Context, credential *buckets.Credential) error {
	if f.fixture.Credentials == nil {
		f.fixture.Credentials = map[string]buckets.Credential{}
	}
	key := strings.TrimSpace(credential.CredentialID)
	if key == "" {
		key = strings.TrimSpace(credential.Bucket)
	}
	f.fixture.Credentials[key] = *credential
	return nil
}

func (f *transferBucketStoreFake) DeleteS3Credential(_ context.Context, bucket string) error {
	delete(f.fixture.Credentials, bucket)
	return nil
}

func transferBucketScopeKey(organization, project string) string {
	return strings.TrimSpace(organization) + "|" + strings.TrimSpace(project)
}

func (f *transferBucketStoreFake) CreateBucketScope(_ context.Context, scope *buckets.Scope) error {
	if f.fixture.BucketScopes == nil {
		f.fixture.BucketScopes = map[string]buckets.Scope{}
	}
	f.fixture.BucketScopes[transferBucketScopeKey(scope.Organization, scope.ProjectID)] = *scope
	return nil
}

func (f *transferBucketStoreFake) DeleteBucketScope(_ context.Context, organization, projectID, credentialID, pathPrefix string) error {
	key := transferBucketScopeKey(organization, projectID)
	scope, ok := f.fixture.BucketScopes[key]
	if !ok || (credentialID != "" && scope.CredentialID != credentialID && scope.Bucket != credentialID) || strings.Trim(scope.PathPrefix, "/") != strings.Trim(pathPrefix, "/") {
		return faults.ErrNotFound
	}
	delete(f.fixture.BucketScopes, key)
	return nil
}

func (f *transferBucketStoreFake) GetBucketScope(_ context.Context, organization, project string) (*buckets.Scope, error) {
	f.fixture.GetBucketScopeCalls++
	scope, ok := f.fixture.BucketScopes[transferBucketScopeKey(organization, project)]
	if !ok {
		return nil, faults.ErrNotFound
	}
	copyScope := scope
	return &copyScope, nil
}

func (f *transferBucketStoreFake) ListBucketScopes(_ context.Context) ([]buckets.Scope, error) {
	out := make([]buckets.Scope, 0, len(f.fixture.BucketScopes))
	for _, scope := range f.fixture.BucketScopes {
		out = append(out, scope)
	}
	return out, nil
}

type transferEventStoreFake struct {
	fixture *transferHTTPFixture
}

var _ domaintransfers.EventRecorder = (*transferEventStoreFake)(nil)

func (f *transferEventStoreFake) RecordTransferAttributionEvents(_ context.Context, events []usage.Event) error {
	f.fixture.TransferEvents = append(f.fixture.TransferEvents, events...)
	return nil
}

type transferFileCounterFake struct {
	fixture *transferHTTPFixture
}

var _ usage.FileCounterRecorder = (*transferFileCounterFake)(nil)

func (f *transferFileCounterFake) RecordFileUpload(_ context.Context, objectID string) error {
	f.record(objectID, true)
	return nil
}

func (f *transferFileCounterFake) RecordFileDownload(_ context.Context, objectID string) error {
	f.record(objectID, false)
	return nil
}

func (f *transferFileCounterFake) record(objectID string, upload bool) {
	if f.fixture.Usage == nil {
		f.fixture.Usage = map[string]usage.FileUsage{}
	}
	current := f.fixture.Usage[objectID]
	current.ObjectID = objectID
	now := time.Now().UTC()
	if upload {
		current.UploadCount++
		current.LastUploadTime = &now
	} else {
		current.DownloadCount++
		current.LastDownloadTime = &now
	}
	current.LastAccessTime = &now
	if obj := f.fixture.Objects[objectID]; obj != nil {
		current.Name = stringValue(obj.Name)
		current.Size = obj.Size
	}
	f.fixture.Usage[objectID] = current
}

func transferHasChecksum(obj *objects.Record, checksum string) bool {
	for _, candidate := range obj.Checksums {
		if strings.EqualFold(strings.TrimSpace(candidate.Checksum), strings.TrimSpace(checksum)) {
			return true
		}
	}
	return false
}

func transferObjectMatchesScope(authz map[string][]string, organization, project string) bool {
	organization = strings.TrimSpace(organization)
	project = strings.TrimSpace(project)
	if organization == "" {
		return true
	}
	projects, ok := authz[organization]
	if !ok {
		return false
	}
	if project == "" || len(projects) == 0 {
		return true
	}
	for _, candidate := range projects {
		if candidate == project {
			return true
		}
	}
	return false
}

func transferCloneAuthzMap(in map[string][]string) map[string][]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string][]string, len(in))
	for organization, projects := range in {
		out[organization] = append([]string(nil), projects...)
	}
	return out
}
