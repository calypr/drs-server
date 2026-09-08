package transfers

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/calypr/syfon/apigen/internalapi"
	"github.com/calypr/syfon/internal/buckets"
	"github.com/calypr/syfon/internal/objects"
	objectrecords "github.com/calypr/syfon/internal/objects/records"
	"github.com/calypr/syfon/internal/persistence/store"
	"github.com/calypr/syfon/internal/storage"
	domaintransfers "github.com/calypr/syfon/internal/transfers"
	"github.com/calypr/syfon/internal/usage"
	"github.com/gofiber/fiber/v3"
)

type failedUploadReader struct{ *store.Store }

func (failedUploadReader) GetObject(context.Context, string) (*objects.Record, error) {
	return nil, errors.New("database lookup failed: QA_PRIVATE_PROVIDER_DETAIL")
}

func (failedUploadReader) GetBulkObjects(context.Context, []string) ([]objects.Record, error) {
	return nil, errors.New("database lookup failed: QA_PRIVATE_PROVIDER_DETAIL")
}

func TestBulkUploadRedactsServerCause(t *testing.T) {
	service := objectrecords.NewService(failedUploadReader{})
	app := fiber.New()
	app.Post("/bulk", handleInternalUploadBulkFiber(service, nil))

	resp, err := app.Test(httptest.NewRequest("POST", "/bulk", strings.NewReader(`{"requests":[{"file_id":"record-id"}]}`)))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 207 {
		t.Fatalf("expected partial success, got %d body=%s", resp.StatusCode, body)
	}
	var output internalapi.InternalUploadBulkOutput
	if err := json.Unmarshal(body, &output); err != nil {
		t.Fatal(err)
	}
	if output.Results == nil || len(*output.Results) != 1 || (*output.Results)[0].Error == nil || *(*output.Results)[0].Error == "" {
		t.Fatalf("expected one failed result, got %+v", output.Results)
	}
	if strings.Contains(*(*output.Results)[0].Error, "QA_PRIVATE_PROVIDER_DETAIL") {
		t.Fatal("internal provider detail leaked into partial-success response")
	}
}

type bulkProviderReader struct {
	*store.Store
	objects map[string]*objects.Record
	errID   string
	err     error
}

func (r *bulkProviderReader) GetObject(_ context.Context, id string) (*objects.Record, error) {
	if id == r.errID {
		return nil, r.err
	}
	obj, ok := r.objects[id]
	if !ok {
		return nil, errors.New("object missing")
	}
	return obj, nil
}

func (r *bulkProviderReader) GetBulkObjects(_ context.Context, ids []string) ([]objects.Record, error) {
	out := make([]objects.Record, 0, len(ids))
	for _, id := range ids {
		obj, err := r.GetObject(context.Background(), id)
		if err != nil {
			return nil, err
		}
		out = append(out, *obj)
	}
	return out, nil
}

type bulkScopeFailure struct{ err error }

func (s bulkScopeFailure) LookupBucketScope(context.Context, string, string) (buckets.Scope, bool, error) {
	return buckets.Scope{}, false, s.err
}

type bulkAccessFailure struct {
	failID string
	err    error
}

func (a bulkAccessFailure) Access(_ context.Context, request storage.AccessRequest) (storage.Access, error) {
	if strings.Contains(request.Target.Location, "/"+a.failID) {
		return storage.Access{}, a.err
	}
	return storage.Access{Location: request.Target.Location + "?signed=true"}, nil
}

type bulkEventFailure struct {
	failID string
	err    error
}

func (e bulkEventFailure) RecordTransferAttributionEvents(_ context.Context, events []usage.Event) error {
	for _, event := range events {
		if event.ObjectID == e.failID {
			return e.err
		}
	}
	return nil
}

func bulkUploadRecord(id string, scoped bool) *objects.Record {
	obj := &objects.Record{
		Id: objects.RecordID(id),
		AccessMethods: &[]objects.AccessMethod{{
			Type:      "s3",
			AccessUrl: &objects.AccessURL{Url: "s3://bucket/" + id},
		}},
	}
	if scoped {
		controlled := []string{"/organization/org/project/project"}
		obj.ControlledAccess = &controlled
	}
	return obj
}

func TestBulkUploadProviderFailuresRedactCauseAndKeepSuccess(t *testing.T) {
	providerError := func(capability string) error {
		return &storage.OperationError{
			Kind:       storage.ErrorInvalid,
			Provider:   "s3",
			Capability: capability,
			Cause:      errors.New("QA_PRIVATE_PROVIDER_DETAIL"),
		}
	}
	tests := []struct {
		name       string
		failure    string
		readerErr  error
		scopeErr   error
		accessErr  error
		eventErr   error
		scopedFail bool
	}{
		{name: "lookup", failure: "lookup", readerErr: providerError("lookup")},
		{name: "target", failure: "target", scopeErr: providerError("target"), scopedFail: true},
		{name: "sign", failure: "sign", accessErr: providerError("sign")},
		{name: "record", failure: "record", eventErr: providerError("record")},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			const failID = "fail"
			reader := &bulkProviderReader{objects: map[string]*objects.Record{
				failID:    bulkUploadRecord(failID, tc.scopedFail),
				"success": bulkUploadRecord("success", false),
			}, errID: "", err: tc.readerErr}
			if tc.readerErr != nil {
				reader.errID = failID
			}
			var scopes domaintransfers.ScopeReader
			if tc.scopeErr != nil {
				scopes = bulkScopeFailure{err: tc.scopeErr}
			}
			var access domaintransfers.AccessPort
			if tc.accessErr != nil {
				access = bulkAccessFailure{failID: failID, err: tc.accessErr}
			} else {
				access = bulkAccessFailure{failID: "never", err: errors.New("unused")}
			}
			var events domaintransfers.EventRecorder
			if tc.eventErr != nil {
				events = bulkEventFailure{failID: failID, err: tc.eventErr}
			} else {
				events = bulkEventFailure{failID: "never", err: errors.New("unused")}
			}
			objectService := objectrecords.NewService(reader)
			transferService := domaintransfers.NewService(domaintransfers.Dependencies{Access: access, Scopes: scopes, Events: events})
			app := fiber.New()
			app.Post("/bulk", handleInternalUploadBulkFiber(objectService, transferService))
			body := strings.NewReader(`{"requests":[{"file_id":"` + failID + `"},{"file_id":"success"}]}`)
			resp, err := app.Test(httptest.NewRequest("POST", "/bulk", body))
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			var output internalapi.InternalUploadBulkOutput
			if err := json.NewDecoder(resp.Body).Decode(&output); err != nil {
				t.Fatal(err)
			}
			if resp.StatusCode != 207 || output.Results == nil || len(*output.Results) != 2 {
				t.Fatalf("unexpected bulk response: status=%d output=%+v", resp.StatusCode, output)
			}
			failed, succeeded := (*output.Results)[0], (*output.Results)[1]
			if failed.Error == nil || *failed.Error != "storage request is invalid" || strings.Contains(*failed.Error, "QA_PRIVATE_PROVIDER_DETAIL") || failed.Status != 400 {
				t.Fatalf("unsafe failed result: %+v", failed)
			}
			if succeeded.Error != nil || succeeded.Url == nil || succeeded.Status != 200 {
				t.Fatalf("successful sibling was not retained: %+v", succeeded)
			}
		})
	}
}
