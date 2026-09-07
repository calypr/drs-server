package common

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"

	syfoncommon "github.com/calypr/syfon/common"
	"github.com/calypr/syfon/internal/faults"
	"github.com/google/uuid"
)

// AccessMethodID is the stable public selector for a stored access method.
// The URL is part of the identity so two replicas of the same type cannot
// accidentally resolve to the same selector.
func AccessMethodID(accessType, accessURL string) string {
	accessType = strings.ToLower(strings.TrimSpace(accessType))
	accessURL = strings.TrimSpace(accessURL)
	digest := sha256.Sum256([]byte(accessType + "\x00" + accessURL))
	return accessType + "-" + hex.EncodeToString(digest[:12])
}

const credentialIDHashLength = 12

// DeriveCredentialID returns a stable internal ID derived from the non-secret
// credential identity. SecretKey is intentionally excluded so credential
// rotation does not change the identity.
func DeriveCredentialID(bucket, provider, region, endpoint, accessKey string) string {
	normalizedProvider := NormalizeProvider(provider, S3Provider)
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

var drsObjectIDNamespace = uuid.NewMD5(uuid.NameSpaceURL, []byte("calypr.org"))

func normalizeSHA256Checksum(raw string) string {
	v := strings.TrimSpace(strings.ToLower(raw))
	v = strings.TrimPrefix(v, "sha256:")
	return v
}

func canonicalProjectScope(authz []string) (string, error) {
	normalized := syfoncommon.NormalizeAccessResources(authz)
	if len(normalized) == 0 {
		return "", fmt.Errorf("%w: project scope is required when object id is not provided", faults.ErrInvalidInput)
	}
	projectScopes := make([]string, 0, len(normalized))
	for _, resource := range normalized {
		org, project, ok := syfoncommon.ResourceScope(resource)
		if !ok || strings.TrimSpace(org) == "" || strings.TrimSpace(project) == "" {
			continue
		}
		projectScopes = append(projectScopes, resource)
	}
	if len(projectScopes) == 0 {
		return "", fmt.Errorf("%w: project scope is required when object id is not provided", faults.ErrInvalidInput)
	}
	if len(projectScopes) > 1 {
		return "", fmt.Errorf("%w: exactly one project scope is required when object id is not provided", faults.ErrInvalidInput)
	}
	return projectScopes[0], nil
}

// MintObjectIDFromChecksum returns a deterministic UUID for a checksum and a
// single canonical project scope. The generated UUID is stable across
// instances for the same sha256 and normalized project resource path.
func MintObjectIDFromChecksum(checksum string, authz []string) (string, error) {
	checksum = normalizeSHA256Checksum(checksum)
	if checksum == "" {
		return "", fmt.Errorf("%w: sha256 checksum is required when object id is not provided", faults.ErrInvalidInput)
	}
	scope, err := canonicalProjectScope(authz)
	if err != nil {
		return "", err
	}
	seed := fmt.Sprintf("sha256:%s|%s", checksum, scope)
	return uuid.NewSHA1(drsObjectIDNamespace, []byte(seed)).String(), nil
}
