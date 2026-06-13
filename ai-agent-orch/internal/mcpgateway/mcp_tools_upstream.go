package mcpgateway

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
)

// RegisterPhase1JTools registers Phase 1J (Gateway-Enforced Tool Calls) tools.
func RegisterPhase1JTools(s *Server, cfg *GatewayConfig) {
	s.RegisterTool(Tool{
		Name:        "list_allowed_tools",
		Title:       "List Allowed Tools",
		Description: "List upstream MCP tools available through the governance gateway, filtered by policy.",
		InputSchema: mustJSONSchema(map[string]any{
			"type": "object",
			"properties": map[string]any{
				"session_id": map[string]any{
					"type":        "string",
					"description": "Governed session ID used for policy-filtered tool listing",
				},
			},
			"required": []string{"session_id"},
		}),
	}, handleListAllowedTools(cfg))

	s.RegisterTool(Tool{
		Name:        "call_governed_tool",
		Title:       "Call Governed Tool",
		Description: "Call an upstream MCP tool through the Governance Shell. The call is policy-checked, credential-safe and audited as gateway_enforced.",
		InputSchema: mustJSONSchema(map[string]any{
			"type": "object",
			"properties": map[string]any{
				"server_id": map[string]any{
					"type":        "string",
					"description": "Upstream MCP server ID (e.g., repo-classification, documentation)",
				},
				"tool_name": map[string]any{
					"type":        "string",
					"description": "Name of the tool to call",
				},
				"arguments": map[string]any{
					"type":        "object",
					"description": "Tool arguments as a JSON object",
				},
				"session_id": map[string]any{
					"type":        "string",
					"description": "Governed session ID for policy, audit and correlation",
				},
			},
			"required": []string{"server_id", "tool_name", "session_id"},
		}),
	}, handleCallGovernedTool(cfg))
}

func handleListAllowedTools(cfg *GatewayConfig) ToolHandler {
	return func(ctx context.Context, args json.RawMessage) (*ToolsCallResult, error) {
		var params struct {
			SessionID string `json:"session_id"`
		}
		if err := json.Unmarshal(args, &params); err != nil {
			return nil, fmt.Errorf("invalid arguments: %w", err)
		}
		if params.SessionID == "" {
			return nil, fmt.Errorf("session_id is required")
		}
		headers := cfg.authHeaders(nil)
		headers["X-AI-Orch-Session-ID"] = params.SessionID
		respBody, err := cfg.doJSON(ctx, http.MethodGet, cfg.GovernanceURL+"/internal/v1/mcp/catalog", nil, headers)
		if err != nil {
			return nil, fmt.Errorf("list allowed tools failed: %w", err)
		}

		var catalog struct {
			Servers map[string]map[string]any `json:"servers"`
		}
		if err := json.Unmarshal(respBody, &catalog); err != nil {
			return nil, fmt.Errorf("parse catalog failed: %w", err)
		}

		msg := "Allowed upstream tools:\n"
		for serverID, info := range catalog.Servers {
			msg += fmt.Sprintf("\n%s:\n", serverID)
			if tools, ok := info["tools"].([]any); ok {
				for _, t := range tools {
					msg += fmt.Sprintf("  - %v\n", t)
				}
			}
		}

		return &ToolsCallResult{Content: []ContentItem{TextContent(msg)}}, nil
	}
}

func handleCallGovernedTool(cfg *GatewayConfig) ToolHandler {
	return func(ctx context.Context, args json.RawMessage) (*ToolsCallResult, error) {
		var params struct {
			ServerID  string         `json:"server_id"`
			ToolName  string         `json:"tool_name"`
			Arguments map[string]any `json:"arguments,omitempty"`
			SessionID string         `json:"session_id"`
		}
		if err := json.Unmarshal(args, &params); err != nil {
			return nil, fmt.Errorf("invalid arguments: %w", err)
		}
		if params.SessionID == "" {
			return nil, fmt.Errorf("session_id is required")
		}

		toolPath := url.PathEscape(params.ServerID) + "/tools/" + url.PathEscape(params.ToolName)
		endpoint := cfg.GovernanceURL + "/internal/v1/mcp/" + toolPath

		headers := cfg.authHeaders(map[string]string{"Content-Type": "application/json"})
		headers["X-AI-Orch-Session-ID"] = params.SessionID

		respBody, err := cfg.doJSON(ctx, http.MethodPost, endpoint, params.Arguments, headers)
		if err != nil {
			return nil, fmt.Errorf("call governed tool failed: %w", err)
		}

		return &ToolsCallResult{Content: []ContentItem{TextContent(string(respBody))}}, nil
	}
}
