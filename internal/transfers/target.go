package transfers

import (
	"context"
	"fmt"
	"net/url"
	"path"
	"sort"
	"strings"

	"github.com/calypr/syfon/internal/buckets"
	"github.com/calypr/syfon/internal/faults"
	"github.com/calypr/syfon/internal/objects"
	"github.com/calypr/syfon/internal/storage/address"
)

// CanonicalStorageTargetRequest describes the object-backed target selection
// used by upload signing and by repairable logical download URLs.
type CanonicalStorageTargetRequest struct {
	Object         *objects.Record
	AccessURL      string
	Bucket         string
	Key            string
	PreferChecksum bool
}

// CanonicalStorageTarget is a provider-neutral target. URL is the canonical
// s3:// representation for bucket/key targets; non-S3 locations are returned
// with only URL populated by ResolveCanonicalStorageTarget.
type CanonicalStorageTarget struct {
	Bucket string
	Key    string
	URL    string
}

// ResolveCanonicalStorageTarget selects the physical target for an object.
// Scoped objects use the last non-empty bucket from their deterministic scope
// order and compose nested path prefixes. Unscoped non-S3 URLs pass through.
func (s *Service) ResolveCanonicalStorageTarget(ctx context.Context, req CanonicalStorageTargetRequest) (CanonicalStorageTarget, error) {
	obj := req.Object
	if obj == nil {
		return CanonicalStorageTarget{}, fmt.Errorf("object is required")
	}

	scopes, err := s.bucketScopesForObject(ctx, obj)
	if err != nil {
		return CanonicalStorageTarget{}, err
	}

	existingURL := strings.TrimSpace(req.AccessURL)
	if existingURL == "" {
		existingURL = firstSupportedAccessURL(obj)
	}
	existingBucket, existingKey, existingOK := parseS3Location(existingURL)

	if len(scopes) > 0 {
		targetBucket := ""
		for _, scope := range scopes {
			if strings.TrimSpace(scope.Bucket) != "" {
				targetBucket = strings.TrimSpace(scope.Bucket)
			}
		}
		if targetBucket == "" {
			return CanonicalStorageTarget{}, fmt.Errorf("unable to resolve scoped storage bucket for object %s", string(obj.Id))
		}
		targetKey := canonicalObjectKey(obj, req.Key, existingKey, req.PreferChecksum)
		if existingOK && strings.EqualFold(strings.TrimSpace(existingBucket), targetBucket) && len(normalizedScopePrefixes(scopes)) == 0 && strings.TrimSpace(existingKey) != "" {
			targetKey = existingKey
		}
		targetKey = normalizeScopedStorageKey(targetKey, scopes)
		if strings.TrimSpace(targetKey) == "" {
			return CanonicalStorageTarget{}, fmt.Errorf("unable to resolve scoped storage key for object %s", string(obj.Id))
		}
		return newCanonicalStorageTarget(targetBucket, targetKey), nil
	}

	if strings.TrimSpace(existingURL) == "" {
		return CanonicalStorageTarget{}, fmt.Errorf("%w: object storage location is unavailable", faults.ErrInvalidInput)
	}
	if existingOK {
		if strings.TrimSpace(existingBucket) == "" || strings.TrimSpace(existingKey) == "" {
			return CanonicalStorageTarget{}, fmt.Errorf("%w: object storage location is invalid", faults.ErrInvalidInput)
		}
		return newCanonicalStorageTarget(existingBucket, existingKey), nil
	}
	return CanonicalStorageTarget{URL: existingURL}, nil
}

