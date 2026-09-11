package services

import (
	"errors"
	"net/http"
	"net/url"
	"testing"

	"github.com/calypr/syfon/apigen/errorapi"
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
	if !errors.Is(err, errorapi.ErrNotFound) {
		t.Fatalf("expected not-found sentinel, got %v", err)
	}
	if apiErr.Code != "not_found" || apiErr.Status != http.StatusNotFound || apiErr.Message != "Resource not found" || apiErr.RequestID != "request-123" {
		t.Fatalf("unexpected API error: %+v", apiErr)
	}
}
