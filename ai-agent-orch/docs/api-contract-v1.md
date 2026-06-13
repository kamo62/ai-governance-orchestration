# API Contract v1 (Beta)

This document freezes the **beta** integration surface for external clients. Breaking changes after `v0.12.0-beta` require a new API version.

## Authentication

| Token | Header | Used for |
| --- | --- | --- |
| Dev | `Authorization: Bearer <AI_ORCH_DEV_TOKEN>` | Sessions, runs, audit lookup, registry writes |
| Admin | `Authorization: Bearer <AI_ORCH_ADMIN_TOKEN>` | `/v1/admin/*` |
| Service | `Authorization: Bearer <AI_ORCH_SERVICE_TOKEN>` | Orchestrator internal calls |
| Runtime | `Authorization: Bearer <AI_ORCH_RUNTIME_TOKEN>` | Model gateway `/v1/*` local/shared beta fallback |
| Runtime composite | `Authorization: Bearer <AI_ORCH_RUNTIME_TOKEN>.<actor>` | Model gateway `/v1/*` for legacy clients that cannot send custom headers; the actor suffix supplies auto-session identity. Actor labels are limited to 64 chars of `[A-Za-z0-9.@_-]` |
| Developer runtime credential | `Authorization: Bearer <air_...>` | Model gateway `/v1/*` for enrolled developers; credentials are actor/client/device-hash bound, 90-day by default, revocable server-side, and stored only as hashes |

Optional OIDC: when `OIDC_ISSUER_URL` and `OIDC_CLIENT_ID` are set on the Governance Shell, dev endpoints accept validated ID tokens instead of the dev token.


## Developer onboarding and runtime credentials

| Method | Path | Purpose |
| --- | --- | --- |
| `POST` | `/v1/developer/runtime-credential` | Issue a 90-day actor-bound AI-Orch runtime credential after the current actor has a server-side Copilot enrolment. Request body accepts `client` and optional `device_name`; response returns `runtime_token`, `actor_subject`, `credential_id`, `device_name_hash`, `issued_at`, `expires_at`, and `expires_in_days`. It never returns provider tokens or raw device names. |

Normal AI-Orch-routed OpenCode setup uses `ai-orch developer enroll --client opencode`, which calls the Copilot enrolment endpoints, requests this runtime credential, installs the `provider.ai-orch` OpenCode block, and can install a user-level refresh job. `ai-orch opencode refresh` repeats only the credential/model-list refresh and preserves unrelated OpenCode providers/settings.

## Governed run (primary client entry)

`POST /v1/runs`

Request (required fields):

```json
{
  "agent": "governance-lead",
  "classification": "internal",
  "prompt": "Clarify the request and route it to the right specialist",
  "permission_mode": "read_only",
  "approval_mode": "manual",
  "workspace_mode": "local"
}
```

Response includes `run_id`, `session_id`, `specialist`, `status`, `sse_url`, `gateway_token`, and router confidence metadata: `routing_confidence`, `human_confirmation_required`, and `routing_alternates`.

`gateway_token` is the per-session model gateway secret. It is returned exactly once at creation; the shell stores only its SHA-256 hash. Clients must send it as `X-AI-Orch-Session-Token` on every model gateway call for that session. `POST /v1/sessions` returns the same field. Sessions created before token binding carry no hash and remain callable with the shared runtime token alone. Server-side runtimes (the ACP lane) receive a separately minted runtime token at dispatch; the gateway accepts either secret for the session.

When `AI_ORCH_REQUIRE_WORK_ITEM=true`, the shell rejects governed runs unless the session has a work item ID and a feature branch containing that ID. Example branch: `feature/WORK-123-governance-fixes`. The work item may come from branch parsing or an explicit `work_item_id` field.

## Session lifecycle

