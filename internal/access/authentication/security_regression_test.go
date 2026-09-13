package authentication

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/calypr/syfon/internal/access"
	"github.com/calypr/syfon/plugin"
)

func TestRuntimeEvaluatorRedactsAuthenticationDecisionLog(t *testing.T) {
	const (
		requestID   = "request-123"
		subject     = "subject-secret-value"
		reason      = "plugin-reason-secret-value"
		claimSecret = "nested-claim-secret-value"
	)
	claims := map[string]interface{}{
		"nested": map[string]interface{}{"secret": claimSecret},
		"scope":  []interface{}{"read", "write"},
	}
	authz := &recordingAuthorizationPlugin{}
	var logs bytes.Buffer
	runtime := &Runtime{
		logger: slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})),
		authentication: &recordingAuthenticationPlugin{output: &plugin.AuthenticationOutput{
			Authenticated: true,
			Subject:       subject,
			Claims:        claims,
			Reason:        reason,
		}},
		authorization: authz,
	}

	result := runtime.Evaluate(access.EvaluationRequest{
		Context:    context.Background(),
		RequestID:  requestID,
		Mode:       "gen3",
		AuthHeader: "Bearer token",
		Method:     http.MethodGet,
		Path:       "/objects/object-id",
	})
	if result.Decision != access.DecisionContinue {
		t.Fatalf("authentication decision = %v, want continue", result.Decision)
	}
	if authz.input == nil {
		t.Fatal("authorization plugin did not receive an input")
	}
	if authz.input.Subject != subject {
		t.Fatalf("authorization subject = %q, want %q", authz.input.Subject, subject)
	}
	nested, ok := authz.input.Claims["nested"].(map[string]interface{})
	if !ok || nested["secret"] != claimSecret {
		t.Fatalf("authorization claims lost nested value: %#v", authz.input.Claims)
	}

	logText := logs.String()
	if !strings.Contains(logText, "authentication plugin output") {
		t.Fatalf("authentication decision log changed event identity: %s", logText)
	}
	for _, secret := range []string{subject, reason, claimSecret, "nested"} {
		if strings.Contains(logText, secret) {
			t.Fatalf("authentication decision log contains %q: %s", secret, logText)
		}
	}
	for _, expected := range []string{"request_id=" + requestID, "authenticated=true", "claim_count=2"} {
		if !strings.Contains(logText, expected) {
			t.Fatalf("authentication decision log missing %q: %s", expected, logText)
		}
	}

	t.Run("plugin error is also bounded", func(t *testing.T) {
		var errorLogs bytes.Buffer
		runtime := &Runtime{
			logger:         slog.New(slog.NewTextHandler(&errorLogs, &slog.HandlerOptions{Level: slog.LevelDebug})),
			authentication: &recordingAuthenticationPlugin{err: errors.New(reason)},
		}
		result := runtime.Evaluate(access.EvaluationRequest{
			Context:    context.Background(),
			RequestID:  requestID,
			Mode:       "gen3",
			AuthHeader: "Bearer token",
		})
		if result.Decision != access.DecisionUnauthorized {
			t.Fatalf("authentication decision = %v, want unauthorized", result.Decision)
		}
		if strings.Contains(errorLogs.String(), reason) {
			t.Fatalf("authentication failure log contains plugin error: %s", errorLogs.String())
		}
	})
}

type redirectAuthResponse struct {
	status   int
	location string
	body     []byte
	closed   *closeCounter
}

type redirectAuthTransport struct {
	mu        sync.Mutex
	responses map[string]redirectAuthResponse
	requests  []string
}

func (t *redirectAuthTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	key := req.URL.Scheme + "://" + req.URL.Host + req.URL.Path
	t.mu.Lock()
	t.requests = append(t.requests, key)
	response, ok := t.responses[key]
	t.mu.Unlock()
	if !ok {
		return nil, fmt.Errorf("unexpected authentication URL %s", req.URL)
	}
	body := io.ReadCloser(io.NopCloser(bytes.NewReader(response.body)))
	if response.closed != nil {
		body = &countingCloseBody{Reader: bytes.NewReader(response.body), counter: response.closed}
	}
	result := &http.Response{
		StatusCode:    response.status,
		Body:          body,
		ContentLength: int64(len(response.body)),
		Header:        make(http.Header),
		Request:       req,
	}
	if response.location != "" {
		result.Header.Set("Location", response.location)
	}
	return result, nil
}

