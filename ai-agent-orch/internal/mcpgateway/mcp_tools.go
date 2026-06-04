package mcpgateway

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// GatewayConfig holds the configuration for MCP gateway tools.
type GatewayConfig struct {
	GovernanceURL string
	DevToken      string
	HTTPClient    *http.Client
}

// HTTPError captures non-2xx governance responses without losing status context.
type HTTPError struct {
	StatusCode int
	Body       string
}

func (e HTTPError) Error() string {
	return fmt.Sprintf("HTTP %d: %s", e.StatusCode, e.Body)
}

func (c *GatewayConfig) client() *http.Client {
	if c.HTTPClient != nil {
		return c.HTTPClient
	}
	return &http.Client{Timeout: 30 * time.Second}
}

func (c *GatewayConfig) authHeaders(extra map[string]string) map[string]string {
	h := map[string]string{
		"Authorization": "Bearer " + c.DevToken,
	}
	for k, v := range extra {
		h[k] = v
	}
	return h
}

func (c *GatewayConfig) doJSON(ctx context.Context, method, url string, body any, headers map[string]string) ([]byte, error) {
	var bodyReader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		bodyReader = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, bodyReader)
	if err != nil {
		return nil, err
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := c.client().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, HTTPError{StatusCode: resp.StatusCode, Body: string(respBody)}
	}
	return respBody, nil
}

// RegisterPhase1GTools registers the Phase 1G tools.
func RegisterPhase1GTools(s *Server, cfg *GatewayConfig) {
	s.RegisterTool(Tool{
		Name:        "mcp_doctor",
		Title:       "MCP Doctor",
		Description: "Check Governance Shell reachability, token setup, and gateway health.",
		InputSchema: mustJSONSchema(map[string]any{
			"type":       "object",
			"properties": map[string]any{},
		}),
	}, handleMCPDoctor(cfg))

	s.RegisterTool(Tool{
		Name:        "start_governed_session",
		Title:       "Start Governed Session",
		Description: "Create a new governed session with the Governance Shell.",
		InputSchema: mustJSONSchema(map[string]any{
			"type": "object",
			"properties": map[string]any{
				"agent": map[string]any{
					"type":        "string",
					"description": "Agent name (e.g., test-generation, playwright-specialist)",
				},
				"classification": map[string]any{
					"type":        "string",
					"description": "Data classification level (public, internal, confidential, restricted)",
					"enum":        []string{"public", "internal", "confidential", "restricted"},
				},
				"prompt": map[string]any{
					"type":        "string",
					"description": "The user prompt or task description",
				},
				"use_case_id": map[string]any{
					"type":        "string",
					"description": "Optional use-case ID for control-plane binding",
				},
				"workflow_id": map[string]any{
					"type":        "string",
					"description": "Optional workflow ID for control-plane binding",
				},
				"repo_url": map[string]any{
					"type":        "string",
					"description": "Optional repository URL",
				},
				"branch": map[string]any{
					"type":        "string",
					"description": "Optional branch name",
				},
				"intent": map[string]any{
					"type":        "string",
					"description": "Optional intent description",
				},
			},
			"required": []string{"agent", "classification", "prompt"},
		}),
	}, handleStartGovernedSession(cfg))
}

func handleMCPDoctor(cfg *GatewayConfig) ToolHandler {
	return func(ctx context.Context, args json.RawMessage) (*ToolsCallResult, error) {
		var results []string

		// Check gateway self health.
		results = append(results, fmt.Sprintf("Gateway: %s (ok)", cfg.GovernanceURL))

		// Check governance shell health.
		healthURL := cfg.GovernanceURL + "/healthz"
		_, err := cfg.doJSON(ctx, http.MethodGet, healthURL, nil, cfg.authHeaders(nil))
		if err != nil {
			results = append(results, fmt.Sprintf("Governance Shell health: FAIL (%v)", err))
		} else {
			results = append(results, "Governance Shell health: OK")
		}

		// Check governance shell readyz.
		readyzURL := cfg.GovernanceURL + "/readyz"
		_, err = cfg.doJSON(ctx, http.MethodGet, readyzURL, nil, cfg.authHeaders(nil))
		if err != nil {
			results = append(results, fmt.Sprintf("Governance Shell readyz: FAIL (%v)", err))
		} else {
			results = append(results, "Governance Shell readyz: OK")
		}

		// Check token.
		if cfg.DevToken == "" {
			results = append(results, "Dev token: NOT SET (HTTP gateway tools will fail closed)")
		} else {
			results = append(results, "Dev token: SET")
		}

		report := "MCP Doctor Report\n"
		for _, r := range results {
			report += "- " + r + "\n"
		}

		return &ToolsCallResult{
			Content: []ContentItem{TextContent(report)},
		}, nil
	}
}

func handleStartGovernedSession(cfg *GatewayConfig) ToolHandler {
	return func(ctx context.Context, args json.RawMessage) (*ToolsCallResult, error) {
		var params struct {
			Agent          string `json:"agent"`
			Classification string `json:"classification"`
			Prompt         string `json:"prompt"`
			UseCaseID      string `json:"use_case_id,omitempty"`
			WorkflowID     string `json:"workflow_id,omitempty"`
			RepoURL        string `json:"repo_url,omitempty"`
			Branch         string `json:"branch,omitempty"`
			Intent         string `json:"intent,omitempty"`
		}
		if err := json.Unmarshal(args, &params); err != nil {
			return nil, fmt.Errorf("invalid arguments: %w", err)
		}

		body := map[string]any{
			"agent":          params.Agent,
			"classification": params.Classification,
			"prompt":         params.Prompt,
		}
		if params.UseCaseID != "" {
			body["use_case_id"] = params.UseCaseID
		}
		if params.WorkflowID != "" {
			body["workflow_id"] = params.WorkflowID
		}
		if params.RepoURL != "" {
			body["repo_url"] = params.RepoURL
		}
		if params.Branch != "" {
			body["branch"] = params.Branch
		}
		if params.Intent != "" {
			body["intent"] = params.Intent
		}

		respBody, err := cfg.doJSON(ctx, http.MethodPost, cfg.GovernanceURL+"/v1/sessions", body,
			cfg.authHeaders(map[string]string{"Content-Type": "application/json"}))
		if err != nil {
			return nil, fmt.Errorf("create session failed: %w", err)
		}

		var result struct {
			SessionID string `json:"session_id"`
			Status    string `json:"status"`
		}
		if err := json.Unmarshal(respBody, &result); err != nil {
			return nil, fmt.Errorf("parse session response: %w", err)
		}

		msg := fmt.Sprintf("Governed session created:\n- session_id: %s\n- status: %s\n\nNext steps:\n1. Send a message with the session ID to trigger routing.\n2. Confirm the recommended specialist.\n3. Stream events to receive patch proposals.",
			result.SessionID, result.Status)

		return &ToolsCallResult{
			Content: []ContentItem{TextContent(msg)},
		}, nil
	}
}

func mustJSONSchema(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}
