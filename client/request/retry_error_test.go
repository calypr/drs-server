package request

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/calypr/syfon/client/apierror"
)

func TestRetryExhaustionPreservesHTTPResponse(t *testing.T) {
	const body = `{"code":"internal_error","message":"server detail","request_id":"retry-request"}`
	attempts := 0
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		attempts++
		return &http.Response{StatusCode: 500, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
	})}
	r := NewBasicAuthRequestor(nil, nil, nil, "https://example.test", "", client).(*Request)
	r.RetryClient.RetryMax = 1
	r.RetryClient.RetryWaitMin = 0
	r.RetryClient.RetryWaitMax = 0
	err := r.Do(context.Background(), http.MethodGet, "/failure", nil, nil)
	var apiErr *apierror.APIError
	if attempts != 2 || !errors.As(err, &apiErr) {
		t.Fatalf("attempts=%d error=%T %v", attempts, err, err)
	}
	if apiErr.Status != 500 || apiErr.Body != body || apiErr.RequestID != "retry-request" || apiErr.Message != "server detail" {
		t.Fatalf("lost final response: %+v", apiErr)
	}
}

func TestRetryExhaustionPreservesTransportCause(t *testing.T) {
	for _, cause := range []error{io.ErrUnexpectedEOF, context.Canceled, context.DeadlineExceeded} {
		t.Run(cause.Error(), func(t *testing.T) {
			client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) { return nil, cause })}
			r := NewBasicAuthRequestor(nil, nil, nil, "https://example.test", "", client).(*Request)
			r.RetryClient.RetryMax = 1
			r.RetryClient.RetryWaitMin = 0
			r.RetryClient.RetryWaitMax = 0
			err := r.Do(context.Background(), http.MethodGet, "/failure", nil, nil)
			var apiErr *apierror.APIError
			if !errors.Is(err, cause) || errors.As(err, &apiErr) {
				t.Fatalf("transport cause changed: %T %v", err, err)
			}
		})
	}
}
