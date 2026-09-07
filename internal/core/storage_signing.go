package core

import (
	"context"
	"fmt"
	"net/url"
	"path"
	"sort"
	"strings"

	syfoncommon "github.com/calypr/syfon/common"
	"github.com/calypr/syfon/internal/common"
	"github.com/calypr/syfon/internal/faults"
	"github.com/calypr/syfon/internal/models"
	"github.com/calypr/syfon/internal/urlmanager"
)

// SignURL generates a signed URL for an object's access method.
func (m *ObjectManager) SignURL(ctx context.Context, accessURL string, options urlmanager.SignOptions) (string, error) {
	return m.uM.SignURL(ctx, m.resolveSigningBucket(ctx, accessURL), accessURL, options)
}

func (m *ObjectManager) SignObjectURL(ctx context.Context, obj *models.InternalObject, accessURL string, options urlmanager.SignOptions) (string, error) {
	targetURL := strings.TrimSpace(accessURL)
	if strings.EqualFold(strings.TrimSpace(options.Method), "PUT") {
		target, err := m.ResolveCanonicalStorageTarget(ctx, CanonicalStorageTargetRequest{
			Object:    obj,
			AccessURL: targetURL,
		})
		if err != nil {
			return "", err
		}
		targetURL = target.URL
	} else {
		var err error
		targetURL, err = m.resolveObjectDownloadURL(ctx, obj, targetURL)
		if err != nil {
			return "", err
		}
	}
	return m.SignURL(ctx, targetURL, options)
}

type CanonicalStorageTargetRequest struct {
	Object         *models.InternalObject
	AccessURL      string
	Bucket         string
	Key            string
	PreferChecksum bool
}

type CanonicalStorageTarget struct {
	Bucket string
	Key    string
	URL    string
}

