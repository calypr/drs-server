package authentication

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	conf "github.com/calypr/syfon/client/config"
	"github.com/calypr/syfon/client/logs"
	"github.com/calypr/syfon/client/request"
)

var newBearerTokenRequestor = request.NewBearerTokenRequestor

type tokenAuthResult struct {
	Resources  []string
	Privileges map[string]map[string]bool
	Negative   bool
}

type tokenAuthResolver struct {
	logger   *slog.Logger
	verifier *tokenVerifier
}

func newTokenAuthResolver(logger *slog.Logger) *tokenAuthResolver {
	if logger == nil {
		logger = slog.Default()
	}
	return &tokenAuthResolver{logger: logger, verifier: newTokenVerifier()}
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
	reqClient := newBearerTokenRequestor(gen3Logger, cred, nil, apiEndpoint, "syfon-server", nil)
	privs, err := fetchPrivileges(ctx, reqClient, cred)
	if err != nil {
		r.logger.Debug("failed to check privileges with internal auth", "error", err)
		return tokenAuthResult{Negative: true}
	}
	resources, privileges := extractPrivileges(privs)
	return tokenAuthResult{Resources: resources, Privileges: privileges}
}

func fetchPrivileges(ctx context.Context, reqClient request.Requester, _ *conf.Credential) (map[string]any, error) {
	var data map[string]any
	if err := reqClient.Do(ctx, http.MethodGet, "/user/user", nil, &data); err != nil {
		return nil, fmt.Errorf("request user info: %w", err)
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
