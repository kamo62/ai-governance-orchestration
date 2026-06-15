# Router Design Note: Best-Score Model Selection and the Driver Role

Status: proposal / discussion
Date: 2026-06-13
Owner: core
Related: `internal/router`, `internal/classifier`, `internal/orchestrator/router.go`, `internal/modelgateway`, `agents/core/governance-lead`, `agents/core/router-agent`

## Summary

This note reviews the sybil-solutions codex-shim AUTO_ROUTER against our own routing, records what our system actually does today, and proposes a direction. Two questions drove it:

1. Should we adopt ideas from codex-shim's auto router, where they fit our governance-first design.
2. Do we have a main agent that drives the work, and where does model selection authority actually live.

The short version: we have two routing layers and a designated lead agent, but the capability-scoring model router is mostly dormant on the live paths, and no agent owns the full task lifecycle. The recommended direction is a best-score model selection keyed to the agent's declared capability needs, with data classification as a hard filter and cost as a deterministic tiebreak. We keep selection deterministic and auditable, and we do not adopt an LLM classifier as the routing authority.

## What codex-shim AUTO_ROUTER does

The codex-shim router selects a model per task:

- A cheap LLM classifier reads the task plus a capability card for each candidate and returns a success probability from 0.0 to 1.0.
- The router picks the cheapest candidate whose score clears a threshold (default 0.7), or the highest scorer if none clear it.
- Cost is applied after capability scoring so the selection is not biased toward expensive models.
- A candidate is hard-scored to zero if the task needs images and the candidate does not support them.
- The decision is cached per task, so the classifier runs once across tool-call round-trips.
- If the classifier fails, times out, or returns garbage, the router falls back to a configured default candidate.
- A log flag exposes raw scores per candidate for tuning.

It is a clean design for cost-aware model selection. The authority is an LLM, which is the part we deliberately do not want.

## What our system does today

We have two layers that both get called "routing", plus a lead agent.

### Specialist routing (which agent handles the work)

`internal/orchestrator/router.go` selects a specialist. Order: branch-prefix mapping first, deterministic keyword matching second, `code-review` as the human-confirmed default. The deterministic classifier in `internal/classifier` runs alongside it for advisory enrichment only; it never changes the specialist and is not authoritative for the classification ceiling.

### Model routing (which model alias runs)

`internal/router` filters candidate aliases by the data-classification ceiling, then either validates a caller-preferred alias or scores the survivors by task alignment and picks the single highest score. Scoring is deterministic keyword matching against each model's free-text `purpose` field.

### Lead agent

`agents/core/governance-lead` is the designated ai-orch OpenCode entry point. It clarifies intent, checks risk and classification, attaches work context, and recommends the specialist that should act next. It explicitly does no delivery work, makes no file changes, and runs no commands.

### The flow as wired today

```text
developer prompt
  -> governance-lead (entry, intent + risk, recommend specialist; no delivery)
  -> router-agent / SelectSpecialist (pick the specialist)
  -> Dispatcher.Dispatch (reads the chosen agent's pinned model.primary)
  -> specialist runtime session (OpenCode/ACP, direct fallback)
```

## Two findings that shape the decision

### Finding 1: the scoring model router is mostly dormant

`Dispatcher.Dispatch` resolves the model from the agent config: it reads `agentCfg.Model.Primary` (with an `AI_ORCH_MODEL_ALIAS_OVERRIDE` escape hatch) and never calls `internal/router`. Every specialist already pins its tier in `agent.config.yaml`:

| Agent | primary | fallback |
|---|---|---|
| governance-lead | coding-gpt55 | coding-balanced |
| router-agent | router-small | coding-economy |
| code-review | coding-balanced | coding-economy |
| unit-tests | coding-fast | coding-economy |
| architecture-review | coding-primary | coding-balanced |
| security-review | coding-primary | coding-balanced |
| terraform-review | coding-primary | coding-balanced |
| security-scan | coding-primary | coding-balanced |
| refactor | coding-primary | coding-balanced |
| backend-development | coding-balanced | coding-economy |
| frontend-development | coding-balanced | coding-economy |
| documentation | coding-economy | router-small |

So "which model" is currently answered by "which agent". The capability mapping lives in twelve config files, not in the router.

The only live caller of `router.Route` is the model compatibility gateway (`internal/modelgateway/gateway_dynamic_copilot.go`), which runs when a developer runtime such as OpenCode or Cline calls the gateway directly. On that path the router mostly validates the runtime-supplied `PreferredAlias` against the classification ceiling and resolves the concrete provider route. The rich scoring inputs (`RiskLevel`, `CostSensitivity`, `LatencySensitivity`, `EvidenceNeeds`, `WorkflowStage`) are not populated there, so the task-alignment scoring only fires when no preferred alias is supplied, and even then it scores on a thin signal.

The result: the best-score engine exists but is not on the path that matters for orchestrated runs, and the path that does call it does not feed it enough to score well.

