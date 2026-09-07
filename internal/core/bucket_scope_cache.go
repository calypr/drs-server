package core

import (
	"strings"
	"sync"
	"time"

	"github.com/calypr/syfon/internal/models"
)

type bucketScopeCache struct {
	mu      sync.RWMutex
	ttl     time.Duration
	entries map[string]cachedBucketScope
}

type cachedBucketScope struct {
	scope   models.BucketScope
	found   bool
	expires time.Time
}

func newBucketScopeCache(ttl time.Duration) *bucketScopeCache {
	return &bucketScopeCache{
		ttl:     ttl,
		entries: make(map[string]cachedBucketScope),
	}
}

func (c *bucketScopeCache) get(organization, project string) (models.BucketScope, bool, bool) {
	if c == nil {
		return models.BucketScope{}, false, false
	}
	key := bucketScopeCacheKey(organization, project)
	now := time.Now()
	c.mu.RLock()
	entry, ok := c.entries[key]
	c.mu.RUnlock()
	if !ok || now.After(entry.expires) {
		return models.BucketScope{}, false, false
	}
	return entry.scope, entry.found, true
}

func (c *bucketScopeCache) set(scope models.BucketScope, found bool) {
	if c == nil {
		return
	}
	scope = normalizeBucketScope(&scope)
	c.mu.Lock()
	c.entries[bucketScopeCacheKey(scope.Organization, scope.ProjectID)] = cachedBucketScope{
		scope:   scope,
		found:   found,
		expires: time.Now().Add(c.ttl),
	}
	c.mu.Unlock()
}

func (c *bucketScopeCache) clear() {
	if c == nil {
		return
	}
	c.mu.Lock()
	c.entries = make(map[string]cachedBucketScope)
	c.mu.Unlock()
}

func bucketScopeCacheKey(organization, project string) string {
	return strings.TrimSpace(organization) + "\x00" + strings.TrimSpace(project)
}

func normalizeBucketScope(scope *models.BucketScope) models.BucketScope {
	if scope == nil {
		return models.BucketScope{}
	}
	return models.BucketScope{
		Organization: strings.TrimSpace(scope.Organization),
		ProjectID:    strings.TrimSpace(scope.ProjectID),
		CredentialID: strings.TrimSpace(scope.CredentialID),
		Bucket:       strings.TrimSpace(scope.Bucket),
		PathPrefix:   strings.Trim(strings.TrimSpace(scope.PathPrefix), "/"),
	}
}
