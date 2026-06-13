# AI-Orch Architecture

This document carries the product-shape and architecture thinking that should not live in the root README. The README is the front door; this file explains the boundary.

## North star

AI-Orch is not meant to be a better single coding agent. The north star is a governance/control plane that lets teams keep using their preferred developer tools while routing meaningful model, tool, patch and evidence decisions through one enforceable boundary.

The target shape is:

- local tools keep repository access and developer ergonomics;
- AI-Orch owns session identity, policy, model routing, runtime credentials, audit, cost and evidence;
- Bifrost, OpenRouter, Copilot, Azure, Bedrock and other provider plumbing stay behind the Governance Shell rather than becoming the product;
- MCP becomes the governed tool boundary where clients support it;
- GitHub or Azure DevOps becomes the delivery evidence boundary once the local governance contract is reliable.

## Core boundary

~~~mermaid
flowchart TB
    Client["Developer client or runtime"] -->|"model calls"| ModelGateway["Model compatibility gateway"]
    Client -->|"MCP/tool calls where supported"| MCPGateway["MCP gateway"]
    Client -->|"patch/evidence events where supported"| EvidenceAPI["Evidence and patch APIs"]

    ModelGateway --> Shell["Governance Shell"]
    MCPGateway --> Shell
    EvidenceAPI --> Shell

    Shell --> Policy["Policy and classification checks"]
    Shell --> Sessions["Session and runtime credential authority"]
    Shell --> Audit["Audit, evidence, cost and ledger storage"]
    Shell --> Router["Governance Router"]

    Router --> Copilot["Actor-bound Copilot backend"]
    Router --> Bifrost["Bifrost sidecar"]
    Bifrost --> Providers["OpenRouter, Azure, Bedrock, OpenAI, Anthropic, DeepSeek"]

    Shell --> UI["Governance UI"]
    Shell --> Orchestrator["Internal Orchestrator"]
    Orchestrator --> Catalog["Agent catalogue and runtime dispatch"]
~~~

The split matters:

- the client owns local repo access;
- the model gateway owns runtime-facing model compatibility;
- the MCP gateway owns governed tool access;
- the Governance Shell owns authority and evidence;
- provider plumbing is replaceable.

## Current beta readiness

As of v0.21.1-beta, the local beta proves the main product direction but does not claim production readiness.

| Area | Readiness | Honest read |
| --- | --- | --- |
| Core governance bet | Close | Sessions, policy gates, model gateway routing, actor-bound Copilot, Bifrost/OpenRouter fallback, fail-closed runtime dispatch, audit, cost attribution, patch decisions and the local ledger UI are working. |
| AI-Orch-routed OpenCode | Strongest client path | OpenCode keeps local workspace access while model traffic and session evidence cross AI-Orch. Full local Task/Read/Edit/Bash transcript capture still needs ACP, MCP or client-event forwarding. |
| Team beta | Partly there | A small technical team can evaluate a central AI-Orch server with developer-owned clients, but onboarding polish, runbooks and operational controls still need work. |
| V1 | Not yet | Needs durable shared-state hardening, richer client-event forwarding or MCP coverage, cleaner admin workflows and GitHub/Azure DevOps delivery evidence. |
| Production | Not claimed | Needs multi-instance persistence, tenant isolation, monitoring, backup/restore, release automation and security review. |

## Component responsibilities

| Component | Responsibility | Should not do |
| --- | --- | --- |
| Governance Shell | Policy, sessions, runtime credentials, model routing authority, audit, evidence, cost and UI APIs. | Become a source-code browser for every developer. |
| Model Gateway | OpenAI-compatible /v1/models, /v1/chat/completions and /v1/responses surface for clients such as OpenCode and Cline. | Compete with Bifrost, LiteLLM or provider-native gateways as a generic gateway product. |
| MCP Gateway | Governed tool discovery and invocation where clients support MCP. | Pretend to audit native client tools that never route through MCP. |
| Orchestrator | Internal runtime dispatch, catalogue validation and controlled agent execution. | Own provider credentials or become the public authority. |
| Agent catalogue | Agent instructions, config, permissions and model aliases. | Replace developer-native tool ergonomics. |
| Bifrost sidecar | Provider translation, retries and multi-provider plumbing behind AI-Orch. | Own policy, session authority, audit or backbilling. |
| Governance UI | Local beta inspection, provider posture, ledger, audit, evidence and basic workflow records. | Become a full enterprise admin console yet. |

## AI-Orch-routed OpenCode flow

~~~mermaid
sequenceDiagram
    participant Dev as Developer
    participant OC as OpenCode
    participant GW as AI-Orch Model Gateway
    participant Shell as Governance Shell
    participant CP as Copilot or Bifrost
    participant Ledger as Ledger/UI

    Dev->>Shell: Enrol through GitHub/Copilot
    Shell->>Shell: Store actor-bound credential server-side
    Shell-->>Dev: Issue revocable AI-Orch runtime credential
    Dev->>OC: Use OpenCode normally
    OC->>GW: OpenAI-compatible model request
    GW->>Shell: Resolve actor, session, model alias and policy
    Shell->>CP: Call approved backend/provider
    CP-->>Shell: Stream/model response
    Shell->>Ledger: Record route, usage, cost, status and audit metadata
    Shell-->>GW: Governed response stream
    GW-->>OC: Compatible response
