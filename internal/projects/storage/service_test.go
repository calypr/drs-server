package storage

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/calypr/syfon/apigen/errorapi"
	"github.com/calypr/syfon/internal/access"
	"github.com/calypr/syfon/internal/buckets"
	"github.com/calypr/syfon/internal/storage"
)

type fakeScopeResolver struct {
	scope buckets.StorageScope
	err   error
}

func (f fakeScopeResolver) ResolveStorageScope(context.Context, string, string) (buckets.StorageScope, error) {
	return f.scope, f.err
}

type fakeCredentials struct {
	values map[string]buckets.Credential
}

func (f fakeCredentials) GetS3Credential(_ context.Context, bucket string) (*buckets.Credential, error) {
	for key, value := range f.values {
		if strings.EqualFold(key, bucket) || strings.EqualFold(value.Bucket, bucket) || strings.EqualFold(value.CredentialID, bucket) {
			copy := value
			return &copy, nil
		}
	}
	return nil, errors.New("not found")
}

func (f fakeCredentials) ListS3Credentials(context.Context) ([]buckets.Credential, error) {
	result := make([]buckets.Credential, 0, len(f.values))
	for _, value := range f.values {
		result = append(result, value)
	}
	return result, nil
}

type fakeVisibility struct {
	values map[string]buckets.VisibleBucket
	called int
}

func (f *fakeVisibility) ListVisibleBuckets(context.Context) (map[string]buckets.VisibleBucket, error) {
	f.called++
	return f.values, nil
}

func TestVisibleBucketContainsMatchesPhysicalAndCredentialAliases(t *testing.T) {
	visible := map[string]buckets.VisibleBucket{
		"credential-id": {Credential: buckets.Credential{CredentialID: "credential-id", Bucket: "physical-bucket"}},
	}
	for _, tc := range []struct {
		name         string
		bucket       string
		credentialID string
		want         bool
	}{
		{name: "physical bucket", bucket: "PHYSICAL-BUCKET", want: true},
		{name: "map credential key", credentialID: "CREDENTIAL-ID", want: true},
		{name: "credential field", credentialID: "credential-id", want: true},
		{name: "unknown", bucket: "other", credentialID: "other", want: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := visibleBucketContains(visible, tc.bucket, tc.credentialID); got != tc.want {
				t.Fatalf("visibleBucketContains()=%v, want %v", got, tc.want)
			}
		})
	}
}

type fakeInventory struct {
	items    []storage.ObjectMetadata
	result   storage.InventoryResult
	requests []storage.InventoryRequest
}

func (f *fakeInventory) Inventory(_ context.Context, request storage.InventoryRequest) (storage.InventoryResult, error) {
	f.requests = append(f.requests, request)
	if f.result.Items != nil || !f.result.Complete {
		return f.result, nil
	}
	return storage.InventoryResult{Items: f.items, Complete: true}, nil
}

type recordingProbe struct {
	targets []storage.Target
}

func (f *recordingProbe) Probe(_ context.Context, targets []storage.ProbeTarget) []storage.ProbeResult {
	if len(targets) == 0 {
		return nil
	}
	target := targets[0].Target
	f.targets = append(f.targets, target)
	return []storage.ProbeResult{{ID: targets[0].ID, Target: target, Metadata: storage.ObjectMetadata{Bucket: target.PhysicalBucket, Key: target.Key}}}
}

type fakeDelete struct {
	locations []string
}

func (f *fakeDelete) DeleteExact(_ context.Context, targets []storage.DeleteTarget) error {
	for _, target := range targets {
		f.locations = append(f.locations, target.Location)
	}
	return nil
}

type fakeCleanupObjects struct {
	deleted []string
	count   int
	err     error
}

func (f *fakeCleanupObjects) DeleteBulkByScope(_ context.Context, organization, project string) (int, error) {
	f.deleted = append(f.deleted, strings.TrimSpace(organization)+"/"+strings.TrimSpace(project))
	return f.count, f.err
}

type fakeCleanupScopes struct {
	scopes  []buckets.Scope
	deleted []string
	err     error
}

func (f *fakeCleanupScopes) ListBucketScopes(context.Context) ([]buckets.Scope, error) {
	return f.scopes, f.err
}

func (f *fakeCleanupScopes) DeleteBucketScope(_ context.Context, organization, project, credential, prefix string) error {
	f.deleted = append(f.deleted, strings.Join([]string{organization, project, credential, prefix}, "/"))
	return f.err
}

func projectService(inventory *fakeInventory, deletePort DeletePort) (*Service, *fakeVisibility) {
	return projectServiceWithTarget(inventory, deletePort, buckets.StorageScope{
		Provider: "s3", Bucket: "bucket", Prefix: "prefix/project", Prefixes: []string{"prefix", "project"},
		Credential: buckets.Credential{CredentialID: "cred", Bucket: "bucket", Provider: "s3"},
	})
}

