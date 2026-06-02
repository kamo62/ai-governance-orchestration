# Changelog

## v0.5.4-alpha - 2026-06-02 (Patch)

Release impact: Patch because this fixes follow-up governance review findings and smoke-test stability without changing public API contracts.

- **Hardened Bridge configuration scope**: Governance URL, dev token, and local identity settings are now machine-scoped so workspace settings cannot redirect authenticated Bridge traffic.
- **Routed ACP MCP metadata through governance**: Dispatcher MCP endpoint resolution now prefers `AI_ORCH_MCP_PROXY_URL`, ACP server metadata includes service/session headers, and Docker Compose mounts `policies/` with the catalog/runtime volumes.
- **Preserved audit retention semantics**: Chain-wrapped file audit stores still report retention as unsupported, SQLite admin-retention tests now mirror production chain-wrapper wiring, and compare-and-append audit writes use the cached chain head before reloading session history.
- **Stabilised CLI smoke orchestration**: The default smoke prompt now uses safe synthetic content, direct-runtime prompts state the patch-envelope and no-secrets contract, and patch extraction tolerates prose before JSON plus string protocol versions.

## v0.5.3-alpha - 2026-06-01 (Patch)

Release impact: Patch because this fixes governance hardening review findings without introducing breaking API contracts.

- **Fixed ACP runtime scoping**: ACP sessions now run from the configured workspace path instead of `/tmp`, pass configured MCP endpoints into `session/new`, recognise `patchId` patch envelopes, increase scanner capacity for large JSON-RPC payloads, and unblock pending requests on cancellation or stream exit.
- **Tightened registry ownership**: context manifests now require session ownership on create/read, and cache, evidence, and maturity export list endpoints filter records to sessions owned by the authenticated actor.
- **Hardened audit-chain behaviour**: SQLite audit appends now use an atomic compare-and-append chain head, and retention now purges only complete expired session chains so retained chains still verify.
- **Reduced fail-open behaviour**: SQLite registry initialisation now fails startup instead of silently downgrading to in-memory storage, command allow-list YAML rejects unknown fields, and tool-less agents no longer fail when the tool broker is unavailable.
- **Improved TTL and migration safety**: session patch tracking and the staged patch buffer now expire individual patches, and SQLite session migration tolerates the exact duplicate-column race between concurrent processes.
- **Addressed review polish**: scoped the Bridge dev-token setting to machine storage while preferring VS Code SecretStorage, made router cache tests self-contained, fixed markdown lint/style items, and pinned research source versions.

## v0.5.2-alpha - 2026-06-01 (Patch)

Release impact: Patch because this restores and clarifies README investigation context without changing runtime behaviour or public API contracts.

- **Restored external governance references in the README**: added direct references to Microsoft Agent Governance Toolkit, GitHub's agentic workflow security architecture, and MCP control-plane thinking.
- **Clarified the current design thesis**: documented why the repo is building a governance/control plane first and keeping the agent plane deliberately thin.
- **Added open design questions**: captured current thinking around AGT adoption, runtime isolation, governance UI boundaries, session caching, maturity outputs, and cost/value modelling.

## v0.5.1-alpha - 2026-06-01 (Patch)

Release impact: Patch because this reorganises documentation without changing runtime behaviour or public API contracts.

- **Refocused the root README**: moved command-heavy local-run details out of the README and made the project aim, governance boundary, current state, and documentation map clearer.
- **Added `deployment.md`**: centralises local verification, Docker cleanup/rebuild, OpenRouter smoke tests, CLI smoke tests, VS Code Bridge setup, MCP stubs, policy toggles, and local-state notes.
- **Removed stale local scratch notes**: deleted ignored research/review/planning drafts that duplicated or contradicted tracked documentation.

## v0.5.0-alpha - 2026-06-01 (Minor)

Release impact: Minor because this adds durable SQLite registry storage, registry metrics, configurable Bridge identity settings, and safer ACP JSON-RPC handling while preserving existing API contracts.

