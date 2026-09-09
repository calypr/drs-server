package storage

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/calypr/syfon/apigen/errorapi"
	"github.com/calypr/syfon/internal/access"
	"github.com/calypr/syfon/internal/buckets"
	"github.com/calypr/syfon/internal/objects"
	providerstorage "github.com/calypr/syfon/internal/storage"
	"github.com/calypr/syfon/internal/storage/address"
)

type fakeRepairRecords struct {
	pages    [][]objects.Record
	queries  []repairQuery
	ids      []objects.RecordID
	updates  []objects.Record
	failNext bool
	collapse []string
}

type repairQuery struct {
	organization string
	project      string
	method       string
	start        string
	limit        int
	offset       int
}

func (f *fakeRepairRecords) ListPreparedObjectsPageByScope(_ context.Context, organization, project, method, start string, limit, offset int) ([]objects.Record, error) {
	f.queries = append(f.queries, repairQuery{organization: organization, project: project, method: method, start: start, limit: limit, offset: offset})
	index := len(f.queries) - 1
	if index >= len(f.pages) {
		return nil, nil
	}
	return f.pages[index], nil
}

func (f *fakeRepairRecords) UpdateRecord(_ context.Context, id string, update objects.Record, _ *int64, _ time.Time) (objects.Record, error) {
	f.ids = append(f.ids, objects.RecordID(id))
	f.updates = append(f.updates, update)
	if f.failNext {
		f.failNext = false
		return objects.Record{}, errors.New("write failed")
	}
	return update, nil
}

func (f *fakeRepairRecords) CollapseProjectChecksumDuplicates(_ context.Context, organization, project string) (int, error) {
	f.collapse = append(f.collapse, strings.TrimSpace(organization)+"/"+strings.TrimSpace(project))
	return 0, nil
}

type fakeRepairBuckets struct {
	credentials []buckets.Credential
	scopes      map[string][]buckets.Scope
}

func (f fakeRepairBuckets) ListS3Credentials(context.Context) ([]buckets.Credential, error) {
	return f.credentials, nil
}

func (f fakeRepairBuckets) ListBucketScopes(context.Context) ([]buckets.Scope, error) {
	var result []buckets.Scope
	for _, scopes := range f.scopes {
		result = append(result, scopes...)
	}
	return result, nil
}

func (f fakeRepairBuckets) GetS3Credential(_ context.Context, bucket string) (*buckets.Credential, error) {
	for _, credential := range f.credentials {
		if strings.EqualFold(strings.TrimSpace(bucket), strings.TrimSpace(credential.Bucket)) || strings.EqualFold(strings.TrimSpace(bucket), strings.TrimSpace(credential.CredentialID)) {
			copy := credential
			return &copy, nil
		}
	}
	return nil, errors.New("credential not found")
}

func (f fakeRepairBuckets) ListVisibleBuckets(context.Context) (map[string]buckets.VisibleBucket, error) {
	visible := make(map[string]buckets.VisibleBucket, len(f.credentials))
	for _, credential := range f.credentials {
		visible[credential.Bucket] = buckets.VisibleBucket{Credential: credential}
	}
	return visible, nil
}

type fakeRepairProbe struct {
	missing       map[string]bool
	calls         []string
	providerError error
}

func (f *fakeRepairProbe) Probe(_ context.Context, targets []providerstorage.ProbeTarget) []providerstorage.ProbeResult {
	if len(targets) == 0 {
		return nil
	}
	target := targets[0].Target
	objectURL := address.BucketToURL(target.PhysicalBucket, target.Key)
	f.calls = append(f.calls, objectURL)
	if f.providerError != nil {
		return []providerstorage.ProbeResult{{Target: target, Err: f.providerError}}
	}
	if f.missing[objectURL] {
		return []providerstorage.ProbeResult{{Target: target, Err: &providerstorage.OperationError{Kind: providerstorage.ErrorNotFound, Provider: "s3"}}}
	}
	return []providerstorage.ProbeResult{{Target: target, Metadata: providerstorage.ObjectMetadata{Provider: "s3", Bucket: target.PhysicalBucket, Key: target.Key}}}
}

