package mcpgateway

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
)

// RegisterPhase1ITools registers Phase 1I (Governed Delegation) and 1K (Self-Reported Audit) tools.
func RegisterPhase1ITools(s *Server, cfg *GatewayConfig) {
	s.RegisterTool(Tool{
		Name:        "delegate_governed_work",
		Title:       "Delegate Governed Work",
		Description: "Send a prompt to a governed session and receive routing results. The Governance Shell resolves policy, chooses the model, and buffers any patch proposals.",
		InputSchema: mustJSONSchema(map[string]any{
			"type": "object",
			"properties": map[string]any{
				"session_id": map[string]any{
					"type":        "string",
					"description": "The governed session ID",
				},
				"prompt": map[string]any{
					"type":        "string",
					"description": "The work prompt or task description",
				},
				"context_manifest_id": map[string]any{
					"type":        "string",
					"description": "Optional context manifest ID",
				},
			},
			"required": []string{"session_id", "prompt"},
		}),
	}, handleDelegateGovernedWork(cfg))

	s.RegisterTool(Tool{
		Name:        "record_patch_decision",
		Title:       "Record Patch Decision",
		Description: "Record a decision for a proposed patch (applied, rejected, or partially_applied).",
		InputSchema: mustJSONSchema(map[string]any{
			"type": "object",
			"properties": map[string]any{
				"session_id": map[string]any{
					"type":        "string",
					"description": "The governed session ID",
				},
				"patch_id": map[string]any{
					"type":        "string",
					"description": "The patch ID",
				},
				"decision": map[string]any{
					"type":        "string",
					"description": "The decision: applied, rejected, or partially_applied",
					"enum":        []string{"applied", "rejected", "partially_applied"},
				},
				"reason": map[string]any{
					"type":        "string",
					"description": "Optional reason for the decision",
				},
			},
			"required": []string{"session_id", "patch_id", "decision"},
		}),
	}, handleRecordPatchDecision(cfg))

	s.RegisterTool(Tool{
		Name:        "lookup_audit",
		Title:       "Lookup Audit",
		Description: "Retrieve audit events and evidence for a governed session.",
		InputSchema: mustJSONSchema(map[string]any{
			"type": "object",
			"properties": map[string]any{
				"session_id": map[string]any{
					"type":        "string",
					"description": "The governed session ID",
				},
			},
			"required": []string{"session_id"},
		}),
	}, handleLookupAudit(cfg))

	s.RegisterTool(Tool{
		Name:        "record_external_tool_call",
		Title:       "Record External Tool Call",
		Description: "Self-report a native tool call that did not route through the gateway. Marked with trust_level: self_reported.",
		InputSchema: mustJSONSchema(map[string]any{
			"type": "object",
			"properties": map[string]any{
				"session_id": map[string]any{
					"type":        "string",
					"description": "The governed session ID",
				},
				"tool_name": map[string]any{
					"type":        "string",
					"description": "Name of the tool that was called",
				},
				"arguments": map[string]any{
					"type":        "object",
					"description": "Arguments passed to the tool",
				},
				"outcome": map[string]any{
					"type":        "string",
					"description": "Brief description of the outcome",
				},
			},
			"required": []string{"session_id", "tool_name"},
		}),
	}, handleRecordExternalToolCall(cfg))

	s.RegisterTool(Tool{
		Name:        "record_external_model_call",
		Title:       "Record External Model Call",
		Description: "Self-report a native model call that did not route through the gateway. Marked with trust_level: self_reported.",
		InputSchema: mustJSONSchema(map[string]any{
			"type": "object",
			"properties": map[string]any{
				"session_id": map[string]any{
					"type":        "string",
					"description": "The governed session ID",
				},
				"model": map[string]any{
					"type":        "string",
					"description": "Model identifier used",
				},
				"prompt_tokens": map[string]any{
					"type":        "integer",
					"description": "Estimated prompt tokens",
				},
				"completion_tokens": map[string]any{
					"type":        "integer",
					"description": "Estimated completion tokens",
				},
				"cost_usd": map[string]any{
					"type":        "number",
					"description": "Estimated cost in USD",
				},
			},
			"required": []string{"session_id", "model"},
		}),
	}, handleRecordExternalModelCall(cfg))
}

func handleDelegateGovernedWork(cfg *GatewayConfig) ToolHandler {
	return func(ctx context.Context, args json.RawMessage) (*ToolsCallResult, error) {
		var params struct {
			SessionID         string `json:"session_id"`
			Prompt            string `json:"prompt"`
			ContextManifestID string `json:"context_manifest_id,omitempty"`
		}
		if err := json.Unmarshal(args, &params); err != nil {
			return nil, fmt.Errorf("invalid arguments: %w", err)
		}

		body := map[string]any{"prompt": params.Prompt}
		if params.ContextManifestID != "" {
			body["context_manifest_id"] = params.ContextManifestID
		}
		sessionID := url.PathEscape(params.SessionID)

		respBody, err := cfg.doJSON(ctx, http.MethodPost,
			fmt.Sprintf("%s/v1/sessions/%s/messages", cfg.GovernanceURL, sessionID),
			body, cfg.authHeaders(map[string]string{"Content-Type": "application/json"}))
		if err != nil {
			return nil, fmt.Errorf("delegate work failed: %w", err)
		}

		var result struct {
			Specialist string `json:"specialist"`
			Reason     string `json:"reason"`
			Status     string `json:"status"`
		}
		_ = json.Unmarshal(respBody, &result)

		msg := fmt.Sprintf("Work delegated:\n- specialist: %s\n- reason: %s\n- status: %s\n\nNext: confirm the specialist and stream events to receive patch proposals.",
			result.Specialist, result.Reason, result.Status)

		return &ToolsCallResult{Content: []ContentItem{TextContent(msg)}}, nil
	}
}

