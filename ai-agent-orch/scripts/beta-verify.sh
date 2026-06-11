#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

echo "==> Go tests"
go test ./...

echo "==> Go vet"
go vet ./...

echo "==> Catalog validation"
go run ./cmd/catalog-validator -catalog-root .

echo "==> Format check"
if [ -n "$(gofmt -l . | grep -v node_modules || true)" ]; then
  echo "gofmt would reformat:"
  gofmt -l . | grep -v node_modules || true
  exit 1
fi

if command -v docker >/dev/null 2>&1 && docker compose version >/dev/null 2>&1; then
  echo "==> Docker Compose beta smoke (no provider keys required)"
  ENV_FILE="${ROOT}/../.env.dev"
  COMPOSE=(docker compose -p ai-agent-orch-beta-verify -f docker-compose.yml -f docker-compose.beta.yml)
  if [ -f "$ENV_FILE" ]; then
    COMPOSE+=(--env-file "$ENV_FILE")
  fi
  cleanup_beta_compose() {
    "${COMPOSE[@]}" --profile beta down -v --remove-orphans >/dev/null 2>&1 || true
  }
  trap cleanup_beta_compose EXIT
  cleanup_beta_compose
  BETA_PORT="${GOVERNANCE_SHELL_PORT:-18081}"
  BETA_GATEWAY_PORT="${MODEL_GATEWAY_PORT:-18083}"
  # The beta verifier must not inherit machine-local backend selection from
  # .env.dev: copilot-user without an enrolled token resolver fails at startup,
  # and per-user backends are not part of the no-provider-key beta contract.
  # Shell environment wins over --env-file in compose interpolation.
  GOVERNANCE_SHELL_PORT="$BETA_PORT" MODEL_GATEWAY_PORT="$BETA_GATEWAY_PORT" \
    AI_ORCH_MODEL_BACKEND=bifrost AI_ORCH_MODEL_ALIAS_OVERRIDE= \
    "${COMPOSE[@]}" --profile beta up -d --build bifrost orchestrator governance-shell
  GOVERNANCE_SHELL_PORT="$BETA_PORT" "${ROOT}/scripts/wait-readyz.sh" "http://127.0.0.1:${BETA_PORT}"
  "${COMPOSE[@]}" --profile beta run --rm beta-catalog
  "${COMPOSE[@]}" --profile beta run --rm --no-deps beta-smoke
  cleanup_beta_compose
  trap - EXIT
else
  echo "==> Skipping Docker beta smoke (docker compose not available)"
fi

echo "==> Beta verification passed"
