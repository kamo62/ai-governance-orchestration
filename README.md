# AI Agent Orchestration POC

This repository is a personal-time proof of concept for a governance platform for AI-assisted engineering work.

The bet is simple: teams will use different agents, IDEs, CLIs, model providers, and workflow tools. Trying to replace all of that with one new agent runtime is the wrong move. The useful layer is a small, strict control plane that can sit in front of those runtimes and make agent work governable.

The part I am still working through is the correct abstraction layer. Is the first-class surface a VS Code extension, an MCP gateway, a runtime adapter, a CLI, or some combination of those? My current read is that the Governance Shell is the product-shaped boundary, MCP is probably the best interoperability layer, and the VS Code Bridge is useful as a first-party experience but should not become the whole strategy.

The newer wrinkle is that the developer integration surface may not be one thing at all. MCP feels like the agent-tool boundary. GitHub feels like the delivery boundary. A local workbench or VS Code Bridge might be the ergonomic layer. I am trying not to confuse those three.

The current practical priority is simpler than the long-term architecture: get the Governance Shell and MCP plumbing right first. The workbench, GitHub/Azure DevOps app layer and Kubernetes runtime pool can wait until the policy, audit, routing and tool-gateway contracts are boring enough to trust.

There is one more piece of plumbing in that first bucket: a small model compatibility gateway. OpenCode and similar runtimes need OpenAI-compatible model endpoints. They should call an `ai-orch` endpoint, not OpenRouter or provider APIs directly, so model routing, streaming, cost and audit still cross the Governance Shell.

This README is also a working view of the current thinking. The operational runbook lives in [deployment.md](deployment.md); this file is meant to explain what is being investigated, what is influencing the design, and what the POC is deliberately avoiding.

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

Current version: `v0.6.0-alpha`.

This is an early local POC. It is not production-ready.

What exists today:

- Go Governance Shell and Orchestrator services.
- Agent catalogue using `agent.md` plus `agent.config.yaml`.
- OpenRouter model proxy owned by the Governance Shell.
- Model compatibility gateway MVP for OpenAI-compatible `/v1/models`, `/v1/chat/completions`, `/v1/responses`, and streaming calls.
- Separate developer, admin, service and runtime-token boundaries for local testing.
- Local CLI smoke path.
- VS Code Bridge scaffold with first-run setup, connection checks, bounded workspace-context packaging, and tested workflow helpers. This is useful, but optional.
- Session creation, ownership checks, routing, confirmation, SSE events, and patch decisions.
- Staged patch buffer so raw patch content is fetched through a governed endpoint.
- Audit events with local tamper-evident hash chaining.
- SQLite-backed local audit, session, and registry storage.
- Use-case, workflow, context-manifest, cache-outcome, evidence, and maturity-export APIs.
- MCP proxy scaffolding with `oauth-user` fail-closed behaviour.
- Local MCP gateway CLI scaffold with stdio client config generation and fail-closed local HTTP transport.
- Command allow-list and tool-loop cap enforcement.
- Docker Compose local runner and OpenRouter smoke tooling.

Still pending:

- real OpenCode patch-producing flow;
- durable multi-instance audit-chain state;
- dedicated team registry storage or Postgres option;
- real user OAuth acquisition for user-scoped MCPs;
- broader CLI coverage for admin and CI workflows;
- richer governed MCP gateway coverage for CLine, Copilot, Claude Code, Codex, Cursor and similar clients;
- fuller model compatibility behaviour beyond the current MVP, especially provider-specific streaming and responses compatibility gaps;
- stronger skills/config factory support that makes those clients route meaningful work through the governed path by default without overwriting existing project files unexpectedly;
- a Governance Router that selects model tier by task, risk, workflow, cost and evidence needs;
- a GitHub App spike that attaches governed sessions to issues, PRs, checks and review evidence;
- a local OpenCode sandbox adapter only after the governance and MCP contracts are stable enough to supervise it;
- richer VS Code Bridge chat/tool-loop ergonomics beyond the current active-file or selection context;
- a CLine-style IDE agent experience or adapter path behind the Governance Shell;
- a governance UI, if the POC proves the control-plane shape.

## What This Is Not

This repo is not trying to become:

- a Claude Code, OpenCode, Cursor, Aider, or IDE-agent replacement;
- a new autonomous agent product;
- a Backstage, Jira, ServiceNow, or documentation portal clone;
- a model gateway competing with OpenRouter, LiteLLM, or provider-native gateways;
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
- a governance UI later, because reporting, evidence, use cases and maturity outputs should not live inside an IDE.

The current answer is not to choose one surface too early. The current answer is to keep the Governance Shell strict and build thin adapters around it.

The first build priority is therefore not the workbench. It is the plumbing that every workbench, IDE, CLI or runtime would have to trust:

- session identity and ownership;
- policy decisions that fail closed;
- model calls through the Governance Shell;
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
- The Governance Shell owns policy, audit, model proxying, patch buffering, and session authority.
- The Orchestrator owns catalogue validation, routing, and runtime dispatch.

That separation is the important part. It keeps governance strict while allowing the runtime layer to stay flexible.

## Current Thinking

The current direction is to build the governance/control plane first, and keep the agent plane deliberately thin. The system can run agents, but it should not become another general-purpose coding-agent product.

The VS Code Bridge is therefore not meant to be a CLine clone in its current form, and it should not be required for the governance story to work. It should work from any VS Code project folder, but the current context model is deliberately bounded: workspace name, git branch and remote where available, active file metadata, and either the current selection or a capped active-file excerpt. CLine-style behaviour belongs either in a richer Bridge agent-plane experience or in an adapter to an existing IDE-native runtime, with this Governance Shell still owning policy, audit, model proxying, patch buffering, and approvals.

The newer thought is that the next serious adapter should probably be `ai-orch-mcp`: a governed MCP gateway plus a small skills/config factory. The gateway would expose governed tools and delegation into CLine, Copilot, Claude Code, Codex, Cursor and similar clients. The skills factory would generate the client-specific instructions and MCP config that nudge those clients to start a governed session, attach a use case, delegate substantial work, submit patches through the buffer and record evidence.

The OpenCode runtime problem adds a second gateway shape: model compatibility. Tool calls should go through `ai-orch-mcp`; model calls should go through an OpenAI-compatible `ai-orch` model endpoint. That endpoint can look boring from the runtime side, but internally it must resolve aliases, apply the Governance Router, call the model proxy, stream responses where needed and audit the decision.

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
- What is the minimum local OpenCode sandbox shape needed to prove supervision without prematurely building the EKS/AKS runtime plane?
- Where is the right local isolation line: simple subprocess/CLI execution, container-per-session, or both depending on workflow risk?
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

- [deployment.md](deployment.md): how to run, verify, smoke test, and use the local POC.
- [Agent catalogue guide](ai-agent-orch/agents/README.md): how agents are structured and validated.
- [Model registry guide](ai-agent-orch/models/README.md): model alias rules and provider strategy.
- [Model compatibility gateway](ai-agent-orch/docs/model-compatibility-gateway.md): why OpenCode-style runtimes should call governed OpenAI-compatible endpoints rather than provider APIs directly.
- [MCP registration guide](ai-agent-orch/mcp/README.md): MCP auth modes, stubs, and fail-closed rules.
- [Policy guide](ai-agent-orch/policies/README.md): command allow-lists, classification, secrets, and cost controls.
- [Local state lifecycle](ai-agent-orch/docs/local-state-lifecycle.md): what is durable, what is process-local, and what must be promoted later.
- [Governance insight and memory direction](ai-agent-orch/docs/governance-insight-and-memory.md): SQLite-first reporting, FTS5 before vectors, and memory as a governed projection.
- [Governed IDE agent plane plan](ai-agent-orch/docs/governed-ide-agent-plane-plan.md): how the VS Code Bridge can move closer to CLine-style ergonomics without replacing the Governance Shell boundary.
- [changelog.md](changelog.md): versioned change history.

## Guiding Principle

Do not mix runtime structure with governance structure too early.

The system should stay small, boring, and strict at the boundary. The runtime can remain flexible behind that boundary.

That is the point of this POC.

## License

This project is licensed under the Apache License 2.0.

If you use, copy, modify, distribute, or build on this work, retain the attribution in [NOTICE](NOTICE) or otherwise provide clear credit to the project author.
