package request

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	conf "github.com/calypr/syfon/client/config"
	"github.com/calypr/syfon/client/logs"
)

func TestNewRequestor(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	cred := &conf.Credential{
		KeyID:       "test-key",
		APIKey:      "test-secret",
		APIEndpoint: "https://example.com",
	}

	// Create a mock config manager
	mockConf := &mockConfigManager{}

	reqInterface := NewBasicAuthRequestor(logs.NewGen3Logger(logger, "", ""), cred, mockConf, "https://example.com", "test-ua", nil)

	if reqInterface == nil {
		t.Fatal("Expected non-nil request interface")
	}

	req, ok := reqInterface.(*Request)
	if !ok {
		t.Fatal("Expected request interface to be of type *Request")
	}

	if req.BaseURL != "https://example.com" {
		t.Errorf("Expected BaseURL 'https://example.com', got '%s'", req.BaseURL)
	}

	if req.UserAgent != "test-ua" {
		t.Errorf("Expected UserAgent 'test-ua', got '%s'", req.UserAgent)
	}
}

func TestRequest_NewBuilder(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	mockConf := &mockConfigManager{}

	reqInterface := NewBasicAuthRequestor(logs.NewGen3Logger(logger, "", ""), nil, mockConf, "https://example.com", "test-ua", nil)
	req := reqInterface.(*Request)

	// Test relative path
	builder := req.newBuilder("GET", "/api/test")
	if builder.Url != "https://example.com/api/test" {
		t.Errorf("Expected URL 'https://example.com/api/test', got '%s'", builder.Url)
	}

	// Test absolute URL
	builder = req.newBuilder("GET", "https://other.com/api/test")
	if builder.Url != "https://other.com/api/test" {
		t.Errorf("Expected URL 'https://other.com/api/test', got '%s'", builder.Url)
	}
}

func TestRequest_Do_Success(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	mockConf := &mockConfigManager{}

	client := &http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			if r.Method != "GET" {
				t.Errorf("Expected GET method, got %s", r.Method)
			}
			if r.Header.Get("User-Agent") != "test-ua" {
				t.Errorf("Expected User-Agent 'test-ua', got '%s'", r.Header.Get("User-Agent"))
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Status:     "200 OK",
				Body:       io.NopCloser(strings.NewReader(`{"status":"success"}`)),
				Header:     make(http.Header),
				Request:    r,
			}, nil
		}),
	}

	reqInterface := NewBasicAuthRequestor(logs.NewGen3Logger(logger, "", ""), nil, mockConf, "https://example.com", "test-ua", client)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var out struct {
		Status string `json:"status"`
	}
	err := reqInterface.Do(ctx, "GET", "/api/test", nil, &out, WithSkipAuth(true))

	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	if out.Status != "success" {
		t.Errorf("Expected status 'success', got %s", out.Status)
	}
}

func TestRequest_Do_WithCustomHeaders(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	mockConf := &mockConfigManager{}

	client := &http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			customHeader := r.Header.Get("X-Custom-Header")
			if customHeader != "test-value" {
				t.Errorf("Expected X-Custom-Header 'test-value', got '%s'", customHeader)
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Status:     "200 OK",
				Body:       io.NopCloser(strings.NewReader("")),
				Header:     make(http.Header),
				Request:    r,
			}, nil
		}),
	}

	reqInterface := NewBasicAuthRequestor(logs.NewGen3Logger(logger, "", ""), nil, mockConf, "https://example.com", "test-ua", client)

	ctx := context.Background()
	err := reqInterface.Do(ctx, "GET", "/api/test", nil, nil, WithHeader("X-Custom-Header", "test-value"))

	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}
}

func TestRequest_Do_RawMode(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	client := &http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Status:     "200 OK",
				Body:       io.NopCloser(strings.NewReader("raw content")),
				Header:     make(http.Header),
				Request:    r,
			}, nil
		}),
	}
	reqInterface := NewBasicAuthRequestor(logs.NewGen3Logger(logger, "", ""), nil, nil, "https://example.com", "test-ua", client)

	var resp *http.Response
	err := reqInterface.Do(context.Background(), "GET", "/raw", nil, &resp)
	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}
	if resp == nil {
		t.Fatal("Expected non-nil response")
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if string(body) != "raw content" {
		t.Errorf("Expected 'raw content', got '%s'", string(body))
	}
}

func TestRequest_Do_BasicAuthDoesNotRefreshOn401(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	mockConf := &mockConfigManager{}
	calls := 0

	client := &http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			calls++
			if r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/user/credentials/api/access_token") {
				t.Fatalf("unexpected refresh request in local basic-auth mode: %s %s", r.Method, r.URL.String())
			}
			return &http.Response{
				StatusCode: http.StatusUnauthorized,
				Status:     "401 Unauthorized",
				Body:       io.NopCloser(strings.NewReader("nope")),
				Header:     make(http.Header),
				Request:    r,
			}, nil
		}),
	}

	reqInterface := NewBasicAuthRequestor(
		logs.NewGen3Logger(logger, "", ""),
		&conf.Credential{KeyID: "user", APIKey: "pass"},
		mockConf,
		"https://example.com",
		"test-ua",
		client,
	)

	err := reqInterface.Do(context.Background(), "GET", "/api/test", nil, nil)
	if err == nil || !strings.Contains(err.Error(), "status 401") {
		t.Fatalf("expected 401 response error, got %v", err)
	}
	if calls != 1 {
		t.Fatalf("expected one request, got %d", calls)
	}
}

func TestRequest_Do_SkipAuthSuppressesTransportAuth(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	mockConf := &mockConfigManager{}

	client := &http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			if got := r.Header.Get("Authorization"); got != "" {
				t.Fatalf("expected no authorization header, got %q", got)
			}
			if got := r.Header.Get("X-Skip-Auth"); got != "" {
				t.Fatalf("expected X-Skip-Auth to be stripped before send, got %q", got)
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Status:     "200 OK",
				Body:       io.NopCloser(strings.NewReader("")),
				Header:     make(http.Header),
				Request:    r,
			}, nil
		}),
	}

	reqInterface := NewBasicAuthRequestor(
		logs.NewGen3Logger(logger, "", ""),
		&conf.Credential{KeyID: "user", APIKey: "pass"},
		mockConf,
		"https://example.com",
		"test-ua",
		client,
	)

	err := reqInterface.Do(context.Background(), "PUT", "https://signed.example/upload?X-Amz-Signature=sig", strings.NewReader("payload"), nil, WithSkipAuth(true))
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
}

// Mock config manager for testing
type mockConfigManager struct{}

func (m *mockConfigManager) Import(filePath, fenceToken string) (*conf.Credential, error) {
	return &conf.Credential{}, nil
}

func (m *mockConfigManager) Load(profile string) (*conf.Credential, error) {
	return &conf.Credential{}, nil
}

func (m *mockConfigManager) Save(cred *conf.Credential) error {
	return nil
}

func (m *mockConfigManager) EnsureExists() error {
	return nil
}

func (m *mockConfigManager) IsCredentialValid(cred *conf.Credential) (bool, error) {
	return true, nil
}

func (m *mockConfigManager) IsTokenValid(token string) (bool, error) {
	return true, nil
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}
