# Zero Context Lab (ZCL) Implementation Plan

This plan is the step-by-step buildout of ZCL as a standalone, funnel-first benchmark harness.
It is self-contained, but cross-references:
- `CONCEPT.md` for the "why" and non-negotiables.
- `ARCHITECTURE.md` for the intended design boundaries and artifact contract.

Rules:
- Execute steps in order. Do not start a step until all previous steps in the phase are checked.
- Do not start the next phase until the current phase is fully checked.
- Keep the surface small and deterministic: bounded outputs, redaction, typed errors, stable JSON.
- At the end of every phase: `./scripts/verify.sh` must pass.

## Phases

### Phase 0: Repo + Toolchain Baseline (Done)

1. [x] Install Go toolchain (Homebrew) and confirm `go version` works on this machine.
2. [x] Initialize the Go module and baseline folder layout (`cmd/`, `internal/`, `scripts/`, `test/fixtures/`).
3. [x] Implement a minimal `zcl` CLI with JSON-first output:
   `zcl contract --json` and `zcl attempt start --suite ... --mission ... --json`.
4. [x] Add SurfWright-style repo validation scripts with minimal `PASS/FAIL` output:
   `scripts/docs-check.sh`, `scripts/contract-snapshot.sh`, `scripts/verify.sh`.
5. [x] Add initial contract snapshot at `test/fixtures/contract/contract.snapshot.json`.
6. [x] Add `.gitignore` so `.zcl/` outputs and `artifacts/` do not get committed.
7. [x] Initialize git, set `origin`, commit, and push `main`.

### Phase 1: Artifact + Schema Contracts (Foundation)

Goal: make evidence artifacts and schema versions explicit so validation/reporting can be strict and stable.

1. [x] Define (in Go structs + in docs) the v1 schemas for:
   `run.json`, `attempt.json`, `feedback.json`, `attempt.report.json`, `tool.calls.jsonl` events, `notes.jsonl` events.
2. [x] Decide and document canonical ID formats (exact strings):
   `runId`, `attemptId`, `suiteId`, `missionId`, optional `agentId`.
3. [x] Update `zcl contract --json` to print:
   supported schema versions, required artifact files, and the minimal required fields (not prose).
4. [x] Update `scripts/contract-snapshot.sh --update` and commit the updated snapshot.
5. [x] Add contract/serialization tests:
   `go test` must fail if contract JSON shape drifts without snapshot update.

### Phase 2: Attempt Lifecycle + Output Root

Goal: orchestrator can allocate attempts deterministically and we can locate artifacts without guesswork.

1. [x] Implement `zcl init` to create a minimal project config and ensure `.zcl/` exists.
2. [x] Extend `zcl attempt start`:
   write `prompt.txt` (optional) when provided, and optionally snapshot input suite (`suite.json`) when running a suite.
3. [x] Add `zcl attempt start --json` output contract documentation to `ARCHITECTURE.md`:
   exact keys and semantics (env map, outDirAbs, ids).
4. [x] Add attempt lifecycle tests:
   create run, create two attempts, ensure IDs/dirs are stable and files are atomic.

### Phase 3: CLI Funnel MVP (`zcl run`)

Goal: funnel-first evidence. Each tool invocation becomes one deterministic trace event.

1. [x] Implement `zcl run -- <cmd> [args...]`:
   spawn process, capture bounded stdout/stderr previews, measure duration, preserve exit code.
2. [x] Write one JSON object per invocation to `tool.calls.jsonl` with:
   canonical IDs, `tool`, `op`, bounded `input`, `result` (ok/code/exitCode/duration), `io` (bytes + previews), redactions list.
3. [x] Implement safe JSONL append:
   cross-platform lock (`mkdir` lock dir) or spool-per-call + merge; document choice in `ARCHITECTURE.md`.
4. [x] Add integration tests:
   passthrough behavior (stdout/stderr), trace emission, and bounds enforcement.

### Phase 4: Finish + Secondary Evidence (`zcl feedback`, `zcl note`)

Goal: runner-agnostic scoring uses `feedback.json`, while self-reports live in `notes.jsonl`.

