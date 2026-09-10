package buckets

import (
	"context"
	"errors"
	"testing"

	"github.com/calypr/syfon/apigen/errorapi"
)

func TestLookupBucketScopeNormalizesResult(t *testing.T) {
	service, _, _ := newFakeService(nil, []Scope{{
		Organization: " org ",
		ProjectID:    " project ",
		CredentialID: " credential ",
		Bucket:       " bucket ",
		PathPrefix:   " /prefix/ ",
	}}, &fakeVisibilityQuery{}, nil)

	got, found, err := service.LookupBucketScope(context.Background(), " org ", " project ")
	if err != nil || !found {
		t.Fatalf("LookupBucketScope()=(%+v,%v,%v)", got, found, err)
	}
	want := Scope{Organization: "org", ProjectID: "project", CredentialID: "credential", Bucket: "bucket", PathPrefix: "prefix"}
	if got != want {
		t.Fatalf("normalized scope=%+v, want %+v", got, want)
	}
}

func TestLookupBucketScopeHandlesMissingAndBackendErrors(t *testing.T) {
	service, _, scopes := newFakeService(nil, nil, &fakeVisibilityQuery{}, nil)
	if got, found, err := service.LookupBucketScope(context.Background(), "org", "missing"); err != nil || found || got != (Scope{}) {
		t.Fatalf("missing LookupBucketScope()=(%+v,%v,%v)", got, found, err)
	}

	scopes.getErr = errors.New("database unavailable")
	if _, _, err := service.LookupBucketScope(context.Background(), "org", "error"); !errors.Is(err, scopes.getErr) {
		t.Fatalf("LookupBucketScope error=%v, want %v", err, scopes.getErr)
	}

	scopes.getErr = errorapi.ErrNotFound
	if _, found, err := service.LookupBucketScope(context.Background(), "org", "missing"); err != nil || found {
		t.Fatalf("not-found LookupBucketScope()=(%v,%v), want nil,false", err, found)
	}
}

func TestCreateBucketScopePersistsOrReturnsWriteError(t *testing.T) {
	service, _, scopes := newFakeService(nil, nil, &fakeVisibilityQuery{}, nil)
	want := &Scope{Organization: "org", ProjectID: "project", PathPrefix: "prefix"}
	if err := service.CreateBucketScope(context.Background(), want); err != nil {
		t.Fatalf("CreateBucketScope: %v", err)
	}
	if scopes.createCalls != 1 || scopes.lastCreated == nil || *scopes.lastCreated != *want {
		t.Fatalf("persisted scope=%+v calls=%d, want %+v once", scopes.lastCreated, scopes.createCalls, want)
	}

	scopes.createErr = errors.New("write failed")
	if err := service.CreateBucketScope(context.Background(), want); !errors.Is(err, scopes.createErr) {
		t.Fatalf("CreateBucketScope error=%v, want %v", err, scopes.createErr)
	}
}

func TestDeleteBucketScopeCleansLastCredential(t *testing.T) {
	invalidator := &recordingInvalidator{}
	credential := Credential{CredentialID: "credential-id", Bucket: "physical-bucket"}
	scope := Scope{Organization: "org", ProjectID: "project", CredentialID: "credential-id", PathPrefix: "prefix"}
	service, credentials, scopes := newFakeService([]Credential{credential}, []Scope{scope}, &fakeVisibilityQuery{}, invalidator)

	if err := service.DeleteBucketScope(context.Background(), "org", "project", "credential-id", "prefix"); err != nil {
		t.Fatalf("DeleteBucketScope: %v", err)
	}
	if scopes.deleteCalls != 1 {
		t.Fatalf("scope delete calls=%d, want 1", scopes.deleteCalls)
	}
	if credentials.deleteCalls != 1 || credentials.lastDeleted != "credential-id" {
		t.Fatalf("last-scope credential cleanup: calls=%d bucket=%q", credentials.deleteCalls, credentials.lastDeleted)
	}
	if len(invalidator.snapshot()) == 0 {
		t.Fatal("last-scope credential cleanup did not invalidate signer aliases")
	}
}

func TestDeleteBucketScopeKeepsCredentialWithLegacySibling(t *testing.T) {
	credential := Credential{CredentialID: "credential-id", Bucket: "physical-bucket"}
	scopes := []Scope{
		{Organization: "org", ProjectID: "project-a", CredentialID: "credential-id"},
		{Organization: "org", ProjectID: "project-b", Bucket: "physical-bucket"},
	}
	service, credentials, store := newFakeService([]Credential{credential}, scopes, &fakeVisibilityQuery{}, nil)
	store.deleteMatcher = func(scope Scope, _, _, credentialID, _ string) bool {
		return scope.CredentialID == credentialID || scope.Bucket == credentialID
	}
	if err := service.DeleteBucketScope(context.Background(), "org", "project-a", "physical-bucket", ""); err != nil {
		t.Fatalf("DeleteBucketScope: %v", err)
	}
	if credentials.deleteCalls != 0 {
		t.Fatalf("deleted credential despite remaining legacy sibling scope: %d calls", credentials.deleteCalls)
	}
}

func TestDeleteBucketScopeDoesNotCleanupAfterFailedDelete(t *testing.T) {
	scope := Scope{Organization: "org", ProjectID: "project", CredentialID: "credential-id"}
	service, credentials, scopes := newFakeService([]Credential{{CredentialID: "credential-id", Bucket: "bucket"}}, []Scope{scope}, &fakeVisibilityQuery{}, nil)
	scopes.deleteErr = errors.New("delete failed")

	if err := service.DeleteBucketScope(context.Background(), "org", "project", "credential-id", ""); !errors.Is(err, scopes.deleteErr) {
		t.Fatalf("DeleteBucketScope error=%v, want %v", err, scopes.deleteErr)
	}
	if credentials.deleteCalls != 0 {
		t.Fatal("failed scope delete cleaned up credential")
	}
}
