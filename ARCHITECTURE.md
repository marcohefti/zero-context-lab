# Zero Context Lab (ZCL) Architecture

This doc describes a feasible, non-overengineered architecture for ZCL as a standalone product.

ZCL is a harness/benchmark that funnels agent actions through instrumented boundaries, producing deterministic evidence artifacts. Runner/session logs (Codex/Claude/OpenCode/etc) are optional enrichments.

For the "why" and the non-negotiables, read `CONCEPT.md` first.
For a quick map of what to read and what matters, see `AGENTS.md`.
For exact artifact schemas (v1), see `SCHEMAS.md`.

## Goals
- **Funnel-first evidence.** All benchmark metrics should be derivable from ZCL artifacts without needing runner internals.
- **Deterministic artifact contract.** Same inputs yield the same artifact *shapes* (bounded previews, stable JSON).
- **Tool-agnostic funnels.** Support CLI, MCP (JSON-RPC), HTTP, and SDK funnels with a common trace schema.
- **Runner-agnostic enrichment.** Codex is one integration; others can be added without changing scoring.
- **Operator UX.** Zero-friction install/update, obvious commands, and artifacts that are easy to diff and reason about.

## Non-goals (for the core)
- ZCL is not an LLM runner, model router, or agent framework.
- ZCL does not try to infer "thought quality" beyond what traces can support.
- ZCL does not ship a complex plugin runtime in v1 (no dynamic loading required to be useful).

## High-Level System Model
- **Agent/runner** produces actions and optionally logs a transcript.
- **ZCL funnel** is the enforced gateway for actions.
- **ZCL artifacts** are the authoritative evidence.
- **Runner adapter** (optional) reads runner logs and emits normalized metrics into ZCL artifacts.

Data flow (conceptual):
1. Orchestrator (or agent) starts an attempt (explicitly or implicitly) so ZCL can allocate an output dir and canonical IDs, then writes metadata (`run.json`, `attempt.json`).
2. Agent performs actions through the funnel (e.g., `zcl run -- surfwright --json ...`).
3. Funnel appends one line per action to `tool.calls.jsonl` and stores bounded stdout/stderr previews.
4. Agent finishes by running `zcl feedback --ok|--fail --result ...` which writes `feedback.json`.
5. `zcl report` computes `attempt.report.json` from traces + feedback (runner-agnostic).
6. Optional: `zcl enrich --runner codex` writes `runner.ref.json` + `runner.metrics.json`.

Boundaries (ownership + responsibilities):
- Orchestrator/runner integration: resolves ZCL entrypoint, starts the attempt, spawns a fresh agent with `ZCL_*` env/preamble, and optionally records secondary notes.
- Spawned agent: performs all evaluated actions through ZCL funnels and records the canonical outcome via `zcl feedback`.
- ZCL core: owns artifact layout, trace writing, validation, and report generation.
- Runner adapters: optional enrichment only (`runner.*.json`); must not affect scoring.
- Write boundary: ZCL writes only under `.zcl/` (project) and optionally `~/.zcl/` (global); it must not mutate repo source/git state unless explicitly permitted.

## Command Surface (MVP)
ZCL should stay small: a few composable commands with deterministic JSON output and deterministic artifacts.

Core commands:
- `zcl init`: writes a minimal project config and creates `.zcl/` output root.
- `zcl attempt start --suite <suiteId> --mission <missionId> [--agent-id <runnerAgentId>] [--mode discovery|ci] [--json]`: allocates an attempt dir + canonical IDs and prints env/pointers for the spawned agent.
- `zcl run -- <cmd> [args...]`: CLI funnel wrapper; appends one trace event per invocation.
- `zcl feedback --ok|--fail --result <string|json>`: writes `feedback.json` (authoritative outcome).
- `zcl report [--strict] <attemptDir|runDir>`: computes `attempt.report.json` from `tool.calls.jsonl` + `feedback.json`.
- `zcl validate [--strict] <attemptDir|runDir>`: artifact integrity validation with typed ZCL error codes.
- `zcl contract --json`: prints the supported artifact layout version(s) + trace schema version(s) and required fields (the "surface contract").
- `zcl doctor`: environment checks (write access, config parse, optional runner availability).
- `zcl gc`: retention (age/size cleanup, pinning support).

