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

// Install generates configuration for a specific client.
func Install(client ClientType, dir string, gatewayURL string) (*InstallResult, error) {
	return InstallWithOptions(client, dir, gatewayURL, InstallOptions{})
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

	result.Instructions = `Claude Code configuration installed.

Next steps:
1. Make sure AI_ORCH_DEV_TOKEN is exported in the environment that launches Claude Code.
2. Restart Claude Code to pick up the MCP server.
3. Verify the gateway appears in /mcp tools.
4. Use start_governed_session before agentic work.
5. Delegate substantial work through delegate_governed_work.

CLAUDE.md provides governance context. .mcp.json registers the stdio gateway.`

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

// GenerateAGENTSMarkdown creates the AGENTS.md content.
func GenerateAGENTSMarkdown(gatewayURL string) string {
	return generateAGENTSMarkdown(gatewayURL)
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
		"2. **Attach context** (use-case, workflow, repo, branch, intent) when available.\n"+
		"3. **Delegate substantial work** through `delegate_governed_work`.\n"+
		"   Do not call provider models directly for governed work.\n"+
		"4. **Submit patch proposals** through the gateway, not via direct file writes.\n"+
		"5. **Record patch decisions** with `record_patch_decision`.\n"+
		"6. **Lookup audit** with `lookup_audit` to verify governance metadata.\n\n"+
		"## Trust Levels\n\n"+
		"- `gateway_enforced`: work that routed through the MCP Gateway and was evaluated by the Governance Shell.\n"+
		"- `self_reported`: work that the agent reports natively but did not route through the gateway.\n\n"+
		"Always prefer gateway_enforced paths for file-changing, model-calling and tool-calling work.\n\n"+
		"## Security\n\n"+
		"- Do not paste raw secrets, API keys, or credentials into prompts.\n"+
		"- Do not call provider models directly for governed work.\n"+
		"- Do not treat self-reported audit records as equivalent to gateway-enforced records.\n\n"+
		"## Tools\n\n"+
		"Available governed tools:\n\n"+
		"- `mcp_doctor` — Check gateway health and configuration.\n"+
		"- `start_governed_session` — Create a new governed session.\n"+
		"- `delegate_governed_work` — Route work to the Governance Shell model proxy.\n"+
		"- `record_patch_decision` — Submit patch decisions (applied, rejected, partially_applied).\n"+
		"- `lookup_audit` — Retrieve audit and evidence metadata.\n"+
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
	default:
		return "", fmt.Errorf("unknown client type: %s", s)
	}
}
