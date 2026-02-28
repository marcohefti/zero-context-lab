# ZCL v1 Schemas (Artifact Contract)

This doc is the exact artifact schema contract for ZCL v1.

Notes:
- All timestamps are RFC3339 UTC (ZCL currently writes `time.RFC3339Nano`).
- JSON files are written atomically (temp file + rename).
- JSONL files are append-only streams; each line is one JSON object.

## Canonical ID Formats (v1)

These strings are the identity surface for artifact layout and trace correlation. They are intentionally:
- path-safe (usable in directory names)
- stable and boring (diff-friendly)

`runId`:
- Format: `YYYYMMDD-HHMMSSZ-<hex6>`
- Regex: `^[0-9]{8}-[0-9]{6}Z-[0-9a-f]{6}$`
- Example: `20260215-180012Z-09c5a6`

`suiteId` / `missionId`:
- Canonical format: lowercase kebab-case components
- Regex: `^[a-z0-9]+(?:-[a-z0-9]+)*$`
- ZCL canonicalizes inputs by: lowercasing, `_` -> `-`, replacing non `[a-z0-9-]` with `-`, collapsing dashes, trimming leading/trailing dashes.

`attemptId`:
- Format: `<index3>-<missionId>-r<retry>`
- Regex: `^[0-9]{3}-[a-z0-9]+(?:-[a-z0-9]+)*-r[0-9]+$`
- Example: `001-latest-blog-title-r1`

`agentId` (optional):
- Opaque runner correlation id (not used in paths).

## `run.json` (v1)

Path: `.zcl/runs/<runId>/run.json`

Required fields:
```json
{
  "schemaVersion": 1,
  "artifactLayoutVersion": 1,
  "runId": "20260215-180012Z-09c5a6",
  "suiteId": "heftiweb-smoke",
  "createdAt": "2026-02-15T18:00:12.123456789Z",
  "pinned": true
}
```

## `suite.json` (snapshot; optional)

Path: `.zcl/runs/<runId>/suite.json`

If `zcl attempt start --suite-file <path>` or `zcl suite plan --file <path>` is used, ZCL snapshots the parsed suite file here as **canonical JSON** for diffability.

Accepted input formats:
- JSON (`.json`)
- YAML (`.yaml`, `.yml`)

Minimal v1 suite shape (example):
```json
{
  "version": 1,
  "suiteId": "heftiweb-smoke",
  "defaults": {
    "timeoutMs": 120000,
    "timeoutStart": "first_tool_call",
    "feedbackPolicy": "auto_fail",
    "mode": "discovery",
    "blind": true,
    "blindTerms": ["zcl", "feedback.json"]
  },
  "missions": [
    {
      "missionId": "latest-blog-title",
      "prompt": "Navigate to https://heftiweb.ch -> Blog -> latest article. Record ARTICLE_TITLE=<title> using zcl feedback.",
      "tags": ["browser", "navigation", "smoke"],
      "expects": {
        "ok": true,
        "result": {
          "type": "json",
          "requiredJsonPointers": ["/proof/title"]
        },
        "trace": {
          "maxToolCallsTotal": 30,
          "maxFailuresTotal": 5,
          "maxRepeatStreak": 10,
          "requireCommandPrefix": ["tool-cli"]
        }
      }
    }
  ]
}
```

`expects.result` supports:
- `type`: `string|json`
- `equals`, `pattern` (for `type=string`)
- `requiredJsonPointers` (for `type=json`): RFC 6901 pointers that must exist in `feedback.resultJson`

## `suite.run.summary.json` (optional; v1)

Path: `.zcl/runs/<runId>/suite.run.summary.json`

Written by `zcl suite run --json` as a persisted run-level execution summary.

Example:
```json
{
  "schemaVersion": 1,
  "ok": true,
  "runId": "20260215-180012Z-09c5a6",
  "suiteId": "heftiweb-smoke",
  "mode": "discovery",
  "outRoot": ".zcl",
  "sessionIsolationRequested": "process",
  "sessionIsolation": "process_runner",
  "hostNativeSpawnCapable": false,
  "runtimeStrategyChain": ["codex_app_server"],
  "runtimeStrategySelected": "",
  "feedbackPolicy": "auto_fail",
  "campaignId": "heftiweb-smoke",
  "campaignStatePath": ".zcl/campaigns/heftiweb-smoke/campaign.state.json",
  "campaignProfile": {
    "mode": "discovery",
    "timeoutMs": 120000,
    "timeoutStart": "first_tool_call",
    "isolationModel": "process_runner",
    "feedbackPolicy": "auto_fail",
    "finalization": "auto_from_result_json",
    "resultChannel": "file_json",
    "resultMinTurn": 3,
    "parallel": 1,
    "total": 2,
    "failFast": true,
    "blind": false
  },
  "comparabilityKey": "cp-abcdef0123456789",
  "attempts": [],
  "passed": 0,
  "failed": 0,
  "createdAt": "2026-02-15T18:00:12.123456789Z"
}
```

