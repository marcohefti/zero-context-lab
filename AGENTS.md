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

Canonical operator templates:
- `examples/campaign.canonical.yaml`
- `examples/semantic.rulepack.yaml`
- `internal/campaign/campaign.spec.schema.json` (strict spec shape; unknown fields fail unless `x-*` extension)

Campaign capability status (operator truth source):
- `implemented+enforced`: campaign lint/run/canary/resume/status/report/publish-check, semantic gate, publish guards, mission plan/progress checkpointing, cleanup hooks (`beforeMission/afterMission/onFailure`), campaign lock, traceability profiles, campaign summary outputs (`campaign.summary.json`, `RESULTS.md`).
- `implemented+enforced`: minimal campaign mode (`missionSource.path` + flows without `suiteFile`) for mission-pack ingestion.
- `implemented+partial`: runner adapter types are normalized through suite-run orchestration (native per-runner lifecycle internals still evolving), but hidden session reuse is blocked (`freshAgentPerAttempt` defaults/enforced true).

## Non-Negotiables (Keep This Boring)

- Primary evidence is artifacts, not transcripts.
  - `tool.calls.jsonl` (funnel trace)
  - `feedback.json` (canonical mission outcome)
- Runner enrichments are optional and must not affect scoring.
- Bounded capture by default; redact obvious secrets.
- Deterministic shapes: stable JSON, versioned schemas, atomic writes, safe JSONL appends.
- If you change artifact layout or schema: update `SCHEMAS.md`, `zcl contract --json`, contract snapshot, and tests together.

## Triggered Routines (Keyword -> Doc)

- Feedback triage + recommendation quality:
  - Use `docs/feedback-evaluation.md` when prompts mention: `feedback`, `recommendation`, `user error`, `out of scope`, `single user`, `generalize`, `should we add this`.
  - Apply the broad-value gate before proposing or implementing changes.
  - Anchor conclusions in artifacts (`report`/`validate`/`attempt explain`), not transcripts.

## Operator Workflow (Golden Path)

1. Initialize: `zcl init`
2. Optional preflight (recommended for agent harnesses):
   - `zcl update status --json` (manual update policy; no auto-update)
   - Set `ZCL_MIN_VERSION=<semver>` in harness env to fail fast on old installs.
3. Start attempt (JSON output is required for automation):
   - Native-spawn path (preferred when host supports it): `zcl attempt start --suite <suiteId> --mission <missionId> --prompt <text> --isolation-model native_spawn --json`
   - Batch-plan a full suite for native host orchestration: `zcl suite plan --file <suite.(yaml|yml|json)> --json`
   - Process-runner fallback: `zcl suite run --file <suite.(yaml|yml|json)> --session-isolation process --feedback-policy auto_fail --campaign-id <campaignId> --progress-jsonl <path|-> --json -- <runner-cmd> [args...]`
   - First-class campaign orchestration:
     - `zcl campaign lint --spec <campaign.(yaml|yml|json)> --json`
     - `zcl campaign canary --spec <campaign.(yaml|yml|json)> --missions 3 --json`
     - `zcl campaign run --spec <campaign.(yaml|yml|json)> --json`
     - `zcl campaign resume --campaign-id <id> --json`
     - `zcl campaign status --campaign-id <id> --json`
   - Minimal mode for routine multi-mission comparison:
     - one `campaign.yaml` with `missionSource.path: ./missions` and flow runner blocks
     - no per-flow `suiteFile` required
   - Env handoff: source `<attemptDir>/attempt.env.sh` (auto-written), or run `zcl attempt env --format sh <attemptDir>`
4. Run actions through the funnel:
   - CLI: `zcl run -- <cmd> [args...]` (writes `tool.calls.jsonl`)
   - MCP: `zcl mcp proxy [--max-tool-calls N] [--idle-timeout-ms N] [--shutdown-on-complete] -- <server-cmd> [args...]` (writes `tool.calls.jsonl`)
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
   - `zcl validate --semantic [--semantic-rules <rules.(yaml|yml|json)>] --json <attemptDir|runDir>`
   - If using suites with `expects`: `zcl expect --strict --json <attemptDir|runDir>`
   - Campaign publication guard: `zcl campaign publish-check --campaign-id <id> --json`
   - Optional: reproduce from trace: `zcl replay --json <attemptDir>`
8. Query/index (automation-friendly):
   - Latest attempt: `zcl attempt latest --suite <suiteId> --mission <missionId> --status ok --json`
   - Attempt index rows: `zcl attempt list --suite <suiteId> --status any --json`
   - Run index rows: `zcl runs list --suite <suiteId> --json`

## Artifact Layout (Default)

Root: `.zcl/`
```
.zcl/
  campaigns/<campaignId>/
    campaign.state.json         (optional; canonical cross-run campaign continuity)
    campaign.run.state.json     (optional; first-class campaign execution state)
    campaign.plan.json          (optional; canonical mission plan for deterministic resume)
    campaign.progress.jsonl     (optional; append-only mission/flow checkpoint ledger)
    campaign.report.json        (optional; first-class campaign aggregate report)
    campaign.summary.json       (optional; operator-facing machine summary)
    RESULTS.md                  (optional; operator-facing markdown summary)
    mission.prompts.json        (optional; deterministic prompt materialization artifact)
  runs/<runId>/
    run.json
    suite.json                  (optional snapshot)
    suite.run.summary.json      (optional; `zcl suite run --json` summary artifact)
    run.report.json             (optional; `zcl report --json <runDir>` aggregate)
    attempts/<attemptId>/
      attempt.json
      attempt.env.sh            (ready-to-source env handoff)
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

If you’re changing first-class campaign orchestration:
- `internal/campaign/`
- `internal/runners/campaign_adapter.go`
- `internal/cli/cmd_campaign.go`
- `SCHEMAS.md` (campaign artifacts) + contract snapshot

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
