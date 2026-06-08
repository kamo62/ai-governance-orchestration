package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"ai-agent-orch/internal/contextresolver"
	"ai-agent-orch/internal/workspace"
)

const (
	defaultGovernanceURL   = "http://127.0.0.1:18080"
	defaultOrchestratorURL = "http://127.0.0.1:8081"
	defaultModelGatewayURL = "http://127.0.0.1:18082"
)

type Config struct {
	GovernanceURL      string
	OrchestratorURL    string
	ModelGatewayURL    string
	Token              string
	AdminToken         string
	RuntimeToken       string
	TrustedClientToken string
}

type eventStreamResult struct {
	Count      int
	PatchIDs   []string
	ModelUsage []string
}

type sessionEvent struct {
	Type    string `json:"type"`
	Payload string `json:"payload"`
}

func loadConfig() Config {
	return Config{
		GovernanceURL:      envOrDefault("AI_ORCH_GOVERNANCE_URL", defaultGovernanceURL),
		OrchestratorURL:    envOrDefault("AI_ORCH_ORCHESTRATOR_URL", defaultOrchestratorURL),
		ModelGatewayURL:    envOrDefault("AI_ORCH_MODEL_GATEWAY_URL", defaultModelGatewayURL),
		Token:              envOrDefault("AI_ORCH_DEV_TOKEN", ""),
		AdminToken:         envOrDefault("AI_ORCH_ADMIN_TOKEN", ""),
		RuntimeToken:       envOrDefault("AI_ORCH_RUNTIME_TOKEN", ""),
		TrustedClientToken: envOrDefault("AI_ORCH_TRUSTED_CLIENT_TOKEN", ""),
	}
}

func envOrDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func smokeSSETimeout() time.Duration {
	if raw := strings.TrimSpace(os.Getenv("AI_ORCH_SMOKE_SSE_TIMEOUT")); raw != "" {
		if d, err := time.ParseDuration(raw); err == nil {
			return d
		}
	}
	return 30 * time.Second
}

func addLocalProjectContext(body map[string]any) {
	if body == nil {
		return
	}
	resolved := contextresolver.New("").Resolve()
	addIfNonEmpty(body, "repo_url", resolved.RepoURL)
	addIfNonEmpty(body, "branch", resolved.Branch)
	addIfNonEmpty(body, "commit_sha", resolved.CommitSHA)
	addIfNonEmpty(body, "work_item_id", resolved.WorkItemID)
	addIfNonEmpty(body, "work_item_type", resolved.WorkItemType)
	addIfNonEmpty(body, "actor_hint", resolved.ActorHint)
	addIfNonEmpty(body, "source_system", resolved.SourceSystem)
}

func addIfNonEmpty(body map[string]any, key string, value string) {
	if value != "" {
		body[key] = value
	}
}

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}
	cfg := loadConfig()
	ctx := context.Background()

	switch os.Args[1] {
	case "session":
		handleSession(ctx, cfg, os.Args[2:])
	case "audit":
		handleAudit(ctx, cfg, os.Args[2:])
	case "killswitch":
		handleKillSwitch(ctx, cfg, os.Args[2:])
	case "smoke":
		if isHelpOnly(os.Args[2:]) {
			printSmokeUsage()
			return
		}
		handleSmoke(ctx, cfg, os.Args[2:])
	case "agents":
		handleAgents(ctx, cfg, os.Args[2:])
	case "negative":
		handleNegative(ctx, cfg, os.Args[2:])
	case "mcp":
		if len(os.Args) < 3 {
			fmt.Fprintln(os.Stderr, "usage: ai-orch mcp start|install|doctor ...")
			os.Exit(1)
		}
		switch os.Args[2] {
		case "start":
			handleMCPStart(ctx, cfg, os.Args[3:])
		case "install":
			handleMCPInstall(cfg, os.Args[3:])
		case "doctor":
			handleMCPDoctor(ctx, cfg, os.Args[3:])
		default:
			fmt.Fprintf(os.Stderr, "unknown mcp subcommand: %s\n", os.Args[2])
			os.Exit(1)
		}
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n", os.Args[1])
		printUsage()
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Println(`ai-orch CLI - local AI agent orchestration client

Usage:
  ai-orch session create --agent <name> --classification <level> --prompt <text> [--workspace]
  ai-orch session message --session-id <id> --prompt <text>
  ai-orch session confirm --session-id <id> --agent <name>
  ai-orch session events --session-id <id>
  ai-orch audit lookup --session-id <id>
  ai-orch killswitch status
  ai-orch killswitch toggle --scope <scope> --id <id> [--enable|--disable]
  ai-orch smoke [--prompt <text>]
  ai-orch agents list
  ai-orch negative secret|classification|killswitch|cost
  ai-orch mcp start [--transport http|stdio] [--host 127.0.0.1] [--port 18081]
  ai-orch mcp install --client <vscode|cline|claude-code|codex> [--force]
  ai-orch mcp doctor

Environment:
  AI_ORCH_GOVERNANCE_URL    Governance Shell base URL (default: http://127.0.0.1:18080)
  AI_ORCH_ORCHESTRATOR_URL  Orchestrator base URL (default: http://127.0.0.1:8081)
  AI_ORCH_MODEL_GATEWAY_URL Runtime model gateway base URL (default: http://127.0.0.1:18082)
  AI_ORCH_DEV_TOKEN         Bearer token for local dev auth
  AI_ORCH_ADMIN_TOKEN       Bearer token for admin routes such as killswitch
  AI_ORCH_RUNTIME_TOKEN     Bearer token for runtime model gateway calls`)
}

