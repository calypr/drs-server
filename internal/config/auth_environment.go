package config

import (
	"os"
	"strings"
)

func exportAuthEnvironment(cfg *Config) {
	// 4. Override with Auth.Mock config if set
	if cfg.Auth.Mock.Enabled {
		os.Setenv("DRS_AUTH_MOCK_ENABLED", "true")
	}
	if cfg.Auth.Mock.RequireAuthHeader {
		os.Setenv("DRS_AUTH_MOCK_REQUIRE_AUTH_HEADER", "true")
	}
	if len(cfg.Auth.Mock.Resources) > 0 {
		os.Setenv("DRS_AUTH_MOCK_RESOURCES", strings.Join(cfg.Auth.Mock.Resources, ","))
	}
	if len(cfg.Auth.Mock.Methods) > 0 {
		os.Setenv("DRS_AUTH_MOCK_METHODS", strings.Join(cfg.Auth.Mock.Methods, ","))
	}
	if cfg.Auth.LocalAuthzCSV != "" {
		os.Setenv("DRS_LOCAL_AUTHZ_CSV", cfg.Auth.LocalAuthzCSV)
	}
	// Plugin paths
	if cfg.Auth.PluginPaths.Authz != "" {
		os.Setenv("SYFON_AUTHZ_PLUGIN_PATH", cfg.Auth.PluginPaths.Authz)
	}
	if cfg.Auth.PluginPaths.Authn != "" {
		os.Setenv("SYFON_AUTHN_PLUGIN_PATH", cfg.Auth.PluginPaths.Authn)
	}
	// Fence URL
	if cfg.Auth.FenceURL != "" {
		os.Setenv("DRS_FENCE_URL", cfg.Auth.FenceURL)
	}
}

func isMockAuthEnabledFromEnv() bool {
	raw := strings.TrimSpace(os.Getenv("DRS_AUTH_MOCK_ENABLED"))
	switch strings.ToLower(raw) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}
