// Package skillsfactory generates client-specific setup files and instructions.
package skillsfactory

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ClientType identifies a supported MCP client.
type ClientType string

const (
	ClientVSCode     ClientType = "vscode"
	ClientCLine      ClientType = "cline"
	ClientClaudeCode ClientType = "claude-code"
	ClientCodex      ClientType = "codex"
	ClientKiro       ClientType = "kiro"
)

// InstallResult describes what was generated.
type InstallResult struct {
	FilesWritten []string
	Instructions string
}

// InstallOptions controls how client configuration files are generated.
type InstallOptions struct {
	Force bool
}

// InstallWithOptions generates configuration for a specific client.
func InstallWithOptions(client ClientType, dir string, gatewayURL string, opts InstallOptions) (*InstallResult, error) {
	switch client {
	case ClientVSCode:
		return installVSCode(dir, gatewayURL, opts)
	case ClientCLine:
		return installCLine(dir, gatewayURL, opts)
	case ClientClaudeCode:
		return installClaudeCode(dir, gatewayURL, opts)
	case ClientCodex:
		return installCodex(dir, gatewayURL, opts)
	case ClientKiro:
		return installKiro(dir, gatewayURL, opts)
	default:
		return nil, fmt.Errorf("unsupported client: %s", client)
	}
}

