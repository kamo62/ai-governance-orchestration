#!/usr/bin/env sh
set -eu

ROOT="$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)"
cd "$ROOT"

ENV_FILE="${AI_ORCH_ENV_FILE:-}"
if [ -n "$ENV_FILE" ] && [ -f "$ENV_FILE" ]; then
  set -a
  # shellcheck disable=SC1090
  . "$ENV_FILE"
  set +a
fi

SCOPE="${AI_ORCH_OPENCODE_SCOPE:-global}"

exec go run ./cmd/ai-orch developer enroll --client opencode --scope "$SCOPE" "$@"
