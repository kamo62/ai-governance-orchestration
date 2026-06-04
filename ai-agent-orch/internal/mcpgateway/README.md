# MCP Gateway

The `ai-orch-mcp` gateway exposes the Governance Shell through the Model Context Protocol (MCP). This lets any MCP-capable client (VS Code, CLine, Claude Code, Codex, Cursor) create governed sessions, delegate work, and record audit evidence without giving those clients direct access to provider API keys.

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

- `delegate_governed_work` — Send a prompt to a governed session. The Governance Shell resolves policy, chooses the model alias, and buffers patch proposals.
- `record_patch_decision` — Record a decision (applied, rejected, partially_applied) for a proposed patch.
- `lookup_audit` — Retrieve audit events and evidence for a session.

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
    |-- Model Backend (Bifrost by default, native OpenRouter fallback)
    |-- Policy Engine
```

## Trust Levels

- `gateway_enforced`: Work routed through the MCP Gateway and evaluated by the Governance Shell.
- `managed_client`: Work coming from a managed client path where ai-orch can trust client configuration more strongly than a self-report, but still distinguish it from gateway-enforced calls.
- `self_reported`: Work reported natively by the agent but not routed through the gateway.

Always prefer `gateway_enforced` paths for file-changing, model-calling, and tool-calling work.
