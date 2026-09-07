package transfers

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/calypr/syfon/internal/access"
	"github.com/calypr/syfon/internal/buckets"
	"github.com/calypr/syfon/internal/faults"
	"github.com/calypr/syfon/internal/objects"
	"github.com/calypr/syfon/internal/requestid"
	"github.com/calypr/syfon/internal/storage"
	"github.com/calypr/syfon/internal/usage"
)

type accessFake struct {
	requests []storage.AccessRequest
	result   storage.Access
	err      error
}

func (f *accessFake) Access(_ context.Context, request storage.AccessRequest) (storage.Access, error) {
	f.requests = append(f.requests, request)
	return f.result, f.err
}

type multipartFake struct {
	beginTarget storage.ObjectTarget
	partRequest storage.MultipartPartRequest
	complete    storage.CompleteMultipartRequest
	beginID     storage.UploadID
	partAccess  storage.Access
	beginErr    error
	partErr     error
	completeErr error
}

func (f *multipartFake) BeginMultipart(_ context.Context, target storage.ObjectTarget) (storage.UploadID, error) {
	f.beginTarget = target
	return f.beginID, f.beginErr
}

func (f *multipartFake) AccessMultipartPart(_ context.Context, request storage.MultipartPartRequest) (storage.Access, error) {
	f.partRequest = request
	return f.partAccess, f.partErr
}

func (f *multipartFake) CompleteMultipart(_ context.Context, request storage.CompleteMultipartRequest) error {
	f.complete = request
	return f.completeErr
}

type scopeFake struct {
	scopes map[string]buckets.Scope
}

func (f scopeFake) LookupBucketScope(_ context.Context, organization, project string) (buckets.Scope, bool, error) {
	scope, ok := f.scopes[organization+"|"+project]
	return scope, ok, nil
}

type credentialFake struct {
	credentials []buckets.Credential
}

func (f credentialFake) ListS3Credentials(context.Context) ([]buckets.Credential, error) {
	return f.credentials, nil
}

type pendingFake struct {
	saved []PendingMetadata
	got   *PendingMetadata
	pop   *PendingMetadata
}

func (f *pendingFake) SavePendingLFSMeta(_ context.Context, entries []PendingMetadata) error {
	f.saved = append([]PendingMetadata(nil), entries...)
	return nil
}

func (f *pendingFake) GetPendingLFSMeta(context.Context, string) (*PendingMetadata, error) {
	return f.got, nil
}

func (f *pendingFake) PopPendingLFSMeta(context.Context, string) (*PendingMetadata, error) {
	return f.pop, nil
}

type eventFake struct {
	events []usage.Event
	err    error
}

func (f *eventFake) RecordTransferAttributionEvents(_ context.Context, events []usage.Event) error {
	f.events = append([]usage.Event(nil), events...)
	return f.err
}

func testRecord() *objects.Record {
	sha := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	accessID := "s3"
	url := "s3://legacy/object"
	methods := []objects.AccessMethod{{AccessId: &accessID, Type: "s3", AccessUrl: &objects.AccessURL{Url: url}}}
	resources := []string{"/organization/org/project/project"}
	return &objects.Record{
		Id:               "record-1",
		Size:             42,
		Checksums:        []objects.Checksum{{Type: "sha256", Checksum: sha}},
		AccessMethods:    &methods,
		ControlledAccess: &resources,
		Authorizations:   map[string][]string{"org": {"project"}},
	}
}

func TestResolveCanonicalStorageTargetComposesScopesAndPrefixes(t *testing.T) {
	service := NewService(Dependencies{Scopes: scopeFake{scopes: map[string]buckets.Scope{
		"org|":        {Organization: "org", Bucket: "physical", PathPrefix: "org-prefix"},
		"org|project": {Organization: "org", ProjectID: "project", Bucket: "physical", PathPrefix: "project-prefix"},
	}}})
	target, err := service.ResolveCanonicalStorageTarget(context.Background(), CanonicalStorageTargetRequest{Object: testRecord(), AccessURL: "s3://legacy/object"})
	if err != nil {
		t.Fatalf("ResolveCanonicalStorageTarget() error = %v", err)
	}
	if target.Bucket != "physical" || target.Key != "org-prefix/project-prefix/object" || target.URL != "s3://physical/org-prefix/project-prefix/object" {
		t.Fatalf("unexpected target: %+v", target)
	}
}

func TestSignObjectURLRepairsLegacyPhysicalURLBeforeDelegating(t *testing.T) {
	accessPort := &accessFake{result: storage.Access{Location: "signed"}}
	service := NewService(Dependencies{
		Access: accessPort,
		Scopes: scopeFake{scopes: map[string]buckets.Scope{
			"org|project": {Organization: "org", ProjectID: "project", Bucket: "physical", PathPrefix: "legacy"},
		}},
		Credentials: credentialFake{},
	})
	got, err := service.SignObjectURL(context.Background(), testRecord(), "s3://legacy/object", storage.AccessOptions{})
	if err != nil {
		t.Fatalf("SignObjectURL() error = %v", err)
	}
	if got != "signed" || len(accessPort.requests) != 1 {
		t.Fatalf("unexpected signed result or calls: got=%q requests=%+v", got, accessPort.requests)
	}
	request := accessPort.requests[0]
	if request.Target.AccessID != "physical" || request.Target.Location != "s3://physical/legacy/object" {
		t.Fatalf("unexpected storage request: %+v", request)
	}
}

