package buckets

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/calypr/syfon/internal/faults"
)

type fakeCredentialStore struct {
	mu sync.Mutex

	credentials []Credential
	getErr      error
	listErr     error
	saveErr     error
	deleteErr   error

	getCalls    int
	listCalls   int
	saveCalls   int
	deleteCalls int
	lastGet     string
	lastSaved   *Credential
	lastDeleted string
}

var _ CredentialReader = (*fakeCredentialStore)(nil)
var _ CredentialAdmin = (*fakeCredentialStore)(nil)

func (f *fakeCredentialStore) GetS3Credential(_ context.Context, bucket string) (*Credential, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.getCalls++
	f.lastGet = bucket
	if f.getErr != nil {
		return nil, f.getErr
	}
	for _, credential := range f.credentials {
		if credential.Bucket == bucket || credential.CredentialID == bucket {
			copy := credential
			return &copy, nil
		}
	}
	return nil, errors.New("credential not found")
}

func (f *fakeCredentialStore) ListS3Credentials(context.Context) ([]Credential, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.listCalls++
	if f.listErr != nil {
		return nil, f.listErr
	}
	return append([]Credential(nil), f.credentials...), nil
}

func (f *fakeCredentialStore) SaveS3Credential(_ context.Context, credential *Credential) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.saveCalls++
	if f.saveErr != nil {
		return f.saveErr
	}
	if credential != nil {
		copy := *credential
		f.lastSaved = &copy
	}
	return nil
}

func (f *fakeCredentialStore) DeleteS3Credential(_ context.Context, bucket string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deleteCalls++
	f.lastDeleted = bucket
	if f.deleteErr != nil {
		return f.deleteErr
	}
	return nil
}

type fakeScopeStore struct {
	mu sync.Mutex

	scopes        []Scope
	getErr        error
	listErr       error
	createErr     error
	deleteErr     error
	deleteMatcher func(Scope, string, string, string, string) bool

	getCalls    int
	listCalls   int
	createCalls int
	deleteCalls int
	lastCreated *Scope
	lastDelete  struct {
		organization string
		projectID    string
		credentialID string
		pathPrefix   string
	}
}

var _ ScopeStore = (*fakeScopeStore)(nil)

func (f *fakeScopeStore) CreateBucketScope(_ context.Context, scope *Scope) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.createCalls++
	if f.createErr != nil {
		return f.createErr
	}
	if scope != nil {
		copy := *scope
		f.lastCreated = &copy
	}
	return nil
}

func (f *fakeScopeStore) DeleteBucketScope(_ context.Context, organization, projectID, credentialID, pathPrefix string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deleteCalls++
	f.lastDelete.organization = organization
	f.lastDelete.projectID = projectID
	f.lastDelete.credentialID = credentialID
	f.lastDelete.pathPrefix = pathPrefix
	if f.deleteErr != nil {
		return f.deleteErr
	}
	filtered := f.scopes[:0]
	for _, scope := range f.scopes {
		matchesCredential := scope.CredentialID == credentialID
		if f.deleteMatcher != nil {
			matchesCredential = f.deleteMatcher(scope, organization, projectID, credentialID, pathPrefix)
		}
		if scope.Organization == organization && scope.ProjectID == projectID &&
			matchesCredential && scope.PathPrefix == pathPrefix {
			continue
		}
		filtered = append(filtered, scope)
	}
	f.scopes = filtered
	return nil
}

func (f *fakeScopeStore) GetBucketScope(_ context.Context, organization, projectID string) (*Scope, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.getCalls++
	if f.getErr != nil {
		return nil, f.getErr
	}
	for _, scope := range f.scopes {
		if scope.Organization == organization && scope.ProjectID == projectID {
			copy := scope
			return &copy, nil
		}
	}
	return nil, faults.ErrNotFound
}

func (f *fakeScopeStore) ListBucketScopes(context.Context) ([]Scope, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.listCalls++
	if f.listErr != nil {
		return nil, f.listErr
	}
	return append([]Scope(nil), f.scopes...), nil
}

type fakeVisibilityQuery struct {
	mu                  sync.Mutex
	rows                []VisibilityRow
	err                 error
	calls               int
	resources           []string
	includeUnscoped     bool
	restrictToResources bool
}

var _ VisibilityQuery = (*fakeVisibilityQuery)(nil)

func (f *fakeVisibilityQuery) ListBucketVisibilityRows(_ context.Context, resources []string, includeUnscoped, restrictToResources bool) ([]VisibilityRow, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	f.resources = append([]string(nil), resources...)
	f.includeUnscoped = includeUnscoped
	f.restrictToResources = restrictToResources
	if f.err != nil {
		return nil, f.err
	}
	return append([]VisibilityRow(nil), f.rows...), nil
}

type recordingInvalidator struct {
	mu      sync.Mutex
	aliases []string
}

func (r *recordingInvalidator) InvalidateBucket(bucket string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.aliases = append(r.aliases, bucket)
}

func (r *recordingInvalidator) snapshot() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.aliases...)
}

func (r *recordingInvalidator) reset() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.aliases = nil
}

type manualClock struct {
	mu       sync.Mutex
	nowValue int64
}

func (c *manualClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return time.Unix(0, c.nowValue)
}

func (c *manualClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.nowValue += d.Nanoseconds()
	c.mu.Unlock()
}

func newFakeService(creds []Credential, scopes []Scope, visibility VisibilityQuery, fallback VisibilityFallback, invalidator cacheInvalidator) (*Service, *fakeCredentialStore, *fakeScopeStore) {
	credentialStore := &fakeCredentialStore{credentials: append([]Credential(nil), creds...)}
	scopeStore := &fakeScopeStore{scopes: append([]Scope(nil), scopes...)}
	service := newService(Dependencies{
		Credentials:     credentialStore,
		CredentialAdmin: credentialStore,
		Scopes:          scopeStore,
		Visibility:      visibility,
		Fallback:        fallback,
	}, invalidator, time.Minute, time.Now)
	return service, credentialStore, scopeStore
}
