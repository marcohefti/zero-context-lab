#!/usr/bin/env bash
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$root"

doctor_json="$(go run ./cmd/zcl doctor --json)"
gc_json="$(go run ./cmd/zcl gc --dry-run --json)"

DOCTOR_JSON="$doctor_json" GC_JSON="$gc_json" python3 - <<'PY'
import json
import os
import sys

doctor = json.loads(os.environ["DOCTOR_JSON"])
gc = json.loads(os.environ["GC_JSON"])

max_deleted = int(os.getenv("ZCL_ENTROPY_MAX_DELETED", "0"))
max_total_before = int(os.getenv("ZCL_ENTROPY_MAX_TOTAL_BEFORE_BYTES", "0"))
max_gc_errors = int(os.getenv("ZCL_ENTROPY_MAX_GC_ERRORS", "0"))

failures = []

if not doctor.get("ok", False):
    failures.append("doctor.ok=false")

if not gc.get("ok", False):
    failures.append("gc.ok=false")

gc_errors = gc.get("errors") or []
if len(gc_errors) > max_gc_errors:
    failures.append(
        f"gc.errors exceeded threshold: {len(gc_errors)} > {max_gc_errors}"
    )

deleted_count = len(gc.get("deleted") or [])
if max_deleted > 0 and deleted_count > max_deleted:
    failures.append(
        f"gc.deleted exceeded threshold: {deleted_count} > {max_deleted}"
    )

total_before = int(gc.get("totalBeforeBytes") or 0)
if max_total_before > 0 and total_before > max_total_before:
    failures.append(
        f"gc.totalBeforeBytes exceeded threshold: {total_before} > {max_total_before}"
    )

if failures:
    for item in failures:
        print(f"entropy-check: FAIL {item}")
    sys.exit(1)

print(
    "entropy-check: PASS "
    f"doctor.ok={doctor.get('ok', False)} "
    f"gc.deleted={deleted_count} "
    f"gc.totalBeforeBytes={total_before}"
)
PY