Optional (later) commands:
- `zcl enrich --runner codex`: emits `runner.ref.json` + `runner.metrics.json` without affecting scoring.
- `zcl note ...`: records bounded/redacted notes or agent self-reports alongside an attempt (secondary evidence; never required for scoring).
- `zcl mcp proxy ...`: MCP funnel at JSON-RPC boundary (stdio first).
- `zcl replay <attemptDir>`: best-effort replay of a trace to reproduce failures.

Output rules:
- Any command intended for automation/agents must support a stable `--json` output mode.
- Avoid log spam; default stderr should be concise and typed (code + message).

## `zcl attempt start --json` Output Contract (v1)

This output is designed for orchestrators: it returns canonical IDs, directories, and the exact `ZCL_*` env map to pass to the spawned "zero context" agent.

Top-level keys (exact):
- `ok` (bool): always `true` on success.
- `runId` (string): canonical `runId` (see `SCHEMAS.md`).
- `suiteId` (string): canonicalized suite id (lowercase kebab-case).
- `missionId` (string): canonicalized mission id (lowercase kebab-case).
- `attemptId` (string): canonical attempt directory id.
- `agentId` (string, optional): runner-provided correlation id (not used in paths).
- `mode` (string): `discovery` or `ci`.
- `outDir` (string): attempt output directory relative to the current working directory.
- `outDirAbs` (string): absolute path to the attempt output directory (this is what we set in `ZCL_OUT_DIR`).
- `env` (object map of string->string): environment variables to pass to the spawned agent process.
- `createdAt` (string): RFC3339 UTC timestamp when the attempt was created.

`env` keys (exact; `ZCL_AGENT_ID` is conditional):
- `ZCL_RUN_ID`
- `ZCL_SUITE_ID`
- `ZCL_MISSION_ID`
- `ZCL_ATTEMPT_ID`
- `ZCL_OUT_DIR`
- `ZCL_AGENT_ID` (only when `--agent-id` is provided)

Example:
```json
{
  "ok": true,
  "runId": "20260215-180012Z-09c5a6",
  "suiteId": "heftiweb-smoke",
  "missionId": "latest-blog-title",
  "attemptId": "001-latest-blog-title-r1",
  "mode": "discovery",
  "outDir": ".zcl/runs/20260215-180012Z-09c5a6/attempts/001-latest-blog-title-r1",
  "outDirAbs": "/abs/path/to/repo/.zcl/runs/20260215-180012Z-09c5a6/attempts/001-latest-blog-title-r1",
  "env": {
    "ZCL_ATTEMPT_ID": "001-latest-blog-title-r1",
    "ZCL_MISSION_ID": "latest-blog-title",
    "ZCL_OUT_DIR": "/abs/path/to/repo/.zcl/runs/20260215-180012Z-09c5a6/attempts/001-latest-blog-title-r1",
    "ZCL_RUN_ID": "20260215-180012Z-09c5a6",
    "ZCL_SUITE_ID": "heftiweb-smoke"
  },
  "createdAt": "2026-02-15T18:00:12.123456789Z"
}
```

## Artifact Contract (Minimum)
ZCL should standardize a stable project output root at `.zcl/` (configurable) and write one directory per run under `.zcl/runs/<runId>/`:
- `.zcl/runs/<runId>/run.json`
- `.zcl/runs/<runId>/suite.json` (optional snapshot)
- `.zcl/runs/<runId>/attempts/<attemptId>/attempt.json`
- `.zcl/runs/<runId>/attempts/<attemptId>/prompt.txt` (optional, but recommended for reproducibility)
- `.zcl/runs/<runId>/attempts/<attemptId>/tool.calls.jsonl` (primary evidence)
- `.zcl/runs/<runId>/attempts/<attemptId>/feedback.json` (authoritative outcome)
- `.zcl/runs/<runId>/attempts/<attemptId>/notes.jsonl` (optional; secondary evidence such as agent self-reports)
- `.zcl/runs/<runId>/attempts/<attemptId>/attempt.report.json` (computed metrics + pointers)
- `.zcl/runs/<runId>/attempts/<attemptId>/runner.ref.json` (optional)
- `.zcl/runs/<runId>/attempts/<attemptId>/runner.metrics.json` (optional)

Guideline: everything a report needs should be local and relative-path referenced.

Example tree (single attempt):
```text
.zcl/
  runs/
    20260215-180012Z-09c5a6/
      run.json
      suite.json
      attempts/
        001-heftiweb-latest-title-r1/
          attempt.json
          prompt.txt
          tool.calls.jsonl
          feedback.json
          notes.jsonl
          attempt.report.json
          runner.ref.json
          runner.metrics.json
```

