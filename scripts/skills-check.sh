#!/usr/bin/env bash
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$root"

failures=()

require_file() {
  local p="$1"
  if [[ ! -f "$p" ]]; then
    failures+=("missing: $p")
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

require_file "skills/zcl/SKILL.md"
require_contains "skills/zcl/SKILL.md" "zcl attempt start" "skills-check: SKILL must mention attempt start"
require_contains "skills/zcl/SKILL.md" "zcl feedback" "skills-check: SKILL must mention feedback"
require_contains "skills/zcl/SKILL.md" "tool.calls.jsonl" "skills-check: SKILL must mention tool.calls.jsonl"

if (( ${#failures[@]} > 0 )); then
  for f in "${failures[@]}"; do
    printf 'skills-check: FAIL %s\n' "$f" >&2
  done
  exit 1
fi

printf 'skills-check: PASS\n'