Notes:
- `runtimeStrategyChain` is the ordered fallback chain considered for native mode.
- `runtimeStrategySelected` is set when native mode selects a strategy.
- `campaignProfile.finalization` records attempt finalization policy (`strict|auto_fail|auto_from_result_json`).
- `campaignProfile.resultChannel` records mission result channel (`none|file_json|stdout_json`).
- `campaignProfile.resultMinTurn` records minimum mission result payload turn accepted for auto finalization.
- `campaignProfile.nativeModel` (optional) records native `thread/start` model override in native mode.
- `campaignProfile.reasoningEffort` and `campaignProfile.reasoningPolicy` (optional) record native reasoning-hint configuration.
- In no-context mode (`promptMode: mission_only`), `auto_from_result_json` is required and ZCL writes `feedback.json` from the configured result channel.

## `attempt.json` (v1)

Path: `.zcl/runs/<runId>/attempts/<attemptId>/attempt.json`

Required fields:
```json
{
  "schemaVersion": 1,
  "runId": "20260215-180012Z-09c5a6",
  "suiteId": "heftiweb-smoke",
  "missionId": "latest-blog-title",
  "attemptId": "001-latest-blog-title-r1",
  "mode": "discovery",
  "startedAt": "2026-02-15T18:00:13.123456789Z"
}
```

Optional fields:
- `agentId` (runner-provided correlation id)
- `isolationModel` (`process_runner` or `native_spawn`; records how fresh session isolation was orchestrated)
- `timeoutMs` (attempt deadline in ms from `startedAt`; funnels should enforce this as a mission-level deadline)
- `timeoutStart` (`attempt_start` or `first_tool_call`; if omitted, discovery defaults to `first_tool_call`)
- `timeoutStartedAt` (set when `timeoutStart=first_tool_call` and first funnel action starts)
- `blind` (enable zero-context prompt contamination checks)
- `blindTerms` (normalized harness terms used by contamination checks)
- `scratchDir` (path relative to `<outRoot>/` for per-attempt scratch space under `<outRoot>/tmp/<runId>/<attemptId>`)
- `attemptEnvSh` (ready-to-source env handoff file path relative to attemptDir; default `attempt.env.sh`)
- `nativeResult` (native codex result extraction provenance):
  - `resultSource` (`task_complete_last_agent_message|phase_final_answer|delta_fallback`; empty when no final-answer source exists)
  - `phaseAware` (whether `phase` metadata was observed on assistant messages)
  - `commentaryMessagesObserved` (count of `phase=commentary` assistant messages observed)
  - `reasoningItemsObserved` (count of reasoning items observed)

## `prompt.txt` (snapshot; optional)

Path: `.zcl/runs/<runId>/attempts/<attemptId>/prompt.txt`

If `zcl attempt start --prompt <text>` is used, ZCL snapshots the prompt text here.

## `attempt.env.sh` (optional; auto-written)

Path: `.zcl/runs/<runId>/attempts/<attemptId>/attempt.env.sh`

Shell export file containing canonical attempt `ZCL_*` env for sourcing.

## `attempt.runtime.env.json` (optional; auto-written)

Path: `.zcl/runs/<runId>/attempts/<attemptId>/attempt.runtime.env.json`

Purpose:
- durable postmortem snapshot of runtime-effective env visibility and prompt provenance
- written by `zcl suite run` for both native and process execution paths

Shape (v1):
```json
{
  "schemaVersion": 1,
  "runId": "20260215-180012Z-09c5a6",
  "suiteId": "heftiweb-smoke",
  "missionId": "latest-blog-title",
  "attemptId": "001-latest-blog-title-r1",
  "createdAt": "2026-02-15T18:00:14.123456789Z",
  "runtime": {
    "isolationModel": "process_runner",
    "toolDriverKind": "shell",
    "runtimeId": "",
    "nativeMode": false
  },
  "prompt": {
    "sourceKind": "suite_prompt",
    "sourcePath": "/abs/path/to/suite.json",
    "templatePath": "/abs/path/to/template.txt",
    "sha256": "2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824",
    "bytes": 123
  },
  "env": {
    "explicit": {
      "ZCL_ATTEMPT_ID": "001-latest-blog-title-r1"
    },
    "effectiveKeys": ["HOME", "PATH", "ZCL_ATTEMPT_ID"],
    "blockedKeys": []
  }
}
```

