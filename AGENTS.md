# ZCL Agent Map (High-Signal)

Start here:
- `CONCEPT.md` (why ZCL exists; non-negotiables; operator UX baseline)
- `ARCHITECTURE.md` (how we implement it without overengineering)

## What ZCL Is
ZCL is a funnel-first benchmark harness. It measures agent/operator UX by producing deterministic, trace-backed artifacts.

## Evidence Hierarchy
Primary evidence (scoring must work from this alone):
- `tool.calls.jsonl` (funnel trace)
- `feedback.json` (canonical mission outcome; written via `zcl feedback`)

Secondary evidence (useful for triage; never overrides trace evidence):
- `notes.jsonl` (agent self-reports, operator notes, structured follow-ups)
- `runner.*.json` (optional enrichment from runners like Codex)

## Operator Invocation Story
When an operator says "run this through ZCL", the orchestrator (Codex skill) should:
1. Resolve the ZCL entrypoint (`zcl` on `PATH`, or a project wrapper like `pnpm zcl`).
2. Start an attempt and pass `ZCL_*` env + a fixed harness preamble to a fresh "zero context" agent.
3. Keep Turn 2 intentionally unstructured (mission prompt is a single sentence).
4. Require the agent to finish with `zcl feedback ...`.
5. Optionally collect free-form + structured self-report feedback and persist it via `zcl note ...`.

## Artifact Layout (Example)
Per project, artifacts live under `.zcl/` by default:
```text
.zcl/
  runs/
    <runId>/
      run.json
      attempts/
        <attemptId>/
          attempt.json
          tool.calls.jsonl
          feedback.json
          notes.jsonl
          attempt.report.json
```

## Command Surface (MVP)
Core:
- `zcl init`
- `zcl attempt start --json` (allocates attempt dir + ids; prints env/pointers)
- `zcl run -- <cmd> ...` (CLI funnel)
- `zcl feedback ...` (canonical outcome)
- `zcl report ...`
- `zcl validate ...`
- `zcl contract --json`
- `zcl doctor`
- `zcl gc`

Optional/later:
- `zcl note ...` (secondary evidence)
- `zcl enrich --runner codex`
- `zcl mcp proxy ...`
- `zcl replay ...`

## Repo Validation
Run the SurfWright-style, script-driven checks before merge/release:
```bash
./scripts/verify.sh
```

## Guardrails
- Don’t couple scoring to runner transcripts.
- Keep captures bounded + redacted by default.
- If you change artifact layout or trace schema, update docs + validation rules together.
