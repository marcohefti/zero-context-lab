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
mkdir -p "$out_dir"

targets=(
  "darwin/arm64"
  "darwin/amd64"
  "linux/amd64"
  "windows/amd64"
)

sha256_file="${out_dir}/SHA256SUMS"
: >"$sha256_file"

sha256() {
  if command -v shasum >/dev/null 2>&1; then
    shasum -a 256 "$1" | awk '{print $1}'
  elif command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" | awk '{print $1}'
  else
    openssl dgst -sha256 "$1" | awk '{print $2}'
  fi
}

for t in "${targets[@]}"; do
  os="${t%/*}"
  arch="${t#*/}"
  ext=""
  if [[ "$os" == "windows" ]]; then
    ext=".exe"
  fi

  bin="${out_dir}/zcl_${os}_${arch}${ext}"
  echo "build: ${os}/${arch} -> ${bin}"
  env GOOS="$os" GOARCH="$arch" CGO_ENABLED=0 \
    go build -trimpath -ldflags "-s -w -X main.version=${version}" -o "$bin" ./cmd/zcl

  sum="$(sha256 "$bin")"
  echo "${sum}  $(basename "$bin")" >>"$sha256_file"
done

echo "release-build: PASS ${out_dir}"

