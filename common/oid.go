package common

import "strings"

// NormalizeOid strips an optional "sha256:" prefix, lowercases, and validates
// that the result is a 64-character hex string. Returns "" for invalid input.
func NormalizeOid(oid string) string {
	v := strings.TrimSpace(strings.ToLower(oid))
	v = strings.TrimPrefix(v, "sha256:")
	if len(v) != 64 {
		return ""
	}
	for _, ch := range v {
		if (ch < '0' || ch > '9') && (ch < 'a' || ch > 'f') {
			return ""
		}
	}
	return v
}
