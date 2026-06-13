# Local State Lifecycle

This document describes which beta state is durable today and which state remains
local-process-only. Every mutable store is listed with its owner, TTL/eviction policy,
and durable-state promotion criteria.

## Purpose

The current implementation supports a local/team beta with SQLite-backed governance
stores when `AI_ORCH_AUDIT_PATH` points at a `.db` file. Some runtime buffers remain
intentionally process-local. Before multi-instance or production use, each remaining
in-memory store needs explicit TTL/eviction, a durable promotion path, or a deliberate
decision that it must stay process-local.

## State Inventory

| State | Owner | Current beta policy | Durable Promotion Criteria |
|-------|-------|---------------------|---------------------------|
| Prompt cache | `SessionService.prompts` | Lazy eviction after `LocalStateTTL` (default 30 min) on read; process-local. | Move to encrypted/session-scoped store only if prompts must survive restart. |
| Patch buffer | `PatchBuffer.patches` | Lazy eviction after `defaultPatchBufferTTL` (30 min) on read; process-local. | Move to encrypted object/blob storage when full patch payloads must survive restart. |
| Patch known set | `SessionService.patches` | Lazy eviction after `LocalStateTTL` (default 30 min) on read; process-local helper state. | Merge with durable patch metadata store if patch lookup must survive restart. |
| SSE history | `EventStore.history` | Bounded to 128 events per session; closed-session eviction capped at 256 sessions. | Move to pub/sub with replay log or Redis stream for multi-instance SSE. |
| Cancellation map | `SessionService.cancels` | Lazy eviction after `LocalStateTTL` (default 30 min); entries deleted on explicit cancel. | No durability needed; cancel funcs are inherently process-local. |
| Audit-link state | `SessionService.lastEventID` | Process-local parent-event helper; durable audit events and hashes remain in the audit store. | Derive latest parent linkage from the audit store before multi-instance use. |
| Kill-switch state | `MemoryKillSwitch` / `SQLiteKillSwitch` | SQLite-backed when `AI_ORCH_AUDIT_PATH` is a `.db`; memory-only fallback otherwise. | Add propagation/watch semantics for multi-instance deployments. |
| OAuth/Copilot token store | `oauth.MemoryTokenStore` / encrypted SQLite token store | Encrypted SQLite when a database path and encryption key are configured; memory-only fallback for local ephemeral runs. | Move to a managed secrets store if organisational secret lifecycle demands it. |
| Developer runtime credentials | `DeveloperCredentialStore` | SQLite stores token hashes, actor/client/device binding, issue/expiry/revocation state; default expiry is 90 days. | Add operator rotation/runbook automation and optional external identity hooks before production. |
| Composition store | `composition.CompositionStore` | In-memory only; lost on restart. | Durable workflow store before team workflows depend on composition history. |
| Session cache | Planned | Not yet implemented. | Session-scoped cache entries scoped by actor/classification/repo/workflow. |

## TTL/Eviction Details

### Prompt Cache

- **Set:** `rememberPrompt` stores the prompt and records the current timestamp.
- **Read:** `promptForSession` triggers lazy eviction of entries older than `LocalStateTTL`.
- **Delete:** `forgetPrompt` explicitly removes the prompt and its timestamp.
- **Default TTL:** 30 minutes.

### Patch Buffer

- **Set:** `PatchBuffer.Store` buffers the full patch and records the current timestamp.
- **Read:** `PatchBuffer.Get` triggers lazy eviction of entries older than `defaultPatchBufferTTL`.
- **Default TTL:** 30 minutes.

### Patch Known Set

- **Set:** `rememberPatch` tracks the patch ID and records the current timestamp.
- **Read:** `patchKnown` triggers lazy eviction of entries older than `LocalStateTTL`.
- **Default TTL:** 30 minutes.

### SSE History

- **Bounded per session:** Maximum 128 events per open session.
- **Closed-session eviction:** Maximum 256 closed sessions retained; oldest evicted first.
- **Rationale:** SSE is a streaming transport, not a durable log.

### Cancellation Map

- **Set:** `registerCancel` stores the cancel func and records the current timestamp.
- **Delete:** `cancelExecution` explicitly removes the cancel func.
- **Eviction:** Lazy eviction after `LocalStateTTL` removes stale entries.
- **Rationale:** Cancel functions are inherently process-local and non-serializable.

## Audit Hash Chain Limitations

The audit chain is tamper-evident, not tamper-proof. On the SQLite path, audit append
uses persisted per-session chain heads so an append can compare the expected previous
hash and update the head in the same store operation. Chain-aware retention also keeps
the verification boundary explicit.

Remaining limitations:

1. JSONL and other non-compare-and-append stores are still local-process friendly, not
   a multi-instance audit writer.
2. Multi-instance production needs a single durable writer boundary, a database tier
   with the same compare-and-update semantics, or external checkpoint anchoring.
3. The chain proves event continuity from the stored data; it does not stop an operator
   with full host/database control from replacing both data and checkpoint state unless
   checkpoints are anchored outside that trust boundary.

## Configuration

- `SessionConfig.LocalStateTTL` controls prompt/patch/cancel eviction.
- `PatchBuffer.ttl` controls patch buffer eviction.
- These are intentionally local-process settings.  Do not increase them without
  considering memory growth and restart behaviour.
