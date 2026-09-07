package config

import (
	"os"
	"testing"
)

func TestAuthEnvironmentCompatibility(t *testing.T) {
	cfg := &Config{Auth: AuthConfig{
		Mock: MockAuthConfig{
			Enabled:           true,
			RequireAuthHeader: true,
			Resources:         []string{"/programs/a", "/programs/b"},
			Methods:           []string{"read", "write"},
		},
		LocalAuthzCSV: "authz.csv",
		PluginPaths: PluginPaths{
			Authz: "/plugins/authz",
			Authn: "/plugins/authn",
		},
		FenceURL: "https://fence.example",
	}}

	keys := []string{
		"DRS_AUTH_MOCK_ENABLED",
		"DRS_AUTH_MOCK_REQUIRE_AUTH_HEADER",
		"DRS_AUTH_MOCK_RESOURCES",
		"DRS_AUTH_MOCK_METHODS",
		"DRS_LOCAL_AUTHZ_CSV",
		"SYFON_AUTHZ_PLUGIN_PATH",
		"SYFON_AUTHN_PLUGIN_PATH",
		"DRS_FENCE_URL",
	}
	for _, key := range keys {
		t.Setenv(key, "")
	}

	exportAuthEnvironment(cfg)
	want := map[string]string{
		"DRS_AUTH_MOCK_ENABLED":             "true",
		"DRS_AUTH_MOCK_REQUIRE_AUTH_HEADER": "true",
		"DRS_AUTH_MOCK_RESOURCES":           "/programs/a,/programs/b",
		"DRS_AUTH_MOCK_METHODS":             "read,write",
		"DRS_LOCAL_AUTHZ_CSV":               "authz.csv",
		"SYFON_AUTHZ_PLUGIN_PATH":           "/plugins/authz",
		"SYFON_AUTHN_PLUGIN_PATH":           "/plugins/authn",
		"DRS_FENCE_URL":                     "https://fence.example",
	}
	for key, expected := range want {
		if got := os.Getenv(key); got != expected {
			t.Errorf("%s=%q, want %q", key, got, expected)
		}
	}
	if !isMockAuthEnabledFromEnv() {
		t.Fatal("expected exported mock-auth environment to enable mock auth")
	}
}
