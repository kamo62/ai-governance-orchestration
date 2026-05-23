package catalog

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveOpenRouterModelIDFindsAlias(t *testing.T) {
	root := t.TempDir()
	writeRegistry(t, root, `
models:
  - alias: smoke-deepseek-v4-flash
    provider: openrouter
    model_id: test-model-id
    fallback_alias: null
`)

	modelID, err := ResolveOpenRouterModelID(root, "smoke-deepseek-v4-flash")
	if err != nil {
		t.Fatalf("ResolveOpenRouterModelID returned error: %v", err)
	}
	if modelID != "test-model-id" {
		t.Fatalf("unexpected model ID %q", modelID)
	}
}

func TestResolveOpenRouterModelIDRejectsMissingAlias(t *testing.T) {
	root := t.TempDir()
	writeRegistry(t, root, `
models:
  - alias: coding-balanced
    provider: openrouter
    model_id: test-fallback-id
    fallback_alias: null
`)

	_, err := ResolveOpenRouterModelID(root, "missing")
	if err == nil {
		t.Fatalf("expected missing alias error")
	}
}

func writeRegistry(t *testing.T, root string, contents string) {
	t.Helper()
	modelsDir := filepath.Join(root, "models")
	if err := os.MkdirAll(modelsDir, 0o755); err != nil {
		t.Fatalf("mkdir models: %v", err)
	}
	if err := os.WriteFile(filepath.Join(modelsDir, "registry.yaml"), []byte(contents), 0o644); err != nil {
		t.Fatalf("write registry: %v", err)
	}
}
