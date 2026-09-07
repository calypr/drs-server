package apierror

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/calypr/syfon/apigen/errorapi"
)

func TestFromResponseSyfonEnvelope(t *testing.T) {
	req := &http.Request{Method: http.MethodPost, URL: &url.URL{Scheme: "https", Host: "example.test", Path: "/objects"}}
	headers := make(http.Header)
	headers.Set("X-Request-Id", "request-123")
	resp := &http.Response{
		StatusCode: http.StatusConflict,
		Header:     headers,
		Request:    req,
	}
	body := []byte(`{"code":"object_conflict","status":409,"message":"object already exists","request_id":"request-123"}`)
	err := FromResponse(resp, body)
	if err.Code != "object_conflict" || err.Status != http.StatusConflict || err.Message != "object already exists" {
		t.Fatalf("unexpected API error: %+v", err)
	}
	if err.RequestID != "request-123" || err.Method != http.MethodPost || err.URL != "https://example.test/objects" {
		t.Fatalf("unexpected request metadata: %+v", err)
	}
	if !errors.Is(err, errorapi.ErrConflict) {
		t.Fatalf("expected conflict sentinel, got %v", err)
	}
}

func TestFromResponseSupportsLegacyPayloads(t *testing.T) {
	tests := []struct {
		name   string
		status int
		body   string
		want   string
		sent   error
	}{
		{name: "ga4gh", status: http.StatusNotFound, body: `{"msg":"missing object","status_code":404}`, want: "missing object", sent: errorapi.ErrNotFound},
		{name: "plain text", status: http.StatusUnauthorized, body: " denied ", want: "denied", sent: errorapi.ErrUnauthorized},
		{name: "lfs", status: http.StatusServiceUnavailable, body: `{"message":"try again","request_id":"r-1"}`, want: "try again", sent: errorapi.ErrUnavailable},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := FromResponse(&http.Response{StatusCode: tt.status, Header: make(http.Header)}, []byte(tt.body))
			if err.Message != tt.want || !errors.Is(err, tt.sent) {
				t.Fatalf("unexpected error: %+v", err)
			}
		})
	}
}

func TestFromResponseUsesStatusCodeWhenPayloadHasNoCode(t *testing.T) {
	err := FromResponse(&http.Response{StatusCode: http.StatusTooManyRequests, Header: make(http.Header)}, nil)
	if err.Code != "rate_limited" || !errors.Is(err, errorapi.ErrRateLimited) {
		t.Fatalf("unexpected rate limit error: %+v", err)
	}
}

func TestUnknownNumericCodeDoesNotRecurse(t *testing.T) {
	tests := []struct {
		status   int
		code     errorapi.ErrorCode
		category errorapi.ErrorCategory
	}{
		{status: 0, code: errorapi.ErrorCodeRequestFailed, category: errorapi.ErrorCategoryInvalidInput},
		{status: http.StatusTeapot, code: errorapi.ErrorCodeRequestFailed, category: errorapi.ErrorCategoryInvalidInput},
		{status: http.StatusInternalServerError, code: errorapi.ErrorCodeInternalError, category: errorapi.ErrorCategoryInternalError},
		{status: 599, code: errorapi.ErrorCodeInternalError, category: errorapi.ErrorCategoryInternalError},
	}
	for _, test := range tests {
		err := FromResponse(&http.Response{StatusCode: test.status, Header: make(http.Header)}, nil)
		if err.Code != test.code || err.Category != test.category {
			t.Fatalf("status %d: got code %q category %q, want %q %q", test.status, err.Code, err.Category, test.code, test.category)
		}
	}
	err := FromResponse(&http.Response{StatusCode: http.StatusInternalServerError, Header: make(http.Header)}, nil)
	if err.Message != http.StatusText(http.StatusInternalServerError) {
		t.Fatalf("empty 500 response message = %q", err.Message)
	}
	if errors.Is(err, errorapi.ErrUnavailable) {
		t.Fatal("generic internal errors must not be classified as service unavailable")
	}
}

func TestWireCodeWinsOverConflictingStatus(t *testing.T) {
	tests := []struct {
		status int
		code   string
		want   error
	}{
		{status: http.StatusUnauthorized, code: "invalid_token", want: errorapi.ErrUnauthorized},
		{status: http.StatusForbidden, code: "conflict", want: errorapi.ErrConflict},
	}
	for _, test := range tests {
		err := FromResponse(
			&http.Response{StatusCode: test.status, Header: make(http.Header)},
			[]byte(`{"code":"`+test.code+`","message":"rejected"}`),
		)
		if !errors.Is(err, test.want) {
			t.Fatalf("status %d code %q: expected %v, got %v", test.status, test.code, test.want, err)
		}
	}
}

func TestExactCodeAndBroadCategoryAreIndependent(t *testing.T) {
	err := FromResponse(
		&http.Response{StatusCode: http.StatusConflict, Header: make(http.Header)},
		[]byte(`{"code":"object_checksum_immutable","category":"not_found","message":"checksum cannot change"}`),
	)
	if err.Category != errorapi.ErrorCategoryConflict {
		t.Fatalf("known code did not correct conflicting wire category: %q", err.Category)
	}
	if !errors.Is(err, errorapi.ErrObjectChecksumImmutable) {
		t.Fatalf("expected exact code sentinel, got %v", err)
	}
	if !errors.Is(err, errorapi.ErrConflict) {
		t.Fatalf("expected broad conflict sentinel, got %v", err)
	}
	if errors.Is(err, errorapi.ErrObjectSizeImmutable) {
		t.Fatal("different exact code must not match")
	}
	wrapped := fmt.Errorf("update failed: %w", err)
	if code, ok := errorapi.CodeOf(wrapped); !ok || code != errorapi.ErrorCodeObjectChecksumImmutable {
		t.Fatalf("wrapped API error code = %q, %t", code, ok)
	}
	if category, ok := errorapi.CategoryOf(wrapped); !ok || category != errorapi.ErrorCategoryConflict {
		t.Fatalf("wrapped API error category = %q, %t", category, ok)
	}
}

func TestLegacyPayloadDerivesCategoryFromKnownCodeBeforeStatus(t *testing.T) {
	err := FromResponse(
		&http.Response{StatusCode: http.StatusBadRequest, Header: make(http.Header)},
		[]byte(`{"code":"object_not_found","msg":"missing"}`),
	)
	if !errors.Is(err, errorapi.ErrObjectNotFound) || !errors.Is(err, errorapi.ErrNotFound) {
		t.Fatalf("expected exact and broad not-found classification, got %v", err)
	}
}

func TestUnknownCodeAndServerMessageArePreserved(t *testing.T) {
	err := FromResponse(
		&http.Response{StatusCode: http.StatusInternalServerError, Header: make(http.Header)},
		[]byte(`{"code":"future_failure","category":"internal_error","message":"secret"}`),
	)
	if err.Code != errorapi.ErrorCode("future_failure") || err.Body == "" {
		t.Fatalf("expected unknown code and raw body, got %+v", err)
	}
	if err.Message != "secret" {
		t.Fatalf("expected server message, got %q", err.Message)
	}
}

func TestUnknownServerStatusPreservesBodyHint(t *testing.T) {
	err := FromResponse(
		&http.Response{StatusCode: 599, Header: make(http.Header)},
		[]byte("secret backend detail"),
	)
	if err.Message != "secret backend detail" || !strings.Contains(err.Error(), "secret backend detail") {
		t.Fatalf("server detail was not preserved: %+v", err)
	}
}
