#!/usr/bin/env bash
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$root"

usage() {
  cat <<'EOF'
Usage:
  scripts/dev-local-install.sh [--prefix <dir>] [--codex-home <dir>] [--no-skill-sync] [--copy-skills] [--quiet]

Builds the host zcl binary from the current checkout and installs it into <prefix>/bin (default: ~/.local).
Also makes the ZCL Codex skill available by linking (default) or copying it into $CODEX_HOME/skills/local/zcl.
EOF
}

prefix="${HOME}/.local"
codex_home="${CODEX_HOME:-${HOME}/.codex}"
skill_sync=1
copy_skills=0
quiet=0

while [[ $# -gt 0 ]]; do
  case "$1" in
    --prefix) prefix="$2"; shift 2 ;;
    --codex-home) codex_home="$2"; shift 2 ;;
    --no-skill-sync) skill_sync=0; shift 1 ;;
    --copy-skills) copy_skills=1; shift 1 ;;
    --quiet) quiet=1; shift 1 ;;
    -h|--help) usage; exit 0 ;;
    *) echo "dev-local-install: ERROR unknown arg $1" >&2; usage; exit 2 ;;
  esac
done

say() {
  if [[ "$quiet" == "1" ]]; then
    return 0
  fi
  echo "$@"
}

version="0.0.0-dev"
if git rev-parse --git-dir >/dev/null 2>&1; then
  version="$(git describe --tags --always --dirty)"
fi

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

os="$(go env GOOS)"
arch="$(go env GOARCH)"
bin="${tmp}/zcl_${os}_${arch}"
if [[ "$os" == "windows" ]]; then
  bin="${bin}.exe"
fi

say "dev-local-install: build ${os}/${arch} version=${version}"
go build -trimpath -ldflags "-s -w -X main.version=${version}" -o "$bin" ./cmd/zcl

say "dev-local-install: install prefix=${prefix}"
./install.sh --file "$bin" --prefix "$prefix" >/dev/null

if [[ "$skill_sync" == "1" ]]; then
  src="${root}/skills/zcl"
  dst_root="${codex_home}/skills/local"
  dst="${dst_root}/zcl"
  mkdir -p "$dst_root"

  if [[ "$copy_skills" == "1" ]]; then
    say "dev-local-install: copy skill -> ${dst}"
    rm -rf "$dst"
    mkdir -p "$dst"
    cp -R "${src}/" "$dst/"
  else
    # Safe-by-default: only replace if dst is missing or a symlink.
    if [[ -e "$dst" && ! -L "$dst" ]]; then
      echo "dev-local-install: ERROR skill target exists and is not a symlink: ${dst}" >&2
      echo "dev-local-install: Refusing to overwrite. Remove it or re-run with --copy-skills." >&2
      exit 2
    fi
    say "dev-local-install: link skill -> ${dst} -> ${src}"
    rm -f "$dst"
    ln -s "$src" "$dst"
  fi
fi

say "dev-local-install: OK ${prefix}/bin/zcl"

