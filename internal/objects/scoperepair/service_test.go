package scoperepair

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/calypr/syfon/internal/buckets"
	"github.com/calypr/syfon/internal/objects"
)

type fakePrepared struct {
	pages   [][]objects.Record
	queries []PreparedRecordQuery
}

func (f *fakePrepared) ListPrepared(_ context.Context, query PreparedRecordQuery) ([]objects.Record, error) {
	f.queries = append(f.queries, query)
	index := len(f.queries) - 1
	if index >= len(f.pages) {
		return nil, nil
	}
	return f.pages[index], nil
}

type fakeScopeReader struct {
	credentials []buckets.Credential
	scopes      map[string][]buckets.Scope
}

func (f fakeScopeReader) ListCredentials(context.Context) ([]buckets.Credential, error) {
	return f.credentials, nil
}
func (f fakeScopeReader) ListScopes(_ context.Context, bucket string) ([]buckets.Scope, error) {
	return f.scopes[bucket], nil
}

type fakeWriter struct {
	ids      []objects.RecordID
	updates  []objects.Record
	failNext bool
}

func (f *fakeWriter) Update(_ context.Context, id objects.RecordID, update objects.Record) error {
	f.ids = append(f.ids, id)
	f.updates = append(f.updates, update)
	if f.failNext {
		f.failNext = false
		return errors.New("write failed")
	}
	return nil
}

type fakeProbe struct {
	missing map[string]bool
	calls   []string
}

func (f *fakeProbe) Inspect(_ context.Context, request StorageInspectRequest) (StorageInspectResult, error) {
	f.calls = append(f.calls, request.ObjectURL)
	if f.missing[request.ObjectURL] {
		return StorageInspectResult{}, ErrStorageObjectNotFound
	}
	return StorageInspectResult{ObjectURL: request.ObjectURL}, nil
}

type fakeCollapser struct {
	calls []string
}

func (f *fakeCollapser) Collapse(_ context.Context, organization, project string) (int, error) {
	f.calls = append(f.calls, organization+"/"+project)
	return 0, nil
}

