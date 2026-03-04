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
    print("dep-boundaries-check: FAIL could not determine module path from go.mod", file=sys.stderr)
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

imports = {}
for o in pkgs:
    ip = o.get("ImportPath") or ""
    imports[ip] = list(o.get("Imports") or [])

def rel(import_path):
    if import_path == mod:
        return ""
    if import_path.startswith(mod + "/"):
        return import_path[len(mod) + 1 :]
    return import_path

def internal_deps_for(import_path):
    deps = []
    for d in imports.get(import_path, []):
        if d.startswith(mod + "/internal/"):
            deps.append(rel(d))
    return deps

failures = []

def fail(msg):
    failures.append(msg)

for pkg in sorted(imports.keys()):
    pkg_rel = rel(pkg)
    if not pkg_rel:
        continue

    deps = internal_deps_for(pkg)

    # Entry point boundary: cmd/* only imports internal/interfaces/cli from internal/*
    if pkg_rel.startswith("cmd/"):
        for d in deps:
            if d != "internal/interfaces/cli":
                fail(
                    f"entrypoint boundary: {pkg_rel} imports {d}; cmd/* may only import internal/interfaces/cli"
                )

    # UI boundary: only cmd/* may import internal/interfaces/cli
    if "internal/interfaces/cli" in deps and not pkg_rel.startswith("cmd/"):
        fail(
            f"ui boundary: {pkg_rel} imports internal/interfaces/cli; move shared logic out of interfaces/cli into context packages"
        )

    # Runtime adapters: only interfaces should import them.
    for d in deps:
        if d.startswith("internal/contexts/runtime/infra/") and not pkg_rel.startswith("internal/interfaces/"):
            fail(
                f"adapter boundary: {pkg_rel} imports {d}; only internal/interfaces/* may import runtime infra adapters"
            )

    # Funnel adapters: keep protocol boundaries at the edge.
    if any(d in ("internal/contexts/evidence/app/http_proxy", "internal/contexts/evidence/app/mcp_proxy") for d in deps) and pkg_rel != "internal/interfaces/cli":
        bad = [d for d in deps if d in ("internal/contexts/evidence/app/http_proxy", "internal/contexts/evidence/app/mcp_proxy")]
        fail(
            f"funnel boundary: {pkg_rel} imports {', '.join(bad)}; only internal/interfaces/cli may import these adapters"
        )
    if "internal/kernel/cli_funnel" in deps and pkg_rel not in ("internal/interfaces/cli", "internal/contexts/evidence/app/replay"):
        fail(
            f"funnel boundary: {pkg_rel} imports internal/kernel/cli_funnel; only internal/interfaces/cli or evidence/replay may"
        )

    # Kernel packages: must not depend on contexts or interfaces.
    if pkg_rel.startswith("internal/kernel/"):
        non_kernel = [d for d in deps if not d.startswith("internal/kernel/")]
        if non_kernel:
            fail(f"kernel boundary: {pkg_rel} imports {', '.join(non_kernel)}; kernel must not depend on contexts/interfaces")

if failures:
    for f in failures:
        print(f"dep-boundaries-check: FAIL {f}", file=sys.stderr)
    print("dep-boundaries-check: hint update scripts/dep-boundaries-check.sh if the architecture intentionally changes", file=sys.stderr)
    raise SystemExit(1)

print("dep-boundaries-check: PASS")
PY
)"
