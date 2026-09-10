package buckets

import (
	"context"
	"errors"
	"testing"

	"github.com/calypr/syfon/apigen/errorapi"
)

func TestResolveStorageScopeComposesOrganizationAndProjectPrefixes(t *testing.T) {
	service, _, _ := newFakeService([]Credential{{CredentialID: "credential", Bucket: "bucket", Provider: "s3"}}, []Scope{
		{Organization: "org", Bucket: "bucket", PathPrefix: "prefix"},
		{Organization: "org", ProjectID: "project", Bucket: "bucket", PathPrefix: "project"},
	}, &fakeVisibilityQuery{}, nil)

	got, err := service.ResolveStorageScope(context.Background(), " org ", " project ")
	if err != nil {
		t.Fatalf("ResolveStorageScope() error = %v", err)
	}
	if got.Provider != "s3" || got.Bucket != "bucket" || got.Prefix != "prefix/project" {
		t.Fatalf("resolved scope = %+v", got)
	}
	if len(got.Prefixes) != 2 || got.Prefixes[0] != "prefix" || got.Prefixes[1] != "project" {
		t.Fatalf("resolved prefixes = %v", got.Prefixes)
	}
	if got.Credential.CredentialID != "credential" {
		t.Fatalf("resolved credential = %+v", got.Credential)
	}
}

func TestResolveStorageScopeClassifiesMissingScopeAndCredential(t *testing.T) {
	service, _, _ := newFakeService(nil, nil, &fakeVisibilityQuery{}, nil)
	_, err := service.ResolveStorageScope(context.Background(), "org", "project")
	var resolutionErr *StorageScopeError
	if !errors.As(err, &resolutionErr) || resolutionErr.Kind != StorageScopeNotFound || !errors.Is(err, errorapi.ErrProjectScopeNotFound) {
		t.Fatalf("missing scope error = %v, want classified scope error", err)
	}

	service, _, _ = newFakeService(nil, []Scope{{Organization: "org", ProjectID: "project", Bucket: "bucket"}}, &fakeVisibilityQuery{}, nil)
	_, err = service.ResolveStorageScope(context.Background(), "org", "project")
	if !errors.As(err, &resolutionErr) || resolutionErr.Kind != StorageScopeCredentialMissing || !errors.Is(err, errorapi.ErrStorageCredentialMissing) {
		t.Fatalf("missing credential error = %v, want classified credential error", err)
	}
}
