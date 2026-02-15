#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<EOF
Usage:
  ./install.sh --file <path-to-zcl-binary> [--prefix <dir>]

Installs zcl into <prefix>/bin (default: ~/.local).
EOF
}

file=""
prefix="${HOME}/.local"
while [[ $# -gt 0 ]]; do
  case "$1" in
    --file) file="$2"; shift 2 ;;
    --prefix) prefix="$2"; shift 2 ;;
    -h|--help) usage; exit 0 ;;
    *) echo "install: ERROR unknown arg $1" >&2; usage; exit 2 ;;
  esac
done

[[ -n "$file" ]] || { echo "install: ERROR --file is required" >&2; exit 2; }
[[ -f "$file" ]] || { echo "install: ERROR missing file: $file" >&2; exit 2; }

mkdir -p "${prefix}/bin"
cp "$file" "${prefix}/bin/zcl"
chmod +x "${prefix}/bin/zcl"
echo "install: OK ${prefix}/bin/zcl"

