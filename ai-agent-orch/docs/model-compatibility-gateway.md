# Governed Model Compatibility Gateway

## Summary

OpenCode, Cline and similar developer tools can keep local repository access, but they still need a model API to call. If the runtime calls Bifrost, OpenRouter, OpenAI, Anthropic, Bedrock, Azure OpenAI, LiteLLM or another provider directly, the governance boundary is weakened.

The missing piece is a small model compatibility gateway owned by this system:

```text
Developer runtime model calls
  -> ai-orch Model Compatibility Gateway
  -> Governance Shell
  -> Governance Router
  -> selected model backend
  -> approved provider
```

This is separate from the MCP gateway:

```text
Developer runtime tool calls
  -> ai-orch MCP Gateway
  -> Governance Shell
  -> approved tools and upstream MCP servers
```

The two gateways solve different problems:

| Gateway | Purpose | Governance question |
| --- | --- | --- |
| Model Compatibility Gateway | Expose provider-compatible model endpoints for runtimes such as OpenCode and Cline | Which model is allowed for this work, and how should usage be audited? |
| MCP Gateway | Expose governed tools, prompts and resources | Which tools can this agent use, with which arguments, and under which approval path? |

Both must report to the Governance Shell. Neither should become the source of authority.

## Why This Exists

OpenCode, Cline and similar runtimes need a model provider surface. The practical provider surface is often OpenAI-compatible:

- `/v1/models`;
- `/v1/chat/completions`;
- `/v1/responses`;
- streaming through `text/event-stream`.

OpenCode and Cline both have documented paths for custom/OpenAI-compatible model providers. That means the runtime can point at `ai-orch` as if it were a model provider, while `ai-orch` still owns the routing, policy, secrets and audit trail.

Claude Code is a related but different path. It can use MCP for tools now, and it can route Claude API traffic through `ANTHROPIC_BASE_URL`, but that requires an Anthropic-compatible ai-orch endpoint or adapter. Do not present Claude Code model routing as complete until that compatibility path is implemented and tested.

This avoids giving the worker runtime provider keys. The runtime receives only a session-scoped token for the compatibility gateway.

## Design Principle

The model compatibility gateway should be boring and narrow.

It should not try to compete with Bifrost, OpenRouter, LiteLLM or provider-native gateways. Its job is to translate common runtime-facing model APIs into governed model calls.

The authority stays here:

```text
Governance Shell
  -> policy
  -> session authority
  -> model alias resolution
  -> Governance Router decision
  -> audit
  -> cost and usage records
```

Bifrost is the default OSS provider-plumbing sidecar in the current Compose path. LiteLLM or other gateways can still be evaluated later, but the current POC keeps shared provider routing consolidated in Bifrost.

## Minimal API Surface

The first version should expose only:

```http
GET /v1/models
POST /v1/chat/completions
POST /v1/responses
```

### `GET /v1/models`

Return governed aliases, not raw provider inventory.

Example:

```json
{
  "object": "list",
  "data": [
    {
      "id": "coding-balanced",
      "object": "model",
      "owned_by": "ai-orch"
    },
    {
      "id": "coding-fast",
      "object": "model",
      "owned_by": "ai-orch"
    }
  ]
}
```

The gateway may include only aliases allowed for the current session, actor, classification and workflow.

### `POST /v1/chat/completions`

Support the OpenAI-compatible chat-completions shape first, because many runtimes already know how to use it.

Requirements:

- require a session-scoped runtime token;
- accept model aliases, not raw provider model IDs;
- reject unknown or disallowed aliases;
- route through the Governance Router;
- call the selected Governance Shell model backend;
- support `stream: true`;
- redact or hash sensitive request/response metadata in audit;
- record token usage, estimated cost, selected model and router reasons.

### `POST /v1/responses`

Support this after chat completions, because newer runtimes and model SDKs increasingly use the Responses API.

Requirements:

- support text-only response output first;
- support streaming events where feasible;
- map governed alias selection into provider calls;
- keep raw provider-specific features behind policy until they are understood;
- record response IDs, usage, selected model and router reasons.

The first implementation does not need to support every advanced Responses feature. It needs enough compatibility for a runtime to call governed model aliases without bypassing policy.

## Streaming Contract

Streaming must preserve governance metadata without leaking provider secrets.

For chat completions, the gateway can emit OpenAI-compatible streaming chunks.

For responses, the gateway should eventually emit semantic SSE events such as:

```text
response.created
response.output_text.delta
response.completed
response.failed
```

Provider-native streams may not line up perfectly with these events. The gateway should translate only the stable subset it can support honestly.

Audit should record:

- stream started;
- selected alias;
- selected provider model;
- credential source;
- requested and applied reasoning effort;
- routing reasons;
- request hash;
- final response hash;
- usage;
- stream completed or failed.

