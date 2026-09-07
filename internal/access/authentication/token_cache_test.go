package authentication

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

type countingAuthTransport struct {
	mu              sync.Mutex
	discovery       int
	jwks            int
	jwksStatus      int
	jwksBody        []byte
	discoveryBody   []byte
	jwksBodies      map[string][]byte
	discoveryBodies map[string][]byte
	jwksStatuses    map[string]int
}

func (t *countingAuthTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	var status int
	var body []byte
	switch {
	case strings.HasSuffix(req.URL.Path, "/.well-known/openid-configuration"):
		t.discovery++
		status, body = http.StatusOK, t.discoveryBody
		if mapped, ok := t.discoveryBodies[req.URL.Host]; ok {
			body = mapped
		}
	case req.URL.Path == "/jwks":
		t.jwks++
		status, body = t.jwksStatus, t.jwksBody
		if mapped, ok := t.jwksStatuses[req.URL.Host]; ok {
			status = mapped
		}
		if mapped, ok := t.jwksBodies[req.URL.Host]; ok {
			body = mapped
		}
	default:
		return nil, fmt.Errorf("unexpected authentication URL %s", req.URL)
	}
	if status == 0 {
		status = http.StatusOK
	}
	return &http.Response{
		StatusCode: status,
		Body:       io.NopCloser(bytes.NewReader(body)),
		Header:     make(http.Header),
		Request:    req,
	}, nil
}

func authJWKSBody(t *testing.T, kid string, key *rsa.PrivateKey) []byte {
	t.Helper()
	return []byte(fmt.Sprintf(`{"keys":[{"kty":"RSA","use":"sig","kid":%q,"n":%q,"e":%q}]}`,
		kid,
		base64.RawURLEncoding.EncodeToString(key.PublicKey.N.Bytes()),
		base64.RawURLEncoding.EncodeToString(new(big.Int).SetInt64(int64(key.PublicKey.E)).Bytes()),
	))
}

func authToken(t *testing.T, issuer, kid string, key *rsa.PrivateKey) string {
	t.Helper()
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{
		"iss": issuer,
		"exp": time.Now().Add(time.Hour).Unix(),
	})
	token.Header["kid"] = kid
	value, err := token.SignedString(key)
	if err != nil {
		t.Fatalf("sign token: %v", err)
	}
	return value
}

func newCountingTokenVerifier(transport *countingAuthTransport) *tokenVerifier {
	verifier := newTokenVerifierWithHTTPClient(&http.Client{Transport: transport})
	return verifier
}

func TestTokenVerifierReusesDiscoveryAndKeysConcurrently(t *testing.T) {
	issuer := "https://issuer.example"
	t.Setenv("DRS_FENCE_URL", issuer)
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	transport := &countingAuthTransport{
		discoveryBody: []byte(`{"jwks_uri":"https://keys.example/jwks"}`),
		jwksBody:      authJWKSBody(t, "key-1", key),
	}
	verifier := newCountingTokenVerifier(transport)
	token := authToken(t, issuer, "key-1", key)

	const requests = 20
	var wg sync.WaitGroup
	errs := make(chan error, requests)
	for i := 0; i < requests; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _, err := verifier.parseToken(context.Background(), token)
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent verification failed: %v", err)
		}
	}
	transport.mu.Lock()
	defer transport.mu.Unlock()
	if transport.discovery != 1 || transport.jwks != 1 {
		t.Fatalf("request counts = discovery %d, JWKS %d; want 1, 1", transport.discovery, transport.jwks)
	}
}

