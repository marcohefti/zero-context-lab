#!/usr/bin/env bash
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$root"

python3 -c "$(cat <<'PY'
import re
import sys
from pathlib import Path

ARTIFACTS_FILE = Path("internal/kernel/artifacts/artifacts.go")
SKIP_PREFIXES = [
    Path("internal/kernel/artifacts"),
]
TARGETS = [
    Path("cmd"),
    Path("internal"),
]

if not ARTIFACTS_FILE.exists():
    print(f"artifact-literals-check: FAIL missing: {ARTIFACTS_FILE}", file=sys.stderr)
    raise SystemExit(1)

raw = ARTIFACTS_FILE.read_text(encoding="utf-8", errors="replace")
artifact_values = sorted(set(re.findall(r'=\s*"([^"]+)"', raw)))
if not artifact_values:
    print("artifact-literals-check: FAIL could not extract artifact basenames", file=sys.stderr)
    raise SystemExit(1)

alts = "|".join(re.escape(v) for v in artifact_values)
literal = re.compile(r'\"(?:' + alts + r')\"|`(?:' + alts + r')`')

violations: list[str] = []


def is_skipped(p: Path) -> bool:
    if p.name.endswith("_test.go"):
        return True
    for prefix in SKIP_PREFIXES:
        try:
            if p.is_relative_to(prefix):
                return True
        except AttributeError:
            # Python <3.9 compatibility: manual prefix check.
            try:
                p.relative_to(prefix)
                return True
            except ValueError:
                pass
    return False


for target in TARGETS:
    if not target.exists():
        continue
    for path in sorted(target.rglob("*.go")):
        if is_skipped(path):
            continue
        text = path.read_text(encoding="utf-8", errors="replace")
        m = literal.search(text)
        if not m:
            continue
        line = text.count("\n", 0, m.start()) + 1
        violations.append(f"{path}:{line}: {m.group(0)}")

if violations:
    print("artifact-literals-check: FAIL artifact basenames used as string literals:", file=sys.stderr)
    for v in violations:
        print(v, file=sys.stderr)
    print("use constants from internal/kernel/artifacts.", file=sys.stderr)
    raise SystemExit(1)

print("artifact-literals-check: PASS")
PY
)"
