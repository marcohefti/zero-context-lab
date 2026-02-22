---
name: zcl
description: Orchestrator workflow for running ZeroContext Lab (ZCL) attempts/suites with deterministic artifacts, trace-backed evidence, and fast post-mortems (shim support for "agent only types tool name").
---

# ZCL Orchestrator (Codex Skill)

This skill is for the *orchestrator* (you), not the spawned "zero context" agent.

## Goal

Run a mission through ZCL with funnel-first evidence and deterministic artifacts under `.zcl/`.

## Capability Matrix

| Capability | Implemented | Enforced | Notes |
| --- | --- | --- | --- |
| First-class campaign lifecycle (`campaign lint/run/canary/resume/status/report/publish-check`) | yes | yes | Report/publish paths are guarded by invalid-run policy and `--force`. |
| Mission-by-mission campaign engine with checkpointing (`campaign.plan.json`, `campaign.progress.jsonl`) | yes | yes | Resume and duplicate-attempt guards rely on progress ledger + campaign lock. |
| Runner adapter contract (`process_cmd`, `codex_exec`, `codex_subagent`, `claude_subagent`) | yes | yes | Normalized attempt contract requires `attemptDir`, `status`, `errors`. |
| Adapter parity conformance at campaign layer | yes | yes | Integration coverage asserts identical outcome signatures across adapter types for equivalent flows. |
| Semantic gating (`validate --semantic`, campaign semantic gate) | yes | yes | Semantic-enabled campaigns fail gate when semantic rules are unevaluated or failing. |
| MCP lifecycle controls (`max-tool-calls`, `idle-timeout-ms`, `shutdown-on-complete`) | yes | yes | Flow runner env injects MCP lifecycle knobs deterministically. |
| Cleanup hooks (`cleanup.beforeMission/afterMission/onFailure`) + campaign global timeout | yes | yes | Hook/global timeout failures produce aborted campaign with typed reason codes + cleanup lifecycle progress events. |
| Minimal campaign mode (`missionSource.path` + flows without `suiteFile`) | yes | yes | One `campaign.yaml` + missions directory is enough for routine runs. |
| Flow execution mode (`execution.flowMode`: sequence/parallel) | yes | yes | Campaign mission engine can run flow pairs sequentially or in parallel per mission. |
| Built-in traceability gate profiles (`pairGate.traceProfile`) | yes | yes | Includes `strict_browser_comparison` and `mcp_required` without external validators. |
| Campaign summary outputs (`campaign.summary.json`, `RESULTS.md`) | yes | yes | Auto-written on campaign run/report for claimed-vs-verified review. |
| Mission-only prompt mode (`promptMode: mission_only`) | yes | yes | Parse/lint/publish-check enforce no harness-term leakage in mission prompts. |
| Flow driver contract (`runner.toolDriver.kind`) | yes | yes | Supported kinds: `shell`, `cli_funnel`, `mcp_proxy`, `http_proxy`; shims are merged deterministically. |
| Auto finalization from mission result channel | yes | yes | `runner.finalization.mode=auto_from_result_json` + `resultChannel` auto-writes `feedback.json` and typed failures. |
| 3-turn mission-only finalization gating | yes | yes | `runner.finalization.minResultTurn` blocks early/intermediate result payloads until final turn. |
| Prompt materialization (`mission prompts build`) with deterministic IDs | yes | yes | Uses mission selection and stable hash IDs for reproducible prompt artifacts. |
| Runner-native subagent lifecycle management inside ZCL | partial | no | Accepted design direction; currently adapters normalize through suite-run orchestration. |

Primary evidence:
- `.zcl/.../tool.calls.jsonl`
- `.zcl/.../feedback.json` (authoritative outcome)

Secondary evidence (optional):
- `.zcl/.../notes.jsonl`
- `.zcl/.../runner.*.json`
- `.zcl/.../runner.command.txt`, `.zcl/.../runner.stdout.log`, `.zcl/.../runner.stderr.log` (suite runner IO capture)