func TestTokenVerifierRefreshesExpiredKeysAndRejectsStaleKeys(t *testing.T) {
	issuer := "https://issuer.example"
	t.Setenv("DRS_FENCE_URL", issuer)
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	transport := &countingAuthTransport{
		discoveryBody: []byte(`{"jwks_uri":"https://keys.example/jwks"}`),
		jwksBody:      authJWKSBody(t, "key-1", key),
	}
	verifier := newCountingTokenVerifier(transport)
	clock := time.Date(2026, 9, 6, 0, 0, 0, 0, time.UTC)
	verifier.now = func() time.Time { return clock }
	validToken := authToken(t, issuer, "key-1", key)
	if _, _, err := verifier.parseToken(context.Background(), validToken); err != nil {
		t.Fatalf("initial verification failed: %v", err)
	}
	clock = clock.Add(defaultJWKSCacheTTL + time.Second)
	if _, _, err := verifier.parseToken(context.Background(), validToken); err != nil {
		t.Fatalf("expired-cache verification failed: %v", err)
	}
	transport.mu.Lock()
	if transport.jwks != 2 {
		t.Fatalf("JWKS requests after TTL = %d, want 2", transport.jwks)
	}
	transport.mu.Unlock()

	transport.mu.Lock()
	transport.jwksStatus = http.StatusServiceUnavailable
	transport.mu.Unlock()
	clock = clock.Add(defaultJWKSCacheTTL + time.Second)
	if _, _, err := verifier.parseToken(context.Background(), validToken); err == nil {
		t.Fatal("known key unexpectedly verified after failed expired refresh")
	}

	unknownToken := authToken(t, issuer, "unknown", key)
	if _, _, err := verifier.parseToken(context.Background(), unknownToken); err == nil {
		t.Fatal("unknown key unexpectedly verified after failed expired refresh")
	}
	if _, _, err := verifier.parseToken(context.Background(), unknownToken); err == nil {
		t.Fatal("unknown key unexpectedly verified on cooldown retry")
	}
	transport.mu.Lock()
	defer transport.mu.Unlock()
	if transport.jwks != 3 {
		t.Fatalf("JWKS requests after failed expired refresh = %d, want 3", transport.jwks)
	}
}

func TestTokenVerifierFailedInitialFetchCooldownBoundsRandomKIDs(t *testing.T) {
	issuer := "https://issuer.example"
	t.Setenv("DRS_FENCE_URL", issuer)
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	transport := &countingAuthTransport{
		discoveryBody: []byte(`{"jwks_uri":"https://keys.example/jwks"}`),
		jwksStatus:    http.StatusBadGateway,
		jwksBody:      authJWKSBody(t, "key-1", key),
	}
	verifier := newCountingTokenVerifier(transport)
	clock := time.Date(2026, 9, 6, 0, 0, 0, 0, time.UTC)
	verifier.now = func() time.Time { return clock }
	for _, kid := range []string{"random-1", "random-2", "random-3"} {
		if _, _, err := verifier.parseToken(context.Background(), authToken(t, issuer, kid, key)); err == nil {
			t.Fatalf("unknown key %q unexpectedly verified", kid)
		}
	}
	transport.mu.Lock()
	discovery, jwks := transport.discovery, transport.jwks
	transport.jwksStatus = http.StatusOK
	transport.mu.Unlock()
	if discovery != 1 || jwks != 1 {
		t.Fatalf("failed initial fetch counts = discovery %d, JWKS %d; want 1, 1", discovery, jwks)
	}
	clock = clock.Add(defaultUnknownKeyCooldown + time.Second)
	if _, _, err := verifier.parseToken(context.Background(), authToken(t, issuer, "random-4", key)); err == nil {
		t.Fatal("random key unexpectedly verified after failed-refresh cooldown recovery")
	}
	transport.mu.Lock()
	defer transport.mu.Unlock()
	if transport.jwks != 2 {
		t.Fatalf("JWKS requests after failed-refresh cooldown recovery = %d, want 2", transport.jwks)
	}
}

