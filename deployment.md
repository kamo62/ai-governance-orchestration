# Deployment And Local Run Guide

This document explains how to run and verify the local AI Agent Orchestration POC.

The root [README.md](README.md) explains the aim of the project. This file is intentionally more operational.

## Current Deployment Shape

The runnable scaffold lives in `ai-agent-orch/`.

The local Compose stack runs:

- `governance-shell`: public local API and policy boundary.
- `orchestrator`: internal runtime orchestration service.
- optional MCP stub services.
- optional tools such as `catalog-validator`, `openrouter-smoke`, and `ai-orch`.

The Orchestrator is intentionally internal to Docker Compose. OpenRouter credentials belong to the Governance Shell, and runtime model calls go through the internal model proxy.

## Prerequisites

- Go 1.26 or the repo's Docker build.
- Docker Desktop or compatible Docker Compose.
- Bun for the VS Code Bridge.
- VS Code if testing the Bridge.
- An OpenRouter key only for provider smoke tests or model-backed orchestration.

Keep local secrets in the ignored root `.env.dev` file. Do not commit it.

Expected `.env.dev` fields:

```sh
OPENROUTER_API_KEY=...
AI_ORCH_DEV_TOKEN=local-dev
AI_ORCH_ADMIN_TOKEN=local-admin
AI_ORCH_SERVICE_TOKEN=local-service-token
AI_ORCH_RUNTIME_TOKEN=local-runtime-token
```

## Fast Local Verification

From `ai-agent-orch/`:

```sh
go test ./...
go vet ./...
go run ./cmd/catalog-validator -catalog-root .
docker compose config
```

For the VS Code Bridge:

```sh
cd ai-agent-orch/agent-bridge
bun run typecheck
bun run lint
bun run compile
```

## Docker Cleanup And Rebuild

When Docker changes are involved, clean old local Compose images first:

```sh
cd ai-agent-orch
docker compose --env-file ../.env.dev --profile tools --profile phase2 down --remove-orphans --rmi local
docker compose --env-file ../.env.dev --profile tools build governance-shell orchestrator catalog-validator openrouter-smoke ai-orch
```

Run the containerised catalogue validator:

```sh
docker compose --env-file ../.env.dev --profile tools run --rm catalog-validator
```

Run the OpenRouter provider smoke test:

```sh
docker compose --env-file ../.env.dev --profile tools run --rm openrouter-smoke \
  openrouter-smoke -catalog-root /app \
  -model-alias smoke-deepseek-v4-flash \
  -prompt 'Reply with exactly: docker-smoke-ok'
```

## Start The Local Services

```sh
cd ai-agent-orch
docker compose --env-file ../.env.dev up -d governance-shell orchestrator
```

Check readiness:

```sh
curl http://127.0.0.1:18080/readyz
```

Create a governed session:

```sh
curl -H "Authorization: Bearer local-dev" \
  -H "Content-Type: application/json" \
  -d '{"agent":"test-generation","classification":"internal","prompt":"add regression tests for this module"}' \
  http://127.0.0.1:18080/v1/sessions
```

The host default is `18080`; `8080` is the internal container port. If you override `GOVERNANCE_SHELL_PORT`, use the same host port in curl, the CLI, and the VS Code Bridge setup.

The expected readiness response is:

```json
{"service":"governance-shell","status":"ready"}
```

If `/readyz` returns HTML, or a different JSON service name, the Bridge is pointing at another local app rather than this POC.

## CLI Smoke

The `ai-orch` CLI can exercise the governed path without VS Code.

```sh
docker compose --env-file ../.env.dev --profile tools run --rm ai-orch \
  ai-orch smoke --prompt 'Return only this JSON object and no markdown: {"protocolVersion":1,"patchId":"cli_smoke_patch","summary":"CLI smoke patch","files":[{"path":"SMOKE_TEST.md","action":"create","content":"CLI orchestration smoke passed."}]}'
```

The CLI receives sanitised patch metadata over SSE and records the patch decision by ID. Full patch content is fetched from the Governance Shell patch endpoint before review or apply.

If you are running the Governance Shell on an alternate host port, preserve the same port override for one-off Compose commands:

```sh
GOVERNANCE_SHELL_PORT=19080 docker compose --env-file ../.env.dev --profile tools run --rm ai-orch ai-orch smoke
```

## GPT-5.5 High-Reasoning Smoke

This is an explicit local validation path, not the default runtime policy.

```sh
AI_ORCH_MODEL_ALIAS_OVERRIDE=coding-gpt55 \
AI_ORCH_OPENROUTER_REASONING_EFFORT=high \
AI_ORCH_OPENROUTER_REASONING_EXCLUDE=true \
docker compose --env-file ../.env.dev up -d governance-shell orchestrator
```