func handleRecordPatchDecision(cfg *GatewayConfig) ToolHandler {
	return func(ctx context.Context, args json.RawMessage) (*ToolsCallResult, error) {
		var params struct {
			SessionID string `json:"session_id"`
			PatchID   string `json:"patch_id"`
			Decision  string `json:"decision"`
			Reason    string `json:"reason,omitempty"`
		}
		if err := json.Unmarshal(args, &params); err != nil {
			return nil, fmt.Errorf("invalid arguments: %w", err)
		}

		body := map[string]any{
			"patch_id": params.PatchID,
			"decision": params.Decision,
		}
		if params.Reason != "" {
			body["reason"] = params.Reason
		}
		sessionID := url.PathEscape(params.SessionID)

		_, err := cfg.doJSON(ctx, http.MethodPost,
			fmt.Sprintf("%s/v1/sessions/%s/patch-decision", cfg.GovernanceURL, sessionID),
			body, cfg.authHeaders(map[string]string{"Content-Type": "application/json"}))
		if err != nil {
			return nil, fmt.Errorf("record patch decision failed: %w", err)
		}

		return &ToolsCallResult{Content: []ContentItem{TextContent(fmt.Sprintf("Patch decision recorded: %s for patch %s", params.Decision, params.PatchID))}}, nil
	}
}

func handleLookupAudit(cfg *GatewayConfig) ToolHandler {
	return func(ctx context.Context, args json.RawMessage) (*ToolsCallResult, error) {
		var params struct {
			SessionID string `json:"session_id"`
		}
		if err := json.Unmarshal(args, &params); err != nil {
			return nil, fmt.Errorf("invalid arguments: %w", err)
		}
		sessionID := url.PathEscape(params.SessionID)

		respBody, err := cfg.doJSON(ctx, http.MethodGet,
			fmt.Sprintf("%s/v1/audit/sessions/%s", cfg.GovernanceURL, sessionID),
			nil, cfg.authHeaders(nil))
		if err != nil {
			return nil, fmt.Errorf("lookup audit failed: %w", err)
		}

		var audit struct {
			Events []map[string]any `json:"events"`
		}
		_ = json.Unmarshal(respBody, &audit)

		msg := fmt.Sprintf("Audit lookup for session %s:\n- %d event(s) found\n\n", params.SessionID, len(audit.Events))
		for i, evt := range audit.Events {
			if i >= 5 {
				msg += "... (truncated)\n"
				break
			}
			typ, _ := evt["event_type"].(string)
			actor, _ := evt["actor"].(string)
			msg += fmt.Sprintf("- %s by %s\n", typ, actor)
		}

		return &ToolsCallResult{Content: []ContentItem{TextContent(msg)}}, nil
	}
}

func handleRecordExternalToolCall(cfg *GatewayConfig) ToolHandler {
	return func(ctx context.Context, args json.RawMessage) (*ToolsCallResult, error) {
		var params struct {
			SessionID string         `json:"session_id"`
			ToolName  string         `json:"tool_name"`
			Arguments map[string]any `json:"arguments,omitempty"`
			Outcome   string         `json:"outcome,omitempty"`
		}
		if err := json.Unmarshal(args, &params); err != nil {
			return nil, fmt.Errorf("invalid arguments: %w", err)
		}

		// Submit as an evidence record with trust_level: self_reported.
		body := map[string]any{
			"session_id":       params.SessionID,
			"record_type":      "external_tool_call",
			"tool_name":        params.ToolName,
			"arguments":        params.Arguments,
			"outcome":          params.Outcome,
			"trust_level":      "self_reported",
			"enforcement_mode": "advisory",
		}

		_, err := cfg.doJSON(ctx, http.MethodPost,
			fmt.Sprintf("%s/v1/evidence", cfg.GovernanceURL),
			body, cfg.authHeaders(map[string]string{"Content-Type": "application/json"}))
		if err != nil {
			return nil, fmt.Errorf("record self-reported tool call failed: %w", err)
		}

		return &ToolsCallResult{Content: []ContentItem{TextContent(fmt.Sprintf("Self-reported tool call recorded (trust_level: self_reported): %s", params.ToolName))}}, nil
	}
}

func handleRecordExternalModelCall(cfg *GatewayConfig) ToolHandler {
	return func(ctx context.Context, args json.RawMessage) (*ToolsCallResult, error) {
		var params struct {
			SessionID        string  `json:"session_id"`
			Model            string  `json:"model"`
			PromptTokens     int     `json:"prompt_tokens,omitempty"`
			CompletionTokens int     `json:"completion_tokens,omitempty"`
			CostUSD          float64 `json:"cost_usd,omitempty"`
		}
		if err := json.Unmarshal(args, &params); err != nil {
			return nil, fmt.Errorf("invalid arguments: %w", err)
		}

		body := map[string]any{
			"session_id":        params.SessionID,
			"record_type":       "external_model_call",
			"model":             params.Model,
			"prompt_tokens":     params.PromptTokens,
			"completion_tokens": params.CompletionTokens,
			"cost_usd":          params.CostUSD,
			"trust_level":       "self_reported",
			"enforcement_mode":  "advisory",
		}

		_, err := cfg.doJSON(ctx, http.MethodPost,
			fmt.Sprintf("%s/v1/evidence", cfg.GovernanceURL),
			body, cfg.authHeaders(map[string]string{"Content-Type": "application/json"}))
		if err != nil {
			return nil, fmt.Errorf("record self-reported model call failed: %w", err)
		}

		return &ToolsCallResult{Content: []ContentItem{TextContent(fmt.Sprintf("Self-reported model call recorded (trust_level: self_reported): %s", params.Model))}}, nil
	}
}
