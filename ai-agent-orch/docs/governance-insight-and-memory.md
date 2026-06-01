# Governance Insight And Memory Direction

Status: research-backed proposal, not yet implemented.

Checked: 2026-06-01.

## Purpose

This document records the recommended storage and retrieval direction for governance
insight, memory, and later semantic recall.

The important boundary is this: the system should remember its governance history,
not give agents hidden cross-session memory. Memory must be a projection of governed
audit, session, registry, evidence, and cache records. It must not become another
runtime surface or a second source of truth.

## Recommendation

Use SQLite for Phase 1G and Phase 2 local/team hardening until the system clearly
outgrows it.

Build in this order:

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

The repo currently uses:

- `modernc.org/sqlite` as the SQLite driver.
- SQLite WAL mode for audit and session stores.
- SQLite as the durable local store for audit and session ownership.
- In-memory registry records for Phase 1F use-case, workflow, context-manifest,
  cache-outcome, evidence, and maturity export APIs.

The driver choice matters. `modernc.org/sqlite` is cgo-free and fits the current
Docker and Go-first shape. It should remain the default unless a future semantic
recall spike proves that native extension support is worth the added build and
deployment complexity.

## SQLite Behaviour To Design Around

SQLite remains a good fit for this POC because it is embedded, transactional, local,
easy to back up, and already in the runtime.

The local limits are also clear:

- WAL mode allows readers and a writer to overlap, but there is still only one writer
  at a time.
- WAL shared-memory behaviour assumes processes accessing the database are on the same
  machine.
- Multi-instance writes need a proper durable writer boundary or a database tier.
- Audit hash-chain state needs an atomic latest-hash update before this becomes a
  multi-instance deployment.

So the rule is:

SQLite is right for local and early team hardening. It is not the final answer for
multi-region, multi-writer, organisation-scale governance.

## Structured Insight First

Phase 1G should add a Governance Insight Projection.

This should use normal SQLite schema and SQL queries over governed data. No embeddings,
no vector store, no extra service.

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

## Proposed Phases

### Phase 1G: Governance Insight Projection

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

- `modernc.org/sqlite`: https://pkg.go.dev/modernc.org/sqlite
- `modernc.org/sqlite/vec`: https://pkg.go.dev/modernc.org/sqlite/vec
- SQLite FTS5: https://www.sqlite.org/fts5.html
- SQLite WAL: https://www.sqlite.org/wal.html
- `sqlite-vec`: https://github.com/asg017/sqlite-vec
- `viant/sqlite-vec`: https://github.com/viant/sqlite-vec
- `hugot`: https://github.com/knights-analytics/hugot
- `pgvector`: https://github.com/pgvector/pgvector
- Chroma docs: https://docs.trychroma.com/docs/overview/introduction
- Qdrant: https://qdrant.tech/
