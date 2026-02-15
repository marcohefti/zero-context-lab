#!/usr/bin/env bash
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$root"

mode="check"
snapshot_path="test/fixtures/contract/contract.snapshot.json"

usage() {
  cat <<EOF
Usage: scripts/contract-snapshot.sh [--check|--update] [--snapshot <path>]

Checks or updates the contract snapshot generated from:
  go run ./cmd/zcl contract --json
EOF
}

args=("$@")
i=0
while (( i < ${#args[@]} )); do
  t="${args[$i]}"
  case "$t" in
    --check) mode="check" ;;
    --update) mode="update" ;;
    --snapshot)
      i=$((i + 1))
      snapshot_path="${args[$i]:-}"
      if [[ -z "$snapshot_path" ]]; then
        echo "contract-snapshot: ERROR --snapshot requires a path" >&2
        exit 2
      fi
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      echo "contract-snapshot: ERROR unknown argument: $t" >&2
      exit 2
      ;;
  esac
  i=$((i + 1))
done

mkdir -p "artifacts/contract-snapshot"

current_path="artifacts/contract-snapshot/current.json"
if ! go run ./cmd/zcl contract --json >"$current_path"; then
  echo "contract-snapshot: ERROR failed to read contract from CLI" >&2
  exit 2
fi

if [[ "$mode" == "update" ]]; then
  mkdir -p "$(dirname "$snapshot_path")"
  cp "$current_path" "$snapshot_path"
  echo "contract-snapshot: PASS updated snapshot: $snapshot_path"
  exit 0
fi

if [[ ! -f "$snapshot_path" ]]; then
  echo "contract-snapshot: FAIL missing snapshot at $snapshot_path; run:" >&2
  echo "  scripts/contract-snapshot.sh --update --snapshot $snapshot_path" >&2
  exit 1
fi

if ! cmp -s "$snapshot_path" "$current_path"; then
  echo "contract-snapshot: FAIL snapshot mismatch" >&2
  echo "run:" >&2
  echo "  scripts/contract-snapshot.sh --update --snapshot $snapshot_path" >&2
  exit 1
fi

echo "contract-snapshot: PASS"
