package authentication

import (
	"encoding/base64"
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

func ParseToken(tokenString string) (endpoint string, exp float64, err error) {
	parser := jwt.NewParser(jwt.WithValidMethods([]string{"RS256", "RS384", "RS512"}))
	var claims jwt.MapClaims

	token, err := parser.ParseWithClaims(tokenString, &claims, func(token *jwt.Token) (interface{}, error) {
		if _, ok := token.Method.(*jwt.SigningMethodRSA); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v (expected RSA)", token.Header["alg"])
		}

		kid, ok := token.Header["kid"].(string)
		if !ok || kid == "" {
			return nil, fmt.Errorf("missing KID in token header")
		}

		iss, ok := claims["iss"].(string)
		if !ok || iss == "" {
			return nil, fmt.Errorf("missing or invalid 'iss' claim in token")
		}

		origin, err := normalizeIssuerOrigin(iss)
		if err != nil {
			return nil, fmt.Errorf("invalid issuer URL: %w", err)
		}

		if !isIssuerAllowed(origin) {
			return nil, fmt.Errorf("issuer %q not in allowed list", iss)
		}

		jwksURL, err := discoverJWKSURL(origin)
		if err != nil {
			return nil, fmt.Errorf("JWKS discovery failed: %w", err)
		}
		if !strings.HasPrefix(jwksURL, "https://") {
			return nil, fmt.Errorf("JWKS endpoint must use HTTPS, got: %s", jwksURL)
		}

		cache := NewJWKSCache(jwksURL, 15*time.Minute)
		if err := cache.FetchKeys(); err != nil {
			return nil, fmt.Errorf("fetch JWKS: %w", err)
		}

		publicKey, err := cache.GetKey(kid)
		if err != nil {
			return nil, fmt.Errorf("key not found in JWKS (kid=%s): %w", kid, err)
		}

		return publicKey, nil
	})

	if err != nil {
		return "", 0, fmt.Errorf("JWT signature verification failed: %w", err)
	}

	if !token.Valid {
		return "", 0, fmt.Errorf("invalid token")
	}

	iss, ok := claims["iss"].(string)
	if !ok || iss == "" {
		return "", 0, fmt.Errorf("missing 'iss' claim")
	}

	origin, err := normalizeIssuerOrigin(iss)
	if err != nil {
		return "", 0, fmt.Errorf("failed to normalize issuer URL: %w", err)
	}
	if !strings.HasPrefix(origin, "https://") {
		return "", 0, fmt.Errorf("issuer URL must use https scheme, got %q", iss)
	}

	endpoint = origin

	exp, _ = claims["exp"].(float64)

	return endpoint, exp, nil
}

// isIssuerAllowed checks if an issuer URL matches the configured fence URL.
func isIssuerAllowed(iss string) bool {
	fenceURL := strings.TrimSpace(os.Getenv("DRS_FENCE_URL"))
	if fenceURL == "" {
		return false
	}
	// Must be a valid https:// URL
	u, err := url.Parse(fenceURL)
	if err != nil || u.Scheme != "https" || u.Host == "" {
		return false
	}
	allowedOrigin, err := normalizeIssuerOrigin(fenceURL)
	if err != nil {
		return false
	}
	issuerOrigin, err := normalizeIssuerOrigin(iss)
	if err != nil {
		return false
	}
	return issuerOrigin == allowedOrigin
}

func normalizeIssuerOrigin(raw string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return "", err
	}
	if u.Scheme == "" || u.Host == "" {
		return "", fmt.Errorf("issuer must include scheme and host")
	}
	return strings.ToLower(u.Scheme) + "://" + strings.ToLower(u.Host), nil
}

func ExtractBearerLikeToken(authHeader string) (string, error) {
	trimmed := strings.TrimSpace(authHeader)
	if strings.HasPrefix(strings.ToLower(trimmed), "bearer ") {
		token := strings.TrimSpace(trimmed[len("Bearer "):])
		if token == "" {
			return "", fmt.Errorf("empty bearer token")
		}
		return token, nil
	}
	if strings.HasPrefix(strings.ToLower(trimmed), "basic ") {
		payload := strings.TrimSpace(trimmed[len("Basic "):])
		if payload == "" {
			return "", fmt.Errorf("empty basic auth payload")
		}
		decoded, err := base64.StdEncoding.DecodeString(payload)
		if err != nil {
			return "", fmt.Errorf("invalid basic auth payload: %w", err)
		}
		parts := strings.SplitN(string(decoded), ":", 2)
		if len(parts) != 2 {
			return "", fmt.Errorf("malformed basic auth credentials")
		}
		token := strings.TrimSpace(parts[1])
		if token == "" {
			return "", fmt.Errorf("empty basic auth password token")
		}
		return token, nil
	}
	return "", fmt.Errorf("unsupported authorization scheme")
}
