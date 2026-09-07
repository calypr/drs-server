package objects

import (
	"errors"
	"fmt"
	"regexp"
	"strings"

	syfoncommon "github.com/calypr/syfon/common"
)

var ErrNoValidSHA256 = errors.New("no valid sha256 values provided")
var ErrConflictingSHA256 = errors.New("conflicting sha256 values provided")
var ErrAccessMethodsRequired = errors.New("candidate must include at least one access method with a non-empty url")

var sha256Like = regexp.MustCompile(`^[A-Fa-f0-9]{64}$`)

func LooksLikeSHA256(v string) bool { return sha256Like.MatchString(strings.TrimSpace(v)) }

func NormalizeChecksum(cs string) string {
	if parts := strings.SplitN(cs, ":", 2); len(parts) == 2 {
		return parts[1]
	}
	return cs
}

func NormalizeChecksumType(checksumType string) string {
	normalized := strings.ToLower(strings.TrimSpace(checksumType))
	return strings.ReplaceAll(normalized, "-", "")
}

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

func RecordHasChecksumTypeAndValue(obj Record, hashType, hashValue string) bool {
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

func MergeAdditionalChecksums(existing, additions []Checksum) []Checksum {
	out := make([]Checksum, 0, len(existing)+len(additions))
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
		out = append(out, Checksum{Type: strings.TrimSpace(cs.Type), Checksum: v})
		seenTypes[t] = struct{}{}
	}
	return out
}

func CanonicalSHA256(checksums []Checksum) (string, bool) {
	values := SHA256Values(checksums)
	if len(values) == 0 {
		return "", false
	}
	return values[0], true
}

func SHA256Values(checksums []Checksum) []string {
	seen := make(map[string]struct{})
	values := make([]string, 0, 1)
	for _, cs := range checksums {
		if NormalizeChecksumType(cs.Type) != "sha256" {
			continue
		}
		normalized := syfoncommon.NormalizeOid(cs.Checksum)
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

func ValidateCanonicalSHA256(checksums []Checksum) (string, bool, error) {
	values := SHA256Values(checksums)
	if len(values) > 1 {
		return "", false, fmt.Errorf("%w: %s", ErrConflictingSHA256, strings.Join(values, ", "))
	}
	if len(values) == 0 {
		return "", false, nil
	}
	return values[0], true, nil
}

func NormalizeSHA256Query(value string) (string, bool) {
	normalized := syfoncommon.NormalizeOid(value)
	if normalized == "" {
		return "", false
	}
	return normalized, true
}
