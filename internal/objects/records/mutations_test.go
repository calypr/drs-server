package records_test

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/calypr/syfon/internal/objects"
	objectrecords "github.com/calypr/syfon/internal/objects/records"
)

type drsWorkflowStore struct {
	objectrecords.ObjectStore
	records         map[string]objects.Record
	registerCalls   int
	registered      []objects.Record
	readIDs         []string
	bulkUpdateCalls int
	updateCalls     int
	readErrors      map[string]error
}

func (s *drsWorkflowStore) GetObject(_ context.Context, id string) (*objects.Record, error) {
	s.readIDs = append(s.readIDs, id)
	if err := s.readErrors[id]; err != nil {
		return nil, err
	}
	record, ok := s.records[id]
	if !ok {
		return nil, fmt.Errorf("object %q not found", id)
	}
	return cloneDRSWorkflowRecord(record), nil
}

func (s *drsWorkflowStore) GetBulkObjects(_ context.Context, ids []string) ([]objects.Record, error) {
	result := make([]objects.Record, 0, len(ids))
	for _, id := range ids {
		if record, ok := s.records[id]; ok {
			result = append(result, *cloneDRSWorkflowRecord(record))
		}
	}
	return result, nil
}

func (s *drsWorkflowStore) GetObjectsByChecksum(_ context.Context, checksum string) ([]objects.Record, error) {
	result := make([]objects.Record, 0)
	for _, record := range s.records {
		if recordHasDRSWorkflowChecksum(record, checksum) {
			result = append(result, *cloneDRSWorkflowRecord(record))
		}
	}
	return result, nil
}

func (s *drsWorkflowStore) RegisterObjects(_ context.Context, records []objects.Record) error {
	s.registerCalls++
	s.registered = append([]objects.Record(nil), records...)
	if s.records == nil {
		s.records = make(map[string]objects.Record)
	}
	for _, record := range records {
		stored := *cloneDRSWorkflowRecord(record)
		if stored.Name != nil {
			name := "durable-" + string(stored.Id)
			stored.Name = &name
		}
		s.records[string(stored.Id)] = stored
	}
	return nil
}

func (s *drsWorkflowStore) UpdateObjectAccessMethods(_ context.Context, id string, methods []objects.AccessMethod) error {
	s.updateCalls++
	record, ok := s.records[id]
	if !ok {
		return fmt.Errorf("object %q not found", id)
	}
	copyMethods := append([]objects.AccessMethod(nil), methods...)
	record.AccessMethods = &copyMethods
	s.records[id] = record
	return nil
}

func (s *drsWorkflowStore) BulkUpdateAccessMethods(_ context.Context, updates map[string][]objects.AccessMethod) error {
	s.bulkUpdateCalls++
	for id, methods := range updates {
		record, ok := s.records[id]
		if !ok {
			return fmt.Errorf("object %q not found", id)
		}
		copyMethods := append([]objects.AccessMethod(nil), methods...)
		record.AccessMethods = &copyMethods
		s.records[id] = record
	}
	return nil
}

func cloneDRSWorkflowRecord(record objects.Record) *objects.Record {
	copyRecord := record
	if record.AccessMethods != nil {
		methods := append([]objects.AccessMethod(nil), (*record.AccessMethods)...)
		copyRecord.AccessMethods = &methods
	}
	if record.Checksums != nil {
		copyRecord.Checksums = append([]objects.Checksum(nil), record.Checksums...)
	}
	return &copyRecord
}

func recordHasDRSWorkflowChecksum(record objects.Record, wanted string) bool {
	for _, checksum := range record.Checksums {
		if checksum.Checksum == wanted {
			return true
		}
	}
	return false
}

func TestRegisterBulk_RegistersCandidate(t *testing.T) {
	database := newSQLiteDatabase(t)
	om := newTestService(database, nil)

	candidates := []objects.Candidate{
		{
			Aliases: ptr([]string{"id:test-register-bulk"}),
			Checksums: &[]objects.Checksum{{
				Type:     "sha256",
				Checksum: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			}},
			AccessMethods: &[]objects.AccessMethod{{
				Type:      "s3",
				AccessUrl: &objects.AccessURL{Url: "s3://bucket/test-register-bulk"},
			}},
			Size: ptr(int64(1)),
		},
	}

	count, err := registerCandidates(context.Background(), om, candidates)
	if err != nil {
		t.Fatalf("RegisterBulk error: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected count=1, got=%d", count)
	}

	obj, err := database.GetObject(context.Background(), "test-register-bulk")
	if err != nil {
		t.Fatalf("expected registered object, got error: %v", err)
	}
	if obj == nil || obj.Id != "test-register-bulk" {
		t.Fatalf("unexpected object: %+v", obj)
	}
}

