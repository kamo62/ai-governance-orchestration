# AI Agent Orchestration POC

This repository is a personal-time proof of concept for a governance platform for AI-assisted engineering work.

The bet is simple: teams will use different agents, IDEs, CLIs, model providers, and workflow tools. Trying to replace all of that with one new agent runtime is the wrong move. The useful layer is a small, strict control plane that can sit in front of those runtimes and make agent work governable.

The part I am still working through is the correct abstraction layer. Is the first-class surface a VS Code extension, an MCP gateway, a runtime adapter, a CLI, a provider endpoint, or some combination of those? My current read is that the Governance Shell is the product-shaped boundary, the model gateway is the enforceable model path, MCP is the best interoperability layer for tools, and the VS Code Bridge is useful as a first-party experience but should not become the whole strategy.

The newer wrinkle is that the developer integration surface may not be one thing at all. MCP feels like the agent-tool boundary. GitHub feels like the delivery boundary. A local workbench or VS Code Bridge might be the ergonomic layer. I am trying not to confuse those three.

The current practical priority is simpler than the long-term architecture: get the Governance Shell and MCP plumbing right first. The workbench, GitHub/Azure DevOps app layer and Kubernetes runtime pool can wait until the policy, audit, routing and tool-gateway contracts are boring enough to trust.

There is one more piece of plumbing in that first bucket: a model compatibility gateway. OpenCode, Cline and similar runtimes need configurable model endpoints. They should call an `ai-orch` endpoint, not OpenRouter, Bifrost, Bedrock, Anthropic or provider APIs directly, so model routing, streaming, cost and audit still cross the Governance Shell.

The latest design correction is important: ai-orch does not need access to every developer's repository. The developer's chosen tool should keep repo access and local editing. The organisation can instead point OpenCode, Cline, VS Code/Copilot, Claude Code, Codex or a workbench at the Governance Shell as the model endpoint and MCP/tool gateway. That is the cleaner scale path for many developers: centralise governance and model access, not source-code access.

This README is also a working view of the current thinking. The operational runbook lives in [docs/deployment.md](ai-agent-orch/docs/deployment.md); this file is meant to explain what is being investigated, what is influencing the design, and what the POC is deliberately avoiding.

## What This Is

This POC explores whether a lightweight Governance Shell can provide:

- agent and workflow registration;
- model routing through approved aliases;
- policy checks before execution;
- secret scanning and classification ceilings;
- tool and MCP boundaries;
- staged patch review;
- cost and value tracking;
- audit and evidence records;
- human gates for risky actions;
- outputs that can feed an engineering maturity or governance reporting layer.

The system has an agent plane, but the agent plane is not the product. The product idea is the governance boundary around agentic engineering work.

## Why This Exists

AI-assisted development is moving quickly, but the runtime landscape is fragmented. Some workflows will happen inside IDE-native agents. Some will happen through CLIs. Some will be autonomous. Some teams will build their own wrappers.

That is useful, but it creates a governance problem:

- Which use case was this work tied to?
- Which model was used?
- Which tools and data sources were available?
- Which permissions were active?
- What did the agent propose?
- Was the output tested, reviewed, approved, or rejected?
- What did it cost compared with the human baseline?
- Can the audit trail be trusted later?

This repo is an attempt to answer those questions without rebuilding the whole developer workspace.

## Current State

Current version: `v0.18.5-beta`.

This is a **local beta** for the Governance Shell vertical slice. It is useful for team-local evaluation and demos, but it is not a production deployment.

Honest read as of 2026-06-11:

- The core control-plane shape is now real: sessions, policy gates, model routing, patch decisions, cost metadata, audit records, and the local Governance UI all run in the beta stack.
- The strongest implemented path is OpenCode or a similar OpenAI-compatible client pointing at ai-orch, with ai-orch owning the model endpoint and Bifrost sitting behind it as provider plumbing.
- Per-user Copilot is now an actor-bound route rather than a global shared model secret. `ai-orch/coding-gpt55` prefers that route when the developer is enrolled and falls back to the approved Bifrost/OpenRouter route.
- The agent story is no longer "start every session as unit-tests". OpenCode now starts through a low-reasoning `governance-lead` and delegates to known specialists when the work is clear enough.
- The UI is good enough to inspect a local beta run, audit trail, gateway posture and session ledger. It is not yet an operator console for a shared service.
- The MCP direction is sensible, but client-specific MCP setup for Cline, Copilot, Claude Code, Codex and Cursor is still adapter work, not a finished rollout path.
- State is still mostly local SQLite plus process-local pieces. That is fine for the POC, but production needs durable multi-instance audit-chain handling, stronger identity, secret management, release automation and operational controls.
- The repo should be read as a public POC with working beta paths and visible open questions, not as a packaged enterprise product.

