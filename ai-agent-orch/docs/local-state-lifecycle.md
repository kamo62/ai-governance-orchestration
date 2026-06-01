# Local State Lifecycle

This document describes the in-process state boundaries for the Phase 1 local build.
Every mutable store is listed with its owner, TTL/eviction policy, and durable-state
promotion criteria.

## Purpose

The first implementation is intentionally local-process-first.  Before this can become
a team or multi-instance system, each in-memory store needs explicit TTL/eviction or
a documented durable-store promotion path.

## State Inventory

| State | Owner | Phase 1 Policy | Durable Promotion Criteria |
|-------|-------|--------------|---------------------------|
| Prompt cache | `SessionService.prompts` | Lazy eviction after `LocalStateTTL` (default 30 min) on read. | Move to encrypted/session-scoped store when sessions must survive restart. |
| Patch buffer | `PatchBuffer.patches` | Lazy eviction after `defaultPatchBufferTTL` (30 min) on read. | Move to encrypted object/blob storage when patches must survive restart. |
| Patch known set | `SessionService.patches` | Lazy eviction after `LocalStateTTL` (30 min) on read. | Merge with durable patch metadata store. |
| SSE history | `EventStore.history` | Bounded to 128 events per session; closed-session eviction capped at 256 sessions. | Move to pub/sub with replay log or Redis stream for multi-instance SSE. |
| Cancellation map | `SessionService.cancels` | Lazy eviction after `LocalStateTTL` (30 min); entries deleted on explicit cancel. | No durability needed; cancel funcs are inherently process-local. |
| Audit-link state | `SessionService.lastEventID` | In-memory only; reset on restart breaks `parent_event_id` linkage. | Move to audit hash-chain/store-backed state before multi-instance use. |
| Kill-switch state | `MemoryKillSwitch.state` | In-memory only; lost on restart. | Move to Redis or shared KV with pub/sub propagation before team use. |
| OAuth token store | `oauth.MemoryTokenStore.tokens` | In-memory only; tokens have `ExpiresAt` for expiration. | Replace with secure vault or encrypted DB before real `oauth-user` MCPs. |
| Composition store | `composition.CompositionStore` | In-memory only; lost on restart. | Durable workflow store (Postgres/DynamoDB) before team use. |
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

The `ChainAppender` keeps an in-memory `lastHash` cache, but it refreshes the latest
persisted session hash before every append.  That keeps the local Governance Shell and
Orchestrator processes on one sequential chain when they share the same audit store.

The remaining Phase 1 limitation is atomicity across concurrent multi-instance writes:
two separate processes could still read the same latest hash at the same time and append
parallel next events.  This is acceptable for the local POC because:

1. The audit store itself is durable (JSONL or SQLite).
2. `VerifyChain` validates the normal local sequential session flow.
3. The primary threat model is tamper detection for local development and smoke tests.

For multi-instance or long-running deployments, the promotion path is:
- Use a dedicated `audit_chain` table that stores and updates the latest hash per
  session inside the same transaction as the event append, OR
- Route all audit writes for a session through one durable audit writer.

## Configuration

- `SessionConfig.LocalStateTTL` controls prompt/patch/cancel eviction.
- `PatchBuffer.ttl` controls patch buffer eviction.
- These are intentionally local-process settings.  Do not increase them without
  considering memory growth and restart behaviour.
