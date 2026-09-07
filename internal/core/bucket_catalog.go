package core

import (
	"context"
	"fmt"
	"net/url"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/calypr/syfon/apigen/server/drs"
	syfoncommon "github.com/calypr/syfon/common"
	"github.com/calypr/syfon/internal/access"
	"github.com/calypr/syfon/internal/common"
	"github.com/calypr/syfon/internal/db"
	"github.com/calypr/syfon/internal/faults"
	"github.com/calypr/syfon/internal/models"
	"github.com/calypr/syfon/internal/urlmanager"
)

// bucketCatalog owns bucket credentials, scopes, and the cache/invalidation
// rules that keep those records in sync with storage signers.
type bucketCatalog struct {
	db                     db.CredentialStore
	bucketScopeCache       *bucketScopeCache
	signerCacheInvalidator urlmanager.BucketCacheInvalidator
}

func newBucketCatalog(store db.CredentialStore, uM urlmanager.UrlManager, ttl time.Duration) *bucketCatalog {
	catalog := &bucketCatalog{
		db:               store,
		bucketScopeCache: newBucketScopeCache(ttl),
	}
	if invalidator, ok := uM.(urlmanager.BucketCacheInvalidator); ok {
		catalog.signerCacheInvalidator = invalidator
	}
	return catalog
}

func (c *bucketCatalog) listS3Credentials(ctx context.Context) ([]models.S3Credential, error) {
	return c.db.ListS3Credentials(ctx)
}

func (c *bucketCatalog) getS3Credential(ctx context.Context, credentialID string) (*models.S3Credential, error) {
	return c.db.GetS3Credential(ctx, credentialID)
}

func (c *bucketCatalog) saveS3Credential(ctx context.Context, cred *models.S3Credential) error {
	credentialID := c.credentialIDForCredential(*cred)
	if err := c.db.SaveS3Credential(ctx, cred); err != nil {
		return err
	}
	c.invalidateBucketSignerCache(credentialID)
	if strings.TrimSpace(cred.Bucket) != credentialID {
		c.invalidateBucketSignerCache(cred.Bucket)
	}
	return nil
}

func (c *bucketCatalog) deleteS3Credential(ctx context.Context, credentialID string) error {
	if err := c.db.DeleteS3Credential(ctx, credentialID); err != nil {
		return err
	}
	c.bucketScopeCache.clear()
	c.invalidateBucketSignerCache(credentialID)
	return nil
}

func (c *bucketCatalog) invalidateBucketSignerCache(bucket string) {
	if c.signerCacheInvalidator == nil {
		return
	}
	c.signerCacheInvalidator.InvalidateBucket(bucket)
}

func (c *bucketCatalog) listBucketScopes(ctx context.Context) ([]models.BucketScope, error) {
	return c.db.ListBucketScopes(ctx)
}

func (c *bucketCatalog) createBucketScope(ctx context.Context, scope *models.BucketScope) error {
	if err := c.db.CreateBucketScope(ctx, scope); err != nil {
		return err
	}
	c.bucketScopeCache.set(normalizeBucketScope(scope), true)
	return nil
}

func (c *bucketCatalog) deleteBucketScope(ctx context.Context, organization, projectID, credentialID, pathPrefix string) error {
	if err := c.db.DeleteBucketScope(ctx, organization, projectID, credentialID, pathPrefix); err != nil {
		return err
	}
	c.bucketScopeCache.clear()

	resolvedCredID := credentialID
	if cred, err := c.getS3Credential(ctx, credentialID); err == nil && cred != nil {
		resolvedCredID = cred.CredentialID
	}

	scopes, err := c.listBucketScopes(ctx)
	if err == nil {
		hasRemaining := false
		for _, s := range scopes {
			if strings.TrimSpace(s.CredentialID) == resolvedCredID || strings.TrimSpace(s.Bucket) == resolvedCredID {
				hasRemaining = true
				break
			}
		}
		if !hasRemaining {
			_ = c.deleteS3Credential(ctx, resolvedCredID)
		}
	}

	return nil
}

func (c *bucketCatalog) lookupBucketScope(ctx context.Context, organization, project string) (models.BucketScope, bool, error) {
	if scope, found, cached := c.bucketScopeCache.get(organization, project); cached {
		return scope, found, nil
	}

	scope, err := c.db.GetBucketScope(ctx, organization, project)
	if err != nil {
		if faults.IsNotFoundError(err) {
			c.bucketScopeCache.set(models.BucketScope{Organization: organization, ProjectID: project}, false)
			return models.BucketScope{}, false, nil
		}
		return models.BucketScope{}, false, err
	}
	if scope == nil {
		c.bucketScopeCache.set(models.BucketScope{Organization: organization, ProjectID: project}, false)
		return models.BucketScope{}, false, nil
	}

	normalized := normalizeBucketScope(scope)
	c.bucketScopeCache.set(normalized, true)
	return normalized, true, nil
}

