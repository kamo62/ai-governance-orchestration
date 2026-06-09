# Deployment And Local Run Guide

This document explains how to run and verify the local AI Agent Orchestration POC.

The root [README.md](README.md) explains the aim of the project. This file is intentionally more operational.

## Current Deployment Shape

The runnable scaffold lives in `ai-agent-orch/`.

The local Compose stack runs:

- `governance-shell`: public local API and policy boundary.
- `orchestrator`: internal runtime orchestration service.
- `bifrost`: internal OSS provider gateway sidecar used by the Governance Shell by default.
- `agentgateway`: optional provider gateway sidecar when using `docker-compose.agentgateway.yml`.
- optional MCP stub services.
- optional tools such as `catalog-validator`, `openrouter-smoke`, `opencode-sandbox`, and `ai-orch`.

The Orchestrator is intentionally internal to Docker Compose. Provider credentials belong to the Governance Shell or the selected provider gateway, never to Orchestrator, the VS Code Bridge, MCP clients or runtime containers. Runtime model calls go through the ai-orch model gateway; they do not call Bifrost, AgentGateway, OpenRouter, or provider APIs directly.

## Prerequisites

- Go 1.26 or the repo's Docker build.
- Docker Desktop or compatible Docker Compose.
- Bun for the VS Code Bridge.
- VS Code if testing the Bridge.
- An OpenRouter key only for provider smoke tests or model-backed orchestration.

Keep local secrets in the ignored root `.env.dev` file. Do not commit it.

For the default Compose path, the only required secret is:

```sh
OPENROUTER_API_KEY=...
```

The local tokens and ports have Compose defaults. Add these fields only when you want to override those defaults locally:

```sh
AI_ORCH_DEV_TOKEN=local-dev
AI_ORCH_ADMIN_TOKEN=local-admin
AI_ORCH_SERVICE_TOKEN=local-service-token
AI_ORCH_RUNTIME_TOKEN=local-runtime-token
AI_ORCH_MODEL_GATEWAY_URL=http://127.0.0.1:18082
AI_ORCH_MODEL_BACKEND=bifrost
AI_ORCH_MODEL_PRICING_REFRESH_INTERVAL=24h
AI_ORCH_BIFROST_BASE_URL=http://bifrost:8080
AI_ORCH_AGENTGATEWAY_BASE_URL=
AI_ORCH_AGENTGATEWAY_API_KEY=
AI_ORCH_AGENTGATEWAY_READINESS_URL=
AI_ORCH_TRUSTED_CLIENT_TOKEN=local-trusted-client-token
BIFROST_ENCRYPTION_KEY=local-bifrost-enc-key-32-bytes!!
```

`AI_ORCH_TRUSTED_CLIENT_TOKEN` is the secret that lets trusted clients (the MCP gateway and the VS Code bridge) record privileged audit trust levels (`gateway_enforced`, `managed_client`). The Governance Shell reads it, and trusted clients present it via the `X-AI-Orch-Trusted-Client-Token` header. When it is empty the shell falls back to honoring the `X-AI-Orch-Client` identity header on its own, which is convenient for local dev but means any token holder can claim a privileged trust level. Set a strong value in shared and production deployments, and configure the same value on the gateway/bridge, so trust labels on the audit trail cannot be forged.

Set `AI_ORCH_REQUIRE_WORK_ITEM=true` in shared beta and production-like environments. With this gate enabled, governed runs fail closed unless the current branch or request metadata supplies a work item ID, and the branch contains that ID. Use branch names like `feature/OMENG-300-governance-fixes`, `bugfix/OMENG-301-null-session`, or `docs/OMENG-302-audit-export`.

Optional provider credentials for Bifrost can be added only when you are deliberately testing those paths:

```sh
OPENAI_API_KEY=...
ANTHROPIC_API_KEY=...
DEEPSEEK_API_KEY=...
AWS_ACCESS_KEY_ID=...
AWS_SECRET_ACCESS_KEY=...
AWS_REGION=...
```

## Beta Verification (recommended)

From `ai-agent-orch/`:

```sh
./scripts/beta-verify.sh
```

This runs unit tests, catalog validation, and (when Docker is available) the Compose beta profile:

- `bifrost`, `orchestrator`, `governance-shell`
- `beta-catalog` (catalog-validator)
- `beta-smoke` (`ai-orch smoke` with `AI_ORCH_BETA_SMOKE=true`, no provider API keys)

Manual Compose beta smoke:

```sh
cd ai-agent-orch
docker compose -f docker-compose.yml -f docker-compose.beta.yml --profile beta up -d bifrost orchestrator governance-shell
docker compose -f docker-compose.yml -f docker-compose.beta.yml --profile beta run --rm beta-smoke
```

Team-local durable storage is the default Compose path: `AI_ORCH_AUDIT_PATH=/app/var/audit/audit.db` enables SQLite audit, sessions, registry and model-pricing persistence via the `audit-data` volume. Governance Shell refreshes the OpenRouter model-pricing table on startup and then every `AI_ORCH_MODEL_PRICING_REFRESH_INTERVAL` interval, defaulting to 24 hours.

API contract for integrators: [ai-agent-orch/docs/api-contract-v1.md](ai-agent-orch/docs/api-contract-v1.md).

### CIO demo verification

Use this when the stack needs to be proved and left running for a walkthrough:

```sh
cd ai-agent-orch
./scripts/cio-demo-verify.sh
```

The verifier builds the beta images, starts an isolated Compose project, waits for readiness, validates the agent catalogue, runs the no-provider-key governed smoke, checks protected system status, checks metrics, confirms the UI is served, and then leaves the demo stack running.

Defaults:

- Compose project: `ai-orch-cio-demo`
- Governance UI: `http://127.0.0.1:18081/ui/`
- Model gateway: `http://127.0.0.1:18083/v1`
- Developer token: `AI_ORCH_DEV_TOKEN` from the environment or `.env.dev`, falling back to `local-dev`

Override the defaults when another stack already uses those ports:

```sh
AI_ORCH_CIO_PROJECT=ai-orch-cio-demo-2 \
GOVERNANCE_SHELL_PORT=19081 \
MODEL_GATEWAY_PORT=19083 \
./scripts/cio-demo-verify.sh
```

Cleanup after the walkthrough:

```sh
docker compose -p ai-orch-cio-demo -f docker-compose.yml -f docker-compose.beta.yml --profile beta down --remove-orphans
```

### Provider-backed smoke (optional, requires OpenRouter)

```sh
cd ai-agent-orch
# OPENROUTER_API_KEY must be set in ../.env.dev or the environment
docker compose -f docker-compose.yml -f docker-compose.provider.yml --profile provider up -d bifrost orchestrator governance-shell
docker compose -f docker-compose.yml -f docker-compose.provider.yml --profile provider run --rm provider-gateway-smoke
docker compose -f docker-compose.yml -f docker-compose.provider.yml --profile provider run --rm provider-run-smoke
```

`provider-gateway-smoke` is the OpenCode-style path (governed run + `/v1/chat/completions` on the model gateway). `provider-run-smoke` proves full orchestrator dispatch with a live model response and patch envelope.

CI runs this nightly when `OPENROUTER_API_KEY` is configured as a repository secret.

### Team-local OIDC (optional)

```sh
export OIDC_ISSUER_URL=https://login.microsoftonline.com/<tenant>/v2.0
export OIDC_CLIENT_ID=<application-client-id>
docker compose -f docker-compose.yml -f docker-compose.team-beta.yml up -d bifrost orchestrator governance-shell
```

When OIDC is set, human clients must present a valid ID token; `AI_ORCH_DEV_TOKEN` is not accepted for those requests.

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
docker compose --env-file ../.env.dev pull bifrost
docker compose --env-file ../.env.dev --profile tools build governance-shell orchestrator catalog-validator openrouter-smoke ai-orch
```

Run the containerised catalogue validator:

```sh
docker compose --env-file ../.env.dev --profile tools run --rm catalog-validator
```

Run the direct OpenRouter provider smoke test:

```sh
docker compose --env-file ../.env.dev --profile tools run --rm openrouter-smoke \
  openrouter-smoke -catalog-root /app \
  -model-alias smoke-deepseek-v4-flash \
  -prompt 'Reply with exactly: docker-smoke-ok'
