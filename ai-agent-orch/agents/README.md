# Agent Catalog

This directory contains all agent definitions for the AI Agent Orchestration System.

## How Agent Discovery Works

Agents are discovered automatically from the filesystem. No database registration, API call, or runtime command is required to add a new agent.

### Directory Structure

```text
agents/
  core/         → System agents (e.g., router-agent)
  temp/         → Experimental agents under development
  published/    → Production-ready agents with stricter validation
```

### Required Files Per Agent

Every agent directory must contain exactly three files:

| File | Purpose |
|------|---------|
| `agent.md` | Human/model-readable mission, boundaries, and rules. **No executable config.** |
| `agent.config.yaml` | Strict executable contract: runtime, model alias, MCP servers, tools, permissions, cost cap. |
| `evals/golden-cases.yaml` | Router training data so the system knows when to select this agent. |

### Adding a New Agent

1. Create a directory under the appropriate group:
   - `temp/<agent-name>/` for experiments
   - `published/<agent-name>/` for production (requires stricter validation)

2. Add the three required files.

3. Within 30 seconds, the Orchestrator router cache expires and the new agent becomes selectable.

### Example: Creating a New Agent

```bash
mkdir -p agents/temp/my-agent/evals
```

Create `agents/temp/my-agent/agent.md`:
```markdown
# My Agent

Config: `./agent.config.yaml`

## Goal
What this agent does.

## Use When
When to route to this agent.

## Do Not Use When
When NOT to route to this agent.

## Rules
- Do not install packages.
- Do not modify files outside the workspace.
```

Create `agents/temp/my-agent/agent.config.yaml`:
```yaml
name: my-agent
version: 0.1.0
phase: experimental
owner: local
runtime: opencode
model:
  primary: coding-balanced
  fallback: coding-economy
mcp_servers:
  - repo-classification
tools_allowed:
  - read_file
permissions:
  network: deny
  workspace_write: deny
  outside_workspace_write: deny
governance:
  classification_max: internal
cost:
  per_invocation_cap_usd: 0.25
  consecutive_tool_call_max: 15
evals:
  path: ./evals/
  required_for_phase0: false
```

Create `agents/temp/my-agent/evals/golden-cases.yaml`:
```yaml
cases:
  - prompt: "Example prompt that should route to my-agent"
    expected_specialist: my-agent
```

### Validation Rules

The catalog validator enforces these rules on every refresh:

- `agent.md` must reference `./agent.config.yaml`
- `agent.md` must not contain executable tokens (`tools_allowed:`, `permissions:`, `cost:`, etc.)
- `agent.config.yaml` must use model **aliases** only (defined in `models/registry.yaml`)
- MCP servers referenced must exist in `mcp/registrations/`
- Read-only agents (`workspace_write: deny`) cannot have `write_file` in `tools_allowed`
- Only `test-generation` can use `run_command:playwright`
- `published/` agents must have `phase: published`, `version >= 1.0.0`, and `evals.required_for_phase0: true`
- Router golden cases must cover all temporary and published agents

### Removing an Agent

Delete the agent directory. The cache expires within 30 seconds and the agent is no longer routable.

### Promoting an Agent

To move from `temp/` to `published/`:

1. Move the directory: `mv agents/temp/my-agent agents/published/my-agent`
2. Update `phase: published` and `version: 1.0.0` in `agent.config.yaml`
3. Set `evals.required_for_phase0: true`
4. Run validation: `go run ./cmd/catalog-validator`
5. Commit the changes

### Model Aliases

Agents reference models by alias, not by concrete provider ID. Aliases are resolved via `models/registry.yaml`:

| Alias | Purpose |
|-------|---------|
| `coding-primary` | Highest-quality coding work |
| `coding-balanced` | Default coding and agent workflow |
| `coding-fast` | Fast coding loops |
| `coding-economy` | Cheaper draft generation |
| `router-small` | Routing, summarization, classification |

### Cache Refresh

The Orchestrator caches the validated catalog for 30 seconds. If validation fails, the error is cached for 5 seconds to avoid hammering the filesystem.

### Verification

Run the catalog validator manually:
```bash
cd ai-agent-orch
go run ./cmd/catalog-validator
```

Or via Docker Compose:
```bash
docker compose --profile tools run --rm catalog-validator
```

## See Also

- `../models/registry.yaml` — Model alias definitions
- `../mcp/registrations/` — MCP server registrations
- `../docs/local-state-lifecycle.md` — System state boundaries
