# Write Safety (Atomic JSON + Safe JSONL)

## Problem
Evidence artifacts must be durable, parseable, and non-interleaved. Partial writes and concurrent appends destroy trust fast.

## Design Goals
- Atomic JSON writes: never leave partially-written `*.json` behind.
- Safe JSONL appends: avoid interleaving when multiple processes append concurrently.
- Deterministic shapes: stable JSON encoding and bounded content.
- Containment: artifacts must not escape the attempt/run directory via symlinks or path traversal.

## Non-goals
- Not a general-purpose database layer.
- We do not guarantee multi-writer ordering beyond "each append is whole-line".

## Where The Logic Lives
- Atomic JSON writes: `internal/store/json.go`, `internal/store/file.go` (via `store.WriteJSONAtomic`, `store.WriteFileAtomic`)
- JSONL append + locking: `internal/store/jsonl.go`, `internal/store/lock.go`
- Bounds + redaction: `internal/redact/redact.go`, `internal/trace/trace.go`
- Containment checks: `internal/validate/validate.go`

## Runtime Flow
- JSON artifacts (`run.json`, `attempt.json`, `feedback.json`, `attempt.report.json`):
  - write to a temp file
  - fsync
  - rename into place (atomic on supported filesystems)
- JSONL streams (`tool.calls.jsonl`, `notes.jsonl`, `captures.jsonl`):
  - acquire a cross-platform lock (mkdir-based lock directory adjacent to the stream)
  - stale lock cleanup requires staleness **and** non-alive owner PID when owner metadata is available
  - append a single newline-delimited JSON object
  - fsync
  - release lock

## Invariants / Guardrails
- Every JSONL line is a single JSON object.
- Previews and stored payloads are bounded; truncation is explicit.
- Redaction is applied to stored evidence (previews and captures) but passthrough output remains raw for operator parity.
- Validation fails in strict mode for:
  - missing required artifacts
  - invalid JSON / invalid JSONL
  - schema/version mismatches
  - containment violations (symlink traversal)

## Observability
- Validation emits typed findings with paths and codes (`ZCL_E_IO`, `ZCL_E_INVALID_JSONL`, `ZCL_E_CONTAINMENT`, etc).
- Report aggregates integrity signals (trace present/non-empty, funnel bypass suspected).

## Testing Expectations
- Concurrency tests for JSONL append/lock behavior (no interleaving, no empty lines).
- Truncation + redaction tests pin preview/capture bounds.
- Containment tests pin symlink traversal detection.
