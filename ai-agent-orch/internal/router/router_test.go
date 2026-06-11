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

func TestRouterSelectsFirstAvailableRouteForCapabilityAlias(t *testing.T) {
	r := NewWithRouteAvailability(catalog.ModelRegistry{
		Models: []catalog.ModelDefinition{
			{
				Alias:                  "coding-gpt55",
				Provider:               "openrouter",
				ModelID:                "openai/gpt-5.5",
				AllowedClassifications: []string{"public", "internal"},
				Routes: []catalog.ModelRoute{
					{
						Provider:           "copilot-user",
						ModelID:            "gpt-5.5",
						CredentialSource:   "copilot-user",
						RequiresActorToken: true,
						Reasoning: catalog.ReasoningMetadata{
							DefaultEffort:  "low",
							MaxEffort:      "medium",
							SupportsEffort: boolPtr(false),
						},
					},
					{
						Provider:         "openrouter",
						ModelID:          "openai/gpt-5.5",
						CredentialSource: "platform-openrouter",
						Reasoning: catalog.ReasoningMetadata{
							DefaultEffort:  "medium",
							MaxEffort:      "high",
							SupportsEffort: boolPtr(true),
						},
					},
				},
			},
		},
	}, func(_ context.Context, route catalog.ModelRoute, req Request) bool {
		return route.Provider != "copilot-user" || req.ActorSubject == "dev@example.test"
	})

	decision, err := r.Route(context.Background(), Request{
		Classification: "internal",
		PreferredAlias: "coding-gpt55",
		ActorSubject:   "dev@example.test",
	})
	if err != nil {
		t.Fatalf("route failed: %v", err)
	}
	if decision.Provider != "copilot-user" || decision.SelectedModelID != "gpt-5.5" {
		t.Fatalf("expected copilot route, got %#v", decision)
	}
	if decision.CredentialSource != "copilot-user" {
		t.Fatalf("expected copilot credential source, got %q", decision.CredentialSource)
	}
	if decision.ReasoningSupportsEffort {
		t.Fatal("expected copilot route to report unsupported explicit reasoning effort")
	}

	decision, err = r.Route(context.Background(), Request{
		Classification: "internal",
		PreferredAlias: "coding-gpt55",
		ActorSubject:   "no-token@example.test",
	})
	if err != nil {
		t.Fatalf("route failed: %v", err)
	}
	if decision.Provider != "openrouter" || decision.SelectedModelID != "openai/gpt-5.5" {
		t.Fatalf("expected openrouter fallback route, got %#v", decision)
	}
	if decision.CredentialSource != "platform-openrouter" {
		t.Fatalf("expected platform credential source, got %q", decision.CredentialSource)
	}
	if !decision.ReasoningSupportsEffort || decision.ReasoningDefaultEffort != "medium" || decision.ReasoningMaxEffort != "high" {
		t.Fatalf("unexpected reasoning metadata: %#v", decision)
	}
}

func TestRouterProviderPinnedAliasDoesNotSwitchProvider(t *testing.T) {
	r := NewWithRouteAvailability(catalog.ModelRegistry{
		Models: []catalog.ModelDefinition{
			{
				Alias:                  "openrouter-openai-gpt55",
				Provider:               "openrouter",
				ModelID:                "openai/gpt-5.5",
				AllowedClassifications: []string{"public", "internal"},
				Routes: []catalog.ModelRoute{
					{Provider: "openrouter", ModelID: "openai/gpt-5.5", CredentialSource: "platform-openrouter"},
				},
			},
		},
	}, func(context.Context, catalog.ModelRoute, Request) bool {
		return false
	})

	decision, err := r.Route(context.Background(), Request{
		Classification: "internal",
		PreferredAlias: "openrouter-openai-gpt55",
		ActorSubject:   "dev@example.test",
	})
	if err != nil {
		t.Fatalf("route failed: %v", err)
	}
	if decision.Provider != "openrouter" || decision.SelectedModelID != "openai/gpt-5.5" {
		t.Fatalf("provider-pinned alias switched unexpectedly: %#v", decision)
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

func boolPtr(v bool) *bool {
	return &v
}

func TestRouterEnrichmentPostures(t *testing.T) {
	r := New(catalog.ModelRegistry{
		Models: []catalog.ModelDefinition{
			{Alias: "coding-primary", Provider: "openrouter", ModelID: "m1", Purpose: "Highest-quality coding", AllowedClassifications: []string{"public", "internal"}},
		},
	})

	decision, err := r.Route(context.Background(), Request{
		TaskType:           "coding",
		Classification:     "internal",
		WorkflowStage:      "review",
		RiskLevel:          "high",
		CostSensitivity:    "low",
		LatencySensitivity: "low",
		EvidenceNeeds:      "full",
	})
	if err != nil {
		t.Fatalf("route failed: %v", err)
	}
	if decision.CostPosture != "performance" {
		t.Fatalf("expected performance cost posture, got %q", decision.CostPosture)
	}
	if decision.LatencyPosture != "thorough" {
		t.Fatalf("expected thorough latency posture, got %q", decision.LatencyPosture)
	}
	if len(decision.Reasons) == 0 {
		t.Fatal("expected enriched reasons")
	}
	found := false
	for _, r := range decision.Reasons {
		if r == "evidence_required" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected evidence_required in reasons, got %v", decision.Reasons)
	}
}

func TestRouterScoresByWorkflowStage(t *testing.T) {
	r := New(catalog.ModelRegistry{
		Models: []catalog.ModelDefinition{
			{Alias: "coding-fast", Provider: "openrouter", ModelID: "m1", Purpose: "Fast economy coding", AllowedClassifications: []string{"public", "internal"}},
			{Alias: "coding-deep", Provider: "openrouter", ModelID: "m2", Purpose: "Highest-quality coding review", AllowedClassifications: []string{"public", "internal"}},
		},
	})

	// Draft stage should prefer fast/economy model
	decision, err := r.Route(context.Background(), Request{
		TaskType:       "coding",
		Classification: "internal",
		WorkflowStage:  "draft",
	})
	if err != nil {
		t.Fatalf("route failed: %v", err)
	}
	if decision.SelectedAlias != "coding-fast" {
		t.Fatalf("expected coding-fast for draft stage, got %q", decision.SelectedAlias)
	}

	// Review stage should prefer deep/quality model
	decision, err = r.Route(context.Background(), Request{
		TaskType:       "coding",
		Classification: "internal",
		WorkflowStage:  "review",
	})
	if err != nil {
		t.Fatalf("route failed: %v", err)
	}
	if decision.SelectedAlias != "coding-deep" {
		t.Fatalf("expected coding-deep for review stage, got %q", decision.SelectedAlias)
	}
}
