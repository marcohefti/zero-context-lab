# Suite Run Orchestration (`zcl suite run`)

## Problem
Operators want to run an entire suite end-to-end with a single command, without baking in any runner-specific logic.
We also need guardrails so runs produce valid primary evidence (trace + feedback) and failure modes are typed and automatable.

## Design Goals
- Runner-agnostic: ZCL spawns an external runner per mission attempt and only provides env + artifacts.
- Evidence-first: runner must use ZCL funnels (`zcl run`, `zcl mcp proxy`, `zcl http proxy`) and must finish with `zcl feedback`.
- Deterministic automation:
  - Require `--json`.
  - Reserve stdout for JSON only; stream runner output to stderr.
  - Stable per-attempt summary including runner exit + finish/validate/expect results.
- Strong guardrails:
  - Refuse to run if `attempt.json` IDs don't match the planned `ZCL_*` env.
  - Enforce attempt deadlines (`attempt.json.timeoutMs`) via context deadline; classify as `ZCL_E_TIMEOUT`.

## Non-goals
- ZCL does not interpret runner logs/transcripts for scoring.
- ZCL does not provide a plugin runtime for runners; orchestration is process-level.
- ZCL does not "auto-fix" missing evidence; it surfaces the failure (validate/report error).

## Where The Logic Lives
- CLI entry point: `internal/cli/cli.go`
- Implementation: `internal/cli/cmd_suite_run.go`
- Planning: `internal/planner/suite_plan.go` (calls `attempt.Start` per mission)
- Attempt allocation + artifacts: `internal/attempt/start.go`
- Finish pipeline: `internal/report/report.go`, `internal/validate/validate.go`, `internal/expect/expect.go`
- Tests: `internal/cli/suite_run_integration_test.go`

## Runtime Flow
Per `zcl suite run --file ... --json -- <runner-cmd> ...`:

1. Plan the suite:
   - Parse suite file and allocate attempt dirs via `planner.PlanSuite(...)`.
   - Each mission gets an `attempt.json`, optional `prompt.txt`, and a `ZCL_*` env map (including `ZCL_TMP_DIR`).
2. For each planned mission attempt:
   - Build runner env:
     - start from current process env
     - overlay planned `ZCL_*` env
     - optionally set `ZCL_PROMPT_PATH=<attemptDir>/prompt.txt` if present
   - Guardrail: read `attempt.json` and verify IDs match env (refuse to spawn on mismatch).
   - Spawn runner:
     - stream runner stdout/stderr to ZCL stderr
     - apply attempt deadline (context derived from `attempt.json.startedAt + timeoutMs`)
3. Finish attempt:
   - Build and write `attempt.report.json`
   - Run `validate` and `expect`
4. Emit one JSON summary on stdout and exit:
   - `0` if all attempts OK
   - `2` if suite completed but some attempts failed finish/expect/validate/outcome
   - `1` for harness errors (spawn/I/O/timeouts/runner non-zero exit)

## Invariants / Guardrails
- `--json` is required.
- Runner command must be provided after `--`.
- Runner is spawned with a clean, explicit attempt env (no implicit globals required besides the runner binary).
- ZCL does not consider the suite successful unless:
  - runner exited `0`
  - finish pipeline succeeded (`report` + `validate` + `expect`)

## Observability
- Human-readable progress (per mission) is emitted to stderr:
  - mission id, attempt id, runner basename
- Machine-readable summary is emitted once to stdout:
  - includes `runnerExitCode`, typed `runnerErrorCode` (`ZCL_E_TIMEOUT`, `ZCL_E_SPAWN`, etc), and finish results.

## Testing Expectations
- Happy path:
  - runner calls `zcl run -- echo hi` and `zcl feedback --ok ...`
  - suite run returns exit `0` and summary `ok=true`.
- Missing evidence:
  - runner does not write `feedback.json`
  - suite run returns exit `2` and summary indicates report/validate failures.

