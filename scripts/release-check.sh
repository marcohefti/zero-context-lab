#!/usr/bin/env bash
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$root"

version="${VERSION:-}"
if [[ -z "$version" ]]; then
  if git rev-parse --git-dir >/dev/null 2>&1; then
    version="$(git describe --tags --always --dirty)"
  else
    version="0.0.0-dev"
  fi
fi

out_dir="artifacts/release/${version}"
sha_file="${out_dir}/SHA256SUMS"

fail() { echo "release-check: FAIL $*" >&2; exit 1; }

[[ -d "$out_dir" ]] || fail "missing $out_dir (run scripts/release-build.sh)"
[[ -f "$sha_file" ]] || fail "missing $sha_file"
[[ -f "CHANGELOG.md" ]] || fail "missing CHANGELOG.md"

expected_bins=(
  "zcl_darwin_arm64"
  "zcl_darwin_amd64"
  "zcl_linux_amd64"
  "zcl_windows_amd64.exe"
)

for b in "${expected_bins[@]}"; do
  [[ -f "${out_dir}/${b}" ]] || fail "missing binary: ${out_dir}/${b}"
done

lines="$(wc -l <"$sha_file" | tr -d ' ')"
[[ "$lines" -ge 4 ]] || fail "SHA256SUMS should have >=4 lines"

echo "release-check: PASS ${out_dir}"

