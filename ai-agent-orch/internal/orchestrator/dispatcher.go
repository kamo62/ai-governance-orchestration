package orchestrator

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"ai-agent-orch/internal/catalog"
	"ai-agent-orch/internal/dispatch"
	"ai-agent-orch/internal/openrouter"
)

// Dispatcher resolves model aliases and starts runtime sessions.
type Dispatcher struct {
	catalogRoot   string
	runtimes      map[string]dispatch.Runtime
	broker        *dispatch.ToolBroker
	toolBrokerErr error
}

func NewDispatcher(catalogRoot string) *Dispatcher {
	runtimes := make(map[string]dispatch.Runtime)

	// Try ACP runtime first (OpenCode).
	acp := dispatch.NewACPRuntime("")
	// We can't test StartSession here without a real model config,
	// but we register it. The dispatch will try it and fall back if it fails.
	runtimes["opencode"] = acp

	// Model calls should normally flow through the Governance Shell model proxy,
	// so runtime-facing services never need provider API keys.
	if proxyURL := os.Getenv("AI_ORCH_MODEL_PROXY_URL"); proxyURL != "" {
		runtimes["direct"] = dispatch.NewDirectRuntime(openrouter.NewProxyClient(openrouter.ProxyConfig{
			BaseURL:      proxyURL,
			ServiceToken: os.Getenv("AI_ORCH_SERVICE_TOKEN"),
		}), catalogRoot)
	} else if apiKey := os.Getenv("OPENROUTER_API_KEY"); apiKey != "" {
		runtimes["direct"] = dispatch.NewDirectRuntime(openrouter.NewClient(openrouter.Config{
			APIKey:   apiKey,
			BaseURL:  os.Getenv("OPENROUTER_BASE_URL"),
			Referer:  os.Getenv("OPENROUTER_HTTP_REFERER"),
			AppTitle: envOrDefault("OPENROUTER_APP_TITLE", "ai-agent-orch-local"),
		}), catalogRoot)
	}

	d := &Dispatcher{
		catalogRoot: catalogRoot,
		runtimes:    runtimes,
	}

	if broker, err := dispatch.NewToolBroker(filepath.Join(catalogRoot, "policies", "command-allowlists.yaml")); err == nil {
		d.broker = broker
	} else {
		d.toolBrokerErr = err
	}

	return d
}

func (d *Dispatcher) Dispatch(ctx context.Context, sessionID string, agentName string, prompt string) (dispatch.SessionHandle, error) {
	report, err := catalog.Validate(d.catalogRoot)
	if err != nil {
		return nil, fmt.Errorf("validate catalog: %w", err)
	}
	if !report.HasAgent(agentName) {
		return nil, fmt.Errorf("agent %q not found in catalog", agentName)
	}

	// Load agent config to get model and runtime.
	agentCfg, err := catalog.LoadAgentConfig(d.catalogRoot, agentName)
	if err != nil {
		return nil, fmt.Errorf("load agent config for %q: %w", agentName, err)
	}

	modelAlias := agentCfg.Model.Primary
	if override := os.Getenv("AI_ORCH_MODEL_ALIAS_OVERRIDE"); override != "" {
		modelAlias = override
	}
	if modelAlias == "" {
		return nil, fmt.Errorf("agent %q has no primary model", agentName)
	}

	permissions := map[string]string{
		"network":                 agentCfg.Permissions.Network,
		"workspace_write":         agentCfg.Permissions.WorkspaceWrite,
		"outside_workspace_write": agentCfg.Permissions.OutsideWorkspaceWrite,
	}
	if err := d.validateAllowedTools(agentName, agentCfg.ToolsAllowed, permissions); err != nil {
		return nil, err
	}

	sessionCfg := dispatch.SessionConfig{
		SessionID:    sessionID,
		SystemPrompt: agentCfg.SystemPrompt(d.catalogRoot),
		UserPrompt:   prompt,
		ModelID:      modelAlias,
		AllowedTools: agentCfg.ToolsAllowed,
		CostCapUSD:   agentCfg.Cost.PerInvocationCapUSD,
		MCPEndpoints: resolveMCPEndpoints(agentCfg.MCPServers),
	}

	// Try ACP runtime first if agent specifies opencode.
	if agentCfg.Runtime == "opencode" {
		if runtime, ok := d.runtimes["opencode"]; ok {
			handle, err := runtime.StartSession(ctx, sessionCfg)
			if err == nil {
				return handle, nil
			}
			// ACP failed, log and try fallback.
			fmt.Fprintf(os.Stderr, "ACP runtime failed for %q: %v, trying fallback\n", agentName, err)
		}
	}

	// Fallback to direct OpenRouter runtime.
	if runtime, ok := d.runtimes["direct"]; ok {
		return runtime.StartSession(ctx, sessionCfg)
	}

	// Ultimate fallback: EchoRuntime requires no external APIs.
	// This allows the full chain to work in local Phase 1 without API keys.
	echo := dispatch.NewEchoRuntime()
	return echo.StartSession(ctx, sessionCfg)
}

func (d *Dispatcher) validateAllowedTools(agentName string, tools []string, permissions map[string]string) error {
	if d == nil || d.broker == nil {
		if d != nil && d.toolBrokerErr != nil {
			return fmt.Errorf("tool broker unavailable: %w", d.toolBrokerErr)
		}
		return fmt.Errorf("tool broker unavailable")
	}
	for _, tool := range tools {
		cmd, sub := dispatch.ParseToolCommand(tool)
		if err := d.broker.ValidateWithPermissions(cmd, sub, agentName, permissions); err != nil {
			return fmt.Errorf("tool broker blocked %s: %w", tool, err)
		}
	}
	return nil
}

// Default MCP endpoint URLs within the Docker Compose network.
var defaultMCPEndpoints = map[string]string{
	"repo-classification":      "http://mcp-repo-classification:8091",
	"engineering-standards-kb": "http://mcp-engineering-standards-kb:8092",
	"catalog-introspection":    "http://mcp-catalog-introspection:8093",
	"playwright-cli":           "http://mcp-playwright-cli:8094",
	"issue-tracker":            "http://mcp-issue-tracker:8095",
	"documentation":            "http://mcp-documentation:8096",
	"test-management":          "http://mcp-test-management:8097",
}

func resolveMCPEndpoints(servers []string) map[string]string {
	endpoints := make(map[string]string, len(servers))
	for _, name := range servers {
		if envURL := os.Getenv(mcpEndpointEnvKey(name)); envURL != "" {
			endpoints[name] = envURL
			continue
		}
		if url, ok := defaultMCPEndpoints[name]; ok {
			endpoints[name] = url
		}
	}
	return endpoints
}

func mcpEndpointEnvKey(name string) string {
	normalized := strings.ToUpper(strings.NewReplacer("-", "_", ".", "_").Replace(name))
	return "MCP_" + normalized + "_URL"
}

func envOrDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
