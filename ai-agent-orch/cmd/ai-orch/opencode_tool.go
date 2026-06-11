package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/kamo62/ai-governance-orchestration/ai-agent-orch/internal/betasmoke"
	"github.com/kamo62/ai-governance-orchestration/ai-agent-orch/internal/envx"
)

const (
	defaultGatewayURL = "http://127.0.0.1:18082"
	defaultModel      = "ai-orch/coding-gpt55"
	defaultSmallModel = "ai-orch/coding-fast"
)

// GenerateOpenCodeConfig creates an OpenCode provider configuration
// that routes model calls through the ai-orch Governance Shell.
func GenerateOpenCodeConfig(gatewayURL string) map[string]any {
	return GenerateOpenCodeConfigWithOptions(OpenCodeConfigOptions{GatewayURL: gatewayURL, UseEnvPlaceholders: true})
}

type OpenCodeConfigOptions struct {
	GatewayURL         string
	RuntimeToken       string
	ActorSubject       string
	Classification     string
	UseEnvPlaceholders bool
}

func GenerateOpenCodeConfigWithOptions(opts OpenCodeConfigOptions) map[string]any {
	return map[string]any{
		"$schema":           "https://opencode.ai/config.json",
		"enabled_providers": []string{"ai-orch"},
		"model":             defaultModel,
		"small_model":       defaultSmallModel,
		"agent":             defaultOpenCodeAgentConfig(),
		"provider": map[string]any{
			"ai-orch": aiOrchProviderConfig(opts),
		},
	}
}

func openCodeBaseURL(gatewayURL string) string {
	baseURL := strings.TrimRight(strings.TrimSpace(gatewayURL), "/")
	if baseURL == "" {
		baseURL = defaultGatewayURL
	}
	if strings.HasSuffix(baseURL, "/v1") {
		return baseURL
	}
	return baseURL + "/v1"
}

func installOpenCodeConfig(gatewayURL string, args []string) error {
	fs := flag.NewFlagSet("install-config", flag.ContinueOnError)
	scope := fs.String("scope", "project", "config scope: project or global")
	configPath := fs.String("path", "", "explicit opencode.json path")
	force := fs.Bool("force", false, "replace an existing ai-orch provider block")
	runtimeToken := fs.String("runtime-token", defaultRuntimeTokenForInstall(), "ai-orch runtime token to write into OpenCode config")
	actorSubject := fs.String("actor-subject", defaultActorSubjectForInstall(), "actor subject to write into OpenCode config")
	classification := fs.String("classification", envx.OrDefault("AI_ORCH_GATEWAY_CLASSIFICATION", "internal"), "classification header to write into OpenCode config")
	envPlaceholders := fs.Bool("env-placeholders", false, "write {env:...} placeholders instead of concrete runtime token and actor headers")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if !*envPlaceholders && strings.TrimSpace(*runtimeToken) == "" {
		return errors.New("runtime token is required; pass --runtime-token or set AI_ORCH_RUNTIME_TOKEN")
	}
	if !*envPlaceholders && strings.TrimSpace(*actorSubject) == "" {
		return errors.New("actor subject is required; pass --actor-subject or set AI_ORCH_ACTOR_SUBJECT")
	}

	target, err := resolveOpenCodeConfigPath(*scope, *configPath)
	if err != nil {
		return err
	}

	existing, existingMode, err := readOpenCodeConfig(target)
	if err != nil {
		return err
	}
	merged, changed, err := mergeOpenCodeConfigWithOptions(existing, OpenCodeConfigOptions{
		GatewayURL:         gatewayURL,
		RuntimeToken:       *runtimeToken,
		ActorSubject:       *actorSubject,
		Classification:     *classification,
		UseEnvPlaceholders: *envPlaceholders,
	}, *force)
	if err != nil {
		return err
	}
	if !changed {
		fmt.Printf("OpenCode config already routes ai-orch: %s\n", target)
		return nil
	}

	writeMode := openCodeConfigMode(existingMode, !*envPlaceholders)
	backupPath, err := writeOpenCodeConfig(target, merged, writeMode)
	if err != nil {
		return err
	}
	fmt.Printf("OpenCode config installed: %s\n", target)
	if backupPath != "" {
		fmt.Printf("Backup: %s\n", backupPath)
	}
	fmt.Println("Provider: ai-orch")
	fmt.Printf("Base URL: %s\n", openCodeBaseURL(gatewayURL))
	if *envPlaceholders {
		fmt.Println("Runtime token source: {env:AI_ORCH_RUNTIME_TOKEN}")
		fmt.Println("Actor subject source: {env:AI_ORCH_ACTOR_SUBJECT}")
	} else {
		fmt.Println("Runtime token source: stored in OpenCode config")
		fmt.Printf("Actor subject: %s\n", *actorSubject)
	}
	fmt.Println("Session headers: optional; gateway auto-creates sessions when absent")
	return nil
}

