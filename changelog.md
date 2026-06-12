# Changelog

## v0.20.1-beta - 2026-06-12 (Patch)

Release impact: Patch because this fixes OpenCode config installation to preserve default provider visibility without changing public APIs or model gateway contracts.

- **Preserved existing OpenCode provider visibility**: `ai-orch opencode install-config` no longer creates a new `enabled_providers` allowlist when the user's config did not already have one, so credential-backed providers such as GitHub Copilot, Moonshot AI, OpenCode Go and xAI remain visible after installing the ai-orch provider.
- **Kept explicit allowlists intentional**: configs that already define `enabled_providers` still have `ai-orch` added to that existing allowlist, preserving the user's deliberate restriction.
- **Added regression coverage for installer merges**: focused OpenCode config tests now cover the default no-allowlist shape that exposed this issue.
- **Aligned version references**: root VERSION, Go runtime version, Bridge package metadata, README, and changelog now agree on v0.20.1-beta.

## v0.20.0-beta - 2026-06-12 (Minor)

Release impact: Minor because this adds backward-compatible session-lineage fields and governed child-session markers for model-observed Task delegations without breaking existing request contracts.

- **Linked OpenCode Task delegations into the governed ledger**: when the model gateway observes an OpenCode-style `task` tool call for a known catalog agent, it now creates a `delegated` child session with `parent_session_id`, inherited work context, and a `session.delegated` audit event.
- **Kept the transcript boundary honest**: delegated child sessions prove the model-route and agent handoff, but they do not claim to store the local child agent's Read/Edit/Bash transcript; that still requires ACP, MCP or explicit client-event forwarding.
- **Exposed lineage in APIs, exports and UI**: session records, session-list JSON, audit events, admin CSV export and the local ledger UI now surface `parent_session_id` so parent model sessions and specialist child markers can be followed together.
- **Added regression coverage for task-delegation routing**: gateway tests cover streamed and non-streamed `task` tool calls, governance tests cover linked child-session creation, and session-store tests cover durable `parent_session_id` migration.
- **Aligned version references**: root VERSION, Go runtime version, Bridge package metadata, README, and changelog now agree on v0.20.0-beta.

## v0.19.0-beta - 2026-06-12 (Minor)

Release impact: Minor because this adds backward-compatible model-gateway audit metadata and visible ledger detail for model-emitted tool calls without changing existing request contracts.

- **Recorded model-emitted tool calls in gateway audit events**: streamed and non-streamed model gateway events now include sanitized `tool_call_count` and `tool_call_names` metadata when the model asks the client to run tools such as OpenCode `task`, without storing arguments, prompt text, tool output or file contents.
- **Surfaced tool-call detail in the local ledger UI**: the audit trail now shows the gateway-observed tool-call count and names, while the sessions table can use the existing aggregate count for direct OpenCode sessions.
- **Clarified the observability boundary**: docs now distinguish model-stream tool-call evidence from the local OpenCode Task/Read/Edit execution transcript, which still requires ACP, MCP or deliberate client-event forwarding.
- **Aligned version references**: root VERSION, Go runtime version, Bridge package metadata, README, and changelog now agree on v0.19.0-beta.

## v0.18.8-beta - 2026-06-12 (Patch)

Release impact: Patch because this fixes Copilot-profile route availability, OpenCode subagent prompting, and ledger cost estimates without changing public API contracts.

- **Filtered OpenCode model imports to executable routes**: `/v1/models` now lists static governed aliases only when the current gateway backend and actor can actually execute the selected route, so Copilot-only servers no longer advertise OpenRouter-only aliases such as `coding-fast`.
- **Failed closed on provider/backend mismatches**: the Copilot backend now rejects non-Copilot providers locally instead of forwarding unsupported OpenRouter or Anthropic model IDs to Copilot and surfacing confusing upstream 400s.
- **Restored governed subagent prompting**: generated OpenCode configs now ask before launching specialist subagents, bind write-capable specialists to governed ai-orch model aliases, and explicitly tell write agents to use OpenCode edit operations rather than shell `apply_patch`.
- **Estimated Copilot model cost from equivalent pricing**: session usage now preserves `copilot-user` attribution while estimating GPT-5.5 token cost from the equivalent OpenRouter pricing row when Copilot reports tokens but no USD.
- **Closed auto-created gateway sessions**: generic OpenAI-compatible client sessions created by the model gateway now move to `completed` or `failed` when the request finishes instead of accumulating as `running` in the ledger.
- **Aligned version references**: root VERSION, Go runtime version, Bridge package metadata, README, and changelog now agree on v0.18.8-beta.

## v0.18.7-beta - 2026-06-12 (Patch)

Release impact: Patch because this fixes actor-bound Copilot model discovery and OpenCode tool-call streaming without changing public API contracts.

- **Imported dynamic Copilot picker models**: /v1/models now augments governed registry aliases with the enrolled actor's live Copilot picker chat models, and ai-orch opencode install-config imports that list into OpenCode config so models such as Claude Opus and Sonnet appear when Copilot exposes them.
- **Routed dynamic Copilot aliases**: actor-bound aliases such as copilot-claude-opus-4.8 resolve to the current Copilot model catalog at request time while preserving the existing static governed aliases.
- **Restored OpenCode tool-call loops on Copilot GPT-5-class routes**: the chat-to-Responses bridge now translates Responses function-call stream events back into chat-completion tool_calls chunks with finish_reason=tool_calls, so OpenCode can execute its local read/list/grep tools instead of receiving plain text only.
- **Aligned version references**: root VERSION, Go runtime version, Bridge package metadata, README, and changelog now agree on v0.18.7-beta.

