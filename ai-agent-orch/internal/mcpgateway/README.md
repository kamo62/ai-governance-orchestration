# MCP Gateway

The `ai-orch-mcp` gateway exposes the Governance Shell through the Model Context Protocol (MCP). This lets any MCP-capable client (VS Code, CLine, Claude Code, Codex, Cursor) create governed sessions, delegate work, and record audit evidence without giving those clients direct access to provider API keys.

MCP is the tool and evidence lane. Model governance is handled by the ai-orch model compatibility gateway where the client supports a custom provider endpoint. A client can be configured for both: model calls go to ai-orch `/v1`, and tool/evidence calls go to `ai-orch-mcp`.

## Start the Gateway

### HTTP/SSE mode (default, port 18081)

```bash
ai-orch mcp start --transport http --port 18081
```

### Stdio mode (for clients that prefer stdio transport)

```bash
ai-orch mcp start --transport stdio
```

## Install Client Configuration

Generate client-specific setup files in the current directory:

```bash
# VS Code
ai-orch mcp install --client vscode

# CLine
ai-orch mcp install --client cline

# Claude Code
ai-orch mcp install --client claude-code

# Codex
ai-orch mcp install --client codex
```

## Doctor Check

Verify client configuration:

```bash
ai-orch mcp doctor
```

## Available Tools

### Phase 1G — Gateway Foundation

- `mcp_doctor` — Check gateway health, governance shell reachability, and token setup.
- `start_governed_session` — Create a new governed session with optional control-plane bindings (use-case, workflow, repo, branch, intent).

### Phase 1I — Governed Delegation

- `create_context_manifest` — Create a bounded context manifest for a governed session. Returns a manifest ID for `delegate_governed_work`.
- `attach_use_case` — Register a use case in the governance registry.
- `attach_workflow` — Register a workflow template in the governance registry.
- `delegate_governed_work` — Send a prompt to a governed session. The Governance Shell resolves policy, chooses the model alias, and buffers patch proposals.
- `record_patch_decision` — Record a decision (applied, rejected, partially_applied) for a proposed patch.
- `lookup_audit` — Retrieve audit events and evidence for a session.

### Phase 1J — Gateway-Enforced Tool Calls

- `list_allowed_tools` — List upstream MCP tools available for a governed session after agent/classification policy filtering.
- `call_governed_tool` — Call an upstream MCP tool through the Governance Shell. The call requires a governed session, is policy-checked before forwarding, credential-safe and audited as `gateway_enforced`.

### Phase 1K — Self-Reported Audit Companion

- `record_external_tool_call` — Self-report a native tool call with `trust_level: self_reported`.
- `record_external_model_call` — Self-report a native model call with `trust_level: self_reported`.

## Authentication

The HTTP/SSE gateway supports dev-token authentication via the `Authorization: Bearer <token>` header. If no token is configured, HTTP/SSE tools fail closed with `503`. Stdio mode still relies on local process boundaries and the Governance Shell token used by the tool handlers.

Set the token via environment variable:

```bash
export AI_ORCH_DEV_TOKEN=your-token
```

## Architecture

```text
MCP Client (VS Code, CLine, Claude Code, Codex)
    |
    | MCP protocol (HTTP/SSE or stdio)
    v
ai-orch-mcp Gateway
    |
    | HTTP + Bearer token
    v
Governance Shell (http://127.0.0.1:18080)
    |
    |-- Audit and Evidence
    |-- Patch Buffer
    |-- Model Backend (Bifrost by default, Copilot optional)
    |-- Policy Engine
```

The client keeps local repository access. The gateway should receive bounded context, tool requests, patch decisions and evidence, not a full copy of the developer's source tree.

## Trust Levels

- `gateway_enforced`: Work routed through the MCP Gateway and evaluated by the Governance Shell.
- `managed_client`: Work coming from a managed client path where ai-orch can trust client configuration more strongly than a self-report, but still distinguish it from gateway-enforced calls.
- `self_reported`: Work reported natively by the agent but not routed through the gateway.

Trust levels are observations for audit and reporting. They describe how the work was run; they are not permission settings and should not decide whether a developer is allowed to use a chosen client.