Notes:
- `env.explicit` values are redacted with native env redaction policy.
- `env.effectiveKeys` is key-only visibility after merge/filter (no values).
- `env.blockedKeys` is populated for native runtime policy filtering.
- `prompt.sourceKind` is `suite_prompt` for plain suite runs; campaign runs include flow-aware kinds such as `flow_prompt_source` and `flow_prompt_template`.

## `tool.calls.jsonl` trace events (v1)

Path: `.zcl/runs/<runId>/attempts/<attemptId>/tool.calls.jsonl`

Each line is one v1 `TraceEvent`:
```json
{
  "v": 1,
  "ts": "2026-02-15T18:00:14.123456789Z",
  "runId": "20260215-180012Z-09c5a6",
  "suiteId": "heftiweb-smoke",
  "missionId": "latest-blog-title",
  "attemptId": "001-latest-blog-title-r1",
  "tool": "cli",
  "op": "exec",
  "input": {"argv":["echo","hello"]},
  "result": {
    "ok": true,
    "durationMs": 12,
    "exitCode": 0
  },
  "io": {
    "outBytes": 6,
    "errBytes": 0,
    "outPreview": "hello\n"
  },
  "redactionsApplied": []
}
```

Notes:
- `suiteId` and `agentId` are optional.
- `input`/`enrichment` are stored as bounded/canonicalized JSON when possible; oversized inputs are truncated or replaced with a bounded placeholder object plus `ZCL_W_INPUT_TRUNCATED`.
- `result.code` is a typed ZCL code when ZCL can classify; otherwise a normalized tool error code.
- `redactionsApplied` lists the redaction rules applied to this event (informational only; scoring must not depend on it).
- Native runtime events use `tool: "native"` and carry runtime/session/thread/turn correlation fields in `input`.
- Native stream failures/crashes mark `integrity.truncated=true` and surface typed `ZCL_E_RUNTIME_*` codes.

## `feedback.json` (v1)

Path: `.zcl/runs/<runId>/attempts/<attemptId>/feedback.json`

Exactly one of `result` or `resultJson` must be present:
```json
{
  "schemaVersion": 1,
  "runId": "20260215-180012Z-09c5a6",
  "suiteId": "heftiweb-smoke",
  "missionId": "latest-blog-title",
  "attemptId": "001-latest-blog-title-r1",
  "ok": true,
  "result": "ARTICLE_TITLE=Example",
  "classification": "output_shape",
  "decisionTags": ["success"],
  "createdAt": "2026-02-15T18:00:40.123456789Z",
  "redactionsApplied": []
}
```

## `notes.jsonl` note events (v1)

Path: `.zcl/runs/<runId>/attempts/<attemptId>/notes.jsonl`

Each line is one v1 `NoteEvent`:
```json
{
  "v": 1,
  "ts": "2026-02-15T18:00:41.123456789Z",
  "runId": "20260215-180012Z-09c5a6",
  "suiteId": "heftiweb-smoke",
  "missionId": "latest-blog-title",
  "attemptId": "001-latest-blog-title-r1",
  "kind": "agent",
  "message": "The tool output was ambiguous; a typed error code would help.",
  "tags": ["ux", "naming"],
  "redactionsApplied": []
}
```

Notes:
- Use `message` for free-form (bounded) notes.
- Use `data` for structured notes.

## `captures.jsonl` capture index events (v1)

Path: `.zcl/runs/<runId>/attempts/<attemptId>/captures.jsonl`

Each line is one v1 `CaptureEvent` (secondary evidence index for `zcl run --capture` outputs):
```json
{
  "v": 1,
  "ts": "2026-02-15T18:00:41.123456789Z",
  "runId": "20260215-180012Z-09c5a6",
  "suiteId": "heftiweb-smoke",
  "missionId": "latest-blog-title",
  "attemptId": "001-latest-blog-title-r1",
  "tool": "cli",
  "op": "exec",
  "input": {"argv":["echo","hello"]},
  "stdoutPath": "captures/cli/1771241714107678000.stdout.log",
  "stderrPath": "captures/cli/1771241714107678000.stderr.log",
  "stdoutBytes": 6,
  "stderrBytes": 0,
  "stdoutSha256": "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
  "stderrSha256": "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
  "stdoutTruncated": false,
  "stderrTruncated": false,
  "redacted": true,
  "redactionsApplied": ["openai_key"],
  "maxBytes": 4194304
}
```

