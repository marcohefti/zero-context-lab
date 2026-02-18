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
- Runner-agnostic orchestration: runners are external processes; ZCL provides env + guardrails.
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

Primary evidence:
- `tool.calls.jsonl` (trace)
- `feedback.json` (authoritative outcome)

## Command Surface (Operator Map)
Orchestrator-facing commands should prefer stable `--json` output.

- `zcl init`
- `zcl contract --json`
- `zcl suite plan --file <suite.(yaml|yml|json)> --json`
- `zcl suite run --file <suite.(yaml|yml|json)> --json -- <runner-cmd> [args...]`
- `zcl attempt start --suite <suiteId> --mission <missionId> --json`
- `zcl attempt finish [--strict] [--strict-expect] [--json] [<attemptDir>]`
- `zcl attempt explain [--strict] [--json] [--tail N] [<attemptDir>]`
- `zcl run -- <cmd> [args...]`
- `zcl mcp proxy -- <server-cmd> [args...]`
- `zcl http proxy --upstream <url> [--listen 127.0.0.1:0] [--max-requests N] [--json]`
- `zcl feedback --ok|--fail --result <string>|--result-json <json>`
- `zcl note [--kind agent|operator|system] --message <string>|--data-json <json>`
- `zcl report [--strict] [--json] <attemptDir|runDir>`
- `zcl validate [--strict] [--json] <attemptDir|runDir>`
- `zcl expect [--strict] --json <attemptDir|runDir>`
- `zcl replay [--execute] [--allow <cmd1,cmd2>] [--allow-all] [--max-steps N] [--stdin] --json <attemptDir>`
- `zcl doctor [--json]`
- `zcl gc [--dry-run] [--json]`
- `zcl pin --run-id <runId> --on|--off [--json]`
- `zcl enrich --runner codex --rollout <rollout.jsonl> [<attemptDir>]`

Stdout/stderr contract (operator UX + automation):
- When `--json` is present, stdout is JSON only.
- Human progress logs and runner passthrough go to stderr.

## Contracts (v1)
Exact shapes are in `SCHEMAS.md` and `zcl contract --json`.

Attempt context is provided to runners as env vars:
- `ZCL_RUN_ID`, `ZCL_SUITE_ID`, `ZCL_MISSION_ID`, `ZCL_ATTEMPT_ID`
- `ZCL_OUT_DIR` (attempt directory; identity boundary)
- `ZCL_TMP_DIR` (scratch directory under `<outRoot>/tmp/<runId>/<attemptId>/`)
- `ZCL_AGENT_ID` (optional runner correlation)
- `ZCL_PROMPT_PATH` (optional pointer to `prompt.txt`; set by orchestration when present)

## Code Map (Where Things Live)
- `cmd/zcl`: CLI entrypoint.
- `internal/cli`: command handlers (UX + stable JSON output).
- `internal/attempt`: attempt allocation + metadata (`attempt.json`, `prompt.txt`, `ZCL_TMP_DIR`).
- `internal/planner`: suite planning (suite file -> planned attempts + env).
- `internal/suite`: suite parsing + expectations (runner-agnostic).
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
