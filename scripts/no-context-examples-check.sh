#!/usr/bin/env bash
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$root"

specs=(
  "examples/campaign.canonical.yaml"
  "examples/campaign.no-context.comparison.yaml"
  "examples/campaign.no-context.codex-exec.yaml"
  "examples/campaign.no-context.codex-subagent.yaml"
  "examples/campaign.no-context.claude-subagent.yaml"
)

for spec in "${specs[@]}"; do
  go run ./cmd/zcl campaign lint --spec "$spec" --json >/dev/null
done

echo "no-context-examples-check: PASS"
