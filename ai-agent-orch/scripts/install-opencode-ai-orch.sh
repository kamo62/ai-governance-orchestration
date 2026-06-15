#!/usr/bin/env sh
set -eu

ROOT="$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)"
cd "$ROOT"

echo "scripts/install-opencode-ai-orch.sh is deprecated; use scripts/deployed-opencode-refresh.sh for deployed gateways." >&2
exec scripts/deployed-opencode-refresh.sh "$@"