// ResolveScopedUploadTarget resolves an upload resource into its physical
// bucket/key. Organization and project scopes compose in lookup order.
func (s *Service) ResolveScopedUploadTarget(ctx context.Context, organization, project, key string) (CanonicalStorageTarget, error) {
	organization = strings.TrimSpace(organization)
	project = strings.TrimSpace(project)
	key = strings.Trim(strings.TrimSpace(key), "/")
	if organization == "" {
		return CanonicalStorageTarget{}, fmt.Errorf("%w: organization is required", faults.ErrInvalidInput)
	}
	if project != "" && organization == "" {
		return CanonicalStorageTarget{}, fmt.Errorf("%w: organization required when project is specified", faults.ErrInvalidInput)
	}

	scopes := make([]buckets.Scope, 0, 2)
	if s.scopes != nil {
		if scope, found, err := s.scopes.LookupBucketScope(ctx, organization, ""); err != nil {
			return CanonicalStorageTarget{}, err
		} else if found {
			scopes = append(scopes, scope)
		}
		if project != "" {
			if scope, found, err := s.scopes.LookupBucketScope(ctx, organization, project); err != nil {
				return CanonicalStorageTarget{}, err
			} else if found {
				scopes = append(scopes, scope)
			}
		}
	}
	if len(scopes) == 0 {
		if project != "" {
			return CanonicalStorageTarget{}, fmt.Errorf("%w: no bucket scope configured for organization %q project %q", faults.ErrInvalidInput, organization, project)
		}
		return CanonicalStorageTarget{}, fmt.Errorf("%w: no bucket scope configured for organization %q", faults.ErrInvalidInput, organization)
	}

	bucket := ""
	for _, scope := range scopes {
		if strings.TrimSpace(scope.Bucket) != "" {
			bucket = strings.TrimSpace(scope.Bucket)
		}
	}
	if bucket == "" {
		return CanonicalStorageTarget{}, fmt.Errorf("%w: unable to resolve scoped storage bucket for organization %q project %q", faults.ErrInvalidInput, organization, project)
	}
	key = normalizeScopedStorageKey(key, scopes)
	if key == "" {
		return CanonicalStorageTarget{}, fmt.Errorf("%w: unable to resolve scoped storage key for organization %q project %q", faults.ErrInvalidInput, organization, project)
	}
	return newCanonicalStorageTarget(bucket, key), nil
}

func (s *Service) bucketScopesForObject(ctx context.Context, obj *objects.Record) ([]buckets.Scope, error) {
	if obj == nil || s.scopes == nil {
		return nil, nil
	}
	resources := objects.AccessResources(obj)
	if len(resources) == 0 {
		return nil, nil
	}
	orgProjects := make(map[string][]string)
	for _, resource := range resources {
		organization, project, ok := parseResourceScope(resource)
		if !ok {
			continue
		}
		orgProjects[organization] = append(orgProjects[organization], project)
	}
	organizations := make([]string, 0, len(orgProjects))
	for organization := range orgProjects {
		organizations = append(organizations, organization)
	}
	sort.Strings(organizations)
	scopes := make([]buckets.Scope, 0, len(resources)*2)
	for _, organization := range organizations {
		if scope, found, err := s.scopes.LookupBucketScope(ctx, organization, ""); err != nil {
			return nil, err
		} else if found {
			scopes = append(scopes, scope)
		}
		projects := append([]string(nil), orgProjects[organization]...)
		sort.Strings(projects)
		for _, project := range projects {
			if project == "" {
				continue
			}
			scope, found, err := s.scopes.LookupBucketScope(ctx, organization, project)
			if err != nil {
				return nil, err
			}
			if found {
				scopes = append(scopes, scope)
			}
		}
	}
	return scopes, nil
}

func canonicalObjectKey(obj *objects.Record, explicitKey, existingKey string, preferChecksum bool) string {
	explicitKey = strings.Trim(strings.TrimSpace(explicitKey), "/")
	if explicitKey != "" {
		return explicitKey
	}
	checksum := ""
	if sha, ok := objects.CanonicalSHA256(obj.Checksums); ok {
		checksum = strings.Trim(strings.TrimSpace(sha), "/")
	}
	existingKey = strings.Trim(strings.TrimSpace(existingKey), "/")
	if preferChecksum {
		if checksum != "" {
			return checksum
		}
		if existingKey != "" {
			return existingKey
		}
	} else {
		if existingKey != "" {
			return existingKey
		}
		if checksum != "" {
			return checksum
		}
	}
	return strings.Trim(strings.TrimSpace(string(obj.Id)), "/")
}

