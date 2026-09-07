package repair

import (
	"context"
	"testing"

	"github.com/calypr/syfon/apigen/client/bucketapi"
	drsapi "github.com/calypr/syfon/apigen/client/drs"
	"github.com/calypr/syfon/apigen/client/internalapi"
	syfoncommon "github.com/calypr/syfon/common"
	intcommon "github.com/calypr/syfon/internal/common"
)

func TestLegacyAccessURLWithCanonicalSiblingIsRemovable(t *testing.T) {
	svc := NewService(
		&fakeIndex{records: []internalapi.InternalRecord{
			recordWithMethods(t, "did-1", "abc", []string{"/organization/HTAN_INT/project/BForePC"}, []string{
				"s3://bforepc/META/file.ndjson",
				"s3://bforepc/bforepc-prod/did-1/abc",
			}),
		}},
		fakeBucketsForScope(t, "HTAN_INT", "BForePC", "s3://bforepc/bforepc-prod"),
		&fakeStorageInspector{},
	)

	report, err := svc.Audit(context.Background(), Options{Organization: "HTAN_INT", Project: "BForePC"})
	if err != nil {
		t.Fatalf("audit failed: %v", err)
	}
	if len(report.Objects) != 1 {
		t.Fatalf("expected one object report, got %d", len(report.Objects))
	}
	obj := report.Objects[0]
	if !obj.AutoFixable {
		t.Fatal("expected autofixable object")
	}
	if obj.Findings[0].Kind != FindingLegacyAccessURLRemovable {
		t.Fatalf("expected removable finding, got %s", obj.Findings[0].Kind)
	}
}

func TestLegacyAccessURLWithoutCanonicalSiblingIsRewritable(t *testing.T) {
	svc := NewService(
		&fakeIndex{records: []internalapi.InternalRecord{
			recordWithMethods(t, "did-1", "abc", []string{"/organization/HTAN_INT/project/BForePC"}, []string{
				"s3://bforepc/META/file.ndjson",
			}),
		}},
		fakeBucketsForScope(t, "HTAN_INT", "BForePC", "s3://bforepc/bforepc-prod"),
		&fakeStorageInspector{},
	)

	report, err := svc.Audit(context.Background(), Options{Organization: "HTAN_INT", Project: "BForePC"})
	if err != nil {
		t.Fatalf("audit failed: %v", err)
	}
	if got := report.Objects[0].Findings[0].Kind; got != FindingLegacyAccessURLRewritable {
		t.Fatalf("expected rewritable finding, got %s", got)
	}
}

func TestPathStyleAccessURLWithCanonicalSiblingIsRemovable(t *testing.T) {
	resource, _ := syfoncommon.ResourcePath("HTAN_INT", "BForePC")
	// did is a real UUID
	did := "01208c9f-e188-5aec-a026-51cc31fabd23"
	// sha is a 64-char hex string
	sha := "52aa452e5a90a2c641f20fffbac0e899d79d9cc40a560fe76e931b7f37566bb2"
	svc := NewService(
		&fakeIndex{records: []internalapi.InternalRecord{
			recordWithMethods(t, did, sha, []string{resource}, []string{
				"s3://bforepc-prod/JHU/ashley_kiemen/hematoxylin_eosin_stain/Level_2/HTA201_3/HTA201_3_ndpis/HTA201_3_1_0145.ndpi",
				"s3://bforepc-prod/" + did + "/" + sha,
			}),
		}},
		fakeBucketsForScope(t, "HTAN_INT", "BForePC", "s3://bforepc-prod"),
		&fakeStorageInspector{},
	)

	report, err := svc.Audit(context.Background(), Options{Organization: "HTAN_INT", Project: "BForePC"})
	if err != nil {
		t.Fatalf("audit failed: %v", err)
	}
	if len(report.Objects) != 1 {
		t.Fatalf("expected one object report, got %d", len(report.Objects))
	}
	obj := report.Objects[0]
	if !obj.AutoFixable {
		t.Fatal("expected autofixable object")
	}
	if obj.Findings[0].Kind != FindingLegacyAccessURLRemovable {
		t.Fatalf("expected removable finding, got %s", obj.Findings[0].Kind)
	}
}

