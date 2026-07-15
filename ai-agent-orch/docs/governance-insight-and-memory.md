# Governance Insight And Memory Direction

Status: partially implemented beta foundation; insight projection and recall remain future work.

Checked: 2026-06-13.

## Purpose

This document records the recommended storage and retrieval direction for governance
insight, memory, and later semantic recall.

The important boundary is this: the system should remember its governance history,
not give agents hidden cross-session memory. Memory must be a projection of governed
audit, session, registry, evidence, and cache records. It must not become another
runtime surface or a second source of truth.

## Recommendation

Keep using SQLite for the local/team beta until the system clearly outgrows it.
A later Postgres path should be available for backup/restore, reporting scale, or
organisation-wide multi-writer requirements, but it should not be added before the
beta needs it.

Build remaining capability in this order:

1. Structured governance insight with normal SQLite tables, views, and queries.
2. Keyword recall with SQLite FTS5 over approved unstructured evidence.
3. Semantic recall only after a spike proves the vector path is stable with the
   current Go driver and Docker runtime.
4. Postgres plus `pgvector`, Qdrant, or another vector service only if scale or
   deployment requirements force a service-backed data plane.

The first useful capability is not vector search. It is deterministic insight over
facts the Governance Shell already emits: policy decisions, blocked reasons, patch
decisions, model cost, tool-loop cap events, cache outcomes, evidence completeness,
workflow IDs, use-case IDs, and maturity export records.

## Current Repo Baseline

As of `v0.21.2-beta`, the repo uses:

- `modernc.org/sqlite` as the cgo-free SQLite driver.
- SQLite WAL mode for the beta audit/session database.
- Durable SQLite audit events with hash-chain state when `AI_ORCH_AUDIT_PATH` points
  at a `.db` file.
- Durable SQLite session ownership, registry records, model-pricing rows, developer
  runtime credentials, and provider-facing governance records on the beta path.
- Encrypted SQLite Copilot/OAuth token storage when a database path and encryption key
  are configured; memory fallback remains for local-only or deliberately ephemeral runs.
- In-memory buffers for prompts, patches, SSE replay, cancellation handles, and other
  process-local state that should not yet be treated as durable governance memory.

The driver choice still matters. `modernc.org/sqlite` fits the current Docker and
Go-first shape. It should remain the default unless a future semantic recall spike
proves that native extension support is worth the added build and deployment
complexity.

## SQLite Behaviour To Design Around

SQLite remains a good fit for this POC because it is embedded, transactional, local,
easy to back up, and already in the runtime.

The local limits are also clear:

- WAL mode allows readers and a writer to overlap, but there is still only one writer
  at a time.
- WAL shared-memory behaviour assumes processes accessing the database are on the same
  machine.
- Multi-instance writes need a proper durable writer boundary or a database tier.
- SQLite audit hash-chain appends now compare and update the latest per-session head in
  one store operation. Multi-instance deployments still need an explicit single-writer
  boundary, external checkpointing, or a database tier before claiming tamper-proof
  durability.

So the rule is:

SQLite is right for local and early team hardening. It is not the final answer for
multi-region, multi-writer, organisation-scale governance.

## Structured Insight First

The next reporting step is a Governance Insight Projection.

This should use normal SQLite schema, SQL queries, and existing governed data. No
embeddings, no vector store, no extra service.

Useful first outputs:

- top blocked policies by time window;
- classification blocks by agent, workflow, and repository;
- secret-scan blocks by source and pattern family;
- tool-loop cap hits by agent and tool;
- patch accepted, rejected, and partially applied rates;
- retry count by workflow and use case;
- model cost by model alias, agent, use case, and workflow;
- cache hit and miss rate plus estimated savings;
- evidence completeness by use case;
- cycle-time signal by workflow and risk level.

These should feed the maturity governance layer and later UI, not replace either.

Suggested initial endpoint:

```text
GET /v1/reporting/governance-insights
```

Suggested initial CLI:

```text
ai-orch reporting insights
```

## FTS5 Before Vector Recall

SQLite FTS5 should be the first recall feature for unstructured evidence.

Reason:

- Governance data often has exact tokens that matter: policy IDs, tool names, error
  strings, file paths, model aliases, workflow IDs, and work-item references.
- Keyword search is deterministic and easier to audit.
- It avoids embedding sensitive text before the secret-scan and retention model is
  fully settled.

FTS5 should index only approved unstructured evidence such as:

- failure notes;
- human review comments;
- runtime error text;
- policy rationale;
- incident summaries;
- patch decision rationale.

It should not index raw prompts, raw model responses, raw source files, provider keys,
or unscanned context.

## Semantic Recall Later

Semantic recall can be useful for "have we seen something like this before?", but it
must remain secondary to structured insight and FTS5.

If added, use this control model:

- memory records are derived from canonical audit/evidence records;
- each record links back to source evidence IDs;
- every record has classification, actor, repository, workflow, content hash, and
  retention metadata;
- secret scanning happens before embedding;
- query-time scope filtering is mandatory;
- deletion means rebuilding the projection from canonical records;
- recommendations go to a human gate and never auto-apply policy changes.

## Vector Options

### Preferred Spike Path: `modernc.org/sqlite/vec`

Start here because the repo already uses `modernc.org/sqlite`.

Potential advantages:

- keeps the Go runtime cgo-free;
- avoids a separate SQLite driver;
- aligns with the current Docker build;
- keeps the projection inside the same local database lifecycle.

Risks:

- the package is low-level and needs a small proof of concept before being treated as
  a product dependency;
- the repo may need a `modernc.org/sqlite` upgrade;
- vector APIs and examples should be pinned in tests before any production code uses
  them.

Decision: spike only. Do not add it to the main runtime until the spike proves insert,
query, Docker build, and rebuild behaviour.

### Alternative: upstream `asg017/sqlite-vec`

`sqlite-vec` is a small vector search SQLite extension with Go bindings and `vec0`
virtual tables. It stores and queries float, int8, and binary vectors. It is also
explicitly pre-v1, so breaking changes should be expected.

Potential advantages:

- purpose-built for SQLite vector search;
- language bindings exist;
- small extension footprint compared with a vector service.

Risks:

- it is a C extension;
- loadable extension and cgo behaviour must be proven with the current driver;
- pre-v1 status means pinning and vendor review are mandatory.

Decision: keep as the main conceptual reference. Do not adopt before the modernc path
is tested.

### Not Recommended Now: Chroma

Chroma is a capable AI data store with document storage, embeddings, vector search,
full-text/regex search, metadata filtering, and SDK/cloud paths.

It is not the right default for this POC because it introduces a separate service and
data lifecycle. That weakens the Governance Shell boundary and creates more to govern
before the POC has proven structured insight.

Decision: do not use for Phase 1 or Phase 2 local hardening.

### Not Recommended Now: Qdrant

Qdrant is a strong vector search engine with REST/gRPC APIs, HNSW-style retrieval,
hybrid features, and a built-in UI.

It is too much infrastructure for the current problem. It brings a separate storage
engine, API surface, and operational lifecycle. Reach for it only if semantic recall
becomes large, latency-sensitive, and clearly beyond embedded SQLite.

Decision: not now. Reconsider only for a future dedicated retrieval tier.

### Later Team Option: Postgres Plus `pgvector`

`pgvector` is the best option if the governance store moves to Postgres for team use.
It keeps vector search with relational data and supports exact and approximate nearest
neighbour search.

This becomes attractive when:

- the registry and audit stores move to Postgres;
- multi-user deployment needs Postgres-style durability and operations;
- vector search should live beside durable governance records;
- team reporting needs normal SQL plus vector recall.

Decision: keep as the Phase 2 or Phase 3 durable-store candidate, not the local POC
default.

## Embedding Runtime

Do not add an embedding dependency yet.

If semantic recall is approved later, test `hugot` first because it runs ONNX
transformer pipelines in Go and includes feature-extraction support. Its pure-Go
backend is attractive for small local models, but the spike must measure:

- Docker image size;
- cold start time;
- model file packaging;
- CPU latency;
- memory use;
- behaviour on Apple Silicon and Linux containers;
- whether model downloads are disabled in normal runtime paths.

Candidate model dimensions should be locked before any vector table is created. A
model change means rebuilding the entire vector projection.

## Proposed Remaining Phases

### Implemented Beta Foundation

Already present in `v0.21.2-beta`:

- SQLite-backed audit, sessions, registry records, model-pricing rows, and developer
  runtime credential hashes.
- Chain-aware audit append and retention on the SQLite path.
- Reporting-friendly records for model routing, token usage, cost source, provider
  readiness, evidence, benchmark runs, and maturity exports.