func (t *redirectAuthTransport) requestedURLs() []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]string(nil), t.requests...)
}

type closeCounter struct {
	mu     sync.Mutex
	closed int
}

func (c *closeCounter) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.closed
}

type countingCloseBody struct {
	*bytes.Reader
	counter *closeCounter
}

func (b *countingCloseBody) Close() error {
	b.counter.mu.Lock()
	b.counter.closed++
	b.counter.mu.Unlock()
	return nil
}

func TestTokenVerifierRejectsDirectHTTPJWKS(t *testing.T) {
	transport := &redirectAuthTransport{responses: map[string]redirectAuthResponse{}}
	cache := newJWKSCache("http://keys.example/jwks", time.Minute)
	cache.client = &http.Client{Transport: transport}

	if _, err := cache.keyForToken(context.Background(), "key-1"); err == nil {
		t.Fatal("HTTP JWKS endpoint unexpectedly succeeded")
	}
	if got := transport.requestedURLs(); len(got) != 0 {
		t.Fatalf("HTTP JWKS endpoint issued requests: %v", got)
	}
}

func TestTokenVerifierRejectsDiscoveryDowngradeWithoutFallback(t *testing.T) {
	issuer := "https://issuer.example"
	discoveryURL := issuer + "/.well-known/openid-configuration"
	downgradeURL := "http://downgrade.example/.well-known/openid-configuration"
	fallbackURL := issuer + "/.well-known/jwks.json"
	closed := &closeCounter{}
	transport := &redirectAuthTransport{responses: map[string]redirectAuthResponse{
		discoveryURL: {status: http.StatusFound, location: downgradeURL, closed: closed},
		downgradeURL: {status: http.StatusOK, body: []byte(`{"jwks_uri":"https://keys.example/jwks"}`)},
		fallbackURL:  {status: http.StatusOK, body: []byte(`{"keys":[]}`)},
	}}
	cache := newIssuerJWKSCache(issuer, &http.Client{Transport: transport}, time.Now)

	if _, err := cache.keyForToken(context.Background(), "key-1"); err == nil {
		t.Fatal("discovery downgrade unexpectedly succeeded")
	}
	if got := transport.requestedURLs(); len(got) != 1 || got[0] != discoveryURL {
		t.Fatalf("discovery downgrade requests = %v, want only %q", got, discoveryURL)
	}
	if got := closed.count(); got != 1 {
		t.Fatalf("rejected discovery response closes = %d, want 1", got)
	}
}

func TestTokenVerifierRejectsMultiHopDowngrade(t *testing.T) {
	issuer := "https://issuer.example"
	discoveryURL := issuer + "/.well-known/openid-configuration"
	secondHopURL := "https://redirect.example/.well-known/openid-configuration"
	downgradeURL := "http://downgrade.example/.well-known/openid-configuration"
	firstClosed := &closeCounter{}
	secondClosed := &closeCounter{}
	transport := &redirectAuthTransport{responses: map[string]redirectAuthResponse{
		discoveryURL: {status: http.StatusFound, location: secondHopURL, closed: firstClosed},
		secondHopURL: {status: http.StatusFound, location: downgradeURL, closed: secondClosed},
		downgradeURL: {status: http.StatusOK, body: []byte(`{"jwks_uri":"https://keys.example/jwks"}`)},
	}}
	cache := newIssuerJWKSCache(issuer, &http.Client{Transport: transport}, time.Now)

	if _, err := cache.keyForToken(context.Background(), "key-1"); err == nil {
		t.Fatal("multi-hop discovery downgrade unexpectedly succeeded")
	}
	if got := transport.requestedURLs(); len(got) != 2 || got[0] != discoveryURL || got[1] != secondHopURL {
		t.Fatalf("multi-hop downgrade requests = %v, want HTTPS hops only", got)
	}
	if firstClosed.count() != 1 || secondClosed.count() != 1 {
		t.Fatalf("redirect response closes = first %d, second %d, want 1 each", firstClosed.count(), secondClosed.count())
	}
}

