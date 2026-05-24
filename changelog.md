# Changelog

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
