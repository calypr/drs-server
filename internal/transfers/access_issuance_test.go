package transfers

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/calypr/syfon/internal/objects"
	"github.com/calypr/syfon/internal/storage"
)

type accessWorkflowReader struct {
	records map[string]*objects.Record
	methods []string
}

func (r *accessWorkflowReader) GetObject(_ context.Context, id, method string) (*objects.Record, error) {
	r.methods = append(r.methods, method)
	obj, ok := r.records[id]
	if !ok {
		return nil, errors.New("object not found")
	}
	return obj, nil
}

type accessWorkflowTransfer struct {
	calls       []string
	options     []storage.AccessOptions
	events      []AccessRequest
	signErrors  map[string]error
	eventErrors map[string]error
}

func (t *accessWorkflowTransfer) SignObjectURL(_ context.Context, _ *objects.Record, accessURL string, options storage.AccessOptions) (string, error) {
	t.calls = append(t.calls, "sign:"+accessURL)
	t.options = append(t.options, options)
	if err := t.signErrors[accessURL]; err != nil {
		return "", err
	}
	return "signed:" + accessURL, nil
}

func (t *accessWorkflowTransfer) RecordAccessIssued(_ context.Context, request AccessRequest) error {
	t.calls = append(t.calls, "event:"+request.StorageURL)
	t.events = append(t.events, request)
	return t.eventErrors[request.StorageURL]
}

func accessWorkflowRecord(name string, methods ...objects.AccessMethod) *objects.Record {
	return &objects.Record{Id: objects.RecordID(name), Name: &name, AccessMethods: &methods}
}

func accessWorkflowMethod(id, typ, url string) objects.AccessMethod {
	return objects.AccessMethod{AccessId: &id, Type: typ, AccessUrl: &objects.AccessURL{Url: url}}
}

func TestAccessWorkflowIssuePreservesAuthorizationAndEventOrder(t *testing.T) {
	reader := &accessWorkflowReader{records: map[string]*objects.Record{
		"object": accessWorkflowRecord("object", accessWorkflowMethod("read", "s3", "s3://bucket/object")),
	}}
	transfer := &accessWorkflowTransfer{signErrors: map[string]error{}, eventErrors: map[string]error{}}
	workflow := NewAccessWorkflow(reader, transfer)

	result, err := workflow.Issue(context.Background(), "object", " read ")
	if err != nil {
		t.Fatalf("Issue() error = %v", err)
	}
	if !result.Found || result.URL != "signed:s3://bucket/object" {
		t.Fatalf("Issue() result = %+v", result)
	}
	if !reflect.DeepEqual(reader.methods, []string{"read"}) {
		t.Fatalf("authorization methods = %v", reader.methods)
	}
	if !reflect.DeepEqual(transfer.calls, []string{"sign:s3://bucket/object", "event:s3://bucket/object"}) {
		t.Fatalf("transfer calls = %v", transfer.calls)
	}
	if len(transfer.options) != 1 || transfer.options[0].Method != "GET" || transfer.options[0].DownloadFilename != "object" {
		t.Fatalf("signing options = %+v", transfer.options)
	}
	if len(transfer.events) != 1 || transfer.events[0].AccessID != " read " {
		t.Fatalf("event request = %+v", transfer.events)
	}
}

func TestAccessWorkflowIssueUsesOnlyUniqueLegacyTypeMatch(t *testing.T) {
	reader := &accessWorkflowReader{records: map[string]*objects.Record{
		"object": accessWorkflowRecord("object",
			accessWorkflowMethod("one", "s3", "s3://bucket/one"),
			accessWorkflowMethod("two", "S3", "s3://bucket/two"),
		),
	}}
	transfer := &accessWorkflowTransfer{signErrors: map[string]error{}, eventErrors: map[string]error{}}
	workflow := NewAccessWorkflow(reader, transfer)

	result, err := workflow.Issue(context.Background(), "object", "s3")
	if err != nil {
		t.Fatalf("Issue() error = %v", err)
	}
	if result.Found {
		t.Fatalf("ambiguous legacy type resolved to %+v", result)
	}

	reader.records["object"] = accessWorkflowRecord("object", accessWorkflowMethod("one", "s3", "s3://bucket/one"))
	result, err = workflow.Issue(context.Background(), "object", "S3")
	if err != nil {
		t.Fatalf("Issue() unique legacy error = %v", err)
	}
	if !result.Found || result.URL != "signed:s3://bucket/one" {
		t.Fatalf("unique legacy result = %+v", result)
	}
}

func TestAccessWorkflowIssueBulkPreservesCountsOrderAndContinuation(t *testing.T) {
	reader := &accessWorkflowReader{records: map[string]*objects.Record{
		"object": accessWorkflowRecord("object",
			accessWorkflowMethod("a", "s3", "s3://bucket/a"),
			accessWorkflowMethod("b", "s3", "s3://bucket/sign-fail"),
			accessWorkflowMethod("c", "s3", "s3://bucket/event-fail"),
			accessWorkflowMethod("d", "s3", "s3://bucket/d"),
		),
	}}
	transfer := &accessWorkflowTransfer{
		signErrors:  map[string]error{"s3://bucket/sign-fail": errors.New("sign failed")},
		eventErrors: map[string]error{"s3://bucket/event-fail": errors.New("event failed")},
	}
	workflow := NewAccessWorkflow(reader, transfer)

	result := workflow.IssueBulk(context.Background(), []BulkAccessLookupRequest{
		{ObjectID: " object ", AccessIDs: []string{"a", "b", "c", "d"}},
		{ObjectID: "missing", AccessIDs: []string{"a", "b"}},
		{ObjectID: "empty", AccessIDs: nil},
		{ObjectID: "", AccessIDs: []string{"a"}},
	})

	if result.Requested != 8 || len(result.Resolved) != 2 {
		t.Fatalf("bulk counts = requested %d resolved %d", result.Requested, len(result.Resolved))
	}
	if !reflect.DeepEqual(result.Resolved, []ResolvedAccess{
		{ObjectID: "object", AccessID: "a", URL: "signed:s3://bucket/a"},
		{ObjectID: "object", AccessID: "d", URL: "signed:s3://bucket/d"},
	}) {
		t.Fatalf("resolved = %+v", result.Resolved)
	}
	if !reflect.DeepEqual(result.UnresolvedObjectIDs, []string{"object", "object", "missing", "empty"}) {
		t.Fatalf("unresolved = %v", result.UnresolvedObjectIDs)
	}
	if !reflect.DeepEqual(transfer.calls, []string{
		"sign:s3://bucket/a", "event:s3://bucket/a",
		"sign:s3://bucket/sign-fail",
		"sign:s3://bucket/event-fail", "event:s3://bucket/event-fail",
		"sign:s3://bucket/d", "event:s3://bucket/d",
	}) {
		t.Fatalf("transfer calls = %v", transfer.calls)
	}
}
