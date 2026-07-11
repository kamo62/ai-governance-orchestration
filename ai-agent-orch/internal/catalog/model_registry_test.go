package catalog

import (
	"os"
	"path/filepath"
	"strings"
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

func TestResolveModelDefinitionFindsProviderNativeAlias(t *testing.T) {
	root := t.TempDir()
	writeRegistry(t, root, `
models:
  - alias: bedrock-sonnet
    provider: bedrock
    model_id: anthropic.claude-3-5-sonnet-20240620-v1:0
    fallback_alias: null
`)

	model, err := ResolveModelDefinition(root, "bedrock-sonnet")
	if err != nil {
		t.Fatalf("ResolveModelDefinition returned error: %v", err)
	}
	if model.Provider != "bedrock" {
		t.Fatalf("unexpected provider %q", model.Provider)
	}
	if model.ModelID != "anthropic.claude-3-5-sonnet-20240620-v1:0" {
		t.Fatalf("unexpected model ID %q", model.ModelID)
	}
}

func TestSelectClaudeBackendKeepsAliasesAndSelectsConfiguredRoute(t *testing.T) {
	registry := ModelRegistry{Models: []ModelDefinition{
		{
			Alias:                  "coding-balanced",
			Provider:               "openrouter",
			ModelID:                "anthropic/claude-sonnet-4.5",
			AllowedClassifications: []string{"public", "internal"},
			Routes: []ModelRoute{
				{Provider: "anthropic", ModelID: "claude-sonnet-4.5"},
				{Provider: "bedrock", ModelID: "anthropic.claude-sonnet-4-5-v1:0"},
			},
		},
		{Alias: "coding-fast", Provider: "openrouter", ModelID: "x-ai/grok-build-0.1"},
	}}

	got, err := SelectClaudeBackend(registry, "bedrock")
	if err != nil {
		t.Fatalf("SelectClaudeBackend returned error: %v", err)
	}
	if got.Models[0].Alias != "coding-balanced" || got.Models[0].Provider != "bedrock" || got.Models[0].ModelID != "anthropic.claude-sonnet-4-5-v1:0" {
		t.Fatalf("Claude alias was not pinned to bedrock route: %#v", got.Models[0])
	}
	if got.Models[1].Provider != "openrouter" {
		t.Fatalf("non-Claude alias changed: %#v", got.Models[1])
	}
}

func TestSelectClaudeBackendRejectsMissingRoute(t *testing.T) {
	registry := ModelRegistry{Models: []ModelDefinition{{
		Alias:    "coding-balanced",
		Provider: "openrouter",
		ModelID:  "anthropic/claude-sonnet-4.5",
		Routes:   []ModelRoute{{Provider: "anthropic", ModelID: "claude-sonnet-4.5"}},
	}}}

	_, err := SelectClaudeBackend(registry, "bedrock")
	if err == nil {
		t.Fatal("expected missing route error")
	}
	if !strings.Contains(err.Error(), "coding-balanced") || !strings.Contains(err.Error(), "bedrock") {
		t.Fatalf("expected alias and backend in error, got %v", err)
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
