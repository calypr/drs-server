package storage

import (
	"context"
	"errors"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/calypr/syfon/internal/buckets"
)

type fakeLookup struct {
	credentials map[string]*buckets.Credential
	errors      map[string]error
	queries     []string
}

func (f *fakeLookup) GetS3Credential(_ context.Context, bucket string) (*buckets.Credential, error) {
	f.queries = append(f.queries, bucket)
	if err := f.errors[bucket]; err != nil {
		return nil, err
	}
	return f.credentials[bucket], nil
}

type fakeBackend struct {
	provider string

	accessTargets  []ObjectTarget
	accessOptions  []AccessOptions
	partTargets    []MultipartPartRequest
	completeTarget []CompleteMultipartRequest
	probes         [][]ProbeTarget
	inventories    []InventoryRequest
	deletions      [][]PhysicalTarget
	invalidations  []string

	probeResult     func([]ProbeTarget) []ProbeResult
	inventoryResult InventoryResult
	inventoryErr    error
	deleteErr       error
}

type bareBackend struct{}

func (bareBackend) SignURL(context.Context, ObjectTarget, AccessOptions) (Access, error) {
	return Access{}, nil
}

func (bareBackend) SignDownloadPart(context.Context, ObjectTarget, ByteRange, AccessOptions) (Access, error) {
	return Access{}, nil
}

func (bareBackend) InitMultipartUpload(context.Context, ObjectTarget) (UploadID, error) {
	return "", nil
}

func (bareBackend) SignMultipartPart(context.Context, MultipartPartRequest) (Access, error) {
	return Access{}, nil
}

func (bareBackend) CompleteMultipartUpload(context.Context, CompleteMultipartRequest) error {
	return nil
}

func (f *fakeBackend) SignURL(_ context.Context, target ObjectTarget, options AccessOptions) (Access, error) {
	f.accessTargets = append(f.accessTargets, target)
	f.accessOptions = append(f.accessOptions, options)
	return Access{Location: f.provider + "://" + target.Bucket + "/" + target.Key}, nil
}

func (f *fakeBackend) SignDownloadPart(_ context.Context, target ObjectTarget, _ ByteRange, options AccessOptions) (Access, error) {
	f.accessTargets = append(f.accessTargets, target)
	f.accessOptions = append(f.accessOptions, options)
	return Access{Location: f.provider + "://range/" + target.Key}, nil
}

func (f *fakeBackend) InitMultipartUpload(_ context.Context, target ObjectTarget) (UploadID, error) {
	f.accessTargets = append(f.accessTargets, target)
	return UploadID("upload"), nil
}

func (f *fakeBackend) SignMultipartPart(_ context.Context, request MultipartPartRequest) (Access, error) {
	f.partTargets = append(f.partTargets, request)
	return Access{Location: "part"}, nil
}

func (f *fakeBackend) CompleteMultipartUpload(_ context.Context, request CompleteMultipartRequest) error {
	f.completeTarget = append(f.completeTarget, request)
	return nil
}

func (f *fakeBackend) InvalidateBucket(bucket string) {
	f.invalidations = append(f.invalidations, bucket)
}

func (f *fakeBackend) Probe(_ context.Context, targets []ProbeTarget) []ProbeResult {
	f.probes = append(f.probes, append([]ProbeTarget(nil), targets...))
	if f.probeResult != nil {
		return f.probeResult(targets)
	}
	results := make([]ProbeResult, len(targets))
	for i, target := range targets {
		results[i] = ProbeResult{ID: target.ID, Target: target.Target}
	}
	return results
}

func (f *fakeBackend) Inventory(_ context.Context, request InventoryRequest) (InventoryResult, error) {
	f.inventories = append(f.inventories, request)
	return f.inventoryResult, f.inventoryErr
}

func (f *fakeBackend) Delete(_ context.Context, targets []PhysicalTarget) error {
	f.deletions = append(f.deletions, append([]PhysicalTarget(nil), targets...))
	return f.deleteErr
}

func credential(provider, bucket string) *buckets.Credential {
	return &buckets.Credential{Provider: provider, Bucket: bucket}
}

