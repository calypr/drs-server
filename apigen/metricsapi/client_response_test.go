package metricsapi

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestParseResponseKeepsNon2xxWrapperOnJSONDecodeError(t *testing.T) {
	rawBody := []byte(`{"message":`)
	rsp := &http.Response{
		StatusCode: http.StatusBadRequest,
		Status:     "400 Bad Request",
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(bytes.NewReader(rawBody)),
	}

	response, err := ParseListMetricsFilesResponse(rsp)
	if err != nil {
		t.Fatalf("unexpected malformed non-2xx JSON error: %v", err)
	}
	if response == nil {
		t.Fatal("expected response wrapper with malformed non-2xx JSON")
	}
	if response.HTTPResponse != rsp {
		t.Fatal("response wrapper lost HTTP response")
	}
	if !bytes.Equal(response.Body, rawBody) {
		t.Fatalf("response body = %q, want %q", response.Body, rawBody)
	}
}

type failingResponseBody struct {
	err    error
	closed bool
}

func (b *failingResponseBody) Read([]byte) (int, error) { return 0, b.err }
func (b *failingResponseBody) Close() error {
	b.closed = true
	return nil
}

func TestParseResponsePreservesBodyReadFailure(t *testing.T) {
	cause := errors.New("response read failed")
	body := &failingResponseBody{err: cause}
	response, err := ParseListMetricsFilesResponse(&http.Response{
		StatusCode: http.StatusBadRequest,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       body,
	})
	if response != nil || !errors.Is(err, cause) || !body.closed {
		t.Fatalf("response=%v error=%v closed=%t; want original read error and closed body", response, err, body.closed)
	}
}

func TestParseResponseRejectsMalformedTypedSuccess(t *testing.T) {
	rsp := &http.Response{
		StatusCode: http.StatusOK,
		Status:     "200 OK",
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(`{"data":`)),
	}

	response, err := ParseListMetricsFilesResponse(rsp)
	if err == nil {
		t.Fatal("expected malformed JSON error")
	}
	if response != nil {
		t.Fatalf("response = %#v, want nil for malformed typed success", response)
	}
}