## Operator Invocation Story

When an operator says "run this through ZCL: <mission>", do this:

1. Resolve entrypoint: prefer `zcl` on `PATH`.
2. Preflight version policy for agent reliability:
   - Check latest metadata: `zcl update status --json`
   - Prefer explicit harness floor: set `ZCL_MIN_VERSION=<semver>`; ZCL fails fast with `ZCL_E_VERSION_FLOOR` when below floor.
3. Initialize project if needed: `zcl init` (idempotent).
4. Prefer native host spawning when available (Mode A):
   - Single attempt: `zcl attempt start --suite <suiteId> --mission <missionId> --prompt <promptText> --isolation-model native_spawn --json`
   - Suite batch planning: `zcl suite plan --file <suite.(yaml|yml|json)> --json`
   - Spawn exactly one fresh native agent session per attempt and pass the returned `env`.
5. Use process-runner orchestration only as an explicit fallback (Mode B):
   - `zcl suite run --file <suite.(yaml|yml|json)> --session-isolation process --feedback-policy auto_fail --finalization-mode auto_from_result_json --result-channel file_json --result-min-turn 3 --campaign-id <campaignId> --progress-jsonl <path|-> --shim tool-cli --json -- <runner-cmd> [args...]`
   - `--shim tool-cli` lets the agent type a tool command directly while ZCL still records invocations to `tool.calls.jsonl`.
   - Suite run captures runner IO by default into `runner.*` logs for post-mortems.
   - `--feedback-policy strict` + `--finalization-mode strict` expects explicit `zcl feedback`.
   - `--finalization-mode auto_from_result_json` consumes mission proof JSON from `file_json` or `stdout_json` channel.
6. Use first-class campaign orchestration for multi-flow deterministic benchmarking:
   - `zcl campaign lint --spec <campaign.(yaml|yml|json)> --json`
   - `zcl campaign canary --spec <campaign.(yaml|yml|json)> --missions 3 --json`
   - `zcl campaign run --spec <campaign.(yaml|yml|json)> --json`
   - `zcl campaign resume --campaign-id <id> --json`
   - `zcl campaign status --campaign-id <id> --json`
   - `zcl campaign report --campaign-id <id> --json`
   - `zcl campaign publish-check --campaign-id <id> --json`
   - Minimal mode pattern: `missionSource.path: ./missions` + `flows[].runner` blocks (no `suiteFile` required).
   - Fresh-session policy: keep `runner.freshAgentPerAttempt=true` (default/enforced).
   - Traceability profile: set `pairGate.traceProfile` (`strict_browser_comparison` or `mcp_required`) for baseline gate families.
   - No-context campaign mode:
     - set `promptMode: mission_only`
     - set `flows[].runner.finalization.mode: auto_from_result_json`
     - set `flows[].runner.finalization.resultChannel.kind: file_json|stdout_json` (prefer `file_json` for multi-turn feedback loops)
     - set `flows[].runner.finalization.minResultTurn: 3` for 3-turn feedback campaigns
     - run `zcl campaign lint --spec ... --json` and fail on prompt contamination before campaign execution.
7. Finalize attempts:
   - Harness-aware mode (`promptMode: default`): agent must run `zcl feedback --ok|--fail --result ...` or `--result-json ...`.
   - No-context mode (`promptMode: mission_only`): agent emits mission result JSON on configured channel; ZCL writes `feedback.json` automatically.
8. Optionally ask for self-report feedback and persist it as secondary evidence:
   - `zcl note --kind agent --message "..."`
9. Report back from artifacts (not from transcript):
   - Primary: `tool.calls.jsonl`, `feedback.json`
   - Derived: `attempt.report.json`
   - Post-mortem: `zcl attempt explain [<attemptDir>]` (tail trace + pointers)
10. For retrieval/reporting automation use native query commands:
   - Latest attempt: `zcl attempt latest --suite <suiteId> --mission <missionId> --status ok --json`
   - Attempt index rows: `zcl attempt list --suite <suiteId> --status any --json`
   - Run index rows: `zcl runs list --suite <suiteId> --json`
