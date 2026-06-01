#!/bin/bash
set -euo pipefail

cd "$(dirname "$0")/.."

# Run gofmt in check mode across the Go module.
if [ -d "ai-agent-orch" ]; then
    cd ai-agent-orch
fi

unformatted=$(gofmt -l .)
if [ -n "$unformatted" ]; then
    echo "gofmt check failed. The following files need formatting:"
    echo "$unformatted"
    exit 1
fi

echo "gofmt check passed"
