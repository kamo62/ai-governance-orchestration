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

if [ -z "${AI_ORCH_GOVERNANCE_URL:-}" ] || [ -z "${AI_ORCH_MODEL_GATEWAY_URL:-}" ]; then
  cat >&2 <<'EOF'
Missing deployed gateway endpoints.

Set AI_ORCH_GOVERNANCE_URL and AI_ORCH_MODEL_GATEWAY_URL, or point
AI_ORCH_ENV_FILE at a file that defines them.
EOF
  exit 2
fi

exec go run ./cmd/ai-orch opencode refresh --scope "$SCOPE" "$@"