### Finding 2: there is a lead, but no agent owns the lifecycle

`governance-lead` is the front door and the router is the switch, but neither drives a plan, dispatch, verify, re-route loop. The orchestration steps are sequenced by Go code and the human in the OpenCode session. Dispatch is single-shot per agent. There is no component, agent or code, that holds the task plan, dispatches a specialist, inspects the result, and decides the next specialist or model.

This is the gap the driver-agent question was pointing at. It is real, and it is separable from model routing. We should decide it explicitly rather than leave it implicit in Go control flow.

## Proposed direction: best-score keyed to agent capability

The instinct to lean on best score, linked to specific agents for specific work, fits what we already have. The agent is already the unit of capability. The change is to stop hardcoding a concrete model alias in each agent and instead let the agent declare what it needs, then let the router resolve the concrete alias deterministically.

### Where model authority should live

Agent config declares a capability profile rather than a fixed alias:

- a required tier or capability set (for example `tier: highest` or `capabilities: [coding, security]`),
- optional hard requirements (vision, minimum context, tool calling),
- an explicit fallback tier.

The router then resolves the concrete alias by best score against structured capability metadata on each model, subject to:

- the data-classification ceiling as a hard filter (already implemented and correct),
- hard capability gates that drop ineligible candidates (the general form of codex-shim's image gate),
- cost as a deterministic tiebreak among candidates with equal capability score,
- an explicit per-classification default when nothing clears a minimal bar.

This keeps the agent as the capability signal, removes twelve scattered model pins, and makes the model choice respond to per-task signals (classification, risk) that a static pin cannot see.

### Best score versus cheapest good enough

codex-shim picks the cheapest candidate above a threshold. We lean toward highest capability score instead, because our specialists already encode the quality intent and because governed work should not silently drop to a cheaper model. Cost stays in the design as a tiebreak between equally capable candidates and as an optional posture when the request is explicitly cost-sensitive and low risk. High-risk or restricted work never downgrades for cost.

### Concrete changes, ranked

1. Structured capability metadata on models. Replace `strings.Contains(purpose, "highest-quality")` scoring with explicit fields (`tier`, `capabilities`, hard-requirement flags). Deterministic and auditable.
2. Deterministic tiebreak. Today `if score > bestScore` keeps the first maximum, so ties resolve by registry file order. Make it explicit: capability score, then lowest cost, then alias name.
3. Parse and use cost. The registry already carries a `cost:` block per model that `ModelDefinition` does not parse. Add a numeric relative weight and use it for the tiebreak above.
4. Eligibility floor plus explicit default. `bestScore` starts at -1, so a winner is always chosen even when every candidate scores zero, which silently picks whatever sits first in the file. Add a minimum score to be eligible and a configured default alias per classification when nothing clears it.
5. Surface per-candidate scores in the decision. We compute a score per candidate and discard all but the winner's. Add the considered set with scores to `Decision` so the audit event explains why each alias won or lost. This replaces codex-shim's debug log flag with something stronger that we already have a pipe for.
6. Hard capability gates. Let a request declare needs (vision, minimum context) and filter candidates the same way classification already filters. Lower priority until a task needs it.

### Bug surfaced during the review

`classifyModelRoute` routes `restricted` and `confidential` work to `coding-primary`, but `coding-primary` allows only `[public, internal]`, and no model in the registry permits `restricted`. A restricted task would route to an alias the model router then rejects, returning "no models available for classification restricted". Fix the mapping or add a restricted-capable alias.

## The driver role

Separate from model routing, decide what owns the task lifecycle. Three options:

1. Keep Go orchestration as the driver and treat `governance-lead` purely as an entry and routing conversation. Simplest, matches today, but the lifecycle stays implicit in code.
2. Promote `governance-lead` to own the lifecycle: plan, request dispatch of a specialist, read the result, decide the next step, all within its conversation, still doing no delivery work itself. Closest to a single driver agent, more design and eval work.
3. Add a dedicated orchestrator agent distinct from the lead, so the lead stays a thin front door and the orchestrator owns multi-step delivery.

Recommendation: option 1 for the current beta, with the lifecycle made explicit in the orchestrator code and audited, and revisit option 2 once specialist evals are stable. Adding a driver agent before the model-routing authority is settled would couple two unsettled designs.

## Non-goals

- We do not adopt an LLM classifier as the routing authority. The deterministic classifier keeps the final decision; an LLM stays advisory, as documented in `internal/classifier`.
- We do not change the data-classification ceiling behaviour. It stays caller-supplied and enforced server-side as a hard filter.

## Open decisions

1. Best score versus cheapest good enough as the default selection rule, and whether cost-sensitivity is ever allowed to override capability for low-risk work.
2. Whether agents declare a capability profile or keep pinning concrete aliases. This determines whether the scoring router moves onto the dispatch path at all.
3. The driver-role option above.
4. Whether to wire `RiskLevel`, `CostSensitivity`, and related signals into the gateway path so the existing router scores on a real signal there too.