func defaultRuntimeTokenForInstall() string {
	return envx.OrDefault("AI_ORCH_RUNTIME_TOKEN", "local-runtime-token")
}

func defaultActorSubjectForInstall() string {
	if v := strings.TrimSpace(os.Getenv("AI_ORCH_ACTOR_SUBJECT")); v != "" {
		return v
	}
	if v := strings.TrimSpace(os.Getenv("USER")); v != "" {
		return v
	}
	if v := strings.TrimSpace(os.Getenv("USERNAME")); v != "" {
		return v
	}
	return "local-dev"
}

func resolveOpenCodeConfigPath(scope, explicitPath string) (string, error) {
	if strings.TrimSpace(explicitPath) != "" {
		return expandHome(strings.TrimSpace(explicitPath)), nil
	}
	switch strings.ToLower(strings.TrimSpace(scope)) {
	case "", "project":
		root := strings.TrimSpace(os.Getenv("OPENCODE_PROJECT_DIR"))
		if root == "" {
			var err error
			root, err = os.Getwd()
			if err != nil {
				return "", err
			}
		}
		return filepath.Join(root, "opencode.json"), nil
	case "global":
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		configHome := strings.TrimSpace(os.Getenv("XDG_CONFIG_HOME"))
		if configHome == "" {
			configHome = filepath.Join(home, ".config")
		}
		return filepath.Join(configHome, "opencode", "opencode.json"), nil
	default:
		return "", fmt.Errorf("unsupported scope %q; use project, global, or --path", scope)
	}
}

func expandHome(path string) string {
	if path == "~" {
		home, err := os.UserHomeDir()
		if err == nil {
			return home
		}
	}
	if strings.HasPrefix(path, "~/") || strings.HasPrefix(path, "~"+string(filepath.Separator)) {
		home, err := os.UserHomeDir()
		if err == nil {
			return filepath.Join(home, path[2:])
		}
	}
	return path
}

func readOpenCodeConfig(path string) (map[string]any, os.FileMode, error) {
	info, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return map[string]any{}, 0o644, nil
	}
	if err != nil {
		return nil, 0, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, 0, err
	}
	if len(strings.TrimSpace(string(data))) == 0 {
		return map[string]any{}, info.Mode().Perm(), nil
	}
	var config map[string]any
	if err := json.Unmarshal(data, &config); err != nil {
		return nil, 0, fmt.Errorf("parse %s as JSON: %w; use --path for a generated config or remove JSONC comments before patching", path, err)
	}
	return config, info.Mode().Perm(), nil
}

func openCodeConfigMode(existing os.FileMode, containsConcreteRuntimeToken bool) os.FileMode {
	if containsConcreteRuntimeToken {
		return 0o600
	}
	if existing == 0 {
		return 0o644
	}
	return existing
}