~~~

This is the key scale path: OpenCode keeps the local repository and file tools; AI-Orch governs the model path and records what it can prove. When OpenCode emits model-visible tool calls such as task, AI-Orch records sanitized tool-call names/counts and can create child-session lineage for known catalog agents. It does not claim full local transcript capture unless the event crosses ACP, MCP or a deliberate client-event forwarding lane.

## Developer onboarding model

The desired developer flow is:

1. The operator runs a central AI-Orch server with a durable encrypted token store.
2. The developer runs an enrolment command for their client, currently OpenCode.
3. The developer proves GitHub/Copilot access on their own machine.
4. The server stores the actor-bound Copilot credential encrypted.
5. The server issues a revocable AI-Orch runtime credential for that actor and device hint.
6. The OpenCode config receives only the AI-Orch endpoint, runtime credential, actor label and model list.
7. A local refresh job updates only the AI-Orch provider block and model list.

This keeps provider keys out of project files and avoids wiping a developer's existing OpenCode providers.

## Runtime and trust labels

Trust labels describe evidence strength; they are not client allow-lists.

| Label | Meaning |
| --- | --- |
| gateway_enforced | The model or tool call crossed an AI-Orch gateway and policy/audit were enforceable. |
| managed_client | A managed client reported local events through an approved path. |
| self_reported | The client reported activity that did not cross an enforceable gateway. |

That distinction keeps the ledger honest. AI-Orch should show how the work actually ran instead of pretending every client integration has the same evidence strength.

## State and storage stance

SQLite is the right default for the beta because it keeps the system small and understandable. The hardening path should be boring rather than bloated:

- keep SQLite for local/team beta;
- document backup and restore;
- keep schema migrations explicit;
- preserve audit hash-chain recovery paths;
- support encrypted token-store lifecycle and key rotation;
- leave a PostgreSQL migration path for teams that outgrow single-node SQLite.

Do not add Postgres just to look enterprise. Add it when the usage pattern demands it.

## Governance outputs

AI-Orch should emit machine-readable records that another reporting or maturity layer can consume:

- session summaries;
- actor, team and workflow identifiers;
- policy and control outcomes;
- provider, model, token and cost records;
- context provenance and hashes;
- evidence records;
- cache outcomes;
- patch decisions;
- benchmark records;
- audit-chain records.

The IDE and CLI should send lightweight IDs and intent. The Governance Shell should resolve policy, provenance, evidence and cost behind the boundary.

## What stays outside the product

AI-Orch should not become:

- a replacement coding agent;
- a central repo access service;
- a generic model gateway business;
- a Backstage, Jira, ServiceNow or documentation portal clone;
- an enterprise identity or device-management suite;
- a production platform before the boring operational pieces are hardened.

The platform should borrow useful runtime ideas, not absorb every runtime into itself.

## Design questions still open

- How much OpenCode local transcript detail should be forwarded, and what should be hashed rather than stored raw?
- Should MCP be the next primary adapter once OpenCode model routing is stable?
- How soon should GitHub or Azure DevOps become the delivery evidence surface?
- What should the Governance Router optimise for: task type, risk, workflow stage, classification, provider health, latency, cost or evidence needs?
- How should benchmarks be weighted so teams compare cost-per-success rather than model hype?
- What belongs in the operator UI versus the CLI/runbook?
- When does SQLite stop being enough for a team server?
- How should managed endpoint policy work if teams continue using provider keys directly?

## Related design references

These are not dependencies; they are useful pressure tests for the architecture.

- [Microsoft Agent Governance Toolkit](https://github.com/microsoft/agent-governance-toolkit): runtime security governance for autonomous agents and MCP-style tool boundaries.
- [GitHub Agentic Workflows security architecture](https://github.blog/ai-and-ml/generative-ai/under-the-hood-security-architecture-of-github-agentic-workflows/): zero-secret agent execution, model proxying, MCP gateway access and staged writes.
- [Securing MCP with a control plane](https://developer.microsoft.com/blog/securing-mcp-a-control-plane-for-agent-tool-execution): why MCP needs a policy checkpoint around it.
- [Bifrost](https://github.com/maximhq/bifrost): provider-plumbing reference that should stay behind AI-Orch.
- [Factory Router](https://factory.ai/news/factory-router): useful proof that model choice is becoming its own decision layer.
- [SWE-bench](https://www.swebench.com/), [LiveCodeBench](https://livecodebench.github.io/), [Terminal-Bench](https://www.tbench.ai/), [Aider leaderboards](https://aider.chat/docs/leaderboards/) and [tau-bench](https://github.com/sierra-research/tau-bench): benchmark references for workflow, code, terminal and tool-use evaluation.

## Guiding principle

Do not mix runtime structure with governance structure too early.

Keep the boundary small, strict and auditable. Let developer runtimes stay flexible behind it.
