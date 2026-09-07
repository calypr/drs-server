package request

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	conf "github.com/calypr/syfon/client/config"
)

type trackingManager struct {
	mockConfigManager
	saved *conf.Credential
}

func (m *trackingManager) Save(cred *conf.Credential) error {
	m.saved = cred
	return nil
}

func TestAuthTransportRoundTrip(t *testing.T) {
	t.Parallel()

	t.Run("skip auth header bypasses auth", func(t *testing.T) {
		base := roundTripFunc(func(req *http.Request) (*http.Response, error) {
			if req.Header.Get("X-Skip-Auth") != "" {
				t.Fatal("X-Skip-Auth should be removed before sending")
			}
			if req.Header.Get("Authorization") != "" {
				t.Fatal("authorization should not be injected in skip-auth mode")
			}
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader("ok")), Header: make(http.Header), Request: req}, nil
		})
		transport := &AuthTransport{Base: base, Cred: &conf.Credential{AccessToken: "token"}}
		req, _ := http.NewRequest(http.MethodGet, "https://example.test", nil)
		req.Header.Set("X-Skip-Auth", "true")
		if _, err := transport.RoundTrip(req); err != nil {
			t.Fatalf("RoundTrip returned error: %v", err)
		}
	})

	t.Run("injects basic auth when absent", func(t *testing.T) {
		base := roundTripFunc(func(req *http.Request) (*http.Response, error) {
			if got := req.Header.Get("Authorization"); got != "Basic dXNlcjpwYXNz" {
				t.Fatalf("expected basic auth, got %q", got)
			}
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader("ok")), Header: make(http.Header), Request: req}, nil
		})
		transport := &AuthTransport{Base: base, Mode: AuthModeBasic, Cred: &conf.Credential{KeyID: "user", APIKey: "pass"}}
		req, _ := http.NewRequest(http.MethodGet, "https://example.test", nil)
		if _, err := transport.RoundTrip(req); err != nil {
			t.Fatalf("RoundTrip returned error: %v", err)
		}
	})

	t.Run("injects bearer token when absent", func(t *testing.T) {
		base := roundTripFunc(func(req *http.Request) (*http.Response, error) {
			if got := req.Header.Get("Authorization"); got != "Bearer tok" {
				t.Fatalf("expected bearer token, got %q", got)
			}
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader("ok")), Header: make(http.Header), Request: req}, nil
		})
		transport := &AuthTransport{Base: base, Mode: AuthModeBearer, Cred: &conf.Credential{AccessToken: "tok"}}
		req, _ := http.NewRequest(http.MethodGet, "https://example.test", nil)
		if _, err := transport.RoundTrip(req); err != nil {
			t.Fatalf("RoundTrip returned error: %v", err)
		}
	})

	t.Run("preserves caller authorization", func(t *testing.T) {
		base := roundTripFunc(func(req *http.Request) (*http.Response, error) {
			if got := req.Header.Get("Authorization"); got != "Basic abc" {
				t.Fatalf("expected existing authorization to be preserved, got %q", got)
			}
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader("ok")), Header: make(http.Header), Request: req}, nil
		})
		transport := &AuthTransport{Base: base, Mode: AuthModeBearer, Cred: &conf.Credential{AccessToken: "tok"}}
		req, _ := http.NewRequest(http.MethodGet, "https://example.test", nil)
		req.Header.Set("Authorization", "Basic abc")
		if _, err := transport.RoundTrip(req); err != nil {
			t.Fatalf("RoundTrip returned error: %v", err)
		}
	})
}

