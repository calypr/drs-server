package hash

import (
	"encoding/json"
	"fmt"
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
	hashInfo := HashInfo{}

	for key, value := range inputHashes {
		switch key {
		case string(ChecksumTypeMD5):
			hashInfo.MD5 = value
		case string(ChecksumTypeSHA1):
			hashInfo.SHA = value
		case string(ChecksumTypeSHA256):
			hashInfo.SHA256 = value
		case string(ChecksumTypeSHA512):
			hashInfo.SHA512 = value
		case string(ChecksumTypeCRC32C):
			hashInfo.CRC = value
		case string(ChecksumTypeETag):
			hashInfo.ETag = value
		}
	}

	return hashInfo
}

func ConvertDrsChecksumsToHashInfo(checksums []drsapi.Checksum) HashInfo {
	result := make(map[string]string, len(checksums))
	for _, checksum := range checksums {
		result[checksum.Type] = checksum.Checksum
	}
	return hashInfoFromMap(result)
}

func NormalizeChecksumType(raw string) ChecksumType {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "sha256":
		return ChecksumTypeSHA256
	case "sha512":
		return ChecksumTypeSHA512
	case "sha1", "sha":
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
