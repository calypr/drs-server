package buckets

import (
	"context"
	"net/url"
	"path/filepath"
	"sort"
	"strings"

	"github.com/calypr/syfon/internal/access"
	"github.com/calypr/syfon/internal/storage/address"
)

const readMethod = "read"

// ListVisibleBuckets assembles configured credentials, explicit scopes, and
// object-derived rows. The optional query wins; the fallback is used only when
// that optimization is absent.
func (s *Service) ListVisibleBuckets(ctx context.Context) (map[string]VisibleBucket, error) {
	creds, err := s.ListS3Credentials(ctx)
	if err != nil {
		return nil, err
	}
	if len(creds) == 0 {
		return map[string]VisibleBucket{}, nil
	}

	var rows []VisibilityRow
	filterExplicitScopes := access.IsAuthzEnforced(ctx)
	if s.visibility != nil {
		restrictToResources := access.IsAuthzEnforced(ctx) &&
			!access.HasMethodAccess(ctx, readMethod, []string{"/programs"}) &&
			!access.HasMethodAccess(ctx, readMethod, []string{"/data_file"})
		filterExplicitScopes = restrictToResources
		rows, err = s.visibility.ListBucketVisibilityRows(ctx, access.AuthorizedResources(ctx, readMethod), true, restrictToResources)
	} else {
		if s.fallback == nil {
			return nil, errMissingVisibilitySource
		}
		rows, err = s.fallback(ctx)
	}
	if err != nil {
		return nil, err
	}

	return s.mergeVisibleRows(ctx, creds, rows, filterExplicitScopes)
}

func (s *Service) mergeVisibleRows(ctx context.Context, creds []Credential, rows []VisibilityRow, filterExplicitScopes bool) (map[string]VisibleBucket, error) {
	byCredential := make(map[string]VisibleBucket, len(creds))
	programsSeen := make(map[string]map[string]struct{}, len(creds))
	for _, cred := range creds {
		key := s.credentialIDForCredential(cred)
		byCredential[key] = VisibleBucket{Credential: cred}
		programsSeen[key] = map[string]struct{}{}
	}

	scopes, err := s.ListBucketScopes(ctx)
	if err != nil {
		return nil, err
	}
	explicitScopeOwners := make(map[string]string, len(scopes))
	for _, scope := range scopes {
		credentialID := s.scopeCredentialIDForCredentials(scope, creds)
		entry, exists := byCredential[credentialID]
		if !exists {
			continue
		}
		resource, resourceErr := access.ResourcePath(scope.Organization, scope.ProjectID)
		if resourceErr != nil || strings.TrimSpace(resource) == "" {
			continue
		}
		if filterExplicitScopes && !access.HasMethodAccess(ctx, readMethod, []string{resource}) {
			continue
		}
		if _, seen := programsSeen[credentialID][resource]; seen {
			continue
		}
		explicitScopeOwners[resource] = credentialID
		programsSeen[credentialID][resource] = struct{}{}
		entry.Programs = append(entry.Programs, resource)
		byCredential[credentialID] = entry
	}

	for _, row := range rows {
		credentialID, ok := s.credentialIDForVisibilityRow(row, creds)
		if !ok {
			continue
		}
		entry, exists := byCredential[credentialID]
		if !exists {
			continue
		}
		resource := strings.TrimSpace(row.Resource)
		if resource == "" {
			continue
		}
		if owner, ok := explicitScopeOwners[resource]; ok && owner != credentialID {
			continue
		}
		if _, seen := programsSeen[credentialID][resource]; seen {
			continue
		}
		programsSeen[credentialID][resource] = struct{}{}
		entry.Programs = append(entry.Programs, resource)
		byCredential[credentialID] = entry
	}

	for credentialID, entry := range byCredential {
		sort.Strings(entry.Programs)
		byCredential[credentialID] = entry
	}
	return byCredential, nil
}

// VisibleToCaller reports whether a caller's bucket or credential aliases are
// represented in a visible bucket map.
func VisibleToCaller(visible map[string]VisibleBucket, bucket, credentialID string) bool {
	for key, entry := range visible {
		if strings.EqualFold(strings.TrimSpace(entry.Credential.Bucket), bucket) ||
			strings.EqualFold(strings.TrimSpace(key), credentialID) ||
			strings.EqualFold(strings.TrimSpace(entry.Credential.CredentialID), credentialID) {
			return true
		}
	}
	return false
}

func (s *Service) credentialIDForVisibilityRow(row VisibilityRow, creds []Credential) (string, bool) {
	bucket, ok := bucketForVisibilityRow(row, creds)
	if !ok {
		return "", false
	}
	for _, cred := range creds {
		if strings.TrimSpace(cred.Bucket) == bucket {
			return s.credentialIDForCredential(cred), true
		}
	}
	return bucket, true
}

func bucketForVisibilityRow(row VisibilityRow, creds []Credential) (string, bool) {
	raw := strings.TrimSpace(row.AccessURL)
	if raw == "" {
		return "", false
	}
	if bucket, _, ok := address.ParseS3URL(raw); ok {
		return bucket, true
	}
	scheme := address.SchemeFromURL(raw)
	if provider := address.ProviderFromScheme(scheme); provider == address.GCSProvider || provider == address.AzureProvider {
		if parsed, err := url.Parse(raw); err == nil && strings.TrimSpace(parsed.Host) != "" {
			return strings.TrimSpace(parsed.Host), true
		}
	}

	cleanRaw := filepath.Clean(raw)
	for _, cred := range creds {
		if address.NormalizeProvider(cred.Provider, address.S3Provider) != address.FileProvider {
			continue
		}
		root := strings.TrimSpace(cred.Endpoint)
		if root == "" {
			root = strings.TrimSpace(cred.Bucket)
		}
		if root == "" {
			continue
		}
		cleanRoot := filepath.Clean(root)
		if cleanRaw == cleanRoot || strings.HasPrefix(cleanRaw, cleanRoot+string(filepath.Separator)) {
			return cred.Bucket, true
		}
	}
	return "", false
}
