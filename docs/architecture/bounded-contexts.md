# Bounded Contexts + Layering (DDD-ish)

## Problem
As ZCL grows, it’s easy to accidentally mix concerns:
- CLI/orchestration code pulling in low-level adapters directly (“god package”).
- Domain rules depending on file I/O helpers.
- One context reaching into another context’s internals instead of using explicit contracts.

This is especially risky in a harness: iteration speed is high, and throughput creates entropy.

## Design Goals
- **Clear bounded contexts** so the codebase stays legible under constant change.
- **Layered architecture per context** (domain/app/ports/infra) with enforced import directions.
- **Mechanical enforcement** via a repo script so drift fails fast in CI.
- **Clean-slate friendly**: breaking changes are acceptable; we optimize for future maintainability.

## Non-goals
- Perfect textbook DDD purity. ZCL is a CLI harness; some “domain” is inherently file/artifact-shaped.
- Excessive micro-packages. Packages should be meaningful boundaries, not ceremony.

## Context Map (v1)
ZCL code is organized into these bounded contexts:
- `spec`: suite/campaign spec parsing, mission selection/materialization.
- `execution`: run/attempt lifecycle, campaign/suite orchestration state transitions.
- `evidence`: funnels, trace emission, feedback/notes capture, redaction/bounds policy.
- `evaluation`: validate/report/expect/semantic/oracle judgment.
- `runtime`: native runtime abstractions, strategy resolution, provider adapters, enrich ingestion.
- `ops`: update/doctor/gc/pin/config policies.

Shared kernel (tiny, stable):
- `kernel`: IDs + error codes (and other *truly universal* primitives).

## Layering Rules (per context)
Each context may have these layers:
- `domain`: invariants, pure rules, value objects (no adapters, no OS).
- `ports`: interfaces/DTOs that other contexts may depend on (no adapters).
- `app`: use-cases/orchestration for that context (depends on domain+ports).
- `infra`: adapters (file I/O, process execution, network clients, etc.).

### Allowed Imports (high level)
- `domain` may import: `kernel`, same-context `domain`.
- `ports` may import: `kernel`, same-context `domain`/`ports`.
- `app` may import: `kernel`, same-context `domain`/`ports`/`app`, **other-context `ports` only**.
- `infra` may import: `kernel`, same-context any layer, **other-context `ports` only**.
- `interfaces/*` (CLI) may import: `kernel`, any context `app`/`ports`.
- `bootstrap` may wire `infra` into `app` (composition root).

Hard rules:
- No cross-context imports into another context’s `domain`/`app`/`infra`.
- Cross-context dependencies must go through `ports`.

## Validation (Enforcement)
The boundary rules are enforced by `scripts/arch-boundaries-check.sh` (wired into `./scripts/verify.sh`).

Migration strategy:
- All internal Go packages must live under `internal/kernel/*`, `internal/interfaces/*`, or `internal/contexts/<ctx>/{domain,ports,app,infra}/*`.
- `scripts/arch-coverage-check.sh` fails fast if a legacy/unknown `internal/*` package appears.
