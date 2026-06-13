package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/kamo62/ai-governance-orchestration/ai-agent-orch/internal/workspace"
)

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
	humanConfirmed := hasFlag(args, "--human") || hasFlag(args, "--human-confirmed")
	if sessionID == "" || agent == "" {
		fmt.Fprintln(os.Stderr, "usage: ai-orch session confirm --session-id <id> --agent <name> [--human]")
		os.Exit(1)
	}

	body, _ := json.Marshal(map[string]any{
		"agent":           agent,
		"human_confirmed": humanConfirmed,
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
	if identity := localIdentity(); identity != "" {
		req.Header.Set("X-AI-Orch-Local-Identity", identity)
	}
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
		fmt.Fprintln(os.Stderr, "usage: ai-orch audit lookup|verify --session-id <id>")
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
	case "verify":
		sessionID := flagValue(args[1:], "--session-id")
		if sessionID == "" {
			fmt.Fprintln(os.Stderr, "usage: ai-orch audit verify --session-id <id>")
			os.Exit(1)
		}
		resp, err := doGet(ctx, cfg, fmt.Sprintf("%s/v1/audit/sessions/%s/verify", cfg.GovernanceURL, sessionID))
		if err != nil {
			fmt.Fprintf(os.Stderr, "audit verify failed: %v\n", err)
			os.Exit(1)
		}
		prettyPrintJSON(resp)
		var result struct {
			ChainValid bool `json:"chain_valid"`
		}
		if err := json.Unmarshal([]byte(resp), &result); err == nil && !result.ChainValid {
			os.Exit(1)
		}
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
