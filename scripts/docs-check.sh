#!/usr/bin/env bash
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$root"

failures=()

require_file() {
  local p="$1"
  if [[ ! -f "$p" ]]; then
    failures+=("missing required doc: $p")
  fi
}

require_contains() {
  local file="$1"
  local needle="$2"
  local msg="$3"
  if [[ -f "$file" ]]; then
    if ! grep -Fq -- "$needle" "$file"; then
      failures+=("$msg")
    fi
  fi
}

require_file "CONCEPT.md"
require_file "ARCHITECTURE.md"
require_file "AGENTS.md"

require_contains "CONCEPT.md" "ARCHITECTURE.md" "CONCEPT.md must reference ARCHITECTURE.md"
require_contains "CONCEPT.md" "AGENTS.md" "CONCEPT.md must reference AGENTS.md"

require_contains "ARCHITECTURE.md" "CONCEPT.md" "ARCHITECTURE.md must reference CONCEPT.md"
require_contains "ARCHITECTURE.md" "AGENTS.md" "ARCHITECTURE.md must reference AGENTS.md"

require_contains "AGENTS.md" "CONCEPT.md" "AGENTS.md must reference CONCEPT.md"
require_contains "AGENTS.md" "ARCHITECTURE.md" "AGENTS.md must reference ARCHITECTURE.md"

if (( ${#failures[@]} > 0 )); then
  for f in "${failures[@]}"; do
    printf 'docs-check: FAIL %s\n' "$f" >&2
  done
  exit 1
fi

printf 'docs-check: PASS\n'
