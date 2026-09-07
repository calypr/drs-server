package objects

import (
	"path/filepath"
	"sort"
	"strings"
)

// CleanToBasename extracts a portable basename from either Windows or Unix
// path syntax.
func CleanToBasename(name string) string {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return ""
	}
	trimmed = strings.ReplaceAll(trimmed, "\\", "/")
	base := filepath.Base(trimmed)
	if base == "." || base == "/" || base == "" {
		base = trimmed
	}
	return base
}

func NormalizeNameAliases(primary string, aliases []string) []string {
	primary = CleanToBasename(primary)
	seen := make(map[string]struct{}, len(aliases)+1)
	out := make([]string, 0, len(aliases))
	for _, alias := range aliases {
		name := CleanToBasename(alias)
		if name == "" || name == primary {
			continue
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}
