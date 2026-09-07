package authentication

import (
	"context"
	"crypto/rsa"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

const (
	defaultJWKSCacheTTL          = 15 * time.Minute
	defaultUnknownKeyCooldown    = 30 * time.Second
	defaultAuthenticationTimeout = 10 * time.Second
)

func discoverJWKSURLContext(ctx context.Context, issuer string, client *http.Client) (string, error) {
	issuer = strings.TrimRight(issuer, "/")
	openidConfigURL := issuer + "/.well-known/openid-configuration"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, openidConfigURL, nil)
	if err != nil {
		return "", err
	}
	resp, err := client.Do(req)
	if err == nil {
		if resp.StatusCode == http.StatusOK {
			var data struct {
				JWKSURI string `json:"jwks_uri"`
			}
			err := json.NewDecoder(resp.Body).Decode(&data)
			_ = resp.Body.Close()
			if err == nil && data.JWKSURI != "" {
				return data.JWKSURI, nil
			}
		} else {
			_ = resp.Body.Close()
		}
	}
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	return issuer + "/.well-known/jwks.json", nil
}

// jwksCache holds JWKS public keys for JWT signature verification.
type jwksCache struct {
	mu                    sync.RWMutex
	keys                  map[string]interface{} // kid -> public key
	issuer                string
	jwksURL               string
	ttl                   time.Duration
	unknownKeyCooldown    time.Duration
	lastFetch             time.Time
	lastUnknownKeyRefresh time.Time
	loaded                bool
	client                *http.Client
	now                   func() time.Time
}

// jwk represents a JSON Web Key.
type jwk struct {
	Kty string `json:"kty"` // Key type (RSA, EC, etc)
	Use string `json:"use"` // Use (sig, enc)
	Kid string `json:"kid"` // Key ID
	N   string `json:"n"`   // RSA modulus
	E   string `json:"e"`   // RSA exponent
}

// jwks represents a JSON Web Key Set response.
type jwks struct {
	Keys []jwk `json:"keys"`
}

// newJWKSCache creates a new JWKS cache for the given endpoint.
func newJWKSCache(jwksURL string, ttl time.Duration) *jwksCache {
	return &jwksCache{
		keys:               make(map[string]interface{}),
		jwksURL:            jwksURL,
		ttl:                ttl,
		unknownKeyCooldown: defaultUnknownKeyCooldown,
		client:             &http.Client{Timeout: defaultAuthenticationTimeout},
		now:                time.Now,
	}
}

func newIssuerJWKSCache(issuer string, client *http.Client, now func() time.Time) *jwksCache {
	cache := newJWKSCache("", defaultJWKSCacheTTL)
	cache.issuer = issuer
	if client != nil {
		cache.client = client
	}
	if now != nil {
		cache.now = now
	}
	return cache
}

func (c *jwksCache) fetchKeysLocked(ctx context.Context, force bool) error {
	now := c.now()
	if !force && c.loaded && now.Sub(c.lastFetch) >= 0 && now.Sub(c.lastFetch) < c.ttl {
		return nil
	}
	if c.jwksURL == "" {
		if c.issuer == "" {
			return fmt.Errorf("JWKS issuer is not configured")
		}
		jwksURL, err := discoverJWKSURLContext(ctx, c.issuer, c.client)
		if err != nil {
			return fmt.Errorf("discover JWKS URL: %w", err)
		}
		c.jwksURL = jwksURL
	}
	if err := validateJWKSURL(c.jwksURL); err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.jwksURL, nil)
	if err != nil {
		return fmt.Errorf("create JWKS request: %w", err)
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("fetch JWKS from %s: %w", c.jwksURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, err := io.ReadAll(io.LimitReader(resp.Body, 1024))
		if err != nil {
			return fmt.Errorf("JWKS fetch failed with status %d and unreadable body: %w", resp.StatusCode, err)
		}
		return fmt.Errorf("JWKS fetch failed with status %d: %s", resp.StatusCode, string(body))
	}

	var keySet jwks
	if err := json.NewDecoder(resp.Body).Decode(&keySet); err != nil {
		return fmt.Errorf("decode JWKS response: %w", err)
	}

	// Convert JWKs to Go crypto keys
	keys := make(map[string]interface{})
	for _, jwk := range keySet.Keys {
		if jwk.Kty != "RSA" {
			continue
		}

		pubKey, err := jwkToRSAPublicKey(jwk)
		if err != nil {
			return fmt.Errorf("convert JWK to RSA key (kid=%s): %w", jwk.Kid, err)
		}

		keys[jwk.Kid] = pubKey
	}

	// Replace the key set only after a complete, successful response decode.
	c.keys = keys
	c.lastFetch = now
	c.loaded = true
	return nil
}

func validateJWKSURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("invalid JWKS endpoint: %w", err)
	}
	if u.Scheme != "https" || u.Host == "" {
		return fmt.Errorf("JWKS endpoint must use HTTPS, got: %s", raw)
	}
	return nil
}

// getKey retrieves a key by KID.
func (c *jwksCache) getKey(kid string) (interface{}, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	key, ok := c.keys[kid]
	if !ok {
		return nil, fmt.Errorf("key not found: %s", kid)
	}
	return key, nil
}

// keyForToken loads the current key set and permits one forced refresh for a
// missing key during the cooldown window. The mutex serializes discovery and
// refreshes for an issuer, including failed refresh attempts.
func (c *jwksCache) keyForToken(ctx context.Context, kid string) (interface{}, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	now := c.now()
	ordinaryRefresh := false
	if !c.loaded || now.Sub(c.lastFetch) < 0 || now.Sub(c.lastFetch) >= c.ttl {
		if c.refreshCooldownActive(now) {
			return nil, fmt.Errorf("JWKS refresh cooldown active")
		}
		if err := c.fetchKeysLocked(ctx, false); err != nil {
			// A failed initial or expiry refresh also consumes the cooldown.
			c.lastUnknownKeyRefresh = now
			return nil, err
		}
		// A successful ordinary load does not consume unknown-key refresh budget.
		c.lastUnknownKeyRefresh = time.Time{}
		ordinaryRefresh = true
	}
	if key, ok := c.keys[kid]; ok {
		return key, nil
	}

	// The ordinary cold/expiry refresh already checked the complete new set.
	// Do not immediately fetch it a second time for the same unknown KID.
	if ordinaryRefresh {
		c.lastUnknownKeyRefresh = now
		return nil, fmt.Errorf("key not found: %s", kid)
	}
	if c.refreshCooldownActive(now) {
		return nil, fmt.Errorf("key not found: %s (refresh cooldown active)", kid)
	}
	c.lastUnknownKeyRefresh = now
	if err := c.fetchKeysLocked(ctx, true); err != nil {
		return nil, err
	}
	key, ok := c.keys[kid]
	if !ok {
		return nil, fmt.Errorf("key not found: %s", kid)
	}
	return key, nil
}

func (c *jwksCache) refreshCooldownActive(now time.Time) bool {
	return !c.lastUnknownKeyRefresh.IsZero() && now.Sub(c.lastUnknownKeyRefresh) >= 0 && now.Sub(c.lastUnknownKeyRefresh) < c.unknownKeyCooldown
}

// jwkToRSAPublicKey converts a JWK to an RSA public key
func jwkToRSAPublicKey(jwk jwk) (*rsa.PublicKey, error) {
	// Decode N (modulus)
	nBytes, err := decodeBase64URL(jwk.N)
	if err != nil {
		return nil, fmt.Errorf("decode modulus: %w", err)
	}

	// Decode E (exponent)
	eBytes, err := decodeBase64URL(jwk.E)
	if err != nil {
		return nil, fmt.Errorf("decode exponent: %w", err)
	}

	// Convert to big.Int
	n := new(big.Int).SetBytes(nBytes)
	e := bytesToInt(eBytes)

	if e == 0 {
		return nil, fmt.Errorf("invalid exponent")
	}

	return &rsa.PublicKey{
		N: n,
		E: e,
	}, nil
}

// decodeBase64URL decodes base64url-encoded string
func decodeBase64URL(s string) ([]byte, error) {
	// Add padding if needed
	padding := (4 - len(s)%4) % 4
	s = strings.ReplaceAll(s, "-", "+")
	s = strings.ReplaceAll(s, "_", "/")
	s = s + strings.Repeat("=", padding)

	return base64.StdEncoding.DecodeString(s)
}

// bytesToInt converts bytes to int (big-endian)
func bytesToInt(b []byte) int {
	if len(b) == 0 {
		return 0
	}
	if len(b) <= 4 {
		result := 0
		for _, byte := range b {
			result = result*256 + int(byte)
		}
		return result
	}
	// For larger values, use the last 4 bytes
	return int(binary.BigEndian.Uint32(b[len(b)-4:]))
}
