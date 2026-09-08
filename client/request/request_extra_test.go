package request

import (
	"context"
	"encoding/base64"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/calypr/syfon/apigen/errorapi"
	"github.com/calypr/syfon/client/apierror"
	conf "github.com/calypr/syfon/client/config"
	"github.com/calypr/syfon/client/logs"
)

func TestMain(m *testing.M) {
	oldMin := defaultRetryWaitMin
	oldMax := defaultRetryWaitMax
	defaultRetryWaitMin = 0
	defaultRetryWaitMax = 0
	code := m.Run()
	defaultRetryWaitMin = oldMin
	defaultRetryWaitMax = oldMax
	os.Exit(code)
}

func TestAPIErrorErrorString(t *testing.T) {
	t.Parallel()

	err := (&apierror.APIError{Method: http.MethodGet, URL: "https://example.test/data", Status: http.StatusForbidden, Body: "denied"}).Error()
	if err != "GET https://example.test/data: status 403 body=denied" {
		t.Fatalf("unexpected error string: %q", err)
	}
}

func TestRequestDo_ResponseAndDecodeErrors(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	baseClient := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		switch r.URL.Path {
		case "/forbidden":
			return &http.Response{StatusCode: http.StatusForbidden, Status: "403 Forbidden", Body: io.NopCloser(strings.NewReader(" denied ")), Header: make(http.Header), Request: r}, nil
		case "/badjson":
			return &http.Response{StatusCode: http.StatusOK, Status: "200 OK", Body: io.NopCloser(strings.NewReader("{")), Header: make(http.Header), Request: r}, nil
		default:
			return &http.Response{StatusCode: http.StatusOK, Status: "200 OK", Body: io.NopCloser(strings.NewReader("{}")), Header: make(http.Header), Request: r}, nil
		}
	})}
	req := NewBasicAuthRequestor(logs.NewGen3Logger(logger, "", ""), nil, &mockConfigManager{}, "https://example.test", "ua", baseClient)

	var out map[string]any
	err := req.Do(context.Background(), http.MethodGet, "/forbidden", nil, &out)
	if err == nil {
		t.Fatal("expected APIError")
	}
	respErr, ok := err.(*apierror.APIError)
	if !ok {
		t.Fatalf("expected *apierror.APIError, got %T", err)
	}
	if respErr.Status != http.StatusForbidden || respErr.Code != "forbidden" || respErr.Method != http.MethodGet || respErr.Body != " denied " || respErr.Message != "denied" {
		t.Fatalf("unexpected response error details: %+v", respErr)
	}
	if !errors.Is(respErr, errorapi.ErrForbidden) {
		t.Fatalf("expected forbidden sentinel, got %v", respErr)
	}

	err = req.Do(context.Background(), http.MethodGet, "/badjson", nil, &out)
	if err == nil || !strings.Contains(err.Error(), "decode response") {
		t.Fatalf("expected decode response error, got %v", err)
	}
}

func TestRequestDo_BasicAuthAndPartSize(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	cred := &conf.Credential{KeyID: "kid", APIKey: "secret"}
	baseClient := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		expectedBasic := "Basic " + base64.StdEncoding.EncodeToString([]byte("kid:secret"))
		if got := r.Header.Get("Authorization"); got != expectedBasic {
			t.Fatalf("expected basic auth header %q, got %q", expectedBasic, got)
		}
		if r.ContentLength != 5 {
			t.Fatalf("expected content length 5, got %d", r.ContentLength)
		}
		if ua := r.Header.Get("User-Agent"); ua != "ua" {
			t.Fatalf("expected user-agent header, got %q", ua)
		}
		return &http.Response{StatusCode: http.StatusOK, Status: "200 OK", Body: io.NopCloser(strings.NewReader("{}")), Header: make(http.Header), Request: r}, nil
	})}
	req := NewBasicAuthRequestor(logs.NewGen3Logger(logger, "", ""), cred, &mockConfigManager{}, "https://example.test", "ua", baseClient)

	payload := strings.NewReader("hello")
	if err := req.Do(context.Background(), http.MethodPut, "/upload", payload, nil, WithPartSize(5)); err != nil {
		t.Fatalf("Do returned error: %v", err)
	}
}

type countingReader struct {
	data  string
	read  bool
	calls int
}

func (r *countingReader) Read(p []byte) (int, error) {
	r.calls++
	if r.read {
		return 0, io.EOF
	}
	r.read = true
	return copy(p, r.data), io.EOF
}

func TestRequestDo_WithNoRetryDoesNotPrebufferBody(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	baseClient := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Status:     "200 OK",
			Body:       io.NopCloser(strings.NewReader("{}")),
			Header:     make(http.Header),
			Request:    r,
		}, nil
	})}
	req := NewBasicAuthRequestor(logs.NewGen3Logger(logger, "", ""), nil, &mockConfigManager{}, "https://example.test", "ua", baseClient)

	reader := &countingReader{data: "payload"}
	if err := req.Do(context.Background(), http.MethodPut, "/upload", reader, nil, WithPartSize(7), WithNoRetry(true)); err != nil {
		t.Fatalf("Do returned error: %v", err)
	}
	if reader.calls != 0 {
		t.Fatalf("expected no prebuffer read before transport, got %d reads", reader.calls)
	}
}

func TestRequestDo_DefaultPathDoesNotPrebufferGenericReader(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	transportErr := errors.New("stop after request construction")
	baseClient := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return nil, transportErr
	})}
	req := NewBasicAuthRequestor(logs.NewGen3Logger(logger, "", ""), nil, &mockConfigManager{}, "https://example.test", "ua", baseClient)

	reader := &countingReader{data: "payload"}
	err := req.Do(context.Background(), http.MethodPut, "/upload", reader, nil, WithPartSize(7))
	if err == nil {
		t.Fatal("expected request failure")
	}
	if !errors.Is(err, transportErr) {
		t.Fatalf("transport error was not preserved: %v", err)
	}
	if reader.calls != 0 {
		t.Fatalf("expected generic reader to remain unconsumed when retries are unsafe, got %d reads", reader.calls)
	}
}