func printSmokeUsage() {
	fmt.Println(`Usage:
  ai-orch smoke [--prompt <text>]

Runs the local end-to-end smoke path:
  health -> catalog -> governed run -> confirmation -> events -> patch decision -> audit -> metrics

Environment:
  AI_ORCH_GOVERNANCE_URL    Governance Shell base URL (default: http://127.0.0.1:18080)
  AI_ORCH_ORCHESTRATOR_URL  Orchestrator base URL (default: http://127.0.0.1:8081)
  AI_ORCH_DEV_TOKEN         Bearer token for local dev auth`)
}

func isHelpOnly(args []string) bool {
	if len(args) != 1 {
		return false
	}
	return args[0] == "-h" || args[0] == "--help"
}

func handleSession(ctx context.Context, cfg Config, args []string) {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "usage: ai-orch session create|message|confirm|events ...")
		os.Exit(1)
	}
	switch args[0] {
	case "create":
		createSession(ctx, cfg, args[1:])
	case "message":
		sendMessage(ctx, cfg, args[1:])
	case "confirm":
		confirmSession(ctx, cfg, args[1:])
	case "events":
		streamEvents(ctx, cfg, args[1:])
	default:
		fmt.Fprintf(os.Stderr, "unknown session subcommand: %s\n", args[0])
		os.Exit(1)
	}
}

func createSession(ctx context.Context, cfg Config, args []string) {
	agent := flagValue(args, "--agent")
	classification := flagValue(args, "--classification")
	prompt := flagValue(args, "--prompt")
	withWorkspace := hasFlag(args, "--workspace")
	if agent == "" || classification == "" || prompt == "" {
		fmt.Fprintln(os.Stderr, "usage: ai-orch session create --agent <name> --classification <level> --prompt <text> [--workspace]")
		os.Exit(1)
	}

	// Append workspace context if requested.
	if withWorkspace {
		wd, err := os.Getwd()
		if err != nil {
			fmt.Fprintf(os.Stderr, "get working directory: %v\n", err)
			os.Exit(1)
		}
		packager := workspace.DefaultPackager(wd)
		ctxStr, err := packager.PackageAsContext()
		if err != nil {
			fmt.Fprintf(os.Stderr, "package workspace: %v\n", err)
			os.Exit(1)
		}
		if ctxStr != "" {
			prompt = prompt + ctxStr
			fmt.Fprintf(os.Stderr, "[workspace] packaged %s\n", wd)
		}
	}

	bodyMap := map[string]any{
		"agent":          agent,
		"classification": classification,
		"prompt":         prompt,
	}
	addLocalProjectContext(bodyMap)
	body, _ := json.Marshal(bodyMap)
	resp, err := doPost(ctx, cfg, cfg.GovernanceURL+"/v1/sessions", body)
	if err != nil {
		fmt.Fprintf(os.Stderr, "create session failed: %v\n", err)
		os.Exit(1)
	}
	prettyPrintJSON(resp)
}

