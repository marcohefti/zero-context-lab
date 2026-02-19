# ZCL Architecture (Deep Dives)

`ARCHITECTURE.md` (repo root) is the high-level overview and command map.
This folder is where detailed subsystem docs live so the core architecture stays readable and doesn't rot.

Each deep dive should follow a consistent structure:
- Problem (what breaks / what operators feel)
- Design goals (measurable)
- Non-goals (explicit constraints)
- Where the logic lives (file pointers)
- Runtime flow (step-by-step)
- Invariants / guardrails (what must always be true)
- Observability (what we emit so failures are explainable)
- Testing expectations (what tests pin behavior)

## Deep Dives

- `docs/architecture/suite-run.md`
  - End-to-end suite orchestration (`zcl suite run`): capability guard -> process runner per mission -> finish/validate/expect.
- `docs/architecture/evidence-pipeline.md`
  - Attempt lifecycle and the evidence-first contract (trace + feedback), including strict vs discovery behavior.
- `docs/architecture/write-safety.md`
  - Atomic JSON writes, safe JSONL appends/locking, bounds, containment.
