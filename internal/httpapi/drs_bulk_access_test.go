package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	generated "github.com/calypr/syfon/apigen/drs"
	"github.com/calypr/syfon/apigen/errorapi"
	"github.com/calypr/syfon/internal/storage"
	"github.com/calypr/syfon/internal/transfers"
	"github.com/calypr/syfon/internal/usage"
	"github.com/gofiber/fiber/v3"
)

type bulkAccessObjectPort struct {
	records map[string]*generated.DrsObject
	errors  map[string]error
}

func (p bulkAccessObjectPort) GetObject(_ context.Context, objectID, _ string) (*generated.DrsObject, error) {
	if err := p.errors[objectID]; err != nil {
		return nil, err
	}
	object := p.records[objectID]
	if object == nil {
		return nil, errorapi.ErrObjectNotFound
	}
	return object, nil
}

func (bulkAccessObjectPort) GetObjectsByChecksums(context.Context, []string, string) (map[string][]generated.DrsObject, error) {
	return nil, nil
}

type bulkAccessStorage struct{}

func (bulkAccessStorage) Sign(_ context.Context, request storage.SignRequest) (storage.SignedAccess, error) {
	if strings.Contains(request.Target.OriginalURL, "/signer") || strings.Contains(request.Target.CanonicalURL, "/signer") {
		return storage.SignedAccess{}, errorapi.ErrStorageUnavailable
	}
	return storage.SignedAccess{Location: "https://signed.invalid/" + request.Target.Key}, nil
}

func (bulkAccessStorage) BeginMultipart(context.Context, storage.BeginMultipartRequest) (storage.UploadID, error) {
	return "", nil
}

func (bulkAccessStorage) SignMultipartPart(context.Context, storage.MultipartPartRequest) (storage.SignedAccess, error) {
	return storage.SignedAccess{}, nil
}

func (bulkAccessStorage) CompleteMultipart(context.Context, storage.CompleteMultipartRequest) error {
	return nil
}

type bulkAccessEvents struct{}

func (bulkAccessEvents) RecordTransferAttributionEvents(_ context.Context, events []usage.Event) error {
	if len(events) > 0 && events[0].ObjectID == "event" {
		return errors.New("event recorder unavailable")
	}
	return nil
}

func TestBulkAccessResponsePreservesFailureCategories(t *testing.T) {
	accessID := "access"
	record := func(id, path string, matches bool) *generated.DrsObject {
		methodID := accessID
		if !matches {
			methodID = "other"
		}
		methods := []generated.AccessMethod{{AccessId: &methodID, Type: "s3", AccessUrl: &generated.AccessURL{Url: "s3://bucket/" + path}}}
		return &generated.DrsObject{Id: id, AccessMethods: &methods}
	}
	objectPort := bulkAccessObjectPort{
		records: map[string]*generated.DrsObject{
			"ok":     record("ok", "ok", true),
			"signer": record("signer", "signer", true),
			"event":  record("event", "event", true),
			"no-url": record("no-url", "no-url", false),
		},
		errors: map[string]error{"denied": errorapi.ErrAccessDenied},
	}
	accessService := transfers.NewService(transfers.Dependencies{
		Objects: objectPort,
		Storage: bulkAccessStorage{},
		Events:  bulkAccessEvents{},
	})
	app := fiber.New()
	registerDRSRoutes(app, nil, accessService, generated.N200ServiceInfo{})

	requestBody := []byte(`{"bulk_object_access_ids":[` +
		`{"bulk_object_id":"ok","bulk_access_ids":["access"]},` +
		`{"bulk_object_id":"missing","bulk_access_ids":["access"]},` +
		`{"bulk_object_id":"denied","bulk_access_ids":["access"]},` +
		`{"bulk_object_id":"signer","bulk_access_ids":["access"]},` +
		`{"bulk_object_id":"event","bulk_access_ids":["access"]},` +
		`{"bulk_object_id":"no-url","bulk_access_ids":["access"]}]}`)
	response, err := app.Test(httptest.NewRequest(http.MethodPost, "/objects/access", bytes.NewReader(requestBody)))
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", response.StatusCode)
	}
	defer response.Body.Close()
	var payload struct {
		Resolved []generated.BulkAccessURL `json:"resolved_drs_object_access_urls"`
		Summary  struct {
			Requested  int `json:"requested"`
			Resolved   int `json:"resolved"`
			Unresolved int `json:"unresolved"`
		} `json:"summary"`
		Unresolved []struct {
			ErrorCode int      `json:"error_code"`
			ObjectIDs []string `json:"object_ids"`
		} `json:"unresolved_drs_objects"`
	}
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		t.Fatal(err)
	}
	if payload.Summary.Requested != 6 || payload.Summary.Resolved != 1 || payload.Summary.Unresolved != 5 || len(payload.Resolved) != 1 {
		t.Fatalf("unexpected bulk access result: %+v", payload)
	}
	wantFailures := map[int][]string{
		http.StatusNotFound:            {"missing", "no-url"},
		http.StatusForbidden:           {"denied"},
		http.StatusServiceUnavailable:  {"signer"},
		http.StatusInternalServerError: {"event"},
	}
	gotFailures := make(map[int][]string, len(payload.Unresolved))
	for _, failure := range payload.Unresolved {
		gotFailures[failure.ErrorCode] = failure.ObjectIDs
	}
	if !reflect.DeepEqual(gotFailures, wantFailures) {
		t.Fatalf("failure groups = %v, want %v", gotFailures, wantFailures)
	}
	gotOrder := make([]int, 0, len(payload.Unresolved))
	for _, failure := range payload.Unresolved {
		gotOrder = append(gotOrder, failure.ErrorCode)
	}
	wantOrder := []int{http.StatusNotFound, http.StatusForbidden, http.StatusServiceUnavailable, http.StatusInternalServerError}
	if !reflect.DeepEqual(gotOrder, wantOrder) {
		t.Fatalf("failure status order = %v, want %v", gotOrder, wantOrder)
	}
}

func TestBulkAccessRejectsConfiguredLimitBeforeExpansion(t *testing.T) {
	accessService := transfers.NewService(transfers.Dependencies{
		Objects: bulkAccessObjectPort{records: map[string]*generated.DrsObject{}, errors: map[string]error{}},
		Storage: bulkAccessStorage{},
	})
	app := fiber.New()
	registerDRSRoutes(app, nil, accessService, generated.N200ServiceInfo{}, 1)
	requestBody := []byte(`{"bulk_object_access_ids":[` +
		`{"bulk_object_id":"object-1","bulk_access_ids":["a","b"]},` +
		`{"bulk_object_id":"object-2","bulk_access_ids":["c"]}]}`)
	response, err := app.Test(httptest.NewRequest(http.MethodPost, "/objects/access", bytes.NewReader(requestBody)))
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want %d", response.StatusCode, http.StatusRequestEntityTooLarge)
	}
}
