# AI Agent Orchestration POC

This repository is a personal-time proof of concept for a governance platform for AI-assisted engineering work.

The bet is simple: teams will use different agents, IDEs, CLIs, model providers, and workflow tools. Trying to replace all of that with one new agent runtime is the wrong move. The useful layer is a small, strict control plane that can sit in front of those runtimes and make agent work governable.

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

Current version: `v0.5.1-alpha`.

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
- Docker Compose local runner and OpenRouter smoke tooling.

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

## Strategic References

This POC is being built while watching adjacent governance and runtime patterns.

Microsoft Agent Governance Toolkit is the closest governance-shaped reference. The current position is to track it as a possible policy-engine adapter or reference, not to adopt it as a hard dependency. The native policy boundary stays first until the POC proves its own contract.

GitHub's agentic workflow security architecture is also relevant because it reinforces the same principle: agents should not carry broad secrets, and writes should be staged, reviewed, and mediated.

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