## v0.18.6-beta - 2026-06-12 (Patch)

Release impact: Patch because this fixes generated OpenCode configuration compatibility, bridges a Copilot model-endpoint mismatch, and clarifies central-server client entry paths without changing public API contracts.

- **Fixed generated OpenCode task permissions**: `governance-lead.permission.task` now uses OpenCode's accepted permission-object shape instead of a string list, and the docs examples use the same shape, so generated and manual configs validate in OpenCode 1.16.x.
- **Bridged Copilot GPT-5-class models for chat clients**: the gateway mirrors OpenCode's Copilot route rule by sending Copilot GPT-5-class non-mini models through Responses, keeping Anthropic/Claude and `gpt-5-mini` on chat completions, and translating Responses streams back into chat-completion SSE while preserving OpenAI-style function tools and tool-result turns.
- **Clarified central-server onboarding**: README and deployment docs now separate operator-owned server startup from developer-owned OpenCode/Cline configuration, including the composite runtime-key path for manual Custom/OpenAI-compatible providers.
- **Aligned version references**: root VERSION, README, and changelog now agree on v0.18.6-beta.

## v0.18.5-beta - 2026-06-12 (Patch)

Release impact: Patch because this fixes session-usage accounting, Responses stream audit labels, direct-runtime route selection, OpenCode wrapper reporting, and launcher argument forwarding without breaking public API contracts or deployment compatibility.

- **Fixed Responses stream usage rollups**: session usage now includes `model.gateway_responses_stream.completed` events so streamed Responses sessions report tokens and cost.
- **Corrected incomplete Responses auditing**: provider `response.incomplete` streams now record `model.gateway_responses_stream.incomplete` instead of looking like successful completions.
- **Fixed direct runtime route selection**: the direct runtime now chooses from effective model routes and skips actor-bound routes it cannot serve.
- **Corrected governed OpenCode launch reporting**: the wrapper records the local OpenCode lane as `self_reported`, keeps the routed specialist visible to the developer, and no longer claims a manual confirm gate that is not exercised.
- **Fixed OpenCode launcher argument forwarding**: `scripts/opencode-governed.sh` and `.ps1` now forward governance flags such as `--model-only` to `ai-orch opencode` instead of hiding them behind an extra separator; added regression coverage for the documented model-only launcher path.
- **Cleaned review-follow-up scaffolding**: test-only exports and duplicated helper loops were removed or moved to the production packages that own the behaviour.
- **Aligned version references**: root VERSION, Go runtime version, Bridge package metadata, README, and changelog now agree on v0.18.5-beta.

## v0.18.4-beta - 2026-06-11 (Patch)

Release impact: Patch because this fixes CI beta smoke port wiring without changing public contracts, model routing behaviour, or deployment compatibility.

- **Fixed GitHub Actions beta smoke readiness**: the beta smoke workflow now publishes Governance Shell on the same host port that the readiness probe checks.
- **Aligned version references**: root VERSION, Go runtime version, Bridge package metadata, README, and changelog now agree on v0.18.4-beta.

## v0.18.3-beta - 2026-06-11 (Patch)

Release impact: Patch because this fixes local hardening, module identity, and documentation drift without changing public API contracts, session payloads, model routing behaviour, or deployment compatibility.

- **Restored the canonical Go module identity**: the Go module and imports now use `github.com/kamo62/ai-governance-orchestration/ai-agent-orch`, matching the actual GitHub repository path.
- **Hardened OpenCode config installation**: project or global `opencode.json` files that store a concrete ai-orch runtime token are now written with private `0600` permissions, and local OpenCode config files are ignored by git.
- **Added regression coverage for repo identity and OpenCode file modes**: focused tests now catch module-path drift and token-bearing config files that are not private.
- **Updated stale consolidation docs**: the roadmap and MCP README now refer to `ai-orch smoke gateway|provider` and the consolidated `mcp-stub` shape instead of deleted prototype binaries.
- **Aligned version references**: root VERSION, Go runtime version, Bridge package metadata, README, and changelog now agree on v0.18.3-beta.

## v0.18.2-beta - 2026-06-11 (Patch)

Release impact: Patch because this hardens local verification and test reliability without changing public APIs, model routing, session contracts, or deployment compatibility.

- **Pinned the local Go toolchain to the patched standard library**: root Go commands now request Go 1.26.4, matching CI and Docker, so govulncheck no longer reports reachable standard-library vulnerabilities from Go 1.26.3.
- **Isolated Bridge dependencies from Go package discovery**: the VS Code Bridge now has a nested module boundary so Go package discovery, tests, and vulnerability scans do not traverse Bun-installed node_modules.
- **Added repo-health regression coverage**: a focused Go test now fails if root package discovery starts walking node_modules again.
- **Fixed stale skillsfactory tests**: updated the Doctor test call sites to match the current Doctor(dir, gatewayURL) API.
- **Aligned version references**: root VERSION, Go runtime version, Bridge package metadata, README, deployment prerequisites, and changelog now agree on v0.18.2-beta.

