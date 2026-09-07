package apierror

import (
	"errors"
	"net/http"
	"net/url"
	"strings"
	"testing"
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
	if !errors.Is(err, ErrConflict) {
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
		{name: "ga4gh", status: http.StatusNotFound, body: `{"msg":"missing object","status_code":404}`, want: "missing object", sent: ErrNotFound},
		{name: "plain text", status: http.StatusUnauthorized, body: " denied ", want: "denied", sent: ErrUnauthorized},
		{name: "lfs", status: http.StatusServiceUnavailable, body: `{"message":"try again","request_id":"r-1"}`, want: http.StatusText(http.StatusServiceUnavailable), sent: ErrUnavailable},
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
	if err.Code != "rate_limited" || !errors.Is(err, ErrRateLimited) {
		t.Fatalf("unexpected rate limit error: %+v", err)
	}
}

func TestUnknownNumericCodeDoesNotRecurse(t *testing.T) {
	err := FromResponse(&http.Response{StatusCode: http.StatusInternalServerError, Header: make(http.Header)}, nil)
	if err.Code != CodeInternal {
		t.Fatalf("unexpected internal error code: %q", err.Code)
	}
	if errors.Is(err, ErrUnavailable) {
		t.Fatal("generic internal errors must not be classified as service unavailable")
	}
}

func TestWireCodeWinsOverConflictingStatus(t *testing.T) {
	tests := []struct {
		status int
		code   string
		want   error
	}{
		{status: http.StatusUnauthorized, code: "invalid_token", want: ErrUnauthorized},
		{status: http.StatusForbidden, code: "conflict", want: ErrConflict},
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
		[]byte(`{"code":"object_checksum_immutable","category":"conflict","message":"checksum cannot change"}`),
	)
	if !errors.Is(err, ErrObjectChecksumImmutable) {
		t.Fatalf("expected exact code sentinel, got %v", err)
	}
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("expected broad conflict sentinel, got %v", err)
	}
	if errors.Is(err, ErrObjectSizeImmutable) {
		t.Fatal("different exact code must not match")
	}
}

func TestLegacyPayloadDerivesCategoryFromKnownCodeBeforeStatus(t *testing.T) {
	err := FromResponse(
		&http.Response{StatusCode: http.StatusBadRequest, Header: make(http.Header)},
		[]byte(`{"code":"object_not_found","msg":"missing"}`),
	)
	if !errors.Is(err, ErrObjectNotFound) || !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected exact and broad not-found classification, got %v", err)
	}
}

func TestUnknownCodeIsPreservedAndServerMessagesAreScrubbed(t *testing.T) {
	err := FromResponse(
		&http.Response{StatusCode: http.StatusInternalServerError, Header: make(http.Header)},
		[]byte(`{"code":"future_failure","category":"internal_error","message":"secret"}`),
	)
	if err.Code != Code("future_failure") || err.Body == "" {
		t.Fatalf("expected unknown code and raw body, got %+v", err)
	}
	if err.Message != http.StatusText(http.StatusInternalServerError) {
		t.Fatalf("expected scrubbed message, got %q", err.Message)
	}
}

func TestUnknownServerStatusDoesNotExposeBody(t *testing.T) {
	err := FromResponse(
		&http.Response{StatusCode: 599, Header: make(http.Header)},
		[]byte("secret backend detail"),
	)
	if err.Message != http.StatusText(http.StatusInternalServerError) || strings.Contains(err.Error(), "secret backend detail") {
		t.Fatalf("server detail leaked: %+v", err)
	}
}
