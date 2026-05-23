# Changelog

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
- Scrubbed organization-specific and old internal naming from current docs, code, config, and local planning artifacts.

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