## v0.18.1-beta - 2026-06-11 (Patch)

Release impact: Patch because this hardens beta verification, SQLite contention handling, audit failure guidance, and removes unused prototype code without changing public APIs or model/session contracts.

- **Hardened beta smoke startup**: CI and local beta verification now rebuild the beta services, wait for Governance Shell readiness, and clean Docker volumes after smoke runs.
- **Improved SQLite local-store resilience**: Copilot, OAuth, and kill-switch SQLite stores now enable WAL, a busy timeout, and normal synchronous mode for local beta contention.
- **Added audit failure guidance**: patch-decision audit write failures now return targeted hints for locked SQLite databases, stale audit-chain volumes, and generic persistence failures.
- **Removed unused prototype code**: deleted the unused assembly-line package and the obsolete OpenCode runtime stub while keeping the active ACP/direct runtime paths.
- **Preserved router compatibility**: restored `Router.Resolve` as a compatibility helper over the current route-selection logic.
- **Aligned version references**: root `VERSION`, Go runtime version, Bridge package metadata, README, and changelog now agree on `v0.18.1-beta`.

## v0.18.0-beta - 2026-06-11 (Minor)

Release impact: Minor because this adds backwards-compatible model route metadata, reasoning-effort governance, audit fields, and OpenCode agent defaults without breaking existing session or gateway APIs.

- **Made `coding-gpt55` a capability alias**: the gateway now prefers actor-bound Copilot for enrolled users and falls back to the approved Bifrost/OpenRouter `openai/gpt-5.5` route.
- **Added provider-pinned GPT-5.5 selection**: `openrouter-openai-gpt55` gives model-only sessions an explicit Bifrost/OpenRouter path for comparison and audit clarity.
- **Governed OpenCode reasoning effort**: the model gateway accepts `reasoningEffort`, `reasoning_effort`, and `reasoning.effort`, applies route and agent policy, forwards Bifrost-compatible `reasoning.effort`, and strips unsupported reasoning controls.
- **Expanded model audit metadata**: model events now record requested alias, credential source, requested/applied reasoning effort, and reasoning source alongside existing provider, resolved model, token, cost and hash fields.
- **Tightened OpenCode agent config**: generated and sandbox configs use `governance-lead` as a low-reasoning primary agent with scoped specialist delegation rather than an unrestricted task permission.
- **Aligned version references**: root `VERSION`, Go runtime version, Bridge package metadata, README, and changelog now agree on `v0.18.0-beta`.

## v0.17.0-beta - 2026-06-11 (Minor)

Release impact: Minor because this changes the governed OpenCode default entry flow and adds a model-only governed lane without breaking existing specialist/session APIs.

- **Changed OpenCode's default entry point**: `ai-orch opencode` now creates a `governance-lead` run by default, launches OpenCode with the `governance-lead` primary agent, and records the selected specialist separately as `routed_agent`.
- **Added Copilot-aware default model selection**: governed OpenCode runs default to the `ai-orch/coding-gpt55` capability alias, which resolves through the actor's Copilot entitlement when available and otherwise uses the approved platform route.
- **Added a governed model-only lane**: developers can choose `--model-only` with a required `--governance-intent`, creating a tracked `model-gateway` session instead of pretending the work started as a delivery specialist.
- **Added OpenCode subagent config generation**: generated and sandbox configs now define `governance-lead` as the primary agent and delivery agents as subagents with session-token, actor and intent headers.
- **Cleaned public POC docs**: examples now use org-neutral work-item IDs and stale backend-spike/OpenCode instructions were removed or updated.
- **Aligned version references**: root `VERSION`, Go runtime version, Bridge package metadata, README, and changelog now agree on `v0.17.0-beta`.

## v0.16.0-beta - 2026-06-10 (Minor)

Release impact: Minor because this hardens auto-session governance and adds ledger UI/API fields without removing existing endpoints or response fields.

- **Governed model-gateway auto sessions**: auto-created sessions now pass through kill switches, work-item requirements, policy evaluation, classification ceilings, secret scanning, and cost-cap checks before they can route model calls.
- **Bound auto sessions to per-session tokens**: auto-session responses now return `X-AI-Orch-Session-ID` and `X-AI-Orch-Session-Token`; subsequent explicit calls for that session require the token.
- **Corrected auto-session trust labels**: self-asserted runtime clients record `self_reported/advisory` unless they present the configured trusted-client token.
- **Added ledger fields**: session list APIs now include additive activity-ledger metadata such as latest event, transport, trust/enforcement, patch state/count, tool-call count, and policy reason.
- **Made the UI ledger-first**: the Governance UI now opens on an Activity Ledger table with model, token, cost, trust, status, and detail controls.
- **Safe default environment**: root `.env.example` now boots with Bifrost by default and documents Copilot as opt-in.
- **Culled redundant direct OpenRouter backend**: Bifrost remains the OpenRouter/provider route for the POC, with per-user Copilot as the actor-bound alternate.
- **Culled redundant backend spike**: Bifrost now owns shared provider gateway plumbing for the POC; per-user Copilot remains separate for actor-bound entitlements.
- **Aligned version references**: root `VERSION`, Go runtime version, Bridge package metadata, README, and changelog now agree on `v0.16.0-beta`.

## v0.15.0-beta - 2026-06-08 (Minor)