func TestRegisterBulk_InvalidChecksum(t *testing.T) {
	database := newSQLiteDatabase(t)
	om := newTestService(database, nil)

	candidates := []objects.Candidate{{
		Aliases: ptr([]string{"id:test-invalid-checksum"}),
		Checksums: &[]objects.Checksum{{
			Type:     "md5",
			Checksum: "abc",
		}},
		Size: ptr(int64(1)),
	}}

	if _, err := registerCandidates(context.Background(), om, candidates); err == nil {
		t.Fatalf("expected RegisterBulk error for invalid checksum")
	}
}

func TestBulkDeleteObjects_DeletesAuthorizedObjects(t *testing.T) {
	database := newSQLiteDatabase(t)
	om := newTestService(database, nil)

	_, err := registerCandidates(context.Background(), om, []objects.Candidate{{
		Aliases: ptr([]string{"id:test-delete-bulk"}),
		Checksums: &[]objects.Checksum{{
			Type:     "sha256",
			Checksum: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		}},
		AccessMethods: &[]objects.AccessMethod{{
			Type:      "s3",
			AccessUrl: &objects.AccessURL{Url: "s3://bucket/test-delete-bulk"},
		}},
		Size: ptr(int64(1)),
	}})
	if err != nil {
		t.Fatalf("seed RegisterBulk error: %v", err)
	}

	if err := om.BulkDeleteObjects(context.Background(), []string{"test-delete-bulk"}); err != nil {
		t.Fatalf("BulkDeleteObjects error: %v", err)
	}
	if _, err := database.GetObject(context.Background(), "test-delete-bulk"); err == nil {
		t.Fatalf("expected object to be deleted")
	}
}