func mergeOpenCodeConfigWithOptions(config map[string]any, opts OpenCodeConfigOptions, force bool) (map[string]any, bool, error) {
	if config == nil {
		config = map[string]any{}
	}
	changed := false
	if config["$schema"] == nil {
		config["$schema"] = "https://opencode.ai/config.json"
		changed = true
	}

	providers, ok := config["provider"].(map[string]any)
	if !ok || providers == nil {
		providers = map[string]any{}
		config["provider"] = providers
		changed = true
	}

	nextProvider := aiOrchProviderConfig(opts)
	if current, ok := providers["ai-orch"]; ok {
		equal, err := jsonEqual(current, nextProvider)
		if err != nil {
			return nil, false, err
		}
		if !equal && !force {
			return nil, false, errors.New("provider.ai-orch already exists and differs; rerun with --force to update it")
		}
		if !equal {
			providers["ai-orch"] = nextProvider
			changed = true
		}
	} else {
		providers["ai-orch"] = nextProvider
		changed = true
	}

	enabled, enabledChanged := ensureStringListIncludes(config["enabled_providers"], "ai-orch")
	if enabledChanged {
		config["enabled_providers"] = enabled
		changed = true
	}
	disabled, disabledChanged := removeStringListValue(config["disabled_providers"], "ai-orch")
	if disabledChanged {
		config["disabled_providers"] = disabled
		changed = true
	}

	if config["model"] != defaultModel {
		config["model"] = defaultModel
		changed = true
	}
	if config["small_model"] != defaultSmallModel {
		config["small_model"] = defaultSmallModel
		changed = true
	}
	if ensureOpenCodeAgentConfig(config) {
		changed = true
	}
	return config, changed, nil
}

func ensureOpenCodeAgentConfig(config map[string]any) bool {
	if config == nil {
		return false
	}
	defaults := defaultOpenCodeAgentConfig()
	agents, ok := config["agent"].(map[string]any)
	if !ok || agents == nil {
		config["agent"] = defaults
		return true
	}
	changed := false
	for name, definition := range defaults {
		if _, exists := agents[name]; exists {
			continue
		}
		agents[name] = definition
		changed = true
	}
	return changed
}