func TestTokenVerifierAllowsSeparateHTTPSHostsAndMultiHopHTTPSRedirects(t *testing.T) {
	issuer := "https://issuer.example"
	discoveryURL := issuer + "/.well-known/openid-configuration"
	discoveryHopURL := "https://discovery.example/.well-known/openid-configuration"
	keysURL := "https://keys.example/jwks"
	keysHopURL := "https://keys-redirect.example/jwks"
	keyBody := []byte(`{"keys":[]}`)
	transport := &redirectAuthTransport{responses: map[string]redirectAuthResponse{
		discoveryURL:    {status: http.StatusFound, location: discoveryHopURL},
		discoveryHopURL: {status: http.StatusOK, body: []byte(`{"jwks_uri":"https://keys.example/jwks"}`)},
		keysURL:         {status: http.StatusFound, location: keysHopURL},
		keysHopURL:      {status: http.StatusOK, body: keyBody},
	}}
	var redirects []string
	client := &http.Client{
		Transport: transport,
		CheckRedirect: func(req *http.Request, _ []*http.Request) error {
			redirects = append(redirects, req.URL.String())
			return nil
		},
	}
	cache := newIssuerJWKSCache(issuer, client, time.Now)

	if _, err := cache.keyForToken(context.Background(), "key-1"); err == nil {
		t.Fatal("empty JWKS unexpectedly returned a key")
	}
	wantRedirects := []string{discoveryHopURL, keysHopURL}
	if fmt.Sprint(redirects) != fmt.Sprint(wantRedirects) {
		t.Fatalf("redirect callback targets = %v, want %v", redirects, wantRedirects)
	}
	if got := transport.requestedURLs(); fmt.Sprint(got) != fmt.Sprint([]string{discoveryURL, discoveryHopURL, keysURL, keysHopURL}) {
		t.Fatalf("HTTPS redirect requests = %v", got)
	}
}

func TestTokenVerifierFailedRedirectKeepsLoadedKeys(t *testing.T) {
	issuer := "https://issuer.example"
	discoveryURL := issuer + "/.well-known/openid-configuration"
	keysURL := "https://keys.example/jwks"
	downgradeURL := "http://downgrade.example/jwks"
	keyBody := []byte(`{"keys":[{"kty":"RSA","use":"sig","kid":"key-1","n":"AQ","e":"AQ"}]}`)
	transport := &redirectAuthTransport{responses: map[string]redirectAuthResponse{
		discoveryURL: {status: http.StatusOK, body: []byte(`{"jwks_uri":"https://keys.example/jwks"}`)},
		keysURL:      {status: http.StatusOK, body: keyBody},
		downgradeURL: {status: http.StatusOK, body: keyBody},
	}}
	clock := time.Date(2026, 9, 12, 0, 0, 0, 0, time.UTC)
	cache := newIssuerJWKSCache(issuer, &http.Client{Transport: transport}, func() time.Time { return clock })
	if _, err := cache.keyForToken(context.Background(), "key-1"); err != nil {
		t.Fatalf("initial key fetch failed: %v", err)
	}
	loadedKey := cache.keys["key-1"]
	transport.mu.Lock()
	transport.responses[keysURL] = redirectAuthResponse{status: http.StatusFound, location: downgradeURL}
	transport.mu.Unlock()
	if _, err := cache.keyForToken(context.Background(), "key-2"); err == nil {
		t.Fatal("failed JWKS redirect unexpectedly succeeded")
	}
	if cache.keys["key-1"] != loadedKey || len(cache.keys) != 1 {
		t.Fatalf("loaded keys changed after failed redirect: %#v", cache.keys)
	}
	if _, err := cache.keyForToken(context.Background(), "key-1"); err != nil {
		t.Fatalf("loaded key unavailable after failed redirect: %v", err)
	}
}

func TestTokenVerifierComposesInjectedRedirectError(t *testing.T) {
	issuer := "https://issuer.example"
	discoveryURL := issuer + "/.well-known/openid-configuration"
	hopURL := "https://discovery.example/.well-known/openid-configuration"
	transport := &redirectAuthTransport{responses: map[string]redirectAuthResponse{
		discoveryURL: {status: http.StatusFound, location: hopURL},
		hopURL:       {status: http.StatusOK, body: []byte(`{"jwks_uri":"https://keys.example/jwks"}`)},
	}}
	injectedErr := errors.New("injected redirect policy")
	client := &http.Client{
		Transport: transport,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return injectedErr
		},
	}
	cache := newIssuerJWKSCache(issuer, client, time.Now)
	_, err := cache.keyForToken(context.Background(), "key-1")
	if err == nil || !errors.Is(err, injectedErr) {
		t.Fatalf("injected redirect error = %v, want %v", err, injectedErr)
	}
}