```

That smoke test intentionally bypasses Bifrost. It checks provider availability, not the full governed orchestration path.

## Start The Local Services

Start the default Bifrost-backed stack:

```sh
cd ai-agent-orch
docker compose --env-file ../.env.dev up -d bifrost orchestrator governance-shell
```

Check readiness:

```sh
curl http://127.0.0.1:18080/readyz
docker compose --env-file ../.env.dev exec bifrost wget -qO- http://127.0.0.1:8080/health
```

Open the local Governance UI:

```text
http://127.0.0.1:18080/ui/
```

The UI loads without a token, then uses the developer token for protected governance data. Use the admin token only for admin endpoints. Do not put provider credentials into the UI.

Start a governed run:

```sh
curl -H "Authorization: Bearer local-dev" \
  -H "Content-Type: application/json" \
  -d '{"agent":"unit-tests","classification":"internal","prompt":"add regression tests for this module","permission_mode":"reviewed","approval_mode":"manual","workspace_mode":"local"}' \
  http://127.0.0.1:18080/v1/runs
```

The host default is `18080`; `8080` is the internal container port. If you override `GOVERNANCE_SHELL_PORT`, use the same host port in curl, the CLI, and the VS Code Bridge setup.

The expected readiness response is:

```json
{"service":"governance-shell","status":"ready"}
```

If `/readyz` returns HTML, or a different JSON service name, the Bridge is pointing at another local app rather than this POC.

## Governance UI

The first local Governance UI is intentionally small and operational:

- service and version posture;
- active gateway backend plus Bifrost, AgentGateway and native OpenRouter options;
- metrics counters for sessions, cache, evidence and patch decisions;
- recent sessions with model attribution, token counts, cost source and readable runtime mode labels;
- agent catalogue preview;
- evidence, cache and maturity export views;
- audit lookup by governed session ID;
- basic use-case and workflow registration.

Run it from the Governance Shell:

```text
http://127.0.0.1:18080/ui/
```

The page stores local UI connection settings in browser local storage. API calls still require the normal bearer tokens, and provider keys never belong in the browser.

## CLI Smoke

The `ai-orch` CLI can exercise the governed path without VS Code. With the default Compose settings, this path uses the Bifrost backend through the Governance Shell.

```sh
docker compose --env-file ../.env.dev --profile tools run --rm ai-orch \
  ai-orch smoke --prompt 'Return only this JSON object and no markdown: {"protocolVersion":1,"patchId":"cli_smoke_patch","summary":"CLI smoke patch","files":[{"path":"SMOKE_TEST.md","action":"create","content":"CLI orchestration smoke passed."}]}'
```

The CLI receives sanitised patch metadata over SSE and records the patch decision by ID. In the CLI smoke path, `applied` means "decision recorded", not "workspace file changed". Full patch content is fetched from the Governance Shell patch endpoint before a Bridge review/apply flow changes local files.

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
docker compose --env-file ../.env.dev up -d bifrost orchestrator governance-shell
```

Then run the CLI smoke command above.

## Model Compatibility Gateway

The local model compatibility gateway is exposed separately from the Governance Shell API. It is for runtimes that expect OpenAI-compatible model endpoints.

The model compatibility gateway is the only runtime-facing model endpoint. Bifrost is private Compose plumbing behind the Governance Shell and has no host port by default.

The local Bifrost config is intentionally headless and no-content-logging. ai-orch owns the audit trail; Bifrost should not become a second raw prompt/response store in this phase.

Start the local services:

```sh
cd ai-agent-orch
docker compose --env-file ../.env.dev up -d bifrost orchestrator governance-shell
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

For streamed chat completions, ai-orch asks the selected backend for stream usage and records any returned prompt/output/total token counts and provider-reported cost on the session audit trail. If cost is missing but tokens and model pricing are available, session reporting estimates cost from the stored model-pricing table.

The model gateway uses `AI_ORCH_RUNTIME_TOKEN`, not `AI_ORCH_DEV_TOKEN`. Runtime tokens belong to runtime adapters. Developer tokens belong to the CLI, VS Code Bridge and MCP tools.

### GitHub Copilot User Backend

The experimental Copilot backend is per-user and actor-bound. It is intended for internal beta use where developers already have Copilot seats.

CLI setup:

```sh
export AI_ORCH_COPILOT_TOKEN_ENCRYPTION_KEY='<local secret>'
ai-orch copilot login
ai-orch copilot status
ai-orch copilot models
```

Gateway setup:

```sh
AI_ORCH_MODEL_BACKEND=copilot-user
AI_ORCH_COPILOT_TOKEN_DB=~/.ai-orch/copilot-tokens.db
AI_ORCH_COPILOT_TOKEN_ENCRYPTION_KEY='<same local secret>'
```

OpenCode still points only at ai-orch. Use governed aliases such as `ai-orch/copilot-gpt-5-mini`. Do not configure OpenCode to call `github-copilot` directly in governed mode.

### Backend Control From The UI

The Governance UI can start and stop model sidecars when backend control is explicitly enabled. This requires Docker access inside the Governance Shell container.

Local trusted setup:

```sh
docker compose \
  -f docker-compose.yml \
  -f docker-compose.backend-control.yml \
  up -d governance-shell orchestrator
```

The override mounts `/var/run/docker.sock`, runs `governance-shell` as root, and enables:

```text
AI_ORCH_BACKEND_CONTROL_ENABLED=true
AI_ORCH_BACKEND_CONTROL_WORKDIR=/app
```

Use an admin token in the UI. Backend actions call `POST /v1/backends` and run the corresponding `docker compose` command from the container. Do not enable this mode in untrusted shared deployments without a dedicated sidecar supervisor or Docker API proxy.

Backend selection is controlled by:

```sh
AI_ORCH_MODEL_BACKEND=bifrost
AI_ORCH_BIFROST_BASE_URL=http://bifrost:8080
```

Use `AI_ORCH_MODEL_BACKEND=native-openrouter` only when you intentionally want to bypass the Bifrost sidecar and use the native OpenRouter backend.

For a concrete native OpenRouter run path:

```sh
docker compose --env-file ../.env.dev \
  -f docker-compose.yml \
  -f docker-compose.openrouter.yml \
  up -d orchestrator governance-shell
```

Use `AI_ORCH_MODEL_BACKEND=agentgateway` when testing AgentGateway as an additional provider gateway candidate:

```sh
AI_ORCH_MODEL_BACKEND=agentgateway
AI_ORCH_AGENTGATEWAY_BASE_URL=http://agentgateway:3000
# Optional, only when agentgateway is configured to require an API key.
AI_ORCH_AGENTGATEWAY_API_KEY=...
# Optional, when the agentgateway readiness server is exposed to this container.
AI_ORCH_AGENTGATEWAY_READINESS_URL=http://agentgateway:15021/healthz/ready
```

For a concrete AgentGateway run path:

```sh
docker compose --env-file ../.env.dev \
  -f docker-compose.yml \
  -f docker-compose.agentgateway.yml \
  up -d agentgateway orchestrator governance-shell
```

The runtime-facing endpoint still stays ai-orch:

```text
OpenCode / Cline / workbench
  -> ai-orch model compatibility gateway
  -> Governance Shell session, routing and audit
  -> selected gateway plumbing
  -> provider
```

Bifrost, AgentGateway and native OpenRouter are selectable local gateway paths. Keep them behind ai-orch until each path proves model-call audit, provider/backend metadata, patch/diff evidence and no provider keys in runtime config.

### Local OpenCode E2E

The runtime E2E now uses local OpenCode as an existing developer tool, not as a centrally hosted repo browser.

Target shape:

```text
OpenCode running in a local repo
  -> ai-orch model compatibility gateway
  -> OpenCode ACP file/terminal permissions in the local workspace
  -> Governance Shell audit, patch and evidence records