Then run the CLI smoke command above.

## Model Compatibility Gateway

The local model compatibility gateway is exposed separately from the Governance Shell API. It is for runtimes that expect OpenAI-compatible model endpoints.

Start the local services:

```sh
cd ai-agent-orch
docker compose --env-file ../.env.dev up -d governance-shell orchestrator
```

List governed model aliases:

```sh
curl -H "Authorization: Bearer local-runtime-token" \
  'http://127.0.0.1:18082/v1/models?classification=internal'
```

Run a non-streaming chat-completions smoke call through the gateway:

```sh
curl -H "Authorization: Bearer local-runtime-token" \
  -H "X-AI-Orch-Session-ID: <session_id returned by /v1/sessions>" \
  -H "Content-Type: application/json" \
  -d '{"model":"coding-balanced","messages":[{"role":"user","content":"Reply with exactly: model-gateway-ok"}],"max_tokens":32}' \
  http://127.0.0.1:18082/v1/chat/completions
```

The `X-AI-Orch-Session-ID` header is required for model generation endpoints. `/v1/models` can be called without a session, but `/v1/chat/completions` and `/v1/responses` must attach to a governed session so audit evidence is correlated. The local Governance Shell validates that the session exists before model generation; stronger runtime-session token binding is a later hardening step.

The model gateway uses `AI_ORCH_RUNTIME_TOKEN`, not `AI_ORCH_DEV_TOKEN`. Runtime tokens belong to runtime adapters. Developer tokens belong to the CLI, VS Code Bridge and MCP tools.

## MCP Gateway

The MCP gateway is the portable client path for CLine, Claude Code, Codex, VS Code MCP clients and similar tools. It is not a replacement for the Governance Shell; it is a client-facing adapter that routes useful work into the Governance Shell.

Start a local HTTP MCP gateway for controlled local testing:

```sh
AI_ORCH_DEV_TOKEN=local-dev \
AI_ORCH_GOVERNANCE_URL=http://127.0.0.1:18080 \
go run ./cmd/ai-orch mcp start --transport http --host 127.0.0.1 --port 18081
```

HTTP transport fails closed when `AI_ORCH_DEV_TOKEN` is missing and only allows local browser origins. Prefer stdio for generated client configs because it avoids writing bearer tokens into project files:

```sh
AI_ORCH_DEV_TOKEN=local-dev \
AI_ORCH_GOVERNANCE_URL=http://127.0.0.1:18080 \
go run ./cmd/ai-orch mcp start --transport stdio
```

Generate project-local client guidance from the repo you want to test:

```sh
ai-orch mcp install --client codex
ai-orch mcp install --client claude-code
ai-orch mcp install --client vscode
ai-orch mcp install --client cline
```

The install command refuses to overwrite existing files by default. Use `--force` only when you have reviewed what will be replaced.

## VS Code Bridge

The Bridge scaffold lives at `ai-agent-orch/agent-bridge/`.

Build and install the VSIX after Bridge changes:

```sh
cd ai-agent-orch/agent-bridge
bun run test
bun run typecheck
bun run lint
bun run compile
bun run package
code --install-extension ai-agent-bridge.vsix
```

After reinstalling the VSIX, reload the VS Code window so new command contributions are picked up:

1. Open Command Palette.
2. Run `Developer: Reload Window`.

Start the local services before using the extension:

```sh
cd ai-agent-orch
docker compose --env-file ../.env.dev up -d governance-shell orchestrator
curl http://127.0.0.1:18080/readyz
```

If port `18080` is already in use, run the local stack on another port and enter that URL during Bridge setup:

```sh
GOVERNANCE_SHELL_PORT=19080 docker compose --env-file ../.env.dev up -d governance-shell orchestrator
curl http://127.0.0.1:19080/readyz
```

First-run onboarding in VS Code:

1. Open Command Palette.
2. Run `AI Agent: Setup AI Agent Bridge`.
3. Accept or enter the Governance Shell URL. Default: `http://127.0.0.1:18080`. Use `http://127.0.0.1:19080` if you started Compose with the alternate port above.
4. Enter an audit identity label. Default: `developer`.
5. Enter the developer token. Default local Compose token: `local-dev`.
6. Run `AI Agent: Check AI Agent Bridge Connection`.
7. Run `AI Agent: Invoke AI Agent`.

The setup command stores the developer token in VS Code SecretStorage. The URL and identity remain machine-scoped VS Code settings.

Bridge settings remain available for inspection:

- `aiAgentBridge.governanceUrl`: Governance Shell URL, default `http://127.0.0.1:18080`.
- `aiAgentBridge.devToken`: legacy local dev-token fallback; prefer the setup command so the token is stored in SecretStorage.
- `aiAgentBridge.identity`: local identity label sent as `X-AI-Orch-Local-Identity`.