func managerWithBackends(t *testing.T, lookup *fakeLookup, backends ...*fakeBackend) *Manager {
	t.Helper()
	registrations := make([]Registration, 0, len(backends))
	for _, backend := range backends {
		registrations = append(registrations, NewRegistration(backend.provider, backend))
	}
	manager, err := NewManager(lookup, registrations...)
	if err != nil {
		t.Fatalf("NewManager returned error: %v", err)
	}
	return manager
}

func TestNewManagerRejectsInvalidAndDuplicateRegistrations(t *testing.T) {
	lookup := &fakeLookup{}
	backend := &fakeBackend{provider: "s3"}
	cases := []struct {
		name          string
		registrations []Registration
	}{
		{name: "blank", registrations: []Registration{NewRegistration(" ", backend)}},
		{name: "invalid", registrations: []Registration{NewRegistration("swift", backend)}},
		{name: "duplicate canonical provider", registrations: []Registration{NewRegistration("s3", backend), NewRegistration("S3", &fakeBackend{provider: "s3"})}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := NewManager(lookup, tc.registrations...); err == nil {
				t.Fatal("expected registration validation error")
			}
		})
	}
}

func TestNewManagerRejectsTypedNilBackend(t *testing.T) {
	var backend *fakeBackend
	if _, err := NewManager(&fakeLookup{}, NewRegistration("s3", backend)); err == nil {
		t.Fatal("expected typed nil backend error")
	}
}

func TestNewRegistrationSnapshotsCapabilitiesAndManagerHasNoMutationPath(t *testing.T) {
	backend := &fakeBackend{provider: "s3"}
	manager := managerWithBackends(t, &fakeLookup{}, backend)
	if len(manager.providers) != 1 {
		t.Fatalf("provider count = %d, want 1", len(manager.providers))
	}
	if _, ok := any(manager).(interface{ Register(string, Registration) }); ok {
		t.Fatal("manager unexpectedly exposes a registration mutator")
	}
	registration := manager.providers["s3"]
	if registration.prober == nil || registration.inventory == nil || registration.deleter == nil || registration.invalidator == nil {
		t.Fatal("optional capabilities were not snapshotted")
	}
}

func TestAccessUsesCandidateOrderAndPreservesOriginalHost(t *testing.T) {
	lookup := &fakeLookup{
		credentials: map[string]*buckets.Credential{"logical": credential("gcs", "physical")},
		errors:      map[string]error{"url-bucket": errors.New("stale URL lookup")},
	}
	backend := &fakeBackend{provider: "gcs"}
	manager := managerWithBackends(t, lookup, backend)
	access, err := manager.Access(context.Background(), AccessRequest{
		Target:  AccessTarget{AccessID: "logical", Location: "s3://url-bucket/object"},
		Options: AccessOptions{Method: "GET"},
	})
	if err != nil {
		t.Fatalf("Access returned error: %v", err)
	}
	if got, want := lookup.queries, []string{"url-bucket", "logical"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("credential lookup order = %#v, want %#v", got, want)
	}
	if got, want := backend.accessTargets[0], (ObjectTarget{Bucket: "url-bucket", Key: "object"}); got != want {
		t.Fatalf("backend target = %#v, want %#v", got, want)
	}
	if !strings.HasPrefix(access.Location, "gcs://") {
		t.Fatalf("unexpected access location %q", access.Location)
	}
}

func TestAccessFallsBackToSchemeAndDefaultProvider(t *testing.T) {
	lookup := &fakeLookup{errors: map[string]error{"bucket": errors.New("missing"), "access": errors.New("missing")}}
	gcs := &fakeBackend{provider: "gcs"}
	s3 := &fakeBackend{provider: "s3"}
	manager := managerWithBackends(t, lookup, gcs, s3)
	if _, err := manager.Access(context.Background(), AccessRequest{Target: AccessTarget{AccessID: "access", Location: "gs://bucket/key"}}); err != nil {
		t.Fatalf("scheme fallback returned error: %v", err)
	}
	if len(gcs.accessTargets) != 1 {
		t.Fatalf("scheme fallback selected %d gcs calls, want 1", len(gcs.accessTargets))
	}
	lookup.errors = map[string]error{"bucket": errors.New("missing")}
	if _, err := manager.Access(context.Background(), AccessRequest{Target: AccessTarget{Location: "https://bucket/key"}}); err != nil {
		t.Fatalf("default fallback returned error: %v", err)
	}
	if len(s3.accessTargets) != 1 {
		t.Fatalf("default fallback selected %d s3 calls, want 1", len(s3.accessTargets))
	}
}

