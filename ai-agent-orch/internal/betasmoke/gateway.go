package betasmoke

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// RunGatewaySmoke exercises the governed model gateway path used by OpenCode-style clients.
func RunGatewaySmoke(ctx context.Context, cfg Config) error {
	if strings.TrimSpace(cfg.RuntimeToken) == "" {
		return fmt.Errorf("AI_ORCH_RUNTIME_TOKEN is required")
	}
	client := httpClient{
		devToken:     cfg.DevToken,
		runtimeToken: cfg.RuntimeToken,
		timeout:      cfg.HTTPTimeout,
	}

	fmt.Println("=== beta gateway smoke ===")

	fmt.Println("\n1. Governance Shell health...")
	status, raw, err := client.do(ctx, http.MethodGet, strings.TrimRight(cfg.GovernanceURL, "/")+"/healthz", cfg.DevToken, nil)
	if err != nil {
		return fmt.Errorf("health check: %w", err)
	}
	if err := client.requireOK(status, raw, "health check"); err != nil {
		return err
	}
	fmt.Println("   OK")

	fmt.Println("\n2. Starting governed run...")
	prompt := cfg.Prompt
	if prompt == "" {
		prompt = "Reply with exactly: gateway-smoke-ok"
	}
	runBody, _ := json.Marshal(map[string]any{
		"agent":           "unit-tests",
		"classification":  cfg.Classification,
		"prompt":          prompt,
		"permission_mode": "reviewed",
		"approval_mode":   "manual",
		"workspace_mode":  "local",
	})
	status, raw, err = client.do(ctx, http.MethodPost, strings.TrimRight(cfg.GovernanceURL, "/")+"/v1/runs", cfg.DevToken, runBody)
	if err != nil {
		return fmt.Errorf("start run: %w", err)
	}
	if err := client.requireOK(status, raw, "start run"); err != nil {
		return err
	}
	var runResp struct {
		SessionID  string `json:"session_id"`
		Specialist string `json:"specialist"`
	}
	if err := json.Unmarshal(raw, &runResp); err != nil {
		return fmt.Errorf("decode run response: %w", err)
	}
	if runResp.SessionID == "" || runResp.Specialist == "" {
		return fmt.Errorf("run response missing session_id or specialist")
	}
	fmt.Printf("   session=%s specialist=%s\n", runResp.SessionID, runResp.Specialist)

	fmt.Println("\n3. Confirming specialist...")
	confirmBody, _ := json.Marshal(map[string]any{"agent": runResp.Specialist})
	confirmURL := fmt.Sprintf("%s/v1/sessions/%s/confirm", strings.TrimRight(cfg.GovernanceURL, "/"), runResp.SessionID)
	status, raw, err = client.do(ctx, http.MethodPost, confirmURL, cfg.DevToken, confirmBody)
	if err != nil {
		return fmt.Errorf("confirm: %w", err)
	}
	if err := client.requireOK(status, raw, "confirm"); err != nil {
		return err
	}
	fmt.Println("   OK")

	gatewayBase := strings.TrimRight(cfg.GatewayURL, "/")

	fmt.Println("\n4. Listing gateway models...")
	modelsURL := gatewayBase + "/v1/models"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, modelsURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+cfg.RuntimeToken)
	req.Header.Set("X-AI-Orch-Session-ID", runResp.SessionID)
	httpClient := &http.Client{Timeout: cfg.HTTPTimeout}
	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("list models: %w", err)
	}
	defer resp.Body.Close()
	raw, _ = io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("list models: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	var modelsResp struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &modelsResp); err != nil {
		return fmt.Errorf("decode models: %w", err)
	}
	if len(modelsResp.Data) == 0 {
		return fmt.Errorf("gateway returned no models")
	}
	fmt.Printf("   %d model aliases exposed\n", len(modelsResp.Data))

	fmt.Println("\n5. Gateway chat completion...")
	chatBody, _ := json.Marshal(map[string]any{
		"model": cfg.ModelAlias,
		"messages": []map[string]string{
			{"role": "user", "content": prompt},
		},
		"temperature": 0,
		"max_tokens":  32,
	})
	chatReq, err := http.NewRequestWithContext(ctx, http.MethodPost, gatewayBase+"/v1/chat/completions", bytes.NewReader(chatBody))
	if err != nil {
		return err
	}
	chatReq.Header.Set("Content-Type", "application/json")
	chatReq.Header.Set("Authorization", "Bearer "+cfg.RuntimeToken)
	chatReq.Header.Set("X-AI-Orch-Session-ID", runResp.SessionID)
	chatResp, err := httpClient.Do(chatReq)
	if err != nil {
		return fmt.Errorf("chat completion: %w", err)
	}
	defer chatResp.Body.Close()
	chatRaw, _ := io.ReadAll(chatResp.Body)
	if chatResp.StatusCode < 200 || chatResp.StatusCode >= 300 {
		return fmt.Errorf("chat completion: HTTP %d: %s", chatResp.StatusCode, strings.TrimSpace(string(chatRaw)))
	}
	content, err := extractAssistantContent(chatRaw)
	if err != nil {
		return err
	}
	if err := validateExpected(content, cfg.Expected); err != nil {
		return err
	}
	fmt.Printf("   model response: %s\n", content)

	fmt.Println("\n6. Gateway tool-call transcript compatibility...")
	toolBody, _ := json.Marshal(map[string]any{
		"model": cfg.ModelAlias,
		"messages": []map[string]any{
			{
				"role":    "assistant",
				"content": nil,
				"tool_calls": []map[string]any{
					{
						"id":   "call_ai_orch_smoke",
						"type": "function",
						"function": map[string]any{
							"name":      "read_workspace_state",
							"arguments": `{"target":"smoke"}`,
						},
					},
				},
			},
			{"role": "tool", "tool_call_id": "call_ai_orch_smoke", "content": `{"status":"ok"}`},
			{"role": "user", "content": "Reply with exactly: gateway-tools-ok"},
		},
		"tools": []map[string]any{
			{
				"type": "function",
				"function": map[string]any{
					"name":        "read_workspace_state",
					"description": "Read sanitized workspace state for smoke validation.",
					"parameters": map[string]any{
						"type":       "object",
						"properties": map[string]any{"target": map[string]any{"type": "string"}},
					},
				},
			},
		},
		"tool_choice": "auto",
		"temperature": 0,
		"max_tokens":  32,
	})
	toolReq, err := http.NewRequestWithContext(ctx, http.MethodPost, gatewayBase+"/v1/chat/completions", bytes.NewReader(toolBody))
	if err != nil {
		return err
	}
	toolReq.Header.Set("Content-Type", "application/json")
	toolReq.Header.Set("Authorization", "Bearer "+cfg.RuntimeToken)
	toolReq.Header.Set("X-AI-Orch-Session-ID", runResp.SessionID)
	toolResp, err := httpClient.Do(toolReq)
	if err != nil {
		return fmt.Errorf("tool-call transcript completion: %w", err)
	}
	defer toolResp.Body.Close()
	toolRaw, _ := io.ReadAll(toolResp.Body)
	if toolResp.StatusCode < 200 || toolResp.StatusCode >= 300 {
		return fmt.Errorf("tool-call transcript completion: HTTP %d: %s", toolResp.StatusCode, strings.TrimSpace(string(toolRaw)))
	}
	if len(toolRaw) == 0 {
		return fmt.Errorf("tool-call transcript completion returned empty body")
	}
	fmt.Println("   tool-call transcript accepted")

	fmt.Println("\n7. Audit lookup for gateway events...")
	auditURL := fmt.Sprintf("%s/v1/audit/sessions/%s", strings.TrimRight(cfg.GovernanceURL, "/"), runResp.SessionID)
	status, raw, err = client.do(ctx, http.MethodGet, auditURL, cfg.DevToken, nil)
	if err != nil {
		return fmt.Errorf("audit lookup: %w", err)
	}
	if err := client.requireOK(status, raw, "audit lookup"); err != nil {
		return err
	}
	if !strings.Contains(string(raw), "model.gateway") {
		return fmt.Errorf("audit trail missing model.gateway event")
	}
	if strings.Count(string(raw), "model.gateway") < 2 {
		return fmt.Errorf("audit trail missing tool-call gateway event")
	}
	fmt.Println("   gateway audit event present")

	fmt.Println("\n=== beta gateway smoke passed ===")
	return nil
}
