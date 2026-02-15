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
  "runId": "20260215-180012Z-09c5a6",
  "suiteId": "heftiweb-smoke",
  "createdAt": "2026-02-15T18:00:12.123456789Z"
}
```

## `suite.json` (snapshot; optional)

Path: `.zcl/runs/<runId>/suite.json`

If `zcl attempt start --suite-file <path>` is used, ZCL snapshots the suite JSON here (canonicalized JSON for diffability).

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
  "op": "run",
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
- `input`/`enrichment` are stored as bounded/canonicalized JSON when possible.
- `result.code` is a typed ZCL code when ZCL can classify; otherwise a normalized tool error code.

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
  "ok": true,
  "metrics": {
    "toolCallsTotal": 3,
    "failuresTotal": 0,
    "retriesTotal": 0,
    "timeoutsTotal": 0,
    "wallTimeMs": 42000,
    "outBytesTotal": 12345,
    "errBytesTotal": 12,
    "outPreviewTruncations": 0,
    "errPreviewTruncations": 0
  }
}
```