Notes (secondary evidence):
- `notes.jsonl` should be written via `zcl note ...` (or by the orchestrator calling a ZCL API) and treated as optional enrichment.
- Store both free-form self-reports and structured follow-ups here; keep `feedback.json` for the canonical mission result.

## Trace Schema (tool.calls.jsonl)
Each line is one JSON object representing one tool action.

Minimum recommended fields (v1):
- `v` (schema version integer)
- `ts` (RFC3339 UTC timestamp)
- `runId`, `missionId`, `attemptId` (and optionally `suiteId`, `agentId`)
- `tool` (string)
- `op` (string)
- `input` (canonicalized call representation; bounded)
- `result`:
  - `ok` (bool)
  - `code` (typed failure code if known; otherwise normalized)
  - `exitCode` (CLI only)
  - `durationMs`
- `io`:
  - `outBytes`, `errBytes` (or `respBytes` for HTTP)
  - `outPreview`, `errPreview` (bounded)
- `redactionsApplied` (list)
- `enrichment` (optional tool-specific extras)

Important: do not depend on free-form text; prefer typed, bounded fields.

Notes on identity:
- The attempt directory (and `attemptId`) is the primary identity boundary. `agentId` is optional enrichment for correlation to runner logs.
- ZCL should not require an "agent registration" handshake. The orchestrator/runner integration can attach a runner agent id to `attempt.json` / `runner.ref.json` when available.

## Determinism & Integrity (Practical Choices)
ZCL is a benchmark. If artifacts are nondeterministic, comparisons rot quickly.

Recommended choices:
- **Prefer structs over maps** when writing JSON so field order and presence are stable.
- **Canonicalize JSON-like inputs** before storing them in traces (stable key ordering). If canonicalization fails, store a bounded string representation and emit an integrity warning.
- **Timestamps:** RFC3339 UTC, consistent precision (pick one and stick to it).
- **IDs:** fixed formatting (no UUID variants); include IDs in every artifact so runs are relocatable.

Write safety:
- **Atomic writes** for JSON files (`*.json`): write temp + fsync + rename.
- **Safe appends** for JSONL (`*.jsonl`): implement a simple lock file per attempt (cross-platform) or spool-per-call + merge. Pick one and validate it; do not assume "agents never run two commands concurrently".

## Interfaces & Extension Points (Without a Plugin Framework)
We want clear boundaries, not a complicated runtime plugin system.

Keep compile-time interfaces small:
- Funnel implementations (CLI/MCP/HTTP) all produce the same `TraceEvent` shape and return a `ToolResult`.
- Runner adapters (Codex/Claude/OpenCode/...) all produce the same `runner.ref.json` + `runner.metrics.json` shape.

Conceptual Go interfaces (sketch):
```go
type Funnel interface {
  Kind() string
  Execute(ctx context.Context, call ToolCall) (ToolResult, error)
}

type RunnerAdapter interface {
  Kind() string
  Enrich(ctx context.Context, attemptDir string) (RunnerRef, *RunnerMetrics, error)
}
```

Important boundary rule:
- Funnels/adapters must record what they actually observe at the boundary. If the boundary shape is unknown, fail/flag; don’t invent semantics.

## Core Components (Go)
ZCL core should be a Go single-binary CLI with a small number of internal packages.

Suggested package split (compile-time, not a plugin runtime):
- `cmd/zcl`: CLI entrypoints and command wiring.
- `internal/config`: loads project + global config (`~/.zcl`, repo-local `zcl.config.*`), merges defaults.
- `internal/ids`: run/attempt/mission id generation, stable formatting.
- `internal/store`: artifact directory layout, atomic writes, file locking, retention metadata.
- `internal/trace`: JSONL append writer + bounding + redaction hooks.
- `internal/funnel`: interfaces + implementations (`cli`, `mcp`, later `http`).
- `internal/report`: metrics computation and report emission (`attempt.report.json`).
- `internal/runner`: optional adapters (`codex` first), normalized runner metrics schema.
- `internal/redact`: redaction rules (allowlists, regexes, hashing).

Keep each package small and agent-legible (files not huge; no "framework").

## Funnels (Adapters)
ZCL should implement funnels at protocol boundaries.

### CLI funnel (MVP)
Command shape:
- `zcl run -- <cmd> [args...]`