func TestPathStyleAccessURLWithoutCanonicalSiblingIsRewritable(t *testing.T) {
	resource, _ := syfoncommon.ResourcePath("HTAN_INT", "BForePC")
	did := "01208c9f-e188-5aec-a026-51cc31fabd23"
	sha := "52aa452e5a90a2c641f20fffbac0e899d79d9cc40a560fe76e931b7f37566bb2"
	svc := NewService(
		&fakeIndex{records: []internalapi.InternalRecord{
			recordWithMethods(t, did, sha, []string{resource}, []string{
				"s3://bforepc-prod/JHU/ashley_kiemen/hematoxylin_eosin_stain/Level_2/HTA201_3/HTA201_3_ndpis/HTA201_3_1_0145.ndpi",
			}),
		}},
		fakeBucketsForScope(t, "HTAN_INT", "BForePC", "s3://bforepc-prod"),
		&fakeStorageInspector{},
	)

	report, err := svc.Audit(context.Background(), Options{Organization: "HTAN_INT", Project: "BForePC"})
	if err != nil {
		t.Fatalf("audit failed: %v", err)
	}
	if len(report.Objects) != 1 {
		t.Fatalf("expected one object report, got %d", len(report.Objects))
	}
	obj := report.Objects[0]
	if !obj.AutoFixable {
		t.Fatal("expected autofixable object")
	}
	if obj.Findings[0].Kind != FindingLegacyAccessURLRewritable {
		t.Fatalf("expected rewritable finding, got %s", obj.Findings[0].Kind)
	}
}

func TestMissingControlledAccessRecoverableFromDeterministicScope(t *testing.T) {
	resource, _ := syfoncommon.ResourcePath("HTAN_INT", "BForePC")
	did, err := intcommon.MintObjectIDFromChecksum("abc", []string{resource})
	if err != nil {
		t.Fatalf("mint did: %v", err)
	}
	rec := recordWithMethods(t, did, "abc", nil, []string{"s3://bforepc/bforepc-prod/" + did + "/abc"})
	svc := NewService(
		&fakeIndex{records: []internalapi.InternalRecord{rec}},
		fakeBucketsForScope(t, "HTAN_INT", "BForePC", "s3://bforepc/bforepc-prod"),
		&fakeStorageInspector{},
	)

	report, err := svc.Audit(context.Background(), Options{Organization: "HTAN_INT", Project: "BForePC"})
	if err != nil {
		t.Fatalf("audit failed: %v", err)
	}
	if got := report.Objects[0].Findings[0].Kind; got != FindingMissingControlledAccess {
		t.Fatalf("expected missing controlled access finding, got %s", got)
	}
	if !report.Objects[0].AutoFixable {
		t.Fatal("expected autofixable object")
	}
}

func TestDuplicateSHAReportedWithoutAutofix(t *testing.T) {
	rec1 := recordWithMethods(t, "did-1", "abc", []string{"/organization/HTAN_INT/project/BForePC"}, []string{"s3://bforepc/bforepc-prod/did-1/abc"})
	rec2 := recordWithMethods(t, "did-2", "abc", []string{"/organization/HTAN_INT/project/BForePC"}, []string{"s3://bforepc/bforepc-prod/did-2/abc"})
	svc := NewService(
		&fakeIndex{records: []internalapi.InternalRecord{rec1, rec2}},
		fakeBucketsForScope(t, "HTAN_INT", "BForePC", "s3://bforepc/bforepc-prod"),
		&fakeStorageInspector{},
	)

	report, err := svc.Audit(context.Background(), Options{Organization: "HTAN_INT", Project: "BForePC"})
	if err != nil {
		t.Fatalf("audit failed: %v", err)
	}
	if len(report.Objects) != 2 {
		t.Fatalf("expected two object reports, got %d", len(report.Objects))
	}
	for _, obj := range report.Objects {
		found := false
		for _, finding := range obj.Findings {
			if finding.Kind == FindingDuplicateSHA256Sibling {
				found = true
				if finding.AutoFixable {
					t.Fatal("duplicate sibling should not be autofixable")
				}
			}
		}
		if !found {
			t.Fatalf("expected duplicate finding for %s", obj.ObjectID)
		}
	}
}