Release impact: Minor because this adds scheduled model-pricing refresh, pricing-aware session reporting, streaming token capture and UI reporting fields without breaking existing endpoints.

- **Added model-pricing refresh**: Governance Shell now creates a durable SQLite `model_pricing` table when using the SQLite audit database and refreshes OpenRouter model prices on startup and every `AI_ORCH_MODEL_PRICING_REFRESH_INTERVAL` interval.
- **Added pricing-aware session summaries**: `GET /v1/sessions` and `GET /v1/audit/sessions/{id}` now return model alias, resolved model, provider/backend, prompt/output/total token counts, estimated cost, and cost source where audit data allows it.
- **Captured streaming usage**: the model compatibility gateway requests stream usage, preserves usage-only SSE chunks for compatible clients, and records streamed token/cost data on `model.gateway_stream.completed` audit events.
- **Made session logs easier to read**: the Governance UI now shows human-friendly permission/approval/workspace labels plus model, token and cost lines in Recent Sessions and Session Audit Trail.
- **Aligned version references**: root `VERSION`, Go runtime version, Bridge package metadata, README, and changelog now agree on `v0.15.0-beta`.

## v0.14.0-beta - 2026-06-08 (Minor)

Release impact: Minor because this adds a backward-compatible session listing API and first-class UI audit trail while preserving existing session creation, audit lookup and auth contracts.

- **Added recent session listing**: `GET /v1/sessions` now returns bounded, actor-scoped session summaries for authenticated UI, CLI, IDE and MCP clients without exposing raw prompts or prompt hashes.
- **Made audit evidence visible in the UI**: `/ui/` now shows Recent Sessions and an auto-loaded Session Audit Trail so governed activity, model-gateway events and patch decisions are visible without manually pasting session IDs.
- **Clarified UI auth boundaries**: the UI labels developer/OIDC auth as the human/client control-plane credential and notes that runtime model calls use the separate runtime token outside this screen.
- **Aligned version references**: root `VERSION`, Go runtime version, Bridge package metadata, README, and changelog now agree on `v0.14.0-beta`.

## v0.13.0-beta - 2026-06-08 (Minor)

Release impact: Minor because this adds a backwards-compatible OpenCode custom-provider install path and a real local OpenCode E2E smoke while preserving existing API contracts and beta runtime defaults.

- **Added OpenCode config installation**: `opencode-smoke install-config` patches global, project, or explicit `opencode.json` files with the documented custom provider shape for `ai-orch`, backs up existing files, preserves unrelated settings, and refuses to overwrite a different `provider.ai-orch` block unless `--force` is supplied.
- **Added macOS/Linux and Windows OpenCode wrappers**: `scripts/install-opencode-ai-orch.sh` and `scripts/install-opencode-ai-orch.ps1` give teams a direct way to install the governed provider endpoint on local OpenCode.
- **Added local OpenCode E2E smoke**: `opencode-smoke e2e` creates a governed session, runs the local `opencode` binary against the ai-orch model gateway with `OPENCODE_CONFIG`, `AI_ORCH_RUNTIME_TOKEN`, and `AI_ORCH_SESSION_ID`, then verifies the session audit contains a `model.gateway` event.
- **Aligned version references**: root `VERSION`, Go runtime version, Bridge package metadata, README, and changelog now agree on `v0.13.0-beta`.

## v0.12.2-beta - 2026-06-08 (Patch)

Release impact: Patch because this improves beta demo readiness, operator UI auth clarity, and repeatable local verification without changing public API contracts.

- **Added CIO demo verification**: `scripts/cio-demo-verify.sh` builds the beta images, starts an isolated demo Compose project, validates the catalogue, runs the governed beta smoke, checks protected status/metrics/UI, and leaves the stack running for the demo.
- **Clarified UI auth posture**: the Governance UI now treats a missing developer token as an auth-pending state instead of logging protected-endpoint failures on first load.
- **Added demo readiness status**: the UI first screen now rolls service, auth, gateway, runtime gateway, agent catalogue and smoke evidence into a compact readiness panel for operator checks.
- **Aligned version references**: root `VERSION`, Go runtime version, Bridge package metadata, README, and changelog now agree on `v0.12.2-beta`.

## v0.12.1-beta - 2026-06-04 (Patch)

Release impact: Patch because this fixes beta Bridge flow, MCP OAuth token binding, local prompt cleanup, and verification drift without changing public API contracts.

- **Fixed Bridge follow-up runs**: completed or patch-ready sessions can now continue through `/v1/sessions/{id}/turns` instead of always starting a new governed run.
- **Preserved explicit Bridge context**: attached files, terminal summaries, search hits, prior-session links and local tool notes survive the fresh workspace scan performed before sending a prompt.
- **Restricted Bridge file attachments**: the Bridge now skips files selected outside the open workspace instead of treating absolute paths as workspace-relative input.
- **Hardened MCP OAuth token binding**: `oauth-user` MCP forwarding now resolves user tokens from the durable session owner, not from a caller-supplied `X-AI-Orch-User-ID` header.
- **Cleaned follow-up prompt memory**: auto-confirmed follow-up turns clear the process-local prompt copy once dispatch starts.
- **Completed runtime enforcement labels**: runtime denial and patch-rejection audit events now carry gateway enforcement metadata.
- **Fixed Bridge verification drift**: Bridge test typechecking no longer relies on matcher typings that are absent from the current Bun test types, and package-version alignment is now covered by a test.
- **Aligned version references**: root `VERSION`, Go runtime version, Bridge package metadata, README, plan, and changelog now agree on `v0.12.1-beta`.

