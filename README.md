# Zero Context Lab (ZCL)

<a href="https://github.com/marcohefti/zero-context-lab/releases"><img alt="Release" src="https://img.shields.io/github/v/release/marcohefti/zero-context-lab?sort=semver&amp;style=flat-square"></a>
<a href="https://github.com/marcohefti/zero-context-lab/actions/workflows/ci.yml"><img alt="CI" src="https://img.shields.io/github/actions/workflow/status/marcohefti/zero-context-lab/ci.yml?branch=main&amp;label=ci&amp;style=flat-square"></a>
<a href="https://github.com/marcohefti/zero-context-lab/actions/workflows/entropy-guard.yml"><img alt="Entropy Guard" src="https://img.shields.io/github/actions/workflow/status/marcohefti/zero-context-lab/entropy-guard.yml?branch=main&amp;label=entropy%20guard&amp;style=flat-square"></a>
<a href="https://github.com/marcohefti/zero-context-lab/blob/main/go.mod"><img alt="Go Version" src="https://img.shields.io/github/go-mod/go-version/marcohefti/zero-context-lab?style=flat-square"></a>
<a href="#install"><img alt="Homebrew" src="https://img.shields.io/badge/homebrew-available-2e7d32?style=flat-square&amp;logo=homebrew"></a>
<a href="https://www.npmjs.com/package/@marcohefti/zcl"><img alt="npm" src="https://img.shields.io/npm/v/%40marcohefti/zcl?style=flat-square"></a>
<a href="LICENSE"><img alt="License" src="https://img.shields.io/github/license/marcohefti/zero-context-lab?style=flat-square"></a>

<img src="assets/brand/zcl-banner.png" width="720" alt="Zero Context Lab">

ZCL helps you build better agentic tools by testing them with real agents and turning each run into structured evidence.

You run a mission through ZCL, record the final result, and get a clear breakdown of what happened:
- how many turns/actions the agent needed
- how many tokens it used (when runner usage is available)
- where retries, timeouts, or friction happened

This makes it easier to improve command design, naming, and defaults so agents complete tasks faster with fewer mistakes.

## Design Contract

- Evidence comes from artifacts and traces.
- Scoring is runner-agnostic.
- Artifact shapes are deterministic and versioned.
- Captures are bounded and redacted by default.
- Operator workflows are JSON-first for automation.

## Quick Install

skills.sh (agent-first):

```bash
npx skills add marcohefti/zero-context-lab@zcl
```

## Install

Current install path (from source):

```bash
go build -o bin/zcl ./cmd/zcl
./install.sh --file ./bin/zcl
zcl version
```

Homebrew:

```bash
brew install marcohefti/zero-context-lab/zcl
```

npm:

```bash
npm i -g @marcohefti/zcl
```

Alternative (Go toolchain install):

```bash
go install github.com/marcohefti/zero-context-lab/cmd/zcl@latest
```

skills.sh:

```bash
npx skills add marcohefti/zero-context-lab@zcl
```

## Updates

ZCL does not auto-update at runtime.

Check update status:

```bash
zcl update status --json
zcl update status --cached --json
```

Update explicitly via your install path:

```bash
npm i -g @marcohefti/zcl@latest
brew upgrade marcohefti/zero-context-lab/zcl
go install github.com/marcohefti/zero-context-lab/cmd/zcl@latest
```

Agent harnesses can enforce a minimum installed version via:

```bash
export ZCL_MIN_VERSION=0.2.0
```

Interactive shells get at-most-once-per-day update notices by default.
Set `ZCL_DISABLE_UPDATE_NOTIFY=1` to silence notices, or `ZCL_ENABLE_UPDATE_NOTIFY=1` to force-enable.

`zcl` on skills.sh is synced via GitHub Actions: `.github/workflows/skills-sh.yml`.

## Quick Start (Single Attempt)

1. Initialize workspace:

```bash
zcl init
```

2. Allocate attempt and write an env file:

```bash
zcl attempt start \
  --suite smoke \
  --mission hello-world \
  --prompt "Run echo hello and finish with zcl feedback." \
  --isolation-model native_spawn \
  --env-file .zcl/current-attempt.env \
  --env-format sh \
  --json
```

3. Load attempt env:

```bash
source .zcl/current-attempt.env
```

4. Run actions through a funnel:

```bash
zcl run -- echo "hello"
```

5. Finish with canonical outcome:

```bash
zcl feedback --ok --result "HELLO=hello"
```

6. Compute + validate:

```bash
zcl attempt finish --strict --json
zcl attempt explain --json
```

## Quick Start (Suite)

Native host orchestration path (preferred when host supports native fresh session spawn):

```bash
zcl suite plan --file suite.yaml --json
```

Process-runner fallback path:

```bash
zcl suite run \
  --file suite.yaml \
  --feedback-policy auto_fail \
  --campaign-id smoke-campaign \
  --progress-jsonl .zcl/progress/smoke.jsonl \
  --session-isolation process \
  --json \
  -- <runner-cmd> [args...]
```

## Artifact Layout (Default)

Root: `.zcl/`

```txt
.zcl/
  runs/<runId>/
    run.json
    suite.json                  (optional snapshot)
    suite.run.summary.json      (optional)
    run.report.json             (optional)
    attempts/<attemptId>/
      attempt.json
      prompt.txt                (optional snapshot)
      tool.calls.jsonl          (primary evidence)
      feedback.json             (primary evidence)
      notes.jsonl               (optional)
      captures.jsonl            (optional)
      attempt.report.json       (computed)
      runner.ref.json           (optional)
      runner.metrics.json       (optional)
```

## Command Surface

Core commands:
- `zcl init`
- `zcl update status [--cached] [--json]`
- `zcl contract --json`
- `zcl attempt start|finish|explain|list|latest`
- `zcl suite plan|run`
- `zcl runs list`
- `zcl run`
- `zcl mcp proxy`
- `zcl http proxy`
- `zcl feedback`
- `zcl note`
- `zcl report`
- `zcl validate`
- `zcl expect`
- `zcl replay`
- `zcl doctor`
- `zcl gc`
- `zcl pin`
- `zcl enrich`

For machine-readable command + artifact contract:

```bash
zcl contract --json
```

## Operator FAQ

Find latest successful attempt for a mission:

```bash
zcl attempt latest --suite <suiteId> --mission <missionId> --status ok --json
```

List attempts/runs for automation dashboards:

```bash
zcl attempt list --suite <suiteId> --status any --limit 100 --json
zcl runs list --suite <suiteId> --json
```

Control missing feedback behavior:

```bash
# strict: missing feedback stays a hard failure
zcl suite run --file suite.yaml --feedback-policy strict --json -- <runner>

# auto_fail: synthesize canonical infra-failure feedback when runner exits early
zcl suite run --file suite.yaml --feedback-policy auto_fail --json -- <runner>
```

Stream live machine-readable status:

```bash
zcl suite run --file suite.yaml --progress-jsonl .zcl/progress/suite.jsonl --json -- <runner>
```

Canonical campaign continuity state:

```bash
zcl suite run --file suite.yaml --campaign-id <campaignId> --json -- <runner>
# writes .zcl/campaigns/<campaignId>/campaign.state.json by default
```

## Developer Flow

Single repo gate:

```bash
./scripts/verify.sh
```

This runs formatting/tests/vet + contract/docs/skills checks.

## Docs Map

- `PLAN.md` (execution checklist)
- `CONCEPT.md` (why + non-negotiables)
- `ARCHITECTURE.md` (system model + command map)
- `SCHEMAS.md` (exact v1 schemas and canonical IDs)
- `AGENTS.md` (operator workflow + builder index)
