# AI Agent Orchestration POC

This repository is a personal-time proof of concept for a governance platform for AI-assisted engineering work.

The bet is simple: teams will use different agents, IDEs, CLIs, model providers, and workflow tools. Trying to replace all of that with one new agent runtime is the wrong move. The useful layer is a small, strict control plane that can sit in front of those runtimes and make agent work governable.

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
- Local CLI smoke path.
- VS Code Bridge scaffold.
- Session creation, ownership checks, routing, confirmation, SSE events, and patch decisions.
- Staged patch buffer so raw patch content is fetched through a governed endpoint.
- Audit events with local tamper-evident hash chaining.
- SQLite-backed local audit, session, and registry storage.
- Use-case, workflow, context-manifest, cache-outcome, evidence, and maturity-export APIs.
- MCP proxy scaffolding with `oauth-user` fail-closed behaviour.
- Command allow-list and tool-loop cap enforcement.
- YAML-driven native policy engine for classification, secrets, cost caps, and SDLC workflow evidence expectations.
- Docker Compose local runner and OpenRouter smoke tooling.
- GitHub Actions CI lane for Go checks, catalogue validation, and vulnerability scanning.

Still pending:

- real OpenCode patch-producing flow;
- durable multi-instance audit-chain state;
- dedicated team registry storage or Postgres option;
- real user OAuth acquisition for user-scoped MCPs;
- broader CLI coverage for admin and CI workflows;
- manual VS Code Bridge validation;
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

The practical shape I want to prove is:

- register use cases and workflows before execution;
- keep model/provider details behind stable aliases;
- keep provider, MCP, and OAuth secrets out of the runtime;
- stage writes and patch content before they reach the IDE;
- make every meaningful policy decision auditable;
- keep security, cost, and SDLC workflow policies in loadable policy files;
- attach evidence, cost, and value signals to sessions;
- emit clean records that a separate engineering governance or maturity system can consume.

The key design tension is context. The IDE or CLI should not fill the model window with every possible governance fact. It should send lightweight identifiers, intent, and bounded source context. The Governance Shell should resolve use-case records, workflow policy, context manifests, cache eligibility, model routing, evidence expectations, and audit metadata behind the boundary.

The cost model also needs to stay honest. Human delivery cost is usually sized in days, rates, story points, or review effort. LLM cost is usage-based and can be tiny per call but expensive at scale or under retries. This POC should record both: model/tool/runtime cost on one side, and estimated human baseline plus review/verification effort on the other.

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

The takeaway from these references is not "adopt this whole stack". The takeaway is that runtime governance is becoming a recognisable layer of its own. This POC is an attempt to make that layer concrete for engineering workflows.

## Design Questions Still Open

- Should the policy engine remain native long term, or should AGT become a supported adapter once the local contract is stable?
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
- [MCP registration guide](ai-agent-orch/mcp/README.md): MCP auth modes, stubs, and fail-closed rules.
- [Policy guide](ai-agent-orch/policies/README.md): command allow-lists, classification, secrets, and cost controls.
- [Local state lifecycle](ai-agent-orch/docs/local-state-lifecycle.md): what is durable, what is process-local, and what must be promoted later.
- [Governance insight and memory direction](ai-agent-orch/docs/governance-insight-and-memory.md): SQLite-first reporting, FTS5 before vectors, and memory as a governed projection.
- [changelog.md](changelog.md): versioned change history.

## Guiding Principle

Do not mix runtime structure with governance structure too early.

The system should stay small, boring, and strict at the boundary. The runtime can remain flexible behind that boundary.

That is the point of this POC.

## License

This project is licensed under the Apache License 2.0.

If you use, copy, modify, distribute, or build on this work, retain the attribution in [NOTICE](NOTICE) or otherwise provide clear credit to the project author.
