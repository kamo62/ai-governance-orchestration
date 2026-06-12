package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"strings"
)

type openCodeWrapperOptions struct {
	GovernanceAgent         string
	GovernanceAgentExplicit bool
	Classification          string
	SessionPrompt           string
	Intent                  string
	ModelOnly               bool
	OpenCodeArgs            []string
}

func handleOpenCode(ctx context.Context, cfg Config, args []string) {
	if isHelpOnly(args) {
		fmt.Println(`Usage:
  ai-orch opencode [--governance-agent <name>] [--governance-classification <level>] [--governance-prompt <text>] [-- <opencode args...>]
  ai-orch opencode --model-only --governance-intent <reason> [-- <opencode args...>]

Creates a governed ai-orch session, exports AI_ORCH_SESSION_ID and
AI_ORCH_SESSION_TOKEN for the child process, then launches OpenCode.

Examples:
  ai-orch opencode -- .
  ai-orch opencode -- run --model ai-orch/copilot-gpt-5-mini "Write tests"
  ai-orch opencode --model-only --governance-intent "Need direct model exploration" -- run --model ai-orch/coding-gpt55 "Explore the options"

Developers should launch OpenCode through this wrapper for governed mode.
They do not need to copy session IDs or gateway tokens.`)
		return
	}
	opts := parseOpenCodeWrapperArgs(args)
	if err := validateOpenCodeWrapperOptions(opts); err != nil {
		fmt.Fprintf(os.Stderr, "invalid OpenCode governance options: %v\n", err)
		os.Exit(2)
	}
	if _, err := exec.LookPath("opencode"); err != nil {
		fmt.Fprintf(os.Stderr, "opencode binary not found: %v\n", err)
		os.Exit(2)
	}
	if opts.SessionPrompt == "" {
		opts.SessionPrompt = defaultOpenCodeSessionPrompt(opts)
	}
	openCodeArgs := withDefaultOpenCodeModel(opts.OpenCodeArgs, defaultOpenCodeLeadModel(ctx, cfg, http.DefaultClient))
	if shouldRouteOpenCodeSession(opts) {
		openCodeArgs = withDefaultOpenCodeAgent(openCodeArgs, defaultOpenCodeLeadAgent)
	}
	session, err := createOpenCodeGovernedSession(ctx, cfg, opts)
	if err != nil {
		fmt.Fprintf(os.Stderr, "create governed OpenCode session failed: %v\n", err)
		os.Exit(2)
	}
	runtimeToken := cfg.RuntimeToken
	if runtimeToken == "" {
		runtimeToken = "local-runtime-token"
	}
	fmt.Fprintf(os.Stderr, "ai-orch governed OpenCode session: %s\n", session.SessionID)
	if session.Specialist != "" {
		fmt.Fprintf(os.Stderr, "ai-orch routed specialist suggestion: %s\n", session.Specialist)
	}
	cmd := exec.CommandContext(ctx, "opencode", openCodeArgs...)
	cmd.Env = append(os.Environ(),
		"AI_ORCH_RUNTIME_TOKEN="+runtimeToken,
		"AI_ORCH_SESSION_ID="+session.SessionID,
		"AI_ORCH_SESSION_TOKEN="+session.GatewayToken,
	)
	if identity := localIdentity(); identity != "" {
		cmd.Env = append(cmd.Env, "AI_ORCH_ACTOR_SUBJECT="+identity)
	}
	if opts.Intent != "" {
		cmd.Env = append(cmd.Env, "AI_ORCH_INTENT="+opts.Intent)
	}
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "opencode failed: %v\n", err)
		os.Exit(2)
	}
}

func parseOpenCodeWrapperArgs(args []string) openCodeWrapperOptions {
	opts := openCodeWrapperOptions{
		GovernanceAgent: defaultOpenCodeLeadAgent,
		Classification:  "internal",
	}
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			opts.OpenCodeArgs = append(opts.OpenCodeArgs, args[i+1:]...)
			break
		}
		switch arg {
		case "--governance-agent":
			if i+1 < len(args) {
				i++
				opts.GovernanceAgent = args[i]
				opts.GovernanceAgentExplicit = true
			}
		case "--governance-classification":
			if i+1 < len(args) {
				i++
				opts.Classification = args[i]
			}
		case "--governance-prompt":
			if i+1 < len(args) {
				i++
				opts.SessionPrompt = args[i]
			}
		case "--governance-intent", "--governance-reason":
			if i+1 < len(args) {
				i++
				opts.Intent = args[i]
			}
		case "--model-only":
			opts.ModelOnly = true
			opts.GovernanceAgent = defaultOpenCodeModelOnlyAgent
		default:
			opts.OpenCodeArgs = append(opts.OpenCodeArgs, arg)
		}
	}
	return opts
}

