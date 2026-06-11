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

if [ -z "${AI_ORCH_DEV_TOKEN:-}" ]; then
  export AI_ORCH_DEV_TOKEN="local-dev"
fi
if [ -z "${AI_ORCH_RUNTIME_TOKEN:-}" ]; then
  export AI_ORCH_RUNTIME_TOKEN="local-runtime-token"
fi
if [ -z "${AI_ORCH_ACTOR_SUBJECT:-}" ]; then
  if command -v id >/dev/null 2>&1; then
    export AI_ORCH_ACTOR_SUBJECT="$(id -un)"
  else
    export AI_ORCH_ACTOR_SUBJECT="${USER:-local-dev}"
  fi
fi

exec go run ./cmd/ai-orch opencode -- "$@"