Notes:
- Captured `captures/**` files are redacted by default. Use `zcl run --capture --capture-raw` to store raw output (unsafe).
- In CI/strict contexts, raw capture is blocked unless `ZCL_ALLOW_UNSAFE_CAPTURE=1`.
- Strict validation in `ci` mode rejects raw capture events (`redacted=false`) as `ZCL_E_UNSAFE_EVIDENCE`.

## `attempt.report.json` (v1)

Path: `.zcl/runs/<runId>/attempts/<attemptId>/attempt.report.json`

Required fields (metrics fields may be zero when computed from partial evidence in discovery mode):
```json
{
  "schemaVersion": 1,
  "runId": "20260215-180012Z-09c5a6",
  "suiteId": "heftiweb-smoke",
  "missionId": "latest-blog-title",
  "attemptId": "001-latest-blog-title-r1",
  "computedAt": "2026-02-15T18:00:55.123456789Z",
  "startedAt": "2026-02-15T18:00:13.123456789Z",
  "endedAt": "2026-02-15T18:00:40.123456789Z",
  "ok": true,
  "result": "ARTICLE_TITLE=Example",
  "classification": "output_shape",
  "decisionTags": ["success"],
  "artifacts": {
    "attemptJson": "attempt.json",
    "toolCallsJsonl": "tool.calls.jsonl",
    "feedbackJson": "feedback.json",
    "attemptEnvSh": "attempt.env.sh",
    "attemptRuntimeEnvJson": "attempt.runtime.env.json",
    "notesJsonl": "notes.jsonl",
    "promptTxt": "prompt.txt",
    "runnerCommandTxt": "runner.command.txt",
    "runnerStdoutLog": "runner.stdout.log",
    "runnerStderrLog": "runner.stderr.log"
  },
  "integrity": {
    "tracePresent": true,
    "traceNonEmpty": true,
    "feedbackPresent": true,
    "promptContaminated": false
  },
  "failureCodeHistogram": {
    "ZCL_E_SPAWN": 1
  },
  "timedOutBeforeFirstToolCall": false,
  "tokenEstimates": {
    "source": "runner.metrics",
    "inputTokens": 111,
    "outputTokens": 222,
    "totalTokens": 333
  },
  "signals": {
    "repeatMaxStreak": 12,
    "distinctCommandSignatures": 3,
    "failureRateBps": 7000,
    "noProgressSuspected": true,
    "commandNamesSeen": ["tool-cli", "curl"]
  },
  "metrics": {
    "toolCallsTotal": 3,
    "failuresTotal": 0,
    "failuresByCode": {
      "ZCL_E_SPAWN": 1
    },
    "retriesTotal": 0,
    "timeoutsTotal": 0,
    "wallTimeMs": 42000,
    "durationMsTotal": 123,
    "durationMsMin": 1,
    "durationMsMax": 80,
    "durationMsAvg": 41,
    "durationMsP50": 40,
    "durationMsP95": 79,
    "outBytesTotal": 12345,
    "errBytesTotal": 12,
    "outPreviewTruncations": 0,
    "errPreviewTruncations": 0,
    "toolCallsByTool": {
      "cli": 3
    },
    "toolCallsByOp": {
      "exec": 3
    }
  }
}
```

Optional fields:
- `integrity`: cheap funnel integrity signals (`tracePresent`, `traceNonEmpty`, `feedbackPresent`, `funnelBypassSuspected`).
- `failureCodeHistogram`: top-level alias of `metrics.failuresByCode` for easier aggregation.
- `timedOutBeforeFirstToolCall`: timeout expired before first traced action could run.
- `tokenEstimates`: lightweight token estimates from `runner.metrics.json` (fallback: trace byte heuristic).
- `expectations`: when `suite.json` exists and contains `expects` for the mission, `zcl report` evaluates them against `feedback.json`.
- `nativeResult`: mirrors `attempt.json.nativeResult` provenance for native codex result extraction.

## `oracle.verdict.json` (optional; v1)

Path: `.zcl/runs/<runId>/attempts/<attemptId>/oracle.verdict.json`

Written by:
- first-class campaign gate evaluation when `promptMode: exam`

Purpose:
- records host-side oracle evaluator verdict and typed reason codes without leaking oracle text into agent prompt artifacts.