func newCanonicalStorageTarget(bucket, key string) CanonicalStorageTarget {
	bucket = strings.TrimSpace(bucket)
	key = strings.Trim(strings.TrimSpace(key), "/")
	return CanonicalStorageTarget{Bucket: bucket, Key: key, URL: address.BucketToURL(bucket, key)}
}

func firstSupportedAccessURL(obj *objects.Record) string {
	if obj == nil || obj.AccessMethods == nil {
		return ""
	}
	for _, method := range *obj.AccessMethods {
		if method.AccessUrl == nil || strings.TrimSpace(method.AccessUrl.Url) == "" {
			continue
		}
		scheme := address.SchemeFromURL(method.AccessUrl.Url)
		if scheme != "" && address.ProviderFromScheme(scheme) == "" {
			continue
		}
		return method.AccessUrl.Url
	}
	return ""
}

func parseS3Location(accessURL string) (bucket, key string, ok bool) {
	if bucket, key, ok := address.ParseS3URL(accessURL); ok {
		return bucket, key, true
	}
	parsed, err := url.Parse(strings.TrimSpace(accessURL))
	if err != nil || !strings.EqualFold(strings.TrimSpace(parsed.Scheme), "s3") {
		return "", "", false
	}
	return strings.TrimSpace(parsed.Host), strings.Trim(strings.TrimSpace(parsed.Path), "/"), true
}

func normalizeScopedStorageKey(key string, scopes []buckets.Scope) string {
	key = strings.Trim(strings.TrimSpace(key), "/")
	prefixes := normalizedScopePrefixes(scopes)
	remainder := key
	for _, prefix := range prefixes {
		remainder = trimLeadingStoragePrefix(remainder, prefix)
	}
	composedPrefix := strings.Join(prefixes, "/")
	switch {
	case composedPrefix == "":
		return remainder
	case remainder == "":
		return composedPrefix
	default:
		return path.Join(composedPrefix, remainder)
	}
}

func normalizedScopePrefixes(scopes []buckets.Scope) []string {
	prefixes := make([]string, 0, len(scopes))
	for _, scope := range scopes {
		prefix := strings.Trim(strings.TrimSpace(scope.PathPrefix), "/")
		if prefix == "" {
			continue
		}
		if len(prefixes) == 0 {
			prefixes = append(prefixes, prefix)
			continue
		}
		last := prefixes[len(prefixes)-1]
		switch {
		case prefix == last:
			continue
		case strings.HasPrefix(prefix, last+"/"):
			prefixes[len(prefixes)-1] = prefix
		case strings.HasPrefix(last, prefix+"/"):
			continue
		default:
			prefixes = append(prefixes, prefix)
		}
	}
	return prefixes
}

func trimLeadingStoragePrefix(key, prefix string) string {
	key = strings.Trim(strings.TrimSpace(key), "/")
	prefix = strings.Trim(strings.TrimSpace(prefix), "/")
	if key == "" || prefix == "" {
		return key
	}
	if key == prefix {
		return ""
	}
	if strings.HasPrefix(key, prefix+"/") {
		return strings.TrimPrefix(key, prefix+"/")
	}
	return key
}

func parseResourceScope(resource string) (organization, project string, ok bool) {
	resource = strings.TrimSpace(resource)
	if parsed, err := url.Parse(resource); err == nil && parsed.Path != "" {
		resource = parsed.Path
	}
	parts := strings.Split(strings.Trim(resource, "/"), "/")
	if len(parts) < 2 {
		return "", "", false
	}
	if parts[0] != "organization" && parts[0] != "organizations" && parts[0] != "program" && parts[0] != "programs" {
		return "", "", false
	}
	organization = strings.TrimSpace(parts[1])
	if organization == "" {
		return "", "", false
	}
	if len(parts) >= 4 && (parts[2] == "project" || parts[2] == "projects") {
		project = strings.TrimSpace(parts[3])
	}
	return organization, project, true
}