Implementation:
- Spawn the underlying command.
- Capture stdout/stderr with size bounds + preview truncation.
- Record one trace event with timing + exit code.
- Passthrough stdout/stderr by default so the underlying tool contract is preserved.

### MCP funnel (v2)
Two practical shapes:
- stdio proxy: `zcl mcp proxy -- <server-cmd>` (ZCL sits between client and server)
- TCP/WebSocket proxy: `zcl mcp proxy --listen ... --upstream ...`

Record:
- `initialize`, `tools/list`, `tools/call`, errors
- payload sizes + redacted previews + per-call latency

### HTTP funnel (v3)
Local proxy that logs method/url/status/latency/payload sizes + redactions.

## Scoring/Reporting (Runner-Agnostic)
`zcl report` should compute metrics from:
- `tool.calls.jsonl` (primary evidence)
- `feedback.json` (authoritative outcome)

Minimum metrics:
- `toolCallsTotal`, `toolCallsByOp`
- `failuresTotal`, `failuresByCode`, `timeoutsTotal`, `retriesTotal`
- `wallTimeMs`, `latencyMsByOp`, `slowestCalls`
- `outBytesTotal`, `errBytesTotal`, `previewTruncations`

Classification (optional, but valuable):
- `missing_primitive`
- `naming_ux`
- `output_shape`
- `already_possible_better_way`

Rule: classification should be trace-backed; if it’s a hypothesis, label it as such.

## Runner Enrichment (Optional Adapters)
Runner adapters are optional and must not affect pass/fail scoring.

### Codex adapter (reference integration)
Inputs:
- `rolloutPath` if available (Codex MCP returns it when starting a conversation).
- Otherwise: search in `~/.codex/sessions` using known ids (thread/agent id) or prompt fragments.

Outputs:
- `runner.ref.json` pointing to the rollout file and ids.
- `runner.metrics.json` (normalized):
  - model/provider
  - turn count (if derivable)
  - token usage (Codex provides evidence-grade `token_count` events)

Evidence references (local codebase docs):
- `/Users/marcohefti/Sites/codex/sdk/typescript/README.md`
- `/Users/marcohefti/Sites/codex/codex-rs/docs/codex_mcp_interface.md`

## Config + State Layout
Keep global vs project state separated:
- Global: `~/.zcl/` (defaults, caches, runner adapter configs)
- Project: `.zcl/runs/` (primary evidence artifacts; default output root)

Config precedence (suggested):
1. CLI flags
2. env vars (`ZCL_*`)
3. project config (`zcl.config.*`)
4. global config (`~/.zcl/config.*`)
5. built-in defaults

## Validation & Guardrails (Second Review Round)
This section is the "keep it coherent" layer: mechanical rules + checks so ZCL stays agent-legible as it grows.

The intent is to enforce invariants, not micromanage implementations. When something fails, the question is: "which capability or guardrail is missing, and how do we make it legible and enforceable?"

### Golden Principles (Mechanical Rules)
These are non-negotiable invariants that should be enforced by code + tests:
- **Primary evidence is the funnel trace.** Scoring must work with ZCL artifacts alone; runner metrics are optional enrichment.
- **Bounded capture everywhere.** All stored previews must be capped; large payloads require explicit capture mode and must still be bounded/redacted.
- **Versioned contracts.** Artifact layout + trace schema versions are explicit; changes require deliberate contract updates.
- **Authoritative outcome lives in ZCL.** Pass/fail and the canonical result must come from `feedback.json` (not chat text).
- **No YOLO boundary guesses in adapters.** Funnels/adapters must record typed/structured inputs at the boundary; if the shape is unknown, record what was observed and fail/flag rather than inventing.

### Product Guardrails (Runtime)
Commands that should exist in the ZCL CLI to keep runs safe and comparable:
- `zcl contract --json`: prints artifact schema version(s), trace schema version(s), and the minimum required fields (a stable "surface contract" for tool authors and CI).
- `zcl validate <run|attempt>`: validates artifact integrity (file presence, JSON parse, schema versions, size bounds, required fields, redaction invariants). Returns typed error codes (not stack traces).
- `zcl doctor`: validates installation + environment (write access to `.zcl/runs`, config parse, optional runner integration connectivity, etc).
- `zcl gc`: retention enforcement (age/size-based cleanup, plus pinning to prevent deletion).

