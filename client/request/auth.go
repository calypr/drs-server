package request

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"

	"github.com/calypr/syfon/client/common"
	conf "github.com/calypr/syfon/client/config"
)

type AuthMode string

const (
	AuthModeBasic  AuthMode = "basic"
	AuthModeBearer AuthMode = "bearer"
)

type accessTokenResponse struct {
	AccessToken string `json:"access_token"`
}

type authRequestContextKey struct{}

type authRequestContext struct {
	skipAuth     bool
	explicitAuth bool
}

func (t *AuthTransport) NewAccessToken(ctx context.Context) error {
	if t.Mode != AuthModeBearer {
		return nil
	}
	t.mu.RLock()
	cred := t.Cred
	apiKey := ""
	apiEndpoint := ""
	if cred != nil {
		apiKey = cred.APIKey
		apiEndpoint = strings.TrimSpace(cred.APIEndpoint)
	}
	t.mu.RUnlock()
	if cred == nil || apiKey == "" {
		return errors.New("APIKey is required to refresh access token")
	}
	if apiEndpoint == "" {
		return errors.New("APIEndpoint is required to refresh access token")
	}

	refreshClient := &http.Client{Transport: t.Base}
	payload := map[string]string{"api_key": apiKey}
	reader, err := common.ToJSONReader(payload)
	if err != nil {
		return err
	}

	refreshUrl := strings.TrimRight(apiEndpoint, "/") + common.DataAccessTokenEndpoint
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, refreshUrl, reader)
	if err != nil {
		return err
	}
	req.Header.Set(common.HeaderContentType, common.MIMEApplicationJSON)

	resp, err := refreshClient.Do(req)
	if err != nil {
		return fmt.Errorf("refresh request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, err := io.ReadAll(io.LimitReader(resp.Body, 4096))
		if err != nil {
			return fmt.Errorf("failed to refresh token: read error response body: %w", err)
		}
		bodyText := strings.TrimSpace(string(body))
		if bodyText == "" {
			return fmt.Errorf("failed to refresh token: %s", resp.Status)
		}
		return fmt.Errorf("failed to refresh token: %s body=%s", resp.Status, bodyText)
	}

	var result accessTokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return err
	}
	if strings.TrimSpace(result.AccessToken) == "" {
		return errors.New("refresh response missing access_token")
	}

	t.mu.Lock()
	previousToken := t.Cred.AccessToken
	t.Cred.AccessToken = strings.TrimSpace(result.AccessToken)
	if t.Manager != nil {
		if err := t.Manager.Save(t.Cred); err != nil {
			t.Cred.AccessToken = previousToken
			t.mu.Unlock()
			return fmt.Errorf("save refreshed access token: %w", err)
		}
	}
	t.mu.Unlock()
	return nil
}

type AuthTransport struct {
	Manager   conf.ManagerInterface
	Base      http.RoundTripper
	Cred      *conf.Credential
	Mode      AuthMode
	mu        sync.RWMutex
	refreshMu sync.Mutex
}

func (t *AuthTransport) apply(req *http.Request) {
	skipAuth := req.Header.Get("X-Skip-Auth") == "true"
	if skipAuth {
		req.Header.Del("X-Skip-Auth")
		return
	}
	if req.Header.Get("Authorization") != "" {
		return
	}
	if t.Cred == nil {
		return
	}

	t.mu.RLock()
	defer t.mu.RUnlock()

	switch t.Mode {
	case AuthModeBearer:
		if token := strings.TrimSpace(t.Cred.AccessToken); token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
	case AuthModeBasic:
		if user := strings.TrimSpace(t.Cred.KeyID); user != "" {
			req.SetBasicAuth(user, t.Cred.APIKey)
		}
	}
}

func (t *AuthTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req == nil {
		return nil, errors.New("nil request")
	}
	clone := req.Clone(req.Context())
	clone = clone.WithContext(context.WithValue(clone.Context(), authRequestContextKey{}, authRequestContext{
		skipAuth:     req.Header.Get("X-Skip-Auth") == "true",
		explicitAuth: req.Header.Get("Authorization") != "",
	}))
	t.apply(clone)
	return t.Base.RoundTrip(clone)
}

func (t *AuthTransport) refreshIfCurrent(ctx context.Context, rejectedToken string) error {
	if t.Mode != AuthModeBearer {
		return nil
	}
	t.refreshMu.Lock()
	defer t.refreshMu.Unlock()

	t.mu.RLock()
	if t.Cred == nil {
		t.mu.RUnlock()
		return nil
	}
	currentToken := strings.TrimSpace(t.Cred.AccessToken)
	apiEndpoint := strings.TrimSpace(t.Cred.APIEndpoint)
	t.mu.RUnlock()
	if apiEndpoint == "" || currentToken != rejectedToken {
		return nil
	}

	return t.NewAccessToken(ctx)
}
