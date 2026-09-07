package buckets

import (
	"strings"
	"sync"
	"time"
)

type scopeCache struct {
	mu      sync.RWMutex
	ttl     time.Duration
	now     func() time.Time
	entries map[string]cachedScope
}

type cachedScope struct {
	scope   Scope
	found   bool
	expires time.Time
}

func newScopeCache(ttl time.Duration, now func() time.Time) *scopeCache {
	if now == nil {
		now = time.Now
	}
	return &scopeCache{
		ttl:     ttl,
		now:     now,
		entries: make(map[string]cachedScope),
	}
}

func (c *scopeCache) get(organization, project string) (Scope, bool, bool) {
	if c == nil {
		return Scope{}, false, false
	}
	key := scopeCacheKey(organization, project)
	c.mu.RLock()
	entry, ok := c.entries[key]
	c.mu.RUnlock()
	if !ok || c.now().After(entry.expires) {
		return Scope{}, false, false
	}
	return entry.scope, entry.found, true
}

func (c *scopeCache) set(scope Scope, found bool) {
	if c == nil {
		return
	}
	scope = normalizeScope(&scope)
	c.mu.Lock()
	c.entries[scopeCacheKey(scope.Organization, scope.ProjectID)] = cachedScope{
		scope:   scope,
		found:   found,
		expires: c.now().Add(c.ttl),
	}
	c.mu.Unlock()
}

func (c *scopeCache) clear() {
	if c == nil {
		return
	}
	c.mu.Lock()
	c.entries = make(map[string]cachedScope)
	c.mu.Unlock()
}

func scopeCacheKey(organization, project string) string {
	return strings.TrimSpace(organization) + "\x00" + strings.TrimSpace(project)
}

func normalizeScope(scope *Scope) Scope {
	if scope == nil {
		return Scope{}
	}
	return Scope{
		Organization: strings.TrimSpace(scope.Organization),
		ProjectID:    strings.TrimSpace(scope.ProjectID),
		CredentialID: strings.TrimSpace(scope.CredentialID),
		Bucket:       strings.TrimSpace(scope.Bucket),
		PathPrefix:   strings.Trim(strings.TrimSpace(scope.PathPrefix), "/"),
	}
}