- **Added durable registry storage**: SQLite-backed storage now persists use cases, workflows, context manifests, cache outcomes, evidence records, and maturity export records when the Governance Shell runs with a SQLite audit database.
- **Added registry metrics**: `/metrics` now includes registered use cases, workflows, manifests, cache hits/misses, evidence records, and maturity exports.
- **Hardened registry ownership and IDs**: cache outcomes and evidence records fail closed when session ownership cannot be verified, and generated record IDs are restored before persistence.
- **Hardened local identity mapping**: `X-AI-Orch-Local-Identity` can label local dev-token sessions for audit attribution, but it cannot override OIDC subjects and is restricted to a safe actor-label character set.
- **Improved ACP runtime handling**: ACP responses are routed by request ID, session updates are streamed, tool-call events are surfaced for loop-cap enforcement, and permission requests fail closed until they can pass through the governed tool broker.
- **Added Bridge configuration**: VS Code settings can now configure the Governance Shell URL, local dev token, and local identity label.
- **Added Governance Insight and Memory research doc**: documents the SQLite-first recommendation, FTS5-before-vector build order, vector-store options, and human-gated self-improvement boundary.
- **Updated README project direction**: summarises Governance Insight Projection as the next investigation area and links to the detailed doc.
- **Clarified memory boundary**: memory is a rebuildable governance projection, not agent cross-session memory or an autonomous self-modification loop.

## v0.4.0-alpha - 2026-06-01 (Minor)

Release impact: Minor because this adds tamper-evident audit hash chains, catalog validation caching, local-state TTL/eviction, command-allowlist enforcement, and full Phase 1F control-plane context foundation (use-case/workflow/context-manifest/cost-value/maturity-export/cache-outcome/evidence APIs).

- **Added tamper-evident audit hash chain**: `audit.ChainAppender` wraps any `audit.Store` and auto-computes `PrevEventHash` and `EventHash` per session. Added `audit.VerifyChain` and `audit.VerifyChainForSession` helpers that detect edited, deleted, reordered, and inserted events.
- **Added catalog validation cache to Orchestrator Router**: `cachedCatalog` uses a 30-second TTL for hits and 5-second TTL for errors, avoiding full disk re-validation on every route request. Cache invalidation is time-based; manual backdating triggers re-validation.
- **Added local-state TTL/eviction**: `SessionService` prompts, patches, and cancellations now have lazy eviction after `LocalStateTTL` (default 30 min). `PatchBuffer` has TTL eviction (default 30 min). `EventStore` already had bounded per-session history (128 events) and closed-session eviction (256 sessions).
- **Added `docs/local-state-lifecycle.md`**: Documents every in-process store, its owner, TTL/eviction policy, and durable-state promotion criteria.
- **Added `scripts/check-gofmt.sh`**: Local CI script that fails if any Go file needs `gofmt`.
- **Added command-allowlist enforcement**: `dispatch.ToolBroker` loads `policies/command-allowlists.yaml` and validates runtime tool calls. `orchestrator.Dispatcher` validates `AllowedTools` from the agent config against the broker before dispatching. Fail-closed when no policy is loaded.
- **Added Phase 1F control-plane registry APIs**:
  - `POST /v1/use-cases` and `GET /v1/use-cases/{id}` with owner, domain, expected benefit, linked work item, classification, and risk level.
  - `POST /v1/workflows` for governed workflow templates with stages.
  - `POST /v1/context-manifests` and `GET /v1/context-manifests/{id}` for bounded context briefs with full provenance metadata.
  - `GET /v1/reporting/maturity-governance` for bounded maturity export records without raw prompt/response/source/patch content.
  - `POST /v1/cache-outcomes` and `GET /v1/cache-outcomes` for session-scoped cache hit/miss records with provenance, eligibility, TTL, invalidation, and estimated savings.
  - `POST /v1/evidence` and `GET /v1/evidence` for test results, review outputs, approvals, patch decisions, and external quality-system links.