func TestMultipartDelegationPreservesOpaqueIDAndPartOrder(t *testing.T) {
	port := &multipartFake{beginID: "provider/upload/id", partAccess: storage.Access{Location: "part-signed"}}
	service := NewService(Dependencies{Multipart: port})
	ctx := context.Background()
	id, err := service.InitMultipartUpload(ctx, "bucket", "key")
	if err != nil || id != "provider/upload/id" {
		t.Fatalf("InitMultipartUpload()=(%q,%v)", id, err)
	}
	part, err := service.SignMultipartPart(ctx, "bucket", "key", id, 7)
	if err != nil || part != "part-signed" {
		t.Fatalf("SignMultipartPart()=(%q,%v)", part, err)
	}
	parts := []storage.CompletedPart{{PartNumber: 7, ETag: "seven"}, {PartNumber: 2, ETag: "two"}}
	if err := service.CompleteMultipartUpload(ctx, "bucket", "key", id, parts); err != nil {
		t.Fatalf("CompleteMultipartUpload() error = %v", err)
	}
	if port.partRequest.UploadID != storage.UploadID(id) || port.partRequest.PartNumber != 7 {
		t.Fatalf("unexpected part request: %+v", port.partRequest)
	}
	if !reflect.DeepEqual(port.complete.Parts, parts) {
		t.Fatalf("multipart parts reordered: got=%+v want=%+v", port.complete.Parts, parts)
	}
}

func TestStagePendingMetadataDefaultsCanonicalOIDAndTwentyMinuteTTL(t *testing.T) {
	pending := &pendingFake{}
	service := NewService(Dependencies{Pending: pending})
	now := time.Date(2026, 9, 6, 12, 0, 0, 0, time.FixedZone("PDT", -7*60*60))
	service.now = func() time.Time { return now }
	sha := "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	candidate := objects.Candidate{Checksums: &[]objects.Checksum{{Type: "sha256", Checksum: sha}}}
	if err := service.StagePendingMetadata(context.Background(), PendingMetadata{Candidate: candidate}); err != nil {
		t.Fatalf("StagePendingMetadata() error = %v", err)
	}
	if len(pending.saved) != 1 {
		t.Fatalf("saved %d entries, want 1", len(pending.saved))
	}
	got := pending.saved[0]
	if got.OID != sha || !got.CreatedAt.Equal(now.UTC()) || !got.ExpiresAt.Equal(now.UTC().Add(PendingMetadataTTL)) {
		t.Fatalf("unexpected staged metadata: %+v", got)
	}
}

func TestEventFromObjectPreservesContextAndRangeProjection(t *testing.T) {
	service := NewService(Dependencies{Events: &eventFake{}})
	ctx := requestid.WithRequestID(context.Background(), "request-1")
	session := access.NewSession("jwt")
	session.SetSubject("subject@example.org")
	session.SetClaims(map[string]interface{}{"preferred_username": "preferred@example.org"})
	ctx = access.WithSession(ctx, session)
	start, end := int64(10), int64(19)
	if err := service.RecordAccessIssued(ctx, AccessRequest{Object: testRecord(), AccessID: "s3", RangeStart: &start, RangeEnd: &end}); err != nil {
		t.Fatalf("RecordAccessIssued() error = %v", err)
	}
	recorder := service.events.(*eventFake)
	if len(recorder.events) != 1 {
		t.Fatalf("recorded %d events, want 1", len(recorder.events))
	}
	event := recorder.events[0]
	if event.RequestID != "request-1" || event.ActorEmail != "preferred@example.org" || event.ActorSubject != "subject@example.org" || event.AuthMode != "jwt" {
		t.Fatalf("unexpected identity projection: %+v", event)
	}
	if event.Direction != usage.ProviderTransferDirectionDownload || event.BytesRequested != 10 || event.RangeStart == nil || event.RangeEnd == nil || *event.RangeStart != 10 || *event.RangeEnd != 19 {
		t.Fatalf("unexpected range projection: %+v", event)
	}
	if event.AccessGrantID != usage.GrantID(event) || event.EventID != usage.EventID(event) {
		t.Fatalf("event identity was not projected through usage: %+v", event)
	}
}

func TestUnconfiguredWorkflowsReturnConfigurationErrors(t *testing.T) {
	service := NewService(Dependencies{})
	if _, err := service.SignURL(context.Background(), "s3://bucket/key", storage.AccessOptions{}); err == nil {
		t.Fatal("SignURL() unexpectedly succeeded without access port")
	}
	if _, err := service.InitMultipartUpload(context.Background(), "bucket", "key"); err == nil {
		t.Fatal("InitMultipartUpload() unexpectedly succeeded without multipart port")
	}
	if err := service.StagePendingMetadata(context.Background(), PendingMetadata{}); !errors.Is(err, faults.ErrInvalidInput) {
		t.Fatalf("StagePendingMetadata() error = %v, want invalid input", err)
	}
}