func projectServiceWithTarget(inventory *fakeInventory, deletePort DeletePort, target buckets.StorageScope) (*Service, *fakeVisibility) {
	credential := buckets.Credential{CredentialID: "cred", Bucket: "bucket", Provider: "s3"}
	visibility := &fakeVisibility{values: map[string]buckets.VisibleBucket{
		"cred": {Credential: credential},
	}}
	service := NewService(Dependencies{
		ScopeResolver: fakeScopeResolver{scope: target},
		Credentials:   fakeCredentials{values: map[string]buckets.Credential{"cred": credential}},
		Visibility:    visibility,
		Providers:     Providers{Inventory: inventory, Delete: deletePort},
	})
	return service, visibility
}

func TestInspectProjectPreservesPartialInventoryAndCanonicalItems(t *testing.T) {
	inventory := &fakeInventory{result: storage.InventoryResult{
		Items:    []storage.ObjectMetadata{{Key: "/prefix/project/z", SizeBytes: 2}, {Key: "prefix/project/a", SizeBytes: 3}},
		Complete: false,
	}}
	service, _ := projectService(inventory, nil)
	result, err := service.InspectProjectStorage(context.Background(), " org ", " project ", InspectionOptions{Mode: ModeItems, IncludeHead: true})
	if err != nil {
		t.Fatalf("InspectProjectStorage() error = %v", err)
	}
	if result.Summary.InventoryComplete || result.Summary.InventoryWarning == "" {
		t.Fatalf("partial summary = %+v", result.Summary)
	}
	if result.Items[0].InventoryComplete || result.Items[1].InventoryComplete {
		t.Fatalf("partial items should report incomplete inventory = %+v", result.Items)
	}
	if len(result.Items) != 2 || result.Items[0].Key != "prefix/project/a" || result.Items[1].ObjectUrl != "s3://bucket/prefix/project/z" {
		t.Fatalf("normalized items = %+v", result.Items)
	}
	if len(inventory.requests) != 1 || inventory.requests[0].Prefix != "prefix/project" || !inventory.requests[0].IncludeHead {
		t.Fatalf("inventory requests = %+v", inventory.requests)
	}
}

func TestInspectProjectMarksCompleteInventoryItems(t *testing.T) {
	inventory := &fakeInventory{result: storage.InventoryResult{
		Items:    []storage.ObjectMetadata{{Key: "prefix/project/a", SizeBytes: 3}},
		Complete: true,
	}}
	service, _ := projectService(inventory, nil)
	result, err := service.InspectProjectStorage(context.Background(), "org", "project", InspectionOptions{Mode: ModeItems})
	if err != nil {
		t.Fatalf("InspectProjectStorage() error = %v", err)
	}
	if !result.Summary.InventoryComplete || len(result.Items) != 1 || !result.Items[0].InventoryComplete {
		t.Fatalf("complete inventory markers = summary:%v items:%+v", result.Summary.InventoryComplete, result.Items)
	}
}

func TestProbeObjectNormalizesScopedKeyAgainstEffectivePrefix(t *testing.T) {
	tests := []struct {
		name string
		key  string
		want string
	}{
		{name: "unqualified", key: "file.bin", want: "prefix/project/file.bin"},
		{name: "already qualified", key: "prefix/project/file.bin", want: "prefix/project/file.bin"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service, _ := projectService(&fakeInventory{}, nil)
			probe := &recordingProbe{}
			service.probe = probe
			metadata, err := service.ProbeObject(context.Background(), InspectRequest{Organization: "org", Project: "project", Key: tt.key})
			if err != nil {
				t.Fatalf("ProbeObject() error = %v", err)
			}
			if metadata.Key != tt.want || metadata.ObjectUrl != "s3://bucket/"+tt.want {
				t.Fatalf("metadata = %+v, want key %q", metadata, tt.want)
			}
			if len(probe.targets) != 1 || probe.targets[0].PhysicalBucket != "bucket" || probe.targets[0].Key != tt.want {
				t.Fatalf("probe targets = %+v, want key %q", probe.targets, tt.want)
			}
		})
	}
}

func TestProbeObjectNormalizesLegacyOrganizationPrefixAgainstComposedScope(t *testing.T) {
	service, _ := projectServiceWithTarget(&fakeInventory{}, nil, buckets.StorageScope{
		Provider: "s3", Bucket: "bucket", Prefix: "prefix/project", Prefixes: []string{"prefix", "project"},
		Credential: buckets.Credential{CredentialID: "cred", Bucket: "bucket", Provider: "s3"},
	})
	probe := &recordingProbe{}
	service.probe = probe

	metadata, err := service.ProbeObject(context.Background(), InspectRequest{Organization: "org", Project: "project", Key: "prefix/file.bin"})
	if err != nil {
		t.Fatalf("ProbeObject() error = %v", err)
	}
	if metadata.Key != "prefix/project/file.bin" || metadata.ObjectUrl != "s3://bucket/prefix/project/file.bin" {
		t.Fatalf("metadata = %+v, want key %q", metadata, "prefix/project/file.bin")
	}
	if len(probe.targets) != 1 || probe.targets[0].PhysicalBucket != "bucket" || probe.targets[0].Key != "prefix/project/file.bin" {
		t.Fatalf("probe targets = %+v, want key %q", probe.targets, "prefix/project/file.bin")
	}
}