func defaultOpenCodeAgentConfig() map[string]any {
	readOnly := map[string]any{
		"read": "allow",
		"list": "allow",
		"grep": "allow",
		"glob": "allow",
		"edit": "deny",
		"bash": "deny",
	}
	writeWithReview := map[string]any{
		"read": "allow",
		"list": "allow",
		"grep": "allow",
		"glob": "allow",
		"edit": "ask",
		"bash": "ask",
	}
	return map[string]any{
		"governance-lead": map[string]any{
			"description":     "Clarifies intent, classifies risk, attaches context, and chooses the next ai-orch specialist.",
			"mode":            "primary",
			"model":           "ai-orch/coding-gpt55",
			"reasoningEffort": "low",
			"temperature":     0.1,
			"steps":           6,
			"permission": map[string]any{
				"read": "allow",
				"list": "allow",
				"grep": "allow",
				"glob": "allow",
				"edit": "deny",
				"bash": "deny",
				"task": []string{
					"code-review",
					"unit-tests",
					"backend-development",
					"frontend-development",
					"security-review",
					"architecture-review",
					"documentation",
					"refactor",
					"security-scan",
					"terraform-review",
				},
			},
			"prompt": "You are the ai-orch governance lead. Clarify intent, classify risk, attach work-item context, and choose a specialist subagent. Do not edit files or run shell commands.",
		},
		"code-review": map[string]any{
			"description":     "Reviews code and diffs for bugs, regressions, and missing tests.",
			"mode":            "subagent",
			"reasoningEffort": "medium",
			"permission":      readOnly,
			"prompt":          "Review code with severity-ordered findings. Do not edit files.",
		},
		"unit-tests": map[string]any{
			"description":     "Adds or updates focused tests using existing project patterns.",
			"mode":            "subagent",
			"reasoningEffort": "medium",
			"permission":      writeWithReview,
			"prompt":          "Add focused tests for the requested behavior. Follow existing test patterns and avoid unrelated refactors.",
		},
		"backend-development": map[string]any{
			"description":     "Implements backend code, APIs, data access, and server-side tests.",
			"mode":            "subagent",
			"reasoningEffort": "medium",
			"permission":      writeWithReview,
			"prompt":          "Implement backend changes narrowly, with tests and existing project conventions.",
		},
		"frontend-development": map[string]any{
			"description":     "Implements frontend UI, client behavior, and browser-facing tests.",
			"mode":            "subagent",
			"reasoningEffort": "medium",
			"permission":      writeWithReview,
			"prompt":          "Implement frontend changes narrowly, preserving accessibility and existing design patterns.",
		},
		"security-review": map[string]any{
			"description":     "Reviews security-sensitive code, dependencies, containers, and data exposure risks.",
			"mode":            "subagent",
			"reasoningEffort": "high",
			"permission":      readOnly,
			"prompt":          "Perform a security review with concrete findings. Do not print raw secrets.",
		},
		"architecture-review": map[string]any{
			"description":     "Reviews service boundaries, data flow, runtime shape, and tradeoffs.",
			"mode":            "subagent",
			"reasoningEffort": "medium",
			"permission":      readOnly,
			"prompt":          "Assess architecture, boundaries, risks, and tradeoffs without editing files.",
		},
		"documentation": map[string]any{
			"description":     "Improves developer-facing documentation.",
			"mode":            "subagent",
			"reasoningEffort": "medium",
			"permission":      writeWithReview,
			"prompt":          "Update documentation to reflect implemented behavior. Remove stale instructions.",
		},
		"refactor": map[string]any{
			"description":     "Performs scoped behavior-preserving cleanup.",
			"mode":            "subagent",
			"reasoningEffort": "medium",
			"permission":      writeWithReview,
			"prompt":          "Refactor narrowly without changing public behavior.",
		},
		"security-scan": map[string]any{
			"description":     "Scans for likely secret exposure and auth or data handling issues.",
			"mode":            "subagent",
			"reasoningEffort": "high",
			"permission":      readOnly,
			"prompt":          "Scan for security risks and summarize findings without exposing raw secrets.",
		},
		"terraform-review": map[string]any{
			"description":     "Reviews Terraform and infrastructure definitions for security, cost, and reliability.",
			"mode":            "subagent",
			"reasoningEffort": "medium",
			"permission":      readOnly,
			"prompt":          "Review infrastructure code for security, cost, reliability, and state-management risks.",
		},
	}
}

func aiOrchProviderConfig(opts OpenCodeConfigOptions) map[string]any {
	apiKey := strings.TrimSpace(opts.RuntimeToken)
	actorSubject := strings.TrimSpace(opts.ActorSubject)
	if opts.UseEnvPlaceholders {
		apiKey = "{env:AI_ORCH_RUNTIME_TOKEN}"
		actorSubject = "{env:AI_ORCH_ACTOR_SUBJECT}"
	}
	classification := strings.TrimSpace(opts.Classification)
	if classification == "" {
		classification = "internal"
	}
	return map[string]any{
		"npm":  "@ai-sdk/openai-compatible",
		"name": "AI Orch Governed Router",
		"options": map[string]any{
			"baseURL": openCodeBaseURL(opts.GatewayURL),
			"apiKey":  apiKey,
			"headers": map[string]any{
				"X-AI-Orch-Session-ID":     "{env:AI_ORCH_SESSION_ID}",
				"X-AI-Orch-Session-Token":  "{env:AI_ORCH_SESSION_TOKEN}",
				"X-AI-Orch-Actor-Subject":  actorSubject,
				"X-AI-Orch-Intent":         "{env:AI_ORCH_INTENT}",
				"X-AI-Orch-Client":         "opencode",
				"X-AI-Orch-Classification": classification,
			},
		},
		"models": map[string]any{
			"coding-gpt55": map[string]any{
				"name": "Governed GPT-5.5 Capability Route",
			},
			"openrouter-openai-gpt55": map[string]any{
				"name": "Governed OpenRouter OpenAI GPT-5.5",
			},
			"copilot-gpt-5-mini": map[string]any{
				"name": "Governed Copilot GPT-5 Mini",
			},
			"copilot-gpt-5.3-codex": map[string]any{
				"name": "Governed Copilot GPT-5.3 Codex",
			},
			"copilot-gpt-5.5": map[string]any{
				"name": "Governed Copilot GPT-5.5",
			},
			"coding-primary": map[string]any{
				"name": "Governed Coding Primary",
			},
			"coding-balanced": map[string]any{
				"name": "Governed Coding Balanced",
			},
			"coding-fast": map[string]any{
				"name": "Governed Coding Fast",
			},
			"coding-economy": map[string]any{
				"name": "Governed Coding Economy",
			},
		},
	}
}

