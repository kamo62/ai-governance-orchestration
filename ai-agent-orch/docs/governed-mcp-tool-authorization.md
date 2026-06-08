# Governed MCP Tool Authorisation

## Status

Implemented in `v0.8.0-alpha`.

Phase 1J makes `gateway_enforced` meaningful for upstream MCP tool calls. MCP clients call `call_governed_tool` through `ai-orch-mcp`; the Governance Shell then resolves the governed session, evaluates the runtime MCP registration policy, writes an audit event, and only then forwards to the upstream MCP service.

## Why This Matters

MCP is useful as an agent-tool boundary, but MCP discovery alone is not governance. A client can be shown a tool and still call it in the wrong session, with the wrong agent, at the wrong classification level, or outside the declared registration policy.

The Governance Shell therefore treats MCP tool calls as policy decisions, not just proxy traffic.

## Runtime Contract

Every upstream MCP tool call must include:

- a valid bearer token for the Governance Shell;
- `X-AI-Orch-Session-ID`;
- a registered MCP `server_id`;
- a tool name declared in that server's `tool_policy.allow`.

The shell fails closed when:

- the session ID is missing;
- the durable session store is not configured;
- the session cannot be found;
- the MCP server is unknown;
- the session agent is not in `allowed_agents`;
- the tool is not in `tool_policy.allow`;
- the tool is in `tool_policy.deny`;
- the session classification exceeds the effective classification ceiling;
- upstream credential resolution fails;
- audit persistence fails before forwarding.

## Policy Source

Runtime MCP registration policy is loaded from `mcp/registrations/*.yaml`.

The Governance Shell still preserves Compose-specific internal service endpoints, because the YAML files use localhost endpoints for local/manual reference while Docker services use Compose DNS names.

## Audit Semantics

Allowed and denied calls are recorded as `mcp.proxy_call` events with:

- `trust_level: gateway_enforced`;
- `mcp_server_id`;
- `mcp_tool_name`;
- `auth_mode`;
- `agent`;
- `classification`;
- `policy_decision_id`;
- `reason`.

The shell writes the allowed-call audit event before forwarding. If the audit write fails, the upstream call is blocked.

Raw prompts, raw responses, provider keys and upstream credentials are not stored in audit records.

## Remaining Maturity Steps

- Add human approval handling for MCP registrations with `tool_policy.default_approval: manual`.
- Add optional classification ceilings per MCP registration once the YAML schema needs them.
- Add Bifrost or sidecar-level tool filters only as defence-in-depth, not as the source of authority.
- Add a signed or externally anchored audit checkpoint when multi-instance tamper evidence becomes a Phase 2 requirement.