func TestMultipartResolvesProviderStrictlyFromBucket(t *testing.T) {
	lookup := &fakeLookup{credentials: map[string]*buckets.Credential{"bucket": credential("gcs", "bucket")}}
	gcs := &fakeBackend{provider: "gcs"}
	manager := managerWithBackends(t, lookup, gcs)
	request := MultipartPartRequest{Target: ObjectTarget{Bucket: "bucket", Key: "object"}, UploadID: "upload", PartNumber: 3}
	if _, err := manager.AccessMultipartPart(context.Background(), request); err != nil {
		t.Fatalf("AccessMultipartPart returned error: %v", err)
	}
	if got := gcs.partTargets[0]; !reflect.DeepEqual(got, request) {
		t.Fatalf("multipart request = %#v, want %#v", got, request)
	}
}

func TestMissingCapabilitiesReturnTypedErrors(t *testing.T) {
	lookup := &fakeLookup{credentials: map[string]*buckets.Credential{"bucket": credential("s3", "bucket")}}
	manager, err := NewManager(lookup, NewRegistration("s3", bareBackend{}))
	if err != nil {
		t.Fatalf("NewManager returned error: %v", err)
	}
	probe := manager.Probe(context.Background(), []ProbeTarget{{ID: "one", Target: ObjectTarget{Bucket: "bucket", Key: "key"}}})
	var probeErr *OperationError
	if len(probe) != 1 || !errors.As(probe[0].Err, &probeErr) || probeErr.Kind != ErrorUnsupported {
		t.Fatalf("probe error = %#v, want typed unsupported error", probe[0].Err)
	}
	_, err = manager.Inventory(context.Background(), InventoryRequest{Target: PrefixTarget{Bucket: "bucket"}})
	var inventoryErr *OperationError
	if !errors.As(err, &inventoryErr) || inventoryErr.Kind != ErrorUnsupported {
		t.Fatalf("inventory error = %#v, want typed unsupported error", err)
	}
}

func TestProbeGroupsByProviderAndRestoresInputOrder(t *testing.T) {
	lookup := &fakeLookup{credentials: map[string]*buckets.Credential{
		"s3-bucket":  credential("s3", "s3-bucket"),
		"gcs-bucket": credential("gcs", "gcs-bucket"),
	}}
	s3 := &fakeBackend{provider: "s3", probeResult: func(targets []ProbeTarget) []ProbeResult {
		results := make([]ProbeResult, len(targets))
		for i, target := range targets {
			results[i] = ProbeResult{
				ID:       "spoofed-" + target.ID,
				Target:   ObjectTarget{Bucket: "spoofed-bucket", Key: "spoofed-key"},
				Metadata: ObjectMetadata{Bucket: target.Target.Bucket, Key: target.Target.Key},
				Err:      errors.New("provider probe result"),
			}
		}
		return results
	}}
	gcs := &fakeBackend{provider: "gcs", probeResult: s3.probeResult}
	manager := managerWithBackends(t, lookup, s3, gcs)
	targets := []ProbeTarget{
		{ID: "g1", Target: ObjectTarget{Bucket: "gcs-bucket", Key: "a"}},
		{ID: "s1", Target: ObjectTarget{Bucket: "s3-bucket", Key: "b"}},
		{ID: "g2", Target: ObjectTarget{Bucket: "gcs-bucket", Key: "c"}},
	}
	results := manager.Probe(context.Background(), targets)
	if got := []string{results[0].ID, results[1].ID, results[2].ID}; !reflect.DeepEqual(got, []string{"g1", "s1", "g2"}) {
		t.Fatalf("result IDs = %#v", got)
	}
	if got := []ObjectTarget{results[0].Target, results[1].Target, results[2].Target}; !reflect.DeepEqual(got, []ObjectTarget{targets[0].Target, targets[1].Target, targets[2].Target}) {
		t.Fatalf("result targets = %#v, want %#v", got, []ObjectTarget{targets[0].Target, targets[1].Target, targets[2].Target})
	}
	if results[1].Metadata.Key != "b" {
		t.Fatalf("result metadata = %#v, want provider metadata", results[1].Metadata)
	}
	if results[1].Err == nil || results[1].Err.Error() != "provider probe result" {
		t.Fatalf("result error = %v, want provider error", results[1].Err)
	}
	if len(gcs.probes) != 1 || len(gcs.probes[0]) != 2 || len(s3.probes) != 1 || len(s3.probes[0]) != 1 {
		t.Fatalf("probe groups = gcs %#v, s3 %#v", gcs.probes, s3.probes)
	}
}