Example:
```json
{
  "schemaVersion": 1,
  "campaignId": "cmp-exam",
  "flowId": "flow-a",
  "missionId": "m1",
  "attemptId": "001-m1-r1",
  "attemptDir": "/abs/path/.zcl/runs/20260222-120000Z-a1b2c3/attempts/001-m1-r1",
  "oraclePath": "/abs/path/to/host-oracles/m1.md",
  "evaluatorKind": "script",
  "evaluatorCommand": ["node", "./scripts/eval-mission.mjs"],
  "promptMode": "exam",
  "ok": false,
  "reasonCodes": ["ZCL_E_CAMPAIGN_ORACLE_EVALUATION_FAILED"],
  "message": "proof/title did not match oracle",
  "mismatches": [
    {
      "field": "blogUrl",
      "op": "eq",
      "mismatchClass": "format",
      "expected": "https://blog.heftiweb.ch",
      "actual": "https://blog.heftiweb.ch/",
      "normalizedExpected": "https://blog.heftiweb.ch",
      "normalizedActual": "https://blog.heftiweb.ch"
    }
  ],
  "policyDisposition": "warn",
  "warnings": ["format_only_oracle_mismatch"],
  "executedAt": "2026-02-22T12:00:22.123456789Z"
}
```

## `run.report.json` (optional; v1)

Path: `.zcl/runs/<runId>/run.report.json`

Written by `zcl report [--strict] --json <runDir>`.
The JSON emitted to stdout for run-level report is the same shape that is persisted here.

Required fields:
```json
{
  "schemaVersion": 1,
  "ok": true,
  "target": "run",
  "runId": "20260215-180012Z-09c5a6",
  "suiteId": "heftiweb-smoke",
  "path": "/abs/path/to/.zcl/runs/20260215-180012Z-09c5a6",
  "attempts": [],
  "aggregate": {
    "attemptsTotal": 0,
    "passed": 0,
    "failed": 0,
    "task": {
      "passed": 0,
      "failed": 0,
      "unknown": 0
    },
    "evidence": {
      "complete": 0,
      "incomplete": 0
    },
    "orchestration": {
      "healthy": 0,
      "infraFailed": 0
    }
  }
}
```

## `campaign.spec.v1` (input contract; strict)

Path:
- `internal/campaign/campaign.spec.schema.json`
- commonly authored as `campaign.yaml` / `campaign.json`

Parse/validation behavior:
- Unknown fields fail parse (strict decode), except explicit `x-*` extensions.
- First-class commands (`zcl campaign lint/run/canary/resume`) all use this contract.

Core enforced fields:
- `campaignId`, `flows[]`
- `promptMode`: `default|mission_only|exam`
- `noContext.forbiddenPromptTerms`: contamination guard list (`mission_only` defaults to harness-term leakage checks; `exam` defaults to oracle-leakage patterns)
- mission sources:
  - legacy minimal mode: `missionSource.path`
  - split exam mode: `missionSource.promptSource.path`, `missionSource.oracleSource.path`, `missionSource.oracleSource.visibility` (`workspace|host_only`)
  - `missionSource.selection` (`all|mission_id|index|range`)
- `evaluation`:
  - `mode`: `none|oracle`
  - `evaluator.kind`: `script|builtin_rules`
  - `evaluator.command`: argv (required when `evaluator.kind=script` in exam mode)
  - `oraclePolicy.mode`: `strict|normalized|semantic`
  - `oraclePolicy.formatMismatch`: `fail|warn|ignore`
- `execution.flowMode` (`sequence|parallel`)
- `pairGate` (`enabled`, `stopOnFirstMissionFailure`, `traceProfile`)
- `flowGate` alias of `pairGate` (for N-flow semantics; if both are set they must match)
- `semantic` (`enabled`, `rulesPath`)
- `cleanup` (`beforeMission`, `afterMission`, `onFailure`)
- `timeouts` (`campaignGlobalTimeoutMs`, `defaultAttemptTimeoutMs`, `cleanupHookTimeoutMs`, `missionEnvelopeMs`, `watchdogHeartbeatMs`, `watchdogHardKillContinue`, `timeoutStart`)
- `invalidRunPolicy` (`statuses`, `publishRequiresValid`, `forceFlag`)
- flow prompt controls:
  - `flows[].promptSource.path` (per-flow mission-pack prompt source override when `suiteFile` is omitted)
  - `flows[].promptTemplate.path` (flow-level prompt template materialized at campaign parse/runtime)
  - `flows[].promptTemplate.allowRunnerEnvKeys` (allowlisted `runner.env` keys exposed as `{{runnerEnv.KEY}}`)
