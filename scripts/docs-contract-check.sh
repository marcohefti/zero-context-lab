#!/usr/bin/env bash
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$root"

contract_json="$(go run ./cmd/zcl contract --json)"

python3 -c "$(cat <<'PY'
import json
import sys
from pathlib import Path

contract = json.load(sys.stdin)

docs = [
    Path("AGENTS.md"),
    Path("CONCEPT.md"),
    Path("ARCHITECTURE.md"),
    Path("SCHEMAS.md"),
    Path("PLAN.md"),
]
doc_text = {}
for p in docs:
    try:
        doc_text[p.name] = p.read_text(encoding="utf-8", errors="replace")
    except FileNotFoundError:
        doc_text[p.name] = ""

def in_any_doc(needle: str) -> bool:
    return any(needle in t for t in doc_text.values())

failures = []

for cmd in contract.get("commands", []):
    usage = (cmd.get("usage") or "").strip()
    toks = usage.split()
    if len(toks) < 2:
        continue
    if len(toks) >= 3 and toks[1] in ("attempt", "suite", "mcp"):
        phrase = " ".join(toks[:3])
    else:
        phrase = " ".join(toks[:2])
    if not in_any_doc(phrase):
        failures.append(f"missing doc mention for command: {phrase}")

schemas = doc_text.get("SCHEMAS.md", "")
for art in contract.get("artifacts", []):
    aid = (art.get("id") or "").strip()
    if aid and aid not in schemas:
        failures.append(f"SCHEMAS.md missing artifact id: {aid}")

if failures:
    for f in failures:
        print(f"docs-contract-check: FAIL {f}")
    raise SystemExit(1)

print("docs-contract-check: PASS")
PY
)" <<<"$contract_json"
