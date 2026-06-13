package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

func smokeSSETimeout() time.Duration {
	if raw := strings.TrimSpace(os.Getenv("AI_ORCH_SMOKE_SSE_TIMEOUT")); raw != "" {
		if d, err := time.ParseDuration(raw); err == nil {
			return d
		}
	}
	return 30 * time.Second
}

func printSmokeUsage() {
	fmt.Println(`Usage:
  ai-orch smoke [--prompt <text>]
  ai-orch smoke openrouter [flags]
  ai-orch smoke gateway|provider

The default form runs the local end-to-end smoke path:
  health -> catalog -> governed run -> confirmation -> events -> patch decision -> audit -> metrics

"openrouter" sends one direct OpenRouter completion to verify the provider
key and model alias. "gateway" and "provider" run the beta gateway and
provider-backed smoke suites against a running stack.

Environment:
  AI_ORCH_GOVERNANCE_URL    Governance Shell base URL (default: http://127.0.0.1:18080)
  AI_ORCH_ORCHESTRATOR_URL  Orchestrator base URL (default: http://127.0.0.1:8081)
  AI_ORCH_DEV_TOKEN         Bearer token for local dev auth`)
}

func handleNegative(ctx context.Context, cfg Config, args []string) {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "usage: ai-orch negative secret|classification|killswitch|cost")
		os.Exit(1)
	}

	switch args[0] {
	case "secret":
		testNegativeSecret(ctx, cfg)
	case "classification":
		testNegativeClassification(ctx, cfg)
	case "killswitch":
		testNegativeKillSwitch(ctx, cfg)
	case "cost":
		testNegativeCost(ctx, cfg)
	default:
		fmt.Fprintf(os.Stderr, "unknown negative test: %s\n", args[0])
		os.Exit(1)
	}
}