func TestInventoryReturnsRawBackendResultAndError(t *testing.T) {
	sentinel := errors.New("partial listing")
	lookup := &fakeLookup{credentials: map[string]*buckets.Credential{"bucket": credential("s3", "bucket")}}
	backend := &fakeBackend{provider: "s3", inventoryResult: InventoryResult{Items: []ObjectMetadata{{Bucket: "bucket", Key: "key"}}, Complete: false}, inventoryErr: sentinel}
	manager := managerWithBackends(t, lookup, backend)
	result, err := manager.Inventory(context.Background(), InventoryRequest{Target: PrefixTarget{Bucket: "bucket", Prefix: "prefix"}, IncludeHead: true, ExactPrefix: true, MaxKeys: 2})
	if !errors.Is(err, sentinel) || !reflect.DeepEqual(result, backend.inventoryResult) {
		t.Fatalf("inventory = %#v, %v; want %#v, %v", result, err, backend.inventoryResult, sentinel)
	}
}

func TestDeleteExactPreservesPhysicalTargetsAndGroupsByProvider(t *testing.T) {
	lookup := &fakeLookup{credentials: map[string]*buckets.Credential{
		"s3-a":            credential("s3", "s3-a"),
		"s3-b":            credential("s3", "s3-b"),
		"gcs-bucket":      credential("gcs", "gcs-bucket"),
		"override-bucket": credential("gcs", "canonical-bucket"),
		"azure-bucket":    credential("azure", "azure-bucket"),
	}}
	s3 := &fakeBackend{provider: "s3"}
	gcs := &fakeBackend{provider: "gcs"}
	azure := &fakeBackend{provider: "azure"}
	file := &fakeBackend{provider: "file"}
	manager := managerWithBackends(t, lookup, gcs, azure, file, s3)
	filePath := "/tmp/syfon-storage-object"
	if err := manager.DeleteExact(context.Background(), []DeleteTarget{
		{Location: "s3://s3-b/key-2"},
		{Location: "gs://gcs-bucket/gcs-key"},
		{Location: "s3://override-bucket//physical/key/"},
		{Location: "s3://s3-a/key-2"},
		{Location: filePath},
		{Location: "azblob://azure-bucket/azure-key"},
		{Location: "s3://s3-a/key-1"},
		{Location: "gs://gcs-bucket/gcs-key"},
		{Location: filePath},
		{Location: "s3://s3-b/key-1"},
		{Location: "https://ignored.example/object"},
	}); err != nil {
		t.Fatalf("DeleteExact returned error: %v", err)
	}
	if got, want := gcs.deletions, [][]PhysicalTarget{
		{{Provider: "gcs", Bucket: "gcs-bucket", Key: "gcs-key"}},
		{{Provider: "gcs", Bucket: "override-bucket", Key: "physical/key"}},
	}; !reflect.DeepEqual(got, want) {
		t.Fatalf("gcs targets = %#v, want %#v", got, want)
	}
	if got, want := file.deletions, [][]PhysicalTarget{{{Provider: "file", Path: filePath}}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("file targets = %#v, want %#v", got, want)
	}
	if got, want := azure.deletions, [][]PhysicalTarget{{{Provider: "azure", Bucket: "azure-bucket", Key: "azure-key"}}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("azure targets = %#v, want %#v", got, want)
	}
	if got, want := s3.deletions, [][]PhysicalTarget{
		{{Provider: "s3", Bucket: "s3-a", Key: "key-1"}, {Provider: "s3", Bucket: "s3-a", Key: "key-2"}},
		{{Provider: "s3", Bucket: "s3-b", Key: "key-1"}, {Provider: "s3", Bucket: "s3-b", Key: "key-2"}},
	}; !reflect.DeepEqual(got, want) {
		t.Fatalf("s3 targets = %#v, want %#v", got, want)
	}
}

