package authentication

import (
	"log/slog"
	"os"
	"strings"

	"github.com/calypr/syfon/plugin"
)

// Runtime contains authentication mechanisms assembled during server startup.
// The HTTP layer receives its individual dependencies through its own options.
type Runtime struct {
	Authentication       plugin.AuthenticationPlugin
	Authorization        plugin.AuthorizationPlugin
	TokenResolver        *TokenAuthResolver
	Mock                 MockConfig
	LocalAuthzError      error
	LocalAuthzForSubject func(string) ([]string, map[string]map[string]bool, bool)
}

// NewRuntime assembles configured authentication mechanisms and swallows plugin
// startup failures so request handling retains the existing fallback behavior.
func NewRuntime(logger *slog.Logger, mode, basicUser, basicPass string) *Runtime {
	if logger == nil {
		logger = slog.Default()
	}
	var localUsers *LocalAuthzStore
	runtime := &Runtime{
		Mock:          LoadMockAuthConfigFromEnv(),
		TokenResolver: NewTokenAuthResolver(logger),
	}

	if strings.EqualFold(strings.TrimSpace(mode), "local") {
		localCSV := strings.TrimSpace(os.Getenv("DRS_LOCAL_AUTHZ_CSV"))
		if localCSV != "" {
			users, err := LoadLocalAuthzCSV(localCSV)
			if err != nil {
				runtime.LocalAuthzError = err
				logger.Error("failed to load local authz csv", "path", localCSV, "err", err)
			} else {
				localUsers = users
				runtime.LocalAuthzForSubject = users.AuthzForSubject
			}
		}
	}

	if pluginPath := os.Getenv("SYFON_AUTHZ_PLUGIN_PATH"); pluginPath != "" {
		if authorizer, err := NewAuthorizationPluginManager(pluginPath); err == nil {
			runtime.Authorization = authorizer
		}
	}
	if pluginPath := os.Getenv("SYFON_AUTHN_PLUGIN_PATH"); pluginPath != "" {
		if authenticator, err := NewAuthenticationPluginManager(pluginPath); err == nil {
			runtime.Authentication = authenticator
		}
	}

	if runtime.Authentication == nil {
		switch strings.ToLower(strings.TrimSpace(mode)) {
		case "local":
			runtime.Authentication = &LocalAuthPlugin{
				BasicUser: basicUser,
				BasicPass: basicPass,
				Users:     localUsers,
			}
		case "gen3":
			if !runtime.Mock.Enabled {
				runtime.Authentication = &Gen3AuthPlugin{MockConfig: runtime.Mock}
			}
		}
	}

	return runtime
}
