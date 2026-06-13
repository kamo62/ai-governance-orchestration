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

Return governed aliases, not raw provider inventory. When the active backend can list actor-bound Copilot models and the request includes actor context, the gateway also includes the enrolled actor's current Copilot picker chat models as governed `copilot-...` aliases. Hidden Copilot entries and non-chat models are filtered out.

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

For the Copilot user backend, ai-orch mirrors OpenCode's Copilot route selection at the gateway boundary. GPT-5-class non-mini Copilot models are sent upstream through Responses and translated back into chat-completion SSE for Custom/OpenAI-compatible clients. Anthropic/Claude, Gemini-style, `gpt-5-mini` and GPT-4-class Copilot models remain on chat completions. The chat-to-Responses bridge must preserve function tool definitions, assistant tool calls and tool-result turns, and it must translate Responses function-call stream events back into chat `tool_calls` chunks so agent loops keep working.

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
- sanitized model-emitted tool-call count and names when the response asks the client to run tools;
- `parent_session_id` lineage when a model-observed OpenCode `task` call creates a delegated child session for a known catalog agent;
- usage;
- stream completed or failed.

Audit should not store raw prompts, raw provider responses or provider credentials by default. The gateway can observe model-emitted function/tool calls that cross the model stream, such as OpenCode's `task` delegation request, and records only count/name metadata. When a `task` call names a known ai-orch catalog agent, the shell also creates a `delegated` child session linked to the parent by `parent_session_id`; this is lineage evidence, not a stored copy of the client-side subagent transcript. The gateway still cannot see file contents, Read/Edit/Bash output or the child subagent local loop unless that work routes through ACP, the MCP gateway or a deliberate client-event forwarding path.

## OpenCode Configuration Shape

The OpenCode provider configuration should point at the compatibility gateway. Prefer `ai-orch opencode install-config` for real developers because it imports the current actor-bound `/v1/models` list into the provider config:

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
        "task": {
          "code-review": "allow",
          "unit-tests": "allow",
          "backend-development": "allow",
          "frontend-development": "allow",
          "security-review": "allow"
        }
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
tool_call_count: 1
tool_call_names:
  - task
usage:
  input_tokens: 1234
  output_tokens: 567
  reasoning_tokens: 0
cost:
  estimated_usd: 0.0123
enforcement_mode: gateway
trust_level: gateway_enforced
```

This gives reporting and maturity exports a clean record without making the audit log a raw transcript store. For local tools, the model gateway audit proves that the model requested a tool/subagent call; it is not a substitute for ACP/MCP/client-event evidence of what the local tool actually did.

## Implemented Status And Remaining Work

### Implemented In The Current Beta

The compatibility gateway contract is now real enough for AI-Orch-routed OpenCode:

- selectable model backends include `bifrost` and `copilot-user`;
- `/v1/models`, `/v1/chat/completions`, and `/v1/responses` are exposed through the
  Governance Shell model gateway;
- streaming chat calls preserve OpenAI-compatible SSE frames, forward usage chunks where
  providers send them, and record stream completion/failure audit events;
- route-aware model listing hides aliases that the selected backend/actor cannot execute
  and appends actor-bound Copilot picker models when enrolled;
- runtime authentication supports the shared beta token, legacy composite actor tokens,
  session-bound gateway tokens, and 90-day developer runtime credentials;
- model gateway audit records sanitized model/tool-call metadata, provider/model
  attribution, token usage, cost source, work context, and delegated child-session
  lineage for OpenCode-style `task` calls;
- AI-Orch-routed OpenCode has been proven as the strongest current client lane for
  model route, stream, cost, session lifecycle, and patch/diff evidence.

This does not mean ai-orch has the full local OpenCode Task/Read/Edit/Bash transcript.
The gateway proves model routing and model-emitted tool-call intent. Full local tool
transcript evidence still needs ACP hooks, MCP-routing, or sanitized client-event
forwarding.

### Remaining Compatibility Work

Keep this work small and provider-specific:

- keep OpenCode fixture tests current as OpenCode payloads change;
- prove Bedrock and Azure AI Foundry routes with real tool-call payloads before marking
  those enterprise routes production-ready;
- add provider-specific compatibility fixtures for DeepSeek, OpenRouter, Anthropic,
  Bedrock, Foundry, and GitHub-backed routes where they differ from OpenAI-compatible
  chat/Responses;
- add an Anthropic-native adapter only when Claude Code or Anthropic-native clients need
  gateway-enforced model routing;
- keep Bifrost, Bedrock, Foundry and provider credentials behind ai-orch rather than
  exposing them directly to OpenCode.

### OpenCode E2E Boundary

The useful E2E shape is now:

```text
OpenCode local workspace
  -> ai-orch /v1 model gateway
  -> Governance Shell policy/session/audit
  -> Bifrost or actor-bound Copilot backend
  -> ledger evidence for model route, usage, cost and patch/diff lifecycle
```

Sandbox/worktree isolation remains a risk-based mode, not the default proof of the
provider endpoint lane.

## References

- [OpenCode providers](https://opencode.ai/docs/providers/)
- [Bifrost](https://github.com/maximhq/bifrost)
- [LiteLLM Responses API](https://docs.litellm.ai/docs/response_api)
- [OpenAI streaming Responses guide](https://platform.openai.com/docs/guides/streaming-responses)