func TestRegisterObjects_CanonicalizesProjectChecksumDuplicates(t *testing.T) {
	database := newSQLiteDatabase(t)
	om := newTestService(database, nil)
	now := time.Now().UTC()
	later := now.Add(time.Minute)
	accessURL1 := "s3://bucket/original"
	accessURL2 := "s3://bucket/renamed"

	first := objects.Record{

		Id:               "did-1",
		ControlledAccess: &[]string{"/organization/org/project/proj"},
		Name:             ptr("original.tsv"),
		Size:             42,
		CreatedTime:      now,
		UpdatedTime:      &now,
		Checksums:        []objects.Checksum{{Type: "sha256", Checksum: "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"}},
		AccessMethods: &[]objects.AccessMethod{{
			Type:      "s3",
			AccessUrl: &objects.AccessURL{Url: accessURL1},
		}},
	}
	second := objects.Record{

		Id:               "did-2",
		ControlledAccess: &[]string{"/organization/org/project/proj"},
		Name:             ptr("renamed.tsv"),
		Size:             42,
		CreatedTime:      later,
		UpdatedTime:      &later,
		Checksums:        first.Checksums,
		AccessMethods: &[]objects.AccessMethod{{
			Type:      "s3",
			AccessUrl: &objects.AccessURL{Url: accessURL2},
		}},
	}

	if err := om.RegisterObjects(context.Background(), []objects.Record{first}); err != nil {
		t.Fatalf("RegisterObjects(first) error: %v", err)
	}
	if err := om.RegisterObjects(context.Background(), []objects.Record{second}); err != nil {
		t.Fatalf("RegisterObjects(second) error: %v", err)
	}

	records, err := om.GetObjectsByChecksum(context.Background(), first.Checksums[0].Checksum, "")
	if err != nil {
		t.Fatalf("GetObjectsByChecksum error: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("expected 1 canonical record, got %d", len(records))
	}
	if records[0].Id != "did-1" {
		t.Fatalf("expected canonical did-1, got %q", records[0].Id)
	}
	if got := records[0].Name; got == nil || *got != "renamed.tsv" {
		t.Fatalf("expected latest name renamed.tsv, got %+v", got)
	}
	if !slices.Equal(records[0].NameAliases, []string{"original.tsv"}) {
		t.Fatalf("unexpected name aliases: %#v", records[0].NameAliases)
	}

	aliasObj, err := om.GetObject(context.Background(), "did-2", "")
	if err != nil {
		t.Fatalf("GetObject(alias) error: %v", err)
	}
	if aliasObj.Id != "did-1" {
		t.Fatalf("expected alias lookup to return canonical did-1, got %q", aliasObj.Id)
	}
	scopeIDs, err := om.ListObjectIDsByScope(context.Background(), "org", "proj", "")
	if err != nil {
		t.Fatalf("ListObjectIDsByScope error: %v", err)
	}
	if !slices.Equal(scopeIDs, []string{"did-1"}) {
		t.Fatalf("unexpected scoped ids: %#v", scopeIDs)
	}
}

func TestRegisterObjects_ReusesContentAcrossProjects(t *testing.T) {
	database := newSQLiteDatabase(t)
	om := newTestService(database, nil)
	sha := "dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd"
	now := time.Date(2026, 9, 4, 16, 0, 0, 0, time.UTC)
	later := now.Add(time.Minute)
	firstResource := "/organization/org/project/first"
	secondResource := "/organization/org/project/second"

	first := objects.Record{

		Id:               "canonical-did",
		Name:             ptr("first.tsv"),
		Size:             42,
		CreatedTime:      now,
		UpdatedTime:      &now,
		Checksums:        []objects.Checksum{{Type: "sha256", Checksum: sha}},
		ControlledAccess: &[]string{firstResource},
		AccessMethods: &[]objects.AccessMethod{{
			Type:      "s3",
			AccessUrl: &objects.AccessURL{Url: "s3://bucket/first"},
		}},
	}
	second := objects.Record{

		Id:               "second-did",
		Name:             ptr("second.tsv"),
		Size:             42,
		CreatedTime:      later,
		UpdatedTime:      &later,
		Checksums:        []objects.Checksum{{Type: "sha256", Checksum: sha}},
		ControlledAccess: &[]string{secondResource},
		AccessMethods: &[]objects.AccessMethod{{
			Type:      "s3",
			AccessUrl: &objects.AccessURL{Url: "s3://bucket/second"},
		}},
	}

	if err := om.RegisterObjects(context.Background(), []objects.Record{first}); err != nil {
		t.Fatalf("RegisterObjects(first) error: %v", err)
	}
	if err := om.RegisterObjects(context.Background(), []objects.Record{second}); err != nil {
		t.Fatalf("RegisterObjects(second) error: %v", err)
	}

	physicalRecords, err := database.GetObjectsByChecksum(context.Background(), sha)
	if err != nil {
		t.Fatalf("database.GetObjectsByChecksum error: %v", err)
	}
	if len(physicalRecords) != 1 {
		t.Fatalf("expected one physical content record, got %d", len(physicalRecords))
	}

	byChecksum, err := om.GetObjectsByChecksum(context.Background(), sha, "")
	if err != nil {
		t.Fatalf("GetObjectsByChecksum error: %v", err)
	}
	if len(byChecksum) != 1 {
		t.Fatalf("expected one canonical checksum record, got %d", len(byChecksum))
	}
	canonical := byChecksum[0]
	if string(canonical.Id) != string(first.Id) {
		t.Fatalf("expected deterministic read representative %q, got %q", string(first.Id), string(canonical.Id))
	}
	if canonical.AccessMethods == nil || len(*canonical.AccessMethods) != 2 {
		t.Fatalf("expected both access methods, got %+v", canonical.AccessMethods)
	}
	if canonical.ControlledAccess == nil || len(*canonical.ControlledAccess) != 2 || !slices.Contains(*canonical.ControlledAccess, firstResource) || !slices.Contains(*canonical.ControlledAccess, secondResource) {
		t.Fatalf("expected both controlled-access resources, got %+v", canonical.ControlledAccess)
	}

	for _, ident := range []string{string(first.Id), string(second.Id)} {
		got, err := om.GetObject(context.Background(), ident, "")
		if err != nil {
			t.Fatalf("GetObject(%q) error: %v", ident, err)
		}
		if got.Id != canonical.Id {
			t.Fatalf("GetObject(%q) returned id %q, want canonical %q", ident, got.Id, string(canonical.Id))
		}
		if got.AccessMethods == nil || len(*got.AccessMethods) != 2 {
			t.Fatalf("GetObject(%q) lost merged access methods: %+v", ident, got.AccessMethods)
		}
	}
}

func TestRegisterCandidatesMaterializesAndRereadsInRequestOrder(t *testing.T) {
	store := &drsWorkflowStore{records: make(map[string]objects.Record)}
	service := newTestService(store)

	registered, err := service.RegisterCandidates(context.Background(), []objects.Candidate{
		drsCandidate("first", "first.tsv", "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"),
		drsCandidate("second", "second.tsv", "ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"),
	})
	if err != nil {
		t.Fatalf("RegisterCandidates() error = %v", err)
	}
	if store.registerCalls != 1 {
		t.Fatalf("RegisterObjects calls = %d, want 1", store.registerCalls)
	}
	if !slices.Equal(store.readIDs, []string{"first", "second"}) {
		t.Fatalf("durable reread order = %#v", store.readIDs)
	}
	if len(registered) != 2 || registered[0].Name == nil || *registered[0].Name != "durable-first" || registered[1].Name == nil || *registered[1].Name != "durable-second" {
		t.Fatalf("durable records = %#v", registered)
	}
	for i, record := range store.registered {
		if record.CreatedTime.IsZero() || record.UpdatedTime == nil {
			t.Fatalf("record %d was not materialized: %#v", i, record)
		}
	}
}

func TestRegisterCandidatesPreservesReadAfterWriteFailure(t *testing.T) {
	readErr := errors.New("reread failed")
	store := &drsWorkflowStore{
		records:    make(map[string]objects.Record),
		readErrors: map[string]error{"first": readErr},
	}
	service := newTestService(store)

	registered, err := service.RegisterCandidates(context.Background(), []objects.Candidate{
		drsCandidate("first", "first.tsv", "1111111111111111111111111111111111111111111111111111111111111111"),
	})
	if !errors.Is(err, readErr) {
		t.Fatalf("RegisterCandidates() error = %v, want %v", err, readErr)
	}
	if registered != nil {
		t.Fatalf("registered result = %#v, want nil on reread failure", registered)
	}
	if store.registerCalls != 1 {
		t.Fatalf("RegisterObjects calls = %d, want 1", store.registerCalls)
	}
	if _, ok := store.records["first"]; !ok {
		t.Fatal("read failure rolled back the durable write")
	}
}

func TestUpdateAccessMethodsAndReadUsesDurableRead(t *testing.T) {
	store := &drsWorkflowStore{records: map[string]objects.Record{"one": {Id: "one"}}}
	service := newTestService(store)

	updated, err := service.UpdateAccessMethodsAndRead(context.Background(), "one", []objects.AccessMethod{drsAccessMethod("s3")})
	if err != nil {
		t.Fatalf("UpdateAccessMethodsAndRead() error = %v", err)
	}
	if store.updateCalls != 1 {
		t.Fatalf("single update calls = %d, want 1", store.updateCalls)
	}
	if updated == nil || updated.AccessMethods == nil || len(*updated.AccessMethods) != 1 || (*updated.AccessMethods)[0].Type != "s3" {
		t.Fatalf("updated durable record = %#v", updated)
	}
	if !slices.Equal(store.readIDs, []string{"one", "one"}) {
		t.Fatalf("single update read sequence = %#v", store.readIDs)
	}
}

func TestBulkUpdateAccessMethodsAndReadPreservesOrderAndLastDuplicate(t *testing.T) {
	store := &drsWorkflowStore{records: map[string]objects.Record{
		"one": {Id: "one"},
		"two": {Id: "two"},
	}}
	service := newTestService(store)

	updated, err := service.BulkUpdateAccessMethodsAndRead(context.Background(), []objectrecords.AccessMethodUpdate{
		{ObjectID: "one", Methods: []objects.AccessMethod{drsAccessMethod("first")}},
		{ObjectID: "two", Methods: []objects.AccessMethod{drsAccessMethod("second")}},
		{ObjectID: "one", Methods: []objects.AccessMethod{drsAccessMethod("last")}},
	})
	if err != nil {
		t.Fatalf("BulkUpdateAccessMethodsAndRead() error = %v", err)
	}
	if store.bulkUpdateCalls != 1 {
		t.Fatalf("bulk update calls = %d, want 1", store.bulkUpdateCalls)
	}
	if !slices.Equal(store.readIDs, []string{"one", "two"}) {
		t.Fatalf("bulk reread order = %#v", store.readIDs)
	}
	if len(updated) != 2 || updated[0].Id != "one" || updated[1].Id != "two" {
		t.Fatalf("bulk response order = %#v", updated)
	}
	if updated[0].AccessMethods == nil || (*updated[0].AccessMethods)[0].Type != "last" {
		t.Fatalf("duplicate update did not use last value: %#v", updated[0].AccessMethods)
	}
}

func drsCandidate(id, name, checksum string) objects.Candidate {
	return objects.Candidate{
		Aliases:   ptr([]string{"id:" + id}),
		Name:      ptr(name),
		Checksums: ptr([]objects.Checksum{{Type: "sha256", Checksum: checksum}}),
		AccessMethods: ptr([]objects.AccessMethod{{
			Type:      "s3",
			AccessUrl: &objects.AccessURL{Url: "s3://bucket/" + id},
		}}),
	}
}

func drsAccessMethod(kind string) objects.AccessMethod {
	return objects.AccessMethod{Type: kind, AccessUrl: &objects.AccessURL{Url: "s3://bucket/" + kind}}
}
