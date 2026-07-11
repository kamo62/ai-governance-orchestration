package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/kamo62/ai-governance-orchestration/ai-agent-orch/internal/contextresolver"
	"github.com/kamo62/ai-governance-orchestration/ai-agent-orch/internal/envx"
)

const (
	defaultGovernanceURL   = "http://127.0.0.1:18080"
	defaultOrchestratorURL = "http://127.0.0.1:8081"
	defaultModelGatewayURL = "http://127.0.0.1:18082"

	defaultOpenCodeLeadAgent      = "governance-lead"
	defaultOpenCodeModelOnlyAgent = "model-gateway"
	defaultOpenCodeFallbackModel  = "ai-orch/coding-gpt55"
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
		GovernanceURL:      envx.OrDefault("AI_ORCH_GOVERNANCE_URL", defaultGovernanceURL),
		OrchestratorURL:    envx.OrDefault("AI_ORCH_ORCHESTRATOR_URL", defaultOrchestratorURL),
		ModelGatewayURL:    envx.OrDefault("AI_ORCH_MODEL_GATEWAY_URL", defaultModelGatewayURL),
		Token:              envx.OrDefault("AI_ORCH_DEV_TOKEN", ""),
		AdminToken:         envx.OrDefault("AI_ORCH_ADMIN_TOKEN", ""),
		RuntimeToken:       envx.OrDefault("AI_ORCH_RUNTIME_TOKEN", ""),
		TrustedClientToken: envx.OrDefault("AI_ORCH_TRUSTED_CLIENT_TOKEN", ""),
	}
}

// localIdentity is the actor label asserted on dev-token requests so sessions
// and Copilot enrollment share one subject. AI_ORCH_ACTOR_SUBJECT overrides
// the OS username.
func localIdentity() string {
	if v := strings.TrimSpace(os.Getenv("AI_ORCH_ACTOR_SUBJECT")); v != "" {
		return v
	}
	return strings.TrimSpace(os.Getenv("USER"))
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
	case "hook":
		handleHook(ctx, cfg, os.Args[2:])
	case "copilot":
		handleCopilot(ctx, cfg, os.Args[2:])
	case "developer":
		handleDeveloper(ctx, cfg, os.Args[2:])
	case "bench":
		handleBench(ctx, cfg, os.Args[2:])
	case "opencode":
		if len(os.Args) > 2 && openCodeToolSubcommands[os.Args[2]] {
			handleOpenCodeTool(os.Args[2:])
			return
		}
		handleOpenCode(ctx, cfg, os.Args[2:])
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
  ai-orch session confirm --session-id <id> --agent <name> [--human]
  ai-orch session events --session-id <id>
  ai-orch audit lookup --session-id <id>
  ai-orch audit verify --session-id <id>
  ai-orch killswitch status
  ai-orch killswitch toggle --scope <scope> --id <id> [--enable|--disable]
  ai-orch smoke [--prompt <text>]
  ai-orch agents list
  ai-orch negative secret|classification|killswitch|cost
  ai-orch mcp start [--transport http|stdio] [--host 127.0.0.1] [--port 18081]
  ai-orch mcp install --client <vscode|cline|claude-code|codex> [--force]
  ai-orch mcp doctor
  ai-orch hook prompt-submit|post-tool|stop  (reads lifecycle event JSON on stdin)
  ai-orch developer enroll --client opencode [--scope global|project]
  ai-orch copilot login|status|models|logout|refresh [--local]
  ai-orch copilot smoke --local [--model <id>] [--prompt <text>]
  ai-orch bench run --workflow <workflow> --models all-enabled
  ai-orch opencode [--governance-agent <name>] [--governance-classification <level>] [--governance-prompt <text>] [-- <opencode args...>]

Environment:
  AI_ORCH_GOVERNANCE_URL    Governance Shell base URL (default: http://127.0.0.1:18080)
  AI_ORCH_ORCHESTRATOR_URL  Orchestrator base URL (default: http://127.0.0.1:8081)
  AI_ORCH_MODEL_GATEWAY_URL Runtime model gateway base URL (default: http://127.0.0.1:18082)
  AI_ORCH_DEV_TOKEN         Bearer token for local dev auth
  AI_ORCH_ADMIN_TOKEN       Bearer token for admin routes such as killswitch
  AI_ORCH_RUNTIME_TOKEN     Bearer token for runtime model gateway calls
  AI_ORCH_ACTOR_SUBJECT     Actor label for dev-token requests (default: OS username)
  AI_ORCH_COPILOT_TOKEN_ENCRYPTION_KEY  Required for copilot --local token storage`)
}

type openCodeSessionTokens struct {
	SessionID    string
	GatewayToken string
	Specialist   string
}

func isHelpOnly(args []string) bool {
	if len(args) != 1 {
		return false
	}
	return args[0] == "-h" || args[0] == "--help"
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
	// In dev-token mode the shell derives the actor from this header. Sending
	// it on every CLI call keeps session ownership and Copilot enrollment
	// keyed to the same actor subject. OIDC deployments ignore it.
	if identity := localIdentity(); identity != "" {
		req.Header.Set("X-AI-Orch-Local-Identity", identity)
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
