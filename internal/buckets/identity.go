package buckets

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/calypr/syfon/internal/storage/address"
)

const credentialIDHashLength = 12

// DeriveCredentialID returns a stable internal ID derived from the non-secret
// credential identity. SecretKey is intentionally excluded so credential
// rotation does not change the identity.
func DeriveCredentialID(bucket, provider, region, endpoint, accessKey string) string {
	normalizedProvider := address.NormalizeProvider(provider, address.S3Provider)
	normalizedEndpoint := strings.TrimRight(strings.ToLower(strings.TrimSpace(endpoint)), "/")
	normalizedRegion := strings.ToLower(strings.TrimSpace(region))
	normalizedBucket := strings.TrimSpace(bucket)
	normalizedAccessKey := strings.TrimSpace(accessKey)

	material := strings.Join([]string{
		normalizedProvider,
		normalizedEndpoint,
		normalizedBucket,
		normalizedRegion,
		normalizedAccessKey,
	}, "\x00")
	sum := sha256.Sum256([]byte(material))
	return fmt.Sprintf("%s_%s", credentialIDPrefix(normalizedBucket), hex.EncodeToString(sum[:])[:credentialIDHashLength])
}

func credentialIDPrefix(bucket string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(strings.TrimSpace(bucket)) {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r)
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		case b.Len() > 0:
			b.WriteByte('_')
		}
		if b.Len() >= 32 {
			break
		}
	}
	prefix := strings.Trim(b.String(), "_")
	if prefix == "" {
		return "credential"
	}
	return prefix
}