```

This proves the organisation-scale idea: managed policy can point developer tools at the Governance Shell as the provider endpoint, while repository access stays inside the developer's normal workspace.

Install or patch local OpenCode config with the documented custom-provider shape:

```sh
cd ai-agent-orch

# macOS/Linux global config: ~/.config/opencode/opencode.json
./scripts/install-opencode-ai-orch.sh --scope global

# Project config: ./opencode.json
./scripts/install-opencode-ai-orch.sh --scope project

# Explicit path
go run ./cmd/opencode-smoke install-config --path /tmp/opencode-ai-orch.json
```

Windows PowerShell:

```powershell
cd ai-agent-orch

# User-wide config: %USERPROFILE%\\.config\\opencode\\opencode.json
.\\scripts\\install-opencode-ai-orch.ps1 -Scope global

# Project config
.\\scripts\\install-opencode-ai-orch.ps1 -Scope project
```

Existing files are backed up before writing. Existing unrelated OpenCode settings are preserved. A different existing `provider.ai-orch` block is not overwritten unless `--force` / `-Force` is supplied.

Run the local OpenCode E2E against the CIO demo stack:

```sh
mkdir -p /tmp/ai-orch-opencode-e2e
AI_ORCH_GOVERNANCE_URL=http://127.0.0.1:18081 \
AI_ORCH_MODEL_GATEWAY_URL=http://127.0.0.1:18083 \
AI_ORCH_DEV_TOKEN=local-dev \
AI_ORCH_RUNTIME_TOKEN=local-runtime-token \
OPENCODE_EXPECT=opencode-e2e-ok \
go run ./cmd/opencode-smoke e2e --dir /tmp/ai-orch-opencode-e2e
```

The E2E creates a governed session, runs the local `opencode` binary with `OPENCODE_CONFIG`, `AI_ORCH_RUNTIME_TOKEN` and `AI_ORCH_SESSION_ID`, then verifies the session audit contains `model.gateway` events. ACP runs additionally emit durable runtime events and patch evidence from file-write hooks or before/after workspace diffs.

Use `/Users/kamogelo/Code/ado_scripts` as a realistic repo target only through a disposable worktree or a deliberately temporary test file.

The implemented E2E verifies model routing, audit evidence and ACP patch/diff evidence:

1. **Read-only model routing.** OpenCode performs a review/explanation task. The model call appears in ai-orch audit with session ID, alias, provider/backend, usage and trust label.
2. **Patch evidence.** OpenCode changes a disposable file or worktree. ai-orch records patch/diff metadata, buffers patch content where available and records the human patch decision.
3. **ACP permissions.** OpenCode ACP permissions are accepted in the ACP lane by default. MCP is not injected through ACP; MCP calls must use the MCP gateway route.

The generated OpenCode config should point at the ai-orch model gateway, not Bifrost or a provider directly. `opencode-smoke generate-config` emits the local config shape, including the required runtime token and session header placeholders:

Expected config shape once verified:

```json
{
  "$schema": "https://opencode.ai/config.json",
  "provider": {
    "ai-orch": {
      "npm": "@ai-sdk/openai-compatible",
      "name": "AI Orch Governed Router",
      "options": {
        "baseURL": "http://127.0.0.1:18082/v1",
        "apiKey": "{env:AI_ORCH_RUNTIME_TOKEN}",
        "headers": {
          "X-AI-Orch-Session-ID": "{env:AI_ORCH_SESSION_ID}"
        }
      },
      "models": {
        "coding-balanced": {
          "name": "Governed Coding Balanced"
        }
      }
    }
  },
  "enabled_providers": ["ai-orch"],
  "model": "ai-orch/coding-balanced"
}
```

The runtime token must be an ai-orch runtime token. Do not put OpenRouter, OpenAI, Anthropic, DeepSeek, AWS or Bifrost credentials into OpenCode for this E2E.

When you do not want to use a local OpenCode install, run the opt-in Docker sandbox against the Compose network. Create or choose a governed session first, then pass that session ID into the sandbox:

```sh
AI_ORCH_SESSION_ID=<governed-session-id> \
docker compose --env-file ../.env.dev --profile opencode run --rm opencode-sandbox
```

The sandbox uses `ai-agent-orch/opencode/sandbox/opencode.json`, talks to `http://governance-shell:18082/v1`, and writes only inside the `opencode-sandbox-workspace` Docker volume unless you deliberately change the Compose mount.

