package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/kamo62/ai-governance-orchestration/ai-agent-orch/internal/catalog"
	"github.com/kamo62/ai-governance-orchestration/ai-agent-orch/internal/dispatch"
	"github.com/kamo62/ai-governance-orchestration/ai-agent-orch/internal/envx"
	"github.com/kamo62/ai-governance-orchestration/ai-agent-orch/internal/openrouter"
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
			AppTitle: envx.OrDefault("OPENROUTER_APP_TITLE", "ai-agent-orch-local"),
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

func (d *Dispatcher) Dispatch(ctx context.Context, sessionID string, agentName string, prompt string, gatewayToken string) (dispatch.SessionHandle, error) {
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
		SessionID:     sessionID,
		GatewayToken:  gatewayToken,
		SystemPrompt:  agentCfg.SystemPrompt(d.catalogRoot),
		UserPrompt:    prompt,
		ModelID:       modelAlias,
		WorkspacePath: workspaceRoot(d.catalogRoot),
		AllowedTools:  agentCfg.ToolsAllowed,
		AgentName:     agentName,
		ToolBroker:    d.broker,
		Permissions:   permissions,
		CostCapUSD:    agentCfg.Cost.PerInvocationCapUSD,
		MCPEndpoints:  resolveMCPEndpoints(agentCfg.MCPServers),
	}

	// Try ACP runtime first if agent specifies opencode.
	// Beta/CI smoke uses EchoRuntime so governed runs complete without provider keys.
	if betaSmokeEnabled() {
		echo := dispatch.NewEchoRuntime()
		return echo.StartSession(ctx, sessionCfg)
	}

	var runtimeFailures []string
	if agentCfg.Runtime == "opencode" {
		if runtime, ok := d.runtimes["opencode"]; ok {
			handle, err := runtime.StartSession(ctx, sessionCfg)
			if err == nil {
				return handle, nil
			}
			runtimeFailures = append(runtimeFailures, "opencode: "+err.Error())
			// ACP failed, log and try fallback.
			fmt.Fprintf(os.Stderr, "ACP runtime failed for %q: %v, trying fallback\n", agentName, err)
		} else {
			runtimeFailures = append(runtimeFailures, "opencode: runtime not registered")
		}
	}

	// Fallback to direct OpenRouter runtime.
	if runtime, ok := d.runtimes["direct"]; ok {
		return runtime.StartSession(ctx, sessionCfg)
	}

	msg := fmt.Sprintf("no real runtime available for agent %q; configure OpenCode/ACP, AI_ORCH_MODEL_PROXY_URL, OPENROUTER_API_KEY, or set AI_ORCH_BETA_SMOKE=true for explicit EchoRuntime smoke", agentName)
	if len(runtimeFailures) > 0 {
		msg += "; attempted " + strings.Join(runtimeFailures, "; ")
	}
	return nil, errors.New(msg)
}

func (d *Dispatcher) validateAllowedTools(agentName string, tools []string, permissions map[string]string) error {
	if len(tools) == 0 {
		return nil
	}
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
	proxyBase := strings.TrimRight(os.Getenv("AI_ORCH_MCP_PROXY_URL"), "/")
	for _, name := range servers {
		if envURL := os.Getenv(mcpEndpointEnvKey(name)); envURL != "" {
			endpoints[name] = envURL
			continue
		}
		if proxyBase != "" {
			endpoints[name] = proxyBase + "/" + name
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

func betaSmokeEnabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("AI_ORCH_BETA_SMOKE"))) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func workspaceRoot(catalogRoot string) string {
	if override := os.Getenv("AI_ORCH_WORKSPACE_ROOT"); override != "" {
		return override
	}
	if catalogRoot != "" {
		return catalogRoot
	}
	if cwd, err := os.Getwd(); err == nil {
		return cwd
	}
	return "."
}