func TestApplyUpdatesAccessMethodsAndControlledAccess(t *testing.T) {
	resource, _ := syfoncommon.ResourcePath("HTAN_INT", "BForePC")
	did, err := intcommon.MintObjectIDFromChecksum("abc", []string{resource})
	if err != nil {
		t.Fatalf("mint did: %v", err)
	}
	rec := recordWithMethods(t, did, "abc", nil, []string{"s3://bforepc/META/file.ndjson"})
	idx := &fakeIndex{records: []internalapi.InternalRecord{rec}}
	svc := NewService(idx, fakeBucketsForScope(t, "HTAN_INT", "BForePC", "s3://bforepc/bforepc-prod"), &fakeStorageInspector{})

	result, err := svc.Apply(context.Background(), Options{Organization: "HTAN_INT", Project: "BForePC"})
	if err != nil {
		t.Fatalf("apply failed: %v", err)
	}
	if result.Mutated != 1 {
		t.Fatalf("expected one mutation, got %d", result.Mutated)
	}
	if len(idx.updated) != 1 {
		t.Fatalf("expected one updated record, got %d", len(idx.updated))
	}
	updated := idx.updated[0]
	if updated.ControlledAccess == nil || len(*updated.ControlledAccess) != 1 || (*updated.ControlledAccess)[0] != resource {
		t.Fatalf("expected controlled access backfill, got %#v", updated.ControlledAccess)
	}
	if got := accessMethodURLs(updated.AccessMethods); len(got) != 1 || got[0] != "s3://bforepc/bforepc-prod/"+did+"/abc" {
		t.Fatalf("unexpected access methods: %#v", got)
	}
}

func TestCheckStorageAddsMissingObjectFinding(t *testing.T) {
	rec := recordWithMethods(t, "did-1", "abc", []string{"/organization/HTAN_INT/project/BForePC"}, []string{"s3://bforepc/bforepc-prod/did-1/abc"})
	req := &fakeStorageInspector{
		err: ErrStorageObjectNotFound,
	}
	svc := NewService(&fakeIndex{records: []internalapi.InternalRecord{rec}}, fakeBucketsForScope(t, "HTAN_INT", "BForePC", "s3://bforepc/bforepc-prod"), req)

	report, err := svc.Audit(context.Background(), Options{Organization: "HTAN_INT", Project: "BForePC", CheckStorage: true})
	if err != nil {
		t.Fatalf("audit failed: %v", err)
	}
	found := false
	for _, finding := range report.Objects[0].Findings {
		if finding.Kind == FindingStorageObjectMissing {
			found = true
		}
	}
	if !found {
		t.Fatal("expected storage object missing finding")
	}
	if req.calls == 0 {
		t.Fatal("expected storage inspect call")
	}
}

func TestCheckStorageWithNilInspectorSkipsProbes(t *testing.T) {
	rec := recordWithMethods(t, "did-1", "abc", []string{"/organization/HTAN_INT/project/BForePC"}, []string{"s3://bforepc/bforepc-prod/did-1/abc"})
	svc := NewService(
		&fakeIndex{records: []internalapi.InternalRecord{rec}},
		fakeBucketsForScope(t, "HTAN_INT", "BForePC", "s3://bforepc/bforepc-prod"),
		nil,
	)

	report, err := svc.Audit(context.Background(), Options{Organization: "HTAN_INT", Project: "BForePC", CheckStorage: true})
	if err != nil {
		t.Fatalf("audit failed with nil storage inspector: %v", err)
	}
	if len(report.Objects) != 0 {
		t.Fatalf("expected no findings when storage inspection is unavailable, got %d", len(report.Objects))
	}
}