- A Governance UI that can inspect system posture, providers, sessions, audit events,
  evidence, costs, and workflow records.

### Phase 1G.x: Governance Insight Projection

Deliverables:

- SQLite-backed insight schema or views.
- Insight query service inside the Governance Shell.
- `GET /v1/reporting/governance-insights`.
- `ai-orch reporting insights`.
- Tests for policy, cost, patch-decision, cache, and evidence rollups.

No embeddings. No vector table.

### Phase 2R: Recall Spike

Deliverables:

- FTS5 proof of concept over approved evidence text.
- `modernc.org/sqlite/vec` proof of concept in a separate package or spike branch.
- Optional comparison against upstream `sqlite-vec`.
- No runtime adoption unless Docker, tests, and rebuild behaviour are proven.

### Phase 2S: Governed Recall

Deliverables:

- Memory projection tables.
- FTS5 recall endpoint behind auth and scope filters.
- Optional vector recall if the spike passed.
- Rebuild command for retention/erasure.
- Audit events for memory writes, rebuilds, and recall queries.

### Phase 3: Recommendations To Human Gate

Deliverables:

- Improvement recommendation records.
- Evidence-linked recommendation generation.
- Human approval/rejection path.
- Ratchet rule: loosening controls requires explicit approval; tightening can follow a
  lighter path but still records rationale.

## Data Model Sketch

```sql
CREATE TABLE governance_insight_snapshot (
  id TEXT PRIMARY KEY,
  window_start TEXT NOT NULL,
  window_end TEXT NOT NULL,
  kind TEXT NOT NULL,
  dimensions_json TEXT NOT NULL,
  metrics_json TEXT NOT NULL,
  source_event_count INTEGER NOT NULL,
  generated_at TEXT NOT NULL
);

CREATE TABLE memory_record (
  id TEXT PRIMARY KEY,
  evidence_id TEXT NOT NULL,
  kind TEXT NOT NULL,
  classification TEXT NOT NULL,
  actor TEXT NOT NULL,
  repository TEXT,
  workflow_id TEXT,
  content TEXT NOT NULL,
  content_hash TEXT NOT NULL,
  created_at TEXT NOT NULL
);

CREATE VIRTUAL TABLE memory_fts USING fts5(
  content,
  content='memory_record',
  content_rowid='rowid'
);

CREATE TABLE improvement_recommendation (
  id TEXT PRIMARY KEY,
  signal_summary TEXT NOT NULL,
  evidence_ids_json TEXT NOT NULL,
  proposed_change TEXT NOT NULL,
  direction TEXT NOT NULL,
  status TEXT NOT NULL,
  decided_by TEXT,
  decided_at TEXT,
  rationale TEXT
);
```

Vector tables are intentionally omitted from the first schema. Add them only after the
Phase 2R spike.

## Non-Negotiables

- Memory is a projection, never source of truth.
- Runtime agents do not query memory directly.
- Raw prompts and raw source content are not indexed.
- Secret scanning happens before text enters FTS or embedding.
- Classification is enforced at write and query time.
- Actor, repository, and workflow scope are enforced at query time.
- Retention and erasure rebuild the projection.
- Self-improvement is recommendation plus human gate, not autonomous policy mutation.

## Sources Checked

- `modernc.org/sqlite` v1.50.1: https://pkg.go.dev/modernc.org/sqlite@v1.50.1
- `modernc.org/sqlite/vec` with `modernc.org/sqlite` v1.50.1: https://pkg.go.dev/modernc.org/sqlite@v1.50.1/vec
- SQLite FTS5 docs, checked 2026-06-01: https://www.sqlite.org/fts5.html
- SQLite WAL docs, checked 2026-06-01: https://www.sqlite.org/wal.html
- `sqlite-vec` v0.1.9: https://github.com/asg017/sqlite-vec/releases/tag/v0.1.9
- `viant/sqlite-vec` v0.3.0: https://github.com/viant/sqlite-vec/releases/tag/v0.3.0
- `hugot` v0.7.4: https://github.com/knights-analytics/hugot/releases/tag/v0.7.4
- `pgvector` v0.8.2: https://github.com/pgvector/pgvector/releases/tag/v0.8.2
- Chroma docs / release 1.5.9, checked 2026-06-01: https://github.com/chroma-core/chroma/releases/tag/1.5.9
- Qdrant v1.18.1: https://github.com/qdrant/qdrant/releases/tag/v1.18.1
