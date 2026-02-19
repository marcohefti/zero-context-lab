# Zero Context Lab (ZCL)

[![Release](https://img.shields.io/github/v/release/marcohefti/zero-context-lab?sort=semver&style=flat-square)](https://github.com/marcohefti/zero-context-lab/releases)
[![CI](https://img.shields.io/github/actions/workflow/status/marcohefti/zero-context-lab/ci.yml?branch=main&label=ci&style=flat-square)](https://github.com/marcohefti/zero-context-lab/actions/workflows/ci.yml)
[![Entropy Guard](https://img.shields.io/github/actions/workflow/status/marcohefti/zero-context-lab/entropy-guard.yml?branch=main&label=entropy%20guard&style=flat-square)](https://github.com/marcohefti/zero-context-lab/actions/workflows/entropy-guard.yml)
[![Go Version](https://img.shields.io/github/go-mod/go-version/marcohefti/zero-context-lab?style=flat-square)](https://github.com/marcohefti/zero-context-lab/blob/main/go.mod)
[![Homebrew (planned)](https://img.shields.io/badge/homebrew-planned-lightgrey?style=flat-square&logo=homebrew)](#install)
[![npm (planned)](https://img.shields.io/badge/npm-planned-lightgrey?style=flat-square&logo=npm)](#install)

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

## Install

Current install path (from source):

```bash
go build -o bin/zcl ./cmd/zcl
./install.sh --file ./bin/zcl
zcl version
```

Alternative (Go toolchain install):

```bash
go install github.com/marcohefti/zero-context-lab/cmd/zcl@latest
```

Planned distribution channels (not published yet):
- Homebrew formula/tap
- npm wrapper package

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
- `zcl contract --json`
- `zcl attempt start|finish|explain`
- `zcl suite plan|run`
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
