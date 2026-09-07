package transfers

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/calypr/syfon/internal/faults"
	"github.com/calypr/syfon/internal/objects"
	"github.com/calypr/syfon/internal/storage"
)

type lfsUploadMultipartSpy struct {
	events     []string
	partURLs   []string
	partNumber []int32
	completed  storage.CompleteMultipartRequest
}

func (s *lfsUploadMultipartSpy) BeginMultipart(_ context.Context, target storage.ObjectTarget) (storage.UploadID, error) {
	s.events = append(s.events, "begin")
	if target != (storage.ObjectTarget{Bucket: "bucket", Key: "object"}) {
		return "", fmt.Errorf("unexpected target: %+v", target)
	}
	return "opaque-upload-id", nil
}

func (s *lfsUploadMultipartSpy) AccessMultipartPart(_ context.Context, request storage.MultipartPartRequest) (storage.Access, error) {
	s.events = append(s.events, "sign")
	s.partURLs = append(s.partURLs, request.Target.Bucket+"/"+request.Target.Key)
	s.partNumber = append(s.partNumber, request.PartNumber)
	return storage.Access{Location: "https://provider.invalid/part"}, nil
}

func (s *lfsUploadMultipartSpy) CompleteMultipart(_ context.Context, request storage.CompleteMultipartRequest) error {
	s.events = append(s.events, "complete")
	s.completed = request
	return nil
}

type lfsUploadAccountingSpy struct {
	events *[]string
	object string
	err    error
}

func (s *lfsUploadAccountingSpy) RecordFileUpload(_ context.Context, objectID string) error {
	*s.events = append(*s.events, "account")
	s.object = objectID
	return s.err
}

func TestLFSUploadWorkflowPreservesPartSizeOrderAndAccountingOrder(t *testing.T) {
	events := make([]string, 0, 8)
	multipart := &lfsUploadMultipartSpy{events: events}
	accounting := &lfsUploadAccountingSpy{events: &multipart.events}
	partLengths := make([]int, 0, 2)
	workflow := NewLFSUploadWorkflow(NewService(Dependencies{Multipart: multipart}), func(_ context.Context, _ string, content []byte) (string, error) {
		multipart.events = append(multipart.events, "upload")
		partLengths = append(partLengths, len(content))
		return fmt.Sprintf("etag-%d", len(partLengths)), nil
	}, accounting)

	body := bytes.NewReader(bytes.Repeat([]byte{'x'}, lfsMultipartPartSize+1))
	if err := workflow.Upload(context.Background(), body, "bucket", "object", "record"); err != nil {
		t.Fatalf("Upload() error = %v", err)
	}

	wantEvents := []string{"begin", "sign", "upload", "sign", "upload", "complete", "account"}
	if strings.Join(multipart.events, ",") != strings.Join(wantEvents, ",") {
		t.Fatalf("events = %v, want %v", multipart.events, wantEvents)
	}
	if len(partLengths) != 2 || partLengths[0] != lfsMultipartPartSize || partLengths[1] != 1 {
		t.Fatalf("part lengths = %v", partLengths)
	}
	if len(multipart.partNumber) != 2 || multipart.partNumber[0] != 1 || multipart.partNumber[1] != 2 {
		t.Fatalf("part numbers = %v", multipart.partNumber)
	}
	if len(multipart.completed.Parts) != 2 || multipart.completed.Parts[0].ETag != "etag-1" || multipart.completed.Parts[1].ETag != "etag-2" {
		t.Fatalf("completed parts = %+v", multipart.completed.Parts)
	}
	if accounting.object != "record" {
		t.Fatalf("accounted object = %q", accounting.object)
	}
}

type lfsMetadataObjectSpy struct {
	events      *[]string
	getErr      error
	object      *objects.Record
	registered  []objects.Record
	registerErr error
}

func (s *lfsMetadataObjectSpy) GetObject(_ context.Context, _, _ string) (*objects.Record, error) {
	*s.events = append(*s.events, "get")
	return s.object, s.getErr
}

func (s *lfsMetadataObjectSpy) RegisterObjects(_ context.Context, records []objects.Record) error {
	*s.events = append(*s.events, "register")
	s.registered = append([]objects.Record(nil), records...)
	return s.registerErr
}

type lfsMetadataPendingSpy struct {
	events *[]string
	entry  *PendingMetadata
}

func (s *lfsMetadataPendingSpy) SavePendingLFSMeta(context.Context, []PendingMetadata) error {
	return nil
}

func (s *lfsMetadataPendingSpy) GetPendingLFSMeta(context.Context, string) (*PendingMetadata, error) {
	return s.entry, nil
}

func (s *lfsMetadataPendingSpy) PopPendingLFSMeta(context.Context, string) (*PendingMetadata, error) {
	*s.events = append(*s.events, "pop")
	return s.entry, nil
}

func TestLFSMetadataWorkflowConsumesRegistersThenAccounts(t *testing.T) {
	events := make([]string, 0, 5)
	sha := strings.Repeat("a", 64)
	methods := []objects.AccessMethod{{Type: "s3", AccessUrl: &objects.AccessURL{Url: "s3://bucket/" + sha}}}
	candidate := objects.Candidate{
		Aliases:       &[]string{"id:" + sha},
		Checksums:     &[]objects.Checksum{{Type: "sha256", Checksum: sha}},
		AccessMethods: &methods,
	}
	pending := &lfsMetadataPendingSpy{
		events: &events,
		entry:  &PendingMetadata{OID: sha, Candidate: candidate},
	}
	objectsPort := &lfsMetadataObjectSpy{events: &events, getErr: faults.ErrNotFound}
	accounting := &lfsUploadAccountingSpy{events: &events}
	workflow := NewLFSMetadataWorkflow(NewService(Dependencies{Pending: pending}), objectsPort, accounting)

	if err := workflow.Verify(context.Background(), sha); err != nil {
		t.Fatalf("Verify() error = %v", err)
	}

	wantEvents := []string{"get", "pop", "register", "account"}
	if strings.Join(events, ",") != strings.Join(wantEvents, ",") {
		t.Fatalf("events = %v, want %v", events, wantEvents)
	}
	if len(objectsPort.registered) != 1 {
		t.Fatalf("registered records = %+v", objectsPort.registered)
	}
	if accounting.object != string(objectsPort.registered[0].Id) {
		t.Fatalf("accounted object = %q, registered object = %q", accounting.object, objectsPort.registered[0].Id)
	}
}

func TestLFSMetadataWorkflowExistingObjectOnlyAccounts(t *testing.T) {
	events := make([]string, 0, 2)
	object := &objects.Record{Id: "existing"}
	objectsPort := &lfsMetadataObjectSpy{events: &events, object: object}
	accounting := &lfsUploadAccountingSpy{events: &events}
	workflow := NewLFSMetadataWorkflow(NewService(Dependencies{}), objectsPort, accounting)

	if err := workflow.Verify(context.Background(), "oid"); err != nil {
		t.Fatalf("Verify() error = %v", err)
	}

	wantEvents := []string{"get", "account"}
	if strings.Join(events, ",") != strings.Join(wantEvents, ",") {
		t.Fatalf("events = %v, want %v", events, wantEvents)
	}
	if accounting.object != "existing" {
		t.Fatalf("accounted object = %q", accounting.object)
	}
}
