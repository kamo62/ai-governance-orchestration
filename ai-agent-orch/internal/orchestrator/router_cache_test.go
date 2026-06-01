package orchestrator

import (
	"testing"
	"time"

	"ai-agent-orch/internal/catalog"
)

func realCatalogRoot() string {
	return "../.."
}

func TestRouterCachedCatalog_Hit(t *testing.T) {
	root := realCatalogRoot()

	r := NewRouter(RouterConfig{CatalogRoot: root})
	_, err := r.cachedCatalog()
	if err != nil {
		t.Fatalf("first call failed: %v", err)
	}

	// Second call within TTL should be a cache hit.
	_, err = r.cachedCatalog()
	if err != nil {
		t.Fatalf("second call failed: %v", err)
	}

	r.cacheMu.RLock()
	cached := r.cached
	r.cacheMu.RUnlock()
	if cached == nil {
		t.Fatal("expected cache to be populated")
	}
	if time.Since(cached.validatedAt) > time.Minute {
		t.Fatal("cache should be recent")
	}
}

func TestRouterCachedCatalog_ErrorCached(t *testing.T) {
	root := t.TempDir()
	// No catalog files -> validation fails.
	r := NewRouter(RouterConfig{CatalogRoot: root})
	_, err := r.cachedCatalog()
	if err == nil {
		t.Fatal("expected validation to fail")
	}

	// Error should be cached briefly.
	r.cacheMu.RLock()
	cached := r.cached
	r.cacheMu.RUnlock()
	if cached == nil {
		t.Fatal("expected error to be cached")
	}
	if cached.err == nil {
		t.Fatal("expected cached error")
	}
}

func TestRouterCachedCatalog_InvalidatesAfterTTL(t *testing.T) {
	root := realCatalogRoot()

	r := NewRouter(RouterConfig{CatalogRoot: root})
	_, err := r.cachedCatalog()
	if err != nil {
		t.Fatalf("first call failed: %v", err)
	}

	// Manually backdate the cache so it appears stale.
	r.cacheMu.Lock()
	r.cached.validatedAt = time.Now().Add(-time.Hour)
	r.cacheMu.Unlock()

	// Next call should re-validate (cache miss).
	_, err = r.cachedCatalog()
	if err != nil {
		t.Fatalf("revalidation failed: %v", err)
	}

	r.cacheMu.RLock()
	cached := r.cached
	r.cacheMu.RUnlock()
	if time.Since(cached.validatedAt) > 10*time.Second {
		t.Fatal("expected cache to be refreshed")
	}
}

func mustCreateMinimalCatalog(t *testing.T, root string) {
	t.Helper()
	_ = root
	_ = catalog.Report{}
}
