#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

GATEWAY_URL="${AI_ORCH_MODEL_GATEWAY_URL:-http://127.0.0.1:18082}"
RUNTIME_TOKEN="${AI_ORCH_RUNTIME_TOKEN:-local-runtime-token}"
ACTOR_SUBJECT="${AI_ORCH_ACTOR_SUBJECT:-${USER:-local-dev}}"
CONFIG_JSON="$(mktemp)"
trap 'rm -f "$CONFIG_JSON"' EXIT

if ! command -v jq >/dev/null 2>&1; then
  echo "missing required command: jq" >&2
  exit 2
fi
if ! command -v curl >/dev/null 2>&1; then
  echo "missing required command: curl" >&2
  exit 2
fi

go run ./cmd/ai-orch opencode generate-config > "$CONFIG_JSON"

default_model="$(jq -r '.model // "ai-orch/coding-gpt55"' "$CONFIG_JSON")"

jq -r --arg default_model "$default_model" '
  .agent
  | to_entries[]
  | [.key, (.value.model // $default_model)]
  | @tsv
' "$CONFIG_JSON" | while IFS=$'\t' read -r agent model; do
  [[ -z "$agent" || -z "$model" ]] && continue
  provider="${model%%/*}"
  alias="${model#*/}"
  if [[ "$provider" == "$model" ]]; then
    provider="ai-orch"
    alias="$model"
  fi

  case "$provider" in
    ai-orch-responses)
      endpoint="responses"
      payload="$(jq -n --arg model "$alias" --arg agent "$agent" '{model:$model,input:("Reply exactly: matrix-ok for " + $agent),max_output_tokens:32}')"
      url="${GATEWAY_URL%/}/v1/responses"
      ;;
    ai-orch)
      endpoint="chat.completions"
      payload="$(jq -n --arg model "$alias" --arg agent "$agent" '{model:$model,messages:[{role:"user",content:("Reply exactly: matrix-ok for " + $agent)}],max_tokens:32}')"
      url="${GATEWAY_URL%/}/v1/chat/completions"
      ;;
    *)
      echo "SKIP $agent $model (unknown provider prefix)"
      continue
      ;;
  esac

  status="$(curl -sS -o /tmp/ai-orch-matrix-response.json -w '%{http_code}' \
    -H "Authorization: Bearer ${RUNTIME_TOKEN}" \
    -H "X-AI-Orch-Actor-Subject: ${ACTOR_SUBJECT}" \
    -H "X-AI-Orch-Client: matrix-smoke" \
    -H "X-AI-Orch-Intent: OpenCode agent matrix smoke for ${agent}" \
    -H "Content-Type: application/json" \
    -d "$payload" \
    "$url")"

  if [[ "$status" =~ ^2 ]]; then
    echo "PASS $agent $model via /v1/$endpoint"
  else
    echo "FAIL $agent $model via /v1/$endpoint HTTP $status" >&2
    cat /tmp/ai-orch-matrix-response.json >&2
    exit 1
  fi
done
