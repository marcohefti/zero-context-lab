# ZCL Concept (Tool-Agnostic): Funnel-First Agent UX Evaluation Harness

## Purpose
ZCL (ZeroContext Lab) is a harness pattern for measuring and improving **agent/operator UX** for any action surface we want to route agents through.

SurfWright is just the first concrete target. ZCL must also support:
- CLI tools
- MCP servers and clients
- HTTP APIs / SDKs
- internal tools/services
- anything else where we can enforce a single “funnel” and produce deterministic traces

## ZCL Is A Benchmark (Not The Tool)
SurfWright's mission is to *be* efficient and resourceful (fast, composable primitives; deterministic JSON; explicit handles; typed failures).

ZCL's mission is to *measure* that, repeatably, with trace-backed evidence. ZCL is the harness/benchmark layer that:
- defines missions/suites and success criteria
- enforces a funnel + trace-integrity gate (so what happened is observable and comparable)
- emits deterministic artifacts (tool-call traces + attempt/run summaries)
- computes metrics (pass rate, action count, retries, latency, failure codes, output size)
- classifies friction into actionable buckets: `missing_primitive`, `naming_ux`, `output_shape`, `already_possible_better_way`

ZCL answers:
- What does a fresh agent do on first contact?
- How many actions did it take?
- Where did it get stuck?
- Is friction due to `missing_primitive`, `naming_ux`, `output_shape`, or `already_possible_better_way`?

## Non-Negotiables
- **Evidence-first.** Primary evidence must be trace-backed, not self-reported.
- **Deterministic artifacts.** Same inputs should yield the same artifact shapes.
- **Funnel integrity.** If actions bypass the funnel, the run is invalid for discovery scoring.
- **Bounded output.** Captured previews are capped; secrets are redacted.
- **Runner-agnostic.** Should work whether the agent is spawned as a chat sub-agent or launched as an external process.

## Harness Engineering Principles (What We’re Copying On Purpose)
ZCL is a harness. The design should explicitly borrow from proven "harness engineering" practices:
- **Agent legibility is the goal.** The harness exists to make the evaluated surface legible to agents and operators (stable contracts, predictable artifacts, bounded outputs).
- **Knowledge base is the system of record.** Docs are not decoration; they are the operational map agents follow.
- **Feedback loops must be tight.** Missions should yield deterministic evidence and actionable friction classification.
- **Throughput creates entropy.** Without explicit garbage collection, traces, runs, and configs will drift into a swamp.

Reference (reading that influenced this concept):
- `https://openai.com/index/harness-engineering/`

How this changes ZCL (concrete implications):
- ZCL should ship with a small, high-signal doc set (`CONCEPT.md`, `ARCHITECTURE.md`, and a minimal `AGENTS.md` map).
- Artifact contracts must be versioned and stable so “what happened” is comparable across time and runners.
- Implement retention/GC as a first-class concern (age/size based cleanup, plus “blessed” pinned runs).
- Prefer "map, not manual": keep `AGENTS.md` as a table of contents that points to deeper docs, rather than a monolithic brain dump.
- Enforce invariants mechanically: lightweight linters + structural tests should prevent contract drift (artifact layout, schema versions, bounded capture, redaction rules).
- Prefer SurfWright-style repo validation: a few manually-written scripts with deterministic `PASS/FAIL` output, orchestrated by a single `verify` entrypoint, instead of a pile of ad-hoc commands.
- Validate boundary data shapes: funnels/adapters should record structured inputs/outputs at the protocol boundary and fail/flag on unknown shapes instead of guessing.

## Core Terms
- **Runner**: How the agent is executed (chat sub-agent, `codex exec`, CI, other frameworks).
- **Funnel**: The single blessed gateway for doing actions against the evaluated surface.
- **Trace**: The deterministic telemetry emitted by the funnel.
- **Mission**: A task prompt.
- **Attempt**: One execution of a mission.
- **Suite**: A set of missions + expectations.

## Efficiency + Resourcefulness (What ZCL Measures)
ZCL is only useful if it produces measurable signals about **agent/operator UX**.

### Efficiency (minimum cost for a correct result)
Efficiency means: reach a correct mission outcome with minimum measurable cost. In ZCL, "cost" is primarily:
- tool-call count (total, and by operation)
- wall time (and per-call latency distribution)
- failures and retries (including timeouts)
- bytes in/out (a proxy for context pressure and output bloat)

Efficiency is a harness-evaluable property because it falls out of traces.

### Resourcefulness (maximum leverage per action)
Resourcefulness means: use the highest-leverage primitives already available so each action yields maximum progress.

In traces, this tends to show up as:
- reusing explicit handles (`sessionId`/`targetId`) rather than re-discovering state
- using structured primitives (extract/inspect) rather than brittle scrape loops
- reacting to typed failures (`code`) rather than blind retries
- doing targeted "contract discovery" only when needed, then acting directly

Resourcefulness is partially measurable (pattern detection in traces) and partially judgment-based (classification of friction).

## How Agents Must Use ZCL (Prompt Contract)
If ZCL is the benchmark, we need prompts that make runs comparable across runners (Codex, Claude, OpenCode, CI, etc.).