11. For semantic integrity and publication guards:
   - `zcl validate --semantic [--semantic-rules <rules.(yaml|yml|json)>] --json <attemptDir|runDir>`
   - `zcl campaign publish-check --campaign-id <id> --json` before publishing any benchmark summary.
12. For deterministic prompt materialization:
   - `zcl mission prompts build --spec <campaign.(yaml|yml|json)> --template <template.txt|md> --json`
13. Start from in-repo canonical templates when bootstrapping campaign specs:
   - `examples/campaign.canonical.yaml`
   - `examples/campaign.no-context.comparison.yaml`
   - `examples/campaign.no-context.codex-exec.yaml`
   - `examples/campaign.no-context.codex-subagent.yaml`
   - `examples/campaign.no-context.claude-subagent.yaml`
   - `examples/semantic.rulepack.yaml`

## Prompt Policy (Turn 1)

Choose exactly one mode:

- `promptMode: mission_only` (preferred for zero-context):
  - Give mission intent + output contract only.
  - Do **not** mention harness commands (`zcl run`, `zcl mcp proxy`, `zcl feedback`, artifact filenames).
  - Ensure campaign finalization uses `auto_from_result_json` and prefer `resultChannel.kind=file_json`.
- `promptMode: default` (harness-aware fallback):
  - Include finish rule (`zcl feedback ...`) and funnel execution requirements.
  - If running under `zcl suite run --shim <tool>`, the agent can invoke the tool directly.
  - If no shim is installed, actions must go through ZCL funnels (for example `zcl run -- ...`).

## Mission-Only 3-Turn Recipe (Recommended)

Campaign config requirements:
- `promptMode: mission_only`
- `runner.finalization.mode: auto_from_result_json`
- `runner.finalization.resultChannel.kind: file_json`
- `runner.finalization.minResultTurn: 3`

Turn contract:
1. Mission prompt turn:
   - mission intent + output contract only.
   - no harness terms.
2. Candid feedback turn:
   - unstructured/free-text critique is allowed.
   - this turn is non-finalizable by policy (`minResultTurn: 3`).
3. Structured extraction turn:
   - runner emits mission result JSON with `"turn": 3` on the configured result channel.
   - ZCL finalizes attempt automatically.

Example mission-only final payload (`file_json`):
```json
{"ok":true,"turn":3,"resultJson":{"proof":"...","claims":[],"verified":[]}}
```

## Harness-Aware Prompting (Fallback)

Use this only when mission-only is not feasible.

Example turn:
"Use a CLI/browser tool to navigate to https://example.com and record TITLE=<...> via `zcl feedback --ok --result ...`."

## Expectations (Suite Guardrails)

Suite `expects` can be grounded in:
- `feedback.json` (`expects.ok`, `expects.result.*`)
- trace-derived constraints (`expects.trace.*`), e.g.:
  - `maxToolCallsTotal`, `maxFailuresTotal`, `maxRepeatStreak`
  - `requireCommandPrefix: ["tool-cli"]` to ensure the intended tool was actually invoked

## Migration Path: Harness Prompt -> Mission-Only

1. Move harness instructions out of mission prompts.
2. Set `promptMode: mission_only` in campaign spec.
3. Set flow finalization to `auto_from_result_json` and choose `resultChannel` (`file_json` or `stdout_json`).
4. Set `runner.toolDriver` and shims so ZCL owns tool funnel policy.
5. Run `zcl campaign lint --spec ... --json` and fix any `promptMode` violations.
6. Run canary, then full run, then `zcl campaign publish-check --campaign-id ... --json`.

## Local Install (CLI + Skill)

If `zcl` is not on `PATH` (or you want it rebuilt from this checkout):
1. Build + install the CLI and link the skill:
   - `scripts/dev-local-install.sh`
2. Optional: auto-install on `git pull` / branch switch:
   - `scripts/dev-install-git-hooks.sh`
