package services

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"testing"

	"github.com/calypr/syfon/client/apierror"
)

func TestAPIResponseErrorReturnsTypedContract(t *testing.T) {
	req := &http.Request{Method: http.MethodGet, URL: &url.URL{Scheme: "https", Host: "example.test", Path: "/objects/missing"}}
	resp := &http.Response{
		StatusCode: http.StatusNotFound,
		Header:     http.Header{"X-Request-Id": []string{"request-123"}},
		Request:    req,
	}
	err := apiResponseError(resp, []byte(`{"code":"not_found","status":404,"message":"Resource not found","request_id":"request-123"}`))
	var apiErr *apierror.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected *apierror.APIError, got %T", err)
	}
	if !errors.Is(err, apierror.ErrNotFound) {
		t.Fatalf("expected not-found sentinel, got %v", err)
	}
	if apiErr.Code != "not_found" || apiErr.Status != http.StatusNotFound || apiErr.Message != "Resource not found" || apiErr.RequestID != "request-123" {
		t.Fatalf("unexpected API error: %+v", apiErr)
	}
}

func TestDRSListVariantsPreserveEmptyArray(t *testing.T) {
	service := NewDRSService(nil, NewIndexService(nil, &fakeRequester{}))
	ctx := context.Background()
	for _, list := range []func() (DRSPage, error){
		func() (DRSPage, error) { return service.ListObjects(ctx, 10, 1) },
		func() (DRSPage, error) { return service.ListObjectsAfter(ctx, 10, "after") },
		func() (DRSPage, error) { return service.ListObjectsByProject(ctx, "project", 10, 1) },
		func() (DRSPage, error) { return service.ListObjectsByProjectAfter(ctx, "project", 10, "after") },
	} {
		page, err := list()
		if err != nil {
			t.Fatal(err)
		}
		data, err := json.Marshal(page)
		if err != nil {
			t.Fatal(err)
		}
		if string(data) != `{"drs_objects":[]}` {
			t.Fatalf("empty list JSON changed: %s", data)
		}
	}
}
