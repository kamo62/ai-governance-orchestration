# API Contract v1 (Beta)

This document freezes the **beta** integration surface for external clients. Breaking changes after `v0.12.0-beta` require a new API version.

## Authentication

| Token | Header | Used for |
| --- | --- | --- |
| Dev | `Authorization: Bearer <AI_ORCH_DEV_TOKEN>` | Sessions, runs, audit lookup, registry writes |
| Admin | `Authorization: Bearer <AI_ORCH_ADMIN_TOKEN>` | `/v1/admin/*` |
| Service | `Authorization: Bearer <AI_ORCH_SERVICE_TOKEN>` | Orchestrator internal calls |
| Runtime | `Authorization: Bearer <AI_ORCH_RUNTIME_TOKEN>` | Model gateway `/v1/*` |

Optional OIDC: when `OIDC_ISSUER_URL` and `OIDC_CLIENT_ID` are set on the Governance Shell, dev endpoints accept validated ID tokens instead of the dev token.

## Governed run (primary client entry)

`POST /v1/runs`

Request (required fields):

```json
{
  "agent": "unit-tests",
  "classification": "internal",
  "prompt": "Write tests for login",
  "permission_mode": "reviewed",
  "approval_mode": "manual",
  "workspace_mode": "local"
}
```

Response includes `run_id`, `session_id`, `specialist`, `status`, and `events_url`.

## Session lifecycle

| Method | Path | Purpose |
| --- | --- | --- |
| `GET` | `/v1/sessions` | List recent actor-scoped session summaries for UI/client audit discovery; raw prompts and prompt hashes are not returned. Summaries include `usage_summary` when audit events are available |
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
| `GET` | `/v1/audit/sessions/{id}` | Session audit events (hashes only; no raw prompts) + `usage_summary` |
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
  "gateway_backend": "bifrost",
  "cost_source": "provider_reported"
}
```

`cost_source` is `provider_reported` when the provider/backend supplied cost, `pricing_table` when ai-orch estimated from stored model prices and token counts, `mixed` when a session has both, and `unavailable` when token usage exists but pricing is not available yet.

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
- Audit events use hash chaining; raw prompts are not returned from audit lookup APIs.
- Trust labels (`gateway_enforced`, `managed_client`, `self_reported`) are reporting metadata only.

## Non-goals for beta

- Anthropic-native gateway for Claude Code (planned).
- Multi-instance audit chain replication.
- OAuth acquisition flows for user-scoped MCPs (Phase 2).
