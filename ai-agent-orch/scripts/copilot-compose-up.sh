#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

echo "scripts/copilot-compose-up.sh is deprecated; use scripts/local-copilot-compose-up.sh for local Compose." >&2
exec scripts/local-copilot-compose-up.sh "$@"
