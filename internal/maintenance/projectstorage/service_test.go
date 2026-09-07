package projectstorage

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/calypr/syfon/internal/buckets"
	"github.com/calypr/syfon/internal/objects"
	"github.com/calypr/syfon/internal/storage"
)

type fakeScopes struct {
	values map[string]buckets.Scope
}

func (f fakeScopes) LookupBucketScope(_ context.Context, organization, project string) (buckets.Scope, bool, error) {
	scope, ok := f.values[strings.TrimSpace(organization)+"/"+strings.TrimSpace(project)]
	return scope, ok, nil
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
	targets []storage.ObjectTarget
}

func (f *recordingProbe) Probe(_ context.Context, targets []storage.ProbeTarget) []storage.ProbeResult {
	if len(targets) == 0 {
		return nil
	}
	target := targets[0].Target
	f.targets = append(f.targets, target)
	return []storage.ProbeResult{{ID: targets[0].ID, Target: target, Metadata: storage.ObjectMetadata{Bucket: target.Bucket, Key: target.Key}}}
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

type fakePhysical struct {
	records []objects.Record
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

func (f fakePhysical) ListPhysicalObjectsByScope(context.Context, string, string, string) ([]objects.Record, error) {
	return f.records, nil
}

func projectService(inventory *fakeInventory, deletePort DeletePort) (*Service, *fakeVisibility) {
	return projectServiceWithScopes(inventory, deletePort, map[string]buckets.Scope{
		"org/":        {Organization: "org", Bucket: "bucket", PathPrefix: "prefix"},
		"org/project": {Organization: "org", ProjectID: "project", Bucket: "bucket", PathPrefix: "prefix/project"},
	})
}

func projectServiceWithScopes(inventory *fakeInventory, deletePort DeletePort, scopes map[string]buckets.Scope) (*Service, *fakeVisibility) {
	credential := buckets.Credential{CredentialID: "cred", Bucket: "bucket", Provider: "s3"}
	visibility := &fakeVisibility{values: map[string]buckets.VisibleBucket{
		"cred": {Credential: credential},
	}}
	service := NewService(Dependencies{
		Scopes:      fakeScopes{values: scopes},
		Credentials: fakeCredentials{values: map[string]buckets.Credential{"cred": credential}},
		Visibility:  visibility,
		Inventory:   inventory,
		Delete:      deletePort,
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
	if len(result.Items) != 2 || result.Items[0].Key != "prefix/project/a" || result.Items[1].ObjectURL != "s3://bucket/prefix/project/z" {
		t.Fatalf("normalized items = %+v", result.Items)
	}
	if len(inventory.requests) != 1 || inventory.requests[0].Target.Prefix != "prefix/project" || !inventory.requests[0].IncludeHead {
		t.Fatalf("inventory requests = %+v", inventory.requests)
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
			if metadata.Key != tt.want || metadata.ObjectURL != "s3://bucket/"+tt.want {
				t.Fatalf("metadata = %+v, want key %q", metadata, tt.want)
			}
			if len(probe.targets) != 1 || probe.targets[0].Bucket != "bucket" || probe.targets[0].Key != tt.want {
				t.Fatalf("probe targets = %+v, want key %q", probe.targets, tt.want)
			}
		})
	}
}

func TestProbeObjectNormalizesLegacyOrganizationPrefixAgainstComposedScope(t *testing.T) {
	service, _ := projectServiceWithScopes(&fakeInventory{}, nil, map[string]buckets.Scope{
		"org/":        {Organization: "org", Bucket: "bucket", PathPrefix: "prefix"},
		"org/project": {Organization: "org", ProjectID: "project", Bucket: "bucket", PathPrefix: "project"},
	})
	probe := &recordingProbe{}
	service.probe = probe

	metadata, err := service.ProbeObject(context.Background(), InspectRequest{Organization: "org", Project: "project", Key: "prefix/file.bin"})
	if err != nil {
		t.Fatalf("ProbeObject() error = %v", err)
	}
	if metadata.Key != "prefix/project/file.bin" || metadata.ObjectURL != "s3://bucket/prefix/project/file.bin" {
		t.Fatalf("metadata = %+v, want key %q", metadata, "prefix/project/file.bin")
	}
	if len(probe.targets) != 1 || probe.targets[0].Bucket != "bucket" || probe.targets[0].Key != "prefix/project/file.bin" {
		t.Fatalf("probe targets = %+v, want key %q", probe.targets, "prefix/project/file.bin")
	}
}

func TestValidateInventoryDeduplicatesAndRestoresRequestOrder(t *testing.T) {
	inventory := &fakeInventory{result: storage.InventoryResult{Items: []storage.ObjectMetadata{{Key: "prefix/a.txt", SizeBytes: 10}}, Complete: true}}
	service, visibility := projectService(inventory, nil)
	requests := []ListValidationRequest{
		{ID: "first", ObjectURL: "s3://bucket/prefix/a.txt", ExpectedSizeBytes: int64Ptr(10)},
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
	if results[0].Status != ProbePresent || results[0].ValidationStatus != ValidationMatched || results[1].ValidationStatus != ValidationMismatched {
		t.Fatalf("validation results = %+v", results)
	}
	if results[2].Status != ProbeInvalid || results[2].ID != "invalid" {
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

func TestAuditProjectRecordsPreservesPhysicalDuplicatesAndSegmentPrefixes(t *testing.T) {
	first := objects.Record{Id: "one", Checksums: []objects.Checksum{{Type: "sha256", Checksum: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}}, AccessMethods: &[]objects.AccessMethod{{Type: "s3", AccessUrl: &objects.AccessURL{Url: "s3://bucket/prefix/project/CONFIG/file"}}}}
	duplicate := first
	duplicate.Id = "two"
	falsePrefix := objects.Record{Id: "three", Checksums: []objects.Checksum{{Type: "sha256", Checksum: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}}, AccessMethods: &[]objects.AccessMethod{{Type: "s3", AccessUrl: &objects.AccessURL{Url: "s3://bucket/prefix/project/CONFIGURATION/file"}}}}
	service, _ := projectService(&fakeInventory{}, nil)
	service.physical = fakePhysical{records: []objects.Record{first, duplicate, falsePrefix}}
	result, err := service.AuditProjectRecords(context.Background(), "org", "project", "CONFIG")
	if err != nil {
		t.Fatalf("AuditProjectRecords() error = %v", err)
	}
	if len(result) != 2 || result[0].ObjectID != "one" || result[1].ObjectID != "two" {
		t.Fatalf("audit records = %+v", result)
	}
}

func TestDeleteProjectDataDeletesObjectsBeforeMatchingScopes(t *testing.T) {
	objects := &fakeCleanupObjects{count: 3}
	scopes := &fakeCleanupScopes{scopes: []buckets.Scope{
		{Organization: "org", ProjectID: "project", CredentialID: "cred-a", PathPrefix: "prefix/a"},
		{Organization: "other", ProjectID: "project", CredentialID: "cred-b"},
		{Organization: "org", ProjectID: "project", Bucket: "bucket-c"},
	}}
	service := NewService(Dependencies{CleanupObjects: objects, CleanupScopes: scopes})
	result, err := service.DeleteProjectData(context.Background(), " org ", " project ")
	if err != nil {
		t.Fatalf("DeleteProjectData() error = %v", err)
	}
	if result.DeletedObjects != 3 || result.DeletedBucketScopes != 2 {
		t.Fatalf("cleanup result = %+v", result)
	}
	if !reflect.DeepEqual(objects.deleted, []string{"org/project"}) {
		t.Fatalf("object deletions = %v", objects.deleted)
	}
	if !reflect.DeepEqual(scopes.deleted, []string{"org/project/cred-a/prefix/a", "org/project/bucket-c/"}) {
		t.Fatalf("scope deletions = %v", scopes.deleted)
	}
}

func int64Ptr(value int64) *int64 { return &value }