## v0.12.0-beta - 2026-06-04 (Beta)

Release impact: Beta marks a frozen local integration surface for governed runs, model gateway headers, and MCP session binding. Provider-backed smoke is optional and runs nightly when configured.

- **Added provider-backed beta smoke**: `beta-gateway-smoke` exercises governed-run + model gateway chat completions; `provider` mode proves live orchestrator dispatch and patch envelopes.
- **Added nightly provider workflow**: `.github/workflows/nightly-provider-smoke.yml` runs when `OPENROUTER_API_KEY` is configured.
- **Added Compose profiles**: `docker-compose.provider.yml` for live model checks; `docker-compose.team-beta.yml` for optional OIDC on the Governance Shell.
- **Extended OpenCode smoke**: `opencode-smoke gateway-smoke` runs the same gateway path without the OpenCode binary.
- **Fixed provider smoke prompt wiring**: provider run smoke now uses `AI_ORCH_PROVIDER_PROMPT` instead of inheriting the gateway chat prompt default.

- **Promoted local beta verification**: `scripts/beta-verify.sh`, Compose profile `beta`, and CI job `beta-smoke` run governed-run smoke without provider API keys.
- **Added beta smoke dispatch mode**: `AI_ORCH_BETA_SMOKE=true` routes orchestrator dispatch through `EchoRuntime` for CI and local vertical-slice checks.
- **Added offline router golden-case tests**: router-agent `golden-cases.yaml` is executed in `go test` via `TestRouterGoldenCasesOffline`.
- **Improved keyword routing**: orchestrator keyword routing now covers frontend, backend, terraform, and security-review cases aligned with the catalog.
- **Frozen API contract**: `docs/api-contract-v1.md` documents beta-stable `/v1` routes, auth tokens, patch envelopes, and gateway headers.
- **Added patch protocol tests**: `internal/patch` JSON round-trip coverage for beta patch envelopes.
- **Fixed orchestrator route context encoding**: `SessionContext` now serializes with snake_case JSON fields so `/v1/runs` routing works against the orchestrator API.
- **Aligned version references**: root `VERSION`, Go runtime version, Bridge package metadata, README, plan, and changelog now agree on `v0.12.0-beta`.

## v0.11.0-alpha - 2026-06-04 (Minor)

Release impact: Minor because this adds backwards-compatible gateway runtime paths and a first Governance UI while preserving the existing APIs and Bifrost default.

- **Explored alternate backend plumbing**: the beta briefly carried extra Compose/backend paths while the provider-gateway boundary was being tested; the active POC path is now Bifrost plus actor-bound Copilot.
- **Removed the hard Bifrost startup dependency**: Governance Shell now waits for the selected backend health endpoint itself, so local startup can report backend readiness clearly.
- **Added backend health retry tests**: Governance Shell startup now retries selected backend readiness before failing closed, covering slower sidecar startup.
- **Added a simple Governance UI**: Governance Shell now serves `/ui/` with service posture, gateway options, metrics, agents, evidence, maturity exports, audit lookup, and use-case/workflow registration backed by existing APIs.
- **Added system status API**: `GET /v1/system/status` returns version, active backend, model gateway status, policy settings and supported gateway options for the UI and operator checks.
- **Aligned version references**: root `VERSION`, Go runtime version, Bridge package metadata, README current state and changelog now agree on `v0.11.0-alpha`.

## v0.10.0-alpha - 2026-06-04 (Minor)

Release impact: Minor because this adds a backwards-compatible model-backend option and configuration surface while preserving the existing Bifrost path.

- **Tested alternate provider plumbing**: early beta work validated that ai-orch can keep session, routing and audit ownership while delegating provider compatibility behind the shell.
- **Kept Bifrost as the proven local sidecar**: Bifrost remains the default provider-plumbing backend for the POC.
- **Added an OpenCode Docker sandbox path**: Compose now has an opt-in `opencode-sandbox` profile with a session-bound ai-orch OpenCode config, so provider-gateway E2E can be tested without using the local OpenCode install.
- **Made OpenCode smoke config session-aware**: `opencode-smoke generate-config` now emits `AI_ORCH_RUNTIME_TOKEN` plus `X-AI-Orch-Session-ID` env placeholders, and `opencode-smoke run` can launch local OpenCode when a governed session is supplied.
- **Reduced backend adapter duplication**: OpenAI-compatible HTTP/streaming adapter code is shared where the backend contract allows it.
- **Aligned version references**: root `VERSION`, Go runtime version, Bridge package metadata, README current state and changelog now agree on `v0.10.0-alpha`.

## v0.9.2-alpha - 2026-06-04 (Patch)

Release impact: Patch because this fixes governance reporting integrity, local migration safety, and generated runtime guidance without changing public API routes.