Concrete validation rules (v1):
- Artifact layout: run directory exists and required files are present for each attempt (strict mode: missing `tool.calls.jsonl` or `feedback.json` is a hard fail).
- JSON/JSONL parse: all JSON artifacts are parseable; `tool.calls.jsonl` and `notes.jsonl` are valid JSONL (one JSON object per line).
- ID consistency: `runId`/`attemptId` in artifacts and trace events match the directory they live in.
- Schema versions: trace schema `v` (and artifact layout version, once defined) are supported; unknown versions fail with `ZCL_E_SCHEMA_UNSUPPORTED`.
- Bounds: previews and stored note payloads respect configured caps; truncation is explicit and counted.
- Containment: all artifact paths resolve within the run directory (no path traversal / symlink escape).

Implementation guardrails:
- **Atomic writes** for JSON artifacts: write temp + fsync + rename; never leave partial JSON files behind.
- **Append safety** for JSONL traces: use file locks or per-call spool files + merge (pick one; don’t assume single-process).
- **Deterministic serialization**: stable key ordering (avoid randomized maps), consistent timestamp format, consistent id formatting.
- **Strict vs best-effort integrity**: support `--strict` validation for CI (missing funnel events invalidates the attempt) and best-effort for discovery runs (still records integrity warnings in `attempt.report.json`).

### Repository Guardrails (Dev/CI)
Validation must be cheap to run locally and deterministic in CI.

Recommended baseline (Go):
- Formatting: `gofmt` (CI must fail on diffs).
- Lint: `golangci-lint` (include `depguard`-style rules to enforce package layering and prevent cross-layer imports).
- Static checks: `go vet`, optional `govulncheck` for security scanning.
- Tests:
  - Unit tests for: bounding/truncation, redaction rules, trace schema writing, report computation, config precedence.
  - Golden fixture tests: feed known `tool.calls.jsonl` fixtures + `feedback.json` and snapshot expected `attempt.report.json`.
  - Integration tests: `zcl run -- <cmd>` passthrough behavior (stdout/stderr equivalence) plus trace emission.

Docs/knowledge-base checks (lightweight, not a bureaucracy):
- Require `CONCEPT.md` + `ARCHITECTURE.md` + `AGENTS.md` to exist and be cross-linked.
- Add a simple doc check that ensures required commands are documented (`contract`, `validate`, `doctor`, `gc`, `update` when implemented).

### Distribution Guardrails (Release/Install)
Steal the best parts of OpenClaw/Codex shipping UX, but keep it simple:
- Signed checksums for GitHub Release artifacts; installers verify downloads.
- If we ship installer scripts (`install.sh`, `install-cli.sh`, `install.ps1`), exercise them in CI (at least smoke mode). If we don’t ship installers, keep the release UX to “download binary + checksum”.
- If we ship a self-update flow (`zcl update`), require `status` and `--json` output so automation can reason without scraping.

### Runner Integrations (Optional, but Validated)
Runner adapters should be separately validated because they are inherently unstable across runner versions:
- Codex adapter:
  - Prefer `rolloutPath` when available (Codex MCP returns it on `newConversation`) to avoid filesystem scans.
  - Validate that token usage parsing is robust against missing fields and schema drift.
- For Claude/OpenCode/etc:
  - Treat "runner metrics" as nullable; absence must not break report generation.
  - Always emit `runner.ref.json` as a stable pointer when possible.

## Retention / Garbage Collection
ZCL must ship with explicit retention rules (copying the “entropy is real” harness principle):
- `zcl gc --older-than 14d` (age-based cleanup)
- `zcl gc --max-bytes 10gb` (size-based cleanup)
- allow pinning runs to exempt them from GC (`run.json` metadata)

## MVP Plan (Feasible Sequencing)
1. **MVP1 (core usefulness):** CLI funnel + `feedback` + `report` + stable artifacts.
2. **MVP2 (Codex enrichment):** `enrich --runner codex` producing `runner.*.json`.
3. **MVP3 (protocol funnels):** MCP stdio proxy funnel.
4. **MVP4 (polish):** installer scripts + `update` channel flow + `gc` retention ergonomics.

## Architectural Guardrails (Avoid Overengineering)
- No dynamic plugin system in v1; compile-time adapters are enough.
- Version the artifact contracts early; keep them backwards readable.
- Prefer small, composable commands over a mega-command.
- Keep outputs bounded and JSON-first; avoid log spam.
- Treat "timeouts" as design bugs; don't solve flakiness by increasing timeouts by default.