func TestTokenVerifierUnknownKIDRotationAndCooldownRecovery(t *testing.T) {
	issuer := "https://issuer.example"
	t.Setenv("DRS_FENCE_URL", issuer)
	keyOne, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate first key: %v", err)
	}
	keyTwo, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate rotated key: %v", err)
	}
	transport := &countingAuthTransport{
		discoveryBody: []byte(`{"jwks_uri":"https://keys.example/jwks"}`),
		jwksBody:      authJWKSBody(t, "key-1", keyOne),
	}
	verifier := newCountingTokenVerifier(transport)
	clock := time.Date(2026, 9, 6, 0, 0, 0, 0, time.UTC)
	verifier.now = func() time.Time { return clock }
	if _, _, err := verifier.parseToken(context.Background(), authToken(t, issuer, "key-1", keyOne)); err != nil {
		t.Fatalf("initial key verification failed: %v", err)
	}

	transport.mu.Lock()
	transport.jwksBody = authJWKSBody(t, "key-2", keyTwo)
	transport.mu.Unlock()
	if _, _, err := verifier.parseToken(context.Background(), authToken(t, issuer, "key-2", keyTwo)); err != nil {
		t.Fatalf("rotated key verification failed: %v", err)
	}
	transport.mu.Lock()
	if transport.jwks != 2 {
		t.Fatalf("JWKS requests after successful rotation = %d, want 2", transport.jwks)
	}
	transport.mu.Unlock()

	clock = clock.Add(defaultUnknownKeyCooldown + time.Second)
	unknown := func(kid string) error {
		_, _, err := verifier.parseToken(context.Background(), authToken(t, issuer, kid, keyTwo))
		return err
	}
	if err := unknown("random-1"); err == nil {
		t.Fatal("random unknown KID unexpectedly verified")
	}
	if err := unknown("random-2"); err == nil {
		t.Fatal("random unknown KID unexpectedly bypassed cooldown")
	}
	transport.mu.Lock()
	if transport.jwks != 3 {
		t.Fatalf("JWKS requests during unknown-KID flood = %d, want 3", transport.jwks)
	}
	transport.mu.Unlock()

	clock = clock.Add(defaultUnknownKeyCooldown + time.Second)
	if err := unknown("random-3"); err == nil {
		t.Fatal("random unknown KID unexpectedly verified after cooldown")
	}
	transport.mu.Lock()
	jwks := transport.jwks
	transport.mu.Unlock()
	if jwks != 4 {
		t.Fatalf("JWKS requests after cooldown recovery = %d, want 4", jwks)
	}

	clock = clock.Add(defaultUnknownKeyCooldown + time.Second)
	transport.mu.Lock()
	transport.jwksStatus = http.StatusServiceUnavailable
	transport.mu.Unlock()
	if err := unknown("random-4"); err == nil {
		t.Fatal("unknown KID unexpectedly verified after failed refresh")
	}
	if err := unknown("random-5"); err == nil {
		t.Fatal("unknown KID unexpectedly bypassed failed-refresh cooldown")
	}
	transport.mu.Lock()
	jwks = transport.jwks
	transport.mu.Unlock()
	if jwks != 5 {
		t.Fatalf("JWKS requests during failed fresh-cache refresh = %d, want 5", jwks)
	}

	clock = clock.Add(defaultUnknownKeyCooldown + time.Second)
	transport.mu.Lock()
	transport.jwksStatus = http.StatusOK
	transport.mu.Unlock()
	if err := unknown("random-6"); err == nil {
		t.Fatal("unknown KID unexpectedly verified after failed-refresh recovery")
	}
	transport.mu.Lock()
	defer transport.mu.Unlock()
	if transport.jwks != 6 {
		t.Fatalf("JWKS requests after failed-refresh recovery = %d, want 6", transport.jwks)
	}
}

