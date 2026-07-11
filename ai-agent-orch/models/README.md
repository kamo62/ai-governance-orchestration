# Model Registry

This directory contains the centralized model alias registry for the AI Agent Orchestration System.

## Purpose

Agents reference models by stable aliases, not by concrete provider IDs. This allows model upgrades without editing every agent definition.

## File: `registry.yaml`

```yaml
models:
  - alias: coding-primary
    provider: openrouter
    model_id: anthropic/claude-opus-4.7
    description: Highest-quality coding, architecture, security, and deep refactor work.
    fallback_alias: coding-balanced

  - alias: coding-balanced
    provider: openrouter
    model_id: anthropic/claude-sonnet-4.5
    description: Default coding and agent workflow model.
    fallback_alias: coding-fast

  - alias: coding-fast
    provider: openrouter
    model_id: x-ai/grok-build-0.1
    description: Fast coding loops and patch iteration.
    fallback_alias: coding-economy

  - alias: coding-economy
    provider: openrouter
    model_id: qwen/qwen3.7-max
    description: Cheaper draft generation and routine implementation work.
    fallback_alias: router-small

  - alias: router-small
    provider: openrouter
    model_id: google/gemini-3.5-flash
    description: Routing, summarization, and classification.
    fallback_alias: null
```

The registry also contains smoke-test aliases such as `smoke-openai-gpt4o-mini`, `smoke-anthropic-haiku` for direct Anthropic Haiku validation, `smoke-deepseek-chat`, and `smoke-deepseek-v4-flash`. Capability aliases such as `coding-gpt55` can define ordered `routes`; the router chooses the first available route for the current actor, classification and policy context.

## Rules

- **Only** `models/registry.yaml` may contain concrete provider model IDs.
- All `agent.config.yaml` files must reference aliases only.
- The catalog validator rejects any agent config containing `model_id:`.
- Fallback chains allow graceful degradation when an alias is rejected or unavailable.
- Route candidates allow a stable alias to prefer actor-bound Copilot while retaining an approved Bifrost/OpenRouter fallback.
- Reasoning metadata records default and maximum effort for routes that support explicit reasoning controls.

## Adding a New Alias

1. Edit `registry.yaml`
2. Add the alias with provider, model_id, purpose, allowed classifications, fallback alias, and cost metadata
3. Run catalog validation to ensure no agents reference unknown aliases
4. Commit the change

## Provider Strategy

**Phase 1 (Current):** The Governance Shell owns the model-backend decision. Compose defaults to Bifrost OSS as the proven provider-plumbing sidecar. Per-user Copilot is available as an explicit alternate for actor-bound entitlements.

**Phase 1G.2:** The `ai-orch` model compatibility gateway exposes runtime-facing OpenAI-compatible endpoints such as `/v1/models`, `/v1/chat/completions`, `/v1/responses`, and streaming. Runtimes such as OpenCode should call governed aliases through that gateway instead of receiving provider API keys or calling Bifrost directly.

**Phase 2+:** Additional Bifrost providers, LiteLLM, direct cloud providers such as Azure OpenAI, or private gateways can be added behind the same alias system without changing agent definitions. Bifrost, Copilot and LiteLLM should remain backend adapters or compatibility references, not the authority for policy, audit, session identity, or patch decisions.

See [Governed Model Compatibility Gateway](../docs/model-compatibility-gateway.md) for the runtime-facing model API plan.

## Environment Overrides

For testing or emergency overrides:

```bash
# Override all agent model selections
export AI_ORCH_MODEL_ALIAS_OVERRIDE=coding-economy

# Override OpenRouter-specific settings
export OPENROUTER_BASE_URL=https://openrouter.ai/api/v1
export OPENROUTER_HTTP_REFERER=https://localhost
export OPENROUTER_APP_TITLE=ai-agent-orch-local

# Select model backend
export AI_ORCH_MODEL_BACKEND=bifrost
export AI_ORCH_BIFROST_BASE_URL=http://bifrost:8080
# Use AI_ORCH_MODEL_BACKEND=copilot-user only after per-user Copilot enrollment.

# Optional: keep Claude aliases stable while selecting their server-side route.
export AI_ORCH_CLAUDE_BACKEND=anthropic # anthropic, bedrock, or foundry
# Foundry Claude is config-only until the Foundry Anthropic adapter is added.

# Request reasoning effort through the runtime request or OpenCode agent config.
# ai-orch normalizes reasoningEffort/reasoning_effort/reasoning.effort and
# forwards Bifrost-compatible reasoning.effort when the selected route supports it.
```