Required prompt rules:
- **Funnel-only rule:** all actions against the evaluated surface must go through the ZCL funnel (wrapper/proxy/instrumented endpoint).
- **Explicit finish rule:** the agent must record the final outcome via ZCL (not only by chatting it), so scoring does not depend on runner logs.

Practical finish mechanism:
- Provide a single canonical command the agent must run at the end: `zcl feedback ...`
- Optionally also require a single machine-parsable chat line for convenience: `RESULT: ...` (secondary evidence only)
- If we want agent self-reports (confusion/friction/suggestions) to be runner-agnostic and diffable, capture them explicitly as ZCL artifacts (secondary evidence; never used as primary scoring input). Recommended shape: a separate `zcl note ...` artifact stream (do not overload `feedback.json`).

## Usage Baseline (Codex Orchestrator + “Zero Context” Agents)
In practice, ZCL will usually be driven by an orchestrator agent (example: Codex) that spawns a *fresh* sub-agent to execute a mission.

The critical UX goal:
- When an operator says “run this through ZCL”, they should not have to explain how ZCL works.
- The orchestrator should automatically route a fresh agent through ZCL, collect evidence, and report back from artifacts.

### Skill shipping (expected)
ZCL will likely ship with a Codex skill (and analogous integrations for other runners) that teaches the orchestrator:
- what ZCL is (benchmark/harness) and what it must capture
- how to funnel tool usage through ZCL
- how to end missions with `zcl feedback` (authoritative outcome)
- how to locate artifacts (`.zcl/runs/...`) and summarize from evidence first

Orchestrator responsibilities (Codex skill contract):
- Resolve the ZCL entrypoint (`zcl` on `PATH`; otherwise a project wrapper like `pnpm zcl` / `npx zcl`).
- Create the run/attempt up front (or ask ZCL to do it) and pass canonical IDs + `ZCL_OUT_DIR` to the spawned agent.
- If the runner provides an `agentId` (Codex does), record it as optional enrichment for correlation; never require it for scoring.
- Keep Turn 2 intentionally unstructured; only introduce structure in Turn 3 if needed.
- Optionally collect agent self-report feedback (free-form, then structured if needed) and persist it as secondary evidence alongside the attempt.

Operator invocation story ("run this through ZCL"):
1. Operator asks: "Run this evaluation/tool through ZCL: <mission>."
2. Orchestrator (via the ZCL skill) resolves the ZCL entrypoint and creates an attempt with canonical IDs + output dir.
3. Orchestrator spawns a fresh "zero context" agent with the fixed harness preamble + env (`ZCL_*`) and the unstructured mission prompt.
4. Agent executes via the funnel and records the canonical result using `zcl feedback`.
5. Orchestrator optionally asks for self-report feedback and records it as secondary evidence (do not mix it into primary scoring).

### What “zero context” means here
The spawned execution agent should be “zero context” about the *target tool/surface* and the domain task.

We intentionally keep the mission prompt minimal and unstructured to surface:
- missing primitives
- naming/UX confusion
- output shape issues
- common failure loops

The only structure we enforce is the harness boundary and finish signal (use ZCL + `zcl feedback`).

### Turn protocol (unstructured mission, structured follow-up only if needed)
Turn 1: harness preamble (fixed).
- Delivered by the runner integration/skill (not the operator), so “run this through ZCL” stays low-friction.
- States: funnel-only rule, explicit finish rule (`zcl feedback`), and how the agent should discover the ZCL entrypoint.
- Must stay stable across missions (no task-specific hints).

Turn 2 (default): unstructured mission prompt + execution.
- Mission text is a simple sentence like:
  - “Use SurfWright through ZCL to go to heftiweb.ch and give me the title of the page.”
- The agent executes via the funnel and records the final result with `zcl feedback`.
- The orchestrator reports back primarily from artifacts (`tool.calls.jsonl`, `attempt.report.json`, `feedback.json`), not from the agent’s narrative.

Turn 3 (optional): structured follow-up.
- Only if information is missing or we want to standardize output, ask for a structured response (or run `zcl report --strict` / `zcl validate`).
- Turn 3 is for *format*, classification, or additional constraints, not for leading the agent during discovery.
  
Optional (between turns): agent self-report feedback (secondary evidence).
- The orchestrator may ask the spawned agent for free-form feedback ("what was confusing / what would have helped") and, if needed, a structured follow-up response in Turn 3.
- By default this exists only in the runner transcript. If we want it portable across runners and easy to diff later, record it into ZCL artifacts as an explicit "note"/"annotation" alongside the attempt.
- Self-reports can inform classification/triage, but they must not override trace evidence; scoring stays trace + `feedback.json`.

### How the agent finds “the ZCL command”
The operator should not have to specify whether ZCL is available as `zcl`, `pnpm zcl`, an `npx` wrapper, etc.

Design requirement:
- The ZCL integration (skill) or the harness environment must make the entrypoint discoverable.

Recommended baseline:
- In evaluation environments, prefer making ZCL available as a plain `zcl` command on `PATH`.
- If that is not true in a given project, the orchestrator must provide the concrete entrypoint it detected (e.g., a project-local wrapper) without changing the mission into a long, structured recipe.

## Safety Defaults (Write Boundaries)
ZCL exists to *measure* a surface, not to mutate the world. By default, ZCL should be safe to run in any repo.

