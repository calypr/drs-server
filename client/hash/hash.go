package hash

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	drsapi "github.com/calypr/syfon/apigen/drs"
)

// ChecksumType represents the digest method used to create the checksum
type ChecksumType string

func (ct ChecksumType) String() string {
	return string(ct)
}

// IANA Named Information Hash Algorithm Registry values and other common types
const (
	ChecksumTypeSHA1     ChecksumType = "sha1"
	ChecksumTypeSHA256   ChecksumType = "sha256"
	ChecksumTypeSHA512   ChecksumType = "sha512"
	ChecksumTypeMD5      ChecksumType = "md5"
	ChecksumTypeETag     ChecksumType = "etag"
	ChecksumTypeCRC32C   ChecksumType = "crc32c"
	ChecksumTypeTrunc512 ChecksumType = "trunc512"
)

type HashInfo struct {
	MD5    string `json:"md5,omitempty"`
	SHA    string `json:"sha,omitempty"`
	SHA256 string `json:"sha256,omitempty"`
	SHA512 string `json:"sha512,omitempty"`
	CRC    string `json:"crc,omitempty"`
	ETag   string `json:"etag,omitempty"`
}

// UnmarshalJSON accepts both the DRS map-based schema (Indexd) and the array-of-checksums schema (GA4GH).
func (h *HashInfo) UnmarshalJSON(data []byte) error {
	if string(data) == "null" {
		*h = HashInfo{}
		return nil
	}

	var mapPayload map[string]string
	if err := json.Unmarshal(data, &mapPayload); err == nil {
		*h = hashInfoFromMap(mapPayload)
		return nil
	}

	var checksumPayload []drsapi.Checksum
	if err := json.Unmarshal(data, &checksumPayload); err == nil {
		*h = ConvertDrsChecksumsToHashInfo(checksumPayload)
		return nil
	}

	return fmt.Errorf("unsupported HashInfo payload: %s", string(data))
}

func hashInfoFromMap(inputHashes map[string]string) HashInfo {
	keys := make([]string, 0, len(inputHashes))
	for key := range inputHashes {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		left := strings.ToLower(strings.TrimSpace(keys[i]))
		right := strings.ToLower(strings.TrimSpace(keys[j]))
		if left != right {
			return left < right
		}
		return keys[i] < keys[j]
	})

	hashInfo := HashInfo{}
	choices := make(map[ChecksumType]checksumChoice)
	for _, key := range keys {
		setHashValue(&hashInfo, choices, key, inputHashes[key])
	}
	return hashInfo
}

func ConvertDrsChecksumsToHashInfo(checksums []drsapi.Checksum) HashInfo {
	result := HashInfo{}
	choices := make(map[ChecksumType]checksumChoice)
	for _, checksum := range checksums {
		setHashValue(&result, choices, checksum.Type, checksum.Checksum)
	}
	return result
}

type checksumChoice struct {
	rawType string
	rank    int
}

func setHashValue(hashInfo *HashInfo, choices map[ChecksumType]checksumChoice, rawType, value string) {
	typ := NormalizeChecksumType(rawType)
	if _, ok := checksumField(typ); !ok {
		return
	}

	candidate := checksumChoice{rawType: rawType, rank: checksumTypeRank(rawType, typ)}
	if previous, ok := choices[typ]; ok && !checksumChoiceWins(candidate, previous) {
		return
	}
	choices[typ] = candidate

	switch typ {
	case ChecksumTypeMD5:
		hashInfo.MD5 = value
	case ChecksumTypeSHA1:
		hashInfo.SHA = value
	case ChecksumTypeSHA256:
		hashInfo.SHA256 = value
	case ChecksumTypeSHA512:
		hashInfo.SHA512 = value
	case ChecksumTypeCRC32C:
		hashInfo.CRC = value
	case ChecksumTypeETag:
		hashInfo.ETag = value
	}
}

func checksumField(typ ChecksumType) (struct{}, bool) {
	switch typ {
	case ChecksumTypeMD5, ChecksumTypeSHA1, ChecksumTypeSHA256, ChecksumTypeSHA512, ChecksumTypeCRC32C, ChecksumTypeETag:
		return struct{}{}, true
	default:
		return struct{}{}, false
	}
}

func checksumTypeRank(raw string, canonical ChecksumType) int {
	trimmed := strings.TrimSpace(raw)
	if trimmed == string(canonical) {
		return 0
	}
	if strings.EqualFold(trimmed, string(canonical)) {
		return 1
	}
	return 2
}

func checksumChoiceWins(candidate, previous checksumChoice) bool {
	if candidate.rank != previous.rank {
		return candidate.rank < previous.rank
	}
	candidateType := strings.ToLower(strings.TrimSpace(candidate.rawType))
	previousType := strings.ToLower(strings.TrimSpace(previous.rawType))
	if candidateType != previousType {
		return candidateType < previousType
	}
	return candidate.rawType < previous.rawType
}

func NormalizeChecksumType(raw string) ChecksumType {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "sha256", "sha-256":
		return ChecksumTypeSHA256
	case "sha512", "sha-512":
		return ChecksumTypeSHA512
	case "sha1", "sha-1", "sha":
		return ChecksumTypeSHA1
	case "md5":
		return ChecksumTypeMD5
	case "etag":
		return ChecksumTypeETag
	case "crc32c":
		return ChecksumTypeCRC32C
	case "trunc512":
		return ChecksumTypeTrunc512
	default:
		return ChecksumType(raw)
	}
}

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

// NormalizeChecksum trims whitespace and an optional sha256: prefix.
func NormalizeChecksum(raw string) string {
	raw = strings.TrimSpace(raw)
	raw = strings.TrimPrefix(raw, "sha256:")
	return strings.TrimSpace(raw)
}
