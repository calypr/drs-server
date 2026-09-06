package authentication

import (
	"crypto/rsa"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"strings"
	"sync"
	"time"
)

func discoverJWKSURL(issuer string) (string, error) {
	issuer = strings.TrimRight(issuer, "/")
	openidConfigURL := issuer + "/.well-known/openid-configuration"
	resp, err := http.Get(openidConfigURL)
	if err == nil {
		if resp.StatusCode == http.StatusOK {
			var data struct {
				JWKSURI string `json:"jwks_uri"`
			}
			err := json.NewDecoder(resp.Body).Decode(&data)
			closeErr := resp.Body.Close()
			if closeErr != nil {
				return "", fmt.Errorf("close openid configuration response: %w", closeErr)
			}
			if err == nil && data.JWKSURI != "" {
				return data.JWKSURI, nil
			}
		} else {
			if closeErr := resp.Body.Close(); closeErr != nil {
				return "", fmt.Errorf("close openid configuration response: %w", closeErr)
			}
		}
	}
	return issuer + "/.well-known/jwks.json", nil
}

// JWKSCache holds JWKS public keys for JWT signature verification
type JWKSCache struct {
	mu        sync.RWMutex
	keys      map[string]interface{} // kid -> public key
	jwksURL   string
	ttl       time.Duration
	lastFetch time.Time
}

// JWK represents a JSON Web Key
type JWK struct {
	Kty string `json:"kty"` // Key type (RSA, EC, etc)
	Use string `json:"use"` // Use (sig, enc)
	Kid string `json:"kid"` // Key ID
	N   string `json:"n"`   // RSA modulus
	E   string `json:"e"`   // RSA exponent
}

// JWKS represents a JSON Web Key Set response
type JWKS struct {
	Keys []JWK `json:"keys"`
}

// NewJWKSCache creates a new JWKS cache for the given endpoint
func NewJWKSCache(jwksURL string, ttl time.Duration) *JWKSCache {
	return &JWKSCache{
		keys:    make(map[string]interface{}),
		jwksURL: jwksURL,
		ttl:     ttl,
	}
}

// FetchKeys retrieves and caches JWKS keys
func (c *JWKSCache) FetchKeys() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Check if cache is still valid
	if time.Since(c.lastFetch) < c.ttl && len(c.keys) > 0 {
		return nil
	}

	// Fetch JWKS
	resp, err := http.Get(c.jwksURL)
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

	var jwks JWKS
	if err := json.NewDecoder(resp.Body).Decode(&jwks); err != nil {
		return fmt.Errorf("decode JWKS response: %w", err)
	}

	// Convert JWKs to Go crypto keys
	keys := make(map[string]interface{})
	for _, jwk := range jwks.Keys {
		if jwk.Kty != "RSA" {
			continue
		}

		pubKey, err := jwkToRSAPublicKey(jwk)
		if err != nil {
			return fmt.Errorf("convert JWK to RSA key (kid=%s): %w", jwk.Kid, err)
		}

		keys[jwk.Kid] = pubKey
	}

	c.keys = keys
	c.lastFetch = time.Now()
	return nil
}

// GetKey retrieves a key by KID
func (c *JWKSCache) GetKey(kid string) (interface{}, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	key, ok := c.keys[kid]
	if !ok {
		return nil, fmt.Errorf("key not found: %s", kid)
	}
	return key, nil
}

// jwkToRSAPublicKey converts a JWK to an RSA public key
func jwkToRSAPublicKey(jwk JWK) (*rsa.PublicKey, error) {
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