- flow tool isolation policy:
  - `flows[].toolPolicy.allow[]|deny[]` with `namespace` and/or `prefix`
  - `flows[].toolPolicy.aliases` for deterministic prefix alias expansion
- `flows[].runner`:
  - `type`: `process_cmd|codex_exec|codex_subagent|claude_subagent|codex_app_server`
  - `command` (required except `codex_app_server`), `env`, `sessionIsolation`, `feedbackPolicy`, `freshAgentPerAttempt`
  - `runtimeStrategies`: ordered strategy fallback chain for native execution (for example `["codex_app_server","provider_stub"]`)
  - `model` (optional, `codex_app_server` only): native `thread/start` model override
  - `modelReasoningEffort` (optional, `codex_app_server` only): `none|minimal|low|medium|high|xhigh`
  - `modelReasoningPolicy` (optional, `codex_app_server` only): `best_effort|required` (defaults to `best_effort` when effort is set)
  - `toolDriver.kind`: `shell|cli_funnel|mcp_proxy|http_proxy`
  - `finalization.mode`: `strict|auto_fail|auto_from_result_json`
  - `finalization.minResultTurn`: integer >= 1 (supports non-finalizable intermediate turns)
  - `finalization.resultChannel.kind`: `none|file_json|stdout_json`

Mission-only guardrails:
- `promptMode: mission_only` requires `flows[].runner.finalization.mode=auto_from_result_json`.
- Prompt contamination against forbidden harness terms fails lint/parse/publish-check with `ZCL_E_CAMPAIGN_PROMPT_MODE_VIOLATION`.
- `promptMode: mission_only` with `flows[].runner.toolDriver.kind=cli_funnel` requires one of `runner.shims` or `runner.toolDriver.shims`; violations return `ZCL_E_CAMPAIGN_TOOL_DRIVER_SHIM_REQUIRED`.
- `flows[].runner.finalization.minResultTurn` can enforce 3-turn workflows by requiring mission result payload field `"turn"` to be >= configured value.

Exam-mode guardrails:
- `promptMode: exam` requires split mission architecture (`missionSource.oracleSource.path`) and prompt sources via campaign-level `missionSource.promptSource.path` or per-flow `flows[].promptSource.path`; `missionSource.path` is rejected.
- `promptMode: exam` requires `evaluation.mode=oracle` and evaluator config: `evaluation.evaluator.kind=script` with non-empty `evaluation.evaluator.command`, or `evaluation.evaluator.kind=builtin_rules`; missing/invalid config returns `ZCL_E_CAMPAIGN_ORACLE_EVALUATOR_REQUIRED`.
- `evaluation.oraclePolicy.formatMismatch=warn|ignore` allows format-only oracle mismatches to be non-gating while preserving mismatch evidence in `oracle.verdict.json`.
- `promptMode: exam` enforces prompt contamination checks against oracle-leak patterns; violations return `ZCL_E_CAMPAIGN_EXAM_PROMPT_VIOLATION`.
- `promptMode: exam` with `missionSource.oracleSource.visibility=host_only` rejects oracle paths inside the detected agent-readable workspace root and returns `ZCL_E_CAMPAIGN_ORACLE_VISIBILITY_VIOLATION`.
- campaign gate writes `oracle.verdict.json` per attempt and merges oracle evaluator reason codes into mission gate validity.

Tool-policy guardrails:
- `flows[].toolPolicy` is enforced from `tool.calls.jsonl` with typed violation code `ZCL_E_CAMPAIGN_TOOL_POLICY_VIOLATION`.
- invalid tool policy config fails lint/parse with `ZCL_E_CAMPAIGN_TOOL_POLICY_INVALID`.

Contract discoverability:
- `zcl contract --json` includes `campaignSchema` (campaign fields) and `runtimeSchema` (strategy IDs, capabilities, health metrics, defaults).

## `campaign.state.json` (optional; v1)

Path: `.zcl/campaigns/<campaignId>/campaign.state.json`

Written/updated by `zcl suite run` when campaign continuity is enabled (default campaign id = suite id).

Example:
```json
{
  "schemaVersion": 1,
  "campaignId": "heftiweb-smoke",
  "suiteId": "heftiweb-smoke",
  "updatedAt": "2026-02-20T10:01:02.123456789Z",
  "latestRunId": "20260215-180012Z-09c5a6",
  "runs": [
    {
      "runId": "20260215-180012Z-09c5a6",
      "createdAt": "2026-02-15T18:00:12.123456789Z",
      "mode": "discovery",
      "outRoot": ".zcl",
      "sessionIsolation": "process_runner",
      "comparabilityKey": "cp-abcdef0123456789",
      "feedbackPolicy": "auto_fail",
      "parallel": 1,
      "total": 2,
      "failFast": true,
      "passed": 2,
      "failed": 0
    }
  ]
}
```

