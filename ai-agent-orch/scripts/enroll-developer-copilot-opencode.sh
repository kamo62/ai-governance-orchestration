#!/usr/bin/env sh
set -eu

ROOT="$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)"
cd "$ROOT"

echo "scripts/enroll-developer-copilot-opencode.sh is deprecated; use scripts/deployed-opencode-enroll.sh for deployed gateways." >&2
exec scripts/deployed-opencode-enroll.sh "$@"