| Method | Path | Purpose |
| --- | --- | --- |
| `GET` | `/v1/sessions` | List recent actor-scoped session summaries for UI/client audit discovery; raw prompts and prompt hashes are not returned. Summaries include `usage_summary`, optional `parent_session_id` for delegated child sessions, and optional ledger fields when audit events are available |
| `POST` | `/v1/sessions` | Create session (legacy; prefer `/v1/runs`) |
| `POST` | `/v1/sessions/{id}/messages` | Route prompt to specialist (initial `created` state only) |
| `POST` | `/v1/sessions/{id}/turns` | Same-session follow-up dispatch (`done` / `failed` / `patch_ready`) |
| `POST` | `/v1/sessions/{id}/confirm` | Confirm routed specialist |
| `GET` | `/v1/sessions/{id}/events` | SSE stream (`Accept: text/event-stream`) |
| `GET` | `/v1/sessions/{id}/patches/{patchId}` | Fetch staged patch payload |
| `POST` | `/v1/sessions/{id}/patch-decision` | Record `applied`, `partially_applied`, or `rejected` |

## Model compatibility gateway

Base URL: Governance Shell gateway listen address (default `http://127.0.0.1:18082`).

| Method | Path | Notes |
| --- | --- | --- |
| `GET` | `/v1/models` | Lists governed aliases |
| `POST` | `/v1/chat/completions` | OpenAI-compatible chat |
| `POST` | `/v1/responses` | Responses API subset |

Required headers for runtime calls:

- `Authorization: Bearer <AI_ORCH_RUNTIME_TOKEN>`
- `X-AI-Orch-Session-ID: <session_id>`
- `X-AI-Orch-Session-Token: <gateway_token>` when the session was created with token binding (any session created via `/v1/runs` or `/v1/sessions` on current builds); calls without it receive `401`

Session-less auto mode: when `X-AI-Orch-Session-ID` is absent and auto sessions are enabled, the gateway creates a governed `model-gateway` session from `X-AI-Orch-Actor-Subject` (falling back to `X-AI-Orch-Local-Identity`) plus optional `X-AI-Orch-Classification`, `X-AI-Orch-Intent` and work context headers. Auto sessions are subject to kill switches, work-item requirements, policy checks, classification ceilings, secret scanning and cost caps. The gateway echoes the new session ID and one-time session token in `X-AI-Orch-Session-ID` and `X-AI-Orch-Session-Token`; subsequent calls for that session must present the token. Model calls are rejected with `409` once a session reaches a terminal status (`done`, `failed`, `aborted`). Use `X-AI-Orch-Intent` to capture the business reason when a developer chooses a governed model-only lane instead of a routed specialist. If the gateway later observes an OpenCode-style `task` tool call for a known catalog agent, it creates a non-executable `delegated` child session linked by `parent_session_id`; the child proves delegation lineage, not local tool transcript capture.

Streaming chat calls set `stream_options.include_usage=true` upstream. When the selected backend returns a usage-only final SSE chunk, the gateway forwards the usage frame to the client and records token/cost data in audit.

## MCP tool gateway

Clients connect to the Governance Shell MCP proxy using session-bound tool authorisation. Tool catalogues are policy-filtered per session.

MCP tool `start_governed_run` mirrors `POST /v1/runs` for MCP-native clients.

## System and audit

