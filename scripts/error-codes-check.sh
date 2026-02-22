#!/usr/bin/env bash
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$root"

targets=(
  internal/cli
  internal/campaign
  internal/contract
)

if command -v rg >/dev/null 2>&1; then
  violations="$(rg -n --glob '!**/*_test.go' '"ZCL_E_[A-Z0-9_]+"' "${targets[@]}" || true)"
else
  # macOS CI images may not include ripgrep; fallback keeps the check enforceable.
  violations="$(grep -RIn --exclude='*_test.go' -E '"ZCL_E_[A-Z0-9_]+"' "${targets[@]}" || true)"
fi
if [[ -n "${violations}" ]]; then
  echo "error-codes-check: FAIL raw ZCL_E code literals found in runtime paths:" >&2
  printf '%s\n' "${violations}" >&2
  echo "replace with constants from internal/codes (or package-local aliases)." >&2
  exit 1
fi

echo "error-codes-check: PASS"