- **Extended session binding and cost/value sizing**: `CreateSessionRequest` and `SessionRecord` now carry `use_case_id`, `workflow_id`, `work_item_id`, `repo_url`, `branch`, `intent`, and cost/value fields (`story_points`, `estimated_dev_days`, `blended_day_rate_usd`, `baseline_cost_usd`, `model_cost_usd`, `tool_cost_usd`, `platform_cost_usd`, `review_cost_usd`, `verification_cost_usd`, `retry_count`). SQLite session schema migrated with `ensureSessionColumn`.
- **Added VS Code Bridge typecheck and lint verification**: `bun run typecheck` and `bun run lint` pass cleanly.
- **Added coverage hardening for dispatch and openrouter**: New tests for EchoRuntime event flow, `extractJSONObject`, `normalizePatchEnvelope` edge cases, `ProxyClient` validation, and `FirstContent` empty-choices handling.
- **Fixed wrapped SQLite audit retention**: `audit.ChainAppender` now delegates retention to SQLite so the admin retention endpoint continues to work after hash-chain wrapping.
- **Fixed local two-process audit chaining**: hash chaining refreshes the latest persisted session hash before appending so Governance Shell and Orchestrator do not create independent chains during sequential local flows.
- **Fixed SQLite session migration compatibility**: existing session rows with newly added nullable Phase 1F fields now read back as zero values instead of failing during ownership checks.
- **Fixed command allow-list enforcement**: dispatch now fails closed when the broker is unavailable and allows `write_file` only when the agent config grants `workspace_write: allow`.
- **Fixed registry route and ownership gaps**: cache-outcome and evidence endpoints are wired into the running mux and require the referenced session to belong to the authenticated requester.
- **Fixed cancellation TTL eviction**: expired process-local cancel functions are evicted before cancellation lookup.
- **Documented current user surfaces**: README now states that there is no standalone web UI yet and gives the local VS Code Bridge VSIX test path.
- **Fixed gofmt cleanliness**: All Go files now pass `gofmt -l`.

## v0.3.2-alpha - 2026-06-01 (Patch)

Release impact: Patch because this tightens governance authorization, ownership checks, concurrency safety, and performance without changing API contracts.

- **Required auth for audit-retention administration**: `POST /v1/admin/audit/retention` now uses the same authorised request path as the rest of the Governance Shell admin surface.
- **Enforced composition ownership across the lifecycle**: `GET`, `complete`, `approve`, and `advance` composition calls now require an owned durable session, not only an authenticated token.
- **Hardened composition state access**: The in-memory composition store now returns cloned snapshots and applies mutations under lock to avoid leaking mutable state across concurrent requests.
- **Added locking to the local OAuth token store** so future user-token reads and writes do not race under concurrent MCP proxy calls.
- **Removed SQLite single-connection bottleneck**: Changed `db.SetMaxOpenConns(1)` to `8` on both audit and session SQLite stores, with `MaxIdleConns(4)` and `ConnMaxLifetime(30m)`. WAL mode now actually supports concurrent reads instead of serialising everything through one connection.
- **Added SQLite `PRAGMA synchronous = NORMAL`** on both stores for better write throughput without sacrificing WAL durability.
- **Increased SQLite busy timeout** from 5s to 10s to reduce contention retries under load.
- **Switched FileStore to `sync.RWMutex`**: `EventsBySession` and `AllEvents` now use `RLock()` so concurrent audit reads don't block each other. `Append` still uses `Lock()` for writes.
- **Added HTTP server hardening without breaking long dispatches**: `ReadHeaderTimeout: 5s`, `MaxHeaderBytes: 1MiB`, and a 10-minute write timeout on both governance-shell and orchestrator. This keeps slowloris protection while allowing governed model calls and SSE dispatches to exceed short request/response timings.
- **Added request latency logging**: Middleware logs any request exceeding 500ms with method, path, and duration to help identify slow endpoints in production.
- **Added SSE per-write deadlines**: `http.NewResponseController` sets a 5-second write deadline on every SSE event and keepalive comment to prevent slow subscribers from blocking the server indefinitely.

## v0.3.1-alpha - 2026-05-25 (Patch)

Release impact: Patch because this wires previously scaffolded packages into the running system without breaking existing API contracts.

