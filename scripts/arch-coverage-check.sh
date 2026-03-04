#!/usr/bin/env bash
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$root"

python3 -c "$(cat <<'PY'
import json
import subprocess
import sys
from pathlib import Path

mod = ""
try:
    for line in Path("go.mod").read_text(encoding="utf-8", errors="replace").splitlines():
        line = line.strip()
        if line.startswith("module "):
            mod = line.split()[1].strip()
            break
except FileNotFoundError:
    mod = ""

if not mod:
    print("arch-coverage-check: FAIL could not determine module path from go.mod", file=sys.stderr)
    raise SystemExit(1)

raw = subprocess.run(
    ["go", "list", "-json", "./..."],
    check=True,
    stdout=subprocess.PIPE,
    stderr=subprocess.PIPE,
    text=True,
).stdout

dec = json.JSONDecoder()
i = 0
pkgs = []
while i < len(raw):
    while i < len(raw) and raw[i].isspace():
        i += 1
    if i >= len(raw):
        break
    obj, j = dec.raw_decode(raw, i)
    pkgs.append(obj)
    i = j


def rel(import_path: str) -> str:
    if import_path == mod:
        return ""
    if import_path.startswith(mod + "/"):
        return import_path[len(mod) + 1 :]
    return import_path


VALID_LAYERS = {"domain", "ports", "app", "infra"}


def is_allowed_internal(pkg_rel: str) -> bool:
    if pkg_rel.startswith("internal/kernel/"):
        return True
    if pkg_rel.startswith("internal/interfaces/"):
        return True
    if pkg_rel == "internal/bootstrap" or pkg_rel.startswith("internal/bootstrap/"):
        return True
    if pkg_rel.startswith("internal/contexts/"):
        parts = pkg_rel.split("/")
        if len(parts) < 4:
            return False
        ctx = parts[2].strip()
        layer = parts[3].strip()
        return bool(ctx) and layer in VALID_LAYERS
    return False


failures = []
for o in pkgs:
    ip = o.get("ImportPath") or ""
    if not ip.startswith(mod + "/internal/"):
        continue
    pkg_rel = rel(ip)
    if not pkg_rel:
        continue
    if not is_allowed_internal(pkg_rel):
        failures.append(pkg_rel)

if failures:
    failures = sorted(set(failures))
    for f in failures:
        print(f"arch-coverage-check: FAIL legacy/unknown internal package: {f}", file=sys.stderr)
    print("arch-coverage-check: hint migrate packages under internal/contexts/*, internal/interfaces/*, or internal/kernel/*", file=sys.stderr)
    raise SystemExit(1)

print("arch-coverage-check: PASS")
PY
)"

