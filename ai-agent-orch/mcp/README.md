# MCP Server Registrations

This directory contains the MCP (Model Context Protocol) server registrations that agents can use during execution.

## What is MCP?

MCP servers provide context and tools to agent runtimes: repository classification, engineering standards, issue trackers, documentation systems, test management, and more.

## Registration Files

Each `.yaml` file in this directory registers one MCP server:

```yaml
server_id: repo-classification
endpoint: http://mcp-repo-classification:8091
auth_mode: platform          # none | local-dev-token | platform | oauth-user
allowed_agents:              # Optional: restrict which agents can use this MCP
  - unit-tests
  - code-review
  - refactor
```

## Auth Modes

| Mode | Behavior |
|------|----------|
| `none` | No authentication required |
| `local-dev-token` | Accepts the local dev token |
| `platform` | Uses a shared platform token from `AI_ORCH_MCP_TOKEN` env var |
| `oauth-user` | Requires a user-scoped OAuth token; **fails closed** if missing or expired |

## Fail-Closed Rule

When `auth_mode: oauth-user` is configured and no valid user token exists, the MCP proxy returns `403 oauth_user_token_missing`. There is **no** fallback to a platform token or shared system token.

## Phase 1 MCPs (Active)

| Server | Purpose | Auth |
|--------|---------|------|
| `repo-classification` | Returns repo classification and owner/team | platform |
| `engineering-standards-kb` | Provides testing and coding standards | platform |
| `catalog-introspection` | Lets router inspect agent metadata | platform |
| `playwright-cli` | Controlled Playwright command execution | platform |

## Phase 2 MCPs (Profile-Gated)

| Server | Purpose | Auth |
|--------|---------|------|
| `issue-tracker` | Read-only issue tracker integration | oauth-user |
| `documentation` | Read-only documentation system | oauth-user |
| `test-management` | Read-only test management system | oauth-user |

The local stub containers can still be started without real OAuth, but calls through the Governance Shell MCP proxy fail closed until a user-scoped token exists.

Enable Phase 2 MCP stubs in Docker Compose:
```bash
docker compose --profile phase2 up
```

## Adding a New MCP Registration

1. Create `mcp/registrations/<server-id>.yaml`
2. Add the server ID to agent configs that need it
3. Point the registration at a real MCP service endpoint, or add a local
   `mcp-stub <server-id>` handler when the endpoint is only for demos
4. Register the service in Docker Compose if it should run with the local stack

## Agent Configuration

Agents reference MCP servers in their `agent.config.yaml`:

```yaml
mcp_servers:
  - repo-classification
  - engineering-standards-kb
```

The catalog validator rejects agents referencing unknown MCP servers.

## See Also

- `../agents/` — Agent definitions that reference these MCPs
- `../internal/governance/mcp_proxy.go` — MCP proxy implementation
