#!/usr/bin/env bash
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$root"

./scripts/skills-check.sh
./scripts/mod-tidy-check.sh
./scripts/docs-check.sh

go_files="$(find . -type f -name '*.go' -not -path './vendor/*' || true)"
if [[ -n "${go_files}" ]]; then
  unformatted="$(gofmt -l ${go_files} || true)"
  if [[ -n "${unformatted}" ]]; then
    echo "gofmt-check: FAIL unformatted files:" >&2
    printf '%s\n' "${unformatted}" >&2
    echo "run: gofmt -w <files>" >&2
    exit 1
  fi
fi
echo "gofmt-check: PASS"

go test ./...
go vet ./...

./scripts/contract-snapshot.sh --check
./scripts/docs-contract-check.sh

echo "verify: PASS"