1. [x] Implement `zcl feedback --ok|--fail --result <string>` (+ `--result-json` option):
   atomic write to `feedback.json`, bounded payload, redaction.
2. [x] Implement `zcl note`:
   append a bounded/redacted note event to `notes.jsonl` (agent self-report or operator note).
3. [x] Document the difference in `AGENTS.md` and `CONCEPT.md`:
   primary evidence vs secondary evidence.
4. [x] Add tests for:
   feedback schema, note schema, bounds/redaction behavior.

### Phase 5: Reporting (`zcl report`)

Goal: compute deterministic `attempt.report.json` from trace + feedback (runner-agnostic).

1. [x] Implement `zcl report [--strict] <attemptDir|runDir>`:
   parse artifacts, compute metrics, write `attempt.report.json` atomically.
2. [x] Metrics v1 must include at least:
   toolCallsTotal, failuresTotal/byCode, retriesTotal, timeoutsTotal, wallTimeMs, bytes totals, truncation counts.
3. [x] Add golden fixture tests:
   known `tool.calls.jsonl` + `feedback.json` => expected `attempt.report.json`.
4. [x] Ensure `--strict` fails when required artifacts are missing (typed ZCL codes).

### Phase 6: Validation (`zcl validate`)

Goal: enforce artifact integrity mechanically (discovery mode best-effort; CI mode strict).

1. [x] Implement `zcl validate [--strict] <attemptDir|runDir>` with typed ZCL error codes.
2. [x] Validation v1 rules must cover:
   required file presence, JSON/JSONL parse, schema versions, ID consistency with directory, bounds, containment (no path traversal/symlink escape).
3. [x] Add tests for failure cases:
   missing artifact, invalid json, schema unsupported, bounds exceeded.

### Phase 7: Operator UX Commands (`doctor`, `gc`) + Config

Goal: keep the harness usable at scale (many runs) and safe by default.

1. [ ] Implement minimal config load/merge (CLI flags > env > project config > global config > defaults).
2. [ ] Implement `zcl doctor` (human output + `--json`):
   write access, config parse, environment sanity, optional runner availability.
3. [ ] Implement `zcl gc`:
   age/size-based cleanup under `.zcl/runs/`, support pinning runs in `run.json`.
4. [ ] Add tests or deterministic fixtures for GC selection logic.

### Phase 8: Codex Enrichment + Skill Pack (MVP2)

Goal: keep scoring runner-agnostic, but allow optional enrichment from Codex sessions and ship the orchestrator workflow.

1. [ ] Implement `zcl enrich --runner codex`:
   emit `runner.ref.json` + `runner.metrics.json` (tokens/turns/model when available).
2. [ ] Add robust parsing for Codex rollout JSONL:
   tolerate missing fields/schema drift; treat metrics as nullable.
3. [ ] Create and ship a Codex skill in-repo (e.g. `skills/zcl/`):
   includes orchestrator responsibilities and the Turn 1/2/3 protocol.
4. [ ] Add skill validation script + wire it into `./scripts/verify.sh`.

### Phase 9: MCP Funnel (MVP3)

Goal: proxy MCP JSON-RPC boundaries and emit trace events comparable to CLI funnel events.

1. [ ] Implement `zcl mcp proxy -- <server-cmd>` (stdio first).
2. [ ] Trace v1: record `initialize`, `tools/list`, `tools/call` with timing + payload sizes + redacted previews.
3. [ ] Add deterministic integration tests with a tiny fake MCP server.

### Phase 10: Distribution + Release Guardrails (MVP4)

Goal: ship a single binary with simple, safe install/update patterns and release validation.

1. [ ] Add a release build script and produce per-platform artifacts (mac/linux/windows) + checksums.
2. [ ] Add `scripts/release-check.*` mirroring SurfWright's approach:
   validate checksums exist, versions match, and release notes/changelog policy is satisfied.
3. [ ] Add optional installer scripts (`install.sh`, `install.ps1`) and test them in CI (smoke).
4. [ ] Decide whether `zcl update` exists; if yes, implement `zcl update status --json` first.

## Progress Execution Log

Update this log while executing the plan.