- **Fixed evidence-store migrations**: existing SQLite registry databases now add `trust_level` and `enforcement_mode` columns before evidence writes use them.
- **Hardened trust-label reporting**: Governance Shell now derives trust and enforcement labels from the request path/client class, and external native tool/model evidence is always recorded as `self_reported` and `advisory`.
- **Completed audit enforcement labels**: model gateway, model proxy, MCP proxy, session, run, message, confirmation and patch-decision audit events now include matching `enforcement_mode` values.
- **Stopped leaking runtime tokens in OpenCode config output**: generated OpenCode provider config now references `AI_ORCH_RUNTIME_TOKEN` instead of embedding the runtime token, and the helper no longer presents an unimplemented OpenCode E2E as executed.
- **Sanitised CLI git context**: Go-side repository context now strips username/password data from HTTPS remotes before sending or storing repo metadata.
- **Cleaned duplicated policy entries**: MCP registrations and command allow-lists no longer repeat `unit-tests`.
- **Aligned version references**: root `VERSION`, Go runtime version, Bridge package metadata, README current state and changelog now agree on `v0.9.2-alpha`.

## v0.9.1-alpha - 2026-06-04 (Patch)

Release impact: Patch because this corrects governance wording and generated client guidance without changing public APIs or runtime behaviour.

- **Clarified trust labels as reporting facts**: README, implementation notes, runtime integration docs and MCP gateway docs now state that `gateway_enforced`, `managed_client` and `self_reported` describe how work ran, not whether a developer is allowed to use a client.
- **Updated generated client guidance**: Skills Factory output now says trust levels are audit/reporting observations, not permission settings.
- **Aligned version references**: root `VERSION`, Go runtime version, Bridge package metadata, README current state and changelog now agree on `v0.9.1-alpha`.

## v0.9.0-alpha - 2026-06-04 (Minor)

Release impact: Minor because this adds a first-class governed run API, explicit permission/approval modes, MCP run creation, and Bridge/CLI run-flow changes while preserving the existing alpha session APIs.

- **Added governed runs**: `POST /v1/runs` now creates a governed session, records run context, routes the first prompt, returns the next approval gate, and exposes the session SSE URL.
- **Added run metadata to audit and sessions**: audit events and durable session records now carry `run_id`, `permission_mode`, `approval_mode`, `workspace_mode`, branch/work-item hints, commit SHA, actor hint and source system before routing.
- **Made server-side context resolution opt-in**: local git context resolution is disabled by default and can only be enabled with `AI_ORCH_ENABLE_SERVER_CONTEXT_RESOLVER=true` or `--enable-server-context-resolver`; client-supplied context is the primary source.
- **Added permission and approval labels**: `read_only`, `reviewed`, `auto_apply`, `full_access`, `manual`, `auto_approved`, `yolo` and `self_reported` are now validated and recorded for governance reporting.
- **Added MCP run creation**: `ai-orch-mcp` now exposes `start_governed_run` so MCP clients can start a governed run with project/work-item and permission metadata in one call.
- **Updated CLI smoke semantics**: `ai-orch smoke` now starts a governed run through `/v1/runs`, sends lightweight local git/work-item context, and says explicitly that `applied` records a patch decision without mutating the workspace.
- **Updated VS Code Bridge run flow**: the Bridge now starts a governed run through `/v1/runs`, sends parsed branch work-item hints, then confirms and streams the routed session.
- **Aligned agent naming**: active code, docs, policies and MCP registrations now use `unit-tests` instead of the deleted `test-generation` agent.
- **Tightened branch routing**: broad `feature/*`, `chore/*` and `release/*` branches no longer blindly route to backend development; only specific frontend/backend/test/docs/refactor/security/bugfix hints override keyword routing.
- **Restored local roadmap**: added ignored local `plan.md` with Phase 0 through Phase 4 and the current real-patching target.
- **Documented patch semantics**: README, implementation notes and deployment docs now distinguish CLI decision-only behaviour, Bridge local apply behaviour and pending OpenCode sandbox/worktree patching.
- **Aligned version references**: root `VERSION`, Go runtime version, Bridge package metadata, README current state, and changelog now agree on `v0.9.0-alpha`.

## v0.8.0-alpha - 2026-06-04 (Minor)

Release impact: Minor because this adds governed MCP delegation, policy-enforced upstream MCP tool calls, and richer router decision metadata while preserving the existing alpha APIs.

- **Closed Phase 1I: Governed Delegation**: added `create_context_manifest`, `attach_use_case` and `attach_workflow` MCP tools so clients can bind bounded context, use cases and workflows before delegating work.
- **Closed Phase 1I.5: Governance Router enrichment**: router decisions now include `cost_posture`, `latency_posture`, `requested_alias` and richer reason codes (workflow stage, risk level, evidence required). Scoring now considers workflow stage, latency sensitivity and evidence needs alongside task type and risk.
- **Closed Phase 1J: Gateway-Enforced Tool Calls**: upstream MCP tools are now exposed through `ai-orch-mcp` via `list_allowed_tools` and `call_governed_tool`. The Governance Shell MCP proxy now requires a durable governed session, filters catalog results by session agent/classification policy, denies disallowed tools before forwarding, and audits allowed/denied calls as `gateway_enforced`.
- **Fixed MCP proxy forwarding path**: the proxy now calls upstream endpoints directly (`/{toolName}`) instead of incorrectly prefixing `/tools/`, matching the actual upstream server implementations.
- **Loaded MCP runtime policy from registrations**: runtime MCP registrations now carry `allowed_agents` and `tool_policy` data from `mcp/registrations/*.yaml` instead of maintaining a separate hardcoded tool list.
- **Added MCP catalog endpoint**: `GET /internal/v1/mcp/catalog` returns only the upstream servers and tools allowed for the supplied governed session.
- **Hardened MCP audit semantics**: forwarded tool calls are audited before the upstream request is sent, denied tool calls produce `tool_call_denied` audit events with `policy_decision_id`, and audit persistence failure blocks forwarding.
- **Added integration and policy tests**: tests now cover session-required MCP forwarding, denied tools, denied agents, policy-filtered catalog results, safe context manifest IDs, and the full MCP gateway loop from session creation through audit lookup.
- **Made context manifest IDs route-safe**: `create_context_manifest` now sends stable hashed manifest IDs instead of raw `source_system/source_object_id` strings that can contain path separators.
- **Updated Skills Factory guidance**: generated `AGENTS.md`, `.clinerules` and `CLAUDE.md` now reference the complete tool surface including context manifests, use cases, workflows and upstream tool calls.
- **Fixed CLI smoke help behaviour**: `ai-orch smoke --help` and `ai-orch smoke -h` now print usage instead of accidentally running the live smoke path.
- **Aligned version references**: root `VERSION`, Go runtime version, Bridge package metadata, README current state, and changelog now agree on `v0.8.0-alpha`.