func ensureStringListIncludes(value any, item string) ([]string, bool) {
	list, _ := stringList(value)
	for _, current := range list {
		if current == item {
			return list, false
		}
	}
	return append(list, item), true
}

func removeStringListValue(value any, item string) ([]string, bool) {
	list, ok := stringList(value)
	if !ok {
		return list, false
	}
	next := make([]string, 0, len(list))
	changed := false
	for _, current := range list {
		if current == item {
			changed = true
			continue
		}
		next = append(next, current)
	}
	return next, changed
}

func stringList(value any) ([]string, bool) {
	switch typed := value.(type) {
	case nil:
		return []string{}, true
	case []string:
		return append([]string{}, typed...), true
	case []any:
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			if s, ok := item.(string); ok && strings.TrimSpace(s) != "" {
				out = append(out, s)
			}
		}
		return out, true
	default:
		return []string{}, false
	}
}

func jsonEqual(a, b any) (bool, error) {
	aJSON, err := json.Marshal(a)
	if err != nil {
		return false, err
	}
	bJSON, err := json.Marshal(b)
	if err != nil {
		return false, err
	}
	return bytes.Equal(aJSON, bJSON), nil
}

func writeOpenCodeConfig(path string, config map[string]any, mode os.FileMode) (string, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	var backupPath string
	if _, err := os.Stat(path); err == nil {
		backupPath = path + ".bak." + time.Now().UTC().Format("20060102T150405Z")
		if err := copyFile(path, backupPath, mode); err != nil {
			return "", err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}

	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return "", err
	}
	data = append(data, '\n')
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, mode); err != nil {
		return "", err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return "", err
	}
	return backupPath, nil
}

func copyFile(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return err
	}
	defer out.Close()
	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Close()
}

// openCodeToolSubcommands lists the OpenCode setup/verification subcommands
// served by handleOpenCodeTool; anything else under "ai-orch opencode" is the
// governed launch wrapper.
var openCodeToolSubcommands = map[string]bool{
	"generate-config": true,
	"install-config":  true,
	"verify":          true,
	"run":             true,
	"e2e":             true,
	"gateway-smoke":   true,
}