func TestValidateInventoryDeduplicatesAndRestoresRequestOrder(t *testing.T) {
	inventory := &fakeInventory{result: storage.InventoryResult{Items: []storage.ObjectMetadata{{Key: "prefix/a.txt", SizeBytes: 10}}, Complete: true}}
	service, visibility := projectService(inventory, nil)
	expectedSize := int64(10)
	requests := []InspectRequest{
		{ID: "first", ObjectURL: "s3://bucket/prefix/a.txt", ExpectedSizeBytes: &expectedSize},
		{ID: "duplicate", ObjectURL: "s3://bucket/prefix/a.txt", ExpectedName: "wrong.txt"},
		{ID: "invalid", ObjectURL: "https://bucket/prefix/a.txt"},
	}
	results := service.ValidateInventoryObjects(context.Background(), requests)
	if visibility.called != 1 {
		t.Fatalf("visibility calls = %d, want one request-local lookup", visibility.called)
	}
	if len(inventory.requests) != 1 || inventory.requests[0].MaxKeys != 1 || !inventory.requests[0].ExactPrefix {
		t.Fatalf("inventory requests = %+v", inventory.requests)
	}
	if results[0].Status != "present" || results[0].ValidationStatus != "matched" || results[1].ValidationStatus != "mismatched" {
		t.Fatalf("validation results = %+v", results)
	}
	if results[2].Status != "invalid" || results[2].Id != "invalid" {
		t.Fatalf("invalid result = %+v", results[2])
	}
}

func TestDeleteProjectObjectsPreservesPolicyOrderAndConflictSafety(t *testing.T) {
	deletePort := &fakeDelete{}
	service, _ := projectService(&fakeInventory{}, deletePort)
	results := service.DeleteProjectObjects(context.Background(), "org", "project", []string{
		" s3://bucket/prefix/project/a ",
		"s3://bucket/prefix/project/a",
		"s3://bucket/other/b",
		"not-a-storage-url",
	})
	if len(results) != 3 || results[0].Status != "deleted" || results[1].Status != "forbidden" || results[2].Status != "invalid" {
		t.Fatalf("delete results = %+v", results)
	}
	if len(deletePort.locations) != 1 || deletePort.locations[0] != "s3://bucket/prefix/project/a" {
		t.Fatalf("delete locations = %+v", deletePort.locations)
	}
}

func TestDeleteProjectDataAuthorizedChecksBeforeAnyDeletion(t *testing.T) {
	objects := &fakeCleanupObjects{count: 3}
	scopes := &fakeCleanupScopes{scopes: []buckets.Scope{{Organization: "org", ProjectID: "project", CredentialID: "cred"}}}
	service := NewService(Dependencies{ObjectCleanup: objects, ScopeCatalog: scopes})
	session := access.NewSession("local")
	session.AuthzEnforced = true
	session.SetAuthorizations(nil, nil, true)
	ctx := access.WithSession(context.Background(), session)

	result, err := service.DeleteProjectDataAuthorized(ctx, " org ", " project ")
	if !errors.Is(err, errorapi.ErrAccessDenied) {
		t.Fatalf("DeleteProjectDataAuthorized() error = %v, want access denied", err)
	}
	if result.Organization != "org" || result.ProjectId != "project" {
		t.Fatalf("authorized result = %+v, want trimmed identifiers", result)
	}
	if len(objects.deleted) != 0 || len(scopes.deleted) != 0 {
		t.Fatalf("denied cleanup caused writes: objects=%v scopes=%v", objects.deleted, scopes.deleted)
	}
}

func TestDeleteProjectDataAuthorizedPreservesTrustedCleanupOrder(t *testing.T) {
	objects := &fakeCleanupObjects{count: 3}
	scopes := &fakeCleanupScopes{scopes: []buckets.Scope{{Organization: "org", ProjectID: "project", CredentialID: "cred"}}}
	service := NewService(Dependencies{ObjectCleanup: objects, ScopeCatalog: scopes})
	session := access.NewSession("local")
	session.AuthzEnforced = true
	session.SetAuthorizations(nil, map[string]map[string]bool{
		"/organization/org/project/project": {"delete": true},
	}, true)
	ctx := access.WithSession(context.Background(), session)

	result, err := service.DeleteProjectDataAuthorized(ctx, " org ", " project ")
	if err != nil {
		t.Fatalf("DeleteProjectDataAuthorized() error = %v", err)
	}
	if result.DeletedObjects != 3 || result.DeletedBucketScopes != 1 {
		t.Fatalf("authorized cleanup result = %+v", result)
	}
	if !reflect.DeepEqual(objects.deleted, []string{"org/project"}) || !reflect.DeepEqual(scopes.deleted, []string{"org/project/cred/"}) {
		t.Fatalf("cleanup writes = objects:%v scopes:%v", objects.deleted, scopes.deleted)
	}
}