## v0.7.0-alpha - 2026-06-04 (Minor)

Release impact: Minor because this adds a selectable model-backend layer, Bifrost OSS sidecar integration, and managed MCP/doctor trust-labelling improvements.

- **Added provider-neutral model backends**: Governance Shell model calls now route through a backend interface, with Bifrost as the retained local sidecar path.
- **Added Bifrost OSS sidecar support**: Docker Compose now starts a pinned `maximhq/bifrost:v1.5.7` sidecar by default, keeps it off the host network, mounts file-based config, disables Bifrost content logging, and lets Governance Shell use it through `AI_ORCH_MODEL_BACKEND=bifrost`.
- **Kept OpenRouter health smoke coverage**: the `openrouter-smoke` tool remains a provider-health check while runtime calls route through governed backend plumbing.
- **Kept provider secrets out of runtimes**: the Orchestrator, VS Code Bridge, MCP clients, and runtime-facing model gateway still call ai-orch endpoints rather than Bifrost or provider APIs directly.
- **Added Bifrost model mapping**: provider/model registry entries are translated to Bifrost model names such as `openrouter/deepseek/...`, `anthropic/...`, and `bedrock/...`.
- **Added direct provider smoke aliases**: model registry now includes local Bifrost smoke aliases for direct OpenAI, Anthropic Haiku, and DeepSeek credentials in addition to the OpenRouter DeepSeek smoke path.
- **Added model-backend audit metadata**: model gateway and model proxy audit events now record gateway backend, provider, resolved model, usage, request/response hashes, and `trust_level: gateway_enforced` without storing raw prompt or response content.
- **Added managed-client trust labels**: Governance Shell audit helpers now accept `gateway_enforced`, `managed_client`, and `self_reported` labels, and the MCP gateway marks routed calls as `gateway_enforced`.
- **Expanded MCP doctor checks**: `ai-orch mcp doctor` now reports generated client config status, developer token status, Governance Shell readiness, runtime token status, and model gateway reachability.
- **Updated local configuration docs**: `.env.example`, README, implementation notes, deployment guide, MCP gateway notes, and model registry docs now describe Bifrost as replaceable provider plumbing rather than the governance plane.
- **Aligned version references**: root `VERSION`, Go runtime version, Bridge package metadata, README current state, and changelog now agree on `v0.7.0-alpha`.

## v0.6.0-alpha - 2026-06-03 (Minor)

Release impact: Minor because this adds first-run Bridge onboarding plus local governance gateway hardening in the alpha line. Admin routes now use a separate local admin token, and runtime model endpoints use a separate runtime token.

