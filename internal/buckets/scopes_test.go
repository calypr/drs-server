package buckets

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/calypr/syfon/internal/faults"
)

func newClockedService(clock *manualClock, scopes []Scope, credentials []Credential, invalidator cacheInvalidator) (*Service, *fakeCredentialStore, *fakeScopeStore) {
	credentialStore := &fakeCredentialStore{credentials: append([]Credential(nil), credentials...)}
	scopeStore := &fakeScopeStore{scopes: append([]Scope(nil), scopes...)}
	service := newService(Dependencies{
		Credentials:     credentialStore,
		CredentialAdmin: credentialStore,
		Scopes:          scopeStore,
		Fallback:        func(context.Context) ([]VisibilityRow, error) { return nil, nil },
	}, invalidator, 10*time.Second, clock.Now)
	return service, credentialStore, scopeStore
}

func TestLookupBucketScopeNormalizesAndCachesPositiveResult(t *testing.T) {
	clock := &manualClock{}
	service, _, scopes := newClockedService(clock, []Scope{{
		Organization: " org ",
		ProjectID:    " project ",
		CredentialID: " credential ",
		Bucket:       " bucket ",
		PathPrefix:   " /prefix/ ",
	}}, nil, nil)

	got, found, err := service.LookupBucketScope(context.Background(), " org ", " project ")
	if err != nil || !found {
		t.Fatalf("first LookupBucketScope()=(%+v,%v,%v)", got, found, err)
	}
	want := Scope{Organization: "org", ProjectID: "project", CredentialID: "credential", Bucket: "bucket", PathPrefix: "prefix"}
	if got != want {
		t.Fatalf("normalized scope=%+v, want %+v", got, want)
	}
	got.Organization = "mutated"
	second, secondFound, err := service.LookupBucketScope(context.Background(), "org", "project")
	if err != nil || !secondFound || second != want {
		t.Fatalf("cached LookupBucketScope()=(%+v,%v,%v), want %+v,true,nil", second, secondFound, err, want)
	}
	if scopes.getCalls != 1 {
		t.Fatalf("expected one scope backend lookup, got %d", scopes.getCalls)
	}
}

func TestLookupBucketScopeCachesNotFoundButNotBackendErrors(t *testing.T) {
	clock := &manualClock{}
	service, _, scopes := newClockedService(clock, nil, nil, nil)

	for i := 0; i < 2; i++ {
		got, found, err := service.LookupBucketScope(context.Background(), "org", "missing")
		if err != nil || found {
			t.Fatalf("cached miss=(%+v,%v,%v)", got, found, err)
		}
		if i == 0 && got != (Scope{}) {
			t.Fatalf("first not-found lookup returned cached key %+v", got)
		}
		if i == 1 && got != (Scope{Organization: "org", ProjectID: "missing"}) {
			t.Fatalf("cached not-found lookup returned %+v", got)
		}
	}
	if scopes.getCalls != 1 {
		t.Fatalf("negative lookup made %d backend calls, want 1", scopes.getCalls)
	}

	service, _, scopes = newClockedService(clock, nil, nil, nil)
	scopes.getErr = errors.New("database unavailable")
	for range 2 {
		if _, _, err := service.LookupBucketScope(context.Background(), "org", "error"); !errors.Is(err, scopes.getErr) {
			t.Fatalf("LookupBucketScope error=%v, want %v", err, scopes.getErr)
		}
	}
	if scopes.getCalls != 2 {
		t.Fatalf("backend error was cached after %d calls, want 2", scopes.getCalls)
	}
}

func TestLookupBucketScopeCacheExpiresDeterministically(t *testing.T) {
	clock := &manualClock{}
	service, _, scopes := newClockedService(clock, []Scope{{Organization: "org", ProjectID: "project", Bucket: "bucket"}}, nil, nil)

	if _, _, err := service.LookupBucketScope(context.Background(), "org", "project"); err != nil {
		t.Fatalf("first lookup: %v", err)
	}
	clock.Advance(9 * time.Second)
	if _, _, err := service.LookupBucketScope(context.Background(), "org", "project"); err != nil {
		t.Fatalf("lookup before expiry: %v", err)
	}
	if scopes.getCalls != 1 {
		t.Fatalf("lookup before expiry made %d backend calls", scopes.getCalls)
	}
	clock.Advance(time.Second)
	if _, _, err := service.LookupBucketScope(context.Background(), "org", "project"); err != nil {
		t.Fatalf("lookup at expiry: %v", err)
	}
	if scopes.getCalls != 1 {
		t.Fatalf("lookup at expiry made %d backend calls, want 1", scopes.getCalls)
	}
	clock.Advance(time.Nanosecond)
	if _, _, err := service.LookupBucketScope(context.Background(), "org", "project"); err != nil {
		t.Fatalf("lookup after expiry: %v", err)
	}
	if scopes.getCalls != 2 {
		t.Fatalf("lookup after expiry made %d backend calls, want 2", scopes.getCalls)
	}
}