func TestAuthTransportRefreshOnce(t *testing.T) {
	t.Parallel()

	transport := &AuthTransport{}
	if err := transport.refreshOnce(context.Background()); err != nil {
		t.Fatalf("refreshOnce with nil cred returned error: %v", err)
	}

	transport = &AuthTransport{Mode: AuthModeBearer, Cred: &conf.Credential{AccessToken: "already-present", APIEndpoint: "https://example.test"}}
	if err := transport.refreshOnce(context.Background()); err != nil {
		t.Fatalf("refreshOnce with existing token returned error: %v", err)
	}

	transport = &AuthTransport{Mode: AuthModeBasic, Cred: &conf.Credential{APIKey: "basic-pass"}}
	if err := transport.refreshOnce(context.Background()); err != nil {
		t.Fatalf("refreshOnce without APIEndpoint should no-op, got error: %v", err)
	}
}

func TestAuthTransportNewAccessToken(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	t.Run("requires api key", func(t *testing.T) {
		transport := &AuthTransport{Mode: AuthModeBearer, Cred: &conf.Credential{APIEndpoint: "https://example.test"}}
		if err := transport.NewAccessToken(ctx); err == nil || !strings.Contains(err.Error(), "APIKey is required") {
			t.Fatalf("expected APIKey required error, got %v", err)
		}
	})

	t.Run("requires api endpoint", func(t *testing.T) {
		transport := &AuthTransport{Mode: AuthModeBearer, Cred: &conf.Credential{APIKey: "key"}}
		if err := transport.NewAccessToken(ctx); err == nil || !strings.Contains(err.Error(), "APIEndpoint is required") {
			t.Fatalf("expected APIEndpoint required error, got %v", err)
		}
	})

	t.Run("non-200 includes response body", func(t *testing.T) {
		transport := &AuthTransport{
			Mode: AuthModeBearer,
			Base: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: http.StatusUnauthorized, Status: "401 Unauthorized", Body: io.NopCloser(strings.NewReader("nope")), Header: make(http.Header), Request: req}, nil
			}),
			Cred: &conf.Credential{APIKey: "key", APIEndpoint: "https://example.test"},
		}
		if err := transport.NewAccessToken(ctx); err == nil || !strings.Contains(err.Error(), "401 Unauthorized") || !strings.Contains(err.Error(), "body=nope") {
			t.Fatalf("expected detailed non-200 error, got %v", err)
		}
	})

	t.Run("decode failure bubbles up", func(t *testing.T) {
		transport := &AuthTransport{
			Mode: AuthModeBearer,
			Base: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: http.StatusOK, Status: "200 OK", Body: io.NopCloser(strings.NewReader("{")), Header: make(http.Header), Request: req}, nil
			}),
			Cred: &conf.Credential{APIKey: "key", APIEndpoint: "https://example.test"},
		}
		if err := transport.NewAccessToken(ctx); err == nil {
			t.Fatal("expected JSON decode error")
		}
	})

	t.Run("success stores refreshed token and persists", func(t *testing.T) {
		mgr := &trackingManager{}
		transport := &AuthTransport{
			Mode: AuthModeBearer,
			Base: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				if req.Method != http.MethodPost {
					t.Fatalf("expected POST refresh request, got %s", req.Method)
				}
				if ct := req.Header.Get("Content-Type"); ct != "application/json" {
					t.Fatalf("expected JSON content type, got %q", ct)
				}
				return &http.Response{StatusCode: http.StatusOK, Status: "200 OK", Body: io.NopCloser(strings.NewReader(`{"access_token":"new-token"}`)), Header: make(http.Header), Request: req}, nil
			}),
			Manager: mgr,
			Cred:    &conf.Credential{APIKey: "key", APIEndpoint: "https://example.test", AccessToken: "old-token"},
		}

		if err := transport.NewAccessToken(ctx); err != nil {
			t.Fatalf("NewAccessToken returned error: %v", err)
		}
		if transport.Cred.AccessToken != "new-token" {
			t.Fatalf("expected refreshed token, got %q", transport.Cred.AccessToken)
		}
		if mgr.saved == nil || mgr.saved.AccessToken != "new-token" {
			t.Fatalf("expected credential to be saved, got %+v", mgr.saved)
		}
	})
}
