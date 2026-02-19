# ZCL Map (Operator + Builder Index)

This file is the navigation map. It should stay short and high-signal.
The system of record lives in the linked docs + contracts + validation scripts.

## Start Here (In Order)

1. `PLAN.md`
   - Execution checklist. Do steps in order. Update the log as you go.
2. `CONCEPT.md`
   - Why ZCL exists and the non-negotiables (funnel-first evidence, bounded outputs, runner-agnostic scoring).
3. `ARCHITECTURE.md`
   - The intended shape: command surface, artifacts, determinism, guardrails.
4. `SCHEMAS.md`
   - Exact v1 artifact schemas and canonical ID formats.

Repo validation (must be green after meaningful changes):
- `./scripts/verify.sh`

## Non-Negotiables (Keep This Boring)

- Primary evidence is artifacts, not transcripts.
  - `tool.calls.jsonl` (funnel trace)
  - `feedback.json` (canonical mission outcome)
- Runner enrichments are optional and must not affect scoring.
- Bounded capture by default; redact obvious secrets.
- Deterministic shapes: stable JSON, versioned schemas, atomic writes, safe JSONL appends.
- If you change artifact layout or schema: update `SCHEMAS.md`, `zcl contract --json`, contract snapshot, and tests together.

## Operator Workflow (Golden Path)

1. Initialize: `zcl init`
2. Optional preflight (recommended for agent harnesses):
   - `zcl update status --json` (manual update policy; no auto-update)
   - Set `ZCL_MIN_VERSION=<semver>` in harness env to fail fast on old installs.
3. Start attempt (JSON output is required for automation):
   - Native-spawn path (preferred when host supports it): `zcl attempt start --suite <suiteId> --mission <missionId> --prompt <text> --isolation-model native_spawn --json`
   - Batch-plan a full suite for native host orchestration: `zcl suite plan --file <suite.(yaml|yml|json)> --json`
   - Process-runner fallback: `zcl suite run --file <suite.(yaml|yml|json)> --session-isolation process --json -- <runner-cmd> [args...]`
4. Run actions through the funnel:
   - CLI: `zcl run -- <cmd> [args...]` (writes `tool.calls.jsonl`)
   - MCP: `zcl mcp proxy -- <server-cmd> [args...]` (writes `tool.calls.jsonl`)
   - HTTP: `zcl http proxy --upstream <url> [--listen 127.0.0.1:0] [--max-requests N] [--json]` (writes `tool.calls.jsonl`)
5. Finish with authoritative outcome:
   - `zcl feedback --ok|--fail --result <string>` or `--result-json <json>`
6. Optional secondary evidence:
   - `zcl note --kind agent|operator --message <text>`
   - `zcl enrich --runner codex|claude --rollout <rollout.jsonl> [<attemptDir>]`
   - Example: `zcl enrich --runner claude --rollout /Users/<you>/.claude/projects/<project>/<session>.jsonl .zcl/runs/<runId>/attempts/<attemptId>`
7. Compute and validate:
   - `zcl report --strict <attemptDir|runDir>`
   - `zcl validate --strict <attemptDir|runDir>`
   - If using suites with `expects`: `zcl expect --strict --json <attemptDir|runDir>`
   - Optional: reproduce from trace: `zcl replay --json <attemptDir>`

## Artifact Layout (Default)

Root: `.zcl/`
```
.zcl/
  runs/<runId>/
    run.json
    suite.json                  (optional snapshot)
    suite.run.summary.json      (optional; `zcl suite run --json` summary artifact)
    run.report.json             (optional; `zcl report --json <runDir>` aggregate)
    attempts/<attemptId>/
      attempt.json
      prompt.txt                (optional snapshot)
      tool.calls.jsonl          (primary evidence)
      feedback.json             (primary evidence)
      notes.jsonl               (optional; secondary evidence)
      attempt.report.json       (computed)
      runner.ref.json           (optional enrichment)
      runner.metrics.json       (optional enrichment)
```

## Where To Change Things (By Intent)

If you’re changing CLI behavior or adding a command:
- `internal/cli/cli.go`
- `internal/contract/contract.go` (command + artifact contract)
- `test/fixtures/contract/contract.snapshot.json` (via `scripts/contract-snapshot.sh --update`)

If you’re changing artifact shapes or schema versions:
- `internal/schema/`
- `SCHEMAS.md`
- `internal/contract/contract.go` + contract snapshot test

If you’re changing trace emission:
- CLI funnel exec: `internal/funnel/cli_funnel/exec.go`
- MCP proxy funnel: `internal/funnel/mcp_proxy/proxy.go`
- Trace writer/util: `internal/trace/trace.go`
- JSONL append safety: `internal/store/jsonl.go` + `internal/store/lock.go`

If you’re changing redaction or bounds:
- `internal/redact/redact.go`
- `internal/feedback/feedback.go`, `internal/note/note.go`
- `internal/validate/validate.go` (bounds are enforced here too)

If you’re changing reporting/metrics:
- `internal/report/report.go`
- Golden fixtures: `test/fixtures/report/`

If you’re changing retention/scale knobs:
- `internal/gc/gc.go`
- `internal/doctor/doctor.go`
- `internal/config/merge.go`

If you’re changing Codex enrichment:
- `internal/enrich/`
- Runner schemas: `internal/schema/runner_v1.go`

## Mechanical Guardrails (What Enforces Coherence)

Single entrypoint:
- `./scripts/verify.sh`

What it runs:
- `scripts/skills-check.sh` (skill pack sanity)
- `scripts/docs-check.sh` (doc cross-links exist)
- gofmt check
- `go test ./...`, `go vet ./...`
- `scripts/contract-snapshot.sh --check` (contract drift is a failing test)
- `scripts/docs-contract-check.sh` (docs mention commands + SCHEMAS matches contract artifacts)

Entropy guard (CI/scheduled):
- `scripts/entropy-check.sh` (runs `zcl doctor --json` + `zcl gc --dry-run --json`)
- Threshold knobs:
  - `ZCL_ENTROPY_MAX_DELETED`
  - `ZCL_ENTROPY_MAX_TOTAL_BEFORE_BYTES`
  - `ZCL_ENTROPY_MAX_GC_ERRORS`
