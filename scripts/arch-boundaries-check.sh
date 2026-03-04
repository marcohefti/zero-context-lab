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
    print("arch-boundaries-check: FAIL could not determine module path from go.mod", file=sys.stderr)
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


def rel(import_path: str) -> str:
    if import_path == mod:
        return ""
    if import_path.startswith(mod + "/"):
        return import_path[len(mod) + 1 :]
    return import_path


VALID_LAYERS = {"domain", "ports", "app", "infra"}


class ContextInfo:
    def __init__(self, context: str, layer: str) -> None:
        self.context = context
        self.layer = layer


def parse_context_layer(pkg_rel: str) -> ContextInfo | None:
    # internal/contexts/<ctx>/<layer>/...
    parts = pkg_rel.split("/")
    if len(parts) < 4:
        return None
    if parts[0] != "internal" or parts[1] != "contexts":
        return None
    ctx = parts[2].strip()
    layer = parts[3].strip()
    if not ctx:
        return None
    if layer not in VALID_LAYERS:
        return None
    return ContextInfo(ctx, layer)


def is_kernel(pkg_rel: str) -> bool:
    return pkg_rel.startswith("internal/kernel/")


def is_interfaces(pkg_rel: str) -> bool:
    return pkg_rel.startswith("internal/interfaces/")


def is_bootstrap(pkg_rel: str) -> bool:
    return pkg_rel == "internal/bootstrap" or pkg_rel.startswith("internal/bootstrap/")


def is_internal_pkg(pkg: str) -> bool:
    return pkg.startswith(mod + "/internal/")


def fail(msg: str) -> None:
    failures.append(msg)


failures: list[str] = []

for pkg in sorted(imports.keys()):
    if not is_internal_pkg(pkg):
        continue
    pkg_rel = rel(pkg)
    info = parse_context_layer(pkg_rel)
    if info is None:
        continue  # legacy package; not enforced yet

    deps = [rel(d) for d in imports[pkg] if is_internal_pkg(d)]
    for dep_rel in deps:
        # Always allow kernel (including transitional allowlist).
        if is_kernel(dep_rel):
            continue
        # Never allow depending on CLI/interfaces/bootstrap from context packages.
        if is_interfaces(dep_rel):
            fail(f"{pkg_rel} imports {dep_rel} (contexts must not depend on interfaces)")
            continue
        if is_bootstrap(dep_rel):
            fail(f"{pkg_rel} imports {dep_rel} (contexts must not depend on bootstrap)")
            continue

        dep_info = parse_context_layer(dep_rel)
        if dep_info is None:
            fail(
                f"{pkg_rel} imports legacy internal package {dep_rel} (migrate dependency behind ports or into kernel)"
            )
            continue

        same_ctx = dep_info.context == info.context

        if info.layer == "domain":
            if not same_ctx or dep_info.layer != "domain":
                fail(f"{pkg_rel} (domain) imports {dep_rel}; domain may only import same-context domain or kernel")
            continue

        if info.layer == "ports":
            if not same_ctx or dep_info.layer not in ("domain", "ports"):
                fail(f"{pkg_rel} (ports) imports {dep_rel}; ports may only import same-context domain/ports or kernel")
            continue

        if info.layer == "app":
            if same_ctx:
                if dep_info.layer == "infra":
                    fail(f"{pkg_rel} (app) imports {dep_rel}; app must not depend on infra (invert via ports)")
            else:
                if dep_info.layer != "ports":
                    fail(f"{pkg_rel} (app) imports {dep_rel}; cross-context deps must go through ports only")
            continue

        if info.layer == "infra":
            if same_ctx:
                # infra may depend on any same-context layer
                continue
            if dep_info.layer != "ports":
                fail(f"{pkg_rel} (infra) imports {dep_rel}; cross-context deps must go through ports only")
            continue

        fail(f"{pkg_rel} has unknown layer {info.layer}")

if failures:
    for f in failures:
        print(f"arch-boundaries-check: FAIL {f}", file=sys.stderr)
    raise SystemExit(1)

print("arch-boundaries-check: PASS")
PY
)"