- **Wired composition into Governance Shell**: Added HTTP endpoints `POST /v1/compositions`, `GET /v1/compositions/{id}`, `POST /v1/compositions/{id}/approve`, `POST /v1/compositions/{id}/advance`, `POST /v1/compositions/{id}/complete`. Session ownership enforced on composition creation. Integration tests prove full create → complete → approve → advance → max-depth flow.
- **Wired workspace packager into CLI**: `ai-orch session create --workspace` packages the current directory and appends file contents to the prompt before sending to the Governance Shell.
- **Kept assembly-line execution as Phase 3 reference material**: the YAML parser remains available under `internal/assemblyline`, but no CLI run loop is exposed yet because sequential runtime composition still needs a proper session-state design.
- **Wired audit retention into admin API**: Added `POST /v1/admin/audit/retention` with `max_age_hours` body. Returns 501 for non-SQLite backends. Integration test verifies old events are purged while recent events survive.
- **Wired OAuth token store into MCP proxy**: Replaced `StaticUserTokenStore{}` with `OAuthTokenStoreAdapter` wrapping `internal/oauth.MemoryTokenStore`. `oauth-user` MCP registrations now look up real tokens and check expiration before forwarding.
- **Added integration tests**: `TestCompositionHandler_CreateAndFlow`, `TestCompositionHandler_RejectUnapprovedAdvance`, `TestAdminAuditHandler_RetentionNotSupportedForFileStore`, `TestAdminAuditHandler_RetentionPurgesOldEvents`.

## v0.3.0-alpha - 2026-05-25 (Minor)

Release impact: Minor because this adds linked audit envelopes, OIDC hardening, workspace context packaging, sequential composition, assembly-line YAML, OAuth scaffolding, and architecture-review promotion without breaking existing API contracts.

- Added linked audit envelope correlation via `ParentEventID` across session creation, routing, confirmation, abort, and patch-decision events.
- Added `rememberEventID` / `parentEventID` helpers to `SessionService` for audit event chaining.
- Added session state machine transitions: `created` → `awaiting_confirmation` (messages) → `confirmed` (confirm) with `CompareAndSwapStatus` guards.
- Added automatic workspace/source-context packaging via `internal/workspace.Packager` with configurable include/exclude globs, file count caps, and size limits.
- Added ACP runtime patch extraction from JSON-RPC results to emit `patch` events.
- Hardened OIDC validator with `nbf` (not before), `iat` (issued at), and `azp` (authorized party) claim checks.
- Added JWKS TTL cache (15-minute expiry) and singleflight refresh to prevent concurrent discovery storms.
- Added EC JWK parsing support for `ES256`, `ES384`, and `ES512` algorithms with curve validation.
- Added HTTP client timeouts (10s) for OIDC discovery and JWKS requests.
- Added `internal/oauth` token store scaffolding with `MemoryTokenStore`, `Token.IsExpired()`, and `TokenSource` interface for future user-scoped OAuth flows.
- Added SQLite audit retention controls: `PurgeBefore` and `RetentionPolicy` for time-based event pruning.
- Promoted `architecture-review` from `agents/temp/` to `agents/published/` with version 1.0.0 and `required_for_phase0: true`.
- Added sequential composition package (`internal/composition`) with human gates, max depth enforcement (default 2), and governed context handoff between stages.
- Added assembly-line YAML parser (`internal/assemblyline`) for ordered multi-stage agent pipelines with validation and JSON export.
- Added regression coverage for audit linking, OIDC nbf/iat/azp/EC-JWK, workspace packaging, composition flow, assembly-line validation, and OAuth token store.

## v0.2.0-alpha - 2026-05-24 (Minor)

Release impact: Minor because this adds new governed runtime boundaries for model proxying, MCP proxying, staged patch retrieval, and tool-loop control without breaking the existing local CLI flow.