When using a non-default Governance Shell port, set `aiAgentBridge.governanceUrl` in VS Code settings.

If `Invoke AI Agent` fails with `Create session failed: 404 {"detail":"Not Found"}`, the Bridge is probably pointed at another local service. On this machine, `metrics-new` can occupy `http://127.0.0.1:8080`. Run `AI Agent: Setup AI Agent Bridge` again and set the Governance Shell URL to the port whose `/readyz` response is `{"service":"governance-shell","status":"ready"}`.

Expected Bridge flow:

1. `Invoke AI Agent` asks for a prompt.
2. The Governance Shell creates and routes a governed session.
3. VS Code asks you to confirm the recommended specialist.
4. The Bridge listens to the session event stream.
5. If a patch is proposed, `Review Diff` opens native VS Code diffs.
6. `Apply`, `Mark Partially Applied`, or `Reject` records the patch decision in audit.

Use the VS Code Output panel named `AI Agent Bridge` for setup and runtime diagnostics.

## Agent Workflow Checklist

Use this checklist when validating the local agent experience. These are the workflows the POC should support today:

| Workflow | Entry point | Expected behaviour |
| --- | --- | --- |
| First-run setup | `AI Agent: Setup AI Agent Bridge` | Stores the Governance Shell URL and identity as machine settings, stores the developer token in VS Code SecretStorage, then checks readiness. |
| Connection check | `AI Agent: Check AI Agent Bridge Connection` | Confirms `/readyz`; if the local service is reachable but the token is missing, offers `Run Setup` immediately. |
| Governed invocation | `AI Agent: Invoke AI Agent` or `ai-orch smoke` | Creates a session, sends the prompt plus bounded current-workspace context, receives a specialist recommendation, and requires human confirmation before dispatch. |
| Event streaming | Bridge SSE listener or `ai-orch session events` | Streams sanitised session events; patch events expose metadata and patch IDs, not raw patch content. |
| Patch review | Bridge `Review Diff` prompt | Fetches full buffered patch content from the Governance Shell, validates paths, and opens native VS Code diffs. |
| Patch decision | Bridge `Apply`, `Mark Partially Applied`, or `Reject`; CLI smoke auto-decision | Records an explicit patch decision against `/v1/sessions/{id}/patch-decision`. |
| Audit lookup | `AI Agent: Show Audit Link` or CLI audit step | Fetches the governed session audit trail without exposing the raw prompt or provider secrets. |
| Provider health | `openrouter-smoke` | Tests OpenRouter availability directly; this is provider health, not the full governed orchestration path. |

Current context shape: the Bridge packages the current workspace name, git branch and origin remote where available, active file metadata, and either the selected text or a capped active-file excerpt. It does not scan the whole repo or automatically fill the model context window with every file.

Current agent-plane limitation: the Bridge is not yet a CLine-style code-agent UI. It is a governed invocation and patch-review surface. A fuller IDE-native experience would need chat history, explicit workspace context selection, file references, terminal/tool approvals, streaming model output, and an adapter/runtime strategy behind the Governance Shell.

## VS Code Bridge Role

The VS Code Bridge is optional. It is useful for first-party onboarding, connection checks, bounded workspace context and native patch review, but it should not be required for the governance boundary to work. The portable path is MCP plus the model compatibility gateway.

## MCP Stubs

Phase 1 MCP stubs run with the default Compose services.

Phase 2 read-only MCP stubs are profile-gated:

```sh
docker compose --env-file ../.env.dev --profile phase2 up -d
```

`oauth-user` MCP calls fail closed until real user OAuth token acquisition exists. There is no fallback to a shared platform token for that mode.

## Policy Toggles

Cost-cap enforcement is off by default:

```sh
AI_ORCH_COST_CAP_ENABLED=false
AI_ORCH_SESSION_COST_CAP_USD=0
```

To test the blocking path:

```sh
AI_ORCH_COST_CAP_ENABLED=true
AI_ORCH_SESSION_COST_CAP_USD=0.50
```

Tool-loop enforcement is on by default:

```sh
AI_ORCH_CONSECUTIVE_TOOL_CALL_MAX=15
```

## Local State

The local POC uses SQLite and process-local state. SQLite stores audit, sessions, and the registry when configured through the Compose audit database path.

Some state is intentionally still local-process state:

- prompt cache;
- patch buffer;
- SSE history;
- cancellation map;
- kill switches;
- OAuth token stubs;
- composition state.

See [Local State Lifecycle](ai-agent-orch/docs/local-state-lifecycle.md) for the promotion path before team or multi-instance use.