Defaults:
- ZCL itself writes only to:
  - per-project artifacts under `.zcl/` (default output root)
  - global state under `~/.zcl/` (config/caches; optional)
- If a funnel needs scratch space, it must live under `.zcl/tmp` (project) or `~/.zcl/tmp` (global) and be referenced from attempt artifacts.
- Nothing in ZCL should modify source files, git state, or project config unless explicitly asked and explicitly permitted.

Clarification:
- Evaluated tools may still write elsewhere (profiles, caches, downloads). ZCL should not pretend otherwise.
- If we want write isolation, it must be an explicit mode (e.g. run in a temp workdir) and recorded as part of the attempt metadata.

## Run Modes + Strictness
ZCL should support two run modes with clear semantics so we can use it for both exploration and regression.

`discovery` mode (gap-finding / exploration):
- best-effort data capture
- attempts may be partial (timeouts/crashes still produce artifacts + integrity warnings)
- bypass detection is recorded but can be scored as "invalid for benchmark" rather than aborting the whole run

`ci` / `regression` mode (benchmark enforcement):
- strict artifact completeness: missing required artifacts makes the attempt fail
- strict funnel integrity: bypass = invalid attempt (hard fail if suite requires it)
- strict finish requirement: missing `feedback.json` = fail
- suite can define explicit expectations; failures are typed and comparable

One important rule: do not "fix flakiness" by increasing timeouts by default. Treat timeouts as evidence of missing primitives, unstable waits, or harness/tool mismatches.

## Suites + Missions (Definition Format)
To run 50+ sessions without ad-hoc prompts, ZCL needs a minimal suite format that is runner-agnostic.

Minimal suite file (example YAML; JSON equivalent is fine):
```yaml
version: 1
suiteId: heftiweb-smoke
defaults:
  timeoutMs: 120000
  mode: discovery
missions:
  - missionId: latest-blog-title
    prompt: "Navigate to https://heftiweb.ch -> Blog -> latest article. Record ARTICLE_TITLE=<title> using zcl feedback."
    tags: ["browser", "navigation", "smoke"]
    expects:
      ok: true
      result:
        type: string
        pattern: "^ARTICLE_TITLE=.*"
```

Notes:
- The suite format should stay tiny: prompt + timeouts + tags + optional expectations.
- Expectations should validate `feedback.json` (not chat text).

## Correlation Strategy (Runner-Agnostic)
Correlation must be possible without scraping prompts or relying on runner-specific metadata.

Canonical IDs:
- `runId`, `suiteId`, `missionId`, `attemptId`, optional `agentId`

Propagation rules:
- The orchestrator creates the run/attempt up front (so the spawned agent can stay "zero context" about ZCL internals).
- Every event in `tool.calls.jsonl` includes the canonical IDs.
- `attempt.json` and `attempt.report.json` include the same IDs.
- ZCL exports canonical IDs to the agent via environment variables (or explicit text in the prompt):
  - `ZCL_RUN_ID`, `ZCL_SUITE_ID`, `ZCL_MISSION_ID`, `ZCL_ATTEMPT_ID`, optional `ZCL_AGENT_ID`
  - `ZCL_OUT_DIR` (where the funnel should write artifacts for this attempt)
- `agentId` is runner-provided when available (example: Codex `spawn_agent` returns one). ZCL should not require an "agent registration" handshake: if the orchestrator knows the runner agent id, it records it (and can pass it along); otherwise omit it and rely on `attemptId` for identity.
- Practical helper: `zcl attempt start --json` can allocate the attempt dir + ids and print the env/pointers the orchestrator should pass to the spawned agent.

Runner enrichment rules:
- Runner adapters must not change scoring and must never be required for correctness.
- Runner adapters should preferentially use a stable pointer when available (example: Codex MCP returns `rolloutPath`), otherwise fall back to searching runner session stores.

## ZCL Error Taxonomy (Typed Failures)
ZCL must have its own typed error codes (distinct from evaluated tool error codes) so harness failures are not misattributed to the tool under test.

Principles:
- Underlying tool failures should preserve the tool’s typed `code` if available.
- ZCL-level failures use `ZCL_E_*` and should be emitted by `zcl validate`, `attempt.report.json` integrity, and any command that can fail.

Minimal ZCL error code set (starting point):
- `ZCL_E_MISSING_ARTIFACT` (required artifact missing)
- `ZCL_E_INVALID_JSON` (artifact not parseable JSON/JSONL)
- `ZCL_E_SCHEMA_UNSUPPORTED` (unknown/unsupported schema version)
- `ZCL_E_BOUNDS` (capture/preview exceeded configured bounds)
- `ZCL_E_UNSAFE_EVIDENCE` (evidence violates safety policy, e.g. raw captures in strict CI mode)
- `ZCL_E_REDACTION_FAILED` (secrets could not be safely redacted)
- `ZCL_E_FUNNEL_BYPASS` (actions detected outside the funnel, if detectable)
- `ZCL_E_TOOL_FAILED` (wrapped tool failed without a typed code; include exit code and stderr preview)
- `ZCL_E_TIMEOUT` (harness-level timeout; distinguish from tool typed timeouts when possible)
- `ZCL_E_RUNNER_ENRICH_FAILED` (runner adapter failed; non-fatal for scoring)

## Replayability (Make Failures Deterministic)
When a mission fails, the harness should make it easy to reproduce and minimize.

