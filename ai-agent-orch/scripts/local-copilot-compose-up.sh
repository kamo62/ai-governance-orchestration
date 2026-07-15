#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

PROJECT="${AI_ORCH_COPILOT_PROJECT:-ai-orch-copilot}"
KEY_FILE="${AI_ORCH_COPILOT_KEY_FILE:-${HOME}/.ai-orch/copilot-token.key}"
GOVERNANCE_URL="${AI_ORCH_GOVERNANCE_URL:-http://127.0.0.1:${GOVERNANCE_SHELL_PORT:-18080}}"
MODEL_GATEWAY_URL="${AI_ORCH_MODEL_GATEWAY_URL:-http://127.0.0.1:${MODEL_GATEWAY_PORT:-18082}}"
DEV_TOKEN="${AI_ORCH_DEV_TOKEN:-local-dev}"
REFRESH_OPENCODE="${AI_ORCH_REFRESH_OPENCODE:-true}"

if [[ -z "${AI_ORCH_COPILOT_TOKEN_ENCRYPTION_KEY:-}" ]]; then
  if [[ ! -s "$KEY_FILE" ]]; then
    cat >&2 <<EOF
Missing Copilot token-store encryption key.

This is not a GitHub Copilot OAuth token. It is the server-side key used to
decrypt the local Copilot token database. Run scripts/copilot-verify.sh once to
create ${KEY_FILE}, or export AI_ORCH_COPILOT_TOKEN_ENCRYPTION_KEY from your
secret manager before starting the local Copilot-backed Compose stack.
EOF
    exit 2
  fi
  export AI_ORCH_COPILOT_TOKEN_ENCRYPTION_KEY="$(tr -d '\n' < "$KEY_FILE")"
  echo "Using Copilot token-store encryption key file: $KEY_FILE"
else
  echo "Using AI_ORCH_COPILOT_TOKEN_ENCRYPTION_KEY from environment"
fi

# docker-compose.yml validates this variable even when the Copilot override
# disables the bifrost service. A generated value is fine for this Copilot-only
# local stack because bifrost is not started.
if [[ -z "${BIFROST_ENCRYPTION_KEY:-}" ]]; then
  if command -v openssl >/dev/null 2>&1; then
    export BIFROST_ENCRYPTION_KEY="$(openssl rand -hex 16)"
  else
    export BIFROST_ENCRYPTION_KEY="ai-orch-copilot-local-placeholder"
  fi
fi

export AI_ORCH_MODEL_BACKEND="${AI_ORCH_MODEL_BACKEND:-copilot-user}"

docker compose \
  -p "$PROJECT" \
  -f docker-compose.yml \
  -f docker-compose.copilot.yml \
  up -d --build governance-shell orchestrator

./scripts/wait-readyz.sh "$GOVERNANCE_URL" >/dev/null

if [[ "$REFRESH_OPENCODE" == "true" ]]; then
  if [[ -f "${HOME}/.config/opencode/opencode.json" ]]; then
    AI_ORCH_GOVERNANCE_URL="$GOVERNANCE_URL" \
    AI_ORCH_MODEL_GATEWAY_URL="$MODEL_GATEWAY_URL" \
    AI_ORCH_DEV_TOKEN="$DEV_TOKEN" \
      go run ./cmd/ai-orch opencode refresh --scope global || \
      echo "warning: could not refresh global OpenCode config; run scripts/deployed-opencode-enroll.sh" >&2
  fi
  if [[ -f "${ROOT}/opencode.json" ]]; then
    AI_ORCH_GOVERNANCE_URL="$GOVERNANCE_URL" \
    AI_ORCH_MODEL_GATEWAY_URL="$MODEL_GATEWAY_URL" \
    AI_ORCH_DEV_TOKEN="$DEV_TOKEN" \
      go run ./cmd/ai-orch opencode refresh --scope project || \
      echo "warning: could not refresh project OpenCode config; run ai-orch opencode refresh --scope project" >&2
  fi
fi
