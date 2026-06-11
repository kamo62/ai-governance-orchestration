package governance

import (
	"context"
	"errors"

	"github.com/kamo62/ai-governance-orchestration/ai-agent-orch/internal/audit"
)

// SessionUsageSummary aggregates token and cost signals for a governed session.
type SessionUsageSummary struct {
	TotalTokens              int     `json:"total_tokens"`
	PromptTokens             int     `json:"prompt_tokens"`
	CompletionTokens         int     `json:"completion_tokens"`
	EstimatedCostUSD         float64 `json:"estimated_cost_usd"`
	ModelProxyCalls          int     `json:"model_proxy_calls"`
	MCPProxyCalls            int     `json:"mcp_proxy_calls"`
	TurnCount                int     `json:"turn_count"`
	ModelAlias               string  `json:"model_alias,omitempty"`
	ModelResolved            string  `json:"model_resolved,omitempty"`
	Provider                 string  `json:"provider,omitempty"`
	CredentialSource         string  `json:"credential_source,omitempty"`
	ReasoningEffortRequested string  `json:"reasoning_effort_requested,omitempty"`
	ReasoningEffortApplied   string  `json:"reasoning_effort_applied,omitempty"`
	ReasoningSource          string  `json:"reasoning_source,omitempty"`
	GatewayBackend           string  `json:"gateway_backend,omitempty"`
	CostSource               string  `json:"cost_source,omitempty"`
}

func SummarizeSessionUsageWithPricing(ctx context.Context, events []audit.Event, pricing ModelPricingStore) SessionUsageSummary {
	var summary SessionUsageSummary
	for _, event := range events {
		switch event.EventType {
		case "model.proxy_call", "model.gateway_call", "model.gateway_responses", "model.gateway_stream.completed":
			summary.ModelProxyCalls++
			rememberModelAttribution(&summary, event)
			addTokenUsage(&summary, event.TokenUsage)
			// Add cost exactly once per call with a clear precedence:
			// provider-reported cost, then a pricing-table estimate, then any
			// caller-supplied estimate on the event. This avoids double-counting
			// when more than one signal is present.
			if cost := floatFromAny(event.TokenUsage["cost_usd"]); cost > 0 {
				summary.EstimatedCostUSD += cost
				summary.CostSource = mergeCostSource(summary.CostSource, "provider_reported")
			} else if estimate, ok := estimateCostFromPricing(ctx, event, pricing); ok {
				summary.EstimatedCostUSD += estimate
				summary.CostSource = mergeCostSource(summary.CostSource, "pricing_table")
			} else if event.EstimatedCostUSD > 0 {
				summary.EstimatedCostUSD += event.EstimatedCostUSD
				summary.CostSource = mergeCostSource(summary.CostSource, "caller_estimated")
			} else if event.TokenUsage != nil && summary.CostSource == "" {
				summary.CostSource = "unavailable"
			}
		case "mcp.proxy_call":
			if event.Reason == "forwarded" {
				summary.MCPProxyCalls++
			}
		case "session.turn.requested", "session.turn.confirmed":
			summary.TurnCount++
		case "session.created":
			if event.EstimatedCostUSD > 0 {
				summary.EstimatedCostUSD += event.EstimatedCostUSD
			}
		}
	}
	return summary
}

func rememberModelAttribution(summary *SessionUsageSummary, event audit.Event) {
	if summary == nil {
		return
	}
	if event.ModelAlias != "" {
		summary.ModelAlias = event.ModelAlias
	}
	if event.ModelResolved != "" {
		summary.ModelResolved = event.ModelResolved
	}
	if event.Provider != "" {
		summary.Provider = event.Provider
	}
	if event.CredentialSource != "" {
		summary.CredentialSource = event.CredentialSource
	}
	if event.ReasoningEffortRequested != "" {
		summary.ReasoningEffortRequested = event.ReasoningEffortRequested
	}
	if event.ReasoningEffortApplied != "" {
		summary.ReasoningEffortApplied = event.ReasoningEffortApplied
	}
	if event.ReasoningSource != "" {
		summary.ReasoningSource = event.ReasoningSource
	}
	if event.GatewayBackend != "" {
		summary.GatewayBackend = event.GatewayBackend
	}
}

func estimateCostFromPricing(ctx context.Context, event audit.Event, pricing ModelPricingStore) (float64, bool) {
	if pricing == nil || event.TokenUsage == nil {
		return 0, false
	}
	modelID := event.ModelResolved
	if modelID == "" {
		modelID = event.ModelAlias
	}
	record, err := pricing.GetModelPricing(ctx, event.Provider, modelID)
	if err != nil {
		if errors.Is(err, ErrModelPricingNotFound) {
			return 0, false
		}
		return 0, false
	}
	promptTokens := intFromAny(event.TokenUsage["prompt_tokens"])
	completionTokens := intFromAny(event.TokenUsage["completion_tokens"])
	estimate := float64(promptTokens)*record.PromptCostPerToken + float64(completionTokens)*record.CompletionCostPerToken
	return estimate, estimate > 0
}

func addTokenUsage(summary *SessionUsageSummary, usage map[string]any) {
	if summary == nil || len(usage) == 0 {
		return
	}
	summary.TotalTokens += intFromAny(usage["total_tokens"])
	prompt := intFromAny(usage["prompt_tokens"])
	if prompt == 0 {
		prompt = intFromAny(usage["input_tokens"])
	}
	completion := intFromAny(usage["completion_tokens"])
	if completion == 0 {
		completion = intFromAny(usage["output_tokens"])
	}
	summary.PromptTokens += prompt
	summary.CompletionTokens += completion
	if intFromAny(usage["total_tokens"]) == 0 {
		summary.TotalTokens += prompt + completion
	}
}

func mergeCostSource(current string, next string) string {
	if next == "" || current == next {
		return current
	}
	if current == "" || current == "unavailable" {
		return next
	}
	return "mixed"
}

func intFromAny(value any) int {
	switch typed := value.(type) {
	case int:
		return typed
	case int64:
		return int(typed)
	case float64:
		return int(typed)
	case float32:
		return int(typed)
	default:
		return 0
	}
}

func floatFromAny(value any) float64 {
	switch typed := value.(type) {
	case float64:
		return typed
	case float32:
		return float64(typed)
	case int:
		return float64(typed)
	case int64:
		return float64(typed)
	default:
		return 0
	}
}