func TestCreateBucketScopeSeedsCacheOnlyAfterSuccessfulWrite(t *testing.T) {
	clock := &manualClock{}
	service, _, scopes := newClockedService(clock, nil, nil, nil)
	scope := &Scope{Organization: " org ", ProjectID: " project ", PathPrefix: " /prefix/ "}

	if err := service.CreateBucketScope(context.Background(), scope); err != nil {
		t.Fatalf("CreateBucketScope: %v", err)
	}
	if _, found, err := service.LookupBucketScope(context.Background(), "org", "project"); err != nil || !found {
		t.Fatalf("seeded LookupBucketScope()=(%v,%v)", err, found)
	}
	if scopes.getCalls != 0 {
		t.Fatalf("seeded scope performed %d backend reads", scopes.getCalls)
	}

	service, _, scopes = newClockedService(clock, nil, nil, nil)
	scopes.createErr = errors.New("write failed")
	if err := service.CreateBucketScope(context.Background(), scope); !errors.Is(err, scopes.createErr) {
		t.Fatalf("CreateBucketScope error=%v, want %v", err, scopes.createErr)
	}
	if _, _, err := service.LookupBucketScope(context.Background(), "org", "project"); err != nil {
		t.Fatalf("lookup after failed create: %v", err)
	}
	if scopes.getCalls != 1 {
		t.Fatalf("failed create unexpectedly seeded cache; backend calls=%d", scopes.getCalls)
	}
}

func TestDeleteBucketScopeClearsCacheAndCleansLastCredential(t *testing.T) {
	clock := &manualClock{}
	invalidator := &recordingInvalidator{}
	credential := Credential{CredentialID: "credential-id", Bucket: "physical-bucket"}
	scope := Scope{Organization: "org", ProjectID: "project", CredentialID: "credential-id", PathPrefix: "prefix"}
	service, credentials, scopes := newClockedService(clock, []Scope{scope}, []Credential{credential}, invalidator)
	if _, _, err := service.LookupBucketScope(context.Background(), "org", "project"); err != nil {
		t.Fatalf("prime scope cache: %v", err)
	}
	if err := service.DeleteBucketScope(context.Background(), "org", "project", "credential-id", "prefix"); err != nil {
		t.Fatalf("DeleteBucketScope: %v", err)
	}
	if credentials.deleteCalls != 1 || credentials.lastDeleted != "credential-id" {
		t.Fatalf("last-scope credential cleanup: calls=%d bucket=%q", credentials.deleteCalls, credentials.lastDeleted)
	}
	if len(invalidator.snapshot()) == 0 {
		t.Fatal("last-scope credential cleanup did not invalidate signer aliases")
	}
	before := scopes.getCalls
	if _, found, err := service.LookupBucketScope(context.Background(), "org", "project"); err != nil || found {
		t.Fatalf("lookup after delete=(%v,%v), want miss", err, found)
	}
	if scopes.getCalls != before+1 {
		t.Fatalf("scope cache was not cleared after delete: calls before=%d after=%d", before, scopes.getCalls)
	}
}

func TestDeleteBucketScopeRecognizesLegacyPhysicalAliasWithRemainingSibling(t *testing.T) {
	clock := &manualClock{}
	credential := Credential{CredentialID: "credential-id", Bucket: "physical-bucket"}
	scopes := []Scope{
		{Organization: "org", ProjectID: "project-a", CredentialID: "credential-id"},
		{Organization: "org", ProjectID: "project-b", Bucket: "physical-bucket"},
	}
	service, credentials, stores := newClockedService(clock, scopes, []Credential{credential}, nil)
	stores.deleteMatcher = func(scope Scope, _, _, credentialID, _ string) bool {
		return scope.CredentialID == credentialID || scope.Bucket == credentialID
	}
	if err := service.DeleteBucketScope(context.Background(), "org", "project-a", "physical-bucket", ""); err != nil {
		t.Fatalf("DeleteBucketScope: %v", err)
	}
	if credentials.deleteCalls != 0 {
		t.Fatalf("deleted credential despite remaining legacy sibling scope: %d calls", credentials.deleteCalls)
	}
	if stores.deleteCalls != 1 {
		t.Fatalf("scope delete calls=%d, want 1", stores.deleteCalls)
	}
}

func TestDeleteBucketScopeDoesNotClearOrCleanupAfterFailedDelete(t *testing.T) {
	clock := &manualClock{}
	scope := Scope{Organization: "org", ProjectID: "project", CredentialID: "credential-id"}
	service, credentials, scopes := newClockedService(clock, []Scope{scope}, []Credential{{CredentialID: "credential-id", Bucket: "bucket"}}, nil)
	if _, _, err := service.LookupBucketScope(context.Background(), "org", "project"); err != nil {
		t.Fatalf("prime cache: %v", err)
	}
	scopes.deleteErr = errors.New("delete failed")
	if err := service.DeleteBucketScope(context.Background(), "org", "project", "credential-id", ""); !errors.Is(err, scopes.deleteErr) {
		t.Fatalf("DeleteBucketScope error=%v, want %v", err, scopes.deleteErr)
	}
	if credentials.deleteCalls != 0 {
		t.Fatalf("failed scope delete cleaned up credential")
	}
	before := scopes.getCalls
	if _, found, err := service.LookupBucketScope(context.Background(), "org", "project"); err != nil || !found {
		t.Fatalf("failed-delete cached lookup=(%v,%v), want cached hit", err, found)
	}
	if scopes.getCalls != before {
		t.Fatalf("failed scope delete cleared cache: calls before=%d after=%d", before, scopes.getCalls)
	}
}

func TestLookupBucketScopeConvertsNotFoundErrorsToCachedMisses(t *testing.T) {
	clock := &manualClock{}
	service, _, scopes := newClockedService(clock, nil, nil, nil)
	scopes.getErr = faults.ErrNotFound
	if _, found, err := service.LookupBucketScope(context.Background(), "org", "project"); err != nil || found {
		t.Fatalf("not-found lookup=(%v,%v), want nil,false", err, found)
	}
	if _, found, err := service.LookupBucketScope(context.Background(), "org", "project"); err != nil || found {
		t.Fatalf("cached not-found lookup=(%v,%v), want nil,false", err, found)
	}
	if scopes.getCalls != 1 {
		t.Fatalf("not-found cache made %d backend calls, want 1", scopes.getCalls)
	}
}