func validateOpenCodeWrapperOptions(opts openCodeWrapperOptions) error {
	if opts.ModelOnly && strings.TrimSpace(opts.Intent) == "" {
		return errors.New("--model-only requires --governance-intent <reason>")
	}
	if opts.ModelOnly && strings.TrimSpace(opts.GovernanceAgent) != defaultOpenCodeModelOnlyAgent {
		return errors.New("--model-only must use the model-gateway governance agent")
	}
	return nil
}

func defaultOpenCodeSessionPrompt(opts openCodeWrapperOptions) string {
	command := "interactive session"
	if len(opts.OpenCodeArgs) > 0 {
		command = "opencode " + strings.Join(opts.OpenCodeArgs, " ")
	}
	if opts.ModelOnly {
		return "Governed OpenCode model-only session: " + command
	}
	if opts.GovernanceAgent == defaultOpenCodeLeadAgent {
		return "Governance lead for OpenCode: clarify intent, classify risk, attach context, and choose a specialist before delivery. Session: " + command
	}
	return "Governed OpenCode specialist session: " + command
}

func createOpenCodeGovernedSession(ctx context.Context, cfg Config, opts openCodeWrapperOptions) (openCodeSessionTokens, error) {
	permissionMode := "reviewed"
	if shouldRouteOpenCodeSession(opts) {
		permissionMode = "read_only"
	}
	// The wrapper hands the session token straight to a developer-driven
	// local OpenCode process; no specialist confirm gate is ever exercised
	// on this lane, so the session must not claim manual approval.
	bodyMap := map[string]any{
		"agent":           opts.GovernanceAgent,
		"classification":  opts.Classification,
		"prompt":          opts.SessionPrompt,
		"permission_mode": permissionMode,
		"approval_mode":   "self_reported",
		"workspace_mode":  "local",
		"source_system":   "opencode",
	}
	if strings.TrimSpace(opts.Intent) != "" {
		bodyMap["intent"] = strings.TrimSpace(opts.Intent)
	}
	addLocalProjectContext(bodyMap)
	body, _ := json.Marshal(bodyMap)
	endpoint := "/v1/sessions"
	if shouldRouteOpenCodeSession(opts) {
		endpoint = "/v1/runs"
	}
	resp, err := doPost(ctx, cfg, cfg.GovernanceURL+endpoint, body)
	if err != nil {
		return openCodeSessionTokens{}, err
	}
	var session struct {
		SessionID    string `json:"session_id"`
		GatewayToken string `json:"gateway_token"`
		Specialist   string `json:"specialist"`
	}
	if err := json.Unmarshal([]byte(resp), &session); err != nil || session.SessionID == "" || session.GatewayToken == "" {
		return openCodeSessionTokens{}, fmt.Errorf("unexpected session response: %s", resp)
	}
	return openCodeSessionTokens{SessionID: session.SessionID, GatewayToken: session.GatewayToken, Specialist: session.Specialist}, nil
}

func shouldRouteOpenCodeSession(opts openCodeWrapperOptions) bool {
	return !opts.ModelOnly && opts.GovernanceAgent == defaultOpenCodeLeadAgent
}

func defaultOpenCodeLeadModel(_ context.Context, _ Config, _ *http.Client) string {
	return defaultOpenCodeFallbackModel
}

func withDefaultOpenCodeModel(args []string, model string) []string {
	out := append([]string{}, args...)
	if strings.TrimSpace(model) == "" || openCodeArgsHaveModel(out) {
		return out
	}
	if len(out) > 0 && out[0] == "run" {
		return append([]string{"run", "--model", model}, out[1:]...)
	}
	return append([]string{"--model", model}, out...)
}

func withDefaultOpenCodeAgent(args []string, agent string) []string {
	out := append([]string{}, args...)
	if strings.TrimSpace(agent) == "" || openCodeArgsHaveAgent(out) {
		return out
	}
	if len(out) > 0 && out[0] == "run" {
		return append([]string{"run", "--agent", agent}, out[1:]...)
	}
	return append([]string{"--agent", agent}, out...)
}

func openCodeArgsHaveModel(args []string) bool {
	for i, arg := range args {
		if arg == "--model" || arg == "-m" {
			return i+1 < len(args)
		}
		if strings.HasPrefix(arg, "--model=") || strings.HasPrefix(arg, "-m=") {
			return true
		}
	}
	return false
}

func openCodeArgsHaveAgent(args []string) bool {
	for i, arg := range args {
		if arg == "--agent" {
			return i+1 < len(args)
		}
		if strings.HasPrefix(arg, "--agent=") {
			return true
		}
	}
	return false
}
