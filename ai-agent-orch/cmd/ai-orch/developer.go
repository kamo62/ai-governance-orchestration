package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/kamo62/ai-governance-orchestration/ai-agent-orch/internal/skillsfactory"
)

func handleDeveloper(ctx context.Context, cfg Config, args []string) {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "usage: ai-orch developer enroll --client opencode|claude-code|kiro")
		os.Exit(1)
	}
	switch args[0] {
	case "enroll":
		if err := developerEnroll(ctx, cfg, args[1:]); err != nil {
			fmt.Fprintf(os.Stderr, "developer enrolment failed: %v\n", err)
			os.Exit(2)
		}
	default:
		fmt.Fprintf(os.Stderr, "unknown developer command: %s\n", args[0])
		os.Exit(1)
	}
}

func developerEnroll(ctx context.Context, cfg Config, args []string) error {
	fs := flag.NewFlagSet("developer enroll", flag.ContinueOnError)
	client := fs.String("client", "opencode", "developer client to configure: opencode, claude-code, or kiro")
	scope := fs.String("scope", "global", "OpenCode config scope: global or project")
	configPath := fs.String("path", "", "explicit config path (OpenCode config, or Claude Code settings.json)")
	classification := fs.String("classification", "internal", "classification header for AI-Orch-routed OpenCode")
	installJob := fs.Bool("install-refresh-job", true, "install a user-level AI-Orch-routed OpenCode refresh job")
	dir := fs.String("dir", ".", "working directory for client config generation (claude-code, kiro)")
	force := fs.Bool("force", false, "overwrite existing generated client config files (claude-code, kiro)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	switch *client {
	case "opencode":
		return enrollOpenCode(ctx, cfg, *scope, *configPath, *classification, *installJob)
	case "claude-code":
		return enrollClaudeCode(ctx, cfg, *dir, *configPath, *force)
	case "kiro":
		return enrollKiro(ctx, cfg, *dir, *force)
	default:
		return fmt.Errorf("unsupported client %q; supported: opencode, claude-code, kiro", *client)
	}
}

func enrollOpenCode(ctx context.Context, cfg Config, scope, configPath, classification string, installJob bool) error {
	if _, err := doGet(ctx, cfg, cfg.GovernanceURL+"/v1/copilot/models"); err != nil {
		fmt.Fprintln(os.Stderr, "Copilot enrolment needs refresh; starting GitHub device login")
		copilotRemoteLogin(ctx, cfg)
	}
	cred, err := requestDeveloperRuntimeCredential(ctx, cfg, "opencode")
	if err != nil {
		return err
	}
	installArgs := []string{"--scope", scope, "--force", "--runtime-token", cred.RuntimeToken, "--actor-subject", cred.ActorSubject, "--classification", classification}
	if configPath != "" {
		installArgs = append(installArgs, "--path", configPath)
	}
	if err := installOpenCodeConfig(cfg.ModelGatewayURL, installArgs); err != nil {
		return err
	}
	if installJob {
		path, err := writeOpenCodeRefreshJob(runtime.GOOS, defaultOpenCodeRefreshCommand(scope, configPath))
		if err != nil {
			fmt.Fprintf(os.Stderr, "warning: could not install refresh job: %v\n", err)
		} else if path != "" {
			fmt.Fprintf(os.Stderr, "AI-Orch-routed OpenCode refresh job installed: %s\n", path)
		}
	}
	fmt.Printf("AI-Orch-routed OpenCode enrolled for %s; runtime credential expires %s\n", cred.ActorSubject, cred.ExpiresAt.Format(time.RFC3339))
	return nil
}

// resolveClaudeSettingsPath returns the developer's Claude Code settings.json
// path. An explicit override (the --path flag) wins; otherwise it is
// ~/.claude/settings.json on every OS (Windows resolves %USERPROFILE% via
// os.UserHomeDir).
func resolveClaudeSettingsPath(override string) (string, error) {
	if override != "" {
		return override, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".claude", "settings.json"), nil
}