func testNegativeSecret(ctx context.Context, cfg Config) {
	fmt.Println("=== negative test: secret detection ===")
	fakeToken := "sk-or-v1-" + "test1234567890"
	body, _ := json.Marshal(map[string]any{
		"agent":          "unit-tests",
		"classification": "internal",
		"prompt":         "use OPENROUTER_API_KEY=" + fakeToken,
	})
	resp, err := doPost(ctx, cfg, cfg.GovernanceURL+"/v1/sessions", body)
	if err == nil {
		fmt.Fprintf(os.Stderr, "FAIL: expected secret to be blocked, got: %s\n", resp)
		os.Exit(1)
	}
	if !strings.Contains(err.Error(), "403") {
		fmt.Fprintf(os.Stderr, "FAIL: expected 403, got: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("PASS: secret blocked")
}

func testNegativeClassification(ctx context.Context, cfg Config) {
	fmt.Println("=== negative test: classification ceiling ===")
	body, _ := json.Marshal(map[string]any{
		"agent":          "unit-tests",
		"classification": "restricted",
		"prompt":         "restricted content",
	})
	resp, err := doPost(ctx, cfg, cfg.GovernanceURL+"/v1/sessions", body)
	if err == nil {
		fmt.Fprintf(os.Stderr, "FAIL: expected classification to be blocked, got: %s\n", resp)
		os.Exit(1)
	}
	if !strings.Contains(err.Error(), "403") {
		fmt.Fprintf(os.Stderr, "FAIL: expected 403, got: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("PASS: classification blocked")
}

func testNegativeKillSwitch(ctx context.Context, cfg Config) {
	fmt.Println("=== negative test: kill switch ===")
	// Enable kill switch
	_, _ = doPost(ctx, cfg, cfg.GovernanceURL+"/v1/admin/killswitch/agent/unit-tests", nil)

	body, _ := json.Marshal(map[string]any{
		"agent":          "unit-tests",
		"classification": "internal",
		"prompt":         "ordinary prompt",
	})
	resp, err := doPost(ctx, cfg, cfg.GovernanceURL+"/v1/sessions", body)
	if err == nil {
		fmt.Fprintf(os.Stderr, "FAIL: expected kill switch to block, got: %s\n", resp)
		os.Exit(1)
	}
	if !strings.Contains(err.Error(), "423") {
		fmt.Fprintf(os.Stderr, "FAIL: expected 423, got: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("PASS: kill switch blocked")

	// Disable kill switch
	_, _ = doRequest(ctx, cfg, http.MethodDelete, cfg.GovernanceURL+"/v1/admin/killswitch/agent/unit-tests", nil)
}

func testNegativeCost(ctx context.Context, cfg Config) {
	fmt.Println("=== negative test: cost cap ===")
	body, _ := json.Marshal(map[string]any{
		"agent":              "unit-tests",
		"classification":     "internal",
		"prompt":             "expensive prompt",
		"estimated_cost_usd": 0.50,
	})
	resp, err := doPost(ctx, cfg, cfg.GovernanceURL+"/v1/sessions", body)
	if err == nil {
		fmt.Fprintf(os.Stderr, "FAIL: expected cost cap to block, got: %s\n", resp)
		os.Exit(1)
	}
	if !strings.Contains(err.Error(), "402") && !strings.Contains(err.Error(), "Payment") {
		fmt.Fprintf(os.Stderr, "FAIL: expected 402, got: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("PASS: cost cap blocked")
}

func handleSmoke(ctx context.Context, cfg Config, args []string) {
	if len(args) > 0 {
		switch args[0] {
		case "openrouter":
			handleOpenRouterSmoke(args[1:])
			return
		case "gateway", "provider":
			handleBetaSmoke(args[0])
			return
		}
	}
	prompt := flagValue(args, "--prompt")
	if prompt == "" {
		prompt = `Create a smoke test patch envelope for the orchestration path.
Return only one JSON object that creates SMOKE_SOURCE_CONTEXT.md with safe, non-sensitive placeholder content.
Do not include passwords, tokens, API keys, credentials, private URLs, or external service calls.`
	}

	fmt.Println("=== ai-orch smoke test ===")

	// 1. Health check
	fmt.Println("\n1. Governance Shell health...")
	if _, err := doGet(ctx, cfg, cfg.GovernanceURL+"/healthz"); err != nil {
		fmt.Fprintf(os.Stderr, "governance-shell health failed: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("   OK")

	// 2. Catalog validation via readyz
	fmt.Println("\n2. Catalog validation via readyz...")
	if _, err := doGet(ctx, cfg, cfg.GovernanceURL+"/readyz"); err != nil {
		fmt.Fprintf(os.Stderr, "governance-shell readyz failed: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("   OK")

	// 3. Start governed run.
	fmt.Println("\n3. Starting governed run...")
	runBody := map[string]any{
		"agent":           "unit-tests",
		"classification":  "internal",
		"prompt":          prompt,
		"permission_mode": "reviewed",
		"approval_mode":   "manual",
		"workspace_mode":  "local",
	}
	addLocalProjectContext(runBody)
	body, _ := json.Marshal(runBody)
	runResp, err := doPost(ctx, cfg, cfg.GovernanceURL+"/v1/runs", body)
	if err != nil {
		fmt.Fprintf(os.Stderr, "start governed run failed: %v\n", err)
		os.Exit(1)
	}
	prettyPrintJSON(runResp)

	var runResult struct {
		RunID      string `json:"run_id"`
		SessionID  string `json:"session_id"`
		Specialist string `json:"specialist"`
	}
	_ = json.Unmarshal([]byte(runResp), &runResult)
	if runResult.SessionID == "" {
		fmt.Fprintln(os.Stderr, "session ID not returned")
		os.Exit(1)
	}
	if runResult.RunID == "" {
		fmt.Fprintln(os.Stderr, "run ID not returned")
		os.Exit(1)
	}
	if runResult.Specialist == "" {
		fmt.Fprintln(os.Stderr, "no specialist returned")
		os.Exit(1)
	}
	fmt.Printf("   Specialist: %s\n", runResult.Specialist)

	// 4. Confirm specialist.
	fmt.Println("\n4. Confirming specialist...")
	confirmBody, _ := json.Marshal(map[string]any{"agent": runResult.Specialist})
	_, err = doPost(ctx, cfg, fmt.Sprintf("%s/v1/sessions/%s/confirm", cfg.GovernanceURL, runResult.SessionID), confirmBody)
	if err != nil {
		fmt.Fprintf(os.Stderr, "confirm session failed: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("   Confirmed")

	// 5. Stream events (SSE).
	fmt.Println("\n5. Streaming events (SSE)...")
	eventsURL := fmt.Sprintf("%s/v1/sessions/%s/events", cfg.GovernanceURL, runResult.SessionID)
	eventResult, err := streamEventsFromURL(ctx, cfg, eventsURL)
	if err != nil {
		fmt.Fprintf(os.Stderr, "events stream reported failure after %d event(s): %v\n", eventResult.Count, err)
		os.Exit(1)
	}
	fmt.Printf("   Received %d events\n", eventResult.Count)
	for _, usage := range eventResult.ModelUsage {
		fmt.Printf("   Model usage: %s\n", strings.TrimSpace(usage))
	}
	if len(eventResult.PatchIDs) == 0 {
		fmt.Fprintln(os.Stderr, "no patch event received")
		os.Exit(1)
	}
	patchID := eventResult.PatchIDs[0]
	fmt.Printf("   Patch: %s\n", patchID)

	// 6. Patch decision.
	fmt.Println("\n6. Submitting patch decision...")
	patchBody, _ := json.Marshal(map[string]any{
		"patch_id": patchID,
		"decision": "applied",
	})
	_, err = doPost(ctx, cfg, fmt.Sprintf("%s/v1/sessions/%s/patch-decision", cfg.GovernanceURL, runResult.SessionID), patchBody)
	if err != nil {
		fmt.Fprintf(os.Stderr, "patch decision failed: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("   Patch decision recorded: applied (CLI does not mutate the workspace)")

	// 7. Audit lookup.
	fmt.Println("\n7. Audit lookup...")
	auditResp, err := doGet(ctx, cfg, fmt.Sprintf("%s/v1/audit/sessions/%s", cfg.GovernanceURL, runResult.SessionID))
	if err != nil {
		fmt.Fprintf(os.Stderr, "audit lookup failed: %v\n", err)
		os.Exit(1)
	}
	var auditResult struct {
		Events []map[string]any `json:"events"`
	}
	_ = json.Unmarshal([]byte(auditResp), &auditResult)
	fmt.Printf("   %d audit events found\n", len(auditResult.Events))

	// 9. Verify no raw prompt in audit
	if strings.Contains(auditResp, prompt) {
		fmt.Fprintf(os.Stderr, "FAIL: raw prompt found in audit response\n")
		os.Exit(1)
	}
	fmt.Println("   Raw prompt not in audit: OK")

	// 8. Agents list.
	fmt.Println("\n8. Agents list...")
	_, err = doGet(ctx, cfg, cfg.GovernanceURL+"/v1/agents")
	if err != nil {
		fmt.Fprintf(os.Stderr, "agents list failed: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("   OK")

	// 9. Orchestrator health.
	fmt.Println("\n9. Orchestrator health...")
	if _, err := doGet(ctx, cfg, cfg.OrchestratorURL+"/healthz"); err != nil {
		fmt.Fprintf(os.Stderr, "orchestrator health failed: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("   OK")

	// 10. Metrics.
	fmt.Println("\n10. Metrics...")
	metricsResp, err := doGet(ctx, cfg, cfg.GovernanceURL+"/metrics")
	if err != nil {
		fmt.Fprintf(os.Stderr, "metrics failed: %v\n", err)
		os.Exit(1)
	}
	prettyPrintJSON(metricsResp)

	fmt.Println("\n=== smoke test passed ===")
}

func streamEventsFromURL(ctx context.Context, cfg Config, url string) (eventStreamResult, error) {
	var result eventStreamResult
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return result, fmt.Errorf("events request failed: %w", err)
	}
	token := cfg.Token
	if token == "" {
		token = "local-dev"
	}
	req.Header.Set("Authorization", "Bearer "+token)
	if identity := localIdentity(); identity != "" {
		req.Header.Set("X-AI-Orch-Local-Identity", identity)
	}
	req.Header.Set("Accept", "text/event-stream")

	client := &http.Client{Timeout: smokeSSETimeout()}
	resp, err := client.Do(req)
	if err != nil {
		return result, fmt.Errorf("events stream failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return result, fmt.Errorf("events stream failed: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "data: ") {
			result.Count++
			var event sessionEvent
			data := strings.TrimPrefix(line, "data: ")
			if err := json.Unmarshal([]byte(data), &event); err != nil {
				return result, fmt.Errorf("decode event: %w", err)
			}
			switch event.Type {
			case "patch":
				patchID := extractPatchID(event.Payload)
				if patchID == "" {
					return result, errors.New("patch event missing patch id")
				}
				result.PatchIDs = append(result.PatchIDs, patchID)
			case "model_usage":
				if event.Payload != "" {
					result.ModelUsage = append(result.ModelUsage, event.Payload)
				}
			case "error":
				if event.Payload == "" {
					return result, errors.New("runtime emitted error event")
				}
				return result, errors.New(event.Payload)
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return result, fmt.Errorf("events stream read failed: %w", err)
	}
	return result, nil
}
