package hash

import (
	"encoding/json"
	"fmt"

	drsapi "github.com/calypr/syfon/apigen/client/drs"
)

// ChecksumType represents the digest method used to create the checksum
type ChecksumType string

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



// String returns the string representation of the checksum type


var SupportedChecksums = map[string]bool{
	string(ChecksumTypeSHA1):     true,
	string(ChecksumTypeSHA256):   true,
	string(ChecksumTypeSHA512):   true,
	string(ChecksumTypeMD5):      true,
	string(ChecksumTypeETag):     true,
	string(ChecksumTypeCRC32C):   true,
	string(ChecksumTypeTrunc512): true,
}

type Checksum drsapi.Checksum

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
		*h = ConvertStringMapToHashInfo(mapPayload)
		return nil
	}

	var checksumPayload []Checksum
	if err := json.Unmarshal(data, &checksumPayload); err == nil {
		*h = ConvertChecksumsToHashInfo(checksumPayload)
		return nil
	}

	return fmt.Errorf("unsupported HashInfo payload: %s", string(data))
}

func ConvertStringMapToHashInfo(inputHashes map[string]string) HashInfo {
	hashInfo := HashInfo{}

	for key, value := range inputHashes {
		if !SupportedChecksums[key] {
			continue // Disregard unsupported types
		}
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



func ConvertChecksumsToMap(checksums []Checksum) map[string]string {
	result := make(map[string]string, len(checksums))
	for _, c := range checksums {
		result[c.Type] = c.Checksum
	}
	return result
}

func ConvertChecksumsToHashInfo(checksums []Checksum) HashInfo {
	checksumMap := ConvertChecksumsToMap(checksums)
	return ConvertStringMapToHashInfo(checksumMap)
}