func sendMessage(ctx context.Context, cfg Config, args []string) {
	sessionID := flagValue(args, "--session-id")
	prompt := flagValue(args, "--prompt")
	if sessionID == "" || prompt == "" {
		fmt.Fprintln(os.Stderr, "usage: ai-orch session message --session-id <id> --prompt <text>")
		os.Exit(1)
	}

	body, _ := json.Marshal(map[string]any{
		"prompt": prompt,
	})
	resp, err := doPost(ctx, cfg, fmt.Sprintf("%s/v1/sessions/%s/messages", cfg.GovernanceURL, sessionID), body)
	if err != nil {
		fmt.Fprintf(os.Stderr, "send message failed: %v\n", err)
		os.Exit(1)
	}
	prettyPrintJSON(resp)
}

func confirmSession(ctx context.Context, cfg Config, args []string) {
	sessionID := flagValue(args, "--session-id")
	agent := flagValue(args, "--agent")
	if sessionID == "" || agent == "" {
		fmt.Fprintln(os.Stderr, "usage: ai-orch session confirm --session-id <id> --agent <name>")
		os.Exit(1)
	}

	body, _ := json.Marshal(map[string]any{
		"agent": agent,
	})
	resp, err := doPost(ctx, cfg, fmt.Sprintf("%s/v1/sessions/%s/confirm", cfg.GovernanceURL, sessionID), body)
	if err != nil {
		fmt.Fprintf(os.Stderr, "confirm session failed: %v\n", err)
		os.Exit(1)
	}
	prettyPrintJSON(resp)
}

func streamEvents(ctx context.Context, cfg Config, args []string) {
	sessionID := flagValue(args, "--session-id")
	if sessionID == "" {
		fmt.Fprintln(os.Stderr, "usage: ai-orch session events --session-id <id>")
		os.Exit(1)
	}

	url := fmt.Sprintf("%s/v1/sessions/%s/events", cfg.GovernanceURL, sessionID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "events request failed: %v\n", err)
		os.Exit(1)
	}
	token := cfg.Token
	if token == "" {
		token = "local-dev"
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "text/event-stream")

	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		fmt.Fprintf(os.Stderr, "events stream failed: %v\n", err)
		os.Exit(1)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		fmt.Fprintf(os.Stderr, "events stream failed: HTTP %d: %s\n", resp.StatusCode, string(body))
		os.Exit(1)
	}

	fmt.Printf("=== SSE stream for session %s ===\n", sessionID)
	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "data: ") {
			data := strings.TrimPrefix(line, "data: ")
			var event map[string]any
			if err := json.Unmarshal([]byte(data), &event); err == nil {
				prettyPrintJSON(data)
			} else {
				fmt.Println(data)
			}
		}
	}
	if err := scanner.Err(); err != nil {
		fmt.Fprintf(os.Stderr, "events stream error: %v\n", err)
	}
}

func handleAudit(ctx context.Context, cfg Config, args []string) {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "usage: ai-orch audit lookup --session-id <id>")
		os.Exit(1)
	}
	switch args[0] {
	case "lookup":
		sessionID := flagValue(args[1:], "--session-id")
		if sessionID == "" {
			fmt.Fprintln(os.Stderr, "usage: ai-orch audit lookup --session-id <id>")
			os.Exit(1)
		}
		resp, err := doGet(ctx, cfg, fmt.Sprintf("%s/v1/audit/sessions/%s", cfg.GovernanceURL, sessionID))
		if err != nil {
			fmt.Fprintf(os.Stderr, "audit lookup failed: %v\n", err)
			os.Exit(1)
		}
		prettyPrintJSON(resp)
	default:
		fmt.Fprintf(os.Stderr, "unknown audit subcommand: %s\n", args[0])
		os.Exit(1)
	}
}