- Added selectable native policy-engine wiring, with AGT reserved as a fail-closed future adapter until implemented.
- Added a Governance Shell model proxy so runtime-facing services can call OpenRouter through a service-token boundary without receiving the provider API key.
- Added model proxy audit metadata with provider, alias, resolved model, request/response hashes, proxy call IDs, and token-usage metadata.
- Added an MCP proxy stub with explicit `oauth-user` fail-closed behaviour when a user token is missing, with no platform-token fallback for that mode.
- Added a staged patch buffer so SSE patch events carry sanitized metadata while full patch content is fetched from the Governance Shell during review/apply.
- Added a consecutive tool/MCP-call cap with a default of 15 and catalog validation for per-agent `cost.consecutive_tool_call_max`.
- Updated Docker Compose so OpenRouter credentials live on the Governance Shell side of the model proxy instead of the Orchestrator container.
- Updated the VS Code Bridge to fetch buffered patch content before rendering native diffs or applying proposed changes.
- Added regression coverage for the policy engine boundary, model proxy, MCP `oauth-user` fail-closed contract, patch buffer/fetch flow, and tool-loop cap.

## v0.1.0-alpha - 2026-05-24 (Minor)

Release impact: Minor because this adds opt-in OpenRouter reasoning-effort and model-alias override support for CLI-driven orchestration tests without breaking existing defaults.

- Added a `coding-gpt55` OpenRouter model alias for local GPT-5.5 orchestration validation.
- Added DirectRuntime support for OpenRouter `reasoning.effort`, defaulting reasoning output exclusion on when enabled.
- Added model-usage runtime events with model, token, reasoning-token and cost metadata.
- Added an Orchestrator model-alias override for explicit smoke-test runs.
- Passed OpenRouter and reasoning override environment into the Docker Compose Orchestrator service.
- Added regression coverage for reasoning request payloads, reasoning usage parsing and model-alias override routing.
- Updated the README project state and local run instructions for CLI and OpenRouter smoke testing.
- Normalised DirectRuntime patch extraction for source-aware CLI runs that return common `changes`/`op` or `operation` envelopes instead of canonical `files`/`action` envelopes.
- Documented that current source-aware CLI tests require selected source excerpts in the prompt until workspace-context packaging lands.

## v0.0.13-alpha - 2026-05-24 (Patch)

Release impact: Patch because this fixes local governance, audit, catalog validation, and Compose regressions without changing the documented external API shape.

- Fixed compile regressions in Governance Shell session handling.
- Restored fail-closed behaviour when no local dev token or OIDC authorizer is configured.
- Ensured session records are persisted only after the authoritative `session.created` audit event succeeds.
- Added durable session ownership and state-machine guards for routing, confirmation, abort, and patch-decision flows.
- Hardened local SQLite session-store setup with directory creation and restricted database-file permissions.
- Fixed Orchestrator audit-store selection so `.db` audit paths use SQLite instead of appending JSONL to a database file.
- Added catalog validation for MCP registration existence and per-agent MCP allow-lists.
- Restored Docker Compose defaults so cost-cap enforcement is disabled unless explicitly enabled.
- Kept Phase 2 read-only MCP stubs behind the `phase2` Compose profile.
- Tightened local secret/build hygiene for `.env.*`, generated VSIX files, and temporary review artifacts.

## v0.0.10-alpha - 2026-05-24 (Patch)

Release impact: Patch because this fixes local auth, audit, catalog, CLI and Compose regressions without changing the documented public API shape.

- Fixed OIDC wiring so the optional OIDC authorizer is only active when OIDC is configured, preserving fail-closed dev-token behavior otherwise.
- Fixed OIDC JWT validation to verify signatures against the raw JWT signing input, avoid startup discovery fetches and avoid network fetches for malformed tokens or exact dev-token matches.
- Added optional SQLite audit storage with private database-file permissions and fail-fast startup behavior for invalid SQLite audit paths.
- Fixed the `ai-orch killswitch toggle` command so `--enable` blocks via `POST` and `--disable` unblocks via `DELETE`.
- Kept Docker Compose cost-cap enforcement disabled by default while leaving opt-in enforcement available.
- Moved Phase 2 issue-tracker, documentation and test-management MCP stubs behind the `phase2` Compose profile and standardised their registrations on the existing MCP schema.
- Added published security-review and terraform-review catalog scaffolds while rejecting duplicate agent names to avoid ambiguous specialist resolution.
- Ignored generated local Go binaries in Git and Docker build context.
- Documented the ignored `.env.dev` workflow for repeated local OpenRouter smoke testing.
- Added regression coverage for OIDC validation, SQLite audit storage, audit-store selection, duplicate agent names and kill-switch CLI toggles.

