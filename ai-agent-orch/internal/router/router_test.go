package router

import (
	"context"
	"strings"
	"testing"

	"ai-agent-orch/internal/catalog"
)

func TestRouterSelectsPreferredAlias(t *testing.T) {
	r := New(catalog.ModelRegistry{
		Models: []catalog.ModelDefinition{
			{Alias: "coding-primary", Provider: "openrouter", ModelID: "anthropic/claude-opus-4.7", AllowedClassifications: []string{"public", "internal"}},
			{Alias: "coding-fast", Provider: "openrouter", ModelID: "x-ai/grok-build-0.1", AllowedClassifications: []string{"public", "internal"}},
		},
	})

	decision, err := r.Route(context.Background(), Request{
		TaskType:       "coding",
		Classification: "internal",
		PreferredAlias: "coding-fast",
	})
	if err != nil {
		t.Fatalf("route failed: %v", err)
	}
	if decision.SelectedAlias != "coding-fast" {
		t.Fatalf("expected coding-fast, got %q", decision.SelectedAlias)
	}
}

func TestRouterRejectsPreferredAliasByClassification(t *testing.T) {
	r := New(catalog.ModelRegistry{
		Models: []catalog.ModelDefinition{
			{Alias: "public-only", Provider: "openrouter", ModelID: "m1", AllowedClassifications: []string{"public"}},
			{Alias: "internal-ok", Provider: "openrouter", ModelID: "m2", AllowedClassifications: []string{"public", "internal"}},
		},
	})

	_, err := r.Route(context.Background(), Request{
		TaskType:       "coding",
		Classification: "internal",
		PreferredAlias: "public-only",
	})
	if err == nil {
		t.Fatal("expected error when preferred alias rejected by classification")
	}
	if !strings.Contains(err.Error(), "not found or not allowed") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRouterFiltersByClassification(t *testing.T) {
	r := New(catalog.ModelRegistry{
		Models: []catalog.ModelDefinition{
			{Alias: "public-only", Provider: "openrouter", ModelID: "m1", AllowedClassifications: []string{"public"}},
		},
	})

	_, err := r.Route(context.Background(), Request{
		TaskType:       "coding",
		Classification: "internal",
	})
	if err == nil || !strings.Contains(err.Error(), "no models available") {
		t.Fatalf("expected no models error, got %v", err)
	}
}

func TestRouterScoresByTaskType(t *testing.T) {
	r := New(catalog.ModelRegistry{
		Models: []catalog.ModelDefinition{
			{Alias: "coding-primary", Provider: "openrouter", ModelID: "m1", Purpose: "Highest-quality coding", AllowedClassifications: []string{"public", "internal"}},
			{Alias: "router-small", Provider: "openrouter", ModelID: "m2", Purpose: "Routing and summarization", AllowedClassifications: []string{"public", "internal"}},
		},
	})

	decision, err := r.Route(context.Background(), Request{
		TaskType:       "coding",
		Classification: "internal",
	})
	if err != nil {
		t.Fatalf("route failed: %v", err)
	}
	if decision.SelectedAlias != "coding-primary" {
		t.Fatalf("expected coding-primary for coding task, got %q", decision.SelectedAlias)
	}
}

func TestRouterFallbackChain(t *testing.T) {
	fallback := "coding-balanced"
	r := New(catalog.ModelRegistry{
		Models: []catalog.ModelDefinition{
			{Alias: "coding-primary", Provider: "openrouter", ModelID: "m1", FallbackAlias: &fallback, AllowedClassifications: []string{"public", "internal"}},
			{Alias: "coding-balanced", Provider: "openrouter", ModelID: "m2", AllowedClassifications: []string{"public", "internal"}},
		},
	})

	decision, err := r.Route(context.Background(), Request{
		TaskType:       "coding",
		Classification: "internal",
	})
	if err != nil {
		t.Fatalf("route failed: %v", err)
	}
	if len(decision.FallbackChain) != 1 || decision.FallbackChain[0] != "coding-balanced" {
		t.Fatalf("expected fallback chain [coding-balanced], got %v", decision.FallbackChain)
	}
}

func TestRouterFallbackChainFiltersByClassification(t *testing.T) {
	publicFallback := "public-only"
	internalFallback := "internal-ok"
	r := New(catalog.ModelRegistry{
		Models: []catalog.ModelDefinition{
			{Alias: "coding-primary", Provider: "openrouter", ModelID: "m1", FallbackAlias: &publicFallback, AllowedClassifications: []string{"public", "internal"}},
			{Alias: "public-only", Provider: "openrouter", ModelID: "m2", FallbackAlias: &internalFallback, AllowedClassifications: []string{"public"}},
			{Alias: "internal-ok", Provider: "openrouter", ModelID: "m3", AllowedClassifications: []string{"internal"}},
		},
	})

	decision, err := r.Route(context.Background(), Request{
		TaskType:       "coding",
		Classification: "internal",
		PreferredAlias: "coding-primary",
	})
	if err != nil {
		t.Fatalf("route failed: %v", err)
	}
	if len(decision.FallbackChain) != 0 {
		t.Fatalf("expected public fallback to be excluded, got %v", decision.FallbackChain)
	}
}

func TestRouterResolve(t *testing.T) {
	r := New(catalog.ModelRegistry{
		Models: []catalog.ModelDefinition{
			{Alias: "coding-primary", Provider: "openrouter", ModelID: "anthropic/claude-opus-4.7"},
		},
	})

	modelID, provider, err := r.Resolve("coding-primary")
	if err != nil {
		t.Fatalf("resolve failed: %v", err)
	}
	if modelID != "anthropic/claude-opus-4.7" {
		t.Fatalf("unexpected model ID %q", modelID)
	}
	if provider != "openrouter" {
		t.Fatalf("unexpected provider %q", provider)
	}
}

func TestRouterAliasesFiltered(t *testing.T) {
	r := New(catalog.ModelRegistry{
		Models: []catalog.ModelDefinition{
			{Alias: "public-only", Provider: "openrouter", ModelID: "m1", AllowedClassifications: []string{"public"}},
			{Alias: "internal-ok", Provider: "openrouter", ModelID: "m2", AllowedClassifications: []string{"public", "internal"}},
		},
	})

	aliases := r.Aliases("internal")
	if len(aliases) != 1 || aliases[0].Alias != "internal-ok" {
		t.Fatalf("expected 1 internal alias, got %v", aliases)
	}
}
