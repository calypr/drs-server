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

func TestAPIErrorBoundaryFallbacks(t *testing.T) {
	var nilError *APIError
	if nilError.Error() != "<nil>" || nilError.ErrorCode() != "" || nilError.ErrorCategory() != "" || !nilError.Is(nil) {
		t.Fatal("nil API error did not return zero-value classifications")
	}

	empty := FromResponse(nil, nil)
	if empty.Status != 0 || empty.Code != errorapi.ErrorCodeRequestFailed || empty.Message != "request failed" {
		t.Fatalf("unexpected nil response fallback: %+v", empty)
	}
	if got := (&APIError{Status: 599}).Error(); !strings.Contains(got, http.StatusText(http.StatusInternalServerError)) {
		t.Fatalf("unknown server status did not use the internal fallback: %q", got)
	}
}

func TestFromResponseDecodesNestedLegacyErrors(t *testing.T) {
	err := FromResponse(
		&http.Response{StatusCode: http.StatusBadRequest, Header: make(http.Header)},
		[]byte(`{"error":{"error_code":"invalid_input","msg":"bad input","requestId":"request-legacy"}}`),
	)
	if err.Code != errorapi.ErrorCodeInvalidInput || err.Message != "bad input" || err.RequestID != "request-legacy" {
		t.Fatalf("unexpected nested error: %+v", err)
	}

	err = FromResponse(
		&http.Response{StatusCode: http.StatusInternalServerError, Header: make(http.Header)},
		[]byte(`{"error":"backend hint"}`),
	)
	if err.Message != "backend hint" {
		t.Fatalf("nested string error was not preserved: %+v", err)
	}

	err = FromResponse(
		&http.Response{StatusCode: http.StatusTeapot, Header: make(http.Header)},
		[]byte(`{"code":123,"message":"legacy numeric code"}`),
	)
	if err.Code != errorapi.ErrorCodeRequestFailed || err.Category != errorapi.ErrorCategoryInvalidInput {
		t.Fatalf("numeric legacy code was not classified through its status: %+v", err)
	}

	err = FromResponse(
		&http.Response{StatusCode: http.StatusBadRequest, Header: make(http.Header)},
		[]byte(`{"code":404,"error_code":"object_not_found","message":"missing"}`),
	)
	if err.Code != errorapi.ErrorCodeObjectNotFound {
		t.Fatalf("exact same-object error_code did not beat numeric code: %+v", err)
	}

	err = FromResponse(
		&http.Response{StatusCode: http.StatusBadRequest, Header: make(http.Header)},
		[]byte(`{"code":404,"error":{"error_code":"object_not_found","message":"missing"}}`),
	)
	if err.Code != errorapi.ErrorCodeObjectNotFound {
		t.Fatalf("nested exact error_code did not beat outer numeric code: %+v", err)
	}
}

func TestFromResponsePreservesRawBodyAndBoundaryMetadata(t *testing.T) {
	req := &http.Request{Method: http.MethodPatch, URL: &url.URL{Scheme: "https", Host: "example.test", Path: "/objects/1"}}
	header := make(http.Header)
	header.Set("X-Request-ID", "header-request")
	header.Set("X-Trace", "trace-1")
	resp := &http.Response{StatusCode: http.StatusBadRequest, Header: header, Request: req}
	body := []byte("  not-json\n")
	err := FromResponse(resp, body)
	if err.Body != string(body) {
		t.Fatalf("raw body changed: got %q, want %q", err.Body, body)
	}
	if err.Message != "not-json" || err.Status != http.StatusBadRequest || err.Method != http.MethodPatch || err.URL != "https://example.test/objects/1" {
		t.Fatalf("unexpected boundary metadata: %+v", err)
	}
	header.Set("X-Trace", "changed")
	if err.Headers.Get("X-Trace") != "trace-1" {
		t.Fatalf("headers were not cloned: %v", err.Headers)
	}
}

func TestFromResponseNestedPrecedenceAndBoundaryConflicts(t *testing.T) {
	header := make(http.Header)
	header.Set("X-Request-ID", "header-request")
	resp := &http.Response{
		StatusCode: http.StatusBadRequest,
		Header:     header,
		Request:    &http.Request{Method: http.MethodGet, URL: &url.URL{Scheme: "https", Host: "example.test", Path: "/nested"}},
	}
	err := FromResponse(resp, []byte(`{"error":{"error":{"error_code":"object_not_found","msg":"deep message","requestId":"deep-request"}}}`))
	if err.Code != errorapi.ErrorCodeObjectNotFound || err.Message != "deep message" || err.RequestID != "header-request" {
		t.Fatalf("three-level nested payload was not decoded: %+v", err)
	}

	err = FromResponse(resp, []byte(`{"status":418,"code":"conflict","message":"outer","request_id":"body-request","error":{"code":"object_not_found","message":"inner"}}`))
	if err.Status != http.StatusBadRequest || err.Code != errorapi.ErrorCodeConflict || err.Message != "outer" || err.RequestID != "header-request" {
		t.Fatalf("payload overrode boundary precedence: %+v", err)
	}
}

func TestFromResponseScalarArrayAndNullPayloads(t *testing.T) {
	for _, body := range []string{`null`, `[]`, `42`, `"message"`, `{"error":null}`, `{"error":[]}`} {
		err := FromResponse(&http.Response{StatusCode: http.StatusBadRequest, Header: make(http.Header)}, []byte(body))
		if err == nil || err.Status != http.StatusBadRequest || err.Body != body {
			t.Fatalf("unexpected scalar payload result for %q: %+v", body, err)
		}
	}
}

func FuzzFromResponseNeverPanics(f *testing.F) {
	f.Add([]byte(`{"code":"not_found","message":"missing"}`), http.StatusNotFound)
	f.Add([]byte{0, 1, 2, '\n', 'x'}, http.StatusInternalServerError)
	f.Fuzz(func(t *testing.T, body []byte, status int) {
		if len(body) > 64<<10 {
			t.Skip()
		}
		header := make(http.Header)
		header.Set("X-Request-ID", "fuzz-request")
		resp := &http.Response{
			StatusCode: status,
			Header:     header,
			Request:    &http.Request{Method: http.MethodPost, URL: &url.URL{Scheme: "https", Host: "example.test", Path: "/fuzz"}},
		}
		err := FromResponse(resp, body)
		if err == nil || err.Status != status || err.Body != string(body) || err.RequestID != "fuzz-request" || err.Method != http.MethodPost || err.URL != "https://example.test/fuzz" {
			t.Fatalf("boundary invariant failed: %+v", err)
		}
		resp.Header.Set("X-Request-ID", "changed")
		if err.Headers.Get("X-Request-ID") != "fuzz-request" {
			t.Fatal("response headers were not cloned")
		}
	})
}

func TestMalformedNumericCodeDoesNotInventClassification(t *testing.T) {
	for _, body := range []string{`{"code":404.5}`, `{"code":1e100}`} {
		err := FromResponse(&http.Response{StatusCode: http.StatusInternalServerError}, []byte(body))
		if err.Code != errorapi.ErrorCodeInternalError || err.Category != errorapi.ErrorCategoryInternalError {
			t.Fatalf("invalid numeric code %s changed HTTP fallback: %+v", body, err)
		}
	}
	unknown := FromResponse(&http.Response{StatusCode: 500}, []byte(`{"code":"404.5"}`))
	if unknown.Code != "404.5" {
		t.Fatalf("unknown string code was not preserved: %+v", unknown)
	}
}
