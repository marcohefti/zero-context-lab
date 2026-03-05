#!/usr/bin/env bash
set -euo pipefail

version="v1.64.8"
cmd=(go run github.com/golangci/golangci-lint/cmd/golangci-lint@"${version}")

# Full-repo lint can be enabled explicitly for debt reduction sessions.
if [[ "${ZCL_GOLANGCI_FULL:-0}" == "1" ]]; then
  "${cmd[@]}" run ./...
  exit 0
fi

base_ref=""
if [[ -n "${GITHUB_BASE_REF:-}" ]] && git rev-parse --verify "origin/${GITHUB_BASE_REF}" >/dev/null 2>&1; then
  base_ref="$(git merge-base HEAD "origin/${GITHUB_BASE_REF}")"
elif git rev-parse --verify origin/main >/dev/null 2>&1; then
  base_ref="$(git merge-base HEAD origin/main)"
elif git rev-parse --verify HEAD~1 >/dev/null 2>&1; then
  base_ref="HEAD~1"
fi

if [[ -z "${base_ref}" ]]; then
  "${cmd[@]}" run ./...
  exit 0
fi

"${cmd[@]}" run --new-from-rev "${base_ref}" ./...

