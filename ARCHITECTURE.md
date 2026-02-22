# Zero Context Lab (ZCL) Architecture

This doc is the high-level system overview and command map.
Detailed subsystem docs live under `docs/architecture.md` to keep this file short and to avoid drift.

Read order:
- `CONCEPT.md` (why + non-negotiables)
- `AGENTS.md` (operator workflow + builder index)
- `SCHEMAS.md` (exact v1 artifact schemas + canonical IDs)
- `docs/architecture.md` (deep dives)

## Goals
- Funnel-first evidence: metrics/scoring derive from ZCL artifacts, not runner internals.
- Deterministic artifact contract: stable shapes, bounded payloads, atomic writes.
- Tool-agnostic funnels: CLI/MCP/HTTP boundaries emit a common trace schema.
- Capability-first orchestration: prefer native host spawn isolation when available; use process runners as an explicit fallback.
- Operator UX: obvious workflows, typed errors, stable `--json` outputs for automation.

## Non-goals (core)
- ZCL is not an LLM runner, model router, or agent framework.
- ZCL does not score "thought quality" beyond what boundary evidence can support.
- No plugin runtime in v1 (compile-time packages, optional enrichments).

## High-Level System Model
- Runner/orchestrator allocates an attempt (or a suite of attempts).
- Runner performs actions only through ZCL funnels (writes `tool.calls.jsonl`).
- Runner writes authoritative outcome via `zcl feedback` (writes `feedback.json`).
- ZCL computes + validates derived artifacts (`attempt.report.json`, `zcl validate`, `zcl expect`).
- First-class campaigns execute mission-by-mission across flows with lock-protected checkpoints (`campaign.plan.json`, `campaign.progress.jsonl`, `campaign.run.state.json`).

Primary evidence:
- `tool.calls.jsonl` (trace)
- `feedback.json` (authoritative outcome)

## Command Surface (Operator Map)
Orchestrator-facing commands should prefer stable `--json` output.

- `zcl init`
- `zcl update status [--cached] [--json]`
- `zcl contract --json`
- `zcl suite plan --file <suite.(yaml|yml|json)> --json`
- `zcl suite run --file <suite.(yaml|yml|json)> [--session-isolation auto|process|native] [--feedback-policy strict|auto_fail] [--campaign-id <id>] [--campaign-state <path>] [--progress-jsonl <path|->] --json -- <runner-cmd> [args...]`
- `zcl campaign lint --spec <campaign.(yaml|yml|json)> [--json]`
- `zcl campaign run --spec <campaign.(yaml|yml|json)> [--missions N] [--mission-offset N] [--json]`
- `zcl campaign canary --spec <campaign.(yaml|yml|json)> [--missions N] [--mission-offset N] [--json]`
- `zcl campaign resume --campaign-id <id> [--json]`
- `zcl campaign status --campaign-id <id> [--json]`
- `zcl campaign report --campaign-id <id> [--format json,md] [--force] [--json]`
- `zcl campaign publish-check --campaign-id <id> [--force] [--json]`
- `zcl runs list [--out-root .zcl] [--suite <suiteId>] [--status any|ok|fail|missing_feedback] [--limit N] --json`
- `zcl attempt start --suite <suiteId> --mission <missionId> [--isolation-model process_runner|native_spawn] --json`
- `zcl attempt env [--format sh|dotenv] [--json] [<attemptDir>]`
- `zcl attempt finish [--strict] [--strict-expect] [--json] [<attemptDir>]`
- `zcl attempt explain [--strict] [--json] [--tail N] [<attemptDir>]`
- `zcl attempt list [--out-root .zcl] [--suite <suiteId>] [--mission <missionId>] [--status any|ok|fail|missing_feedback] [--tag <tag>] [--limit N] --json`
- `zcl attempt latest [--out-root .zcl] [--suite <suiteId>] [--mission <missionId>] [--status any|ok|fail|missing_feedback] [--tag <tag>] --json`
- `zcl run -- <cmd> [args...]`
- `zcl mcp proxy [--max-tool-calls N] [--idle-timeout-ms N] [--shutdown-on-complete] -- <server-cmd> [args...]`
- `zcl http proxy --upstream <url> [--listen 127.0.0.1:0] [--max-requests N] [--json]`
- `zcl feedback --ok|--fail --result <string>|--result-json <json>`
- `zcl note [--kind agent|operator|system] --message <string>|--data-json <json>`
- `zcl report [--strict] [--json] <attemptDir|runDir>`
- `zcl validate [--strict] [--semantic] [--semantic-rules <path>] [--json] <attemptDir|runDir>`
- `zcl expect [--strict] --json <attemptDir|runDir>`
- `zcl mission prompts build --spec <campaign.(yaml|yml|json)> --template <template.txt|md> [--out <path>] [--json]`
- `zcl replay [--execute] [--allow <cmd1,cmd2>] [--allow-all] [--max-steps N] [--stdin] --json <attemptDir>`
- `zcl doctor [--json]`
- `zcl gc [--dry-run] [--json]`
- `zcl pin --run-id <runId> --on|--off [--json]`
- `zcl enrich --runner codex|claude --rollout <rollout.jsonl> [<attemptDir>]`
- Real example command:
- `zcl enrich --runner claude --rollout /Users/<you>/.claude/projects/<project>/<session>.jsonl .zcl/runs/<runId>/attempts/<attemptId>`

