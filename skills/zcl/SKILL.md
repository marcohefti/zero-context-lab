# ZCL Orchestrator (Codex Skill)

This skill is for the *orchestrator* (you), not the spawned "zero context" agent.

## Goal

Run a mission through ZCL with funnel-first evidence and deterministic artifacts under `.zcl/`.

Primary evidence:
- `.zcl/.../tool.calls.jsonl`
- `.zcl/.../feedback.json` (authoritative outcome)

Secondary evidence (optional):
- `.zcl/.../notes.jsonl`
- `.zcl/.../runner.*.json`

## Operator Invocation Story

When an operator says "run this through ZCL: <mission>", do this:

1. Resolve entrypoint: prefer `zcl` on `PATH`.
2. Initialize project if needed: `zcl init` (idempotent).
3. Start an attempt (JSON output is required):
   - `zcl attempt start --suite <suiteId> --mission <missionId> --prompt <promptText> --json`
   - Capture the returned `env` map and pass it to the spawned agent process.
4. Spawn a fresh "zero context" agent with:
   - a fixed harness preamble (funnel-only rule + finish rule)
   - Turn 2 is one unstructured sentence mission prompt (no recipes)
5. Require the agent to finish by running:
   - `zcl feedback --ok|--fail --result ...` or `--result-json ...`
6. Optionally ask for self-report feedback and persist it as secondary evidence:
   - `zcl note --kind agent --message "..."`
7. Report back from artifacts (not from transcript): `tool.calls.jsonl`, `feedback.json`, and `attempt.report.json` (if computed).

## Fixed Harness Preamble (Turn 1)

You must tell the spawned agent:
- Funnel-only: all actions must go through ZCL funnels (e.g. `zcl run -- ...`).
- Finish rule: must end with `zcl feedback ...` (required for scoring).
- Attempt context: ZCL attempt env vars are already provided (do not invent ids).

## Turn 2 (Default)

One sentence mission prompt. Example:

"Use SurfWright through ZCL to navigate to https://example.com and record TITLE=<...> via `zcl feedback --ok --result ...`."

## Turn 3 (Optional)

Only if needed: request structured formatting or classification, but do not lead the agent during discovery.

