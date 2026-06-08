package mcpgateway

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
)

// RegisterPhase1ITools registers Phase 1I (Governed Delegation) and 1K (Self-Reported Audit) tools.
func RegisterPhase1ITools(s *Server, cfg *GatewayConfig) {
	s.RegisterTool(Tool{
		Name:        "create_context_manifest",
		Title:       "Create Context Manifest",
		Description: "Create a bounded context manifest for a governed session. Returns a manifest ID that can be passed to delegate_governed_work.",
		InputSchema: mustJSONSchema(map[string]any{
			"type": "object",
			"properties": map[string]any{
				"session_id": map[string]any{
					"type":        "string",
					"description": "The governed session ID",
				},
				"summary": map[string]any{
					"type":        "string",
					"description": "Brief summary of the bounded context",
				},
				"source_system": map[string]any{
					"type":        "string",
					"description": "Source system identifier (e.g., repo, jira, confluence)",
				},
				"source_object_id": map[string]any{
					"type":        "string",
					"description": "Object ID in the source system",
				},
				"classification": map[string]any{
					"type":        "string",
					"description": "Data classification level",
					"enum":        []string{"public", "internal", "confidential", "restricted"},
				},
				"cache_status": map[string]any{
					"type":        "string",
					"description": "Cache status for this context",
				},
			},
			"required": []string{"session_id", "summary", "source_system", "source_object_id", "classification"},
		}),
	}, handleCreateContextManifest(cfg))

	s.RegisterTool(Tool{
		Name:        "attach_use_case",
		Title:       "Attach Use Case",
		Description: "Register a use case in the governance registry. Use the returned ID when starting governed sessions.",
		InputSchema: mustJSONSchema(map[string]any{
			"type": "object",
			"properties": map[string]any{
				"id": map[string]any{
					"type":        "string",
					"description": "Unique use-case ID",
				},
				"owner": map[string]any{
					"type":        "string",
					"description": "Owner of the use case",
				},
				"domain": map[string]any{
					"type":        "string",
					"description": "Domain or team",
				},
				"expected_benefit": map[string]any{
					"type":        "string",
					"description": "Expected benefit or outcome",
				},
				"classification": map[string]any{
					"type":        "string",
					"description": "Data classification level",
					"enum":        []string{"public", "internal", "confidential", "restricted"},
				},
				"risk_level": map[string]any{
					"type":        "string",
					"description": "Risk level",
					"enum":        []string{"low", "medium", "high"},
				},
			},
			"required": []string{"id", "owner", "domain", "classification", "risk_level"},
		}),
	}, handleAttachUseCase(cfg))

	s.RegisterTool(Tool{
		Name:        "attach_workflow",
		Title:       "Attach Workflow",
		Description: "Register a workflow template in the governance registry. Use the returned ID when starting governed sessions.",
		InputSchema: mustJSONSchema(map[string]any{
			"type": "object",
			"properties": map[string]any{
				"id": map[string]any{
					"type":        "string",
					"description": "Unique workflow ID",
				},
				"name": map[string]any{
					"type":        "string",
					"description": "Workflow name",
				},
				"description": map[string]any{
					"type":        "string",
					"description": "Workflow description",
				},
				"stages": map[string]any{
					"type":        "array",
					"description": "Workflow stages",
					"items":       map[string]any{"type": "string"},
				},
			},
			"required": []string{"id", "name"},
		}),
	}, handleAttachWorkflow(cfg))

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

func handleCreateContextManifest(cfg *GatewayConfig) ToolHandler {
	return func(ctx context.Context, args json.RawMessage) (*ToolsCallResult, error) {
		var params struct {
			SessionID      string `json:"session_id"`
			Summary        string `json:"summary"`
			SourceSystem   string `json:"source_system"`
			SourceObjectID string `json:"source_object_id"`
			Classification string `json:"classification"`
			CacheStatus    string `json:"cache_status,omitempty"`
		}
		if err := json.Unmarshal(args, &params); err != nil {
			return nil, fmt.Errorf("invalid arguments: %w", err)
		}

		manifestID := contextManifestID(params.SessionID, params.SourceSystem, params.SourceObjectID)
		body := map[string]any{
			"id":               manifestID,
			"session_id":       params.SessionID,
			"summary":          params.Summary,
			"source_system":    params.SourceSystem,
			"source_object_id": params.SourceObjectID,
			"actor":            "ai-orch-mcp",
			"auth_scope":       "gateway_enforced",
			"classification":   params.Classification,
			"cache_status":     params.CacheStatus,
		}
		if params.CacheStatus == "" {
			body["cache_status"] = "fresh"
		}

		respBody, err := cfg.doJSON(ctx, http.MethodPost, cfg.GovernanceURL+"/v1/context-manifests", body,
			cfg.authHeaders(map[string]string{"Content-Type": "application/json"}))
		if err != nil {
			return nil, fmt.Errorf("create context manifest failed: %w", err)
		}

		var result struct {
			ID string `json:"id"`
		}
		_ = json.Unmarshal(respBody, &result)

		msg := fmt.Sprintf("Context manifest created:\n- manifest_id: %s\n- session: %s\n\nPass this manifest_id to delegate_governed_work.", result.ID, params.SessionID)
		return &ToolsCallResult{Content: []ContentItem{TextContent(msg)}}, nil
	}
}

func contextManifestID(sessionID string, sourceSystem string, sourceObjectID string) string {
	sum := sha256.Sum256([]byte(sessionID + "\x00" + sourceSystem + "\x00" + sourceObjectID))
	return "ctx_" + hex.EncodeToString(sum[:])[:24]
}

func handleAttachUseCase(cfg *GatewayConfig) ToolHandler {
	return func(ctx context.Context, args json.RawMessage) (*ToolsCallResult, error) {
		var params struct {
			ID              string `json:"id"`
			Owner           string `json:"owner"`
			Domain          string `json:"domain"`
			ExpectedBenefit string `json:"expected_benefit,omitempty"`
			Classification  string `json:"classification"`
			RiskLevel       string `json:"risk_level"`
		}
		if err := json.Unmarshal(args, &params); err != nil {
			return nil, fmt.Errorf("invalid arguments: %w", err)
		}

		body := map[string]any{
			"id":               params.ID,
			"owner":            params.Owner,
			"domain":           params.Domain,
			"classification":   params.Classification,
			"risk_level":       params.RiskLevel,
			"expected_benefit": params.ExpectedBenefit,
		}

		respBody, err := cfg.doJSON(ctx, http.MethodPost, cfg.GovernanceURL+"/v1/use-cases", body,
			cfg.authHeaders(map[string]string{"Content-Type": "application/json"}))
		if err != nil {
			return nil, fmt.Errorf("attach use case failed: %w", err)
		}

		var result struct {
			ID string `json:"id"`
		}
		_ = json.Unmarshal(respBody, &result)

		msg := fmt.Sprintf("Use case registered:\n- use_case_id: %s\n- owner: %s\n- domain: %s\n\nReference this ID when starting a governed session.", result.ID, params.Owner, params.Domain)
		return &ToolsCallResult{Content: []ContentItem{TextContent(msg)}}, nil
	}
}

func handleAttachWorkflow(cfg *GatewayConfig) ToolHandler {
	return func(ctx context.Context, args json.RawMessage) (*ToolsCallResult, error) {
		var params struct {
			ID          string   `json:"id"`
			Name        string   `json:"name"`
			Description string   `json:"description,omitempty"`
			Stages      []string `json:"stages,omitempty"`
		}
		if err := json.Unmarshal(args, &params); err != nil {
			return nil, fmt.Errorf("invalid arguments: %w", err)
		}

		body := map[string]any{
			"id":          params.ID,
			"name":        params.Name,
			"description": params.Description,
			"stages":      params.Stages,
		}

		respBody, err := cfg.doJSON(ctx, http.MethodPost, cfg.GovernanceURL+"/v1/workflows", body,
			cfg.authHeaders(map[string]string{"Content-Type": "application/json"}))
		if err != nil {
			return nil, fmt.Errorf("attach workflow failed: %w", err)
		}

		var result struct {
			ID string `json:"id"`
		}
		_ = json.Unmarshal(respBody, &result)

		msg := fmt.Sprintf("Workflow registered:\n- workflow_id: %s\n- name: %s\n- stages: %v\n\nReference this ID when starting a governed session.", result.ID, params.Name, params.Stages)
		return &ToolsCallResult{Content: []ContentItem{TextContent(msg)}}, nil
	}
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

		description := fmt.Sprintf("Self-reported native tool call: %s", params.ToolName)
		if params.Outcome != "" {
			description += fmt.Sprintf(" — outcome: %s", params.Outcome)
		}

		body := map[string]any{
			"session_id":       params.SessionID,
			"evidence_type":    "external_tool_call",
			"description":      description,
			"test_result":      params.Outcome,
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

		description := fmt.Sprintf("Self-reported native model call: %s", params.Model)
		if params.PromptTokens > 0 || params.CompletionTokens > 0 {
			description += fmt.Sprintf(" — tokens: %d prompt / %d completion", params.PromptTokens, params.CompletionTokens)
		}

		body := map[string]any{
			"session_id":       params.SessionID,
			"evidence_type":    "external_model_call",
			"description":      description,
			"test_result":      fmt.Sprintf("model=%s prompt_tokens=%d completion_tokens=%d cost_usd=%.4f", params.Model, params.PromptTokens, params.CompletionTokens, params.CostUSD),
			"trust_level":      "self_reported",
			"enforcement_mode": "advisory",
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