Conceptual commands:
- `zcl replay <attemptDir>`: replays `tool.calls.jsonl` through the same funnel (best-effort; not all actions are replayable).
- `zcl reduce <attemptDir>`: attempts to minimize the trace to a smaller failing sequence (optional; future capability).

Even partial replayability is valuable: it turns “agent flaked” into “this boundary sequence fails under these conditions.”

## Schema Evolution Rules (Don’t Break Evidence)
ZCL is only credible if old evidence remains readable.

Rules:
- `tool.calls.jsonl` includes `v` (schema version). Additive fields are allowed without breaking older readers.
- Breaking changes require bumping `v` and keeping readers compatible with at least the previous major version (configurable, but stated explicitly).
- Artifact layout/version should be explicit (e.g., recorded in `run.json`), and `zcl contract --json` should be the authoritative way to discover supported versions and required fields.

## Ground Rules (Operator + Repo)
This concept is intentionally written to stand alone for future sessions. It captures the constraints we are optimizing for and where to look for supporting evidence.

### Operator priorities (from this thread)
- Clean architecture + scalability + maintainability, with real operator UX as a first-class constraint (fast, obvious workflows; minimal friction).
- Be direct and concrete; verify assumptions with checks/evidence instead of guessing.
- Prefer clean-slate solutions while the system is still early; avoid backwards-compat complexity unless it buys clear value.
- Treat changes as precious: clarity, momentum, and respect for attention. Default to evaluation-only runs that do not modify the repo.
- ZCL and funnels should minimize token bloat: bounded outputs, deterministic JSON where possible.

### Repo source of truth (ZCL)
ZCL must be designed so agents can keep it up to date by reading a small, high-signal knowledge base.

Within the ZCL repo, the intended system of record is:
- `CONCEPT.md` (this file): why ZCL exists and what it must guarantee.
- `ARCHITECTURE.md`: the feasible design that implements the concept without overengineering.
- A short `AGENTS.md` that acts as a map to the docs and the most important commands.

### Validation discipline (SurfWright-style scripts; Go-native)
ZCL should copy SurfWright's repo validation approach: small, manually-written scripts with deterministic output, wired into one obvious entrypoint.

