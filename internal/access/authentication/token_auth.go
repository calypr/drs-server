package authentication

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"github.com/calypr/syfon/client/apierror"
	conf "github.com/calypr/syfon/client/config"
	"github.com/calypr/syfon/client/logs"
	"github.com/calypr/syfon/client/request"
)

type tokenAuthResult struct {
	Resources  []string
	Privileges map[string]map[string]bool
	Negative   bool
}

type tokenAuthResolver struct {
	logger   *slog.Logger
	verifier *tokenVerifier
}

func newTokenAuthResolver(logger *slog.Logger, fenceURL string) *tokenAuthResolver {
	if logger == nil {
		logger = slog.Default()
	}
	return &tokenAuthResolver{logger: logger, verifier: newTokenVerifier(fenceURL)}
}

func (r *tokenAuthResolver) Resolve(ctx context.Context, tokenString string) tokenAuthResult {
	apiEndpoint, _, err := r.verifier.parseToken(ctx, tokenString)
	if err != nil {
		r.logger.Debug("failed to parse token", "error", err)
		return tokenAuthResult{Negative: true}
	}

	cred := &conf.Credential{
		AccessToken: tokenString,
		APIEndpoint: apiEndpoint,
	}
	gen3Logger := logs.NewGen3Logger(r.logger, "", "syfon")
	httpClient := request.NewClient(gen3Logger, cred, nil, "syfon-server", nil, request.AuthModeBearer)
	privs, err := fetchPrivileges(ctx, httpClient, apiEndpoint)
	if err != nil {
		r.logger.Debug("failed to check privileges with internal auth", "error", err)
		return tokenAuthResult{Negative: true}
	}
	resources, privileges := extractPrivileges(privs)
	return tokenAuthResult{Resources: resources, Privileges: privileges}
}

func fetchPrivileges(ctx context.Context, httpClient request.HTTPDoer, apiEndpoint string) (map[string]any, error) {
	endpoint := strings.TrimRight(strings.TrimSpace(apiEndpoint), "/") + "/user/user"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("create user info request: %w", err)
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request user info: %w", err)
	}
	if resp == nil || resp.Body == nil {
		return nil, fmt.Errorf("request user info: empty response")
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("request user info: read response: %w", err)
	}
	if resp.StatusCode >= http.StatusBadRequest {
		return nil, fmt.Errorf("request user info: %w", apierror.FromResponse(resp, body))
	}
	var data map[string]any
	if err := json.Unmarshal(body, &data); err != nil {
		return nil, fmt.Errorf("request user info: decode response: %w", err)
	}

	resourceAccess, ok := data["authz"].(map[string]any)
	if !ok || len(resourceAccess) == 0 {
		resourceAccess, ok = data["project_access"].(map[string]any)
		if !ok {
			return nil, fmt.Errorf("no authz/project_access found in user response")
		}
	}
	return resourceAccess, nil
}

func extractPrivileges(privs map[string]any) ([]string, map[string]map[string]bool) {
	resources := make([]string, 0, len(privs))
	out := make(map[string]map[string]bool, len(privs))
	for path, raw := range privs {
		resources = append(resources, path)
		methods := map[string]bool{}
		entries, ok := raw.([]any)
		if !ok {
			out[path] = methods
			continue
		}
		for _, entry := range entries {
			mm, ok := entry.(map[string]any)
			if !ok {
				continue
			}
			service, _ := mm["service"].(string)
			method, _ := mm["method"].(string)
			if method == "" {
				continue
			}
			service = strings.ToLower(strings.TrimSpace(service))
			method = strings.ToLower(strings.TrimSpace(method))
			switch service {
			case "", "*", "indexd", "drs":
				methods[method] = true
				if service != "" {
					methods[service+":"+method] = true
				}
			default:
				methods[service+":"+method] = true
			}
		}
		out[path] = methods
	}
	return resources, out
}