- 2026-02-15: Bootstrapped repo, added Go module + minimal CLI (`contract`, `attempt start`), and added script-driven `./scripts/verify.sh` with contract snapshot. (Phase 0 done)
- 2026-02-15: Phase 1 Step 1: Added explicit v1 artifact schemas in `internal/schema` and documented exact JSON/JSONL shapes in `SCHEMAS.md` (linked from `AGENTS.md`/`ARCHITECTURE.md`). Next: lock canonical ID formats and reflect them in docs + contract output.
- 2026-02-15: Phase 1 Step 2: Documented canonical ID formats in `SCHEMAS.md` and enforced canonicalization/validation in attempt creation (`suiteId`/`missionId` sanitized; `runId` validated). Next: expand `zcl contract --json` with required artifacts + required fields.
- 2026-02-15: Phase 1 Step 3-4: Expanded `zcl contract --json` with explicit artifact/event requirements + supported schema versions, then updated `test/fixtures/contract/contract.snapshot.json`. Next: add tests that fail on contract drift without snapshot updates.
- 2026-02-15: Phase 1 Step 5: Added a strict contract snapshot test so `go test` fails if the contract drifts without updating the snapshot. Next: Phase 2 begins (attempt lifecycle + output root).
- 2026-02-15: Phase 2 Step 1: Implemented `zcl init` (writes `zcl.config.json`, ensures `.zcl/runs` exists) with unit tests; updated contract + snapshot accordingly. Next: extend `attempt start` to write `prompt.txt` and optionally snapshot suites.
- 2026-02-15: Phase 2 Step 2: Extended `zcl attempt start` with optional prompt (`prompt.txt`) and suite snapshot (`suite.json`), with tests and contract snapshot updates. Next: document the `attempt start --json` output contract in `ARCHITECTURE.md`.
- 2026-02-15: Phase 2 Step 3: Documented the exact `zcl attempt start --json` output keys and env semantics in `ARCHITECTURE.md`. Next: add lifecycle tests covering multi-attempt runs and atomic file guarantees.
- 2026-02-15: Phase 2 Step 4: Added lifecycle tests for multi-attempt runs (stable attempt IDs/dirs) and verified atomic writers leave no temp files behind. Next: Phase 3 begins (`zcl run` funnel + trace emission).
- 2026-02-15: Phase 3 Step 1: Implemented `zcl run` CLI funnel wrapper (passthrough + bounded capture + duration + exit code) as a foundation for trace emission. Next: emit `tool.calls.jsonl` events per invocation.
- 2026-02-15: Phase 3 Step 2: Added trace emission for `zcl run` (one v1 event per invocation appended to `tool.calls.jsonl` using `ZCL_*` attempt env). Next: make JSONL appends concurrency-safe (lock/spool) and document the approach.
- 2026-02-15: Phase 3 Step 3: Made JSONL appends concurrency-safe via `mkdir` lock dirs (documented in `ARCHITECTURE.md`) and added `fsync` on append. Next: integration tests covering passthrough, trace emission, and bounds.
- 2026-02-15: Phase 3 Step 4: Added integration tests for `zcl run` covering passthrough stdout/stderr, trace emission, and preview bounds/truncation signaling. Next: Phase 4 begins (`zcl feedback` + `zcl note`).
- 2026-02-15: Phase 4 Step 1-4: Implemented `zcl feedback` (atomic `feedback.json`) and `zcl note` (append `notes.jsonl`) with bounded payload + basic redaction, updated contract snapshot, and added tests for schema + redaction behavior. Next: Phase 5 begins (`zcl report`).
- 2026-02-15: Phase 5: Implemented `zcl report` with `--strict`, expanded metrics to include `failuresByCode`, added golden fixtures and strict-missing-artifact tests, and updated schemas/docs accordingly. Next: Phase 6 (`zcl validate`).
- 2026-02-15: Phase 6: Implemented `zcl validate` (strict vs best-effort) with typed codes for missing artifacts, invalid JSON/JSONL, unsupported schema versions, id mismatches, bounds, and containment, plus tests. Next: Phase 7 (config merge, doctor, gc).
- YYYY-MM-DD: (who) (what step) (what changed) (what remains)
