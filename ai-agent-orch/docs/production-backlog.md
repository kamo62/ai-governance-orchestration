# Production Backlog

Open hardening items distilled from the 2026-06-09/10 production review. Everything else from that review is implemented; history lives in git (`fable.md` was removed once its findings landed).

## Secrets

- Rotate the provider keys in the local `.env.dev` (they circulated during testing) and move shared deployments to a secret manager.
- Replace env-var-derived AES keys for the Copilot and OAuth token stores with KMS/Key Vault before a shared deployment holds real user tokens.
- Enable Bifrost inference auth (`enforce_auth_on_inference`) before Bifrost is reachable beyond the shell.

## State

- `EventStore` (SSE history) and the composition store are in-memory by design; document as ephemeral or persist before multi-instance use.
- Add a startup backfill that re-prices historical sessions with token counts but `cost_source: unavailable` from the current pricing table (derived data only; never mutate hash-chained audit events).
- Capture Copilot AI-unit pricing so `copilot-*` sessions stop reporting `unavailable` (co-pilot.md section 11).

## Operations

- Structured logging and trace propagation from gateway call to audit event (request IDs exist; correlation into audit does not).
- Release pipeline: tagged images, container scanning, SBOM.
- Deep identity/durability review of the orchestrator and VS Code bridge.
- Replace localStorage admin tokens in the console with an OIDC-backed operator role and a role/status endpoint.

## Agents

- Enforce `per_invocation_cap_usd` at dispatch (currently declared in agent configs but never read) or remove it from the configs.
- Build a minimal eval runner that executes `evals/golden-cases.yaml` through the gateway so `required_for_phase0` means something.
