package buckets

import (
	"testing"
	"time"
)

func TestScopeCacheNormalizesKeysAndValues(t *testing.T) {
	clock := &manualClock{}
	cache := newScopeCache(time.Minute, clock.Now)
	cache.set(Scope{
		Organization: " org ",
		ProjectID:    " project ",
		CredentialID: " credential ",
		Bucket:       " bucket ",
		PathPrefix:   " /nested/path/ ",
	}, true)

	got, found, cached := cache.get("org", "project")
	if !cached || !found {
		t.Fatalf("cache lookup=(%v,%v,%v), want hit", got, found, cached)
	}
	want := Scope{Organization: "org", ProjectID: "project", CredentialID: "credential", Bucket: "bucket", PathPrefix: "nested/path"}
	if got != want {
		t.Fatalf("cached scope=%+v, want %+v", got, want)
	}
}

func TestScopeCacheStoresPositiveAndNegativeEntries(t *testing.T) {
	clock := &manualClock{}
	cache := newScopeCache(time.Minute, clock.Now)

	cache.set(Scope{Organization: "org", ProjectID: "present"}, true)
	if _, found, cached := cache.get("org", "present"); !cached || !found {
		t.Fatal("positive entry was not returned as a cache hit")
	}
	cache.set(Scope{Organization: "org", ProjectID: "missing"}, false)
	if _, found, cached := cache.get("org", "missing"); !cached || found {
		t.Fatal("negative entry was not returned as a cached miss")
	}
}

func TestScopeCacheExpiresAtTTLBoundary(t *testing.T) {
	clock := &manualClock{}
	cache := newScopeCache(10*time.Second, clock.Now)
	cache.set(Scope{Organization: "org", ProjectID: "project"}, true)

	clock.Advance(9 * time.Second)
	if _, _, cached := cache.get("org", "project"); !cached {
		t.Fatal("entry expired before its TTL")
	}
	clock.Advance(time.Second)
	if _, _, cached := cache.get("org", "project"); !cached {
		t.Fatal("entry expired at its TTL boundary")
	}
	clock.Advance(time.Nanosecond)
	if _, _, cached := cache.get("org", "project"); cached {
		t.Fatal("entry remained cached after its TTL")
	}
}

func TestScopeCacheClearRemovesHitsAndMisses(t *testing.T) {
	cache := newScopeCache(time.Minute, time.Now)
	cache.set(Scope{Organization: "org", ProjectID: "present"}, true)
	cache.set(Scope{Organization: "org", ProjectID: "missing"}, false)
	cache.clear()
	if _, _, cached := cache.get("org", "present"); cached {
		t.Fatal("clear retained positive entry")
	}
	if _, _, cached := cache.get("org", "missing"); cached {
		t.Fatal("clear retained negative entry")
	}
}
