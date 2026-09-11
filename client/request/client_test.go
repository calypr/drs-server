package request

import (
	"context"
	"io"
	"net/http"
	"net/http/cookiejar"
	"strings"
	"sync"
	"testing"

	conf "github.com/calypr/syfon/client/config"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

type mockConfigManager struct{}

func (m *mockConfigManager) Import(string, string) (*conf.Credential, error) {
	return &conf.Credential{}, nil
}

func (m *mockConfigManager) Load(string) (*conf.Credential, error) {
	return &conf.Credential{}, nil
}

func (m *mockConfigManager) Save(*conf.Credential) error { return nil }

func (m *mockConfigManager) EnsureExists() error { return nil }

func (m *mockConfigManager) IsCredentialValid(*conf.Credential) (bool, error) {
	return true, nil
}

func (m *mockConfigManager) IsTokenValid(string) (bool, error) { return true, nil }

func TestClientPreservesStandardSettings(t *testing.T) {
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	redirect := func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	base := &http.Client{
		Jar:           jar,
		Timeout:       12,
		CheckRedirect: redirect,
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: http.StatusNoContent, Body: io.NopCloser(strings.NewReader("")), Header: make(http.Header), Request: req}, nil
		}),
	}
	client := NewClient(nil, &conf.Credential{}, nil, "test-client", base, AuthModeBasic)
	got := client.StandardClient()
	if got.Timeout != base.Timeout || got.Jar != jar {
		t.Fatalf("standard settings changed: timeout=%v jar-preserved=%v", got.Timeout, got.Jar == jar)
	}
	if got.CheckRedirect == nil {
		t.Fatal("redirect policy was not preserved")
	}
}

func TestClientRetriesSafeRequestsAndAppliesDefaults(t *testing.T) {
	var mu sync.Mutex
	calls := 0
	base := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		mu.Lock()
		calls++
		attempt := calls
		mu.Unlock()
		if req.Header.Get("User-Agent") != "test-client" {
			t.Errorf("unexpected user agent %q", req.Header.Get("User-Agent"))
		}
		if req.Header.Get("Accept") != "application/json" {
			t.Errorf("unexpected accept header %q", req.Header.Get("Accept"))
		}
		status := http.StatusBadGateway
		body := "retry"
		if attempt == 2 {
			status = http.StatusOK
			body = "ok"
		}
		return &http.Response{StatusCode: status, Status: http.StatusText(status), Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header), Request: req}, nil
	})
	client := NewClient(nil, nil, nil, "test-client", &http.Client{Transport: base}, AuthModeBasic)
	client.retry.RetryWaitMin = 0
	client.retry.RetryWaitMax = 0
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, "https://example.test/data", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("GET returned error: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK || calls != 2 {
		t.Fatalf("GET result status=%d calls=%d", resp.StatusCode, calls)
	}
}

func TestClientDoesNotRetryRequestsWithBodies(t *testing.T) {
	calls := 0
	client := NewClient(nil, nil, nil, "", &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		calls++
		return &http.Response{StatusCode: http.StatusBadGateway, Body: io.NopCloser(strings.NewReader("failed")), Header: make(http.Header), Request: req}, nil
	})}, AuthModeBasic)
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPut, "https://example.test/data", strings.NewReader("payload"))
	if err != nil {
		t.Fatal(err)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("PUT returned error: %v", err)
	}
	defer resp.Body.Close()
	if calls != 1 {
		t.Fatalf("PUT was retried %d times", calls)
	}
}
