#!/usr/bin/env sh
set -eu

ROOT="$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)"
cd "$ROOT"

SCOPE="${AI_ORCH_OPENCODE_SCOPE:-global}"
exec go run ./cmd/ai-orch opencode install-config --scope "$SCOPE" "$@"
