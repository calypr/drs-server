package objects

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"

	clientaccess "github.com/calypr/syfon/client/access"
	"github.com/calypr/syfon/internal/faults"
	"github.com/google/uuid"
)

// AccessMethodID is stable for a type/URL pair and is safe to expose as the
// selector used by the DRS access endpoint.
func AccessMethodID(accessType, accessURL string) string {
	accessType = strings.ToLower(strings.TrimSpace(accessType))
	accessURL = strings.TrimSpace(accessURL)
	digest := sha256.Sum256([]byte(accessType + "\x00" + accessURL))
	return accessType + "-" + hex.EncodeToString(digest[:12])
}

var drsObjectIDNamespace = uuid.NewMD5(uuid.NameSpaceURL, []byte("calypr.org"))

func normalizeSHA256Checksum(raw string) string {
	v := strings.TrimSpace(strings.ToLower(raw))
	return strings.TrimPrefix(v, "sha256:")
}

func canonicalProjectScope(authz []string) (string, error) {
	normalized := clientaccess.NormalizeAccessResources(authz)
	if len(normalized) == 0 {
		return "", fmt.Errorf("%w: project scope is required when object id is not provided", faults.ErrInvalidInput)
	}
	projectScopes := make([]string, 0, len(normalized))
	for _, resource := range normalized {
		org, project, ok := clientaccess.ResourceScope(resource)
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

// MintRecordIDFromChecksum returns a deterministic record ID for a checksum
// and one canonical project scope.
func MintRecordIDFromChecksum(checksum string, authz []string) (RecordID, error) {
	checksum = normalizeSHA256Checksum(checksum)
	if checksum == "" {
		return "", fmt.Errorf("%w: sha256 checksum is required when object id is not provided", faults.ErrInvalidInput)
	}
	scope, err := canonicalProjectScope(authz)
	if err != nil {
		return "", err
	}
	seed := fmt.Sprintf("sha256:%s|%s", checksum, scope)
	return RecordID(uuid.NewSHA1(drsObjectIDNamespace, []byte(seed)).String()), nil
}