func handleKillSwitch(ctx context.Context, cfg Config, args []string) {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "usage: ai-orch killswitch status|toggle ...")
		os.Exit(1)
	}
	switch args[0] {
	case "status":
		resp, err := doGet(ctx, cfg, fmt.Sprintf("%s/v1/admin/killswitch", cfg.GovernanceURL))
		if err != nil {
			fmt.Fprintf(os.Stderr, "killswitch status failed: %v\n", err)
			os.Exit(1)
		}
		prettyPrintJSON(resp)
	case "toggle":
		scope := flagValue(args[1:], "--scope")
		id := flagValue(args[1:], "--id")
		enable := hasFlag(args[1:], "--enable")
		disable := hasFlag(args[1:], "--disable")
		if scope == "" || id == "" || (enable == disable) {
			fmt.Fprintln(os.Stderr, "usage: ai-orch killswitch toggle --scope <scope> --id <id> --enable|--disable")
			os.Exit(1)
		}
		method, url := killSwitchToggleRequest(cfg, scope, id, enable)
		resp, err := doRequest(ctx, cfg, method, url, nil)
		if err != nil {
			fmt.Fprintf(os.Stderr, "killswitch toggle failed: %v\n", err)
			os.Exit(1)
		}
		prettyPrintJSON(resp)
	default:
		fmt.Fprintf(os.Stderr, "unknown killswitch subcommand: %s\n", args[0])
		os.Exit(1)
	}
}

func killSwitchToggleRequest(cfg Config, scope, id string, enable bool) (method, url string) {
	url = fmt.Sprintf("%s/v1/admin/killswitch/%s/%s", cfg.GovernanceURL, scope, id)
	if enable {
		return http.MethodPost, url
	}
	return http.MethodDelete, url
}

func handleAgents(ctx context.Context, cfg Config, args []string) {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "usage: ai-orch agents list")
		os.Exit(1)
	}
	switch args[0] {
	case "list":
		resp, err := doGet(ctx, cfg, cfg.GovernanceURL+"/v1/agents")
		if err != nil {
			fmt.Fprintf(os.Stderr, "agents list failed: %v\n", err)
			os.Exit(1)
		}
		prettyPrintJSON(resp)
	default:
		fmt.Fprintf(os.Stderr, "unknown agents subcommand: %s\n", args[0])
		os.Exit(1)
	}
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

func doGet(ctx context.Context, cfg Config, url string) (string, error) {
	return doRequest(ctx, cfg, http.MethodGet, url, nil)
}

func doPost(ctx context.Context, cfg Config, url string, body []byte) (string, error) {
	return doRequest(ctx, cfg, http.MethodPost, url, body)
}

func doRequest(ctx context.Context, cfg Config, method, url string, body []byte) (string, error) {
	var bodyReader io.Reader
	if body != nil {
		bodyReader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, bodyReader)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+cfg.bearerTokenForURL(url))
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
	}
	return string(respBody), nil
}

func (cfg Config) bearerTokenForURL(rawURL string) string {
	if isAdminRoute(rawURL) {
		if cfg.AdminToken != "" {
			return cfg.AdminToken
		}
		return "local-admin"
	}
	if cfg.Token != "" {
		return cfg.Token
	}
	return "local-dev"
}

func isAdminRoute(rawURL string) bool {
	parsed, err := url.Parse(rawURL)
	if err == nil {
		path := parsed.EscapedPath()
		return path == "/v1/admin" || strings.Contains(path, "/v1/admin/")
	}
	return strings.Contains(rawURL, "/v1/admin/")
}

func prettyPrintJSON(s string) {
	var v any
	if err := json.Unmarshal([]byte(s), &v); err != nil {
		fmt.Println(s)
		return
	}
	b, _ := json.MarshalIndent(v, "", "  ")
	fmt.Println(string(b))
}

func flagValue(args []string, name string) string {
	for i := 0; i < len(args)-1; i++ {
		if args[i] == name {
			return args[i+1]
		}
	}
	return ""
}

func hasFlag(args []string, name string) bool {
	for _, a := range args {
		if a == name {
			return true
		}
	}
	return false
}

func extractPatchID(payload string) string {
	var patch struct {
		PatchIDCamel string `json:"patchId"`
		PatchIDSnake string `json:"patch_id"`
	}
	if err := json.Unmarshal([]byte(payload), &patch); err != nil {
		return ""
	}
	if patch.PatchIDCamel != "" {
		return patch.PatchIDCamel
	}
	return patch.PatchIDSnake
}
