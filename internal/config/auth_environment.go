package config

import (
	"os"
	"strings"
)

func resolveAuthEnvironment(cfg *Config) {
	if cfg == nil {
		return
	}

	cfg.Auth.Mock.Enabled = cfg.Auth.Mock.Enabled || inheritedBool("DRS_AUTH_MOCK_ENABLED")
	cfg.Auth.Mock.RequireAuthHeader = cfg.Auth.Mock.RequireAuthHeader || inheritedBool("DRS_AUTH_MOCK_REQUIRE_AUTH_HEADER")
	if len(cfg.Auth.Mock.Resources) == 0 {
		cfg.Auth.Mock.Resources = splitCSV(os.Getenv("DRS_AUTH_MOCK_RESOURCES"))
	}
	if len(cfg.Auth.Mock.Methods) == 0 {
		cfg.Auth.Mock.Methods = splitCSV(os.Getenv("DRS_AUTH_MOCK_METHODS"))
	}
	if cfg.Auth.PluginPaths.Authz == "" {
		cfg.Auth.PluginPaths.Authz = os.Getenv("SYFON_AUTHZ_PLUGIN_PATH")
	}
	if cfg.Auth.PluginPaths.Authn == "" {
		cfg.Auth.PluginPaths.Authn = os.Getenv("SYFON_AUTHN_PLUGIN_PATH")
	}
	if cfg.Auth.FenceURL == "" {
		cfg.Auth.FenceURL = os.Getenv("DRS_FENCE_URL")
	}
}

func inheritedMockAuthEnabled() bool {
	return inheritedBool("DRS_AUTH_MOCK_ENABLED")
}

func inheritedBool(name string) bool {
	raw := strings.TrimSpace(os.Getenv(name))
	switch strings.ToLower(raw) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func splitCSV(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return out
}