## v0.0.9-alpha - 2026-05-24 (Patch)

Release impact: Patch because this closes fail-open and false-green local workflow gaps without changing the documented API shape.

- Fixed local dev-token handling so session, audit, and admin endpoints fail closed when no token is configured.
- Fixed the CLI smoke path so runtime error events fail the smoke run and patch decisions use emitted patch IDs instead of hard-coded IDs.
- Added session-correlated EchoRuntime patch IDs and rejected patch decisions for patches that were never emitted for the session.
- Fixed Orchestrator dispatch responses so runtime wait failures and runtime error events return failure instead of `completed`.
- Hardened the VS Code Bridge by removing token fallbacks, avoiding raw prompt logs, escaping audit HTML, replacing runtime `require()` calls, and adding apply, partial, and reject patch-decision flows.
- Excluded generated Bridge output from TypeScript inputs so repeated extension compiles stay clean.
- Kept Docker Compose cost-cap enforcement disabled by default while leaving opt-in enforcement available.
- Added regression coverage for fail-closed auth, prompt handoff, runtime failures, SSE patch IDs, unknown patch decisions, and Bridge lint/typecheck safety.

## v0.0.8-alpha - 2026-05-24 (Patch)

Release impact: Patch because this tightens local governance, runtime safety, bridge hygiene, and observability without changing the documented user-facing Compose flow.

- Fixed specialist dispatch to fail closed when the execution audit event cannot be written.
- Added service-to-service bearer protection for Orchestrator endpoints and token propagation from the Governance Shell.
- Added bearer-token checks to MCP stub tool endpoints while leaving health checks available for Compose.
- Applied the ACP runtime timeout during subprocess execution and surfaced initialization/prompt send failures.
- Wired real Governance Shell request paths into `/metrics` counters and bounded completed SSE session history.
- Removed raw prompt logging from the VS Code Bridge and escaped audit JSON before rendering it in a webview.
- Tightened Docker build context ignores for bridge dependency and TypeScript output folders.

## v0.0.7-alpha - 2026-05-24 (Minor)

Release impact: Minor because this adds the local CLI, bridge scaffold, MCP stubs, dispatch endpoints, and streaming session path, while preserving existing API compatibility.

- Added local `ai-orch` CLI scaffolding for smoke tests, audit lookup, kill-switch checks, session flow commands, and agent listing.
- Added VS Code Bridge and MCP stub scaffolds for local catalogue, standards, repo classification, and Playwright surfaces.
- Added orchestrator dispatch wiring and Governance Shell session subroutes for messages, confirmation, patch decisions, and SSE events.
- Fixed SSE event replay so late subscribers can still read completed session events.
- Fixed specialist dispatch so the confirmed runtime receives the user's transient prompt without storing it in audit.
- Fixed Docker Compose service wiring so the Governance Shell calls the orchestrator service inside the compose network.
- Replaced old system wording in MCP registration defaults.
- Added bridge lint configuration and ignored local Node dependency and TypeScript build outputs.

## v0.0.6-alpha - 2026-05-23 (Patch)

Release impact: Patch because this tightens README wording, status framing, and local-run examples without changing runtime behaviour.

- Clarified that `system` is deliberate wording for the current local POC stage.
- Standardised README prose on ZA English while leaving literal command and code names unchanged.
- Added the planned CLI support surface to the local-run section.
- Removed the client-supplied estimated cost from the README curl example.

## v0.0.5-alpha - 2026-05-23 (Patch)

Release impact: Patch because this neutralises project naming and removes local source artifacts without changing public runtime behaviour.

