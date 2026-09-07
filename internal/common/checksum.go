package common

import (
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/calypr/syfon/apigen/server/drs"
	sycommon "github.com/calypr/syfon/common"
	"github.com/calypr/syfon/internal/models"
)

var ErrNoValidSHA256 = errors.New("no valid sha256 values provided")
var ErrConflictingSHA256 = errors.New("conflicting sha256 values provided")
var ErrAccessMethodsRequired = errors.New("candidate must include at least one access method with a non-empty url")

var sha256Like = regexp.MustCompile(`^[A-Fa-f0-9]{64}$`)

// LooksLikeSHA256 checks if a string matches the format of a SHA256 hash.
func LooksLikeSHA256(v string) bool {
	return sha256Like.MatchString(strings.TrimSpace(v))
}

// NormalizeChecksum removes any "sha256:" prefixes if present.
func NormalizeChecksum(cs string) string {
	if parts := strings.SplitN(cs, ":", 2); len(parts) == 2 {
		return parts[1]
	}
	return cs
}

// NormalizeChecksumType cleans up a checksum type string (lowercase, remove hyphens).
func NormalizeChecksumType(checksumType string) string {
	normalized := strings.ToLower(strings.TrimSpace(checksumType))
	normalized = strings.ReplaceAll(normalized, "-", "")
	return normalized
}

// ParseHashQuery parses a checksum string that might be in "type:value" format.
func ParseHashQuery(rawHash string, rawType string) (string, string) {
	hashType := NormalizeChecksumType(rawType)
	hashValue := strings.Trim(strings.TrimSpace(NormalizeChecksum(rawHash)), `"'`)
	if hashType == "" {
		if parts := strings.SplitN(strings.Trim(strings.TrimSpace(rawHash), `"'`), ":", 2); len(parts) == 2 {
			hashType = NormalizeChecksumType(parts[0])
		}
	}
	return hashType, hashValue
}

func ObjectHasChecksumTypeAndValue(obj models.InternalObject, hashType string, hashValue string) bool {
	if hashType == "" {
		return true
	}
	targetType := NormalizeChecksumType(hashType)
	targetValue := strings.Trim(strings.TrimSpace(NormalizeChecksum(hashValue)), `"'`)
	if targetType == "" || targetValue == "" {
		return false
	}
	for _, checksum := range obj.Checksums {
		if NormalizeChecksumType(checksum.Type) == targetType && strings.Trim(strings.TrimSpace(NormalizeChecksum(checksum.Checksum)), `"'`) == targetValue {
			return true
		}
	}
	return false
}

// MergeAdditionalChecksums merges new checksums into an existing set, avoiding duplicate types.
func MergeAdditionalChecksums(existing []drs.Checksum, additions []drs.Checksum) []drs.Checksum {
	out := make([]drs.Checksum, 0, len(existing)+len(additions))
	seenTypes := make(map[string]struct{}, len(existing)+len(additions))

	for _, cs := range existing {
		if t := NormalizeChecksumType(cs.Type); t != "" {
			seenTypes[t] = struct{}{}
		}
		out = append(out, cs)
	}

	for _, cs := range additions {
		t := NormalizeChecksumType(cs.Type)
		v := strings.TrimSpace(NormalizeChecksum(cs.Checksum))
		if t == "" || v == "" {
			continue
		}
		if _, exists := seenTypes[t]; exists {
			continue
		}
		out = append(out, drs.Checksum{Type: strings.TrimSpace(cs.Type), Checksum: v})
		seenTypes[t] = struct{}{}
	}
	return out
}

// CanonicalSHA256 pulls the sha256 value from a list of checksums if it exists.
func CanonicalSHA256(checksums []drs.Checksum) (string, bool) {
	values := SHA256Values(checksums)
	if len(values) == 0 {
		return "", false
	}
	return values[0], true
}

// SHA256Values returns distinct valid SHA-256 values in their input order.
// Invalid values remain ignored for compatibility with existing bundle data.
func SHA256Values(checksums []drs.Checksum) []string {
	seen := make(map[string]struct{})
	values := make([]string, 0, 1)
	for _, cs := range checksums {
		checksumType := NormalizeChecksumType(cs.Type)
		if checksumType != "sha256" {
			continue
		}
		normalized := sycommon.NormalizeOid(cs.Checksum)
		if normalized == "" {
			continue
		}
		if _, ok := seen[normalized]; ok {
			continue
		}
		seen[normalized] = struct{}{}
		values = append(values, normalized)
	}
	return values
}

// ValidateCanonicalSHA256 returns the sole valid SHA-256 value, if present.
// Multiple distinct valid values identify an invalid registration.
func ValidateCanonicalSHA256(checksums []drs.Checksum) (string, bool, error) {
	values := SHA256Values(checksums)
	if len(values) > 1 {
		return "", false, fmt.Errorf("%w: %s", ErrConflictingSHA256, strings.Join(values, ", "))
	}
	if len(values) == 0 {
		return "", false, nil
	}
	return values[0], true, nil
}

// NormalizeSHA256Query recognizes a bare or prefixed SHA-256 query. Other
// checksum values are returned unchanged by callers so their exact matching
// semantics remain intact.
func NormalizeSHA256Query(value string) (string, bool) {
	normalized := sycommon.NormalizeOid(value)
	if normalized == "" {
		return "", false
	}
	return normalized, true
}
