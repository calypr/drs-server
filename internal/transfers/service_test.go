package transfers

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/calypr/syfon/apigen/drs"
	"github.com/calypr/syfon/internal/access"
	"github.com/calypr/syfon/internal/buckets"
	"github.com/calypr/syfon/internal/requestid"
	"github.com/calypr/syfon/internal/storage"
	"github.com/calypr/syfon/internal/usage"
)

type accessFake struct {
	requests    []storage.SignRequest
	partRequest storage.MultipartPartRequest
	result      storage.SignedAccess
	err         error
}

func (f *accessFake) Sign(_ context.Context, request storage.SignRequest) (storage.SignedAccess, error) {
	f.requests = append(f.requests, request)
	return f.result, f.err
}

func (f *accessFake) BeginMultipart(context.Context, storage.Target) (storage.UploadID, error) {
	return "", nil
}
func (f *accessFake) SignMultipartPart(_ context.Context, request storage.MultipartPartRequest) (storage.SignedAccess, error) {
	f.partRequest = request
	return f.result, f.err
}
func (f *accessFake) CompleteMultipart(context.Context, storage.CompleteMultipartRequest) error {
	return nil
}

type multipartFake struct {
	beginTarget storage.Target
	partRequest storage.MultipartPartRequest
	complete    storage.CompleteMultipartRequest
	beginID     storage.UploadID
	partAccess  storage.SignedAccess
	beginErr    error
	partErr     error
	completeErr error
}

func (f *multipartFake) Sign(_ context.Context, _ storage.SignRequest) (storage.SignedAccess, error) {
	return storage.SignedAccess{}, nil
}

func (f *multipartFake) BeginMultipart(_ context.Context, target storage.Target) (storage.UploadID, error) {
	f.beginTarget = target
	return f.beginID, f.beginErr
}

func (f *multipartFake) SignMultipartPart(_ context.Context, request storage.MultipartPartRequest) (storage.SignedAccess, error) {
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

type eventFake struct {
	events []usage.Event
	err    error
}

func (f *eventFake) RecordTransferAttributionEvents(_ context.Context, events []usage.Event) error {
	f.events = append([]usage.Event(nil), events...)
	return f.err
}

func testRecord() *drs.DrsObject {
	sha := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	accessID := "s3"
	url := "s3://legacy/object"
	methods := []drs.AccessMethod{{AccessId: &accessID, Type: "s3", AccessUrl: &drs.AccessURL{Url: url}}}
	resources := []string{"/organization/org/project/project"}
	return &drs.DrsObject{
		Id:               "record-1",
		Size:             42,
		Checksums:        []drs.Checksum{{Type: "sha256", Checksum: sha}},
		AccessMethods:    &methods,
		ControlledAccess: &resources,
	}
}

type downloadObjectFake struct {
	object *drs.DrsObject
}

func (f downloadObjectFake) GetObject(context.Context, string, string) (*drs.DrsObject, error) {
	return f.object, nil
}

func (downloadObjectFake) GetObjectsByChecksums(context.Context, []string, string) (map[string][]drs.DrsObject, error) {
	return nil, nil
}

type downloadAccountingFake struct {
	calls     []string
	objectIDs []string
}

func (f *downloadAccountingFake) RecordFileUpload(context.Context, string) error {
	return nil
}

func (f *downloadAccountingFake) RecordFileDownload(_ context.Context, objectID string) error {
	f.calls = append(f.calls, "counter")
	f.objectIDs = append(f.objectIDs, objectID)
	return nil
}

func (f *downloadAccountingFake) RecordTransferAttributionEvents(context.Context, []usage.Event) error {
	f.calls = append(f.calls, "event")
	return nil
}

func TestDownloadWithoutOptionalAccountingStillSigns(t *testing.T) {
	access := &accessFake{result: storage.SignedAccess{Location: "signed-download"}}
	service := NewService(Dependencies{
		Objects: downloadObjectFake{object: testRecord()},
		Storage: access,
	})

	result, err := service.Download(context.Background(), DownloadRequest{
		ObjectID:   "record-1",
		Accounting: AccountingDownloadBeforeEvent,
	})
	if err != nil {
		t.Fatalf("Download() with no optional accounting recorder failed: %v", err)
	}
	if result.URL != "signed-download" {
		t.Fatalf("Download() URL = %q, want signed-download", result.URL)
	}
}

func TestSigningExpiryUsesConfiguredDefaultAcrossOperations(t *testing.T) {
	const configured = 37 * time.Second
	for _, test := range []struct {
		name string
		call func(*Service, *accessFake) error
		get  func(*accessFake) time.Duration
	}{
		{
			name: "download",
			call: func(service *Service, fake *accessFake) error {
				_, err := service.Download(context.Background(), DownloadRequest{ObjectID: "record-1"})
				return err
			},
			get: func(fake *accessFake) time.Duration { return fake.requests[0].ExpiresIn },
		},
		{
			name: "upload",
			call: func(service *Service, fake *accessFake) error {
				_, err := service.UploadURL(context.Background(), UploadRequest{ObjectID: "record-1"})
				return err
			},
			get: func(fake *accessFake) time.Duration { return fake.requests[0].ExpiresIn },
		},
		{
			name: "drs access",
			call: func(service *Service, fake *accessFake) error {
				_, err := service.IssueAccess(context.Background(), AccessLookupRequest{ObjectID: "record-1", AccessID: "s3"})
				return err
			},
			get: func(fake *accessFake) time.Duration { return fake.requests[0].ExpiresIn },
		},
		{
			name: "multipart part",
			call: func(service *Service, fake *accessFake) error {
				if _, err := service.BeginMultipart(context.Background(), MultipartInitRequest{Target: &storage.Target{PhysicalBucket: "bucket", Key: "key"}}); err != nil {
					return err
				}
				_, err := service.SignMultipartPart(context.Background(), "", 1)
				return err
			},
			get: func(fake *accessFake) time.Duration { return fake.partRequest.ExpiresIn },
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			storage := &accessFake{result: storage.SignedAccess{Location: "signed"}}
			service := NewService(Dependencies{
				Objects:              downloadObjectFake{object: testRecord()},
				Storage:              storage,
				Events:               &eventFake{},
				DefaultSigningExpiry: configured,
			})
			if err := test.call(service, storage); err != nil {
				t.Fatalf("operation failed: %v", err)
			}
			if got := test.get(storage); got != configured {
				t.Fatalf("expiry = %s, want configured %s", got, configured)
			}
		})
	}
}