func TestDeleteExactAllowsNilCloudCredentialUntilProviderDispatch(t *testing.T) {
	for _, tc := range []struct {
		name     string
		scheme   string
		provider string
	}{
		{name: "s3", scheme: "s3", provider: "s3"},
		{name: "gcs", scheme: "gs", provider: "gcs"},
		{name: "azure", scheme: "azblob", provider: "azure"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			backend := &fakeBackend{provider: tc.provider}
			manager := managerWithBackends(t, &fakeLookup{}, backend)
			if err := manager.DeleteExact(context.Background(), []DeleteTarget{{Location: tc.scheme + "://bucket/key"}}); err != nil {
				t.Fatalf("DeleteExact returned error: %v", err)
			}
			if len(backend.deletions) != 1 || len(backend.deletions[0]) != 1 {
				t.Fatalf("deletion calls = %#v, want one target", backend.deletions)
			}
		})
	}

	file := &fakeBackend{provider: "file"}
	manager := managerWithBackends(t, &fakeLookup{}, file)
	err := manager.DeleteExact(context.Background(), []DeleteTarget{{Location: "file://bucket/key"}})
	var operationErr *OperationError
	if !errors.As(err, &operationErr) || operationErr.Kind != ErrorNotFound {
		t.Fatalf("file nil credential error = %v, want typed not-found error", err)
	}
}

func TestDeleteExactParsesAllTargetsBeforeDispatch(t *testing.T) {
	backend := &fakeBackend{provider: "gcs"}
	lookup := &fakeLookup{credentials: map[string]*buckets.Credential{"bucket": credential("gcs", "bucket")}}
	manager := managerWithBackends(t, lookup, backend)
	err := manager.DeleteExact(context.Background(), []DeleteTarget{
		{Location: "gs://bucket/key"},
		{Location: "gs://%gh&%ij/key"},
	})
	if err == nil {
		t.Fatal("expected malformed target error")
	}
	if len(backend.deletions) != 0 {
		t.Fatalf("dispatches after parse failure = %#v, want none", backend.deletions)
	}
}

func TestInvalidateBucketUsesRegistrationOrderAndTrimmedToken(t *testing.T) {
	first := &fakeBackend{provider: "s3"}
	second := &fakeBackend{provider: "gcs"}
	manager := managerWithBackends(t, &fakeLookup{}, first, second)
	manager.InvalidateBucket("  physical-bucket  ")
	if got, want := first.invalidations, []string{"physical-bucket"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("first invalidations = %#v, want %#v", got, want)
	}
	if got, want := second.invalidations, []string{"physical-bucket"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("second invalidations = %#v, want %#v", got, want)
	}
	manager.InvalidateBucket(" \t")
	if len(first.invalidations) != 1 || len(second.invalidations) != 1 {
		t.Fatal("blank invalidation was dispatched")
	}
}

func TestStorageHelpersPreserveExistingBehavior(t *testing.T) {
	parts := []CompletedPart{{PartNumber: 3}, {PartNumber: 1}, {PartNumber: 2}}
	if got := NormalizedMultipartParts(parts); !reflect.DeepEqual(got, []CompletedPart{{PartNumber: 1}, {PartNumber: 2}, {PartNumber: 3}}) {
		t.Fatalf("normalized parts = %#v", got)
	}
	if got, want := MultipartPartObjectKey("/nested/object", UploadID("upload"), 2), ".syfon-multipart/upload/nested/object/parts/2"; got != want {
		t.Fatalf("multipart part key = %q, want %q", got, want)
	}
	if got, want := DownloadFilename(`nested\\report.txt`), "report.txt"; got != want {
		t.Fatalf("download filename = %q, want %q", got, want)
	}
	if got, want := ContentDispositionAttachment("nested/report final.txt"), `attachment; filename="report final.txt"; filename*=UTF-8''report%20final.txt`; got != want {
		t.Fatalf("content disposition = %q, want %q", got, want)
	}
	if !sort.SliceIsSorted(NormalizedMultipartParts(parts), func(i, j int) bool {
		return NormalizedMultipartParts(parts)[i].PartNumber < NormalizedMultipartParts(parts)[j].PartNumber
	}) {
		t.Fatal("multipart helper did not sort by part number")
	}
}