- Renamed the Go module and runnable scaffold directory to neutral `ai-agent-orch` naming.
- Renamed the router agent area to `agents/core/router-agent`.
- Removed the local source PDF artifact from the workspace and replaced the specific ignore rule with a generic PDF ignore.
- Scrubbed organisation-specific and old internal naming from current docs, code, config, and local planning artifacts.

## v0.0.4-alpha - 2026-05-23 (Patch)

Release impact: Patch because this improves README orientation and local run documentation without changing runtime behaviour, APIs, or deployment compatibility.

- Added a project-state callout that identifies the repository as a personal-time POC in active early development.
- Lifted the Governance Shell thesis closer to the top of the README.
- Added explicit non-goals and local run instructions for the current scaffold.
- Standardised README wording around the catalogue/code naming boundary.

## v0.0.3-alpha - 2026-05-23 (Patch)

Release impact: Patch because this adds project licensing and attribution requirements without changing runtime behaviour, APIs, or deployment compatibility.

- Added Apache License 2.0 licensing.
- Added a NOTICE file requiring attribution to the project author when the work is reused, copied, modified, distributed, or built upon.
- Documented the license and attribution expectation in the README.

## v0.0.2-alpha - 2026-05-23 (Patch)

Release impact: Patch because this adds root project documentation without changing runtime behaviour, APIs, or deployment compatibility.

- Added a root README that frames the repository as a POC for a Governance Shell-centered agent orchestration system.
- Clarified that the project should borrow useful concepts from workspace-shaped coding stacks without rebuilding their runtime structure.

## v0.0.1-alpha - 2026-05-23 (Minor)

Release impact: Minor because this is the first named alpha release for the executable system scaffold, catalogue validation workflow, Docker Compose runner and OpenRouter smoke-test path. `VERSION` is now the canonical system version source.

- Added `VERSION` as the canonical system version source with `v0.0.1-alpha`.
- Added Go catalogue validation tooling for split `agent.md` and `agent.config.yaml` definitions.
- Added Governance Shell and Orchestrator HTTP scaffolds with health, readiness, and catalogue summary endpoints.
- Added local config loading for service address and catalogue root.
- Added Docker Compose local runner for Governance Shell, Orchestrator, catalogue validation and OpenRouter smoke testing.
- Added OpenRouter chat-completion smoke tooling with registry-alias model resolution.
- Added local dev-token guarded `POST /v1/sessions` session creation in the Governance Shell.
- Added append-only JSONL audit storage with raw prompt and raw response storage disabled by default.
- Added Orchestrator session intake audit events keyed by `X-AI-Orch-Session-ID` for cross-service correlation.
- Added Governance Shell policy gates for kill switch, classification ceiling and local secret-pattern blocking before dispatch.
- Added audit-fail-closed behaviour so sessions are not returned when required audit writes fail.
- Added per-session estimated cost-cap preflight with configurable `AI_ORCH_SESSION_COST_CAP_USD`.
- Added local audit lookup at `GET /v1/audit/sessions/{session_id}` for session-correlated audit metadata.
- Added Orchestrator router selection stub at `POST /v1/orchestrator/route` using the temporary agent catalogue.
- Added `router.specialist.selected` audit events tied to the Governance Shell session ID.
- Changed cost-cap enforcement to be disabled by default and enabled only when `AI_ORCH_COST_CAP_ENABLED=true`.
- Changed Git tracking so the source PDF, local `plan.md` roadmap and `implementation.md` guide stay local-only.
- Added tests for catalogue validation, config loading, and service readiness behaviour.
- Added tests for OpenRouter request generation, error handling and model alias resolution.
- Added tests for audit append behaviour, unauthorized session blocking, prompt hashing and Orchestrator session correlation.
- Added tests for classification, secret, kill switch and audit failure negative paths.
- Added tests for session cost-cap denial and session audit lookup.
- Added tests for router golden-case specialist selection and router audit events without raw prompt storage.
- Added tests proving estimated cost is recorded without enforcing a cap by default.
- Added Go module metadata for the new `ai-agent-orch` scaffold.
