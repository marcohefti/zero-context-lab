#!/usr/bin/env bash
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$root"

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

version="smoke"
out_dir="${tmp}/release/${version}"
mkdir -p "$out_dir"

bin="${out_dir}/zcl_$(go env GOOS)_$(go env GOARCH)"
go build -trimpath -ldflags "-s -w -X main.version=${version}" -o "$bin" ./cmd/zcl

prefix="${tmp}/prefix"
./install.sh --file "$bin" --prefix "$prefix"

got="$("${prefix}/bin/zcl" version)"
if [[ "$got" != "$version" ]]; then
  echo "installer-smoke: FAIL expected version ${version}, got ${got}" >&2
  exit 1
fi

echo "installer-smoke: PASS"