### Bifrost Provider Routing Smoke

The current local Bifrost config supports these governed smoke aliases:

| Alias | Provider | Purpose |
| --- | --- | --- |
| `smoke-openai-gpt4o-mini` | `openai` | Direct OpenAI credential route through Bifrost. |
| `smoke-anthropic-haiku` | `anthropic` | Direct Anthropic Haiku credential route through Bifrost. |
| `smoke-deepseek-chat` | `deepseek` | Direct DeepSeek credential route through Bifrost. |
| `smoke-deepseek-v4-flash` | `openrouter` | OpenRouter-routed DeepSeek provider-health path. |

For direct model-gateway checks, create a governed session first, then call `/v1/chat/completions` with one of the aliases and the returned session ID.

For a full CLI orchestration smoke with a temporary provider override, recreate Orchestrator with the alias and then run the CLI tool without recreating dependencies:

```sh
AI_ORCH_MODEL_ALIAS_OVERRIDE=smoke-openai-gpt4o-mini \
GOVERNANCE_SHELL_PORT=19080 \
MODEL_GATEWAY_PORT=19082 \
docker compose --env-file ../.env.dev up -d --force-recreate orchestrator governance-shell

GOVERNANCE_SHELL_PORT=19080 \
MODEL_GATEWAY_PORT=19082 \
docker compose --env-file ../.env.dev --profile tools run --no-deps --rm ai-orch ai-orch smoke
```

Remove the `AI_ORCH_MODEL_ALIAS_OVERRIDE` environment value and recreate Orchestrator again when you want to return to the default agent model.

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
docker compose --env-file ../.env.dev up -d bifrost orchestrator governance-shell
curl http://127.0.0.1:18080/readyz
```

If port `18080` is already in use, run the local stack on another port and enter that URL during Bridge setup:

```sh
GOVERNANCE_SHELL_PORT=19080 docker compose --env-file ../.env.dev up -d bifrost orchestrator governance-shell
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
| Governed invocation | `AI Agent: Invoke AI Agent` or `ai-orch smoke` | Starts a governed run, sends bounded current-workspace context plus branch/work-item hints, receives a specialist recommendation, and requires human confirmation before dispatch. |
| Event streaming | Bridge SSE listener or `ai-orch session events` | Streams sanitised session events; patch events expose metadata and patch IDs, not raw patch content. |
| Patch review | Bridge `Review Diff` prompt | Fetches full buffered patch content from the Governance Shell, validates paths, and opens native VS Code diffs. |
| Patch decision | Bridge `Apply`, `Mark Partially Applied`, or `Reject`; CLI smoke auto-decision | Records an explicit patch decision against `/v1/sessions/{id}/patch-decision`. Bridge `Apply` mutates the local workspace; CLI `applied` only records the decision. |
| Audit lookup | `AI Agent: Show Audit Link` or CLI audit step | Fetches the governed session audit trail without exposing the raw prompt or provider secrets. |
| Provider health | `openrouter-smoke` | Tests OpenRouter availability directly; this is provider health, not the full governed orchestration path. |
| Bifrost health | Compose `bifrost` health check | Confirms the internal provider-plumbing sidecar is reachable by the Governance Shell. |

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

The local POC uses SQLite and process-local state. SQLite stores audit, sessions, registry data, and model pricing when configured through the Compose audit database path.

Some state is intentionally still local-process state:

- prompt cache;
- patch buffer;
- SSE history;
- cancellation map;
- kill switches;
- OAuth token stubs;
- composition state.

See [Local State Lifecycle](ai-agent-orch/docs/local-state-lifecycle.md) for the promotion path before team or multi-instance use.
