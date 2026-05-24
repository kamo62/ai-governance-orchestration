package main

import (
	"bytes"
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

const (
	defaultGovernanceURL = "http://127.0.0.1:8080"
	defaultOrchestratorURL = "http://127.0.0.1:8081"
)

type Config struct {
	GovernanceURL   string
	OrchestratorURL string
	Token           string
}

func loadConfig() Config {
	return Config{
		GovernanceURL:   envOrDefault("AI_ORCH_GOVERNANCE_URL", defaultGovernanceURL),
		OrchestratorURL: envOrDefault("AI_ORCH_ORCHESTRATOR_URL", defaultOrchestratorURL),
		Token:           envOrDefault("AI_ORCH_DEV_TOKEN", ""),
	}
}

func envOrDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
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
		handleSmoke(ctx, cfg, os.Args[2:])
	case "agents":
		handleAgents(ctx, cfg, os.Args[2:])
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n", os.Args[1])
		printUsage()
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Println(`ai-orch CLI — local AI agent orchestration client

Usage:
  ai-orch session create --agent <name> --classification <level> --prompt <text>
  ai-orch session message --session-id <id> --prompt <text>
  ai-orch session confirm --session-id <id> --agent <name>
  ai-orch audit lookup --session-id <id>
  ai-orch killswitch status
  ai-orch killswitch toggle --scope <scope> --id <id> [--enable|--disable]
  ai-orch smoke
  ai-orch agents list

Environment:
  AI_ORCH_GOVERNANCE_URL    Governance Shell base URL (default: http://127.0.0.1:8080)
  AI_ORCH_ORCHESTRATOR_URL  Orchestrator base URL (default: http://127.0.0.1:8081)
  AI_ORCH_DEV_TOKEN         Bearer token for local dev auth`)
}

func handleSession(ctx context.Context, cfg Config, args []string) {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "usage: ai-orch session create|message|confirm ...")
		os.Exit(1)
	}
	switch args[0] {
	case "create":
		createSession(ctx, cfg, args[1:])
	case "message":
		sendMessage(ctx, cfg, args[1:])
	case "confirm":
		confirmSession(ctx, cfg, args[1:])
	default:
		fmt.Fprintf(os.Stderr, "unknown session subcommand: %s\n", args[0])
		os.Exit(1)
	}
}

func createSession(ctx context.Context, cfg Config, args []string) {
	agent := flagValue(args, "--agent")
	classification := flagValue(args, "--classification")
	prompt := flagValue(args, "--prompt")
	if agent == "" || classification == "" || prompt == "" {
		fmt.Fprintln(os.Stderr, "usage: ai-orch session create --agent <name> --classification <level> --prompt <text>")
		os.Exit(1)
	}

	body, _ := json.Marshal(map[string]any{
		"agent":          agent,
		"classification": classification,
		"prompt":         prompt,
	})
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
	// This POST triggers routing. In a full SSE implementation, the response would stream events.
	// For the local CLI, we read the non-streaming response.
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
		var method string
		var url string
		if disable {
			method = http.MethodPost
			url = fmt.Sprintf("%s/v1/admin/killswitch/%s/%s", cfg.GovernanceURL, scope, id)
		} else {
			method = http.MethodDelete
			url = fmt.Sprintf("%s/v1/admin/killswitch/%s/%s", cfg.GovernanceURL, scope, id)
		}
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

func handleSmoke(ctx context.Context, cfg Config, args []string) {
	fmt.Println("=== ai-orch smoke test ===")

	// 1. Health check
	fmt.Println("\n1. Governance Shell health...")
	if _, err := doGet(ctx, cfg, cfg.GovernanceURL+"/healthz"); err != nil {
		fmt.Fprintf(os.Stderr, "governance-shell health failed: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("   OK")

	// 2. Catalog validation via Governance Shell readiness
	fmt.Println("\n2. Catalog validation via readyz...")
	if _, err := doGet(ctx, cfg, cfg.GovernanceURL+"/readyz"); err != nil {
		fmt.Fprintf(os.Stderr, "governance-shell readyz failed: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("   OK")

	// 3. Create session
	fmt.Println("\n3. Creating governed session...")
	body, _ := json.Marshal(map[string]any{
		"agent":          "test-generation",
		"classification": "internal",
		"prompt":         "write a smoke test for the login endpoint",
	})
	sessionResp, err := doPost(ctx, cfg, cfg.GovernanceURL+"/v1/sessions", body)
	if err != nil {
		fmt.Fprintf(os.Stderr, "create session failed: %v\n", err)
		os.Exit(1)
	}
	prettyPrintJSON(sessionResp)

	var session struct {
		SessionID string `json:"session_id"`
	}
	_ = json.Unmarshal([]byte(sessionResp), &session)
	if session.SessionID == "" {
		fmt.Fprintln(os.Stderr, "session ID not returned")
		os.Exit(1)
	}

	// 4. Audit lookup
	fmt.Println("\n4. Audit lookup for session...")
	auditResp, err := doGet(ctx, cfg, fmt.Sprintf("%s/v1/audit/sessions/%s", cfg.GovernanceURL, session.SessionID))
	if err != nil {
		fmt.Fprintf(os.Stderr, "audit lookup failed: %v\n", err)
		os.Exit(1)
	}
	prettyPrintJSON(auditResp)

	// 5. Agents list
	fmt.Println("\n5. Agents list...")
	agentsResp, err := doGet(ctx, cfg, cfg.GovernanceURL+"/v1/agents")
	if err != nil {
		fmt.Fprintf(os.Stderr, "agents list failed: %v\n", err)
		os.Exit(1)
	}
	prettyPrintJSON(agentsResp)

	// 6. Orchestrator health
	fmt.Println("\n6. Orchestrator health...")
	if _, err := doGet(ctx, cfg, cfg.OrchestratorURL+"/healthz"); err != nil {
		fmt.Fprintf(os.Stderr, "orchestrator health failed: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("   OK")

	fmt.Println("\n=== smoke test passed ===")
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
	if cfg.Token != "" {
		req.Header.Set("Authorization", "Bearer "+cfg.Token)
	}
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

// compile-time interface checks
var _ = errors.New
