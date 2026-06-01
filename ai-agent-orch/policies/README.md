# Governance Policies

This directory contains policy definitions enforced by the Governance Shell.

## Policy Files

| File | Purpose |
|------|---------|
| `classification-routing.yaml` | Repository classification levels and routing rules |
| `cost-caps.yaml` | Per-session and per-agent cost limits |
| `secrets-patterns.yaml` | Regex patterns for secret detection in prompts and patches |
| `command-allowlists.yaml` | Which commands agents are permitted to execute |
| `sdlc-governance.yaml` | Workflow evidence and approval expectations for SDLC governance |

## Native Policy Engine

The Governance Shell loads `classification-routing.yaml`, `secrets-patterns.yaml`,
`cost-caps.yaml`, and `sdlc-governance.yaml` into the native policy engine at
startup. Those files are the policy-as-code source for session pre-flight
decisions.

The command allow-list is enforced closer to runtime dispatch by
`dispatch.ToolBroker`, because command validation needs the concrete tool call
and agent permissions.

## Command Allowlists

The command allowlist is the primary runtime security boundary for agent tool execution.

### Default: Deny-All

Unless explicitly allowed, **all** commands are denied. This is a fail-closed default.

### Structure

```yaml
system_commands:
  - name: read_file
    description: Read file contents from the workspace.
    default: allow                      # Safe by default

  - name: write_file
    description: Write or overwrite file contents.
    default: deny
    requires: workspace_write = allow   # Only if agent config allows writes

  - name: run_command
    description: Execute a shell command.
    default: deny
    subcommands:
      - name: playwright
        description: Run Playwright tests.
        allowed_agents:
          - test-generation            # ONLY test-generation can run Playwright

      - name: go
        description: Run Go commands.
        allowed_agents:
          - test-generation
          - code-review

      - name: curl                    # NOT listed = denied for all agents
        # ...omitted = denied
```

### Enforcement

The `dispatch.ToolBroker` loads this file and validates every tool call before the runtime executes it:

```go
broker, _ := dispatch.NewToolBroker("policies/command-allowlists.yaml")
err := broker.Validate("run_command", "playwright", "test-generation")
// err == nil → allowed
// err != nil → denied, runtime blocked
```

### Fail-Closed Behavior

- Unknown commands → denied
- Unknown subcommands → denied
- Agent not in `allowed_agents` list → denied
- Missing policy file → all commands denied
- Nil broker → all commands denied

## Secret Detection

The Governance Shell scans every prompt and patch for secrets using patterns defined in `secrets-patterns.yaml`. If a secret is detected:

1. The request is blocked before reaching the model.
2. A `session.denied` audit event is recorded with `reason: secret detected`.
3. The `secrets_blocked` metric counter increments.

## Classification Enforcement

Every session specifies a `classification` (e.g., `public`, `internal`, `confidential`). The Governance Shell blocks sessions where:

- The classification exceeds the configured maximum (`classification_max`)
- The agent's `governance.classification_max` is lower than the session classification

## Cost Caps

Cost enforcement is **disabled by default**. Enable with:

```bash
export AI_ORCH_COST_CAP_ENABLED=true
export AI_ORCH_SESSION_COST_CAP_USD=0.50
```

When enabled:
- Pre-flight: blocks sessions with `estimated_cost_usd > cap`
- Mid-flight: stops gracefully if the cap is exceeded during execution
- Returns `402 Payment Required` for over-cap requests

If `AI_ORCH_SESSION_COST_CAP_USD` is unset or zero while cost caps are enabled,
the native engine falls back to `defaults.specialist_cap_usd` from
`cost-caps.yaml`.

## SDLC Governance

`sdlc-governance.yaml` declares workflow-level evidence and approval
expectations. This sits at the maturity-governance layer rather than the
low-level runtime-safety layer.

Examples:

- `change-release` can require a linked work item, test result, approval
  receipt and patch decision.
- `security-review` can require security findings, review output and an
  approval receipt.
- `architecture-review` can require a decision record and approval receipt.

At session creation, the native policy engine uses the supplied `workflow_id`
and `work_item_id` to decide whether a session is allowed, requires approval, or
is missing mandatory workflow metadata. Evidence completeness is reported
through the registry/evidence APIs rather than by stuffing all governance
context into the model prompt.

## Modifying Policies

1. Edit the relevant YAML file
2. Restart the Governance Shell (native policy files are loaded at startup)
3. For command allowlists, the broker reloads on each dispatch call

## See Also

- `../docs/local-state-lifecycle.md` — State and durability boundaries
- `../internal/dispatch/tool_broker.go` — Tool broker implementation