func TestSingleControlledAccessWithoutScopeTargetDoesNotPanicOrMutate(t *testing.T) {
	resource, _ := syfoncommon.ResourcePath("HTAN_INT", "BForePC")
	rec := recordWithMethods(t, "did-1", "abc", []string{resource}, []string{"s3://bforepc/legacy/file.ndjson"})
	idx := &fakeIndex{records: []internalapi.InternalRecord{rec}}
	svc := NewService(
		idx,
		&fakeBuckets{
			list: bucketapi.BucketsResponse{
				S3BUCKETS: map[string]bucketapi.BucketMetadata{
					"other-bucket": {},
				},
			},
			scopes: map[string][]bucketapi.BucketScopeResponse{
				"other-bucket": nil,
			},
		},
		&fakeStorageInspector{},
	)

	report, err := svc.Audit(context.Background(), Options{Organization: "HTAN_INT", Project: "BForePC"})
	if err != nil {
		t.Fatalf("audit failed: %v", err)
	}
	if len(report.Objects) != 0 {
		t.Fatalf("expected no findings without a canonical target, got %d", len(report.Objects))
	}

	result, err := svc.Apply(context.Background(), Options{Organization: "HTAN_INT", Project: "BForePC"})
	if err != nil {
		t.Fatalf("apply failed: %v", err)
	}
	if result.Mutated != 0 || result.AutoFixable != 0 {
		t.Fatalf("expected no mutations without a canonical target, got mutated=%d autofixable=%d", result.Mutated, result.AutoFixable)
	}
	if len(idx.updated) != 0 {
		t.Fatalf("expected no updates, got %d", len(idx.updated))
	}
}

type fakeIndex struct {
	records   []internalapi.InternalRecord
	updated   []internalapi.InternalRecord
	listErr   error
	updateErr error
}

func (f *fakeIndex) List(ctx context.Context, opts ListRecordsOptions) (internalapi.ListRecordsResponse, error) {
	if f.listErr != nil {
		return internalapi.ListRecordsResponse{}, f.listErr
	}
	records := append([]internalapi.InternalRecord(nil), f.records...)
	return internalapi.ListRecordsResponse{Records: &records}, nil
}

func (f *fakeIndex) Update(ctx context.Context, did string, rec internalapi.InternalRecord) (internalapi.InternalRecordResponse, error) {
	if f.updateErr != nil {
		return internalapi.InternalRecordResponse{}, f.updateErr
	}
	f.updated = append(f.updated, rec)
	return internalapi.InternalRecordResponse{Did: did}, nil
}

type fakeBuckets struct {
	list   bucketapi.BucketsResponse
	scopes map[string][]bucketapi.BucketScopeResponse
}

func (f *fakeBuckets) List(ctx context.Context) (bucketapi.BucketsResponse, error) {
	return f.list, nil
}

func (f *fakeBuckets) ListScopes(ctx context.Context, bucket string) ([]bucketapi.BucketScopeResponse, error) {
	return f.scopes[bucket], nil
}

type fakeStorageInspector struct {
	err   error
	calls int
}

func (f *fakeStorageInspector) Inspect(ctx context.Context, req StorageInspectRequest) (StorageInspectResult, error) {
	f.calls++
	if f.err != nil {
		return StorageInspectResult{}, f.err
	}
	return StorageInspectResult{ObjectURL: req.ObjectURL}, nil
}

func recordWithMethods(t *testing.T, did, sha string, controlled []string, urls []string) internalapi.InternalRecord {
	t.Helper()
	hashes := internalapi.HashInfo{"sha256": sha}
	methods := make([]drsapi.AccessMethod, 0, len(urls))
	for _, raw := range urls {
		accessURL := raw
		methods = append(methods, drsapi.AccessMethod{
			Type: drsapi.AccessMethodTypeS3,
			AccessUrl: &struct {
				Headers *[]string `json:"headers,omitempty"`
				Url     string    `json:"url"`
			}{Url: accessURL},
		})
	}
	rec := internalapi.InternalRecord{
		Did:           did,
		Hashes:        &hashes,
		AccessMethods: &methods,
	}
	if controlled != nil {
		c := append([]string(nil), controlled...)
		rec.ControlledAccess = &c
	}
	return rec
}

func fakeBucketsForScope(t *testing.T, org, project, rawPath string) *fakeBuckets {
	t.Helper()
	return &fakeBuckets{
		list: bucketapi.BucketsResponse{
			S3BUCKETS: map[string]bucketapi.BucketMetadata{
				"bforepc": {},
			},
		},
		scopes: map[string][]bucketapi.BucketScopeResponse{
			"bforepc": {{
				Organization: org,
				ProjectId:    project,
				Path:         &rawPath,
			}},
		},
	}
}

var _ StorageInspector = (*fakeStorageInspector)(nil)
