package authentication

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/golang-jwt/jwt/v5"
)

// Test CRIT-1 fix: Issuer allowlist validation
func TestParseToken_IssuerAllowlistValidation(t *testing.T) {
	oldTransport := http.DefaultTransport
	http.DefaultTransport = &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}
	defer func() { http.DefaultTransport = oldTransport }()

	privKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate rsa key: %v", err)
	}

	kid := "test-kid"
	jwksBody := map[string]any{
		"keys": []map[string]any{
			{
				"kty": "RSA",
				"use": "sig",
				"kid": kid,
				"n":   base64.RawURLEncoding.EncodeToString(privKey.PublicKey.N.Bytes()),
				"e":   base64.RawURLEncoding.EncodeToString(big.NewInt(int64(privKey.PublicKey.E)).Bytes()),
			},
		},
	}

	httpsJWKS := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/.well-known/jwks.json" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(jwksBody)
	}))
	defer httpsJWKS.Close()

	httpJWKS := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/.well-known/jwks.json" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(jwksBody)
	}))
	defer httpJWKS.Close()

	buildTokenWithClaims := func(claims jwt.MapClaims) string {
		tok := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
		tok.Header["kid"] = kid
		s, signErr := tok.SignedString(privKey)
		if signErr != nil {
			t.Fatalf("sign token with claims: %v", signErr)
		}
		return s
	}

	httpsOrigin := httpsJWKS.URL
	httpOrigin := httpJWKS.URL

	tests := []struct {
		name           string
		allowedIssuers string
		issuerClaim    string
		expUnix        int64
		wantErr        bool
		errContains    string
		wantEndpoint   string
	}{
		{
			name:           "valid issuer with path in token matches allowlist origin",
			allowedIssuers: httpsOrigin,
			issuerClaim:    httpsOrigin + "/user",
			expUnix:        1893456000,
			wantEndpoint:   httpsOrigin,
		},
		{
			name:           "issuer not in allowlist",
			allowedIssuers: httpsOrigin,
			issuerClaim:    "https://attacker.example.com/user",
			expUnix:        1893456000,
			wantErr:        true,
			errContains:    "not in allowed list",
		},
		{
			name:           "http scheme rejected by jwks https enforcement",
			allowedIssuers: httpOrigin,
			issuerClaim:    httpOrigin + "/user",
			expUnix:        1893456000,
			wantErr:        true,
			errContains:    "not in allowed list",
		},
		{
			name:           "empty allowlist rejects all",
			allowedIssuers: "",
			issuerClaim:    httpsOrigin,
			expUnix:        1893456000,
			wantErr:        true,
			errContains:    "not in allowed list",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			oldEnv := os.Getenv("DRS_FENCE_URL")
			defer func() {
				if oldEnv == "" {
					_ = os.Unsetenv("DRS_FENCE_URL")
					return
				}
				_ = os.Setenv("DRS_FENCE_URL", oldEnv)
			}()
			_ = os.Setenv("DRS_FENCE_URL", tt.allowedIssuers)

			tokenString := buildTokenWithClaims(jwt.MapClaims{
				"iss": tt.issuerClaim,
				"exp": tt.expUnix,
			})
			endpoint, exp, parseErr := ParseToken(tokenString)

			if tt.wantErr {
				if parseErr == nil {
					t.Fatalf("expected error, got nil")
				}
				if tt.errContains != "" && !strings.Contains(parseErr.Error(), tt.errContains) {
					t.Fatalf("error %q does not contain %q", parseErr.Error(), tt.errContains)
				}
				return
			}

			if parseErr != nil {
				t.Fatalf("unexpected parseToken error: %v", parseErr)
			}
			if endpoint != tt.wantEndpoint {
				t.Fatalf("endpoint = %q, want %q", endpoint, tt.wantEndpoint)
			}
			if exp != float64(tt.expUnix) {
				t.Fatalf("exp = %v, want %v", exp, float64(tt.expUnix))
			}
		})
	}

	t.Run("malformed iss claim rejects token", func(t *testing.T) {
		oldEnv := os.Getenv("DRS_FENCE_URL")
		defer func() {
			if oldEnv == "" {
				_ = os.Unsetenv("DRS_FENCE_URL")
				return
			}
			_ = os.Setenv("DRS_FENCE_URL", oldEnv)
		}()
		_ = os.Setenv("DRS_FENCE_URL", httpsOrigin)

		tokenString := buildTokenWithClaims(jwt.MapClaims{
			"iss": 12345,
			"exp": int64(1893456000),
		})

		endpoint, exp, parseErr := ParseToken(tokenString)
		if parseErr == nil {
			t.Fatalf("expected error for malformed iss, got nil")
		}
		if !strings.Contains(parseErr.Error(), "missing or invalid 'iss' claim") {
			t.Fatalf("error %q does not contain malformed iss message", parseErr.Error())
		}
		if endpoint != "" {
			t.Fatalf("endpoint = %q, want empty", endpoint)
		}
		if exp != 0 {
			t.Fatalf("exp = %v, want 0", exp)
		}
	})
}

func TestIsIssuerAllowed(t *testing.T) {
	tests := []struct {
		name          string
		allowedIssuer string
		testIssuer    string
		want          bool
	}{
		{
			name:          "exact match",
			allowedIssuer: "https://fence.example.com",
			testIssuer:    "https://fence.example.com",
			want:          true,
		},
		{
			name:          "no match",
			allowedIssuer: "https://fence.example.com",
			testIssuer:    "https://other.example.com",
			want:          false,
		},
		{
			name:          "empty allowlist",
			allowedIssuer: "",
			testIssuer:    "https://fence.example.com",
			want:          false,
		},
		{
			name:          "issuer path matches allowlist origin",
			allowedIssuer: "https://fence.example.com",
			testIssuer:    "https://fence.example.com/user",
			want:          true,
		},
		{
			name:          "allowlist path matches issuer origin",
			allowedIssuer: "https://fence.example.com/user",
			testIssuer:    "https://fence.example.com",
			want:          true,
		},
		{
			name:          "trailing slash ignored for origin match",
			allowedIssuer: "https://fence.example.com/",
			testIssuer:    "https://fence.example.com",
			want:          true,
		},
		{
			name:          "query and fragment ignored for origin match",
			allowedIssuer: "https://fence.example.com",
			testIssuer:    "https://fence.example.com/user?x=1#frag",
			want:          true,
		},
		{
			name:          "host case-insensitive match",
			allowedIssuer: "https://FENCE.EXAMPLE.COM",
			testIssuer:    "https://fence.example.com/user",
			want:          true,
		},
		{
			name:          "different host still rejected",
			allowedIssuer: "https://fence.example.com",
			testIssuer:    "https://evil.example.com/user",
			want:          false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			oldEnv := os.Getenv("DRS_FENCE_URL")
			if err := os.Setenv("DRS_FENCE_URL", tt.allowedIssuer); err != nil {
				t.Fatalf("Setenv failed: %v", err)
			}
			defer func() {
				if oldEnv != "" {
					if err := os.Setenv("DRS_FENCE_URL", oldEnv); err != nil {
						t.Fatalf("Setenv restore failed: %v", err)
					}
				} else {
					if err := os.Unsetenv("DRS_FENCE_URL"); err != nil {
						t.Fatalf("Unsetenv failed: %v", err)
					}
				}
			}()
			got := isIssuerAllowed(tt.testIssuer)
			if got != tt.want {
				t.Errorf("isIssuerAllowed(%q) = %v, want %v", tt.testIssuer, got, tt.want)
			}
		})
	}
}
