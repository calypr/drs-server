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

func (t *AuthTransport) NewAccessToken(ctx context.Context) error {
	if t.Mode != AuthModeBearer {
		return nil
	}
	if t.Cred == nil || t.Cred.APIKey == "" {
		return errors.New("APIKey is required to refresh access token")
	}
	if strings.TrimSpace(t.Cred.APIEndpoint) == "" {
		return errors.New("APIEndpoint is required to refresh access token")
	}

	refreshClient := &http.Client{Transport: t.Base}
	// ... (rest of NewAccessToken implementation)
	payload := map[string]string{"api_key": t.Cred.APIKey}
	reader, err := common.ToJSONReader(payload)
	if err != nil {
		return err
	}

	refreshUrl := t.Cred.APIEndpoint + common.DataAccessTokenEndpoint
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

	t.mu.Lock()
	t.Cred.AccessToken = result.AccessToken
	if t.Manager != nil {
		t.Manager.Save(t.Cred)
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
	if req.Header.Get("X-Skip-Auth") == "true" {
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
	t.apply(req)
	return t.Base.RoundTrip(req)
}

func (t *AuthTransport) refreshOnce(ctx context.Context) error {
	if t.Mode != AuthModeBearer {
		return nil
	}
	if t.Cred == nil {
		return nil
	}
	if strings.TrimSpace(t.Cred.APIEndpoint) == "" {
		return nil
	}
	t.refreshMu.Lock()
	defer t.refreshMu.Unlock()

	t.mu.RLock()
	if t.Cred.AccessToken != "" {
		t.mu.RUnlock()
		return nil
	}
	t.mu.RUnlock()

	return t.NewAccessToken(ctx)
}
