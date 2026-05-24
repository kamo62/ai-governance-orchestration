package orchestrator

import (
	"context"
	"fmt"
	"os"

	"ai-agent-orch/internal/catalog"
	"ai-agent-orch/internal/dispatch"
	"ai-agent-orch/internal/openrouter"
)

// Dispatcher resolves model aliases and starts runtime sessions.
type Dispatcher struct {
	catalogRoot string
	runtimes    map[string]dispatch.Runtime
}

func NewDispatcher(catalogRoot string) *Dispatcher {
	runtimes := make(map[string]dispatch.Runtime)

	// Try ACP runtime first (OpenCode).
	acp := dispatch.NewACPRuntime("")
	// We can't test StartSession here without a real model config,
	// but we register it. The dispatch will try it and fall back if it fails.
	runtimes["opencode"] = acp

	// Direct OpenRouter fallback.
	apiKey := os.Getenv("OPENROUTER_API_KEY")
	if apiKey != "" {
		client := openrouter.NewClient(openrouter.Config{
			APIKey:   apiKey,
			BaseURL:  os.Getenv("OPENROUTER_BASE_URL"),
			Referer:  os.Getenv("OPENROUTER_HTTP_REFERER"),
			AppTitle: envOrDefault("OPENROUTER_APP_TITLE", "ai-agent-orch-local"),
		})
		runtimes["direct"] = dispatch.NewDirectRuntime(client, catalogRoot)
	}

	return &Dispatcher{
		catalogRoot: catalogRoot,
		runtimes:    runtimes,
	}
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
	if modelAlias == "" {
		return nil, fmt.Errorf("agent %q has no primary model", agentName)
	}

	sessionCfg := dispatch.SessionConfig{
		SessionID:    sessionID,
		SystemPrompt: agentCfg.SystemPrompt(d.catalogRoot),
		UserPrompt:   prompt,
		ModelID:      modelAlias,
		AllowedTools: agentCfg.ToolsAllowed,
		CostCapUSD:   agentCfg.Cost.PerInvocationCapUSD,
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

func envOrDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
