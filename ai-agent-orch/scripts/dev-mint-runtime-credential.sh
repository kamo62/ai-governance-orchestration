#!/usr/bin/env bash
set -euo pipefail

# Mints an actor-bound runtime credential for a managed client. It writes only
# the air_ credential to stdout so callers can use command substitution.

BASE_URL="${AI_ORCH_GOVERNANCE_URL:-http://127.0.0.1:18080}"
DEV_TOKEN="${AI_ORCH_DEV_TOKEN:-local-dev}"
ACTOR_SUBJECT="${AI_ORCH_ACTOR_SUBJECT:-${USER:-}}"
CLIENT_NAME="${AI_ORCH_RUNTIME_CREDENTIAL_CLIENT:-t3code}"
DEVICE_NAME="${AI_ORCH_RUNTIME_CREDENTIAL_DEVICE_NAME:-}"

require_command() {
  if ! command -v "$1" >/dev/null 2>&1; then
    echo "missing required command: $1" >&2
    exit 2
  fi
}

store_remediation() {
  cat >&2 <<'EOF'
Copilot credential storage is disabled on the Governance Shell.

For the local Compose stack, set AI_ORCH_COPILOT_TOKEN_ENCRYPTION_KEY and restart
the Copilot profile:
  cd ai-agent-orch
  scripts/copilot-verify.sh
  scripts/local-copilot-compose-up.sh

The encryption key enables the server-side Copilot store; it is not a GitHub
Copilot OAuth token.
EOF
}

enrollment_remediation() {
  cat >&2 <<'EOF'
The actor is not enrolled with Copilot on this Governance Shell.

Complete the GitHub device-login prompt from this helper, or enrol manually:
  cd ai-agent-orch
  go run ./cmd/ai-orch copilot login

Then rerun this helper with the same AI_ORCH_ACTOR_SUBJECT.
EOF
}

require_command curl
require_command jq

if [[ -z "$ACTOR_SUBJECT" ]]; then
  echo "AI_ORCH_ACTOR_SUBJECT is required when USER is unset" >&2
  exit 2
fi

BASE_URL="${BASE_URL%/}"
HTTP_STATUS=""
HTTP_BODY=""

request() {
  local method="$1"
  local path="$2"
  local data="${3:-}"
  local response
  local -a args=(
    --silent
    --show-error
    --connect-timeout 10
    --max-time 30
    --request "$method"
    --header "Authorization: Bearer ${DEV_TOKEN}"
    --header "X-AI-Orch-Local-Identity: ${ACTOR_SUBJECT}"
  )

  if [[ -n "$data" ]]; then
    args+=(--header "Content-Type: application/json" --data "$data")
  fi

  if ! response="$(curl "${args[@]}" --write-out $'\n%{http_code}' "${BASE_URL}${path}")"; then
    echo "request failed: ${method} ${path}" >&2
    exit 1
  fi

  HTTP_STATUS="${response##*$'\n'}"
  HTTP_BODY="${response:0:${#response}-${#HTTP_STATUS}-1}"
  if [[ ! "$HTTP_STATUS" =~ ^[0-9]{3}$ ]]; then
    echo "invalid HTTP response from ${method} ${path}" >&2
    exit 1
  fi
}

mint_credential() {
  local body
  if [[ -n "$DEVICE_NAME" ]]; then
    body="$(jq -n --arg client "$CLIENT_NAME" --arg device "$DEVICE_NAME" '{client: $client, device_name: $device}')"
  else
    body="$(jq -n --arg client "$CLIENT_NAME" '{client: $client}')"
  fi
  request POST /v1/developer/runtime-credential "$body"
}

enroll_actor() {
  local login_id verification_url user_code interval expires_in done login_error deadline

  request POST /v1/copilot/login/start
  case "$HTTP_STATUS" in
    202) ;;
    503)
      store_remediation
      return 1
      ;;
    *)
      echo "could not start Copilot enrolment (HTTP ${HTTP_STATUS})" >&2
      enrollment_remediation
      return 1
      ;;
  esac

  login_id="$(jq -r '.login_id // empty' <<<"$HTTP_BODY")"
  verification_url="$(jq -r '.verification_uri_complete // .verification_uri // empty' <<<"$HTTP_BODY")"
  user_code="$(jq -r '.user_code // empty' <<<"$HTTP_BODY")"
  interval="$(jq -r '.interval // 5' <<<"$HTTP_BODY")"
  expires_in="$(jq -r '.expires_in // 900' <<<"$HTTP_BODY")"
  HTTP_BODY=""

  if [[ -z "$login_id" || -z "$verification_url" || -z "$user_code" || ! "$interval" =~ ^[0-9]+$ || ! "$expires_in" =~ ^[0-9]+$ ]]; then
    echo "invalid Copilot enrolment response" >&2
    enrollment_remediation
    return 1
  fi
  if (( interval < 1 || interval > 60 )); then
    interval=5
  fi

  echo "Copilot enrolment is required for actor ${ACTOR_SUBJECT}." >&2
  echo "Open ${verification_url}" >&2
  echo "Code: ${user_code}" >&2

  deadline=$((SECONDS + expires_in + 60))
  while (( SECONDS < deadline )); do
    sleep "$interval"
    request GET "/v1/copilot/login/${login_id}"
    if [[ "$HTTP_STATUS" != 200 ]]; then
      echo "could not poll Copilot enrolment (HTTP ${HTTP_STATUS})" >&2
      enrollment_remediation
      return 1
    fi
    done="$(jq -r '.done // false' <<<"$HTTP_BODY")"
    login_error="$(jq -r '.error // empty' <<<"$HTTP_BODY")"
    HTTP_BODY=""
    if [[ -n "$login_error" ]]; then
      echo "Copilot enrolment failed at the Governance Shell" >&2
      enrollment_remediation
      return 1
    fi
    if [[ "$done" == true ]]; then
      return 0
    fi
  done

  echo "Copilot enrolment timed out" >&2
  enrollment_remediation
  return 1
}

mint_credential
if [[ "$HTTP_STATUS" == 403 ]]; then
  HTTP_BODY=""
  if ! enroll_actor; then
    exit 1
  fi
  mint_credential
fi

case "$HTTP_STATUS" in
  201) ;;
  503)
    store_remediation
    exit 1
    ;;
  403)
    enrollment_remediation
    exit 1
    ;;
  *)
    echo "could not mint a runtime credential (HTTP ${HTTP_STATUS})" >&2
    exit 1
    ;;
esac

runtime_token="$(jq -r '.runtime_token // empty' <<<"$HTTP_BODY")"
HTTP_BODY=""
if [[ "$runtime_token" != air_* ]]; then
  echo "runtime credential response did not contain an air_ token" >&2
  exit 1
fi

printf '%s\n' "$runtime_token"
