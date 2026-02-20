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
          "type": "string",
          "pattern": "^ARTICLE_TITLE=.*"
        },
        "trace": {
          "maxToolCallsTotal": 30,
          "maxFailuresTotal": 5,
          "maxRepeatStreak": 10,
          "requireCommandPrefix": ["surfwright"]
        }
      }
    }
  ]
}
```

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
  "feedbackPolicy": "auto_fail",
  "campaignId": "heftiweb-smoke",
  "campaignStatePath": ".zcl/campaigns/heftiweb-smoke/campaign.state.json",
  "campaignProfile": {
    "mode": "discovery",
    "timeoutMs": 120000,
    "timeoutStart": "first_tool_call",
    "isolationModel": "process_runner",
    "feedbackPolicy": "auto_fail",
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

## `prompt.txt` (snapshot; optional)

Path: `.zcl/runs/<runId>/attempts/<attemptId>/prompt.txt`

If `zcl attempt start --prompt <text>` is used, ZCL snapshots the prompt text here.

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
- `input`/`enrichment` are stored as bounded/canonicalized JSON when possible; inputs may be truncated with a warning when they exceed bounds.
- `result.code` is a typed ZCL code when ZCL can classify; otherwise a normalized tool error code.
- `redactionsApplied` lists the redaction rules applied to this event (informational only; scoring must not depend on it).

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
    "commandNamesSeen": ["surfwright", "curl"]
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

## `runner.ref.json` (optional; v1)

Path: `.zcl/runs/<runId>/attempts/<attemptId>/runner.ref.json`

Example:
```json
{
  "schemaVersion": 1,
  "runner": "codex",
  "runId": "20260215-180012Z-09c5a6",
  "suiteId": "heftiweb-smoke",
  "missionId": "latest-blog-title",
  "attemptId": "001-latest-blog-title-r1",
  "agentId": "optional-runner-agent-id",
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