func enrollClaudeCode(ctx context.Context, cfg Config, dir, settingsOverride string, force bool) error {
	cred, err := requestDeveloperRuntimeCredential(ctx, cfg, "claude-code")
	if err != nil {
		return err
	}
	// Generate CLAUDE.md, .mcp.json, and .claude/settings.json (hooks + MCP source of truth).
	if _, err := skillsfactory.InstallWithOptions(skillsfactory.ClientClaudeCode, dir, cfg.GovernanceURL, skillsfactory.InstallOptions{Force: force}); err != nil {
		return err
	}
	settingsPath, err := resolveClaudeSettingsPath(settingsOverride)
	if err != nil {
		return err
	}
	// Back up FIRST and abort on any backup error before touching the settings file.
	backup, err := backupFile(settingsPath)
	if err != nil {
		return fmt.Errorf("could not back up %s: %w; aborting", settingsPath, err)
	}
	// Only the runtime token is ever written as a credential — no provider keys.
	overlay := map[string]any{
		"env": map[string]any{
			"ANTHROPIC_BASE_URL":   cfg.ModelGatewayURL,
			"ANTHROPIC_AUTH_TOKEN": cred.RuntimeToken,
		},
		"mcpServers": map[string]any{
			"ai-orch-gateway": map[string]any{
				"command": "ai-orch",
				"args":    []any{"mcp", "start", "--transport", "stdio"},
				"env": map[string]any{
					"AI_ORCH_GOVERNANCE_URL": cfg.GovernanceURL,
				},
			},
		},
		"hooks": map[string]any{
			"UserPromptSubmit": []any{map[string]any{"hooks": []any{map[string]any{"type": "command", "command": "ai-orch hook prompt-submit"}}}},
			"PostToolUse":      []any{map[string]any{"hooks": []any{map[string]any{"type": "command", "command": "ai-orch hook post-tool"}}}},
			"Stop":             []any{map[string]any{"hooks": []any{map[string]any{"type": "command", "command": "ai-orch hook stop"}}}},
		},
	}
	if err := mergeJSONSettings(settingsPath, overlay); err != nil {
		return err
	}
	fmt.Printf("Claude Code enrolled for %s; runtime credential expires %s\n", cred.ActorSubject, cred.ExpiresAt.Format(time.RFC3339))
	if backup != "" {
		fmt.Printf("Backed up previous Claude Code settings to %s\n", backup)
	}
	return nil
}

func enrollKiro(ctx context.Context, cfg Config, dir string, force bool) error {
	cred, err := requestDeveloperRuntimeCredential(ctx, cfg, "kiro")
	if err != nil {
		return err
	}
	// Generate .kiro/settings/mcp.json, steering, and lifecycle hooks.
	if _, err := skillsfactory.InstallWithOptions(skillsfactory.ClientKiro, dir, cfg.GovernanceURL, skillsfactory.InstallOptions{Force: force}); err != nil {
		return err
	}
	// Wire the runtime token into the generated Kiro MCP env. Kiro routes
	// governance/MCP only — no ANTHROPIC_BASE_URL and no model endpoint override.
	mcpPath := filepath.Join(dir, ".kiro", "settings", "mcp.json")
	overlay := map[string]any{
		"mcpServers": map[string]any{
			"ai-orch-gateway": map[string]any{
				"env": map[string]any{
					"AI_ORCH_DEV_TOKEN": cred.RuntimeToken,
				},
			},
		},
	}
	if err := mergeJSONSettings(mcpPath, overlay); err != nil {
		return err
	}
	fmt.Printf("Kiro enrolled for %s; runtime credential expires %s\n", cred.ActorSubject, cred.ExpiresAt.Format(time.RFC3339))
	return nil
}

type developerRuntimeCredentialResponse struct {
	ActorSubject  string    `json:"actor_subject"`
	RuntimeToken  string    `json:"runtime_token"`
	CredentialID  string    `json:"credential_id"`
	ExpiresAt     time.Time `json:"expires_at"`
	ExpiresInDays int       `json:"expires_in_days"`
}

func requestDeveloperRuntimeCredential(ctx context.Context, cfg Config, client string) (developerRuntimeCredentialResponse, error) {
	host, _ := os.Hostname()
	body, _ := json.Marshal(map[string]string{"client": client, "device_name": host})
	resp, err := doPost(ctx, cfg, cfg.GovernanceURL+"/v1/developer/runtime-credential", body)
	if err != nil {
		return developerRuntimeCredentialResponse{}, err
	}
	var parsed developerRuntimeCredentialResponse
	if err := json.Unmarshal([]byte(resp), &parsed); err != nil {
		return developerRuntimeCredentialResponse{}, fmt.Errorf("parse runtime credential response: %w", err)
	}
	if parsed.RuntimeToken == "" || parsed.ActorSubject == "" {
		return developerRuntimeCredentialResponse{}, fmt.Errorf("runtime credential response missing token or actor: %s", resp)
	}
	return parsed, nil
}
