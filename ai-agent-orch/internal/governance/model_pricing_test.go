package governance

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
)

func TestOpenRouterPricingFetcherParsesPerTokenCosts(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/models" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"x-ai/grok-build-0.1","pricing":{"prompt":"0.0000004","completion":"0.0000012"}},{"id":"openai/gpt-test","pricing":{"prompt":0.0000005,"completion":0.0000015}}]}`))
	}))
	defer server.Close()

	records, err := (OpenRouterPricingFetcher{BaseURL: server.URL}).Fetch(context.Background())
	if err != nil {
		t.Fatalf("fetch pricing: %v", err)
	}
	if len(records) != 2 {
		t.Fatalf("expected two pricing records, got %d", len(records))
	}
	if records[0].Provider != "openrouter" || records[0].ModelID != "x-ai/grok-build-0.1" {
		t.Fatalf("unexpected first record identity: %#v", records[0])
	}
	if records[0].PromptCostPerToken != 0.0000004 || records[0].CompletionCostPerToken != 0.0000012 {
		t.Fatalf("unexpected parsed costs: %#v", records[0])
	}
}

func TestRefreshModelPricingCreatesTableRecordsOnFreshStore(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"x-ai/grok-build-0.1","pricing":{"prompt":"0.0000004","completion":"0.0000012"}}]}`))
	}))
	defer server.Close()

	store, err := NewSQLiteModelPricingStore(filepath.Join(t.TempDir(), "audit.db"))
	if err != nil {
		t.Fatalf("new pricing store: %v", err)
	}
	defer store.Close()

	count, err := RefreshModelPricing(context.Background(), store, OpenRouterPricingFetcher{BaseURL: server.URL})
	if err != nil {
		t.Fatalf("refresh pricing: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected 1 pricing record, got %d", count)
	}
	got, err := store.GetModelPricing(context.Background(), "openrouter", "x-ai/grok-build-0.1")
	if err != nil {
		t.Fatalf("get pricing: %v", err)
	}
	if got.PromptCostPerToken != 0.0000004 || got.CompletionCostPerToken != 0.0000012 {
		t.Fatalf("unexpected pricing: %#v", got)
	}
}

func TestSQLiteModelPricingStoreUpsertsAndFindsPrefixedModelIDs(t *testing.T) {
	store, err := NewSQLiteModelPricingStore(filepath.Join(t.TempDir(), "pricing.db"))
	if err != nil {
		t.Fatalf("new pricing store: %v", err)
	}
	defer store.Close()

	if err := store.UpsertModelPricing(context.Background(), []ModelPricingRecord{
		{
			Provider:               "openrouter",
			ModelID:                "x-ai/grok-build-0.1",
			PromptCostPerToken:     0.0000004,
			CompletionCostPerToken: 0.0000012,
			Source:                 "openrouter",
		},
	}); err != nil {
		t.Fatalf("upsert pricing: %v", err)
	}

	got, err := store.GetModelPricing(context.Background(), "openrouter", "openrouter/x-ai/grok-build-0.1")
	if err != nil {
		t.Fatalf("get prefixed pricing: %v", err)
	}
	if got.ModelID != "x-ai/grok-build-0.1" {
		t.Fatalf("expected normalized model id, got %q", got.ModelID)
	}
	if got.PromptCostPerToken != 0.0000004 || got.CompletionCostPerToken != 0.0000012 {
		t.Fatalf("unexpected pricing: %#v", got)
	}
}

// TestModelPricingUniversalPriceBook verifies that prices fetched from the
// OpenRouter catalog (always stored under provider "openrouter" with a
// "<upstream>/<model>" id) resolve for direct-provider routes such as Bifrost,
// so cost telemetry is not zero for non-OpenRouter providers.
func TestModelPricingUniversalPriceBook(t *testing.T) {
	store, err := NewSQLiteModelPricingStore(filepath.Join(t.TempDir(), "pricing.db"))
	if err != nil {
		t.Fatalf("new pricing store: %v", err)
	}
	defer store.Close()

	if err := store.UpsertModelPricing(context.Background(), []ModelPricingRecord{
		{
			Provider:               "openrouter",
			ModelID:                "anthropic/claude-haiku-4.5",
			PromptCostPerToken:     0.0000008,
			CompletionCostPerToken: 0.000004,
			Source:                 "openrouter",
		},
	}); err != nil {
		t.Fatalf("upsert pricing: %v", err)
	}

	cases := []struct {
		name     string
		provider string
		modelID  string
	}{
		{name: "direct provider, bare model", provider: "anthropic", modelID: "claude-haiku-4.5"},
		{name: "direct provider, qualified model", provider: "anthropic", modelID: "anthropic/claude-haiku-4.5"},
		{name: "mixed case", provider: "Anthropic", modelID: "Claude-Haiku-4.5"},
		{name: "openrouter route", provider: "openrouter", modelID: "anthropic/claude-haiku-4.5"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := store.GetModelPricing(context.Background(), tc.provider, tc.modelID)
			if err != nil {
				t.Fatalf("expected price book hit for %s/%s, got error: %v", tc.provider, tc.modelID, err)
			}
			if got.PromptCostPerToken != 0.0000008 || got.CompletionCostPerToken != 0.000004 {
				t.Fatalf("unexpected pricing: %#v", got)
			}
		})
	}

	if _, err := store.GetModelPricing(context.Background(), "anthropic", "no-such-model"); err == nil {
		t.Fatal("expected ErrModelPricingNotFound for an unknown model")
	}
}