## `campaign.run.state.json` (optional; v1)

Path: `.zcl/campaigns/<campaignId>/campaign.run.state.json`

Written by first-class campaign commands (`zcl campaign run|canary|resume`) and consumed by:
- `zcl campaign status`
- `zcl campaign report`
- `zcl campaign publish-check`

Execution model note:
- Campaign orchestration is mission-by-mission.
- Progress is checkpointed via `campaign.plan.json` + `campaign.progress.jsonl`.
- Resume logic uses progress checkpoints (not inferred counters) to avoid duplicate attempts.

Example:
```json
{
  "schemaVersion": 1,
  "campaignId": "cmp-main",
  "runId": "20260222-120000Z-a1b2c3",
  "specPath": "/abs/path/campaign.yaml",
  "outRoot": ".zcl",
  "status": "valid",
  "reasonCodes": [],
  "startedAt": "2026-02-22T12:00:00.123456789Z",
  "updatedAt": "2026-02-22T12:01:00.123456789Z",
  "completedAt": "2026-02-22T12:01:00.123456789Z",
  "totalMissions": 3,
  "missionOffset": 0,
  "missionsCompleted": 3,
  "canary": false,
  "resumedFromRunId": "20260222-110000Z-ffeedd",
  "flowRuns": [],
  "missionGates": []
}
```

Status values:
- `running`
- `valid`
- `invalid`
- `aborted`

## `campaign.plan.json` (optional; v1)

Path: `.zcl/campaigns/<campaignId>/campaign.plan.json`

Written by:
- `zcl campaign run`
- `zcl campaign canary`
- `zcl campaign resume`

Purpose:
- Canonical mission plan for deterministic campaign resume/reconciliation.

Example:
```json
{
  "schemaVersion": 1,
  "campaignId": "cmp-main",
  "specPath": "/abs/path/campaign.yaml",
  "missions": [
    { "missionIndex": 0, "missionId": "m1" },
    { "missionIndex": 1, "missionId": "m2" }
  ],
  "createdAt": "2026-02-22T12:00:00.123456789Z",
  "updatedAt": "2026-02-22T12:00:00.123456789Z"
}
```

## `campaign.progress.jsonl` (optional; v1)

Path: `.zcl/campaigns/<campaignId>/campaign.progress.jsonl`

Written by:
- first-class campaign mission engine (`run|canary|resume`)

Purpose:
- append-only mission/flow checkpoint ledger for deterministic resume and duplicate-attempt guards.

Event example:
```json
{
  "schemaVersion": 1,
  "campaignId": "cmp-main",
  "runId": "20260222-120000Z-a1b2c3",
  "missionIndex": 0,
  "missionId": "m1",
  "flowId": "flow-a",
  "attemptId": "001-m1-r1",
  "attemptDir": ".zcl/runs/20260222-120000Z-abcd01/attempts/001-m1-r1",
  "status": "valid",
  "reasonCodes": [],
  "idempotencyKey": "cmp-main:flow-a:0",
  "createdAt": "2026-02-22T12:00:10.123456789Z"
}
```

Status values include:
- attempt statuses (`valid|invalid|skipped|infra_failed`)
- gate checkpoints (`gate_pass|gate_fail`)
- cleanup lifecycle checkpoints (`cleanup_before_mission_*`, `cleanup_after_mission_*`, `cleanup_on_failure_*`)

## `campaign.report.json` (optional; v1)

Path: `.zcl/campaigns/<campaignId>/campaign.report.json`

Example:
```json
{
  "schemaVersion": 1,
  "campaignId": "cmp-main",
  "runId": "20260222-120000Z-a1b2c3",
  "status": "valid",
  "reasonCodes": [],
  "outRoot": ".zcl",
  "totalMissions": 3,
  "missionsCompleted": 3,
  "gatesPassed": 3,
  "gatesFailed": 0,
  "failureBuckets": {
    "infraFailed": 0,
    "oracleFailed": 0,
    "missionFailed": 0
  },
  "flows": [],
  "updatedAt": "2026-02-22T12:01:00.123456789Z"
}
```

`zcl campaign report` refuses export when `status` is `invalid|aborted` unless `--allow-invalid` or `--force` is set.

