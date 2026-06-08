package betasmoke

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// RunProviderSmoke runs the full governed-run path with real orchestrator dispatch (no EchoRuntime).
func RunProviderSmoke(ctx context.Context, cfg Config) error {
	client := httpClient{
		devToken:     cfg.DevToken,
		runtimeToken: cfg.RuntimeToken,
		timeout:      cfg.HTTPTimeout,
	}

	fmt.Println("=== beta provider smoke ===")

	fmt.Println("\n1. Governance Shell health...")
	status, raw, err := client.do(ctx, http.MethodGet, strings.TrimRight(cfg.GovernanceURL, "/")+"/healthz", cfg.DevToken, nil)
	if err != nil {
		return fmt.Errorf("health check: %w", err)
	}
	if err := client.requireOK(status, raw, "health check"); err != nil {
		return err
	}
	fmt.Println("   OK")

	prompt := envOrDefault("AI_ORCH_PROVIDER_PROMPT", `Write unit tests for login.
Return only one JSON object that creates SMOKE_SOURCE_CONTEXT.md with safe, non-sensitive placeholder content.
Do not include passwords, tokens, API keys, credentials, private URLs, or external service calls.`)
	specialist := envOrDefault("AI_ORCH_PROVIDER_AGENT", "unit-tests")

	fmt.Println("\n2. Starting governed run...")
	runBody, _ := json.Marshal(map[string]any{
		"agent":           specialist,
		"classification":  "internal",
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

	fmt.Println("\n3. Confirming specialist and streaming...")
	confirmAgent := specialist
	if confirmAgent == "" {
		confirmAgent = runResp.Specialist
	}
	confirmBody, _ := json.Marshal(map[string]any{"agent": confirmAgent})
	confirmURL := fmt.Sprintf("%s/v1/sessions/%s/confirm", strings.TrimRight(cfg.GovernanceURL, "/"), runResp.SessionID)

	fmt.Println("\n4. Streaming dispatch events (provider-backed)...")
	eventsURL := fmt.Sprintf("%s/v1/sessions/%s/events", strings.TrimRight(cfg.GovernanceURL, "/"), runResp.SessionID)
	patchID, modelUsage, err := streamUntilPatch(ctx, cfg.DevToken, eventsURL, cfg.SSETimeout, func() error {
		status, raw, err := client.do(ctx, http.MethodPost, confirmURL, cfg.DevToken, confirmBody)
		if err != nil {
			return fmt.Errorf("confirm: %w", err)
		}
		return client.requireOK(status, raw, "confirm")
	})
	if err != nil {
		return err
	}
	fmt.Printf("   patch=%s\n", patchID)
	for _, line := range modelUsage {
		fmt.Printf("   %s\n", line)
	}

	fmt.Println("\n5. Recording patch decision...")
	patchBody, _ := json.Marshal(map[string]any{
		"patch_id": patchID,
		"decision": "applied",
	})
	patchURL := fmt.Sprintf("%s/v1/sessions/%s/patch-decision", strings.TrimRight(cfg.GovernanceURL, "/"), runResp.SessionID)
	status, raw, err = client.do(ctx, http.MethodPost, patchURL, cfg.DevToken, patchBody)
	if err != nil {
		return fmt.Errorf("patch decision: %w", err)
	}
	if err := client.requireOK(status, raw, "patch decision"); err != nil {
		return err
	}
	fmt.Println("   OK")

	fmt.Println("\n=== beta provider smoke passed ===")
	return nil
}

func streamUntilPatch(ctx context.Context, token, url string, timeout time.Duration, confirmFn func() error) (string, []string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "text/event-stream")

	client := &http.Client{Timeout: timeout}
	resp, err := client.Do(req)
	if err != nil {
		return "", nil, fmt.Errorf("events stream: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return "", nil, fmt.Errorf("events stream: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	confirmDone := make(chan error, 1)
	go func() {
		if confirmFn == nil {
			confirmDone <- nil
			return
		}
		confirmDone <- confirmFn()
	}()

	var patchIDs []string
	var modelUsage []string
	var eventTypes []string
	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		var event struct {
			Type    string `json:"type"`
			Payload string `json:"payload"`
		}
		if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &event); err != nil {
			return "", nil, fmt.Errorf("decode event: %w", err)
		}
		eventTypes = append(eventTypes, event.Type)
		switch event.Type {
		case "patch":
			id := extractPatchID(event.Payload)
			if id == "" {
				return "", nil, errors.New("patch event missing patch id")
			}
			patchIDs = append(patchIDs, id)
		case "model_usage":
			if event.Payload != "" {
				modelUsage = append(modelUsage, event.Payload)
			}
		case "error":
			if event.Payload == "" {
				return "", nil, errors.New("runtime emitted error event")
			}
			return "", nil, errors.New(event.Payload)
		}
	}
	if err := scanner.Err(); err != nil {
		return "", nil, err
	}
	if confirmErr := <-confirmDone; confirmErr != nil {
		return "", nil, confirmErr
	}
	if len(patchIDs) == 0 {
		return "", modelUsage, fmt.Errorf("no patch event received from provider dispatch (events=%v)", eventTypes)
	}
	return patchIDs[0], modelUsage, nil
}

func extractPatchID(payload string) string {
	payload = strings.TrimSpace(payload)
	if payload == "" {
		return ""
	}
	var envelope struct {
		PatchID      string `json:"patchId"`
		PatchIDSnake string `json:"patch_id"`
	}
	if err := json.Unmarshal([]byte(payload), &envelope); err != nil {
		return ""
	}
	if envelope.PatchID != "" {
		return envelope.PatchID
	}
	return envelope.PatchIDSnake
}
