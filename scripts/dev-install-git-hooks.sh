#!/usr/bin/env bash
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$root"

usage() {
  cat <<'EOF'
Usage:
  scripts/dev-install-git-hooks.sh

Installs local git hooks that keep ~/.local/bin/zcl and the local Codex skill in sync on:
- git commit (post-commit)
- git pull/merge (post-merge)
- branch switch (post-checkout)
- rebase (post-rewrite)
- git push (pre-push)

Hooks are local-only and not versioned by git.
EOF
}

if [[ "${1:-}" == "-h" || "${1:-}" == "--help" ]]; then
  usage
  exit 0
fi

if [[ ! -d ".git" ]]; then
  echo "dev-install-git-hooks: ERROR not a git repo (missing .git)" >&2
  exit 2
fi

hook_body='#!/usr/bin/env bash
set -euo pipefail

root="$(cd "$(dirname "$0")/../.." && pwd)"
cd "$root"

if [[ -x "scripts/dev-local-install.sh" ]]; then
  # Best-effort: never block git operations.
  scripts/dev-local-install.sh --quiet || true
fi
'

install_hook() {
  local name="$1"
  local path=".git/hooks/${name}"
  printf '%s' "$hook_body" >"$path"
  chmod +x "$path"
  echo "dev-install-git-hooks: OK ${path}"
}

install_hook "post-merge"
install_hook "post-checkout"
install_hook "post-rewrite"
install_hook "post-commit"
install_hook "pre-push"