func TestSigningExpiryDefaultsAndExplicitRequestOverride(t *testing.T) {
	storage := &accessFake{result: storage.SignedAccess{Location: "signed"}}
	service := NewService(Dependencies{Objects: downloadObjectFake{object: testRecord()}, Storage: storage})
	if _, err := service.Download(context.Background(), DownloadRequest{ObjectID: "record-1"}); err != nil {
		t.Fatalf("default download failed: %v", err)
	}
	if got, want := storage.requests[0].ExpiresIn, 15*time.Minute; got != want {
		t.Fatalf("default expiry = %s, want %s", got, want)
	}
	if _, err := service.Download(context.Background(), DownloadRequest{ObjectID: "record-1", ExpiresIn: 41 * time.Second}); err != nil {
		t.Fatalf("explicit download failed: %v", err)
	}
	if got, want := storage.requests[1].ExpiresIn, 41*time.Second; got != want {
		t.Fatalf("explicit expiry = %s, want %s", got, want)
	}
}

func TestDownloadAccountingPreservesConfiguredRecorderOrder(t *testing.T) {
	accounting := &downloadAccountingFake{}
	service := NewService(Dependencies{
		Objects:      downloadObjectFake{object: testRecord()},
		Storage:      &accessFake{result: storage.SignedAccess{Location: "signed-download"}},
		FileCounters: accounting,
		Events:       accounting,
	})

	if _, err := service.Download(context.Background(), DownloadRequest{ObjectID: "record-1", Accounting: AccountingDownloadBeforeEvent}); err != nil {
		t.Fatalf("Download() failed with configured accounting: %v", err)
	}
	if got, want := accounting.calls, []string{"counter", "event"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("accounting call order = %v, want %v", got, want)
	}
	if got, want := accounting.objectIDs, []string{"record-1"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("accounting object IDs = %v, want resolved IDs %v", got, want)
	}
}

func TestDownloadAccountingUsesResolvedObjectIDForAlias(t *testing.T) {
	accounting := &downloadAccountingFake{}
	service := NewService(Dependencies{
		Objects:      downloadObjectFake{object: testRecord()},
		Storage:      &accessFake{result: storage.SignedAccess{Location: "signed-download"}},
		FileCounters: accounting,
		Events:       accounting,
	})

	if _, err := service.Download(context.Background(), DownloadRequest{ObjectID: "submitted-alias", Accounting: AccountingDownloadBeforeEvent}); err != nil {
		t.Fatalf("Download() failed: %v", err)
	}
	if got, want := accounting.objectIDs, []string{"record-1"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("accounting object IDs = %v, want resolved IDs %v", got, want)
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

func TestMultipartDelegationPreservesOpaqueIDAndPartOrder(t *testing.T) {
	port := &multipartFake{beginID: "provider/upload/id", partAccess: storage.SignedAccess{Location: "part-signed"}}
	service := NewService(Dependencies{Objects: downloadObjectFake{object: testRecord()}, Storage: port})
	ctx := context.Background()
	target := storage.Target{PhysicalBucket: "bucket", LookupKey: "bucket", Key: "key", LookupCandidates: []string{"bucket"}}
	guid := "opaque-guid"
	result, err := service.BeginMultipart(ctx, MultipartInitRequest{Target: &target, GUID: &guid})
	if err != nil || result.UploadID != "provider/upload/id" {
		t.Fatalf("BeginMultipart()=(%+v,%v)", result, err)
	}
	part, err := service.SignMultipartPart(ctx, result.UploadID, 7)
	if err != nil || part != "part-signed" {
		t.Fatalf("SignMultipartPart()=(%q,%v)", part, err)
	}
	parts := []CompletedPart{{PartNumber: 7, ETag: "seven"}, {PartNumber: 2, ETag: "two"}}
	providerParts := []storage.CompletedPart{{PartNumber: 7, ETag: "seven"}, {PartNumber: 2, ETag: "two"}}
	if err := service.CompleteMultipart(ctx, result.UploadID, parts); err != nil {
		t.Fatalf("CompleteMultipartUpload() error = %v", err)
	}
	if port.partRequest.UploadID != storage.UploadID(result.UploadID) || port.partRequest.PartNumber != 7 {
		t.Fatalf("unexpected part request: %+v", port.partRequest)
	}
	if !reflect.DeepEqual(port.complete.Parts, providerParts) {
		t.Fatalf("multipart parts reordered: got=%+v want=%+v", port.complete.Parts, providerParts)
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
	if _, err := service.BeginMultipart(context.Background(), MultipartInitRequest{Target: &storage.Target{PhysicalBucket: "bucket", Key: "key"}}); err == nil {
		t.Fatal("BeginMultipart() unexpectedly succeeded without multipart port")
	}
}
