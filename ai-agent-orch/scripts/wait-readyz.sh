#!/usr/bin/env bash
set -euo pipefail

BASE_URL="${1:-http://127.0.0.1:${GOVERNANCE_SHELL_PORT:-18081}}"
MAX_ATTEMPTS="${READYZ_MAX_ATTEMPTS:-60}"
SLEEP_SECONDS="${READYZ_SLEEP_SECONDS:-1}"

for attempt in $(seq 1 "$MAX_ATTEMPTS"); do
  if curl -fsS "${BASE_URL}/readyz" >/dev/null 2>&1; then
    echo "Governance Shell ready (${BASE_URL}/readyz) after ${attempt} attempt(s)"
    exit 0
  fi
  sleep "$SLEEP_SECONDS"
done

echo "Governance Shell not ready after ${MAX_ATTEMPTS} attempt(s): ${BASE_URL}/readyz" >&2
exit 1