What exists today:

- Go Governance Shell and Orchestrator services.
- Agent catalogue using `agent.md` plus `agent.config.yaml`.
- Selectable model backend owned by the Governance Shell: Bifrost OSS by default in Compose, with per-user Copilot as the actor-bound alternate.
- Model compatibility gateway MVP for OpenAI-compatible `/v1/models`, `/v1/chat/completions`, `/v1/responses`, and streaming calls.
- Bifrost OSS sidecar for provider plumbing across OpenRouter, OpenAI, Anthropic and direct DeepSeek today, with optional Bedrock, Vertex, Azure, Ollama or vLLM-style routes later.
- Separate developer, admin, service and runtime-token boundaries for local testing.
- Local CLI smoke path.
- VS Code Bridge scaffold with first-run setup, connection checks, bounded workspace-context packaging, and tested workflow helpers. This is useful, but optional.
- Session creation, first-class governed run creation, ownership checks, routing, confirmation, SSE events, and patch decisions.
- Staged patch buffer so raw patch content is fetched through a governed endpoint.
- Audit events with local tamper-evident hash chaining.
- Explicit run permission labels: `read_only`, `reviewed`, `auto_apply`, and `full_access`, with approval labels such as `manual` and `yolo` recorded for reporting.
- SQLite-backed local audit, session, registry, and model-pricing storage.
- Scheduled OpenRouter model-pricing refresh into a durable `model_pricing` table, used to estimate session cost when provider-reported cost is absent but token counts are present.
- Use-case, workflow, context-manifest, cache-outcome, evidence, and maturity-export APIs.
- Simple Governance UI served by the Governance Shell at `/ui/`, covering service posture, gateway selection, metrics, agents, evidence, maturity exports, audit lookup, and basic use-case/workflow registration.
- Recent-session and audit-trail UI backed by `GET /v1/sessions` plus `/v1/audit/sessions/{id}`, including readable runtime mode labels, model attribution, token counts and cost source, so a demo can show governed activity without pasting session IDs by hand.
- MCP proxy with session-bound tool authorisation, policy-filtered tool catalogues, credential-safe forwarding, and `oauth-user` fail-closed behaviour.
- Local MCP gateway CLI scaffold with stdio client config generation, fail-closed local HTTP transport, and `start_governed_run` for MCP clients.
- Audit trust labels for gateway-enforced, managed-client and self-reported activity.
- Command allow-list and tool-loop cap enforcement.
- Docker Compose local runner, Bifrost sidecar and direct OpenRouter provider-health smoke tooling.
- OpenCode config install tooling and local E2E smoke for testing the governed provider endpoint without putting provider keys in OpenCode.
- Governed OpenCode launcher defaults to a read-only, low-reasoning `governance-lead` primary agent, starts on the `ai-orch/coding-gpt55` capability alias, and records the routed specialist separately when work is delegated.
- Beta verification path: `scripts/beta-verify.sh`, CIO demo verification path `scripts/cio-demo-verify.sh`, Compose profile `beta`, offline router golden-case tests, and frozen API contract in `docs/api-contract-v1.md`.
- Governed-run Compose smoke without provider API keys (`AI_ORCH_BETA_SMOKE` uses EchoRuntime for CI and local beta checks).

### Beta quick start

From `ai-agent-orch/`:

```sh
./scripts/beta-verify.sh
```

For a CIO walkthrough, run the demo verifier instead. It performs the same local governed vertical-slice proof and leaves the UI running:

```sh
./scripts/cio-demo-verify.sh
```

Or with Docker only:

```sh
docker compose -f docker-compose.yml -f docker-compose.beta.yml --profile beta up -d bifrost orchestrator governance-shell
docker compose -f docker-compose.yml -f docker-compose.beta.yml --profile beta run --rm beta-smoke
```

Default Compose already uses SQLite (`audit.db`) for audit, sessions, registry and model-pricing data when `AI_ORCH_AUDIT_PATH` ends with `.db`.

Provider-backed beta (optional, requires `OPENROUTER_API_KEY`):

```sh
docker compose -f docker-compose.yml -f docker-compose.provider.yml --profile provider run --rm provider-gateway-smoke
```

Nightly CI runs gateway + governed-run provider smoke when the repository secret is configured.

Team-local OIDC: use `docker-compose.team-beta.yml` with `OIDC_ISSUER_URL` and `OIDC_CLIENT_ID`.

OpenCode local install path:

```sh
cd ai-agent-orch
./scripts/install-opencode-ai-orch.sh --scope global
mkdir -p /tmp/ai-orch-opencode-e2e
AI_ORCH_GOVERNANCE_URL=http://127.0.0.1:18080 \
AI_ORCH_MODEL_GATEWAY_URL=http://127.0.0.1:18082 \
AI_ORCH_DEV_TOKEN=local-dev \
AI_ORCH_RUNTIME_TOKEN=local-runtime-token \
go run ./cmd/ai-orch opencode e2e --dir /tmp/ai-orch-opencode-e2e
```

Copilot-backed local enrollment for governed OpenCode:

```sh
cd ai-agent-orch
scripts/enroll-developer-copilot-opencode.sh
```

Windows PowerShell:

```powershell
cd ai-agent-orch
.\scripts\enroll-developer-copilot-opencode.ps1
```

The developer completes GitHub device auth on their own machine. ai-orch stores the Copilot credential encrypted for that actor. OpenCode config receives only the ai-orch provider plus session headers; it does not store the Copilot credential.

For repo operators, local settings can live in `.env.dev` at the repository root. Start from `.env.example`; `.env.dev` is machine-local and ignored by git. Application developers do not need this file after enrollment because the OpenCode provider config contains the ai-orch URL, runtime token, actor subject and classification header.

Direct OpenCode or T3 Code launch works after enrollment when these environment variables are set:

```sh
export AI_ORCH_RUNTIME_TOKEN=local-runtime-token
export AI_ORCH_ACTOR_SUBJECT=$(whoami)
export AI_ORCH_INTENT="local model-only exploration"
opencode .
```

PowerShell:

```powershell
$env:AI_ORCH_RUNTIME_TOKEN = "local-runtime-token"
$env:AI_ORCH_ACTOR_SUBJECT = $env:USERNAME
$env:AI_ORCH_INTENT = "local model-only exploration"
opencode .
```

If OpenCode/T3 does not provide `AI_ORCH_SESSION_ID` and `AI_ORCH_SESSION_TOKEN`, the model gateway creates an auto session on the first model call and records audit against that actor. This is the ergonomic default for local beta. `AI_ORCH_INTENT` is optional for direct launches, but it is the right place to capture why a developer chose a model-only path.

Launch governed OpenCode through ai-orch so the session headers are created automatically:

```sh
cd ai-agent-orch
scripts/opencode-governed.sh -- run "Write a small test"
```

Windows PowerShell:

```powershell
cd ai-agent-orch
.\scripts\opencode-governed.ps1 run "Write a small test"
```

The wrapper creates a governed run with `agent=governance-lead`, receives `gateway_token`, exports `AI_ORCH_SESSION_ID` and `AI_ORCH_SESSION_TOKEN` for the child OpenCode process, then starts OpenCode with `--agent governance-lead` and `--model ai-orch/coding-gpt55` unless the user already chose a model. The `coding-gpt55` alias prefers the actor's enrolled Copilot credential when available and falls back to the approved Bifrost/OpenRouter route. The ledger records both the lead and the selected specialist as `routed_agent` when delegation occurs. Developers do not copy those values manually.

Developers who deliberately want a governed model-only session can use an explicit intent reason:

```sh
cd ai-agent-orch
scripts/opencode-governed.sh --model-only --governance-intent "Need direct model exploration before choosing an agent" -- run --model ai-orch/coding-gpt55 "Compare two approaches"
```

Known gaps before V1 or production:

- durable multi-instance audit-chain state;
- dedicated team registry storage or Postgres option;
- real user OAuth acquisition for user-scoped MCPs;
- broader CLI coverage for admin and CI workflows;
- richer governed MCP gateway ergonomics and client-specific setup for CLine, Copilot, Claude Code, Codex, Cursor and similar clients;
- fuller model compatibility behaviour beyond the current MVP, especially provider-specific streaming and responses compatibility gaps;
- live Bedrock checks through Bifrost once credentials are deliberately configured;
- stronger skills/config factory support that makes those clients route meaningful work through the governed path by default without overwriting existing project files unexpectedly;
- a Governance Router that selects model tier by task, risk, workflow, cost and evidence needs;
- a GitHub App spike that attaches governed sessions to issues, PRs, checks and review evidence;
- broader local OpenCode rollout hardening across managed global config, project config, and MCP tools;
- richer VS Code Bridge chat/tool-loop ergonomics beyond the current active-file or selection context;
- a CLine-style IDE agent experience or adapter path behind the Governance Shell;
- a fuller governance UI with team reporting, workflow review queues, and operational controls.

These are not theoretical polish items. They are the line between a useful local beta and something a team could safely run as shared infrastructure.

One design point I want to keep explicit: trust labels are reporting labels, not permission knobs. `gateway_enforced`, `managed_client` and `self_reported` should describe how the work actually ran. They should not become an allow-list that tells a developer which client they are allowed to use.

## What This Is Not

This repo is not trying to become:

- a Claude Code, OpenCode, Cursor, Aider, or IDE-agent replacement;
- a new autonomous agent product;
- a central source-code access service that can browse every developer repository;
- a Backstage, Jira, ServiceNow, or documentation portal clone;
- a model gateway competing with Bifrost, OpenRouter, LiteLLM, or provider-native gateways;
- an enterprise identity, secrets, or device-management system;
- a production deployment template.

The platform should borrow useful runtime ideas, not absorb every runtime into itself.

## Abstraction Tension

This is the messy part of the POC, and I want the README to be honest about it.

There are a few plausible surfaces:

- a VS Code Bridge, because VS Code and Copilot are already where a lot of engineering work happens;
- an MCP gateway, because it can be used by more than one agent client;
- a CLI, because smoke tests, CI hooks and admin operations should not require an IDE;
- a GitHub App, because issues, PRs, checks and reviews are where software delivery becomes visible;
- runtime adapters, because existing coding agents already have better chat, file, terminal and tool-loop ergonomics than this POC should try to rebuild;
- a governance UI, because reporting, evidence, use cases and maturity outputs should not live only inside an IDE.

The current answer is not to choose one surface too early. The current answer is to keep the Governance Shell strict and build thin adapters around it.

The first build priority is therefore not the workbench. It is the plumbing that every workbench, IDE, CLI or runtime would have to trust:

- session identity and ownership;
- policy decisions that fail closed;
- model calls through the Governance Shell, with Bifrost or per-user Copilot used as provider plumbing when selected;
- OpenAI-compatible runtime model calls through a model compatibility gateway;
- MCP tool calls through a gateway;
- patch content through the staged buffer;
- audit records that distinguish enforced activity from self-reported activity;
- cost, cache and evidence records that do not depend on one client UI.

That means MCP is attractive, but MCP is not magic governance. MCP can expose prompts, resources and tools to clients. It can make the right behaviour easier. It cannot audit native tool calls that never route through it. So the real split is:

- skills, prompts and generated config make governed behaviour easy;
- the MCP Tool Gateway enforces policy for tool calls that route through it;
- managed IDE, endpoint or CI policy is needed later if bypass resistance matters;
- audit records must distinguish gateway-enforced activity from self-reported activity.

That distinction matters because otherwise this POC could accidentally become another agent wrapper with prettier logs. The thing I am trying to prove is stronger than that: a reusable governance boundary around whatever agent plane teams actually adopt.

The best mental model right now is:

- MCP captures the agent-tool boundary;
- GitHub captures the delivery boundary;
- the VS Code Bridge or a future workbench improves developer ergonomics;
- the Governance Shell remains the authority.

## Core Shape

The current abstraction is deliberately split:

- `agent.md` contains human and model-readable agent instructions.
- `agent.config.yaml` contains executable configuration, tool access, model aliases, and governance limits.
- `models/registry.yaml` maps stable model aliases to concrete provider model IDs.
- MCP registrations declare external context/tool surfaces.
- The Governance Shell owns policy, audit, model proxying, patch buffering, model-backend selection, and session authority.
- The Orchestrator owns catalogue validation, routing, and runtime dispatch.

That separation is the important part. It keeps governance strict while allowing the runtime layer to stay flexible.

## Current Thinking

The current direction is to build the governance/control plane first, and keep the agent plane deliberately thin. The system can run agents, but it should not become another general-purpose coding-agent product.

The VS Code Bridge is therefore not meant to be a CLine clone in its current form, and it should not be required for the governance story to work. It should work from any VS Code project folder, but the current context model is deliberately bounded: workspace name, git branch and remote where available, parsed branch work-item hints, active file metadata, and either the current selection or a capped active-file excerpt. CLine-style behaviour belongs either in a richer Bridge agent-plane experience or in an adapter to an existing IDE-native runtime, with this Governance Shell still owning policy, audit, model proxying, patch buffering, and approvals.

The patch story is still deliberately split:

- CLI smoke `applied` means a patch decision was recorded. It does not mutate the workspace.
- VS Code Bridge `Apply` means the Bridge fetched the buffered patch and changed the local workspace.
- OpenCode/ACP local editing can now produce governed model traffic and patch/diff evidence in the implemented runtime paths, but managed rollout hardening and client-specific setup are still beta work.

That distinction matters. The POC should only claim local-client editing where the runtime path can prove model calls, permissions, and patch evidence end to end.

The newer thought is that the next serious adapter should probably be `ai-orch-mcp`: a governed MCP gateway plus a small skills/config factory. The gateway would expose governed tools and delegation into CLine, Copilot, Claude Code, Codex, Cursor and similar clients. The skills factory would generate the client-specific instructions and MCP config that nudge those clients to start a governed session, attach a use case, delegate substantial work, submit patches through the buffer and record evidence.

The OpenCode runtime problem adds a second gateway shape: model compatibility. Tool calls should go through `ai-orch-mcp`; model calls should go through an OpenAI-compatible `ai-orch` model endpoint. That endpoint can look boring from the runtime side, but internally it must resolve aliases, apply the Governance Router, select the model backend, stream responses where needed and audit the decision.

This is why the provider endpoint lane now feels more important than a hosted runtime lane. If a managed organisation can set OpenCode, Cline or a workbench to use `https://ai-orch.example/v1` as its provider endpoint, ai-orch can govern model choice, usage, cost and audit without needing to touch the repo. MCP and local evidence reporting then add tool and patch evidence where the client supports it.

That still does not make the system a CLine clone. The local client can keep doing lightweight navigation and conversation. When the work becomes meaningful, expensive, risky or evidence-worthy, it should cross the Governance Shell boundary.

The GitHub App idea feels useful after MCP, not before it. GitHub or Azure DevOps can become the delivery evidence surface, but only once the Governance Shell has a clean answer for session identity, tool routing, model routing, patch decisions and audit evidence. A governed session attached to an issue or PR is more useful than a local IDE-only audit trail, but the session itself has to be trustworthy first.

The practical shape I want to prove is:

- register use cases and workflows before execution;
- keep model/provider details behind stable aliases;
- keep provider, MCP, and OAuth secrets out of the runtime;
- stage writes and patch content before they reach the IDE;
- make every meaningful policy decision auditable;
- attach evidence, cost, and value signals to sessions;
- emit clean records that a separate engineering governance or maturity system can consume.

The key design tension is context. The IDE or CLI should not fill the model window with every possible governance fact. It should send lightweight identifiers, intent, and bounded source context. The Governance Shell should resolve use-case records, workflow policy, context manifests, cache eligibility, model routing, evidence expectations, and audit metadata behind the boundary.