| Method | Path | Purpose |
| --- | --- | --- |
| `GET` | `/healthz` | Liveness |
| `GET` | `/readyz` | Readiness (includes catalog validation) |
| `GET` | `/v1/system/status` | Version, backend, gateway posture |
| `GET` | `/v1/backends` | Current backend, available backend options and compose commands |
| `POST` | `/v1/backends` | Admin-only backend start/stop when backend control is enabled |
| `GET` | `/v1/admin/providers/status` | Admin-only provider readiness summary for OpenRouter, Azure AI Foundry, Bedrock, OpenAI, Anthropic, DeepSeek and Copilot enrolments. Returns configured/missing state only; no secret values. |
| `POST` | `/v1/copilot/login/start` | Start GitHub Copilot device auth for the current actor; returns `login_id`, `user_code`, `verification_uri`, `expires_in`, `interval` |
| `GET` | `/v1/copilot/login/{id}` | Poll Copilot device auth status; returns `done`, `error`, `github_login`. Login state is actor-scoped and pruned after the device window |
| `GET` | `/v1/copilot/status` | Copilot token status for current actor: `configured`, `github_login`, `token_fingerprint`, `refresh_configured`, `access_expires_at` (zero time means GitHub reported no expiry) |
| `GET` | `/v1/copilot/models` | Live Copilot models for current actor (proxied with the actor's enrolled credential) |
| `POST` | `/v1/copilot/logout` | Remove the current actor's Copilot enrollment; subsequent `copilot-*` model calls fail `403` until re-enrollment |
| `GET` | `/v1/audit/sessions/{id}` | Session audit events (hashes only; no raw prompts) + `usage_summary`; delegation events may include `parent_session_id` |
| `GET` | `/v1/use-cases` | List registered use cases (Bridge/POC seed defaults) |
| `GET` | `/v1/workflows` | List registered workflows |
| `GET` | `/v1/mcp/catalog` | MCP tool catalog for session (requires `X-AI-Orch-Session-ID`) |
| `POST` | `/v1/mcp/{server}/tools/{tool}` | Governed MCP tool proxy (dev token + session header) |
| `GET` | `/metrics` | Local metrics snapshot |

`usage_summary` fields:

```json
{
  "total_tokens": 16,
  "prompt_tokens": 12,
  "completion_tokens": 4,
  "estimated_cost_usd": 0.00002,
  "model_proxy_calls": 1,
  "mcp_proxy_calls": 0,
  "turn_count": 0,
  "model_alias": "coding-fast",
  "model_resolved": "openrouter/x-ai/grok-build-0.1",
  "provider": "openrouter",
  "credential_source": "platform-openrouter",
  "reasoning_effort_requested": "high",
  "reasoning_effort_applied": "medium",
  "reasoning_source": "policy_clamped",
  "gateway_backend": "bifrost",
  "cost_source": "provider_reported"
}
```

`cost_source` is `provider_reported` when the provider/backend supplied cost, `pricing_table` when ai-orch estimated from stored model prices and token counts, `mixed` when a session has both, and `unavailable` when token usage exists but pricing is not available yet.

Durable audit events are stored as full JSON payloads and indexed for common reporting fields, including `trust_level`, `enforcement_mode`, `provider`, `model_alias`, `model_resolved`, `requested_model_alias`, `credential_source`, `reasoning_effort_requested`, `reasoning_effort_applied`, `reasoning_source`, `gateway_backend`, `run_id`, `work_item_id`, `patch_id` and token usage.

Runtime execution emits durable audit events for:

- `specialist.dispatch_failed` when dispatch fails closed before a real runtime starts;
- `runtime.started`;
- `runtime.acp.permission`;
- `runtime.acp.file_write`;
- `patch.proposed`;
- `runtime.done`;
- `runtime.failed`.

EchoRuntime is available only for explicit beta smoke runs with `AI_ORCH_BETA_SMOKE=true`. Normal dispatch fails closed when neither OpenCode/ACP nor a direct/provider-backed runtime is available.

Model gateway audit events include session context when available: agent, classification, run ID, permission mode, approval mode, workspace mode, work item, branch, commit SHA and actor hints. Raw prompts and raw responses remain hash-only unless a future policy explicitly enables raw storage.

## Patch envelope wire format

Runtime patch proposals use JSON (no Markdown fences):

```json
{
  "protocolVersion": 1,
  "patchId": "patch_abc",
  "sessionId": "sess_abc",
  "summary": "short summary",
  "files": [
    { "path": "example.ts", "action": "create", "newContent": "..." }
  ]
}
```

## Beta guarantees

- Fail-closed policy and auth errors return `4xx` with JSON `error` fields.
- Runtime misconfiguration does not fall through to fake execution; EchoRuntime requires explicit `AI_ORCH_BETA_SMOKE=true`.
- Router default/miss and conflicting keyword decisions are marked low confidence and require human confirmation.
- Audit events use hash chaining; raw prompts are not returned from audit lookup APIs.
- Trust labels (`gateway_enforced`, `managed_client`, `self_reported`) are reporting metadata only.

## Non-goals for beta

- Anthropic-native gateway for Claude Code (planned).
- Multi-instance audit chain replication.
- OAuth acquisition flows for user-scoped MCPs (Phase 2).
