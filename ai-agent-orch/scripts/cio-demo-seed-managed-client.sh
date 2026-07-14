#!/usr/bin/env bash
set -euo pipefail

# Seeds a clearly synthetic managed-client batch through the public receiver.
# Fixed client session and event IDs make re-runs receipt-deduped. A supplied
# demo air_ credential is reused; otherwise this asks the existing runtime-
# credential endpoint to mint one for the demo actor. Minting needs the
# Copilot store enabled and an enrolled actor, which the default demo stack
# does not have, so a failed mint skips the seed instead of failing the run.
# Set AI_ORCH_CIO_MANAGED_CLIENT_RUNTIME_TOKEN to seed unconditionally.

BASE_URL="${AI_ORCH_CIO_BASE_URL:?AI_ORCH_CIO_BASE_URL is required}"
DEV_TOKEN="${AI_ORCH_DEV_TOKEN:?AI_ORCH_DEV_TOKEN is required}"
RUNTIME_TOKEN="${AI_ORCH_CIO_MANAGED_CLIENT_RUNTIME_TOKEN:-}"
DEMO_ACTOR="demo-managed-client"
CLIENT="demo-managed-client"
CLIENT_SESSION_ID="demo-managed-client-session"

if ! command -v jq >/dev/null 2>&1; then
  echo "missing required command: jq" >&2
  exit 2
fi

TOKEN_SUPPLIED=1
if [ -z "$RUNTIME_TOKEN" ]; then
  TOKEN_SUPPLIED=0
  if credential_response="$(curl -fsS \
    -H "Authorization: Bearer ${DEV_TOKEN}" \
    -H "X-AI-Orch-Local-Identity: ${DEMO_ACTOR}" \
    -H "Content-Type: application/json" \
    -d "$(jq -n --arg client "$CLIENT" '{client: $client, device_name: "cio-demo-managed-client"}')" \
    "${BASE_URL}/v1/developer/runtime-credential")"; then
    RUNTIME_TOKEN="$(echo "$credential_response" | jq -r '.runtime_token // empty')"
  fi
fi

if [[ "$RUNTIME_TOKEN" != air_* ]]; then
  if [ "$TOKEN_SUPPLIED" -eq 1 ]; then
    echo "managed-client demo credential must be an actor-bound air_ token" >&2
    exit 1
  fi
  echo "Managed-client demo evidence skipped: could not mint a demo air_ credential (Copilot store disabled or actor not enrolled in the default stack)." >&2
  echo "To seed it, set AI_ORCH_CIO_MANAGED_CLIENT_RUNTIME_TOKEN to an existing air_ credential and re-run." >&2
  exit 0
fi

response="$(curl -fsS \
  -H "Authorization: Bearer ${RUNTIME_TOKEN}" \
  -H "Content-Type: application/json" \
  -d "$(jq -n --arg client "$CLIENT" --arg sid "$CLIENT_SESSION_ID" '{events: [
    {
      event_id: "cev_demo_managed_client_session_start",
      schema_version: "v0",
      client: $client,
      client_session_id: $sid,
      event_type: "session_start",
      repo: {remote: "https://example.invalid/demo-managed-client", branch: "demo-managed-client", commit: "demo"},
      timestamp: "2026-07-11T09:00:00Z"
    },
    {
      event_id: "cev_demo_managed_client_permission",
      schema_version: "v0",
      client: $client,
      client_session_id: $sid,
      event_type: "permission_decision",
      permission_decision: {tool: "demo-tool", decision: "approved", decider: "auto_policy", reason: "synthetic CIO demo evidence"},
      timestamp: "2026-07-11T09:00:01Z"
    },
    {
      event_id: "cev_demo_managed_client_usage",
      schema_version: "v0",
      client: $client,
      client_session_id: $sid,
      event_type: "token_usage",
      token_usage: {model: "demo-managed-client", input_tokens: 12, output_tokens: 8, source: "client_reported"},
      timestamp: "2026-07-11T09:00:02Z"
    }
  ]}')" \
  "${BASE_URL}/v1/managed-client/evidence")"

accepted="$(echo "$response" | jq -r '.accepted // 0')"
duplicate="$(echo "$response" | jq -r '.duplicate // 0')"
echo "Managed-client demo evidence accepted: ${accepted} (duplicate re-run: ${duplicate})"
