package authentication

import (
	"log/slog"
	"os"
	"strings"

	"github.com/calypr/syfon/plugin"
)

// Runtime contains authentication mechanisms assembled during server startup
// and evaluates framework-neutral authentication requests.
type Runtime struct {
	logger               *slog.Logger
	authentication       plugin.AuthenticationPlugin
	authorization        plugin.AuthorizationPlugin
	tokenResolver        *tokenAuthResolver
	mock                 mockConfig
	localAuthzError      error
	localAuthzForSubject func(string) ([]string, map[string]map[string]bool, bool)
}

// NewRuntime assembles configured authentication mechanisms and swallows plugin
// startup failures so request handling retains the existing fallback behavior.
func NewRuntime(logger *slog.Logger, mode, basicUser, basicPass string) *Runtime {
	if logger == nil {
		logger = slog.Default()
	}
	var localUsers *localAuthzStore
	runtime := &Runtime{
		logger:        logger,
		mock:          loadMockAuthConfigFromEnv(),
		tokenResolver: newTokenAuthResolver(logger),
	}

	if strings.EqualFold(strings.TrimSpace(mode), "local") {
		localCSV := strings.TrimSpace(os.Getenv("DRS_LOCAL_AUTHZ_CSV"))
		if localCSV != "" {
			users, err := loadLocalAuthzCSV(localCSV)
			if err != nil {
				runtime.localAuthzError = err
				logger.Error("failed to load local authz csv", "path", localCSV, "err", err)
			} else {
				localUsers = users
				runtime.localAuthzForSubject = users.authzForSubject
			}
		}
	}

	if pluginPath := os.Getenv("SYFON_AUTHZ_PLUGIN_PATH"); pluginPath != "" {
		if authorizer, err := newAuthorizationPluginManager(pluginPath); err == nil {
			runtime.authorization = authorizer
		}
	}
	if pluginPath := os.Getenv("SYFON_AUTHN_PLUGIN_PATH"); pluginPath != "" {
		if authenticator, err := newAuthenticationPluginManager(pluginPath); err == nil {
			runtime.authentication = authenticator
		}
	}

	if runtime.authentication == nil {
		switch strings.ToLower(strings.TrimSpace(mode)) {
		case "local":
			runtime.authentication = &localAuthPlugin{
				BasicUser: basicUser,
				BasicPass: basicPass,
				Users:     localUsers,
			}
		case "gen3":
			if !runtime.mock.Enabled {
				runtime.authentication = &gen3AuthPlugin{mockConfig: runtime.mock}
			}
		}
	}

	return runtime
}