Principles:
- One front door: `verify` runs everything required before merge/release.
- In a Go repo, prefer a single `scripts/verify.sh` that runs tests + validation sequentially (mirrors SurfWright's `pnpm verify` pattern).
- Each script checks one invariant and prints minimal output (`<check>: PASS` or `<check>: FAIL <reason>`).
- Scripts may also emit machine-readable JSON reports under `artifacts/<check>/report.json` (optional), but the default output must stay small.
- Prefer sequential execution (avoid build races and half-updated artifacts).

Suggested checks (repo/CI; distinct from runtime `zcl validate` which validates attempt artifacts):
- `contract-snapshot-check`: `zcl contract --json` matches a normalized snapshot (sort arrays, drop volatile fields; update via an explicit `--update` path).
- `skill-validate`: shipped skills/integration packs are structurally valid (frontmatter, directory names, no junk docs).
- `release-check`: version/changelog/release artifact sanity (checksums present, reproducible naming, etc).

Script addition gate (to prevent scripts from becoming a swamp): add a new script only if at least two are true:
1. The same manual logic has been repeated in multiple PRs/tasks.
2. Prose instructions have caused mistakes or drift.
3. Deterministic output is required for automation.
4. The script materially reduces token/context cost in repeated agent loops.

### Reference target (example): SurfWright
This concept was developed while evaluating SurfWright as the first concrete target tool. These are example target constraints (not ZCL requirements):
- Stable surface properties: deterministic I/O, composable primitives, JSON-first, explicit handles (`sessionId`/`targetId`), typed failures (`code` + `message`).
- Runtime contract inspection example: `surfwright --json contract`.
- In-repo ZeroContext workflows that inspired ZCL: `docs/zerocontext-lab.md`, `docs/zerocontext-gap-workflow.md`.

### Local evidence context (where the initial evidence in this doc came from)
These paths are included for reproducibility of the early investigation and can be ignored by readers on other machines.

- The initial ZCL concept work happened inside a SurfWright checkout:
  - `/Users/marcohefti/Sites/surfwright` (shell: `zsh`)
- Codex runner session storage (used as secondary evidence):
  - `/Users/marcohefti/.codex/sessions/<YYYY>/<MM>/<DD>/rollout-*.jsonl`
- Codex codebase reference (to implement/verify a runner adapter):
  - `/Users/marcohefti/Sites/codex`

## What ZCL Should Return (Contract)
ZCL has two audiences:
- the **agent** (needs a stable surface to act through without surprises)
- the **operator/CI** (needs deterministic evidence + comparable metrics)

### Per action (agent-facing)
Default: transparent passthrough of the underlying tool's output, with tracing as a side effect.
- The agent runs actions through the funnel (e.g. `zcl run -- <tool> ...`, `zcl mcp proxy ...`, etc).
- ZCL returns the tool's stdout/stderr/exit behavior unchanged (to avoid mutating the tool contract).
- ZCL additionally records a trace event for each action.

Optional modes (must be explicit because they change UX):
- **Envelope mode:** return a ZCL JSON envelope around the tool result (useful for inherently messy tools, or if we standardize error semantics).
- **Capture mode:** store large outputs to artifacts and return a pointer + bounded preview (prevents token bloat).

### End of attempt (operator/CI-facing)
ZCL should write a stable attempt report that can be used without any runner integration:
- `attempt.report.json` with `ok`, metrics, and artifact pointers.
- `tool.calls.jsonl` (the primary evidence stream).

Optionally, ZCL can also emit:
- `runner.metrics.json` (tokens/turns/model/etc) when a runner integration is configured and available.

Suggested minimum fields (conceptual):
- `attempt.report.json`
  - `ok` (boolean)
  - `result` (string or JSON; canonical outcome recorded by `zcl feedback`)
  - `ids`: `runId`, `suiteId`, `missionId`, `attemptId`, `agentId` (if known)
  - `subject`: what we evaluated (e.g. `{ tool:"surfwright", funnelType:"cli-wrapper", toolVersion:"0.1.1" }`)
  - `timing`: `startedAt`, `endedAt`, `wallTimeMs`
  - `metrics`: `toolCallsTotal`, `toolCallsByOp`, `failuresTotal`, `failuresByCode`, `timeoutsTotal`, `retriesTotal`, `outBytesTotal`, `errBytesTotal`
  - `artifacts`: relative paths (`tool.calls.jsonl`, logs, optional screenshots/snapshots)
  - `classification` (optional): `missing_primitive` | `naming_ux` | `output_shape` | `already_possible_better_way`
  - `notes` (optional): short operator notes (not evidence)
- `runner.metrics.json` (optional, nullable fields)
  - `runnerKind`: `codex` | `claude` | `opencode` | `unknown`
  - `runnerSessionRef`: stable pointer (file path, URL, ids)
  - `model`: name/provider, and any runner config that affects behavior
  - `turnCount` (if available)
  - `tokenUsage` (if available): input/cached/output/reasoning/total + context window
  - `runnerToolCalls` (if available): count + total duration

## What We Learned So Far (Evidence + References)

### 1) Spawned chat sub-agent sessions are persisted (and searchable)
We spawned a chat sub-agent inside this session and gave it the mission: navigate to `https://heftiweb.ch` → Blog → latest article → print `ARTICLE_TITLE=<title>`.

Spawned agent id:
- `019c6060-7d91-7e21-a4b5-17347d2fa54f`

The agent’s persisted session rollout contains the exact prompt and the final output line:
- `/Users/marcohefti/.codex/sessions/2026/02/15/rollout-2026-02-15T15-17-42-019c6060-7d91-7e21-a4b5-17347d2fa54f.jsonl`

The parent thread that spawned it (contains `spawn_agent` + `wait` correlation):
- `/Users/marcohefti/.codex/sessions/2026/02/15/rollout-2026-02-15T14-01-48-019c601b-016a-7842-b43b-54f5f8ab3c61.jsonl`

How to locate it:
- By agent id: `rg -n --fixed-strings "019c6060-7d91-7e21-a4b5-17347d2fa54f" /Users/marcohefti/.codex/sessions`
- By prompt fragment: `rg -n --fixed-strings "Mission: using ONLY the \`surfwright\` CLI" /Users/marcohefti/.codex/sessions`

### 2) The current in-repo ZCL harness is process-based
The harness is documented and implemented here:
- `docs/zerocontext-lab.md`
- `docs/zerocontext-gap-workflow.md`
- `scripts/zerocontext-lab.mjs`
- `scripts/zerocontext-lab/run.mjs`
- `scripts/zerocontext-lab/options.mjs`

It writes deterministic run artifacts under:
- `.zerocontext-lab/runs/<runId>-<label>/...`

Note on current naming vs the generalized concept:
- The current in-repo harness logs SurfWright tool calls to `commands.jsonl` (SurfWright-specific name).
- In this generalized concept, we call the tool-call trace `tool.calls.jsonl` to support any funnel/tool type.

This session produced concrete runs in:
- `.zerocontext-lab/runs/20260215-075526Z-115f13-gpt-5` (failed immediately due to runner arg mismatch)
- `.zerocontext-lab/runs/20260215-075552Z-09c5a6-gpt-5` (timed out)
- `.zerocontext-lab/runs/20260215-075920Z-02883b-gpt-5` (passed)
- `.zerocontext-lab/runs/20260215-080310Z-975772-gpt-5` (passed)

### 3) Runner mismatch is real (Codex CLI flag drift)
The docs currently reference `codex exec --prompt-file {prompt_file}` (see `docs/zerocontext-lab.md:114`). On this machine:
- `codex --version` is `0.101.0`
- `codex exec` does **not** support `--prompt-file` (it expects stdin for `-`)

Evidence:
- `.zerocontext-lab/runs/20260215-075526Z-115f13-gpt-5/attempts/001-heftiweb-blog-latest-title-r1/agent.stderr.log` contains `unexpected argument '--prompt-file'`.

This matters because it shows why ZCL cannot be tightly coupled to one runner or one runner’s flags.

### 4) Tool surface reality (SurfWright mission details)
For the mission we ran:
- `https://heftiweb.ch` links Blog to `https://blog.heftiweb.ch/` (Substack).
- DOM waits like “wait for `h1`” can be slow/flaky on heavy JS pages.
- Structured extraction can be a more deterministic primitive than DOM scraping.

Evidence example:
- `.zerocontext-lab/runs/20260215-075552Z-09c5a6-gpt-5/attempts/001-heftiweb-blog-latest-title-r1/commands.jsonl` shows a failed `target wait ... --for-selector h1` leading to `E_WAIT_TIMEOUT`.

## The Issue With the Current Concept
We conflated “ZCL” with “a harness that spawns an OS process and shims PATH for `surfwright`”. That is an implementation detail, not the concept.

Problems with runner-centric ZCL:
- **Chat sub-agents can’t be truly wrapped** by a process-based harness unless we introduce an explicit funnel they are forced to use.
- **Trace integrity is fragile** when the funnel is a PATH shim; agents can bypass it (intentionally or accidentally) by calling alternate entry points.
- **Runner flags drift** (example above) creates spurious failures unrelated to the tool being evaluated.

If the goal is: “funnel agents through X and observe what happened,” then ZCL must be **funnel-first**, not runner-first.

## New Concept: ZCL as a Funnel-First, Tool-Agnostic Harness

### Core idea
ZCL should work for anything we want to evaluate (CLI, MCP, HTTP, SDK) by enforcing:
1. A **single funnel** for actions.
2. A **stable trace contract** emitted by that funnel.
3. Optional correlation to runner/session logs for extra context.

ZCL is successful when we can run 50 missions and produce comparable evidence across:
- different agents
- different runners
- different tools

### Evidence hierarchy
- **Primary evidence:** funnel traces (what was actually attempted).
- **Secondary evidence:** runner/session logs (prompt, conversation structure, narrative).

This matches our current reality: if we want more “agent context”, we can inspect `~/.codex/sessions/...`.

## What We Can Capture Where

ZCL must be designed such that **primary scoring does not depend on any runner**. Runner/session logs are optional enrichment.

### From ZCL (the funnel + artifacts): primary evidence
What ZCL can reliably capture (runner-agnostic):
- The full ordered sequence of tool actions (argv / JSON-RPC params / HTTP method+url / SDK call signature).
- Per-action timing (timestamps + duration) and run/attempt wall time.
- Exit/typed error semantics (exit codes for CLI, `code` for typed failures, normalized codes when needed).
- Input/output size metrics (bytes) and bounded previews (to avoid token bloat).
- Redaction decisions (what was removed/hashed, and how often).
- Deterministic artifact pointers (logs, snapshots/screens, raw responses when capture mode is enabled).

What ZCL can (and should) derive directly from those traces:
- `toolCallsTotal` and `toolCallsByOp`.
- `failuresTotal`, `failuresByCode`, `timeoutsTotal`, `retriesTotal`.
- `latencyMsByOp`, `slowestCalls`.
- Output bloat signals (bytes out, preview truncation counts).
- "No-progress" patterns (heuristics): repeated waits/clicks, repeated navigation, repeated extraction attempts.

What ZCL cannot reliably know without runner integration:
- Chat turn count and message sizes (unless the runner provides them).
- Model token usage (prompt/completion/reasoning) unless the runner provides it.
- True "intent" or "thinking" (only inferable via patterns in tool-call inputs).

### From runner logs (optional enrichment): secondary evidence
Runner integrations should provide extra metrics and context, but the attempt must still stand on ZCL evidence alone.

What runner/session logs can add in general (when available):
- Full prompt and conversation transcript (messages, roles).
- Turn structure and timestamps.
- Runner-level tool-call transcripts (e.g., how the runner invoked the funnel).
- Model and configuration metadata.
- Token usage (only when the runner logs it).

#### Codex integration: exactly what we can derive
Codex rollouts in `~/.codex/sessions/.../rollout-*.jsonl` contain:
- Session meta (id, timestamps, cwd, git commit/branch, model/provider) in `session_meta` and `turn_context`.
- Assistant/user/developer messages in `response_item` with `payload.type:"message"`.
- Runner tool calls + args in `response_item` with `payload.type:"function_call"`.
- Runner tool outputs in `response_item` with `payload.type:"function_call_output"`.
- Evidence-grade token usage in `event_msg` with `payload.type:"token_count"` (includes input/cached/output/reasoning tokens and totals).

Codex evidence from the Codex codebase (useful when implementing the Codex runner adapter):
- Codex SDK (TypeScript) explicitly states that it exchanges **JSONL events** over stdin/stdout and that threads are persisted in `~/.codex/sessions`:
  - `/Users/marcohefti/Sites/codex/sdk/typescript/README.md`
- Codex MCP interface returns a `rolloutPath` when starting a new conversation, which is a clean way for ZCL to record `runner.ref.json` without filesystem searching:
  - `/Users/marcohefti/Sites/codex/codex-rs/docs/codex_mcp_interface.md` (see `newConversation` example returning `rolloutPath`)

Concrete evidence from the spawned sub-agent session:
- `/Users/marcohefti/.codex/sessions/2026/02/15/rollout-2026-02-15T15-17-42-019c6060-7d91-7e21-a4b5-17347d2fa54f.jsonl` includes `token_count` events and the final `ARTICLE_TITLE=Context Amnesia`.

### How to standardize runner enrichment (Codex/Claude/OpenCode/etc)
Treat each runner as an optional adapter that emits:
- `runner.ref.json` (a stable pointer to the source log: file path, URL, ids)
- `runner.metrics.json` (normalized metrics, when present)

Core idea: ZCL scoring uses only ZCL artifacts. Runner metrics are additive and must be nullable/optional.

## Design Outline (Tool-Agnostic ZCL)

### 1) Versioned artifact contract (publishable)
ZCL should emit a consistent directory structure (example; per-project output root `.zcl/`):
- `.zcl/runs/<runId>/run.json`
- `.zcl/runs/<runId>/suite.json` (input snapshot)
- `.zcl/runs/<runId>/attempts/<attemptId>/attempt.json`
- `.zcl/runs/<runId>/attempts/<attemptId>/attempt.report.json` (computed summary metrics + artifact pointers)
- `.zcl/runs/<runId>/attempts/<attemptId>/prompt.txt`
- `.zcl/runs/<runId>/attempts/<attemptId>/tool.calls.jsonl`
- `.zcl/runs/<runId>/attempts/<attemptId>/feedback.json` (output of `zcl feedback`, required for runner-agnostic scoring)
- `.zcl/runs/<runId>/attempts/<attemptId>/notes.jsonl` (optional; secondary evidence such as agent self-reports)
- `.zcl/runs/<runId>/attempts/<attemptId>/runner.ref.json` (optional pointers into runner logs)
- `.zcl/runs/<runId>/attempts/<attemptId>/runner.metrics.json` (optional normalized runner metrics: tokens/turns/model/etc)

### 2) Tool-agnostic event schema
Each line in `tool.calls.jsonl` is one action event. Core fields:
- `v` (schema version)
- `ts`
- `runId`, `missionId`, `attemptId`
- `tool` (e.g. `surfwright`, `mcp:chrome-devtools`, `http`, `sdk:stripe`)
- `op` (e.g. `open`, `tools/call`, `GET`, `createCustomer`)
- `input` (canonicalized representation of the call)
- `result`:
  - `ok`
  - `code` (typed error code if available)
  - `exitCode` (for CLI)
  - `durationMs`
- `io`:
  - `outBytes`, `errBytes` or `respBytes`
  - capped previews
- `redactionsApplied` (list)

Tool-specific details go under `enrichment`, not top-level.

### 3) Funnels (adapters)
ZCL supports multiple funnel types.

CLI funnel:
- Wrapper binary: `zcl run -- surfwright --json ...` or `zcl surfwright --json ...`
- Or built-in tracing via env/flag (preferred long-term because it’s harder to bypass).

MCP funnel:
- Proxy at the JSON-RPC boundary (stdio/websocket) that logs:
  - `initialize`, `tools/list`, `tools/call`, errors
  - latency per call
  - request/response payload sizes
  - redaction of secrets

HTTP funnel:
- Local proxy or wrapper SDK that logs:
  - method, url, status
  - latency
  - payload sizes
  - redacted headers/body

SDK funnel:
- Wrapper module that logs:
  - function name + args (bounded)
  - result summary (bounded)
  - typed errors
  - latency

### 4) Trace integrity gates
A run is valid only if actions go through the funnel.
Enforcement options:
- Provide only the wrapped endpoints/binaries in the agent environment.
- For MCP/HTTP, only expose the proxy endpoint.
- Post-hoc detection via runner logs is possible but weaker than enforcement.

### 5) Scoring and classification
ZCL produces summary metrics from traces:
- action count
- failure count and top error codes
- total duration
- slowest actions

Then classify:
- `missing_primitive`
- `naming_ux`
- `output_shape`
- `already_possible_better_way`

### 6) Feedback hook
Require each mission to end with an explicit finish signal that ZCL can record without runner logs.

Canonical mechanism:
- Agent runs exactly one command: `zcl feedback --ok|--fail --result <string or json>`
- ZCL writes `feedback.json` and uses it as the authoritative mission outcome.

Optional (secondary evidence only):
- Agent also prints a single machine-parsable line to chat, e.g. `RESULT: ...`

### 7) Privacy / redaction / bounds
ZCL must assume secrets exist.
- Default redaction rules.
- Hard caps on previews and payload size.
- Configurable allowlists for safe fields.

## Lessons from OpenClaw (Shipping + Operator UX)
We inspected `/Users/marcohefti/Sites/openclaw` for proven operator UX + distribution patterns we can reuse for ZCL shipping.

Key fact (so we don’t draw the wrong conclusion):
- OpenClaw is primarily Node/TypeScript (entrypoint `openclaw.mjs`, install/update docs under `docs/`).
- The only Go code present is a small docs utility under `scripts/docs-i18n/` (`scripts/docs-i18n/go.mod`), not the main product runtime.

Scope note:
- ZCL is a benchmark tool. Most of the shipping UX patterns below are **optional polish**, not requirements for ZCL v1.

Patterns worth copying into ZCL:
- **Installer scripts as the primary one-liner entrypoint.**
  - OpenClaw serves `install.sh`, `install-cli.sh` (local prefix, no root), and `install.ps1` from a website and documents their flow + flags.
  - Reference: `/Users/marcohefti/Sites/openclaw/docs/install/installer.md`.
  - Implication for ZCL: if we ever want one-liner installers, keep them as small scripts in-repo (or attached to GitHub Releases) and optionally run `zcl doctor` / `zcl init`. A dedicated domain is unnecessary for a benchmark tool.
- **Update is a first-class UX surface (status + wizard + JSON).**
  - OpenClaw provides `openclaw update`, `openclaw update status`, `openclaw update wizard`, `--channel stable|beta|dev`, and `--json`.
  - Reference: `/Users/marcohefti/Sites/openclaw/docs/cli/update.md`.
  - Implication for ZCL (optional): if we implement self-update, provide `zcl update`, `zcl update status`, and `--json` output (and optionally a wizard), plus a post-update safety gate (`zcl doctor`).
- **Channel semantics decoupled from version numbers.**
  - OpenClaw treats dist-tags/channels as the source of truth for npm installs (promotion between channels without changing the version number).
  - Reference: `/Users/marcohefti/Sites/openclaw/docs/install/development-channels.md`.
  - Implication for ZCL (optional): if we do channels, publish a simple channel index (`stable.json`, `beta.json`, `dev.json`) that maps a channel to a release asset/version; `zcl update` can read it. Otherwise, ignore this and just ship versioned binaries.
- **State layout: a global state dir + explicit subdirectories.**
  - OpenClaw standardizes global state under `~/.openclaw/...` (config/logs/sessions/etc), and docs consistently reference it.
  - Reference (global state usage throughout docs): `/Users/marcohefti/Sites/openclaw/docs/**`.
  - Implication for ZCL: standardize global state under `~/.zcl` (config, caches, runner adapters) and keep run artifacts under per-project `.zcl/runs` by default (configurable).
- **Skills are optional UX glue, not the core product definition.**
  - OpenClaw ships `skills/` as a first-class integration surface.
  - Implication for ZCL: ship optional “runner integration packs” (Codex skill, etc.) that enforce the funnel-only + explicit finish rule, but keep ZCL scoring runner-agnostic.

## How a Project Would Implement/Use It (Packaging Model)

### Packaging options
ZCL is a standalone product; SurfWright (and any other tool) is a consumer of ZCL.

Recommended distribution shape for ZCL core:
- **Go single-binary CLI** (`zcl`) published on GitHub Releases with signed checksums.
- Optional package-manager installs for UX: Homebrew (`brew`), WinGet (`winget`), and a `go install ...@version` fallback.

Reasonable install entrypoints (benchmark-tool level):
- Download the correct binary from GitHub Releases and put it on `PATH`.
- Optional: `brew install <tap>/zcl` or `winget install <org>.zcl` (if we decide to maintain them).
- Optional: `go install <module>/cmd/zcl@<version>` (dev-friendly fallback).

Optional convenience distributions:
- **`npx` wrapper** (`@zcl/cli`) that downloads the correct binary for the platform and forwards args (good for JS repos and CI, without making Node a dependency of ZCL core).
- **Docker image** (CI pinning): `ghcr.io/<org>/zcl:<version>`.

### Per-project init
Conceptually:
- `zcl init` creates `zcl.config.json` and a default output root (example `.zcl/runs`).

Note on output roots:
- The current in-repo harness uses `.zerocontext-lab/runs/...`.
- A general ZCL tool can default to `.zcl/runs/...`, but the out root should be configurable.

Config includes:
- out root
- enabled funnels (cli/mcp/http/sdk)
- redaction rules
- suite paths
- correlation policy (whether to record session pointers)

### Global state layout (recommended)
To keep ZCL usable across many projects while keeping evidence local and reproducible:
- Global ZCL state (runner-agnostic): `~/.zcl/`
  - global config (defaults, redaction presets)
  - caches (downloaded binaries for wrappers, schema versions)
  - runner adapter configs (Codex/Claude/OpenCode integrations)
- Per-project ZCL outputs (primary evidence): `.zcl/runs/` (default; configurable)

### Runner integration patterns
ZCL must not depend on a specific runner.

Chat sub-agent mode (preferred operator control):
- Spawn agent.
- Prompt includes:
  - `ZCL_RUN_ID`, `ZCL_MISSION_ID`, `ZCL_AGENT_ID`
  - “All actions must go through the funnel: `<funnel cmd/endpoint>`”
- Funnel writes trace artifacts.
- If deeper context is needed, inspect runner sessions (Codex: `~/.codex/sessions/...`).

External runner mode (CI style):
- ZCL launches runner process.
- ZCL injects funnel environment.
- ZCL asserts `expect` checks.

## Where We Stand
- We have proof that spawned chat sub-agents are persisted and correlatable via agent id:
  - `/Users/marcohefti/.codex/sessions/2026/02/15/rollout-2026-02-15T15-17-42-019c6060-7d91-7e21-a4b5-17347d2fa54f.jsonl`
- We have proof the current in-repo harness produces deterministic artifacts for SurfWright when running an OS-spawned runner:
  - `.zerocontext-lab/runs/...`
- We have evidence that runner coupling is brittle (Codex CLI flag mismatch), reinforcing runner-agnostic design.

## Next decisions (conceptual)
- Prefer **built-in tracing** in evaluated tools (harder to bypass) vs wrapper binaries (easier to prototype).
- Decide the minimal schema for `tool.calls.jsonl` to be publishable and stable.
- Decide how strict trace integrity should be in discovery mode vs CI mode.
