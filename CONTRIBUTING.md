# Contributing

Thanks for your interest in the project. This is a working proof of concept for a governance control plane around AI-assisted engineering. Contributions are welcome, especially ones that harden the governance boundary, improve operability, or add adapters for new runtimes and providers.

## Repository layout

- `ai-agent-orch/` holds the Go module (`github.com/kamo62/ai-governance-orchestration/ai-agent-orch`): the Governance Shell, Orchestrator, model gateway, MCP gateway, CLI, and supporting packages.
- `ai-agent-orch/agent-bridge/` is the optional VS Code extension (Bun + TypeScript).
- `ai-agent-orch/agents/`, `models/`, `policies/`, `mcp/` are the governed catalog: agent definitions, model registry, policy files, and MCP registrations.

## Prerequisites

- Go 1.26 or later.
- Docker with Compose (for the local stack and smoke tests).
- Bun (only if you work on the VS Code bridge).

## Development loop

The Makefile at the repo root mirrors CI:

```bash
make build        # compile all binaries into ai-agent-orch/bin/
make test         # run the Go test suite
make lint         # gofmt check, go vet, staticcheck
make catalog      # validate the agent/model/policy catalog
make up           # start the local stack with Docker Compose
make smoke        # run the end-to-end beta smoke against the running stack
make bridge-test  # test, typecheck, and lint the VS Code bridge
make clean        # remove build outputs
```

Before opening a pull request, run `make build test lint catalog`. CI runs the same checks plus govulncheck and a Compose-based smoke test.

## Conventions

- Keep packages small and focused. Shared helpers live in `internal/httpx`, `internal/envx`, `internal/logx`, and `internal/sqlitex`; do not duplicate them.
- Fail closed. Policy, auth, and audit paths must deny on error, never silently allow.
- Every governance-relevant action needs an audit event. If you add a new enforcement path, add the audit write and a test that asserts it.
- Tests live next to the code. Handler tests use `httptest`; avoid network calls in unit tests.
- Local state stores open through `internal/sqlitex` so pragmas and pooling stay consistent.

## Pull requests

- Keep PRs focused on one change.
- Describe the governance impact: what is enforced, what is audited, what fails closed.
- Update `changelog.md` for user-visible changes.

## Extension points

The POC deliberately ships without organization auth and with SQLite-backed local state. Both are designed to be replaced:

- Identity: `governance.RequestAuthorizer` is the seam for OIDC or any other token validator (see `internal/httpauth`).
- Storage: every store is constructed behind an interface in `cmd/governance-shell`; a Postgres implementation can be wired without touching handlers.
- Model providers: `internal/modelbackend` defines the backend interface; Bifrost, OpenRouter, and per-user Copilot are the current implementations.
- Runtimes: `internal/dispatch.Runtime` is the contract for new agent runtimes.