func repairBuckets() fakeRepairBuckets {
	return fakeRepairBuckets{
		credentials: []buckets.Credential{
			{CredentialID: "s3-credential", Bucket: "repair-bucket", Provider: "s3"},
			{CredentialID: "gcs-credential", Bucket: "ignored-bucket", Provider: "gcs"},
		},
		scopes: map[string][]buckets.Scope{
			"repair-bucket":  {{Organization: "org", ProjectID: "project", Bucket: "repair-bucket", PathPrefix: "prefix"}},
			"ignored-bucket": {{Organization: "org", ProjectID: "project", Bucket: "ignored-bucket", PathPrefix: "wrong"}},
		},
	}
}

func repairInspector(buckets fakeRepairBuckets, probe *fakeRepairProbe) *Inspector {
	return &Inspector{credentials: buckets, visibility: buckets, probe: probe}
}

func repairRecord(id, sha, accessURL string) objects.Record {
	resource := "/programs/org/projects/project"
	controlled := []string{resource}
	methods := []objects.AccessMethod{{Type: "s3", AccessUrl: &objects.AccessURL{Url: accessURL}}}
	name := "file.txt"
	return objects.Record{Id: objects.RecordID(id), Checksums: []objects.Checksum{{Type: "sha256", Checksum: sha}}, ControlledAccess: &controlled, AccessMethods: &methods, Name: &name}
}

func TestRepairAuditUsesS3ScopeAndPreservesCanonicalReport(t *testing.T) {
	records := &fakeRepairRecords{pages: [][]objects.Record{{repairRecord("did-1", strings.Repeat("a", 64), "s3://repair-bucket/legacy")}, nil}}
	service := NewRepairService(records, repairBuckets(), nil)
	state, err := service.audit(context.Background(), RepairOptions{Organization: " org ", Project: " project ", PageSize: 1})
	if err != nil {
		t.Fatalf("audit() error = %v", err)
	}
	if state.report.Scanned != 1 || len(state.report.Objects) != 1 {
		t.Fatalf("report = %+v", state.report)
	}
	object := state.report.Objects[0]
	wantURL := "s3://repair-bucket/prefix/did-1/" + strings.Repeat("a", 64)
	if object.ProposedCanonicalURL != wantURL || object.Findings[0].Kind != FindingLegacyAccessURLRewritable || !object.AutoFixable {
		t.Fatalf("object report = %+v", object)
	}
	if len(records.queries) != 2 || records.queries[1].start != "did-1" || records.queries[0].limit != 1 {
		t.Fatalf("prepared queries = %+v", records.queries)
	}
}

func TestRepairApplyCollapsesBeforeAuditAndContinuesAfterWriteFailure(t *testing.T) {
	records := &fakeRepairRecords{
		pages: [][]objects.Record{{
			repairRecord("did-2", strings.Repeat("b", 64), "s3://repair-bucket/legacy-2"),
			repairRecord("did-1", strings.Repeat("a", 64), "s3://repair-bucket/legacy-1"),
		}, nil},
		failNext: true,
	}
	service := NewRepairService(records, repairBuckets(), nil)
	result, err := service.apply(context.Background(), RepairOptions{Organization: "org", Project: "project", PageSize: 10})
	if err != nil {
		t.Fatalf("apply() error = %v", err)
	}
	if len(records.collapse) != 1 || records.collapse[0] != "org/project" {
		t.Fatalf("collapse calls = %v", records.collapse)
	}
	if result.AutoFixable != 2 || result.Skipped != 1 || result.Mutated != 1 {
		t.Fatalf("apply counters = %+v", result)
	}
	if len(records.ids) != 2 || records.ids[0] != "did-1" || records.ids[1] != "did-2" {
		t.Fatalf("writer order = %v", records.ids)
	}
}

func TestRepairAuditStorageFindingsDistinguishNotFound(t *testing.T) {
	record := repairRecord("did-1", strings.Repeat("a", 64), "s3://repair-bucket/current")
	records := &fakeRepairRecords{pages: [][]objects.Record{{record}}}
	probe := &fakeRepairProbe{missing: map[string]bool{"s3://repair-bucket/current": true}}
	service := NewRepairService(records, repairBuckets(), repairInspector(repairBuckets(), probe))
	state, err := service.audit(context.Background(), RepairOptions{Organization: "org", Project: "project", CheckStorage: true})
	if err != nil {
		t.Fatalf("audit() error = %v", err)
	}
	if len(state.report.Objects) != 1 || len(state.report.Objects[0].Findings) < 2 {
		t.Fatalf("storage report = %+v", state.report)
	}
	for _, finding := range state.report.Objects[0].Findings {
		if finding.Kind == FindingStorageObjectMissing && finding.Severity == SeverityError {
			return
		}
	}
	t.Fatalf("storage findings = %+v", state.report.Objects[0].Findings)
}