The cost model also needs to stay honest. Human delivery cost is usually sized in days, rates, story points, or review effort. LLM cost is usage-based and can be tiny per call but expensive at scale or under retries. This POC should record both: model/tool/runtime cost on one side, and estimated human baseline plus review/verification effort on the other.

There is also a useful cost-control shape here: let developer-side agents use smaller or cheaper models for local steering, then delegate real implementation, review, testing or patch generation to the Governance Shell. The Governance Shell can choose the approved stronger model, reuse cached context where appropriate and record the cost/value evidence centrally.

Factory Router is interesting because it validates that model routing is becoming its own layer. I do not want to build a generic model gateway, but I do want this POC to route by governance context: task type, risk level, workflow stage, classification, cost sensitivity, latency sensitivity, evidence needs and provider health. That is more useful than a developer manually choosing an expensive model for every task.

Bifrost is useful in a different way. It is good open-source provider plumbing: OpenAI-compatible routing, provider translation, streaming, retries and multiple backend families. The important thing is not to confuse that with the governance plane. In this POC, Bifrost sits behind ai-orch. Runtimes do not call Bifrost directly, Bifrost does not own session authority, Bifrost content logging is disabled locally, and Bifrost governance or enterprise features are not the thing being proved here. The current local smoke setup proves OpenRouter, direct OpenAI, direct Anthropic and direct DeepSeek routes through the Governance Shell.

## External Work I Am Watching

This POC is not being built in isolation. The point is to borrow useful ideas without letting this repo become a wrapper around somebody else's agent stack.

