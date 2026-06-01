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
AI_ORCH_SERVICE_TOKEN=local-service-token
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
curl http://127.0.0.1:8080/readyz
```

Create a governed session:

```sh
curl -H "Authorization: Bearer local-dev" \
  -H "Content-Type: application/json" \
  -d '{"agent":"test-generation","classification":"internal","prompt":"add regression tests for this module"}' \
  http://127.0.0.1:8080/v1/sessions
```

If port `8080` is busy, set `GOVERNANCE_SHELL_PORT` in `.env.dev` or your shell before starting Compose.

## CLI Smoke

The `ai-orch` CLI can exercise the governed path without VS Code.

```sh
docker compose --env-file ../.env.dev --profile tools run --rm ai-orch \
  ai-orch smoke --prompt 'Return only this JSON object and no markdown: {"protocolVersion":1,"patchId":"cli_smoke_patch","summary":"CLI smoke patch","files":[{"path":"SMOKE_TEST.md","action":"create","content":"CLI orchestration smoke passed."}]}'
```

The CLI receives sanitised patch metadata over SSE and records the patch decision by ID. Full patch content is fetched from the Governance Shell patch endpoint before review or apply.

## GPT-5.5 High-Reasoning Smoke

This is an explicit local validation path, not the default runtime policy.

```sh
AI_ORCH_MODEL_ALIAS_OVERRIDE=coding-gpt55 \
AI_ORCH_OPENROUTER_REASONING_EFFORT=high \
AI_ORCH_OPENROUTER_REASONING_EXCLUDE=true \
docker compose --env-file ../.env.dev up -d governance-shell orchestrator
```

Then run the CLI smoke command above.

## VS Code Bridge

The Bridge scaffold lives at `ai-agent-orch/agent-bridge/`.

Build and install the VSIX:

```sh
cd ai-agent-orch/agent-bridge
bun run typecheck
bun run lint
bun run compile
bun run package
code --install-extension ai-agent-bridge.vsix
```

Bridge settings:

- `aiAgentBridge.governanceUrl`: Governance Shell URL, default `http://127.0.0.1:8080`.
- `aiAgentBridge.devToken`: local dev token. Falls back to `AI_ORCH_DEV_TOKEN`.
- `aiAgentBridge.identity`: local identity label sent as `X-AI-Orch-Local-Identity`.

When using a non-default Governance Shell port, set `aiAgentBridge.governanceUrl` in VS Code settings.

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
