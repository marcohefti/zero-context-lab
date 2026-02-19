# Evidence Pipeline (Attempt Lifecycle)

## Problem
If we let runners "just do things" and later try to reconstruct what happened, the benchmark becomes untrustworthy.
We need a strict, runner-agnostic evidence contract that is easy to validate and hard to accidentally bypass.

## Design Goals
- Primary evidence is artifacts, not transcripts:
  - `tool.calls.jsonl` is the action trace.
  - `feedback.json` is the authoritative outcome.
- Runners remain swappable across native spawn and process modes; ZCL owns evidence shape and guardrails.
- Strict mode is enforceable and consistent:
  - CI attempts should be strict by default.
  - Strict implies missing required artifacts is a failing condition.

## Non-goals
- ZCL does not attempt to infer intent or "reasoning quality" from chat logs.
- ZCL does not store unbounded payloads by default.

## Where The Logic Lives
- Attempt allocation: `internal/attempt/start.go` (writes `run.json`, `suite.json` snapshot, `attempt.json`, optional `prompt.txt`)
- Funnel trace emission:
  - CLI funnel: `internal/cli/cmd_run.go`
  - MCP funnel: `internal/funnel/mcp_proxy/proxy.go`
  - HTTP funnel: `internal/cli/cmd_http_proxy.go`, `internal/funnel/http_proxy`
- Outcome writing: `internal/feedback/feedback.go` (writes `feedback.json`, enforces prerequisites)
- Report: `internal/report/report.go` (computes `attempt.report.json`)
- Validation: `internal/validate/validate.go` (typed integrity errors, strict enforcement)
- Finish orchestration: `internal/cli/cmd_attempt_finish.go` (report -> validate -> expect)

## Runtime Flow
1. Allocate attempt:
   - `zcl attempt start --json` or `zcl suite plan --json` (or `zcl suite run ...` which allocates attempts just-in-time internally).
   - Output includes `ZCL_*` env, including `ZCL_OUT_DIR` and `ZCL_TMP_DIR`.
2. Execute actions through funnels:
   - `zcl run -- <cmd> ...` appends a trace event to `tool.calls.jsonl`.
   - `zcl mcp proxy -- <server-cmd> ...` appends MCP boundary events.
   - `zcl http proxy ...` appends HTTP boundary events.
3. Write outcome:
   - runner calls `zcl feedback --ok|--fail ...` which writes `feedback.json`.
4. Finish:
   - `zcl attempt finish --json [--strict] [--strict-expect]`
   - writes `attempt.report.json` then runs validate + expect
   - run-level reporting: `zcl report --json <runDir>` writes `run.report.json` and returns the same JSON.

## Invariants / Guardrails
- Attempt identity boundary is the attempt directory (`ZCL_OUT_DIR`).
- IDs must match across:
  - `attempt.json`
  - each trace event in `tool.calls.jsonl`
  - `feedback.json`
- Evidence must be present for an attempt to be considered complete:
  - `feedback.json` without non-empty `tool.calls.jsonl` is treated as funnel bypass (strict fails).
- CI mode implies strict by default (`attempt.EffectiveStrict`).

## Observability
- Typed error codes are the primary interface for automation (`ZCL_E_*`).
- `attempt.report.json` captures:
  - pointers to artifacts
  - metrics derived from trace
  - best-effort integrity signals (trace present/non-empty, feedback present)

## Testing Expectations
- Validate failures are stable and typed on common corruption/missing-artifact cases.
- Funnel commands refuse to run if `attempt.json` doesn't match `ZCL_*` env (id mismatch guardrail).
- `attempt finish` returns `2` when validate/expect/outcome is not OK, not `1`.
