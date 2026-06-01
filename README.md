# AI Agent Orchestration POC

This repository is my proof of concept for an agent orchestration system that I am currently investigating.

The goal is not to rebuild every agent runtime, IDE workflow, or coding assistant stack inside this project. The goal is to test whether a lightweight system layer can give agent work better governance, routing, auditability, and policy control without forcing every team or engineer into one runtime.

## Project State

Current as of 2026-06-01: this is a personal-time POC in active early development, with `v0.4.0-alpha` as the current version.

Implemented:

- Phase 0 catalogue and agent-definition foundation
- session creation, session ownership, and state guards
- audit events, audit lookup, linked audit envelopes, policy gates, and service-token hardening
- tamper-evident audit hash chaining for the local two-process flow
- router selection, specialist dispatch, and SSE event streaming
- Docker Compose local runner, catalogue validation tooling, and hot-path catalogue validation caching
- OpenRouter model smoke tooling
- opt-in OpenRouter-backed CLI orchestration smoke path
- source-aware CLI runs through `ai-orch session create --workspace`
- Governance Shell model proxy for OpenRouter calls, so the Orchestrator does not need the provider API key
- staged patch buffering, with sanitised SSE metadata and governed patch fetch before Bridge apply/review
- MCP proxy stub with `oauth-user` fail-closed behaviour when user OAuth is absent
- native policy-engine boundary with AGT reserved as a future adapter
- consecutive tool/MCP-call cap controls
- command allow-list enforcement against agent permissions
- Phase 1F use-case, workflow, context-manifest, cache-outcome, evidence, and maturity-governance APIs
- local-state TTL documentation and eviction for prompts, patches, cancellations, and patch buffers
- experimental composition APIs and assembly-line reference scaffolding with human gates
- authenticated audit-retention administration for SQLite audit storage
- EchoRuntime and DirectRuntime patch envelopes for local testing
- local CLI, VS Code Bridge, and MCP stub scaffolds
- optional SQLite audit storage and optional OIDC token-validation scaffolding

Next:

- real OpenCode patch-producing flow
- durable audit-chain state for restart and multi-instance use
- durable registry storage for team use
- manual VS Code Bridge validation
- broader CLI coverage for admin and CI-shaped workflows
- real user OAuth token acquisition for `oauth-user` MCPs

Not production-ready.

## What This POC Is Exploring

The strategic bet is that the Governance Shell should become the layer that can sit in front of different agent runtimes, not a runtime that tries to replace all of them.

This POC is built around a deliberately separate abstraction:

- `agent.md` for human and model-readable agent instructions
- `agent.config.yaml` for executable runtime, tool, model, and governance configuration
- a model registry for provider and model indirection
- an agent catalogue for discoverability and validation
- a Governance Shell that sits in front of agent execution

That shape is intentional.

The word "system" is deliberate here: this is still a local POC, not a broader shared product.

The system abstraction is more enterprise-shaped: policy, audit, model routing, catalogue validation, runtime boundaries, and future organisational controls. Some of the runtime patterns I am looking at are more engineer-workspace-shaped, which is useful, but different.

## External Governance Work I Am Watching

This POC is not being built in isolation. Part of the investigation is watching where the wider agent ecosystem is moving, then deciding what to borrow without letting the repo become a wrapper around someone else's stack.

