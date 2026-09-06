package authentication

import (
	"os"
	"strings"
)

type mockConfig struct {
	Enabled           bool
	RequireAuthHeader bool
	Resources         []string
	Methods           []string
}

func loadMockAuthConfigFromEnv() mockConfig {
	enabled := parseBoolEnv("DRS_AUTH_MOCK_ENABLED", false)
	if !enabled {
		return mockConfig{}
	}
	resources := splitCSV(os.Getenv("DRS_AUTH_MOCK_RESOURCES"))
	if len(resources) == 0 {
		resources = []string{"/data_file"}
	}
	methods := splitCSV(os.Getenv("DRS_AUTH_MOCK_METHODS"))
	if len(methods) == 0 {
		methods = []string{"*"}
	}
	return mockConfig{
		Enabled:           true,
		RequireAuthHeader: parseBoolEnv("DRS_AUTH_MOCK_REQUIRE_AUTH_HEADER", false),
		Resources:         resources,
		Methods:           methods,
	}
}

func parseBoolEnv(name string, def bool) bool {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return def
	}
	switch strings.ToLower(raw) {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	default:
		return def
	}
}

func splitCSV(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		out = append(out, p)
	}
	return out
}
