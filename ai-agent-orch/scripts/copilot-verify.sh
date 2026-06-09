#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

MODEL=""
PROMPT="Reply exactly: copilot-smoke-ok"
KEY_FILE="${AI_ORCH_COPILOT_KEY_FILE:-${HOME}/.ai-orch/copilot-token.key}"
MODELS_FILE="${AI_ORCH_COPILOT_MODELS_FILE:-${ROOT}/var/copilot-models.json}"

usage() {
  cat <<'EOF'
Usage:
  scripts/copilot-verify.sh [--model <model-id>] [--prompt <text>]

What it does:
  1. Creates or reuses a local Copilot token encryption key.
  2. Runs `ai-orch copilot login` if no token is configured for the actor.
  3. Fetches the live GitHub Copilot model list for this account.
  4. Selects an available model from the returned model list.
  5. Runs a Copilot smoke request through ai-orch CLI.

Environment:
  AI_ORCH_COPILOT_TOKEN_ENCRYPTION_KEY  Optional. If absent, a local key file is used.
  AI_ORCH_COPILOT_KEY_FILE              Optional key file path. Default: ~/.ai-orch/copilot-token.key
  AI_ORCH_ACTOR_SUBJECT                 Optional actor subject for token lookup.
EOF
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --model)
      MODEL="${2:-}"
      shift 2
      ;;
    --prompt)
      PROMPT="${2:-}"
      shift 2
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      echo "unknown argument: $1" >&2
      usage >&2
      exit 2
      ;;
  esac
done

require_cmd() {
  if ! command -v "$1" >/dev/null 2>&1; then
    echo "missing required command: $1" >&2
    exit 2
  fi
}

require_cmd jq

if [[ -z "${AI_ORCH_COPILOT_TOKEN_ENCRYPTION_KEY:-}" ]]; then
  mkdir -p "$(dirname "$KEY_FILE")"
  if [[ ! -f "$KEY_FILE" ]]; then
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
else
  echo "Using AI_ORCH_COPILOT_TOKEN_ENCRYPTION_KEY from environment"
fi

status_output="$(go run ./cmd/ai-orch copilot status 2>&1 || true)"
echo "$status_output"
if grep -qi "not configured" <<<"$status_output"; then
  echo "Copilot token not configured. Starting GitHub device login."
  go run ./cmd/ai-orch copilot login
fi

mkdir -p "$(dirname "$MODELS_FILE")"
go run ./cmd/ai-orch copilot models > "$MODELS_FILE"
echo "Saved Copilot model list to: $MODELS_FILE"

model_exists() {
  local model_id="$1"
  jq -e --arg wanted "$model_id" '
    def model_items:
      if type == "array" then .
      elif (.data | type) == "array" then .data
      elif (.models | type) == "array" then .models
      else [] end;
    [model_items[] | (.id // .model // .name // empty)] | index($wanted)
  ' "$MODELS_FILE" >/dev/null
}

choose_model() {
  if [[ -n "$MODEL" ]]; then
    if model_exists "$MODEL"; then
      printf '%s' "$MODEL"
      return 0
    fi
    echo "requested model $MODEL is not present in the live Copilot model list" >&2
    exit 3
  fi

  local preferred=(
    "gpt-5-mini"
    "gpt-5.3-codex"
    "gpt-5.5"
    "claude-sonnet-4.5"
  )
  for candidate in "${preferred[@]}"; do
    if model_exists "$candidate"; then
      printf '%s' "$candidate"
      return 0
    fi
  done

  jq -r '
    def model_items:
      if type == "array" then .
      elif (.data | type) == "array" then .data
      elif (.models | type) == "array" then .models
      else [] end;
    model_items[0] | (.id // .model // .name // empty)
  ' "$MODELS_FILE"
}

SELECTED_MODEL="$(choose_model)"
if [[ -z "$SELECTED_MODEL" || "$SELECTED_MODEL" == "null" ]]; then
  echo "no Copilot models returned for this actor" >&2
  exit 3
fi

echo "Selected Copilot model: $SELECTED_MODEL"
go run ./cmd/ai-orch copilot smoke --model "$SELECTED_MODEL" --prompt "$PROMPT"