The most relevant governance-shaped reference right now is [Microsoft Agent Governance Toolkit](https://github.com/microsoft/agent-governance-toolkit). Microsoft introduced it as [open-source runtime security for AI agents](https://opensource.microsoft.com/blog/2026/04/02/introducing-the-agent-governance-toolkit-open-source-runtime-security-for-ai-agents/), with policy enforcement, auditability, threat detection, key management, access control, and governance across different agent frameworks. That is close to the problem space this POC cares about.

My current read is:

- AGT is worth tracking closely as a policy-engine candidate.
- AGT should not be adopted as a hard dependency yet.
- The local native policy engine stays the default while the POC proves its own boundary.
- The `agt` policy-engine option is intentionally reserved as a fail-closed adapter path until there is a proper spike.

That distinction matters. The Governance Shell is the asset I am trying to prove here. If AGT becomes useful, it should plug into that boundary. It should not quietly replace the `agent.md` + `agent.config.yaml` catalogue, the OpenRouter model proxy, the staged patch buffer, or the audit contract.

I am also watching GitHub's agentic workflow security architecture because it makes the same broad point in another way: agents should not carry broad secrets, and file writes should be staged, reviewed, and mediated rather than trusted by default.

## What This POC Is Not

This is not trying to clone a Claude Code-style workspace stack.

That kind of stack is valuable because it fits directly into an engineer's local workflow: repo context, terminal tools, file edits, test loops, and tight feedback. But rebuilding that structure here would blur the purpose of this system.

The better approach is to borrow useful concepts, not copy the structure.

For example, this POC can learn from workspace-shaped tools around:

- how agents receive repo context
- how tools are constrained
- how patch proposals are reviewed
- how local developer workflows stay fast
- how agent instructions remain understandable

But those ideas should be absorbed into the Governance Shell and catalogue model, not replace them.

## Not In Scope

- Replacing Claude Code, OpenCode, Cursor, Aider, or any other IDE-native agent runtime.
- Building an autonomous run loop like Symphony inside this repo.
- Acting as a model gateway in place of OpenRouter, LiteLLM, or provider-native gateways.
- Owning identity, secrets management, or enterprise token brokering at organisation scale.
- Treating the local POC scaffolding as production-grade deployment infrastructure.

## Runtime Patterns To Watch

The strategic question is not "which agent runtime wins?"

The better question is: how does governance stay useful when engineers adopt different runtime patterns?

Right now, I am keeping an eye on three broad patterns:

1. Autonomous runtime patterns

   Tools like Symphony-style autonomous execution may become attractive for longer-running, less interactive work. These need strong controls around scope, audit, approval gates, and failure boundaries.

2. Workspace-shaped coding stacks

   Claude Code-style workflows are close to how engineers already work. They are fast, practical, and developer-friendly, but they may not naturally provide centralised governance, catalogue visibility, or consistent audit trails.

3. Segment-built variants

   Teams may build their own local or domain-specific agent wrappers. This is useful, but it creates the original risk: many parallel implementations with different policies, models, prompts, logs, and review standards.

## The Strategic Bet

The Governance Shell is the important asset.

It should become the layer that can sit in front of different agent runtimes, not a runtime that tries to replace all of them.

If this works, the system can offer a common path through multiple agent execution styles:

- register the agent
- validate its config
- route through approved models
- enforce policy before execution
- record audit events
- require human gates where needed
- keep sensitive workspace behaviour bounded
- allow future promotion from experimental to published agents

That means engineers can still use practical tools, while the system provides a consistent control surface.

## Maturity Governance Outputs

This POC should produce machine-readable governance outputs for a separate engineering maturity and reporting layer. It should not become that reporting layer itself.

The outputs this system needs to emit are:

- **Session summary**: session ID, actor, team, use case ID, workflow ID, work item reference, repository, branch, commit, classification, risk level, requested agent, selected specialist, runtime, model alias, resolved model, timestamps, and final status.
- **Policy and control outcomes**: allow/block decisions, policy reason, classification result, secret-scan result, kill-switch status, OAuth failure, tool-loop cap result, cost-cap result, and human gate decisions.
- **Cost and value sizing**: human baseline sizing from Jira or Azure DevOps when available, estimated dev days, blended day rate, baseline cost, model cost, tool/API cost, platform/runtime cost, human review effort, verification effort, retry count, and estimated net saving.
- **Context provenance**: context manifest ID, source system, source object ID, source path or URL, auth scope, freshness/version, classification, cache status, included summary/chunk hashes, and whether the source influenced a model call.
- **Evidence records**: generated or selected tests, test execution result, quality-system link, security finding, architecture review output, patch metadata, approval receipt, patch decision, and external ticket/work-item link.
- **Outcome metrics**: success or failure, accepted/rejected/partial decision, evidence completeness, cycle-time signal, quality result, review effort, verification cost, blocked-event category, and adoption signal.
- **Cache outcomes**: session cache hits and misses, reusable context summaries, cache eligibility decisions, cache savings estimate, cache expiry, invalidation reason, and whether cached context was allowed by classification, actor, repository, and workflow policy.

These outputs should be exportable to a maturity governance system that already understands engineering health, maturity, reporting, benchmarks, and value realisation. The IDE and CLI should send lightweight IDs and intent; the Governance Shell should resolve context, policy, provenance, evidence, and cost records behind the boundary.

Caching is part of the governance boundary, not a hidden memory product. The first cache should be session-scoped and policy-aware: it can reuse safe context summaries, model-call metadata, and connector read results inside one governed session, but it must record provenance, classification, actor scope, expiry, invalidation, and estimated savings. Cross-session or semantic caching should only be added later with explicit approval rules.

The current hardening line is audit, context, and state integrity. This branch starts that work with local audit hash chaining, explicit local-state lifecycle documentation, command allow-list enforcement, and control-plane registry outputs. Before this POC can be treated as more than a local experiment, those local stores still need durable multi-instance backing and restart-aware chain state.

## Current POC Direction

This repo currently focuses on the system-side foundation:

- agent definition standard
- temporary agent catalogue
- model registry
- Governance Shell scaffold
- Orchestrator scaffold
- local Docker Compose workflow
- OpenRouter-backed model and CLI orchestration smoke testing
- Governance Shell-owned OpenRouter model proxy
- staged patch buffer and patch fetch endpoint
- tamper-evident audit hash chain for local Governance Shell and Orchestrator writes
- use-case, workflow, context-manifest, cache-outcome, evidence, and maturity-governance API surfaces
- command allow-list enforcement tied back to agent permissions
- local CLI and VS Code Bridge scaffolds
- MCP registration and token-guarded stub services
- MCP proxy stub for later user-scoped tool calls
- first dispatch and SSE event path
- local EchoRuntime patch envelope and patch-decision audit path
- audit and policy primitives

The near-term goal is to prove a thin vertical slice rather than overbuild the full system.

A useful first slice is:

1. Select a temporary agent from the catalogue.
2. Resolve its model alias through the registry.
3. Pass through the Governance Shell.
4. Record an audit trail.
5. Route to the Orchestrator.
6. Produce or simulate a patch proposal.
7. Review the patch through an explicit decision gate.

## Local Run

The runnable scaffold lives under `ai-agent-orch/`.

```sh
cd ai-agent-orch
go test ./...
docker compose build
docker compose --profile tools run --rm catalog-validator
AI_ORCH_DEV_TOKEN=local-dev docker compose up governance-shell orchestrator
```

In another terminal:

```sh
curl http://127.0.0.1:8080/readyz
curl -H "Authorization: Bearer local-dev" \
  -H "Content-Type: application/json" \
  -d '{"agent":"test-generation","classification":"internal","prompt":"add regression tests for this module"}' \
  http://127.0.0.1:8080/v1/sessions
```

In Docker Compose, the Orchestrator is intentionally kept on the internal Compose network. The Governance Shell reaches it with a service-to-service token.

OpenRouter credentials are intentionally attached to the Governance Shell service, not the Orchestrator. Full orchestration model calls go through the internal model proxy at `/internal/v1/model/chat`.

OpenRouter smoke testing requires `OPENROUTER_API_KEY`:

```sh
OPENROUTER_API_KEY=... docker compose --profile tools run --rm openrouter-smoke
```

For local repeated testing, keep secrets in an ignored root `.env.dev` file and pass it to Compose. The file should contain `OPENROUTER_API_KEY`; do not commit it.

```sh
docker compose --env-file ../.env.dev --profile tools run --rm openrouter-smoke
```

To smoke test a specific OpenRouter model alias through the registry:

```sh
docker compose --env-file ../.env.dev --profile tools run --rm openrouter-smoke \
  openrouter-smoke -catalog-root /app \
  -model-alias coding-gpt55 \
  -prompt 'Reply with exactly: gpt55-alias-ok'
```

To force a local CLI orchestration smoke through GPT-5.5 with high reasoning effort, start the Orchestrator with explicit overrides. These overrides are opt-in test settings; they are not the default runtime policy.

```sh
AI_ORCH_MODEL_ALIAS_OVERRIDE=coding-gpt55 \
AI_ORCH_OPENROUTER_REASONING_EFFORT=high \
AI_ORCH_OPENROUTER_REASONING_EXCLUDE=true \
docker compose --env-file ../.env.dev up -d governance-shell orchestrator
```

Then run the CLI smoke command:

```sh
docker compose --env-file ../.env.dev --profile tools run --rm ai-orch \
  ai-orch smoke --prompt 'Return only this JSON object and no markdown: {"protocolVersion":1,"patchId":"readme_smoke","summary":"CLI orchestration smoke patch","files":[{"path":"SMOKE_TEST.md","action":"create","content":"CLI orchestration smoke passed."}]}'
```

The `ai-orch` CLI scaffold currently covers local smoke tests, audit lookup, kill-switch checks, session commands, and agent listing:

```sh
AI_ORCH_DEV_TOKEN=local-dev docker compose --profile tools run --rm ai-orch
```

The CLI sends the prompt text through the governed workflow. Until the OpenCode or Bridge path supplies workspace context automatically, include selected source excerpts in the prompt when running source-aware CLI tests.

The CLI receives sanitised patch metadata over SSE and records the patch decision by ID. The VS Code Bridge fetches full buffered patch content from `GET /v1/sessions/{session_id}/patches/{patch_id}` before rendering diffs or applying changes.

Phase 2 read-only MCP stubs for issue tracker, documentation, and test-management context are available behind the `phase2` Compose profile. They are local token-guarded stubs only; user-scoped OAuth is still pending.

The MCP proxy already fails closed for `oauth-user` registrations when user OAuth is absent. That contract is tested with fake token stores; real OAuth acquisition remains Phase 2 work.

Cost-cap enforcement is intentionally off by default. To test the blocking path locally, start the Governance Shell with `AI_ORCH_COST_CAP_ENABLED=true` and a non-zero `AI_ORCH_SESSION_COST_CAP_USD`.

Tool-loop enforcement is on by default with `AI_ORCH_CONSECUTIVE_TOOL_CALL_MAX=15`, blocking a runtime that keeps issuing tool/MCP calls without producing output.

The VS Code Bridge expects `AI_ORCH_DEV_TOKEN` in the extension host environment; it does not fall back to an implicit token.

There is no standalone web UI yet. The working surfaces are the HTTP API, the `ai-orch` CLI, and the VS Code Bridge.

The VS Code Bridge scaffold lives at `ai-agent-orch/agent-bridge/`, with a packaged VSIX at `ai-agent-orch/agent-bridge/ai-agent-bridge.vsix`.

```sh
cd ai-agent-orch/agent-bridge
bun run typecheck
bun run lint
bun run compile
code --install-extension ai-agent-bridge.vsix
AI_ORCH_DEV_TOKEN=local-dev code ../..
```

If the Governance Shell is running on a non-default host port, set `aiAgentBridge.governanceUrl` in VS Code settings, for example `http://127.0.0.1:18080`.

## Guiding Principle

Do not mix runtime structure with governance structure too early.

The system should stay small, boring, and strict at the boundary. The runtime can remain flexible behind that boundary.

That is the point of this POC.

## License

This project is licensed under the Apache License 2.0.

If you use, copy, modify, distribute, or build on this work, retain the attribution in [NOTICE](NOTICE) or otherwise provide clear credit to the project author.
