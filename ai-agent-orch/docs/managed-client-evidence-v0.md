# Managed Client Evidence v0

AI-Orch receives transcript-level evidence from managed clients that do not send model traffic through the model gateway, such as the T3 Code fork using Copilot SDK traffic. This lane is receiving-side only: clients keep local repo access and report evidence over HTTP.

## Endpoint

`POST /v1/managed-client/evidence`

Authentication uses the actor-bound developer runtime credential issued by `/v1/developer/runtime-credential`:

```http
Authorization: Bearer air_...
Content-Type: application/json
```

The shared local dev token is not accepted on this endpoint. The server derives `actor` from the runtime credential and ignores payload identity claims by rejecting fields outside this schema.

## Batch Envelope

```json
{
  "events": [
    {
      "event_id": "evt_t3_01HF...",
      "schema_version": "v0",
      "client": "t3code",
      "client_session_id": "t3-session-123",
      "event_type": "session_start",
      "repo": {
        "remote": "https://github.com/acme/app.git",
        "branch": "feature/ABC-123-managed-client",
        "commit": "abc123"
      },
      "timestamp": "2026-07-02T10:00:00Z"
    }
  ]
}
```

Rules:

- `events` is required and capped at 50 events per request.
- `event_id` is a client-generated idempotency key within the governed session; replays are counted as duplicates and not appended again. AI-Orch generates a separate audit `event_id`, retains this value as `client_event_id` in the audit event payload, and stores the durable mapping in its governance receipt table.
- `schema_version` must be `v0`.
- `client`, `client_session_id`, `event_type`, and `timestamp` are required.
- Repo context is client-reported. AI-Orch strips credentials from `repo.remote` before storing it.
- Stored audit events are tagged `trust_level: managed_client`, `enforcement_mode: advisory`, and `correlation_subject: managed_client`.

## Event Types

`session_start`

Creates or attaches the governed session for the `(actor, client_session_id)` pair.

`session_end`

Appends a session-end evidence event and marks the governed session `done`.

`prompt`

```json
{
  "event_type": "prompt",
  "content": "user prompt text"
}
```

AI-Orch stores only `prompt_sha256` by default. Clients may send `content_sha256` instead of `content`.

`assistant_message`

```json
{
  "event_type": "assistant_message",
  "content": "assistant response text"
}
```

AI-Orch stores only `response_sha256` by default. Clients may send `content_sha256` instead of `content`.

`tool_execution`

```json
{
  "event_type": "tool_execution",
  "tool": {
    "name": "Read",
    "started_at": "2026-07-02T10:01:30Z",
    "ended_at": "2026-07-02T10:01:31Z",
    "status": "ok"
  }
}
```

`permission_decision`

```json
{
  "event_type": "permission_decision",
  "permission_decision": {
    "tool": "Bash",
    "decision": "denied",
    "decider": "auto_policy",
    "reason": "blocked command"
  }
}
```

`tool` or `command` is required. `decision` must be `approved` or `denied`. `decider` must be `user` or `auto_policy`. `reason` is optional.

`file_change`

```json
{
  "event_type": "file_change",
  "file_change": {
    "paths": ["internal/billing.go"],
    "diff_sha256": "sha256:...",
    "diff": "optional diff text"
  }
}
```

AI-Orch stores a diff hash and file count. If `diff_sha256` is omitted, the server hashes `diff`.

`token_usage`

```json
{
  "event_type": "token_usage",
  "token_usage": {
    "model": "gpt-5.5",
    "input_tokens": 1234,
    "output_tokens": 567,
    "source": "client_reported"
  }
}
```

Token usage participates in session usage rollups with `cost_source: client_reported`; AI-Orch does not price these events as gateway-observed provider usage.
