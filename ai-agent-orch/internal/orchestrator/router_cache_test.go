package orchestrator

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRouterCachedCatalog_Hit(t *testing.T) {
	root := createTempCatalog(t)

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
	root := createTempCatalog(t)

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

func realCatalogRoot() string {
	return "../.."
}

func createTempCatalog(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "models", "registry.yaml"), `
models:
  - alias: coding-balanced
    provider: openrouter
    model_id: test/model
`)
	writeTestFile(t, filepath.Join(root, "mcp", "registrations", "catalog-introspection.yaml"), `
server_id: catalog-introspection
allowed_agents:
  - router-agent
`)
	writeTestFile(t, filepath.Join(root, "agents", "core", "router-agent", "agent.md"), `# Router Agent

Config: `+"`./agent.config.yaml`"+`

## Goal
Route requests to a specialist.
`)
	writeTestFile(t, filepath.Join(root, "agents", "core", "router-agent", "agent.config.yaml"), `
name: router-agent
version: 0.1.0
phase: core
owner: local
runtime: opencode
model:
  primary: coding-balanced
mcp_servers:
  - catalog-introspection
tools_allowed:
  - read_file
permissions:
  network: deny
  workspace_write: deny
  outside_workspace_write: deny
governance:
  classification_max: internal
cost:
  per_invocation_cap_usd: 0.01
  consecutive_tool_call_max: 3
evals:
  path: ./evals/
  required_for_phase0: false
`)
	writeTestFile(t, filepath.Join(root, "agents", "core", "router-agent", "evals", "golden-cases.yaml"), `
cases:
  - prompt: "review this code"
    expected_specialist: code-review
`)
	return root
}

func writeTestFile(t *testing.T, path string, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("create test dir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write test file %s: %v", path, err)
	}
}
