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
KEY_FILE="${AI_ORCH_COPILOT_KEY_FILE:-${HOME}/.ai-orch/copilot-token.key}"
GOVERNANCE_URL="${AI_ORCH_GOVERNANCE_URL:-http://127.0.0.1:18080}"
MODEL_GATEWAY_URL="${AI_ORCH_MODEL_GATEWAY_URL:-http://127.0.0.1:18082}"

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
export AI_ORCH_GOVERNANCE_URL="$GOVERNANCE_URL"
export AI_ORCH_MODEL_GATEWAY_URL="$MODEL_GATEWAY_URL"

if [ -z "${AI_ORCH_COPILOT_TOKEN_ENCRYPTION_KEY:-}" ]; then
  mkdir -p "$(dirname "$KEY_FILE")"
  if [ ! -f "$KEY_FILE" ]; then
    if command -v openssl >/dev/null 2>&1; then
      openssl rand -base64 32 > "$KEY_FILE"
    else
      LC_ALL=C tr -dc 'A-Za-z0-9' < /dev/urandom | head -c 48 > "$KEY_FILE"
      printf '\n' >> "$KEY_FILE"
    fi
    chmod 600 "$KEY_FILE"
  fi
  export AI_ORCH_COPILOT_TOKEN_ENCRYPTION_KEY="$(tr -d '\n' < "$KEY_FILE")"
  echo "Using local Copilot encryption key file: $KEY_FILE"
fi

echo "Checking Governance Shell at $AI_ORCH_GOVERNANCE_URL"
go run ./cmd/ai-orch copilot status

if ! go run ./cmd/ai-orch copilot refresh >/dev/null 2>&1; then
  echo "Starting GitHub Copilot device login for actor $AI_ORCH_ACTOR_SUBJECT"
  go run ./cmd/ai-orch copilot login
fi

echo "Verifying Copilot models through ai-orch"
go run ./cmd/ai-orch copilot models >/dev/null

echo "Installing OpenCode ai-orch provider config ($SCOPE)"
go run ./cmd/opencode-smoke install-config --scope "$SCOPE" --force --runtime-token "$AI_ORCH_RUNTIME_TOKEN" --actor-subject "$AI_ORCH_ACTOR_SUBJECT"

cat <<EOF

Enrollment complete.

Direct OpenCode/T3 launches can now use the installed ai-orch provider without
manually setting AI_ORCH_SESSION_ID or AI_ORCH_SESSION_TOKEN. The gateway will
auto-create governed sessions when those values are absent.

The GitHub Copilot credential was enrolled for actor: $AI_ORCH_ACTOR_SUBJECT
No Copilot credential was written to OpenCode config.

Using Cline instead of (or alongside) OpenCode:
  API Provider: OpenAI Compatible
  Base URL:     $AI_ORCH_MODEL_GATEWAY_URL/v1
  API key:      $AI_ORCH_RUNTIME_TOKEN.$AI_ORCH_ACTOR_SUBJECT
  Model:        pick from the dropdown (served by the governed gateway)
  Governed MCP: ai-orch mcp install --client cline
EOF