- **Added VS Code Bridge onboarding**: the extension now exposes `Setup AI Agent Bridge` for configuring the Governance Shell URL, local identity, and developer token without hand-editing settings.
- **Added Bridge connection checks**: the extension now exposes `Check AI Agent Bridge Connection` and preflights readiness before invoke/audit commands, with output-panel guidance for starting the local Compose stack.
- **Made missing-token checks actionable**: a ready Governance Shell with no developer token now prompts `Run Setup` directly instead of leaving the Bridge in a connected-but-unusable state.
- **Improved token handling for first run**: setup stores the developer token in VS Code SecretStorage and keeps machine-scoped settings for non-secret configuration.
- **Added Bridge workflow tests**: Bun tests now cover URL normalisation, SecretStorage token precedence, missing-token detection, command registration, patch IDs, SSE event parsing, auth headers, and safe patch paths.
- **Added bounded workspace context for the Bridge**: VS Code invocation now includes current workspace name, git branch and origin remote where available, active file metadata, and either selected text or a capped active-file excerpt.
- **Hardened Bridge diff review**: proposed patch paths are now validated before both native diff review and workspace apply.
- **Hardened Bridge readiness checks**: the extension now requires `/readyz` to identify the `governance-shell` service, so another local app on port `8080` is not treated as this POC.
- **Moved the local host default to `18080`**: Compose, the CLI default, VS Code Bridge default, and `.env.example` now avoid the common app-dev `8080` port while keeping internal container traffic on `8080`.
- **Clarified agent workflow expectations**: deployment docs now include a workflow checklist, alternate-port CLI smoke guidance, wrong-service troubleshooting, and the current CLine-style agent-plane limitation.
- **Added the governed IDE agent-plane plan**: documented the path towards CLine-style ergonomics while keeping the Governance Shell as the authority boundary.
- **Documented the MCP gateway direction**: root planning notes now capture why `ai-orch-mcp`, Tool Gateway mode, and a Governance Skills Factory are the preferred next abstraction over a VSIX-only agent experience.
- **Clarified README abstraction uncertainty**: README now describes the live POC tension between VS Code Bridge, MCP gateway, runtime adapters, CLI and governance UI while keeping the Governance Shell as the stable boundary.
- **Expanded integration thinking**: README and planning notes now frame Factory Router as a Governance Router signal, GitHub App as the delivery/evidence boundary, and `t3code` as a workbench UX reference rather than a product shape to copy.
- **Re-centred the near-term roadmap on governance plumbing**: README and planning notes now explicitly prioritise Governance Shell contracts and `ai-orch-mcp` before workbench, GitHub/Azure DevOps app, OpenCode sandbox, or Kubernetes runtime work.
- **Added model compatibility gateway planning**: documented why OpenCode-style runtimes need governed OpenAI-compatible `/v1/models`, `/v1/chat/completions`, `/v1/responses`, and streaming endpoints separate from the MCP tool gateway.
- **Cleaned VSIX metadata**: the Bridge package now includes repository and licence metadata so local packaging is warning-free.
- **Strengthened OpenRouter smoke validation**: the smoke tool now fails on blank or unexpected assistant content and requests hidden reasoning with a larger default token budget for DeepSeek V4 Flash.
- **Aligned runtime version reporting**: ACP client metadata now uses a shared Go version constant instead of a stale hard-coded `v0.5.0-alpha` string.
- **Updated canonical version references**: root `VERSION`, README current state, and changelog now agree on `v0.6.0-alpha`.
- **Added a drift guard**: app version tests now compare the Go runtime version constant with the root `VERSION` file.
- **Split local auth tokens by boundary**: the CLI now uses `AI_ORCH_ADMIN_TOKEN` for `/v1/admin/*` routes, while developer, service and runtime paths keep their own tokens.
- **Exposed the model compatibility gateway in Compose**: local Compose now passes `AI_ORCH_RUNTIME_TOKEN`, exposes `MODEL_GATEWAY_PORT`, and documents OpenAI-compatible model-gateway smoke calls.
- **Required real session correlation for model generation**: `/v1/chat/completions` and `/v1/responses` now require `X-AI-Orch-Session-ID`, and the Governance Shell gateway validates that the session exists before generation.
- **Hardened MCP HTTP transport**: HTTP MCP mode now fails closed without a developer token, binds locally by default through the CLI, uses constant-time bearer-token checks, and rejects non-local browser origins.
- **Made MCP setup safer for arbitrary repos**: `ai-orch mcp install` now refuses to overwrite existing client config files unless `--force` is passed, and generated VS Code config uses stdio instead of unauthenticated SSE.
- **Made self-reported MCP evidence honest**: external tool/model self-report tools now surface persistence failures instead of claiming evidence was recorded when the Governance Shell call failed.
- **Expanded CI coverage**: GitHub Actions now runs Bridge Bun install, tests, typecheck, lint and compile alongside Go build, vet and tests.
- **Removed accidental nested Go module metadata**: the VS Code Bridge no longer contains a stray `go.mod`.
- **Added policy-engine boundary tests**: direct policyengine coverage now exercises classification ceilings, secret-pattern detection, command allow-list permissions, required permissions, and tool-loop cap behaviour.
- **Guaranteed critical ACP runtime events**: patch, done, and error events now use a blocking send path with cancellation awareness, while non-critical stream chatter remains best-effort.
- **Required explicit ACP workspaces**: ACP runtime startup now fails closed when no workspace path is configured instead of defaulting to the Governance Shell process directory.
- **Bound model gateway routing to durable sessions**: OpenAI-compatible model calls now derive classification from the stored session record, require durable session storage when the runtime gateway is enabled, and ignore caller-supplied classification headers for routing.
- **Hardened streaming model calls**: OpenRouter streaming requests now send `stream: true`, use a no-overall-timeout streaming client, accept both `data:` and `data: ` SSE prefixes, and audit stream completion or failure with hashes.
- **Hardened local MCP gateway plumbing**: MCP HTTP transport now uses loopback Host enforcement, request-size caps, cryptographically random SSE session IDs, and stdio reads that support large final JSON-RPC lines without a trailing newline.
- **Fixed MCP evidence and path handling**: self-reported MCP evidence now posts to `/v1/evidence`, and MCP session IDs are escaped before use in Governance Shell URL paths.
- **Tightened patch-buffer remediation behaviour**: patch proposals may remove secrets from pre-existing original content, but proposed new content is still rejected when it contains secret patterns.
- **Improved local CI and image hardening**: GitHub Actions and Docker builds now use Go `1.26.4`; CI also pins `govulncheck`, disables persistent checkout credentials, and declares read-only repository permissions.
- **Aligned Bridge package and typechecks**: the Bridge package version now matches `v0.6.0-alpha`, malformed Governance Shell URLs return friendly errors, and Bridge tests are included in TypeScript checking.

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
