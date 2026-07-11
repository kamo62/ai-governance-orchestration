#!/usr/bin/env bash
set -euo pipefail

# Seeds one dogfooding evidence record for the CIO demo: AI-Orch reviewing
# itself, since this AI-Orch instance's own development sessions run through
# AI-Orch governance. Posted through the normal POST /v1/evidence path (not
# written into the store directly), so it exercises the same lane-derivation
# logic (Phase 2) as any other evidence record. Self-reported evidence -- no
# X-AI-Orch-Trusted-Client-Token, just the dev-token bearer -- always lands
# proposed, by design; see the description text this script posts.
#
# Idempotent across demo re-runs: the session is looked up by a fixed
# work_item_id marker before creating a new one, and the evidence POST uses a
# fixed client_event_id ("cev_demo_self_review"), so a re-run against the
# same (session_id, client_event_id) pair is deduped server-side (Phase 3)
# instead of creating a second record.

BASE_URL="${AI_ORCH_CIO_BASE_URL:?AI_ORCH_CIO_BASE_URL is required}"
DEV_TOKEN="${AI_ORCH_DEV_TOKEN:?AI_ORCH_DEV_TOKEN is required}"

if ! command -v jq >/dev/null 2>&1; then
  echo "missing required command: jq" >&2
  exit 2
fi

WORK_ITEM_ID="ai-orch-self-review-demo"
CLIENT_EVENT_ID="cev_demo_self_review"

session_id="$(curl -fsS -H "Authorization: Bearer ${DEV_TOKEN}" "${BASE_URL}/v1/sessions?limit=100" \
  | jq -r --arg wi "$WORK_ITEM_ID" '[.sessions[]? | select(.work_item_id == $wi)][0].session_id // empty')"

if [ -n "$session_id" ]; then
  echo "Reusing self-review session: ${session_id}"
else
  session_id="$(curl -fsS -H "Authorization: Bearer ${DEV_TOKEN}" -H "Content-Type: application/json" \
    -d "$(jq -n --arg wi "$WORK_ITEM_ID" '{
      agent: "governance-lead",
      classification: "internal",
      prompt: "AI-Orch self-review: this session anchors evidence describing how this AI-Orch instance governs its own development.",
      permission_mode: "read_only",
      approval_mode: "manual",
      workspace_mode: "local",
      work_item_id: $wi,
      intent: "self-review"
    }')" \
    "${BASE_URL}/v1/sessions" | jq -r '.session_id')"
  echo "Created self-review session: ${session_id}"
fi

if [ -z "$session_id" ] || [ "$session_id" = "null" ]; then
  echo "self-review session lookup/creation failed" >&2
  exit 1
fi

description="AI-Orch reviewing itself: this AI-Orch instance's own development sessions run through AI-Orch governance, the same Governance Shell this evidence is posted to. This record is self-reported (no trusted-client proof), so by design it lands with proposed status pending human confirmation, the same as any other self-reported evidence -- it is not a gateway-enforced or managed-client record."

response="$(curl -fsS -H "Authorization: Bearer ${DEV_TOKEN}" -H "Content-Type: application/json" \
  -d "$(jq -n --arg sid "$session_id" --arg desc "$description" --arg cev "$CLIENT_EVENT_ID" '{
    session_id: $sid,
    evidence_type: "self_review",
    subject_key: "ai-orch/self",
    description: $desc,
    client_event_id: $cev
  }')" \
  "${BASE_URL}/v1/evidence")"

status="$(echo "$response" | jq -r '.status // "unknown"')"
duplicate="$(echo "$response" | jq -r 'if .duplicate then "yes" else "no" end')"
echo "Self-review evidence status: ${status} (duplicate re-run: ${duplicate})"
