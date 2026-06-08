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
	// TrustedClientToken proves to the Governance Shell that this process is a
	// trusted gateway, so its requests may be recorded at the gateway_enforced
	// trust level. When empty, the shell falls back to honoring the client
	// identity header alone (local dev).
	TrustedClientToken string
	HTTPClient         *http.Client
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
	// The trust level is decided server-side from the client identity (and, when
	// configured, the trusted-client secret). The client does not declare its own
	// trust level, so no X-AI-Orch-Trust-Level header is sent.
	h := map[string]string{
		"Authorization":    "Bearer " + c.DevToken,
		"X-AI-Orch-Client": "ai-orch-mcp",
	}
	if c.TrustedClientToken != "" {
		h["X-AI-Orch-Trusted-Client-Token"] = c.TrustedClientToken
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
					"description": "Agent name (e.g., unit-tests, code-review)",
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

	s.RegisterTool(Tool{
		Name:        "start_governed_run",
		Title:       "Start Governed Run",
		Description: "Create a governed session, route the first prompt, and return the next approval gate.",
		InputSchema: mustJSONSchema(map[string]any{
			"type": "object",
			"properties": map[string]any{
				"agent": map[string]any{
					"type":        "string",
					"description": "Agent name (e.g., unit-tests, code-review)",
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
				"work_item_id": map[string]any{
					"type":        "string",
					"description": "Optional Jira, Azure DevOps, GitHub issue, or local work item ID",
				},
				"work_item_type": map[string]any{
					"type":        "string",
					"description": "Optional work type such as frontend, backend, test, docs, refactor, security, or bugfix",
				},
				"repo_url": map[string]any{
					"type":        "string",
					"description": "Optional repository URL",
				},
				"branch": map[string]any{
					"type":        "string",
					"description": "Optional branch name",
				},
				"commit_sha": map[string]any{
					"type":        "string",
					"description": "Optional commit SHA",
				},
				"intent": map[string]any{
					"type":        "string",
					"description": "Optional intent description",
				},
				"permission_mode": map[string]any{
					"type":        "string",
					"description": "Permission mode for this run",
					"enum":        []string{"read_only", "reviewed", "auto_apply", "full_access"},
				},
				"approval_mode": map[string]any{
					"type":        "string",
					"description": "Approval mode reported for this run",
					"enum":        []string{"manual", "auto_approved", "yolo", "self_reported"},
				},
				"workspace_mode": map[string]any{
					"type":        "string",
					"description": "Optional workspace mode such as local, worktree, or container",
				},
			},
			"required": []string{"agent", "classification", "prompt"},
		}),
	}, handleStartGovernedRun(cfg))
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

func handleStartGovernedRun(cfg *GatewayConfig) ToolHandler {
	return func(ctx context.Context, args json.RawMessage) (*ToolsCallResult, error) {
		var params struct {
			Agent          string `json:"agent"`
			Classification string `json:"classification"`
			Prompt         string `json:"prompt"`
			UseCaseID      string `json:"use_case_id,omitempty"`
			WorkflowID     string `json:"workflow_id,omitempty"`
			WorkItemID     string `json:"work_item_id,omitempty"`
			WorkItemType   string `json:"work_item_type,omitempty"`
			RepoURL        string `json:"repo_url,omitempty"`
			Branch         string `json:"branch,omitempty"`
			CommitSHA      string `json:"commit_sha,omitempty"`
			Intent         string `json:"intent,omitempty"`
			PermissionMode string `json:"permission_mode,omitempty"`
			ApprovalMode   string `json:"approval_mode,omitempty"`
			WorkspaceMode  string `json:"workspace_mode,omitempty"`
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
		if params.WorkItemID != "" {
			body["work_item_id"] = params.WorkItemID
		}
		if params.WorkItemType != "" {
			body["work_item_type"] = params.WorkItemType
		}
		if params.RepoURL != "" {
			body["repo_url"] = params.RepoURL
		}
		if params.Branch != "" {
			body["branch"] = params.Branch
		}
		if params.CommitSHA != "" {
			body["commit_sha"] = params.CommitSHA
		}
		if params.Intent != "" {
			body["intent"] = params.Intent
		}
		if params.PermissionMode != "" {
			body["permission_mode"] = params.PermissionMode
		}
		if params.ApprovalMode != "" {
			body["approval_mode"] = params.ApprovalMode
		}
		if params.WorkspaceMode != "" {
			body["workspace_mode"] = params.WorkspaceMode
		}

		respBody, err := cfg.doJSON(ctx, http.MethodPost, cfg.GovernanceURL+"/v1/runs", body,
			cfg.authHeaders(map[string]string{"Content-Type": "application/json"}))
		if err != nil {
			return nil, fmt.Errorf("start governed run failed: %w", err)
		}

		var result struct {
			RunID      string `json:"run_id"`
			SessionID  string `json:"session_id"`
			Status     string `json:"status"`
			Specialist string `json:"specialist"`
			NextGate   string `json:"next_gate"`
			SSEURL     string `json:"sse_url"`
		}
		if err := json.Unmarshal(respBody, &result); err != nil {
			return nil, fmt.Errorf("parse run response: %w", err)
		}

		msg := fmt.Sprintf("Governed run started:\n- run_id: %s\n- session_id: %s\n- status: %s\n- specialist: %s\n- next_gate: %s\n- events: %s",
			result.RunID, result.SessionID, result.Status, result.Specialist, result.NextGate, result.SSEURL)

		return &ToolsCallResult{
			Content: []ContentItem{TextContent(msg)},
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
