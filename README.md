# AI Agent Orchestration POC

AI-Orch is a proof of concept for governing AI-assisted engineering work without forcing every team into one coding agent, IDE, model provider or workflow.

The bet is simple: teams will keep using OpenCode, Cline, Copilot, Claude Code, Codex, Cursor, VS Code, CI and future agent tools. The useful product is a small, strict control plane that sits on the model, tool, patch and evidence boundary so the organisation can answer: who ran the work, why, through which model, with which permissions, at what cost, and with what audit evidence?

## What this is

AI-Orch is trying to provide:

- approved model routing through stable aliases;
- actor-bound runtime credentials and provider readiness checks;
- policy gates before risky work;
- MCP/tool boundaries where clients support them;
- session, cost, usage, patch and evidence records;
- a local Governance UI for beta inspection;
- a ledger that can later feed engineering governance and maturity reporting.

The agent catalogue exists, but the agent plane is not the product. The product idea is the governance boundary around agentic engineering work.

## Current status

Current version: v0.21.1-beta.

This is a working local/team beta, not a production deployment.

What is real now:

- Governance Shell and Orchestrator services run locally through Docker Compose.
- The OpenAI-compatible model gateway exposes /v1/models, /v1/chat/completions and /v1/responses.
- Bifrost is used as provider plumbing behind AI-Orch, with OpenRouter and other provider routes kept behind the Governance Shell.
- Actor-bound Copilot can be enrolled and used as a per-developer route instead of a shared global model secret.
- AI-Orch-routed OpenCode is the strongest current client path: OpenCode keeps local repo access, while model traffic, routing, token use, cost and session lifecycle cross AI-Orch.
- OpenCode setup and refresh tooling preserves existing providers such as Moonshot, DeepSeek, OpenRouter and Copilot Zen while updating only the ai-orch provider block and model list.
- SQLite-backed audit, session, registry and model-pricing storage works for the beta path.
- The local UI can inspect system posture, provider readiness, sessions, model attribution, token counts, costs, audit events, evidence and basic workflow records.
- A benchmark CLI exists for comparing enabled model routes by workflow, latency, tokens, cost and pass/fail evidence.
- Runtime dispatch now fails closed when no real runtime is available; EchoRuntime is limited to explicit beta smoke runs.

Important caveat: AI-Orch currently sees OpenCode model calls and model-emitted tool-call names, but it does not automatically see the full local OpenCode Task/Read/Edit/Bash transcript. That needs ACP, MCP or deliberate client-event forwarding.

## How it fits together

~~~mermaid
flowchart LR
    Dev["Developer tool: OpenCode, Cline, VS Code, Codex"] --> ModelGateway["AI-Orch Model Gateway: /v1"]
    Dev --> MCPGateway["AI-Orch MCP / tool gateway"]
    ModelGateway --> Shell["Governance Shell"]
    MCPGateway --> Shell
    Shell --> Router["Policy, session and model routing"]
    Router --> Copilot["Actor-bound Copilot"]
    Router --> Bifrost["Bifrost provider plumbing"]
    Bifrost --> Providers["OpenRouter, Azure, Bedrock, OpenAI, Anthropic, DeepSeek"]
    Shell --> Ledger["SQLite beta ledger: audit, sessions, cost, evidence"]
    Shell --> UI["Governance UI"]
~~~

The important boundary is that developer tools keep repository access and local editing. AI-Orch owns governance, model routing, runtime credentials, policy, audit, cost and evidence. Provider keys and Copilot credentials stay behind the server boundary.

For the deeper design, see [docs/architecture.md](ai-agent-orch/docs/architecture.md).

## Quick start

From ai-agent-orch/:

~~~sh
./scripts/beta-verify.sh
~~~

For a CIO/demo walkthrough that leaves the UI running:

~~~sh
./scripts/cio-demo-verify.sh
~~~

Then open the Governance UI printed by the script, usually:

~~~text
http://127.0.0.1:18081/ui/
~~~

The full runbook lives in [docs/deployment.md](ai-agent-orch/docs/deployment.md).

## Developer workflow

For a central team beta, developers should not need to run the AI-Orch Docker stack. The central server exposes:

- a Governance URL for enrolment, sessions, audit and UI calls;
- a Model Gateway URL for OpenAI-compatible model traffic.

A developer enrols OpenCode once:

~~~sh
cd ai-agent-orch
AI_ORCH_GOVERNANCE_URL=https://ai-orch.example.com \
AI_ORCH_MODEL_GATEWAY_URL=https://models.ai-orch.example.com \
AI_ORCH_DEV_TOKEN=<developer-enrollment-token-or-id-token> \
scripts/enroll-developer-copilot-opencode.sh
~~~

The flow completes GitHub/Copilot enrolment, stores the Copilot credential encrypted on the server, issues a revocable AI-Orch runtime credential, and installs only the ai-orch provider block into OpenCode. A refresh job can keep that block and model list current without wiping personal OpenCode config.

Manual compatible-client shape:

~~~text
Provider: OpenAI Compatible / Custom
Base URL: https://models.ai-orch.example.com/v1
API key:  <AI-Orch runtime credential>
Model:    coding-gpt55
~~~

More detail is in [docs/deployment.md](ai-agent-orch/docs/deployment.md) and [docs/runtime-client-integration.md](ai-agent-orch/docs/runtime-client-integration.md).

## What this is not

AI-Orch is not trying to become:

- a replacement for OpenCode, Cline, Copilot, Cursor, Claude Code, Codex or Aider;
- a central source-code access service that browses every developer repository;
- a generic model gateway competing with Bifrost, OpenRouter, LiteLLM or provider-native gateways;
- an enterprise identity, secrets or device-management product;
- a production deployment template yet.

It should stay small, boring and strict at the boundary.

## Documentation

- [Deployment and local run guide](ai-agent-orch/docs/deployment.md): how to run, verify, smoke test, enrol developers and recover local services.
- [Architecture](ai-agent-orch/docs/architecture.md): north star, component boundaries, data flow, readiness and open design questions.
- [API contract](ai-agent-orch/docs/api-contract-v1.md): frozen beta API surface for integrators.
- [Runtime client integration](ai-agent-orch/docs/runtime-client-integration.md): how OpenCode, Cline, Copilot, Claude Code, Codex and workbench-style clients fit without centralising repo access.
- [Model compatibility gateway](ai-agent-orch/docs/model-compatibility-gateway.md): OpenAI-compatible model gateway contract.
- [Local state lifecycle](ai-agent-orch/docs/local-state-lifecycle.md): what is durable today and what must be hardened.
- [Governance insight and memory direction](ai-agent-orch/docs/governance-insight-and-memory.md): SQLite-first reporting and future memory projection.
- [Agent catalogue guide](ai-agent-orch/agents/README.md): agent structure and validation.
- [Model registry guide](ai-agent-orch/models/README.md): model alias rules and provider strategy.
- [MCP registration guide](ai-agent-orch/mcp/README.md): MCP auth modes, stubs and fail-closed rules.
- [Policy guide](ai-agent-orch/policies/README.md): command allow-lists, classification, secrets and cost controls.
- [Production backlog](ai-agent-orch/docs/production-backlog.md): known hardening work before V1/production.
- [Changelog](changelog.md): versioned change history.

## License

This project is licensed under the Apache License 2.0.

If you use, copy, modify, distribute, or build on this work, retain the attribution in [NOTICE](NOTICE) or otherwise provide clear credit to the project author.
