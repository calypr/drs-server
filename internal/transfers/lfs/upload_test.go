package lfs

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/calypr/syfon/apigen/drs"
	"github.com/calypr/syfon/apigen/errorapi"
	"github.com/calypr/syfon/apigen/lfsapi"
	"github.com/calypr/syfon/internal/storage"
	"github.com/calypr/syfon/internal/transfers"
)

type lfsUploadMultipartSpy struct {
	events     []string
	partURLs   []string
	partNumber []int32
	completed  storage.CompleteMultipartRequest
}

func (s *lfsUploadMultipartSpy) Sign(context.Context, storage.SignRequest) (storage.SignedAccess, error) {
	return storage.SignedAccess{Location: "https://provider.invalid/object"}, nil
}

func (s *lfsUploadMultipartSpy) BeginMultipart(_ context.Context, target storage.Target) (storage.UploadID, error) {
	s.events = append(s.events, "begin")
	if target.PhysicalBucket != "bucket" || target.Key != "object" {
		return "", fmt.Errorf("unexpected target: %+v", target)
	}
	return "opaque-upload-id", nil
}

func (s *lfsUploadMultipartSpy) SignMultipartPart(_ context.Context, request storage.MultipartPartRequest) (storage.SignedAccess, error) {
	s.events = append(s.events, "sign")
	s.partURLs = append(s.partURLs, request.Target.PhysicalBucket+"/"+request.Target.Key)
	s.partNumber = append(s.partNumber, request.PartNumber)
	return storage.SignedAccess{Location: "https://provider.invalid/part"}, nil
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

type lfsUploadObjectSpy struct{}

func (lfsUploadObjectSpy) GetObject(context.Context, string, string) (*drs.DrsObject, error) {
	url := "s3://bucket/object"
	methods := []drs.AccessMethod{{Type: "s3", AccessUrl: &drs.AccessURL{Url: url}}}
	return &drs.DrsObject{Id: "record", AccessMethods: &methods}, nil
}
func (lfsUploadObjectSpy) GetObjectsByChecksums(context.Context, []string, string) (map[string][]drs.DrsObject, error) {
	return nil, nil
}
func (lfsUploadObjectSpy) RegisterObjects(context.Context, []drs.DrsObject) ([]drs.DrsObject, error) {
	return nil, nil
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
	transfer := transfers.NewService(transfers.Dependencies{Storage: multipart, Objects: lfsUploadObjectSpy{}})
	service := NewService(transfer, lfsUploadObjectSpy{}, nil, nil, accounting, func(_ context.Context, _ string, content []byte) (string, error) {
		multipart.events = append(multipart.events, "upload")
		partLengths = append(partLengths, len(content))
		return fmt.Sprintf("etag-%d", len(partLengths)), nil
	})

	body := bytes.NewReader(bytes.Repeat([]byte{'x'}, multipartPartSize+1))
	if err := service.UploadProxy(context.Background(), "record", body); err != nil {
		t.Fatalf("Upload() error = %v", err)
	}

	wantEvents := []string{"begin", "sign", "upload", "sign", "upload", "complete", "account"}
	if strings.Join(multipart.events, ",") != strings.Join(wantEvents, ",") {
		t.Fatalf("events = %v, want %v", multipart.events, wantEvents)
	}
	if len(partLengths) != 2 || partLengths[0] != multipartPartSize || partLengths[1] != 1 {
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
	events              *[]string
	getErr              error
	object              *drs.DrsObject
	registered          []drs.DrsObject
	registerErr         error
	objectAfterRegister bool
}

func (s *lfsMetadataObjectSpy) GetObject(_ context.Context, _, _ string) (*drs.DrsObject, error) {
	*s.events = append(*s.events, "get")
	return s.object, s.getErr
}

func (s *lfsMetadataObjectSpy) RegisterObjects(_ context.Context, records []drs.DrsObject) ([]drs.DrsObject, error) {
	*s.events = append(*s.events, "register")
	s.registered = append([]drs.DrsObject(nil), records...)
	if s.objectAfterRegister && len(records) > 0 {
		s.object = &records[0]
		s.getErr = nil
	}
	if s.registerErr != nil {
		return nil, s.registerErr
	}
	return records, nil
}

type metadataPendingSpy struct {
	events      *[]string
	entry       *PendingMetadata
	replacement *PendingMetadata
	getErr      error
	consumeErr  error
}

func (s *metadataPendingSpy) SavePendingMetadata(context.Context, []PendingMetadata) error {
	return nil
}

func (s *metadataPendingSpy) GetPendingMetadata(context.Context, string) (*PendingMetadata, error) {
	if s.getErr != nil {
		return nil, s.getErr
	}
	if s.entry == nil {
		return nil, fmt.Errorf("%w: pending metadata not found", errorapi.ErrNotFound)
	}
	return s.entry, nil
}

func (s *metadataPendingSpy) ConsumePendingMetadata(_ context.Context, _ PendingMetadata) (bool, error) {
	*s.events = append(*s.events, "consume")
	if s.consumeErr != nil {
		return false, s.consumeErr
	}
	if s.replacement != nil {
		s.entry = s.replacement
		s.replacement = nil
		return false, nil
	}
	s.entry = nil
	return true, nil
}

func TestLFSMetadataWorkflowReportsPendingLookupFailureForExistingObject(t *testing.T) {
	events := make([]string, 0, 1)
	pendingErr := fmt.Errorf("pending database unavailable")
	service := NewService(nil,
		&lfsMetadataObjectSpy{events: &events, object: &drs.DrsObject{Id: "record"}},
		nil,
		&metadataPendingSpy{events: &events, getErr: pendingErr},
		&lfsUploadAccountingSpy{events: &events},
		nil,
	)

	if err := service.Verify(context.Background(), "record"); err != pendingErr {
		t.Fatalf("Verify() error = %v, want %v", err, pendingErr)
	}
	if strings.Join(events, ",") != "get" {
		t.Fatalf("events = %v, want pending failure before accounting", events)
	}
}

func TestLFSMetadataWorkflowConsumesRegistersThenAccounts(t *testing.T) {
	events := make([]string, 0, 5)
	sha := strings.Repeat("a", 64)
	typeName := "s3"
	url := "s3://bucket/" + sha
	methods := []lfsapi.AccessMethod{{Type: &typeName, AccessUrl: &lfsapi.AccessMethodAccessUrl{Url: &url}}}
	candidate := lfsapi.DrsObjectCandidate{
		Aliases:       &[]string{"id:" + sha},
		Checksums:     &[]lfsapi.Checksum{{Type: "sha256", Checksum: sha}},
		AccessMethods: &methods,
	}
	pending := &metadataPendingSpy{
		events: &events,
		entry:  &PendingMetadata{OID: sha, Candidate: candidate},
	}
	objectsPort := &lfsMetadataObjectSpy{events: &events, getErr: errorapi.ErrNotFound}
	accounting := &lfsUploadAccountingSpy{events: &events}
	service := NewService(nil, objectsPort, nil, pending, accounting, nil)

	if err := service.Verify(context.Background(), sha); err != nil {
		t.Fatalf("Verify() error = %v", err)
	}

	wantEvents := []string{"get", "register", "consume", "account"}
	if strings.Join(events, ",") != strings.Join(wantEvents, ",") {
		t.Fatalf("events = %v, want %v", events, wantEvents)
	}
	if len(objectsPort.registered) != 1 {
		t.Fatalf("registered records = %+v", objectsPort.registered)
	}
	if accounting.object != objectsPort.registered[0].Id {
		t.Fatalf("accounted object = %q, registered object = %q", accounting.object, objectsPort.registered[0].Id)
	}
	if pending.entry != nil {
		t.Fatal("pending metadata was not consumed")
	}
}

func TestLFSMetadataWorkflowRetainsPendingMetadataWhenRegistrationFails(t *testing.T) {
	events := make([]string, 0, 8)
	sha := strings.Repeat("b", 64)
	typeName := "s3"
	url := "s3://bucket/" + sha
	methods := []lfsapi.AccessMethod{{Type: &typeName, AccessUrl: &lfsapi.AccessMethodAccessUrl{Url: &url}}}
	candidate := lfsapi.DrsObjectCandidate{
		Checksums:     &[]lfsapi.Checksum{{Type: "sha256", Checksum: sha}},
		AccessMethods: &methods,
	}
	pending := &metadataPendingSpy{
		events: &events,
		entry:  &PendingMetadata{OID: sha, Candidate: candidate},
	}
	registerErr := fmt.Errorf("registration unavailable")
	objectsPort := &lfsMetadataObjectSpy{events: &events, getErr: errorapi.ErrNotFound, registerErr: registerErr}
	accounting := &lfsUploadAccountingSpy{events: &events}
	service := NewService(nil, objectsPort, nil, pending, accounting, nil)

	if err := service.Verify(context.Background(), sha); err != registerErr {
		t.Fatalf("first Verify() error = %v, want %v", err, registerErr)
	}
	if pending.entry == nil {
		t.Fatal("pending metadata was consumed after registration failure")
	}
	if strings.Join(events, ",") != "get,register" {
		t.Fatalf("first Verify() events = %v, want [get register]", events)
	}

	objectsPort.registerErr = nil
	if err := service.Verify(context.Background(), sha); err != nil {
		t.Fatalf("retry Verify() error = %v", err)
	}
	if pending.entry != nil {
		t.Fatal("pending metadata was not consumed after successful retry")
	}
	if strings.Join(events, ",") != "get,register,get,register,consume,account" {
		t.Fatalf("retry Verify() events = %v", events)
	}
	if accounting.object == "" {
		t.Fatal("retry did not record upload")
	}
}

func TestLFSMetadataWorkflowProcessesPendingReplacementAfterObjectExists(t *testing.T) {
	events := make([]string, 0, 8)
	sha := strings.Repeat("d", 64)
	typeName := "s3"
	candidate := func(url string) lfsapi.DrsObjectCandidate {
		return lfsapi.DrsObjectCandidate{
			Checksums:     &[]lfsapi.Checksum{{Type: "sha256", Checksum: sha}},
			AccessMethods: &[]lfsapi.AccessMethod{{Type: &typeName, AccessUrl: &lfsapi.AccessMethodAccessUrl{Url: &url}}},
		}
	}
	oldURL := "s3://bucket/old/" + sha
	replacementURL := "s3://bucket/replacement/" + sha
	pending := &metadataPendingSpy{
		events: &events,
		entry: &PendingMetadata{
			OID:       sha,
			Candidate: candidate(oldURL),
		},
		replacement: &PendingMetadata{
			OID:       sha,
			Candidate: candidate(replacementURL),
		},
	}
	objectsPort := &lfsMetadataObjectSpy{
		events:              &events,
		getErr:              errorapi.ErrNotFound,
		objectAfterRegister: true,
	}
	accounting := &lfsUploadAccountingSpy{events: &events}
	service := NewService(nil, objectsPort, nil, pending, accounting, nil)

	if err := service.Verify(context.Background(), sha); err != nil {
		t.Fatalf("first Verify() error = %v", err)
	}
	if err := service.Verify(context.Background(), sha); err != nil {
		t.Fatalf("replacement Verify() error = %v", err)
	}

	if len(objectsPort.registered) != 1 {
		t.Fatalf("registered records = %d, want 1 final record", len(objectsPort.registered))
	}
	if methods := objectsPort.registered[0].AccessMethods; methods == nil || len(*methods) != 1 || (*methods)[0].AccessUrl == nil || (*methods)[0].AccessUrl.Url != replacementURL {
		t.Fatalf("registered record did not use replacement metadata: %+v", objectsPort.registered[0])
	}
	if pending.entry != nil {
		t.Fatal("replacement pending metadata was not consumed")
	}
	if strings.Join(events, ",") != "get,register,consume,get,register,consume,account" {
		t.Fatalf("events = %v, want replacement registration before accounting", events)
	}
}

func TestLFSMetadataWorkflowReturnsConsumptionFailureAfterRegistration(t *testing.T) {
	events := make([]string, 0, 5)
	sha := strings.Repeat("c", 64)
	typeName := "s3"
	url := "s3://bucket/" + sha
	methods := []lfsapi.AccessMethod{{Type: &typeName, AccessUrl: &lfsapi.AccessMethodAccessUrl{Url: &url}}}
	candidate := lfsapi.DrsObjectCandidate{
		Checksums:     &[]lfsapi.Checksum{{Type: "sha256", Checksum: sha}},
		AccessMethods: &methods,
	}
	consumeErr := fmt.Errorf("pending database unavailable")
	pending := &metadataPendingSpy{
		events:     &events,
		entry:      &PendingMetadata{OID: sha, Candidate: candidate},
		consumeErr: consumeErr,
	}
	objectsPort := &lfsMetadataObjectSpy{events: &events, getErr: errorapi.ErrNotFound}
	accounting := &lfsUploadAccountingSpy{events: &events}
	service := NewService(nil, objectsPort, nil, pending, accounting, nil)

	if err := service.Verify(context.Background(), sha); err != consumeErr {
		t.Fatalf("Verify() error = %v, want %v", err, consumeErr)
	}
	if len(objectsPort.registered) != 1 {
		t.Fatalf("registered records = %d, want 1", len(objectsPort.registered))
	}
	if accounting.object != "" {
		t.Fatalf("accounting ran after consumption failure for %q", accounting.object)
	}
	if strings.Join(events, ",") != "get,register,consume" {
		t.Fatalf("events = %v, want [get register consume]", events)
	}
}

func TestLFSMetadataWorkflowExistingObjectWithoutPendingMetadataIsAlreadyVerified(t *testing.T) {
	events := make([]string, 0, 2)
	object := &drs.DrsObject{Id: "existing"}
	objectsPort := &lfsMetadataObjectSpy{events: &events, object: object}
	accounting := &lfsUploadAccountingSpy{events: &events}
	service := NewService(nil, objectsPort, nil, nil, accounting, nil)

	if err := service.Verify(context.Background(), "oid"); err != nil {
		t.Fatalf("Verify() error = %v", err)
	}

	wantEvents := []string{"get"}
	if strings.Join(events, ",") != strings.Join(wantEvents, ",") {
		t.Fatalf("events = %v, want %v", events, wantEvents)
	}
	if accounting.object != "" {
		t.Fatalf("unexpected accounting for already verified object %q", accounting.object)
	}
}
