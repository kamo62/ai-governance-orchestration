#!/usr/bin/env bash
set -euo pipefail

backend="${1:-}"
if [[ -z "$backend" ]]; then
  echo "usage: scripts/backend-compose-command.sh <bifrost|copilot-user>" >&2
  exit 2
fi

case "$backend" in
  bifrost)
    echo "AI_ORCH_MODEL_BACKEND=bifrost docker compose -f docker-compose.yml up -d bifrost governance-shell orchestrator"
    ;;
  copilot-user)
    echo "scripts/local-copilot-compose-up.sh"
    ;;
  *)
    echo "unknown backend: $backend" >&2
    exit 2
    ;;
esac
