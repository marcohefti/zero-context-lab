#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<EOF
Usage:
  scripts/brew-formula-write.sh --version <semver> --sha256 <hex> [--out Formula/zcl.rb]

Writes/updates the Homebrew formula for ZCL.
EOF
}

version=""
sha=""
out="Formula/zcl.rb"

while [[ $# -gt 0 ]]; do
  case "$1" in
    --version) version="$2"; shift 2 ;;
    --sha256) sha="$2"; shift 2 ;;
    --out) out="$2"; shift 2 ;;
    -h|--help) usage; exit 0 ;;
    *) echo "brew-formula-write: ERROR unknown arg $1" >&2; usage; exit 2 ;;
  esac
done

if [[ -z "$version" || -z "$sha" ]]; then
  echo "brew-formula-write: ERROR --version and --sha256 are required" >&2
  exit 2
fi

cat >"$out" <<EOF
class Zcl < Formula
  desc "Zero Context Lab (ZCL): agent evaluation harness with trace-backed evidence"
  homepage "https://github.com/marcohefti/zero-context-lab"
  url "https://codeload.github.com/marcohefti/zero-context-lab/tar.gz/refs/tags/v${version}"
  sha256 "${sha}"
  license "MIT"
  head "https://github.com/marcohefti/zero-context-lab.git", branch: "main"

  depends_on "go" => :build

  def install
    ldflags = "-s -w -X main.version=v#{version}"
    system "go", "build", "-trimpath", "-ldflags", ldflags, "-o", bin/"zcl", "./cmd/zcl"
  end

  test do
    assert_match "v#{version}", shell_output("#{bin}/zcl version").strip
  end
end
EOF

echo "brew-formula-write: OK ${out} (v${version})"
