package governance

import (
	"context"
	"math"
	"testing"

	"ai-agent-orch/internal/audit"
)

func TestSummarizeSessionUsageAggregatesModelAndMCPEvents(t *testing.T) {
	events := []audit.Event{
		{
			EventType: "model.proxy_call",
			TokenUsage: map[string]any{
				"total_tokens":      float64(100),
				"prompt_tokens":     float64(40),
				"completion_tokens": float64(60),
			},
		},
		{
			EventType: "mcp.proxy_call",
			Reason:    "forwarded",
		},
		{
			EventType: "session.turn.requested",
		},
	}
	summary := SummarizeSessionUsage(events)
	if summary.TotalTokens != 100 {
		t.Fatalf("expected 100 total tokens, got %d", summary.TotalTokens)
	}
	if summary.ModelProxyCalls != 1 || summary.MCPProxyCalls != 1 {
		t.Fatalf("unexpected call counts: %#v", summary)
	}
	if summary.TurnCount != 1 {
		t.Fatalf("expected 1 turn, got %d", summary.TurnCount)
	}
}

func TestSummarizeSessionUsageAggregatesModelGatewayEvents(t *testing.T) {
	events := []audit.Event{
		{
			EventType:      "model.gateway_stream.completed",
			Provider:       "openrouter",
			ModelAlias:     "coding-fast",
			ModelResolved:  "openrouter/x-ai/grok-build-0.1",
			GatewayBackend: "bifrost",
			TokenUsage: map[string]any{
				"total_tokens":      float64(16),
				"prompt_tokens":     float64(12),
				"completion_tokens": float64(4),
				"cost_usd":          float64(0.00002),
			},
		},
	}

	summary := SummarizeSessionUsage(events)

	if summary.TotalTokens != 16 || summary.PromptTokens != 12 || summary.CompletionTokens != 4 {
		t.Fatalf("unexpected token summary: %#v", summary)
	}
	if summary.EstimatedCostUSD != 0.00002 {
		t.Fatalf("expected provider cost 0.00002, got %v", summary.EstimatedCostUSD)
	}
	if summary.ModelAlias != "coding-fast" || summary.ModelResolved != "openrouter/x-ai/grok-build-0.1" || summary.GatewayBackend != "bifrost" {
		t.Fatalf("expected model attribution, got %#v", summary)
	}
	if summary.CostSource != "provider_reported" {
		t.Fatalf("expected provider_reported cost source, got %q", summary.CostSource)
	}
}

func TestSummarizeSessionUsageEstimatesMissingCostFromPricingTable(t *testing.T) {
	events := []audit.Event{
		{
			EventType:     "model.gateway_call",
			Provider:      "openrouter",
			ModelAlias:    "coding-fast",
			ModelResolved: "openrouter/x-ai/grok-build-0.1",
			TokenUsage: map[string]any{
				"prompt_tokens":     float64(12),
				"completion_tokens": float64(4),
				"total_tokens":      float64(16),
			},
		},
	}
	pricing := fakeModelPricingStore{
		record: ModelPricingRecord{
			Provider:               "openrouter",
			ModelID:                "x-ai/grok-build-0.1",
			PromptCostPerToken:     0.000001,
			CompletionCostPerToken: 0.000002,
		},
	}

	summary := SummarizeSessionUsageWithPricing(context.Background(), events, pricing)
	want := 12*0.000001 + 4*0.000002
	if math.Abs(summary.EstimatedCostUSD-want) > 0.000000001 {
		t.Fatalf("expected pricing-table estimate %v, got %v", want, summary.EstimatedCostUSD)
	}
	if summary.CostSource != "pricing_table" {
		t.Fatalf("expected pricing_table cost source, got %q", summary.CostSource)
	}
}

func TestSummarizeSessionUsageIncludesResponsesGatewayEvents(t *testing.T) {
	events := []audit.Event{
		{
			EventType:     "model.gateway_responses",
			Provider:      "openrouter",
			ModelAlias:    "coding-balanced",
			ModelResolved: "openrouter/anthropic/claude-sonnet-4.5",
			TokenUsage: map[string]any{
				"prompt_tokens":     float64(30),
				"completion_tokens": float64(20),
				"total_tokens":      float64(50),
				"cost_usd":          float64(0.003),
			},
		},
	}

	summary := SummarizeSessionUsage(events)

	if summary.ModelProxyCalls != 1 || summary.TotalTokens != 50 || summary.EstimatedCostUSD != 0.003 {
		t.Fatalf("expected responses gateway usage in summary, got %#v", summary)
	}
	if summary.ModelAlias != "coding-balanced" {
		t.Fatalf("expected responses model attribution, got %#v", summary)
	}
}

type fakeModelPricingStore struct {
	record ModelPricingRecord
}

func (s fakeModelPricingStore) UpsertModelPricing(context.Context, []ModelPricingRecord) error {
	return nil
}

func (s fakeModelPricingStore) GetModelPricing(_ context.Context, provider string, modelID string) (ModelPricingRecord, error) {
	if provider == s.record.Provider && (modelID == s.record.ModelID || modelID == "openrouter/"+s.record.ModelID) {
		return s.record, nil
	}
	return ModelPricingRecord{}, ErrModelPricingNotFound
}
