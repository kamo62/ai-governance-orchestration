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

	"ai-agent-orch/internal/betasmoke"
)

const (
	defaultGatewayURL = "http://127.0.0.1:18082"
	defaultModel      = "ai-orch/coding-balanced"
	defaultSmallModel = "ai-orch/coding-fast"
)

// GenerateOpenCodeConfig creates an OpenCode provider configuration
// that routes model calls through the ai-orch Governance Shell.
func GenerateOpenCodeConfig(gatewayURL string) map[string]any {
	return map[string]any{
		"$schema":           "https://opencode.ai/config.json",
		"enabled_providers": []string{"ai-orch"},
		"model":             defaultModel,
		"small_model":       defaultSmallModel,
		"provider": map[string]any{
			"ai-orch": aiOrchProviderConfig(gatewayURL),
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
	if err := fs.Parse(args); err != nil {
		return err
	}

	target, err := resolveOpenCodeConfigPath(*scope, *configPath)
	if err != nil {
		return err
	}

	existing, existingMode, err := readOpenCodeConfig(target)
	if err != nil {
		return err
	}
	merged, changed, err := mergeOpenCodeConfig(existing, gatewayURL, *force)
	if err != nil {
		return err
	}
	if !changed {
		fmt.Printf("OpenCode config already routes ai-orch: %s\n", target)
		return nil
	}

	backupPath, err := writeOpenCodeConfig(target, merged, existingMode)
	if err != nil {
		return err
	}
	fmt.Printf("OpenCode config installed: %s\n", target)
	if backupPath != "" {
		fmt.Printf("Backup: %s\n", backupPath)
	}
	fmt.Println("Provider: ai-orch")
	fmt.Printf("Base URL: %s\n", openCodeBaseURL(gatewayURL))
	fmt.Println("Runtime token source: {env:AI_ORCH_RUNTIME_TOKEN}")
	fmt.Println("Session header source: {env:AI_ORCH_SESSION_ID}")
	return nil
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

func mergeOpenCodeConfig(config map[string]any, gatewayURL string, force bool) (map[string]any, bool, error) {
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

	nextProvider := aiOrchProviderConfig(gatewayURL)
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
	return config, changed, nil
}

func aiOrchProviderConfig(gatewayURL string) map[string]any {
	return map[string]any{
		"npm":  "@ai-sdk/openai-compatible",
		"name": "AI Orch Governed Router",
		"options": map[string]any{
			"baseURL": openCodeBaseURL(gatewayURL),
			"apiKey":  "{env:AI_ORCH_RUNTIME_TOKEN}",
			"headers": map[string]any{
				"X-AI-Orch-Session-ID": "{env:AI_ORCH_SESSION_ID}",
			},
		},
		"models": map[string]any{
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

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: opencode-smoke <generate-config|install-config|verify|run|e2e|gateway-smoke>")
		os.Exit(1)
	}

	gatewayURL := os.Getenv("AI_ORCH_MODEL_GATEWAY_URL")
	if gatewayURL == "" {
		gatewayURL = defaultGatewayURL
	}

	switch os.Args[1] {
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
		if err := installOpenCodeConfig(gatewayURL, os.Args[2:]); err != nil {
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
		fmt.Println("1. Install config with: opencode-smoke install-config --scope project")
		fmt.Println("2. Or set OPENCODE_CONFIG to a generated file")
		fmt.Println("3. Set AI_ORCH_RUNTIME_TOKEN and AI_ORCH_SESSION_ID for manual runs")
		fmt.Println("4. Run OpenCode with model 'ai-orch/coding-balanced'")
		fmt.Println("\nNo OpenRouter, Anthropic, or other provider keys should be in OpenCode.")

	case "run":
		if err := runOpenCode(gatewayURL); err != nil {
			fmt.Fprintf(os.Stderr, "opencode smoke run failed: %v\n", err)
			os.Exit(2)
		}

	case "e2e":
		if err := runOpenCodeE2E(gatewayURL, os.Args[2:]); err != nil {
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

	targetDir := envOrDefault("OPENCODE_TARGET_DIR", ".")
	model := envOrDefault("OPENCODE_MODEL", defaultModel)
	prompt := envOrDefault("OPENCODE_PROMPT", "Review this workspace briefly without editing files.")

	cmd := exec.Command("opencode", "run", "--dir", targetDir, "--model", model, prompt)
	cmd.Env = append(os.Environ(), "OPENCODE_CONFIG="+configFile.Name())
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	return cmd.Run()
}

func envOrDefault(key string, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
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
