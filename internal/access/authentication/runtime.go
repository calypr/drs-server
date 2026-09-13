package authentication

import (
	"fmt"
	"log/slog"
	"strings"

	hplugin "github.com/hashicorp/go-plugin"

	"github.com/calypr/syfon/internal/config"
	"github.com/calypr/syfon/plugin"
)

// Runtime contains authentication mechanisms assembled during server startup
// and evaluates framework-neutral authentication requests.
type Runtime struct {
	logger               *slog.Logger
	authentication       plugin.AuthenticationPlugin
	authorization        plugin.AuthorizationPlugin
	pluginProcesses      []*hplugin.Client
	tokenResolver        *tokenAuthResolver
	mock                 config.MockAuthConfig
	localAuthzError      error
	localAuthzForSubject func(string) ([]string, map[string]map[string]bool, bool)
}

// NewRuntime assembles configured authentication mechanisms and swallows plugin
// startup failures so request handling retains the existing fallback behavior.
func NewRuntime(logger *slog.Logger, auth config.AuthConfig) *Runtime {
	runtime, _ := newRuntime(logger, auth, false)
	return runtime
}

func NewRuntimeStrict(logger *slog.Logger, auth config.AuthConfig) (*Runtime, error) {
	return newRuntime(logger, auth, true)
}

func newRuntime(logger *slog.Logger, auth config.AuthConfig, strict bool) (*Runtime, error) {
	if logger == nil {
		logger = slog.Default()
	}
	var localUsers *localAuthzStore
	mock := normalizeMockAuth(auth.Mock)
	runtime := &Runtime{
		logger:        logger,
		mock:          mock,
		tokenResolver: newTokenAuthResolver(logger, auth.FenceURL),
	}
	childEnv := pluginEnvironment(auth)

	if strings.EqualFold(strings.TrimSpace(auth.Mode), "local") {
		localCSV := strings.TrimSpace(auth.LocalAuthzCSV)
		if localCSV != "" {
			users, err := loadLocalAuthzCSV(localCSV)
			if err != nil {
				runtime.localAuthzError = err
				logger.Error("failed to load local authz csv", "path", localCSV, "err", err)
				if strict {
					return nil, fmt.Errorf("failed to load local authz csv %q: %w", localCSV, err)
				}
			} else {
				localUsers = users
				runtime.localAuthzForSubject = users.authzForSubject
			}
		}
	}

	if pluginPath := auth.PluginPaths.Authz; pluginPath != "" {
		authorizer, err := newAuthorizationPluginManager(pluginPath, append([]string(nil), childEnv...))
		if err != nil {
			if strict {
				runtime.Close()
				return nil, fmt.Errorf("failed to start authorization plugin %q: %w", pluginPath, err)
			}
		} else {
			runtime.authorization = authorizer
			runtime.pluginProcesses = append(runtime.pluginProcesses, authorizer.client.client)
		}
	}
	if pluginPath := auth.PluginPaths.Authn; pluginPath != "" {
		authenticator, err := newAuthenticationPluginManager(pluginPath, append([]string(nil), childEnv...))
		if err != nil {
			if strict {
				runtime.Close()
				return nil, fmt.Errorf("failed to start authentication plugin %q: %w", pluginPath, err)
			}
		} else {
			runtime.authentication = authenticator
			runtime.pluginProcesses = append(runtime.pluginProcesses, authenticator.client.client)
		}
	}

	if runtime.authentication == nil {
		switch strings.ToLower(strings.TrimSpace(auth.Mode)) {
		case "local":
			runtime.authentication = &localAuthPlugin{
				BasicUser: auth.Basic.Username,
				BasicPass: auth.Basic.Password,
				Users:     localUsers,
			}
		case "gen3":
			if !runtime.mock.Enabled {
				runtime.authentication = &gen3AuthPlugin{mockConfig: runtime.mock}
			}
		}
	}

	return runtime, nil
}

// Close terminates plugin processes owned by this runtime in reverse startup
// order. Built-in authentication mechanisms do not own external resources.
func (r *Runtime) Close() {
	if r == nil {
		return
	}
	for i := len(r.pluginProcesses) - 1; i >= 0; i-- {
		if process := r.pluginProcesses[i]; process != nil {
			process.Kill()
		}
	}
}

func normalizeMockAuth(mock config.MockAuthConfig) config.MockAuthConfig {
	if !mock.Enabled {
		return config.MockAuthConfig{}
	}
	mock.Resources = normalizeMockList(mock.Resources)
	mock.Methods = normalizeMockList(mock.Methods)
	if len(mock.Resources) == 0 {
		mock.Resources = []string{"/data_file"}
	}
	if len(mock.Methods) == 0 {
		mock.Methods = []string{"*"}
	}
	return mock
}

func normalizeMockList(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			out = append(out, value)
		}
	}
	return out
}