func handleOpenCodeTool(args []string) {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "usage: ai-orch opencode <generate-config|install-config|verify|run|e2e|gateway-smoke>")
		os.Exit(1)
	}

	gatewayURL := os.Getenv("AI_ORCH_MODEL_GATEWAY_URL")
	if gatewayURL == "" {
		gatewayURL = defaultGatewayURL
	}

	switch args[0] {
	case "gateway-smoke":
		if err := runGatewaySmoke(gatewayURL); err != nil {
			fmt.Fprintf(os.Stderr, "gateway smoke failed: %v\n", err)
			os.Exit(2)
		}

	case "generate-config":
		config := GenerateOpenCodeConfig(gatewayURL)
		out, err := json.MarshalIndent(config, "", "  ")
		if err != nil {
			fmt.Fprintf(os.Stderr, "encode config: %v\n", err)
			os.Exit(1)
		}
		fmt.Println(string(out))

	case "install-config":
		if err := installOpenCodeConfig(gatewayURL, args[1:]); err != nil {
			fmt.Fprintf(os.Stderr, "install OpenCode config failed: %v\n", err)
			os.Exit(2)
		}

	case "verify":
		runtimeToken := os.Getenv("AI_ORCH_RUNTIME_TOKEN")
		sessionID := os.Getenv("AI_ORCH_SESSION_ID")
		fmt.Printf("Gateway URL: %s\n", gatewayURL)
		if runtimeToken == "" {
			fmt.Println("Runtime token configured: no")
		} else {
			fmt.Println("Runtime token configured: yes")
		}
		if sessionID == "" {
			fmt.Println("Session ID configured: no")
		} else {
			fmt.Println("Session ID configured: yes")
		}
		fmt.Println("\nTo configure OpenCode:")
		fmt.Println("1. Install config with: ai-orch opencode install-config --scope project")
		fmt.Println("2. Or set OPENCODE_CONFIG to a generated file")
		fmt.Println("3. Set AI_ORCH_RUNTIME_TOKEN, AI_ORCH_SESSION_ID, and AI_ORCH_SESSION_TOKEN for manual runs")
		fmt.Println("4. Run OpenCode with model 'ai-orch/coding-balanced'")
		fmt.Println("\nNo OpenRouter, Anthropic, or other provider keys should be in OpenCode.")

	case "run":
		if err := runOpenCode(gatewayURL); err != nil {
			fmt.Fprintf(os.Stderr, "opencode smoke run failed: %v\n", err)
			os.Exit(2)
		}

	case "e2e":
		if err := runOpenCodeE2E(gatewayURL, args[1:]); err != nil {
			fmt.Fprintf(os.Stderr, "opencode e2e failed: %v\n", err)
			os.Exit(2)
		}

	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n", os.Args[1])
		os.Exit(1)
	}
}

func runOpenCode(gatewayURL string) error {
	if os.Getenv("AI_ORCH_RUNTIME_TOKEN") == "" {
		return fmt.Errorf("AI_ORCH_RUNTIME_TOKEN is required")
	}
	if os.Getenv("AI_ORCH_SESSION_ID") == "" {
		return fmt.Errorf("AI_ORCH_SESSION_ID is required so model calls can be audited")
	}
	if _, err := exec.LookPath("opencode"); err != nil {
		return fmt.Errorf("opencode binary not found; use local OpenCode or the docker compose opencode-sandbox profile")
	}

	configFile, err := os.CreateTemp("", "ai-orch-opencode-*.json")
	if err != nil {
		return fmt.Errorf("create temporary OpenCode config: %w", err)
	}
	defer os.Remove(configFile.Name())

	if err := json.NewEncoder(configFile).Encode(GenerateOpenCodeConfig(gatewayURL)); err != nil {
		configFile.Close()
		return fmt.Errorf("write temporary OpenCode config: %w", err)
	}
	if err := configFile.Close(); err != nil {
		return fmt.Errorf("close temporary OpenCode config: %w", err)
	}

	targetDir := envx.OrDefault("OPENCODE_TARGET_DIR", ".")
	model := envx.OrDefault("OPENCODE_MODEL", defaultModel)
	agent := envx.OrDefault("OPENCODE_AGENT", "governance-lead")
	prompt := envx.OrDefault("OPENCODE_PROMPT", "Review this workspace briefly without editing files.")

	cmd := exec.Command("opencode", "run", "--dir", targetDir, "--model", model, "--agent", agent, prompt)
	cmd.Env = append(os.Environ(), "OPENCODE_CONFIG="+configFile.Name())
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	return cmd.Run()
}

func runGatewaySmoke(gatewayURL string) error {
	cfg := betasmoke.LoadConfigFromEnv()
	if gatewayURL != "" {
		cfg.GatewayURL = gatewayURL
	}
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
	defer cancel()
	return betasmoke.RunGatewaySmoke(ctx, cfg)
}