func TestRepairAuditPathStyleStorageProbePreservesDirectoryName(t *testing.T) {
	record := repairRecord("did-1", strings.Repeat("a", 64), "s3://repair-bucket/legacy")
	name := "dir/file.bin"
	record.Name = &name
	canonical := "s3://repair-bucket/prefix/did-1/" + strings.Repeat("a", 64)
	pathStyle := "s3://repair-bucket/prefix/dir/file.bin"
	records := &fakeRepairRecords{pages: [][]objects.Record{{record}}}
	probe := &fakeRepairProbe{missing: map[string]bool{canonical: true}}
	service := NewRepairService(records, repairBuckets(), repairInspector(repairBuckets(), probe))
	state, err := service.audit(context.Background(), RepairOptions{Organization: "org", Project: "project", CheckStorage: true})
	if err != nil {
		t.Fatalf("audit() error = %v", err)
	}
	if len(state.report.Objects) != 1 || len(state.report.Objects[0].Findings) != 1 || state.report.Objects[0].Findings[0].ProposedCanonicalURL != pathStyle {
		t.Fatalf("report = %+v, want path-style URL %q", state.report, pathStyle)
	}
	for _, call := range probe.calls {
		if call == pathStyle {
			return
		}
	}
	t.Fatalf("probe calls = %v, want %q", probe.calls, pathStyle)
}

func TestRepairApplyRequiresProjectScopeBeforeCallingPorts(t *testing.T) {
	records := &fakeRepairRecords{}
	service := NewRepairService(records, repairBuckets(), nil)
	_, err := service.ApplyAuthorized(context.Background(), RepairOptions{Organization: "org"})
	if err == nil || len(records.collapse) != 0 || len(records.queries) != 0 {
		t.Fatalf("ApplyAuthorized() validation err=%v collapse=%v queries=%v", err, records.collapse, records.queries)
	}
}

func TestRepairAuthorizationHappensBeforePorts(t *testing.T) {
	const resource = "/organization/org/project/project"

	t.Run("denied read does not inspect or collapse", func(t *testing.T) {
		records := &fakeRepairRecords{}
		service := NewRepairService(records, repairBuckets(), nil)
		_, err := service.ApplyAuthorized(repairAuthzContext(map[string]map[string]bool{}), RepairOptions{Organization: "org", Project: "project"})
		if !errors.Is(err, errorapi.ErrAccessDenied) || len(records.queries) != 0 || len(records.collapse) != 0 {
			t.Fatalf("ApplyAuthorized() error=%v collapse=%v queries=%v", err, records.collapse, records.queries)
		}
	})

	t.Run("read-only access does not update or collapse", func(t *testing.T) {
		records := &fakeRepairRecords{}
		service := NewRepairService(records, repairBuckets(), nil)
		_, err := service.ApplyAuthorized(repairAuthzContext(map[string]map[string]bool{resource: {"read": true}}), RepairOptions{Organization: "org", Project: "project"})
		if !errors.Is(err, errorapi.ErrAccessDenied) || len(records.queries) != 0 || len(records.collapse) != 0 {
			t.Fatalf("ApplyAuthorized() error=%v collapse=%v queries=%v", err, records.collapse, records.queries)
		}
	})

	records := &fakeRepairRecords{}
	service := NewRepairService(records, repairBuckets(), nil)
	_, err := service.AuditAuthorized(repairAuthzContext(map[string]map[string]bool{}), RepairOptions{Organization: "org", Project: "project"})
	if !errors.Is(err, errorapi.ErrAccessDenied) || len(records.queries) != 0 {
		t.Fatalf("AuditAuthorized() error=%v queries=%v", err, records.queries)
	}
}

func repairAuthzContext(privileges map[string]map[string]bool) context.Context {
	session := access.NewSession("local")
	session.AuthzEnforced = true
	session.SetAuthorizations(nil, privileges, true)
	return access.WithSession(context.Background(), session)
}
