package config

import (
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

func (man *Manager) IsTokenValid(tokenStr string) (bool, error) {
	if tokenStr == "" {
		return false, fmt.Errorf("token is empty")
	}
	// Parse the token without verifying the signature to access the claims.
	token, _, err := new(jwt.Parser).ParseUnverified(tokenStr, jwt.MapClaims{})
	if err != nil {
		return false, fmt.Errorf("invalid token format: %v", err)
	}

	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok {
		return false, fmt.Errorf("unable to parse claims from provided token")
	}

	exp, ok := claims["exp"].(float64)
	if !ok {
		return false, fmt.Errorf("'exp' claim not found or is not a number")
	}

	iat, ok := claims["iat"].(float64)
	if !ok {
		// iat is not strictly required for validity in all cases, but we'll keep it for now as per original code
		return false, fmt.Errorf("'iat' claim not found or is not a number")
	}

	now := time.Now().UTC()
	expTime := time.Unix(int64(exp), 0).UTC()
	iatTime := time.Unix(int64(iat), 0).UTC()

	if expTime.Before(now) {
		return false, fmt.Errorf("token expired at %s (now: %s)", expTime.Format(time.RFC3339), now.Format(time.RFC3339))
	}
	if iatTime.After(now) {
		return false, fmt.Errorf("token not yet valid: iat %s > now %s", iatTime.Format(time.RFC3339), now.Format(time.RFC3339))
	}

	delta := expTime.Sub(now)
	// threshold days set to 10
	if delta > 0 && delta.Hours() < float64(10*24) {
		daysUntilExpiration := int(delta.Hours() / 24)
		if daysUntilExpiration > 0 && man.Logger != nil {
			man.Logger.Warn(fmt.Sprintf("Token will expire in %d days, on %s", daysUntilExpiration, expTime.Format(time.RFC3339)))
		}
	}

	return true, nil
}

func (man *Manager) IsCredentialValid(profileConfig *Credential) (bool, error) {
	if profileConfig == nil {
		return false, fmt.Errorf("profileConfig is nil")
	}

	accessTokenValid, accessErr := man.IsTokenValid(profileConfig.AccessToken)
	apiKeyValid, apiErr := man.IsTokenValid(profileConfig.APIKey)

	if !accessTokenValid && !apiKeyValid {
		return false, fmt.Errorf("both access_token and api_key are invalid: %v; %v", accessErr, apiErr)
	}

	if !accessTokenValid && apiKeyValid {
		return false, fmt.Errorf("access_token is invalid but api_key is valid: %v", accessErr)
	}

	return true, nil
}