func TestTokenVerifierSeparatesIssuerCachesAndRechecksAllowlist(t *testing.T) {
	issuerA := "https://issuer-a.example"
	issuerB := "https://issuer-b.example"
	keyA, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate issuer A key: %v", err)
	}
	keyB, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate issuer B key: %v", err)
	}
	transport := &countingAuthTransport{
		discoveryBodies: map[string][]byte{
			"issuer-a.example": []byte(`{"jwks_uri":"https://keys-a.example/jwks"}`),
			"issuer-b.example": []byte(`{"jwks_uri":"https://keys-b.example/jwks"}`),
		},
		jwksBodies: map[string][]byte{
			"keys-a.example": authJWKSBody(t, "shared-kid", keyA),
			"keys-b.example": authJWKSBody(t, "shared-kid", keyB),
		},
	}
	verifier := newCountingTokenVerifier(transport)
	t.Setenv("DRS_FENCE_URL", issuerA)
	tokenA := authToken(t, issuerA, "shared-kid", keyA)
	if _, _, err := verifier.parseToken(context.Background(), tokenA); err != nil {
		t.Fatalf("issuer A verification failed: %v", err)
	}

	t.Setenv("DRS_FENCE_URL", issuerB)
	wrongB := authToken(t, issuerB, "shared-kid", keyA)
	if _, _, err := verifier.parseToken(context.Background(), wrongB); err == nil {
		t.Fatal("issuer B accepted issuer A key for shared KID")
	}
	validB := authToken(t, issuerB, "shared-kid", keyB)
	if _, _, err := verifier.parseToken(context.Background(), validB); err != nil {
		t.Fatalf("issuer B verification failed with its own key: %v", err)
	}
	if _, _, err := verifier.parseToken(context.Background(), tokenA); err == nil {
		t.Fatal("cached issuer A token passed after allowlist moved to issuer B")
	}

	transport.mu.Lock()
	defer transport.mu.Unlock()
	if transport.discovery != 2 || transport.jwks != 2 {
		t.Fatalf("issuer-isolated request counts = discovery %d, JWKS %d; want 2, 2", transport.discovery, transport.jwks)
	}
}

func TestTokenVerifierRejectsInsecureDiscoveredJWKS(t *testing.T) {
	issuer := "https://issuer.example"
	t.Setenv("DRS_FENCE_URL", issuer)
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	transport := &countingAuthTransport{
		discoveryBodies: map[string][]byte{
			"issuer.example": []byte(`{"jwks_uri":"http://keys.example/jwks"}`),
		},
		jwksBody: authJWKSBody(t, "key-1", key),
	}
	verifier := newCountingTokenVerifier(transport)
	if _, _, err := verifier.parseToken(context.Background(), authToken(t, issuer, "key-1", key)); err == nil {
		t.Fatal("token unexpectedly verified through insecure discovered JWKS")
	}
	transport.mu.Lock()
	defer transport.mu.Unlock()
	if transport.discovery != 1 || transport.jwks != 0 {
		t.Fatalf("insecure JWKS request counts = discovery %d, JWKS %d; want 1, 0", transport.discovery, transport.jwks)
	}
}

func TestTokenVerifierRejectsInvalidSignatureWithCachedKey(t *testing.T) {
	issuer := "https://issuer.example"
	t.Setenv("DRS_FENCE_URL", issuer)
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	wrongKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate wrong key: %v", err)
	}
	transport := &countingAuthTransport{
		discoveryBody: []byte(`{"jwks_uri":"https://keys.example/jwks"}`),
		jwksBody:      authJWKSBody(t, "key-1", key),
	}
	verifier := newCountingTokenVerifier(transport)
	valid := authToken(t, issuer, "key-1", key)
	if _, _, err := verifier.parseToken(context.Background(), valid); err != nil {
		t.Fatalf("valid token verification failed: %v", err)
	}
	if _, _, err := verifier.parseToken(context.Background(), authToken(t, issuer, "key-1", wrongKey)); err == nil {
		t.Fatal("invalid signature unexpectedly passed with cached key")
	}
	transport.mu.Lock()
	defer transport.mu.Unlock()
	if transport.jwks != 1 {
		t.Fatalf("JWKS requests while checking cached invalid signature = %d, want 1", transport.jwks)
	}
}
