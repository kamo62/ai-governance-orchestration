# HANDOFF

Updated: 2026-07-15. Next session: Monday 2026-07-20 (CIO demo day).

## Current state

- Branch `feat/governed-client-onboarding`, head `85ca4f8`, pushed. CI fully green (go, bridge, beta-smoke). Draft PR #6 open against main.
- Version `v0.23.1-beta` aligned across `VERSION`, `internal/appversion/version.go`, and `agent-bridge/package.json`. Go toolchain 1.26.5 (clears GO-2026-5856).
- Live evidence on-ramp complete: an `air_` runtime credential (actor `kamogelo`, client `neokod`, device `neokod-demo`) is installed in both `~/.neokod/dev/settings.json` and `~/.neokod/userdata/settings.json` under `providerInstances.githubCopilot.config.managedClientEvidence` with `enabled: true`, `gatewayEnabled: false`, `governanceUrl: http://127.0.0.1:18080`. Evidence lane verified live with a 202 (accepted=1).
- Local stack: docker compose project `ai-orch-copilot` on port 18080 (governance-shell and orchestrator healthy). The credential is bound to this stack; it does not work against the isolated `ai-orch-cio-demo` project.
- Full on-ramp documentation: `ai-agent-orch/docs/deployment.md` ("Managed-client live evidence on-ramp") and `ai-agent-orch/docs/runtime-client-integration.md` ("Managed-Client Runtime Credential").

## Remaining before the demo

1. Rehearsal via `ai-agent-orch/scripts/cio-demo-verify.sh` has not yet run against branch head. Two stale volumes from June 12 (`ai-orch-cio-demo_bifrost-data`, created with an unrecorded ephemeral encryption key, and `ai-orch-cio-demo_audit-data` with old seed data) can break bifrost on re-run. Recommended clean slate first:

   ```sh
   docker volume rm ai-orch-cio-demo_bifrost-data ai-orch-cio-demo_audit-data
   echo "BIFROST_ENCRYPTION_KEY=$(openssl rand -hex 16)" >> .env.dev
   ```

   Pinning the key in `.env.dev` (gitignored) keeps every later run compatible with the persisted bifrost volume. Then:

   ```sh
   cd ai-agent-orch && ./scripts/cio-demo-verify.sh
   ```

   Success ends with "CIO demo stack is ready." UI at http://127.0.0.1:18081/ui/.

2. Demo day: the `ai-orch-copilot` project must be running for the live Neokod lane. After any restart, rerun `ai-agent-orch/scripts/local-copilot-compose-up.sh`; the installed credential keeps working. Neokod test-connection should return 202.

## Open decisions

- `gatewayEnabled` is deliberately false in the Neokod settings (recording-only posture). Flipping it routes MCP through the model gateway. Never select the foundry backend live; it fails closed by design.
- Changelog: the `v0.23.1-beta` entry sits above the unreleased `v0.23.0-beta` section. Decide whether to fold both into a `v0.24.0-beta`.
- PR #6 is a draft. Marking it ready for review triggers CodeRabbit. Merge decision after that.

## Deferred, only on explicit direction

Generic work queue; evidence conflict resolution via authority ordering; router/classifier confidence unification; UI mock mode; t3code secret-store migration; ai-orch native actor registry (SSO/RBAC lane) to replace the GitHub-enrolment dependency in credential minting.

## Security notes

- The `air_` credential sits in plaintext in both Neokod settings files until the secret-store migration. Never show it on a projected screen and never commit it.
- `.env.dev` holds live provider API keys and is gitignored. Keep it out of screen shares.