Stdout/stderr contract (operator UX + automation):
- When `--json` is present, stdout is JSON only.
- Human progress logs and runner passthrough go to stderr.
- `zcl report --json <runDir>` also persists `run.report.json` in the run directory.
- `zcl suite run --progress-jsonl <path|->` emits structured progress events suitable for dashboards/watchers.

## Contracts (v1)
Exact shapes are in `SCHEMAS.md` and `zcl contract --json`.

Attempt context is provided to runners as env vars:
- `ZCL_RUN_ID`, `ZCL_SUITE_ID`, `ZCL_MISSION_ID`, `ZCL_ATTEMPT_ID`
- `ZCL_OUT_DIR` (attempt directory; identity boundary)
- `ZCL_TMP_DIR` (scratch directory under `<outRoot>/tmp/<runId>/<attemptId>/`)
- `ZCL_AGENT_ID` (optional runner correlation)
- `ZCL_ISOLATION_MODEL` (optional; `process_runner|native_spawn`)
- `ZCL_PROMPT_PATH` (optional pointer to `prompt.txt`; set by orchestration when present)
- `ZCL_MIN_VERSION` (optional semver floor; if set and current `zcl` is below floor, commands fail fast with `ZCL_E_VERSION_FLOOR`)
- `attempt.env.sh` is auto-written in each attempt dir and can be sourced directly for operator/agent handoff.

Safety knobs:
- `zcl run --capture --capture-raw` is blocked in CI/strict contexts unless `ZCL_ALLOW_UNSAFE_CAPTURE=1`.

## Code Map (Where Things Live)
- `cmd/zcl`: CLI entrypoint.
- `internal/cli`: command handlers (UX + stable JSON output).
- `internal/attempt`: attempt allocation + metadata (`attempt.json`, `attempt.env.sh`, `prompt.txt`, `ZCL_TMP_DIR`).
- `internal/planner`: suite planning (suite file -> planned attempts + env).
- `internal/suite`: suite parsing + expectations (runner-agnostic).
- `internal/campaign`: first-class campaign specs, run-state persistence, campaign report materialization.
- `internal/semantic`: semantic validity gates and rule-pack evaluation.
- `internal/runners`: runner adapters used by campaign mission engine.
- `internal/funnel`: protocol funnels (CLI/MCP/HTTP).
- `internal/trace`: trace shaping, bounds, redaction hooks.
- `internal/report`: computes `attempt.report.json`.
- `internal/validate`: typed integrity validation.
- `internal/expect`: suite expectation evaluation.
- `internal/store`: atomic writes, JSONL append safety, retention helpers.
- `internal/enrich`: optional runner enrichment (must not affect scoring).

## Deep Dives
See `docs/architecture.md`, starting with:
- `docs/architecture/suite-run.md`
- `docs/architecture/evidence-pipeline.md`
- `docs/architecture/write-safety.md`
