#!/usr/bin/env bash
set -euo pipefail

backend="${1:-}"
if [[ -z "$backend" ]]; then
  echo "usage: scripts/backend-compose-command.sh <bifrost|native-openrouter|agentgateway|copilot-user>" >&2
  exit 2
fi

case "$backend" in
  bifrost)
    echo "AI_ORCH_MODEL_BACKEND=bifrost docker compose -f docker-compose.yml up -d bifrost governance-shell orchestrator"
    ;;
  native-openrouter)
    echo "docker compose -f docker-compose.yml -f docker-compose.openrouter.yml up -d governance-shell orchestrator"
    ;;
  agentgateway)
    echo "docker compose -f docker-compose.yml -f docker-compose.agentgateway.yml up -d agentgateway governance-shell orchestrator"
    ;;
  copilot-user)
    echo "AI_ORCH_MODEL_BACKEND=copilot-user docker compose -f docker-compose.yml -f docker-compose.copilot.yml up -d governance-shell orchestrator"
    ;;
  *)
    echo "unknown backend: $backend" >&2
    exit 2
    ;;
esac