## `campaign.summary.json` (optional; v1)

Path: `.zcl/campaigns/<campaignId>/campaign.summary.json`

Written by:
- `zcl campaign run|canary|resume`
- `zcl campaign report`

Purpose:
- Operator-focused machine summary for comparison workflows.
- Includes claimed vs verified counts, mismatch totals, per-mission A/B flow results, top failure codes, and evidence paths.

Example:
```json
{
  "schemaVersion": 1,
  "campaignId": "cmp-main",
  "runId": "20260222-120000Z-a1b2c3",
  "status": "invalid",
  "reasonCodes": ["ZCL_E_CAMPAIGN_GATE_FAILED"],
  "updatedAt": "2026-02-22T12:01:00.123456789Z",
  "totalMissions": 3,
  "missionsCompleted": 3,
  "gatesPassed": 2,
  "gatesFailed": 1,
  "failureBuckets": {
    "infraFailed": 1,
    "oracleFailed": 1,
    "missionFailed": 0
  },
  "claimedMissionsOk": 3,
  "verifiedMissionsOk": 2,
  "mismatchCount": 1,
  "topFailureCodes": [{ "code": "ZCL_E_CAMPAIGN_TRACE_PROFILE_MCP_REQUIRED", "count": 1 }],
  "missions": [],
  "evidencePaths": {
    "runStatePath": ".zcl/campaigns/cmp-main/campaign.run.state.json",
    "reportPath": ".zcl/campaigns/cmp-main/campaign.report.json",
    "summaryPath": ".zcl/campaigns/cmp-main/campaign.summary.json",
    "resultsMdPath": ".zcl/campaigns/cmp-main/RESULTS.md",
    "attemptDirs": []
  },
  "flows": [
    {
      "flowId": "flow-a",
      "runnerType": "codex_app_server",
      "attemptsTotal": 1,
      "valid": 0,
      "invalid": 1,
      "skipped": 0,
      "infraFailed": 1,
      "oracleFailed": 0,
      "missionFailed": 0
    }
  ]
}
```

## `RESULTS.md` (optional; v1)

Path: `.zcl/campaigns/<campaignId>/RESULTS.md`

Written by:
- `zcl campaign run|canary|resume`
- `zcl campaign report`

Purpose:
- Human-facing operator summary generated from `campaign.summary.json`.
- Includes status, claimed vs verified mismatch, per-mission A/B rollup, top failure codes, and evidence paths.

## `mission.prompts.json` (optional; v1)

Path: `.zcl/campaigns/<campaignId>/mission.prompts.json`

Written by:
- `zcl mission prompts build --spec ... --template ...`

Determinism note:
- Prompt IDs are stable hash IDs.
- Output ordering follows canonical flow+mission selection order.
- `createdAt` is deterministic from spec/template/prompt content (not wall-clock time).

Example:
```json
{
  "schemaVersion": 1,
  "campaignId": "cmp-main",
  "specPath": "/abs/path/campaign.yaml",
  "templatePath": "/abs/path/template.md",
  "outPath": ".zcl/campaigns/cmp-main/mission.prompts.json",
  "createdAt": "2026-02-22T12:05:00.123456789Z",
  "prompts": [
    {
      "id": "flow-a-m1-001",
      "flowId": "flow-a",
      "suiteId": "suite-a",
      "missionId": "m1",
      "missionIndex": 0,
      "prompt": "..."
    }
  ]
}
```

## `runner.ref.json` (optional; v1)

Path: `.zcl/runs/<runId>/attempts/<attemptId>/runner.ref.json`

Example:
```json
{
  "schemaVersion": 1,
  "runner": "codex_app_server",
  "runId": "20260215-180012Z-09c5a6",
  "suiteId": "heftiweb-smoke",
  "missionId": "latest-blog-title",
  "attemptId": "001-latest-blog-title-r1",
  "agentId": "optional-runner-agent-id",
  "runtimeId": "codex_app_server",
  "sessionId": "pid:92314",
  "threadId": "thr_abc123",
  "transport": "stdio",
  "rolloutPath": "/path/to/rollout.jsonl"
}
```

## `runner.metrics.json` (optional; v1)

Path: `.zcl/runs/<runId>/attempts/<attemptId>/runner.metrics.json`

Example:
```json
{
  "schemaVersion": 1,
  "runner": "codex",
  "model": "gpt-5.1",
  "totalTokens": 12345,
  "inputTokens": 111,
  "outputTokens": 222,
  "cachedInputTokens": 0,
  "reasoningOutputTokens": 333
}
```