func repairScopeReader() fakeScopeReader {
	return fakeScopeReader{
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

func repairRecord(id, sha, accessURL string) objects.Record {
	resource := "/programs/org/projects/project"
	controlled := []string{resource}
	methods := []objects.AccessMethod{{Type: "s3", AccessUrl: &objects.AccessURL{Url: accessURL}}}
	name := "file.txt"
	return objects.Record{Id: objects.RecordID(id), Checksums: []objects.Checksum{{Type: "sha256", Checksum: sha}}, ControlledAccess: &controlled, AccessMethods: &methods, Name: &name}
}

func TestAuditUsesS3ScopeAndPreservesCanonicalRepairReport(t *testing.T) {
	prepared := &fakePrepared{pages: [][]objects.Record{{repairRecord("did-1", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "s3://repair-bucket/legacy")}, nil}}
	probe := &fakeProbe{}
	service := NewService(prepared, nil, repairScopeReader(), probe, nil)
	report, err := service.Audit(context.Background(), Options{Organization: " org ", Project: " project ", PageSize: 1})
	if err != nil {
		t.Fatalf("Audit() error = %v", err)
	}
	if report.Scanned != 1 || len(report.Objects) != 1 {
		t.Fatalf("report = %+v", report)
	}
	object := report.Objects[0]
	wantURL := "s3://repair-bucket/prefix/did-1/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	if object.ProposedCanonicalURL != wantURL || object.Findings[0].Kind != FindingLegacyAccessURLRewritable || !object.AutoFixable {
		t.Fatalf("object report = %+v", object)
	}
	if len(prepared.queries) != 2 || prepared.queries[1].Start != "did-1" || prepared.queries[0].Limit != 1 {
		t.Fatalf("prepared queries = %+v", prepared.queries)
	}
	if len(probe.calls) != 0 {
		t.Fatalf("unexpected storage probe calls with CheckStorage=false: %v", probe.calls)
	}
}

func TestApplyCollapsesBeforeAuditAndContinuesAfterWriteFailure(t *testing.T) {
	first := repairRecord("did-1", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "s3://repair-bucket/legacy-1")
	second := repairRecord("did-2", "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", "s3://repair-bucket/legacy-2")
	prepared := &fakePrepared{pages: [][]objects.Record{{second, first}, nil}}
	writer := &fakeWriter{failNext: true}
	collapser := &fakeCollapser{}
	service := NewService(prepared, writer, repairScopeReader(), nil, collapser)
	result, err := service.Apply(context.Background(), Options{Organization: "org", Project: "project", PageSize: 10})
	if err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	if len(collapser.calls) != 1 || collapser.calls[0] != "org/project" {
		t.Fatalf("collapse calls = %v", collapser.calls)
	}
	if result.AutoFixable != 2 || result.Skipped != 1 || result.Mutated != 1 {
		t.Fatalf("apply counters = %+v", result)
	}
	if len(writer.ids) != 2 || writer.ids[0] != "did-1" || writer.ids[1] != "did-2" {
		t.Fatalf("writer order = %v", writer.ids)
	}
}

func TestAuditStorageFindingsDistinguishNotFoundFromProbeFailure(t *testing.T) {
	record := repairRecord("did-1", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "s3://repair-bucket/current")
	prepared := &fakePrepared{pages: [][]objects.Record{{record}}}
	probe := &fakeProbe{missing: map[string]bool{"s3://repair-bucket/current": true}}
	service := NewService(prepared, nil, repairScopeReader(), probe, nil)
	report, err := service.Audit(context.Background(), Options{Organization: "org", Project: "project", CheckStorage: true})
	if err != nil {
		t.Fatalf("Audit() error = %v", err)
	}
	if len(report.Objects) != 1 || len(report.Objects[0].Findings) < 2 {
		t.Fatalf("storage report = %+v", report)
	}
	foundMissing := false
	for _, finding := range report.Objects[0].Findings {
		if finding.Kind == FindingStorageObjectMissing && finding.Severity == SeverityError {
			foundMissing = true
		}
	}
	if !foundMissing {
		t.Fatalf("storage findings = %+v", report.Objects[0].Findings)
	}
}

func TestAuditPathStyleStorageProbePreservesDirectoryName(t *testing.T) {
	record := repairRecord("did-1", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "s3://repair-bucket/legacy")
	name := "dir/file.bin"
	record.Name = &name
	canonical := "s3://repair-bucket/prefix/did-1/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	pathStyle := "s3://repair-bucket/prefix/dir/file.bin"
	prepared := &fakePrepared{pages: [][]objects.Record{{record}}}
	probe := &fakeProbe{missing: map[string]bool{canonical: true}}
	service := NewService(prepared, nil, repairScopeReader(), probe, nil)
	report, err := service.Audit(context.Background(), Options{Organization: "org", Project: "project", CheckStorage: true})
	if err != nil {
		t.Fatalf("Audit() error = %v", err)
	}
	if len(report.Objects) != 1 || len(report.Objects[0].Findings) != 1 || report.Objects[0].Findings[0].ProposedCanonicalURL != pathStyle {
		t.Fatalf("report = %+v, want path-style URL %q", report, pathStyle)
	}
	foundPathStyleProbe := false
	for _, call := range probe.calls {
		if call == pathStyle {
			foundPathStyleProbe = true
			break
		}
	}
	if !foundPathStyleProbe {
		t.Fatalf("probe calls = %v, want %q", probe.calls, pathStyle)
	}
}

func TestApplyRequiresProjectScopeBeforeCallingPorts(t *testing.T) {
	prepared := &fakePrepared{}
	collapser := &fakeCollapser{}
	service := NewService(prepared, nil, repairScopeReader(), nil, collapser)
	_, err := service.Apply(context.Background(), Options{Organization: "org"})
	if err == nil || len(collapser.calls) != 0 || len(prepared.queries) != 0 {
		t.Fatalf("Apply() validation err=%v collapse=%v queries=%v", err, collapser.calls, prepared.queries)
	}
}

func TestScopeRepairTypesRemainPlainDomainValues(t *testing.T) {
	var _ PreparedRecordReader = (*fakePrepared)(nil)
	var _ ReferenceWriter = (*fakeWriter)(nil)
	var _ ScopeReader = fakeScopeReader{}
	var _ StorageProbe = (*fakeProbe)(nil)
	var _ DuplicateCollapser = (*fakeCollapser)(nil)
	if got := fmt.Sprintf("%T", Finding{}); got != "scoperepair.Finding" {
		t.Fatalf("unexpected type = %s", got)
	}
}