Audit should not store raw prompts, raw provider responses or provider credentials by default.

## OpenCode Configuration Shape

The OpenCode provider configuration should point at the local compatibility gateway:

```json
{
  "$schema": "https://opencode.ai/config.json",
  "provider": {
    "ai-orch": {
      "npm": "@ai-sdk/openai-compatible",
      "name": "AI Orch Governed Router",
      "options": {
        "baseURL": "http://127.0.0.1:18082/v1",
        "apiKey": "{env:AI_ORCH_RUNTIME_TOKEN}",
        "headers": {
          "X-AI-Orch-Session-ID": "{env:AI_ORCH_SESSION_ID}",
          "X-AI-Orch-Session-Token": "{env:AI_ORCH_SESSION_TOKEN}",
          "X-AI-Orch-Actor-Subject": "{env:AI_ORCH_ACTOR_SUBJECT}",
          "X-AI-Orch-Intent": "{env:AI_ORCH_INTENT}",
          "X-AI-Orch-Client": "opencode",
          "X-AI-Orch-Classification": "internal"
        }
      },
      "models": {
        "coding-gpt55": {
          "name": "Governed GPT-5.5 Capability Route"
        },
        "openrouter-openai-gpt55": {
          "name": "Governed OpenRouter OpenAI GPT-5.5"
        }
      }
    }
  },
  "enabled_providers": ["ai-orch"],
  "model": "ai-orch/coding-gpt55",
  "small_model": "ai-orch/coding-fast",
  "agent": {
    "governance-lead": {
      "mode": "primary",
      "model": "ai-orch/coding-gpt55",
      "reasoningEffort": "low",
      "permission": {
        "edit": "deny",
        "bash": "deny",
        "task": ["code-review", "unit-tests", "backend-development", "frontend-development", "security-review"]
      }
    }
  }
}
```

The runtime should never receive `OPENROUTER_API_KEY`, `ANTHROPIC_API_KEY`, AWS credentials, Bifrost tokens or any provider key.

The runtime should receive:

- `AI_ORCH_RUNTIME_TOKEN`;
- `AI_ORCH_SESSION_ID`;
- `AI_ORCH_SESSION_TOKEN`;
- `AI_ORCH_ACTOR_SUBJECT`;
- model aliases;
- MCP gateway URL;
- session ID;
- workspace path or mounted workspace;
- policy-limited tool configuration.

The equivalent Cline shape is the OpenAI-compatible provider with:

```text
Base URL: http://127.0.0.1:18082/v1
API key: AI_ORCH_RUNTIME_TOKEN
Model ID: coding-balanced
```

The equivalent Claude Code shape is future work:

```text
ANTHROPIC_BASE_URL=<ai-orch anthropic-compatible gateway>
ANTHROPIC_AUTH_TOKEN=<session or runtime token>
```

That endpoint is not the same as `/v1/chat/completions`, so it belongs in the roadmap until implemented.

## Relationship To Provider Gateways

Bifrost is useful because it already handles a large part of the provider-compatibility problem as open-source plumbing. Direct one-off provider backends were removed so the POC keeps shared provider plumbing behind one sidecar abstraction.

Possible roles:

- default local Compose backend for OpenAI-compatible provider calls through Bifrost;
- provider translation for OpenRouter, Anthropic, Bedrock, OpenAI, Vertex, Azure, Ollama/vLLM-style providers;
- streaming and retry plumbing behind the Governance Shell;
- replaceable backend adapter if another provider gateway fits better later.

Non-goal:

- replace the Governance Shell;
- let Bifrost, OpenRouter or another gateway own policy, audit, session identity, patch buffering, evidence, or model-routing decisions;
- use Bifrost governance/UI features as the ai-orch control plane in this phase;
- route runtimes directly to Bifrost, OpenRouter or a provider without the Governance Shell.

The correct relationship is:

```text
Runtime
  -> ai-orch Model Compatibility Gateway
  -> Governance Shell
  -> Bifrost or per-user Copilot
  -> approved provider
```

Bifrost remains the default OpenRouter/provider route for this POC.

## Relationship To LiteLLM

LiteLLM remains useful, but it should not become the authority for this POC.

Possible roles:

- reference implementation for OpenAI-compatible proxy behaviour;
- optional provider adapter behind the Governance Shell;
- future backend when multi-provider routing, spend controls or compatibility breadth justify it.

Non-goal:

- replace the Governance Shell with LiteLLM;
- let LiteLLM own policy, audit, session identity or patch decisions;
- route runtimes directly to LiteLLM without the Governance Shell.

The correct relationship is:

```text
Runtime
  -> ai-orch Model Compatibility Gateway
  -> Governance Shell
  -> optional Bifrost, LiteLLM or per-user Copilot backend
```

## Security Contract

The gateway must enforce:

- no provider keys in runtime environment;
- no Bifrost endpoint exposed to runtimes or host network by default;
- session-scoped runtime token required;
- model aliases only;
- classification ceilings;
- kill switch;
- cost policy if enabled;
- request and response secret scanning where practical;
- audit on allow, deny, failure and provider error;
- fail-closed behaviour when routing policy cannot be evaluated.

The runtime should not be able to change:

- selected concrete provider model;
- provider base URL;
- provider API key;
- audit destination;
- session ID;
- actor identity.

## Audit And Evidence

Every model call should emit a model decision envelope:

```yaml
event_type: model.gateway.call
session_id: sess_123
actor: developer
requested_model: coding-balanced
selected_alias: coding-primary
selected_provider: openrouter
selected_model: provider/model
router_reasons:
  - patch_generation_required
  - workflow_requires_higher_quality
request_hash: sha256:...
response_hash: sha256:...
usage:
  input_tokens: 1234
  output_tokens: 567
  reasoning_tokens: 0
cost:
  estimated_usd: 0.0123
enforcement_mode: gateway
trust_level: gateway_enforced
```

This gives reporting and maturity exports a clean record without making the audit log a raw transcript store.

## Build Order

### Phase 1G.2: Model Compatibility Gateway Contract

Deliverables:

- define selectable model backends: `bifrost` and `copilot-user`;
- define provider-sidecar health and fail-closed startup behaviour for selected backends;
- define runtime-token authentication for model calls;
- define OpenAI-compatible response shapes for `/v1/models`, `/v1/chat/completions` and `/v1/responses`;
- define streaming subset and failure semantics;
- define audit metadata envelope;
- define model-router decision schema;
- document OpenCode provider configuration.

### Phase 1G.3: Chat Completions MVP

Deliverables:

- implement `GET /v1/models`;
- implement non-streaming `POST /v1/chat/completions`;
- preserve OpenAI-compatible request fields such as `tools`, `tool_choice`, `tool_calls`, `tool_call_id`, `response_format`, multimodal content arrays and provider metadata;
- route model alias through the Governance Shell backend selector;
- rewrite only the concrete upstream `model` field before provider forwarding;
- return the governed alias in the response `model` field;
- record model decision audit event;
- prove the runtime does not receive provider keys.

### Phase 1G.4: Streaming MVP

Deliverables:

- support `stream: true` for chat completions;
- proxy OpenAI-compatible SSE chunks while rewriting only the chunk `model` field where present;
- preserve tool-call deltas, reasoning deltas, provider-specific fields and usage chunks;
- record stream start, completion and failure audit events.

### Phase 1G.5: Responses API MVP

Deliverables:

- implement non-streaming `POST /v1/responses`;
- forward raw OpenAI-compatible Responses payloads for backends that support `/v1/responses`;
- preserve `input`, `tools`, `tool_choice`, `include`, `store`, `reasoning`, `text`, `previous_response_id`, `prompt_cache_key` and provider metadata;
- support Responses streaming for raw-compatible backends;
- add response ID correlation;
- add response usage audit records.

### Phase 1G.6: Enterprise Provider Compatibility

Deliverables:

- verify OpenCode `v1.16.2` fixture payloads against the gateway contract;
- prove `OpenCode -> ai-orch -> Bifrost -> Bedrock` with tool-call payloads;
- prove `OpenCode -> ai-orch -> Bifrost or Foundry adapter -> Azure AI Foundry` with tool-call payloads;
- keep direct Anthropic, Bedrock and Foundry SDK translation behind Bifrost or a dedicated backend adapter;
- do not expose Bifrost, Bedrock, Foundry or provider credentials directly to OpenCode.

### Phase 1M Dependency: OpenCode Managed-Provider E2E

The local OpenCode E2E should not start until the compatibility gateway can prove:

- runtime token works;
- model aliases resolve through Governance Shell;
- provider key is absent from runtime env;
- model calls are audited;
- ACP runtime file changes produce durable patch evidence;
- MCP calls, when needed, use the MCP gateway route rather than the ACP session configuration.

The first E2E should run OpenCode locally against a real repo or disposable worktree while routing model calls through ai-orch. Sandbox/worktree isolation remains a later risk-based mode, not the default proof of the provider endpoint lane.

## References

- [OpenCode providers](https://opencode.ai/docs/providers/)
- [Bifrost](https://github.com/maximhq/bifrost)
- [LiteLLM Responses API](https://docs.litellm.ai/docs/response_api)
- [OpenAI streaming Responses guide](https://platform.openai.com/docs/guides/streaming-responses)
