package request

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"

	conf "github.com/calypr/syfon/client/config"
)

type trackingManager struct {
	mockConfigManager
	saved   *conf.Credential
	saveErr error
}

func (m *trackingManager) Save(cred *conf.Credential) error {
	m.saved = cred
	return m.saveErr
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
		if req.Header.Get("X-Skip-Auth") != "true" {
			t.Fatal("RoundTrip must preserve skip marker on the caller request")
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

func TestRequestDoRefreshesRejectedBearerOnceForConcurrent401s(t *testing.T) {
	t.Parallel()

	var mu sync.Mutex
	refreshCalls := 0
	dataCalls := 0
	base := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		mu.Lock()
		defer mu.Unlock()
		if req.URL.Path == "/user/credentials/api/access_token" {
			refreshCalls++
			return &http.Response{StatusCode: http.StatusOK, Status: "200 OK", Body: io.NopCloser(strings.NewReader(`{"access_token":"new-token"}`)), Header: make(http.Header), Request: req}, nil
		}
		dataCalls++
		if req.Header.Get("Authorization") != "Bearer new-token" {
			return &http.Response{StatusCode: http.StatusUnauthorized, Status: "401 Unauthorized", Body: io.NopCloser(strings.NewReader("expired")), Header: make(http.Header), Request: req}, nil
		}
		return &http.Response{StatusCode: http.StatusOK, Status: "200 OK", Body: io.NopCloser(strings.NewReader("")), Header: make(http.Header), Request: req}, nil
	})
	cred := &conf.Credential{APIKey: "api-key", APIEndpoint: "https://example.test", AccessToken: "old-token"}
	req := NewBearerTokenRequestor(nil, cred, &trackingManager{}, "https://example.test", "ua", &http.Client{Transport: base})

	const workers = 8
	errs := make(chan error, workers)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- req.Do(context.Background(), http.MethodGet, "/data", nil, nil)
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent request returned error: %v", err)
		}
	}

	mu.Lock()
	defer mu.Unlock()
	if refreshCalls != 1 {
		t.Fatalf("expected one token refresh, got %d (data calls=%d)", refreshCalls, dataCalls)
	}
	if cred.AccessToken != "new-token" {
		t.Fatalf("expected refreshed token, got %q", cred.AccessToken)
	}
}

func TestRequestDoPreservesExplicitBearerOn401(t *testing.T) {
	var calls int
	var refreshCalls int
	base := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		calls++
		if state, ok := req.Context().Value(authRequestContextKey{}).(authRequestContext); !ok || !state.explicitAuth {
			t.Fatalf("explicit auth context marker missing: state=%+v ok=%v", state, ok)
		}
		if req.URL.Path == "/user/credentials/api/access_token" {
			refreshCalls++
		}
		return &http.Response{StatusCode: http.StatusUnauthorized, Status: "401 Unauthorized", Body: io.NopCloser(strings.NewReader("denied")), Header: make(http.Header), Request: req}, nil
	})
	cred := &conf.Credential{APIKey: "api-key", APIEndpoint: "https://example.test", AccessToken: "managed-token"}
	req := NewBearerTokenRequestor(nil, cred, &trackingManager{}, "https://example.test", "ua", &http.Client{Transport: base})
	err := req.Do(context.Background(), http.MethodGet, "/data", nil, nil, WithHeader("Authorization", "Bearer caller-token"))
	if err == nil || !strings.Contains(err.Error(), "status 401") {
		t.Fatalf("expected caller authorization error, got %v", err)
	}
	if calls != 1 || refreshCalls != 0 {
		t.Fatalf("expected one request and no refresh, got requests=%d refreshes=%d", calls, refreshCalls)
	}
}

func TestRequestDoRefreshesEmptyBearerAfter401(t *testing.T) {
	var refreshCalls int
	base := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.URL.Path == "/user/credentials/api/access_token" {
			refreshCalls++
			return &http.Response{StatusCode: http.StatusOK, Status: "200 OK", Body: io.NopCloser(strings.NewReader(`{"access_token":"bootstrapped"}`)), Header: make(http.Header), Request: req}, nil
		}
		if req.Header.Get("Authorization") != "Bearer bootstrapped" {
			return &http.Response{StatusCode: http.StatusUnauthorized, Status: "401 Unauthorized", Body: io.NopCloser(strings.NewReader("missing token")), Header: make(http.Header), Request: req}, nil
		}
		return &http.Response{StatusCode: http.StatusOK, Status: "200 OK", Body: io.NopCloser(strings.NewReader("")), Header: make(http.Header), Request: req}, nil
	})
	cred := &conf.Credential{APIKey: "api-key", APIEndpoint: "https://example.test"}
	req := NewBearerTokenRequestor(nil, cred, &trackingManager{}, "https://example.test", "ua", &http.Client{Transport: base})
	if err := req.Do(context.Background(), http.MethodGet, "/data", nil, nil); err != nil {
		t.Fatalf("empty-token request returned error: %v", err)
	}
	if refreshCalls != 1 || cred.AccessToken != "bootstrapped" {
		t.Fatalf("expected one bootstrap refresh, got calls=%d token=%q", refreshCalls, cred.AccessToken)
	}
}

func TestAuthTransportRefreshIfCurrent(t *testing.T) {
	t.Parallel()

	transport := &AuthTransport{}
	if err := transport.refreshIfCurrent(context.Background(), ""); err != nil {
		t.Fatalf("refreshIfCurrent with nil cred returned error: %v", err)
	}

	transport = &AuthTransport{Mode: AuthModeBearer, Cred: &conf.Credential{AccessToken: "already-present", APIEndpoint: "https://example.test"}}
	if err := transport.refreshIfCurrent(context.Background(), "old-token"); err != nil {
		t.Fatalf("refreshIfCurrent with changed token returned error: %v", err)
	}

	transport = &AuthTransport{Mode: AuthModeBasic, Cred: &conf.Credential{APIKey: "basic-pass"}}
	if err := transport.refreshIfCurrent(context.Background(), "old-token"); err != nil {
		t.Fatalf("refreshIfCurrent in basic mode should no-op, got error: %v", err)
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

	t.Run("save failure is returned and token is restored", func(t *testing.T) {
		wantErr := errors.New("save failed")
		mgr := &trackingManager{saveErr: wantErr}
		cred := &conf.Credential{APIKey: "key", APIEndpoint: "https://example.test", AccessToken: "old-token"}
		transport := &AuthTransport{
			Mode:    AuthModeBearer,
			Manager: mgr,
			Base: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: http.StatusOK, Status: "200 OK", Body: io.NopCloser(strings.NewReader(`{"access_token":"new-token"}`)), Header: make(http.Header), Request: req}, nil
			}),
			Cred: cred,
		}
		if err := transport.NewAccessToken(ctx); err == nil || !errors.Is(err, wantErr) {
			t.Fatalf("expected save error, got %v", err)
		}
		if cred.AccessToken != "old-token" {
			t.Fatalf("expected old token after save failure, got %q", cred.AccessToken)
		}
	})
}