func installVSCode(dir, gatewayURL string, opts InstallOptions) (*InstallResult, error) {
	result := &InstallResult{}

	// Write .vscode/mcp.json
	vscodeDir := filepath.Join(dir, ".vscode")
	if err := os.MkdirAll(vscodeDir, 0755); err != nil {
		return nil, fmt.Errorf("mkdir .vscode: %w", err)
	}

	mcpConfig, err := marshalConfig(map[string]any{
		"servers": map[string]any{
			"ai-orch-gateway": map[string]any{
				"command": "ai-orch",
				"args":    []string{"mcp", "start", "--transport", "stdio"},
				"env": map[string]string{
					"AI_ORCH_GOVERNANCE_URL": gatewayURL,
				},
			},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("encode mcp.json: %w", err)
	}

	mcpPath := filepath.Join(vscodeDir, "mcp.json")
	if err := writeConfigFile(mcpPath, mcpConfig, opts); err != nil {
		return nil, fmt.Errorf("write mcp.json: %w", err)
	}
	result.FilesWritten = append(result.FilesWritten, mcpPath)

	result.Instructions = `VS Code configuration installed.

Next steps:
1. Install the MCP extension for VS Code if not already present.
2. Make sure AI_ORCH_DEV_TOKEN is exported in the environment that launches VS Code.
3. Reload the window (Cmd/Ctrl+Shift+P → "Developer: Reload Window").
4. Open the MCP panel and verify "ai-orch-gateway" appears.
5. Use the governed tools: start_governed_session, delegate_governed_work, lookup_audit.

Do not call provider models directly for governed work. Route all agentic engineering through the gateway.`

	return result, nil
}

func installCLine(dir, gatewayURL string, opts InstallOptions) (*InstallResult, error) {
	result := &InstallResult{}

	// Write .clinerules guidance
	rules := generateAGENTSMarkdown(gatewayURL)
	rulesPath := filepath.Join(dir, ".clinerules")
	if err := writeConfigFile(rulesPath, []byte(rules), opts); err != nil {
		return nil, fmt.Errorf("write .clinerules: %w", err)
	}
	result.FilesWritten = append(result.FilesWritten, rulesPath)

	result.Instructions = `CLine configuration installed.

Next steps:
1. Restart CLine to pick up the new rules.
2. When starting agentic work, use start_governed_session first.
3. Delegate substantial work through delegate_governed_work.
4. Record patch decisions with record_patch_decision.

The .clinerules file provides context about the governance boundary.`

	return result, nil
}

func installClaudeCode(dir, gatewayURL string, opts InstallOptions) (*InstallResult, error) {
	result := &InstallResult{}

	// Write CLAUDE.md
	claudeMD := generateAGENTSMarkdown(gatewayURL)
	claudePath := filepath.Join(dir, "CLAUDE.md")
	if err := writeConfigFile(claudePath, []byte(claudeMD), opts); err != nil {
		return nil, fmt.Errorf("write CLAUDE.md: %w", err)
	}
	result.FilesWritten = append(result.FilesWritten, claudePath)

	// Write .mcp.json for Claude Code
	mcpConfig, err := marshalConfig(map[string]any{
		"mcpServers": map[string]any{
			"ai-orch-gateway": map[string]any{
				"command": "ai-orch",
				"args":    []string{"mcp", "start", "--transport", "stdio"},
				"env": map[string]string{
					"AI_ORCH_GOVERNANCE_URL": gatewayURL,
				},
			},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("encode .mcp.json: %w", err)
	}

	mcpPath := filepath.Join(dir, ".mcp.json")
	if err := writeConfigFile(mcpPath, mcpConfig, opts); err != nil {
		return nil, fmt.Errorf("write .mcp.json: %w", err)
	}
	result.FilesWritten = append(result.FilesWritten, mcpPath)

	// Write .claude/settings.json lifecycle hooks mapping Claude Code events to
	// ai-orch hook subcommands. Mirrors the declarative Kiro hook construction.
	claudeDir := filepath.Join(dir, ".claude")
	if err := os.MkdirAll(claudeDir, 0755); err != nil {
		return nil, fmt.Errorf("mkdir .claude: %w", err)
	}

	claudeHooks := map[string][]map[string]any{
		"UserPromptSubmit": {{"hooks": []map[string]any{{"type": "command", "command": "ai-orch hook prompt-submit"}}}},
		"PostToolUse":      {{"hooks": []map[string]any{{"type": "command", "command": "ai-orch hook post-tool"}}}},
		"Stop":             {{"hooks": []map[string]any{{"type": "command", "command": "ai-orch hook stop"}}}},
	}
	settingsConfig, err := marshalConfig(map[string]any{"hooks": claudeHooks})
	if err != nil {
		return nil, fmt.Errorf("encode .claude/settings.json: %w", err)
	}

	settingsPath := filepath.Join(claudeDir, "settings.json")
	if err := writeConfigFile(settingsPath, settingsConfig, opts); err != nil {
		return nil, fmt.Errorf("write .claude/settings.json: %w", err)
	}
	result.FilesWritten = append(result.FilesWritten, settingsPath)

	result.Instructions = `Claude Code configuration installed.

Next steps:
1. Make sure AI_ORCH_DEV_TOKEN is exported in the environment that launches Claude Code.
2. Restart Claude Code to pick up the MCP server.
3. Verify the gateway appears in /mcp tools.
4. Use start_governed_session before agentic work.
5. Delegate substantial work through delegate_governed_work.

CLAUDE.md provides governance context, .mcp.json registers the stdio gateway, and .claude/settings.json wires the prompt-submit, post-tool, and stop lifecycle events to the ai-orch hook lane.`

	return result, nil
}

func installCodex(dir, gatewayURL string, opts InstallOptions) (*InstallResult, error) {
	result := &InstallResult{}

	// Write AGENTS.md
	agentsMD := generateAGENTSMarkdown(gatewayURL)
	agentsPath := filepath.Join(dir, "AGENTS.md")
	if err := writeConfigFile(agentsPath, []byte(agentsMD), opts); err != nil {
		return nil, fmt.Errorf("write AGENTS.md: %w", err)
	}
	result.FilesWritten = append(result.FilesWritten, agentsPath)

	result.Instructions = `Codex configuration installed.

Next steps:
1. AGENTS.md is now present in the repository root.
2. Codex will read AGENTS.md for governance instructions.
3. Use the MCP gateway tools for session creation and delegation.
4. Record patch decisions and audit lookups through the gateway.`

	return result, nil
}

// kiroHookConfig describes a single Kiro lifecycle hook file.
type kiroHookConfig struct {
	fileName  string
	name      string
	whenType  string
	toolTypes []string
	command   string
}

func installKiro(dir, gatewayURL string, opts InstallOptions) (*InstallResult, error) {
	result := &InstallResult{}

	settingsDir := filepath.Join(dir, ".kiro", "settings")
	steeringDir := filepath.Join(dir, ".kiro", "steering")
	hooksDir := filepath.Join(dir, ".kiro", "hooks")

	// .kiro/settings/mcp.json (same mcpServers shape Claude Code uses).
	mcpConfig, err := marshalConfig(map[string]any{
		"mcpServers": map[string]any{
			"ai-orch-gateway": map[string]any{
				"command": "ai-orch",
				"args":    []string{"mcp", "start", "--transport", "stdio"},
				"env": map[string]string{
					"AI_ORCH_GOVERNANCE_URL": gatewayURL,
				},
			},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("encode mcp.json: %w", err)
	}

	hooks := []kiroHookConfig{
		{
			fileName: "ai-orch-prompt-submit.kiro.hook.json",
			name:     "ai-orch-prompt-submit",
			whenType: "promptSubmit",
			command:  "ai-orch hook prompt-submit",
		},
		{
			fileName:  "ai-orch-post-tool.kiro.hook.json",
			name:      "ai-orch-post-tool",
			whenType:  "postToolUse",
			toolTypes: []string{"write", "edit"},
			command:   "ai-orch hook post-tool",
		},
		{
			fileName: "ai-orch-stop.kiro.hook.json",
			name:     "ai-orch-stop",
			whenType: "agentStop",
			command:  "ai-orch hook stop",
		},
	}

	// Build every target file (path + content + dir) before touching disk so that,
	// when a conflict is found without --force, no target file is written.
	type targetFile struct {
		path    string
		dir     string
		content []byte
	}
	targets := []targetFile{
		{path: filepath.Join(settingsDir, "mcp.json"), dir: settingsDir, content: mcpConfig},
		{path: filepath.Join(steeringDir, "ai-orch.md"), dir: steeringDir, content: []byte(generateAGENTSMarkdown(gatewayURL))},
	}
	for _, h := range hooks {
		when := map[string]any{"type": h.whenType}
		if len(h.toolTypes) > 0 {
			when["toolTypes"] = h.toolTypes
		}
		hookConfig, err := marshalConfig(map[string]any{
			"name":    h.name,
			"version": "1",
			"when":    when,
			"then": map[string]any{
				"type":    "runCommand",
				"command": h.command,
			},
		})
		if err != nil {
			return nil, fmt.Errorf("encode %s: %w", h.fileName, err)
		}
		targets = append(targets, targetFile{path: filepath.Join(hooksDir, h.fileName), dir: hooksDir, content: hookConfig})
	}

	// Fail closed before writing anything when a target already exists and force is unset.
	if !opts.Force {
		for _, t := range targets {
			if _, err := os.Stat(t.path); err == nil {
				return nil, fmt.Errorf("%s already exists; rerun with --force to overwrite", filepath.Base(t.path))
			} else if !os.IsNotExist(err) {
				return nil, err
			}
		}
	}

	for _, t := range targets {
		if err := os.MkdirAll(t.dir, 0755); err != nil {
			return nil, fmt.Errorf("mkdir %s: %w", t.dir, err)
		}
		if err := writeConfigFile(t.path, t.content, opts); err != nil {
			return nil, fmt.Errorf("write %s: %w", filepath.Base(t.path), err)
		}
		result.FilesWritten = append(result.FilesWritten, t.path)
	}

	result.Instructions = `Kiro configuration installed.

Next steps:
1. Make sure AI_ORCH_DEV_TOKEN is exported in the environment that launches Kiro.
2. Restart Kiro to pick up the MCP server and lifecycle hooks.
3. Verify "ai-orch-gateway" appears in the MCP panel.
4. Use start_governed_session before agentic work.
5. Delegate substantial work through delegate_governed_work.

.kiro/settings/mcp.json registers the stdio gateway, .kiro/steering/ai-orch.md provides governance context, and .kiro/hooks/*.kiro.hook.json wire the prompt-submit, post-tool, and stop lifecycle events to the ai-orch hook lane.`

	return result, nil
}

func writeConfigFile(path string, content []byte, opts InstallOptions) error {
	if !opts.Force {
		if _, err := os.Stat(path); err == nil {
			return fmt.Errorf("%s already exists; rerun with --force to overwrite", filepath.Base(path))
		} else if !os.IsNotExist(err) {
			return err
		}
	}
	return os.WriteFile(path, content, 0644)
}

func marshalConfig(value any) ([]byte, error) {
	content, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(content, '\n'), nil
}

func generateAGENTSMarkdown(gatewayURL string) string {
	return fmt.Sprintf("# AI Orchestration Governance — Agent Instructions\n\n"+
		"This repository is governed by the ai-orch Governance Shell.\n"+
		"All agentic engineering work should route through the gateway when possible.\n\n"+
		"## Gateway\n\n"+
		"- **Governance Shell URL**: %s\n"+
		"- **Preferred transport**: stdio MCP gateway launched by the client\n"+
		"- **HTTP transport**: local-only, bearer-token protected, and intended for controlled local tests\n\n"+
		"## Workflow\n\n"+
		"1. **Start a governed session** before agentic engineering work.\n"+
		"   Use the `start_governed_session` tool.\n"+
		"2. **Attach context** with `create_context_manifest`, `attach_use_case` and `attach_workflow` when available.\n"+
		"3. **Delegate substantial work** through `delegate_governed_work`.\n"+
		"   Do not call provider models directly for governed work.\n"+
		"4. **Use upstream tools** through `list_allowed_tools` and `call_governed_tool` with the governed session ID for repo classification, documentation, tests and issue tracking.\n"+
		"5. **Submit patch proposals** through the gateway, not via direct file writes.\n"+
		"6. **Record patch decisions** with `record_patch_decision`.\n"+
		"7. **Lookup audit** with `lookup_audit` to verify governance metadata.\n\n"+
		"## Trust Levels\n\n"+
		"- `gateway_enforced`: work that routed through the MCP Gateway and was evaluated by the Governance Shell.\n"+
		"- `managed_client`: work from a managed client path where setup is controlled, but enforcement still differs from gateway-routed tools.\n"+
		"- `self_reported`: work that the agent reports natively but did not route through the gateway.\n\n"+
		"Trust levels are observations for audit and reporting. They describe how the work was run; they are not permission settings.\n\n"+
		"## Security\n\n"+
		"- Do not paste raw secrets, API keys, or credentials into prompts.\n"+
		"- Do not call provider models directly for governed work.\n"+
		"- Do not treat self-reported audit records as equivalent to gateway-enforced records.\n\n"+
		"## Tools\n\n"+
		"Available governed tools:\n\n"+
		"- `mcp_doctor` — Check gateway health and configuration.\n"+
		"- `start_governed_session` — Create a new governed session.\n"+
		"- `create_context_manifest` — Create a bounded context manifest for a session.\n"+
		"- `attach_use_case` — Register a use case in the governance registry.\n"+
		"- `attach_workflow` — Register a workflow template in the governance registry.\n"+
		"- `delegate_governed_work` — Route work to the Governance Shell model proxy.\n"+
		"- `record_patch_decision` — Submit patch decisions (applied, rejected, partially_applied).\n"+
		"- `lookup_audit` — Retrieve audit and evidence metadata.\n"+
		"- `list_allowed_tools` — List upstream MCP tools available for a governed session.\n"+
		"- `call_governed_tool` — Call an upstream MCP tool through the Governance Shell for a governed session.\n"+
		"- `record_external_tool_call` — Self-report native tool calls.\n"+
		"- `record_external_model_call` — Self-report native model calls.\n",
		gatewayURL)
}

// Doctor checks the current directory for client configuration issues.
func Doctor(dir string, gatewayURL string) []string {
	var issues []string

	// Check for AGENTS.md
	if _, err := os.Stat(filepath.Join(dir, "AGENTS.md")); os.IsNotExist(err) {
		issues = append(issues, "AGENTS.md not found. Run: ai-orch mcp install --client codex")
	}

	// Check for .vscode/mcp.json
	if _, err := os.Stat(filepath.Join(dir, ".vscode", "mcp.json")); os.IsNotExist(err) {
		issues = append(issues, ".vscode/mcp.json not found. Run: ai-orch mcp install --client vscode")
	}

	// Check for .clinerules
	if _, err := os.Stat(filepath.Join(dir, ".clinerules")); os.IsNotExist(err) {
		issues = append(issues, ".clinerules not found. Run: ai-orch mcp install --client cline")
	}

	// Check for CLAUDE.md
	if _, err := os.Stat(filepath.Join(dir, "CLAUDE.md")); os.IsNotExist(err) {
		issues = append(issues, "CLAUDE.md not found. Run: ai-orch mcp install --client claude-code")
	}

	// Check for .mcp.json
	if _, err := os.Stat(filepath.Join(dir, ".mcp.json")); os.IsNotExist(err) {
		issues = append(issues, ".mcp.json not found. Run: ai-orch mcp install --client claude-code")
	}

	// Check for .kiro/settings/mcp.json
	if _, err := os.Stat(filepath.Join(dir, ".kiro", "settings", "mcp.json")); os.IsNotExist(err) {
		issues = append(issues, ".kiro/settings/mcp.json not found. Run: ai-orch mcp install --client kiro")
	}

	// Check for Kiro hook configuration under .kiro/hooks
	if hookEntries, err := os.ReadDir(filepath.Join(dir, ".kiro", "hooks")); err != nil || len(hookEntries) == 0 {
		issues = append(issues, ".kiro/hooks hook configuration not found. Run: ai-orch mcp install --client kiro")
	}

	if len(issues) == 0 {
		return []string{"All client configurations present."}
	}
	return issues
}

// ParseClientType converts a string to a validated ClientType.
func ParseClientType(s string) (ClientType, error) {
	switch strings.ToLower(s) {
	case "vscode":
		return ClientVSCode, nil
	case "cline":
		return ClientCLine, nil
	case "claude-code", "claude":
		return ClientClaudeCode, nil
	case "codex":
		return ClientCodex, nil
	case "kiro":
		return ClientKiro, nil
	default:
		return "", fmt.Errorf("unknown client type: %s", s)
	}
}
