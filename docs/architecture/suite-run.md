# Suite Run Orchestration (`zcl suite run`)

## Problem
Operators want to run an entire suite end-to-end with a single command, without baking in any runner-specific logic.
We also need guardrails so runs produce valid primary evidence (trace + feedback) and failure modes are typed and automatable.
We must also avoid silently preferring process orchestration when the host can natively spawn fresh agent sessions.

## Design Goals
- Capability-first isolation:
  - Prefer native host spawning when available.
  - Make process runner usage explicit when native spawning is available.
- Evidence-first: runner must use ZCL funnels (`zcl run`, `zcl mcp proxy`, `zcl http proxy`) and must finish with `zcl feedback`.
- Deterministic automation:
  - Require `--json`.
  - Reserve stdout for JSON only; stream runner output to stderr.
  - Stable per-attempt summary including runner exit + finish/validate/expect results.
- Strong guardrails:
  - Refuse to run if `attempt.json` IDs don't match the planned `ZCL_*` env.
  - Enforce attempt deadlines (`attempt.json.timeoutMs`) with configurable anchors (`attempt_start` or `first_tool_call`).
  - Optional blind mode rejects contaminated prompts with typed evidence (`ZCL_E_CONTAMINATED_PROMPT`).

## Non-goals
- ZCL does not interpret runner logs/transcripts for scoring.
- ZCL does not provide a plugin runtime for native spawning.
- `zcl suite run` is not the native-spawn orchestrator; it is the process-runner executor with capability guards.
- ZCL does not infer task success/failure from transcripts. It only auto-writes canonical **infra-failure** evidence when the runner exits before writing feedback.

## Where The Logic Lives
- CLI entry point: `internal/cli/cli.go`
- Implementation: `internal/cli/cmd_suite_run.go`
- Suite parsing: `internal/suite/parse.go`
- Attempt allocation + artifacts: `internal/attempt/start.go`
- Finish pipeline: `internal/report/report.go`, `internal/validate/validate.go`, `internal/expect/expect.go`
- Tests: `internal/cli/suite_run_integration_test.go`

## Runtime Flow
Per `zcl suite run --file ... --session-isolation auto|process|native --parallel N --total M --json -- <runner-cmd> ...`:

1. Parse suite:
   - Read suite file once and resolve defaults/overrides (`mode`, `timeoutMs`, `timeoutStart`, `blind`).
2. Resolve session isolation:
   - Parse `--session-isolation` (`auto|process|native`).
   - Read host capability signal `ZCL_HOST_NATIVE_SPAWN`.
   - `auto` + native-capable host => fail fast with `ZCL_E_USAGE` (refuse implicit process fallback).
   - `native` => fail fast with `ZCL_E_USAGE` and direct operator to `zcl suite plan --json` + native host orchestration.
   - `process` (or `auto` without native capability) => continue with process runner orchestration.
3. Execute in waves:
   - Build mission queue (`--total`, cycling missions when `total > mission count`).
   - Allocate attempts just-in-time before each runner spawn (`attempt.Start(...)`) to avoid pre-expiry.
   - Stamp each attempt with `attempt.json.isolationModel=process_runner`.
   - Run up to `--parallel` attempts concurrently per wave.
4. For each allocated attempt:
   - Build runner env:
     - start from current process env
     - overlay attempt `ZCL_*` env
     - include `ZCL_ISOLATION_MODEL=process_runner`
     - optionally set `ZCL_PROMPT_PATH=<attemptDir>/prompt.txt` if present
   - Guardrail: read `attempt.json` and verify IDs match env (refuse to spawn on mismatch).
   - Blind mode (when enabled): reject prompt contamination and write typed evidence (`tool.calls.jsonl` + `feedback.json`) without spawning the runner.
   - Spawn runner:
     - stream runner stdout/stderr to ZCL stderr
     - apply attempt deadline semantics from attempt timeout config
5. Finish attempt:
   - If runner exits early and `feedback.json` is missing, write canonical fail evidence (`tool.calls.jsonl` + `feedback.json`) with typed infra code.
   - Build and write `attempt.report.json`
   - Run `validate` and `expect`
6. Emit one JSON summary on stdout and exit:
   - `0` if all attempts OK
   - `2` if suite completed but some attempts failed finish/expect/validate/outcome
   - `1` for harness errors (spawn/I/O/timeouts/runner non-zero exit)

## Invariants / Guardrails
- `--json` is required.
- `--session-isolation=auto` will not silently use process orchestration when `ZCL_HOST_NATIVE_SPAWN=1`.
- Runner command must be provided after `--`.
- Runner is spawned with a clean, explicit attempt env (no implicit globals required besides the runner binary).
- ZCL does not consider the suite successful unless:
  - runner exited `0`
  - finish pipeline succeeded (`report` + `validate` + `expect`)

## Observability
- Human-readable progress (per mission) is emitted to stderr:
  - mission id, attempt id, runner basename
- Machine-readable summary is emitted once to stdout:
   - includes `sessionIsolationRequested`, `sessionIsolation`, and `hostNativeSpawnCapable`
   - includes `runnerExitCode`, typed `runnerErrorCode` (`ZCL_E_TIMEOUT`, `ZCL_E_SPAWN`, `ZCL_E_CONTAMINATED_PROMPT`), and finish results.
   - summary is also persisted as `suite.run.summary.json` in the run directory for post-mortems.

## Testing Expectations
- Happy path:
  - runner calls `zcl run -- echo hi` and `zcl feedback --ok ...`
  - suite run returns exit `0` and summary `ok=true`.
- Missing evidence:
  - runner does not write `feedback.json`
  - suite run returns exit `2` and summary indicates report/validate failures.
