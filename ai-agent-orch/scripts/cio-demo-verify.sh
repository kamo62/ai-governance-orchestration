#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

PROJECT="${AI_ORCH_CIO_PROJECT:-ai-orch-cio-demo}"
ENV_FILE="${AI_ORCH_ENV_FILE:-${ROOT}/../.env.dev}"
export GOVERNANCE_SHELL_PORT="${GOVERNANCE_SHELL_PORT:-18081}"
export MODEL_GATEWAY_PORT="${MODEL_GATEWAY_PORT:-18083}"

COMPOSE=(docker compose -p "$PROJECT")
if [ -f "$ENV_FILE" ]; then
  COMPOSE+=(--env-file "$ENV_FILE")
fi
COMPOSE+=(-f docker-compose.yml -f docker-compose.beta.yml)

env_value() {
  local name="$1"
  local fallback="$2"
  local current="${!name:-}"
  if [ -n "$current" ]; then
    printf '%s' "$current"
    return
  fi
  if [ -f "$ENV_FILE" ]; then
    local from_file
    from_file="$(awk -F= -v key="$name" '$1 == key {sub(/^[^=]*=/, ""); print; exit}' "$ENV_FILE")"
    if [ -n "$from_file" ]; then
      printf '%s' "$from_file"
      return
    fi
  fi
  printf '%s' "$fallback"
}

DEV_TOKEN="$(env_value AI_ORCH_DEV_TOKEN local-dev)"
# Compose requires BIFROST_ENCRYPTION_KEY with no default. Prefer the shell or
# .env.dev value so re-runs against a persisted bifrost volume keep working;
# fall back to an ephemeral key for one-off demo runs.
export BIFROST_ENCRYPTION_KEY="$(env_value BIFROST_ENCRYPTION_KEY "$(openssl rand -hex 16)")"
BASE_URL="http://127.0.0.1:${GOVERNANCE_SHELL_PORT}"
UI_URL="${BASE_URL}/ui/"

echo "==> Building CIO demo images"
"${COMPOSE[@]}" --profile beta build governance-shell orchestrator beta-catalog beta-smoke

echo "==> Starting CIO demo stack (${PROJECT})"
"${COMPOSE[@]}" --profile beta up -d --force-recreate bifrost orchestrator governance-shell

echo "==> Waiting for Governance Shell readiness"
for _ in $(seq 1 45); do
  if curl -fsS "${BASE_URL}/readyz" >/dev/null 2>&1; then
    break
  fi
  sleep 2
done
curl -fsS "${BASE_URL}/readyz" >/dev/null

echo "==> Validating catalog"
"${COMPOSE[@]}" --profile beta run --rm beta-catalog

echo "==> Running governed beta smoke"
"${COMPOSE[@]}" --profile beta run --rm --no-deps beta-smoke

echo "==> Checking protected system status"
curl -fsS -H "Authorization: Bearer ${DEV_TOKEN}" "${BASE_URL}/v1/system/status" >/dev/null

echo "==> Checking metrics and UI"
curl -fsS "${BASE_URL}/metrics" >/dev/null
curl -fsS "${UI_URL}" | grep -q "CIO Demo Readiness"

echo
echo "CIO demo stack is ready."
echo "UI: ${UI_URL}"
echo "Project: ${PROJECT}"
echo "Model gateway: http://127.0.0.1:${MODEL_GATEWAY_PORT}/v1"
if [ "$DEV_TOKEN" = "local-dev" ]; then
  echo "Developer token source: default local-dev"
else
  echo "Developer token source: environment or ${ENV_FILE}"
fi
echo "Cleanup: docker compose -p ${PROJECT} -f docker-compose.yml -f docker-compose.beta.yml --profile beta down --remove-orphans"
