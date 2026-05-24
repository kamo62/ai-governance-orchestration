# AI Agent Orchestration POC

This repository is my proof of concept for an agent orchestration system that I am currently investigating.

The goal is not to rebuild every agent runtime, IDE workflow, or coding assistant stack inside this project. The goal is to test whether a lightweight system layer can give agent work better governance, routing, auditability, and policy control without forcing every team or engineer into one runtime.

Project state: this is a personal-time POC in active early development. The current backbone includes session creation, audit events, policy gates, audit lookup, router selection, Docker Compose, catalogue validation, OpenRouter smoke tooling, a local CLI scaffold, MCP stubs, a VS Code Bridge scaffold, service-token hardening, and the first dispatch/SSE path. A real patch-producing agent flow is next. Not production-ready.

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

## Current POC Direction

This repo currently focuses on the system-side foundation:

- agent definition standard
- temporary agent catalogue
- model registry
- Governance Shell scaffold
- Orchestrator scaffold
- local Docker Compose workflow
- OpenRouter-backed model smoke testing
- local CLI and VS Code Bridge scaffolds
- MCP registration and token-guarded stub services
- first dispatch and SSE event path
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

OpenRouter smoke testing requires `OPENROUTER_API_KEY`:

```sh
OPENROUTER_API_KEY=... docker compose --profile tools run --rm openrouter-smoke
```

The `ai-orch` CLI scaffold currently covers local smoke tests, audit lookup, kill-switch checks, session commands, and agent listing:

```sh
AI_ORCH_DEV_TOKEN=local-dev docker compose --profile tools run --rm ai-orch
```

## Guiding Principle

Do not mix runtime structure with governance structure too early.

The system should stay small, boring, and strict at the boundary. The runtime can remain flexible behind that boundary.

That is the point of this POC.

## License

This project is licensed under the Apache License 2.0.

If you use, copy, modify, distribute, or build on this work, retain the attribution in [NOTICE](NOTICE) or otherwise provide clear credit to the project author.