// The ObjectManager methods below preserve the core-facing operations used by
// API packages while keeping catalog state private to this package.
func (m *ObjectManager) ListS3Credentials(ctx context.Context) ([]models.S3Credential, error) {
	return m.bucketCatalog.listS3Credentials(ctx)
}

func (m *ObjectManager) GetS3Credential(ctx context.Context, credentialID string) (*models.S3Credential, error) {
	return m.bucketCatalog.getS3Credential(ctx, credentialID)
}

func (m *ObjectManager) SaveS3Credential(ctx context.Context, cred *models.S3Credential) error {
	return m.bucketCatalog.saveS3Credential(ctx, cred)
}

func (m *ObjectManager) DeleteS3Credential(ctx context.Context, credentialID string) error {
	return m.bucketCatalog.deleteS3Credential(ctx, credentialID)
}

func (m *ObjectManager) ListBucketScopes(ctx context.Context) ([]models.BucketScope, error) {
	return m.bucketCatalog.listBucketScopes(ctx)
}

func (m *ObjectManager) ListVisibleBuckets(ctx context.Context) (map[string]VisibleBucket, error) {
	return m.listVisibleBucketsCached(ctx)
}

func (m *ObjectManager) listVisibleBucketsUncached(ctx context.Context) (map[string]VisibleBucket, error) {
	creds, err := m.bucketCatalog.listS3Credentials(ctx)
	if err != nil {
		return nil, err
	}
	if len(creds) == 0 {
		return map[string]VisibleBucket{}, nil
	}

	if lister, ok := m.db.(db.BucketVisibilityLister); ok {
		return m.listVisibleBucketsFromRows(ctx, lister, creds)
	}

	objects, err := m.listBucketsVisibleObjects(ctx)
	if err != nil {
		return nil, err
	}

	byCredential := make(map[string]VisibleBucket, len(creds))
	programsSeen := make(map[string]map[string]struct{}, len(creds))
	for _, cred := range creds {
		key := m.bucketCatalog.credentialIDForCredential(cred)
		byCredential[key] = VisibleBucket{Credential: cred}
		programsSeen[key] = map[string]struct{}{}
	}

	// Configured bucket scopes must be visible even before any objects are
	// written into a bucket, otherwise scoped uploads cannot resolve a target
	// bucket from the catalog on a fresh deployment.
	scopes, err := m.bucketCatalog.listBucketScopes(ctx)
	if err != nil {
		return nil, err
	}
	explicitScopeOwners := make(map[string]string, len(scopes))
	for _, scope := range scopes {
		credentialID := m.bucketCatalog.credentialIDForScope(scope)
		entry, exists := byCredential[credentialID]
		if !exists {
			continue
		}
		resource, resourceErr := syfoncommon.ResourcePath(scope.Organization, scope.ProjectID)
		if resourceErr != nil || strings.TrimSpace(resource) == "" {
			continue
		}
		if access.IsAuthzEnforced(ctx) && !access.HasMethodAccess(ctx, objectMethodRead, []string{resource}) {
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

	for _, obj := range objects {
		programs := ObjectAccessResources(&obj)
		if obj.AccessMethods == nil {
			continue
		}
		for _, method := range *obj.AccessMethods {
			credentialID, ok := m.bucketCatalog.credentialIDForAccessMethod(method, creds)
			if !ok {
				continue
			}
			entry, exists := byCredential[credentialID]
			if !exists {
				continue
			}
			for _, program := range programs {
				if program == "" {
					continue
				}
				if owner, ok := explicitScopeOwners[program]; ok && owner != credentialID {
					continue
				}
				if _, seen := programsSeen[credentialID][program]; seen {
					continue
				}
				programsSeen[credentialID][program] = struct{}{}
				entry.Programs = append(entry.Programs, program)
			}
			byCredential[credentialID] = entry
		}
	}

	for credentialID, entry := range byCredential {
		sort.Strings(entry.Programs)
		byCredential[credentialID] = entry
	}
	return byCredential, nil
}

func (m *ObjectManager) listVisibleBucketsFromRows(ctx context.Context, lister db.BucketVisibilityLister, creds []models.S3Credential) (map[string]VisibleBucket, error) {
	restrictToResources := access.IsAuthzEnforced(ctx) &&
		!access.HasMethodAccess(ctx, objectMethodRead, []string{"/programs"}) &&
		!access.HasMethodAccess(ctx, objectMethodRead, []string{"/data_file"})
	rows, err := lister.ListBucketVisibilityRows(ctx, readableResources(ctx), true, restrictToResources)
	if err != nil {
		return nil, err
	}

	byCredential := make(map[string]VisibleBucket, len(creds))
	programsSeen := make(map[string]map[string]struct{}, len(creds))
	publicSeen := make(map[string]bool, len(creds))
	for _, cred := range creds {
		key := m.bucketCatalog.credentialIDForCredential(cred)
		byCredential[key] = VisibleBucket{Credential: cred}
		programsSeen[key] = map[string]struct{}{}
	}

	scopes, err := m.bucketCatalog.listBucketScopes(ctx)
	if err != nil {
		return nil, err
	}
	explicitScopeOwners := make(map[string]string, len(scopes))
	for _, scope := range scopes {
		credentialID := m.bucketCatalog.credentialIDForScope(scope)
		entry, exists := byCredential[credentialID]
		if !exists {
			continue
		}
		resource, resourceErr := syfoncommon.ResourcePath(scope.Organization, scope.ProjectID)
		if resourceErr != nil || strings.TrimSpace(resource) == "" {
			continue
		}
		if restrictToResources && !access.HasMethodAccess(ctx, objectMethodRead, []string{resource}) {
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
		methodType := drs.AccessMethodType(strings.TrimSpace(row.AccessType))
		method := drs.AccessMethod{
			Type: methodType,
			AccessUrl: &struct {
				Headers *[]string `json:"headers,omitempty"`
				Url     string    `json:"url"`
			}{Url: row.AccessURL},
		}
		credentialID, ok := m.bucketCatalog.credentialIDForAccessMethod(method, creds)
		if !ok {
			continue
		}
		entry, exists := byCredential[credentialID]
		if !exists {
			continue
		}
		resource := strings.TrimSpace(row.Resource)
		if resource == "" {
			publicSeen[credentialID] = true
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

func (m *ObjectManager) CreateBucketScope(ctx context.Context, scope *models.BucketScope) error {
	return m.bucketCatalog.createBucketScope(ctx, scope)
}

func (m *ObjectManager) DeleteBucketScope(ctx context.Context, organization, projectID, credentialID, pathPrefix string) error {
	return m.bucketCatalog.deleteBucketScope(ctx, organization, projectID, credentialID, pathPrefix)
}

func (m *ObjectManager) listBucketsVisibleObjects(ctx context.Context) ([]models.InternalObject, error) {
	ids, err := m.db.ListObjectIDsByScope(ctx, "", "")
	if err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		return []models.InternalObject{}, nil
	}
	objects, err := m.db.GetBulkObjects(ctx, ids)
	if err != nil {
		return nil, err
	}
	if !access.IsAuthzEnforced(ctx) || access.HasMethodAccess(ctx, objectMethodRead, []string{"/programs"}) || access.HasMethodAccess(ctx, objectMethodRead, []string{"/data_file"}) {
		return objects, nil
	}
	return m.filterObjectsByMethod(ctx, objects, objectMethodRead), nil
}

func (c *bucketCatalog) credentialIDForCredential(cred models.S3Credential) string {
	if credentialID := strings.TrimSpace(cred.CredentialID); credentialID != "" {
		return credentialID
	}
	return strings.TrimSpace(cred.Bucket)
}

func (c *bucketCatalog) credentialIDForScope(scope models.BucketScope) string {
	if credentialID := strings.TrimSpace(scope.CredentialID); credentialID != "" {
		return credentialID
	}
	return strings.TrimSpace(scope.Bucket)
}

func (c *bucketCatalog) credentialIDForAccessMethod(method drs.AccessMethod, creds []models.S3Credential) (string, bool) {
	bucket, ok := c.bucketForAccessMethod(method, creds)
	if !ok {
		return "", false
	}
	for _, cred := range creds {
		if strings.TrimSpace(cred.Bucket) == bucket {
			return c.credentialIDForCredential(cred), true
		}
	}
	return bucket, true
}

func (c *bucketCatalog) bucketForAccessMethod(method drs.AccessMethod, creds []models.S3Credential) (string, bool) {
	if method.AccessUrl == nil {
		return "", false
	}
	raw := strings.TrimSpace(method.AccessUrl.Url)
	if raw == "" {
		return "", false
	}
	if bucket, _, ok := common.ParseS3URL(raw); ok {
		return bucket, true
	}
	scheme := common.SchemeFromURL(raw)
	if provider := common.ProviderFromScheme(scheme); provider == common.GCSProvider || provider == common.AzureProvider {
		if parsed, err := url.Parse(raw); err == nil && strings.TrimSpace(parsed.Host) != "" {
			return strings.TrimSpace(parsed.Host), true
		}
	}
	cleanRaw := filepath.Clean(strings.TrimSpace(raw))
	for _, cred := range creds {
		if common.NormalizeProvider(cred.Provider, common.S3Provider) != common.FileProvider {
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

func (c *bucketCatalog) resolveBucketName(creds []models.S3Credential, bucketName string) (string, error) {
	if len(creds) == 0 {
		return "", fmt.Errorf("no buckets configured")
	}
	if bucketName == "" {
		return creds[0].Bucket, nil
	}
	for _, cred := range creds {
		if cred.Bucket == bucketName {
			return cred.Bucket, nil
		}
	}
	return "", fmt.Errorf("bucket %q not configured", bucketName)
}

func ObjectAccessResources(obj *models.InternalObject) []string {
	if obj == nil {
		return nil
	}
	if obj.ControlledAccess != nil {
		return syfoncommon.NormalizeAccessResources(*obj.ControlledAccess)
	}
	return syfoncommon.AuthzMapToList(obj.Authorizations)
}
