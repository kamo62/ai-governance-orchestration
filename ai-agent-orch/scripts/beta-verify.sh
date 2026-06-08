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
  COMPOSE=(docker compose -f docker-compose.yml -f docker-compose.beta.yml)
  if [ -f "$ENV_FILE" ]; then
    COMPOSE+=(--env-file "$ENV_FILE")
  fi
  "${COMPOSE[@]}" --profile beta down --remove-orphans >/dev/null 2>&1 || true
  BETA_PORT="${GOVERNANCE_SHELL_PORT:-18081}"
  BETA_GATEWAY_PORT="${MODEL_GATEWAY_PORT:-18083}"
  GOVERNANCE_SHELL_PORT="$BETA_PORT" MODEL_GATEWAY_PORT="$BETA_GATEWAY_PORT" \
    "${COMPOSE[@]}" --profile beta up -d bifrost orchestrator governance-shell
  "${COMPOSE[@]}" --profile beta run --rm beta-catalog
  "${COMPOSE[@]}" --profile beta run --rm --no-deps beta-smoke
  "${COMPOSE[@]}" --profile beta down --remove-orphans
else
  echo "==> Skipping Docker beta smoke (docker compose not available)"
fi

echo "==> Beta verification passed"