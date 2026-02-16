#!/usr/bin/env bash
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$root"

before_mod="$(cat go.mod 2>/dev/null || true)"
before_sum="$(cat go.sum 2>/dev/null || true)"

go mod tidy

after_mod="$(cat go.mod 2>/dev/null || true)"
after_sum="$(cat go.sum 2>/dev/null || true)"

if [[ "$before_mod" != "$after_mod" || "$before_sum" != "$after_sum" ]]; then
  echo "mod-tidy-check: FAIL go.mod/go.sum not tidy (go mod tidy changed files)" >&2
  echo "run: go mod tidy" >&2
  exit 1
fi

echo "mod-tidy-check: PASS"