[Microsoft Agent Governance Toolkit](https://github.com/microsoft/agent-governance-toolkit) is the closest governance-shaped reference right now. [Microsoft's launch post](https://opensource.microsoft.com/blog/2026/04/02/introducing-the-agent-governance-toolkit-open-source-runtime-security-for-ai-agents/) describes AGT as an open-source, MIT-licensed runtime security governance project for autonomous agents, designed to work with existing frameworks rather than replace them. That is very close to the boundary this POC is trying to prove: policy before action, auditability, identity, and controlled tool execution.

My current read on AGT:

- track it closely as a policy-engine and MCP-governance reference;
- keep the native policy engine as the default until this POC proves its own contract;
- treat an AGT adapter as a spike, not a mandatory dependency;
- do not replace `agent.md`, `agent.config.yaml`, model aliases, patch buffering, or the audit envelope with AGT-specific structure too early.

[GitHub's Agentic Workflows security architecture](https://github.blog/ai-and-ml/generative-ai/under-the-hood-security-architecture-of-github-agentic-workflows/) is relevant because it validates several design choices already in this repo: zero-secret agent execution, model calls through a proxy, MCP access through a trusted gateway, explicit workflow stages, and staged writes.

The Microsoft write-up on [securing MCP with a control plane](https://developer.microsoft.com/blog/securing-mcp-a-control-plane-for-agent-tool-execution) is also important. MCP standardises tool discovery and invocation, but it does not provide the policy checkpoint by itself. That supports this repo's direction: MCP registrations are useful, but credentialed or risky calls need to go through the Governance Shell.

[Factory Router](https://factory.ai/news/factory-router) is interesting for the model-routing question. I am not reading it as something to copy; I am reading it as proof that "which model should do this work?" is becoming a decision layer in its own right. This POC should make that decision auditable and policy-aware.

[Bifrost](https://github.com/maximhq/bifrost) is interesting for the provider-plumbing question. It already does the OpenAI-compatible gateway work across provider families. That makes it a good sidecar candidate behind the Governance Shell, as long as this repo keeps policy, session identity, audit, patch buffering, evidence, and model-routing decisions in ai-orch.

The [GitHub Copilot app preview](https://github.com/features/preview/github-app) is interesting for the delivery-surface question. It points at an issue-to-merge workbench rather than just an IDE extension. That matters because governance has to show up where code is reviewed and merged, not only where prompts are typed.

[t3code](https://github.com/pingdotgg/t3code) is interesting as a workbench UX reference, especially because it already has source-control provider ideas around GitHub, GitLab, Bitbucket and Azure DevOps. That makes it a stronger reference than a plain chat UI. Still, I do not want this repo to become a fork or clone of that shape. The lesson is ergonomics and source-control workflow, not governance architecture.

The takeaway from these references is not "adopt this whole stack". The takeaway is that runtime governance is becoming a recognisable layer of its own. This POC is an attempt to make that layer concrete for engineering workflows, while staying honest that the adapter layer is still in flux.

## Design Questions Still Open

- Should the policy engine remain native long-term, or should AGT become a supported adapter once the local contract is stable?
- Should the next primary adapter be `ai-orch-mcp`, with the VS Code Bridge kept as an optional first-party experience?
- Should the next major integration after MCP be a GitHub App, because the merge/review boundary may be a stronger governance anchor than the IDE?
- What should the Governance Router decide from: task type, workflow stage, risk, classification, evidence needs, cost, latency, provider health, or all of the above?
- What is the minimum OpenAI-compatible model surface needed for OpenCode: chat completions first, Responses API first, or both behind one gateway?
- How far can generated skills, prompts and MCP config take adoption before managed IDE or endpoint policy is required?
- How should the audit model label gateway-enforced activity versus self-reported native tool activity?
- Can a managed provider endpoint plus MCP config give enough governance coverage for OpenCode, Cline and workbench-style clients before we build any hosted runtime plane?
- Where is the right local isolation line: direct workspace, disposable worktree, container-per-session, or all three depending on workflow risk?
- What belongs in the governance/control plane UI, and what should stay in the IDE or CLI?
- How much context should be cached per session before the cache becomes a hidden memory product?
- Which maturity outputs are essential enough to be first-class API records, and which should remain derived reporting views?
- How should cost and value be shown without pretending token cost maps directly to engineering effort?

## Governance Outputs

The system should emit machine-readable facts that another maturity or reporting layer can consume. It should not become that reporting layer itself.

The important output classes are:

- session summary;
- policy and control outcomes;
- cost and value sizing;
- context provenance;
- evidence records;
- outcome metrics;
- cache outcomes;
- audit and patch-decision records.

The IDE and CLI should send lightweight IDs and intent. The Governance Shell should resolve context, policy, provenance, evidence, and cost records behind the boundary.

## Documentation Map

- [docs/deployment.md](ai-agent-orch/docs/deployment.md): how to run, verify, smoke test, and use the local POC.
- [Agent catalogue guide](ai-agent-orch/agents/README.md): how agents are structured and validated.
- [Model registry guide](ai-agent-orch/models/README.md): model alias rules and provider strategy.
- [Model compatibility gateway](ai-agent-orch/docs/model-compatibility-gateway.md): why OpenCode-style runtimes should call governed OpenAI-compatible endpoints rather than provider APIs directly.
- [MCP registration guide](ai-agent-orch/mcp/README.md): MCP auth modes, stubs, and fail-closed rules.
- [Policy guide](ai-agent-orch/policies/README.md): command allow-lists, classification, secrets, and cost controls.
- [Local state lifecycle](ai-agent-orch/docs/local-state-lifecycle.md): what is durable, what is process-local, and what must be promoted later.
- [Governance insight and memory direction](ai-agent-orch/docs/governance-insight-and-memory.md): SQLite-first reporting, FTS5 before vectors, and memory as a governed projection.
- [Governed IDE agent plane plan](ai-agent-orch/docs/governed-ide-agent-plane-plan.md): how the VS Code Bridge can move closer to CLine-style ergonomics without replacing the Governance Shell boundary.
- [Runtime client integration strategy](ai-agent-orch/docs/runtime-client-integration.md): how OpenCode, Cline, Copilot, Claude Code, Codex and workbench-style clients can point at ai-orch without centralising repo access.
- [changelog.md](changelog.md): versioned change history.

## Guiding Principle

Do not mix runtime structure with governance structure too early.

The system should stay small, boring, and strict at the boundary. The runtime can remain flexible behind that boundary.

That is the point of this POC.

## License

This project is licensed under the Apache License 2.0.

If you use, copy, modify, distribute, or build on this work, retain the attribution in [NOTICE](NOTICE) or otherwise provide clear credit to the project author.