func (m *ObjectManager) ResolveCanonicalStorageTarget(ctx context.Context, req CanonicalStorageTargetRequest) (CanonicalStorageTarget, error) {
	obj := req.Object
	if obj == nil {
		return CanonicalStorageTarget{}, fmt.Errorf("object is required")
	}

	scopes, err := m.bucketScopesForObject(ctx, obj)
	if err != nil {
		return CanonicalStorageTarget{}, err
	}

	existingURL := strings.TrimSpace(req.AccessURL)
	if existingURL == "" {
		existingURL = FirstSupportedAccessURL(obj)
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
			return CanonicalStorageTarget{}, fmt.Errorf("unable to resolve scoped storage bucket for object %s", obj.Id)
		}
		targetKey := m.canonicalObjectKey(obj, req.Key, existingKey, req.PreferChecksum)
		if existingOK && strings.EqualFold(strings.TrimSpace(existingBucket), targetBucket) && len(normalizedScopePrefixes(scopes)) == 0 && strings.TrimSpace(existingKey) != "" {
			targetKey = existingKey
		}
		targetKey = normalizeScopedStorageKey(targetKey, scopes)
		if strings.TrimSpace(targetKey) == "" {
			return CanonicalStorageTarget{}, fmt.Errorf("unable to resolve scoped storage key for object %s", obj.Id)
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

func (m *ObjectManager) ResolveScopedUploadTarget(ctx context.Context, organization, project, key string) (CanonicalStorageTarget, error) {
	organization = strings.TrimSpace(organization)
	project = strings.TrimSpace(project)
	key = strings.Trim(strings.TrimSpace(key), "/")
	if organization == "" {
		return CanonicalStorageTarget{}, fmt.Errorf("%w: organization is required", faults.ErrInvalidInput)
	}
	if _, err := syfoncommon.ResourcePath(organization, project); err != nil {
		return CanonicalStorageTarget{}, fmt.Errorf("%w: %v", faults.ErrInvalidInput, err)
	}

	scopes := make([]models.BucketScope, 0, 2)
	if scope, found, err := m.bucketCatalog.lookupBucketScope(ctx, organization, ""); err != nil {
		return CanonicalStorageTarget{}, err
	} else if found {
		scopes = append(scopes, scope)
	}
	if project != "" {
		if scope, found, err := m.bucketCatalog.lookupBucketScope(ctx, organization, project); err != nil {
			return CanonicalStorageTarget{}, err
		} else if found {
			scopes = append(scopes, scope)
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

func (m *ObjectManager) canonicalObjectKey(obj *models.InternalObject, explicitKey string, existingKey string, preferChecksum bool) string {
	explicitKey = strings.Trim(strings.TrimSpace(explicitKey), "/")
	if explicitKey != "" {
		return explicitKey
	}
	checksum := ""
	if sha, ok := common.CanonicalSHA256(obj.Checksums); ok {
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
	return strings.Trim(strings.TrimSpace(obj.Id), "/")
}

func newCanonicalStorageTarget(bucket string, key string) CanonicalStorageTarget {
	bucket = strings.TrimSpace(bucket)
	key = strings.Trim(strings.TrimSpace(key), "/")
	return CanonicalStorageTarget{
		Bucket: bucket,
		Key:    key,
		URL:    common.BucketToURL(bucket, key),
	}
}

// ResolveBucket validates a bucket name or returns the default one.
func (m *ObjectManager) ResolveBucket(ctx context.Context, bucketName string) (string, error) {
	creds, err := m.bucketCatalog.listS3Credentials(ctx)
	if err != nil {
		return "", err
	}
	return m.bucketCatalog.resolveBucketName(creds, bucketName)
}

func (m *ObjectManager) SignDownloadPart(ctx context.Context, bucket, accessURL string, start, end int64, options urlmanager.SignOptions) (string, error) {
	return m.uM.SignDownloadPart(ctx, bucket, accessURL, start, end, options)
}

func (m *ObjectManager) SignObjectDownloadPart(ctx context.Context, obj *models.InternalObject, bucket, accessURL string, start, end int64, options urlmanager.SignOptions) (string, error) {
	var err error
	accessURL, err = m.resolveObjectDownloadURL(ctx, obj, accessURL)
	if err != nil {
		return "", err
	}
	if b, _, ok := common.ParseS3URL(accessURL); ok {
		bucket = b
	}
	return m.SignDownloadPart(ctx, bucket, accessURL, start, end, options)
}

func (m *ObjectManager) resolveObjectDownloadURL(ctx context.Context, obj *models.InternalObject, accessURL string) (string, error) {
	accessURL = strings.TrimSpace(accessURL)
	legacyURL, err := m.resolveLegacyS3DownloadURL(ctx, obj, accessURL)
	if err != nil {
		return "", err
	}
	if legacyURL != accessURL || !isUnscopedCanonicalSHA256(obj, accessURL) {
		return legacyURL, nil
	}

	target, err := m.ResolveCanonicalStorageTarget(ctx, CanonicalStorageTargetRequest{
		Object:    obj,
		AccessURL: accessURL,
	})
	if err != nil {
		return "", err
	}
	return target.URL, nil
}

func isUnscopedCanonicalSHA256(obj *models.InternalObject, accessURL string) bool {
	if obj == nil {
		return false
	}
	_, key, ok := parseS3Location(accessURL)
	if !ok || strings.Contains(strings.Trim(key, "/"), "/") {
		return false
	}
	sha, ok := common.CanonicalSHA256(obj.Checksums)
	return ok && strings.EqualFold(strings.Trim(key, "/"), strings.TrimSpace(sha))
}

func (m *ObjectManager) resolveLegacyS3DownloadURL(ctx context.Context, obj *models.InternalObject, accessURL string) (string, error) {
	accessURL = strings.TrimSpace(accessURL)
	bucket, key, ok := parseS3Location(accessURL)
	if !ok || strings.TrimSpace(bucket) == "" || strings.TrimSpace(key) == "" {
		return accessURL, nil
	}

	scopes, err := m.bucketScopesForObject(ctx, obj)
	if err != nil {
		return "", err
	}

	mappedURLs := make([]string, 0, 1)
	for _, scope := range scopes {
		prefix := strings.Trim(strings.TrimSpace(scope.PathPrefix), "/")
		if prefix == "" || bucket != prefix {
			continue
		}
		targetBucket := strings.TrimSpace(scope.Bucket)
		if targetBucket == "" {
			continue
		}
		mappedKey := prefix + "/" + strings.TrimLeft(key, "/")
		candidate := common.BucketToURL(targetBucket, mappedKey)
		if len(mappedURLs) == 0 || mappedURLs[len(mappedURLs)-1] != candidate {
			mappedURLs = append(mappedURLs, candidate)
		}
	}
	if len(mappedURLs) == 0 {
		return accessURL, nil
	}

	credentials, err := m.bucketCatalog.listS3Credentials(ctx)
	if err != nil {
		return "", err
	}
	for _, credential := range credentials {
		if strings.TrimSpace(credential.Bucket) == bucket {
			return accessURL, nil
		}
	}
	if len(mappedURLs) > 1 {
		return "", fmt.Errorf("%w: legacy S3 URL %q maps to conflicting physical locations %q and %q", faults.ErrConflict, accessURL, mappedURLs[0], mappedURLs[1])
	}
	return mappedURLs[0], nil
}

func (m *ObjectManager) resolveSigningBucket(ctx context.Context, accessURL string) string {
	if bucket, _, ok := common.ParseS3URL(accessURL); ok {
		return bucket
	}
	return ""
}

func parseS3Location(accessURL string) (bucket string, key string, ok bool) {
	if bucket, key, ok := common.ParseS3URL(accessURL); ok {
		return bucket, key, true
	}
	parsed, err := url.Parse(strings.TrimSpace(accessURL))
	if err != nil {
		return "", "", false
	}
	if !strings.EqualFold(strings.TrimSpace(parsed.Scheme), "s3") {
		return "", "", false
	}
	return strings.TrimSpace(parsed.Host), strings.Trim(strings.TrimSpace(parsed.Path), "/"), true
}

func normalizeScopedStorageKey(key string, scopes []models.BucketScope) string {
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

func normalizedScopePrefixes(scopes []models.BucketScope) []string {
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

func (m *ObjectManager) bucketScopesForObject(ctx context.Context, obj *models.InternalObject) ([]models.BucketScope, error) {
	if obj == nil {
		return nil, nil
	}
	authz := syfoncommon.ControlledAccessToAuthzMap(ObjectAccessResources(obj))
	if len(authz) == 0 {
		return nil, nil
	}
	orgs := make([]string, 0, len(authz))
	for org := range authz {
		orgs = append(orgs, org)
	}
	sort.Strings(orgs)
	scopes := make([]models.BucketScope, 0, len(orgs)*2)
	for _, org := range orgs {
		if scope, found, err := m.bucketCatalog.lookupBucketScope(ctx, org, ""); err != nil {
			return nil, err
		} else if found {
			scopes = append(scopes, scope)
		}
		projects := append([]string(nil), authz[org]...)
		sort.Strings(projects)
		for _, project := range projects {
			scope, found, err := m.bucketCatalog.lookupBucketScope(ctx, org, project)
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
