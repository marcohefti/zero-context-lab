# Native Runtime Architecture

This doc describes runtime strategy selection and native execution invariants.

## Strategy Model

- Strategy IDs are explicit (`codex_app_server`, `provider_stub`, ...).
- Resolver input is an ordered strategy chain.
- Resolver checks:
  1. strategy exists in registry
  2. strategy capabilities satisfy required set
  3. strategy probe passes
- First passing strategy is selected; all failures are retained for diagnostics.

## Capability Contract

Canonical capability keys:
- `supports_thread_start`
- `supports_turn_steer`
- `supports_interrupt`
- `supports_event_stream`
- `supports_parallel_sessions`

## Execution Invariants

- One fresh runtime session per attempt in native suite mode.
- Session/thread identifiers are persisted in `runner.ref.json`.
- Native `thread/start` can be pinned per flow via campaign runner fields (`model`, `modelReasoningEffort`, `modelReasoningPolicy`).
- Native events are mapped into canonical `tool.calls.jsonl` (`tool=native`) with bounds/redaction.
- Missing/partial native event streams set integrity flags and typed failure codes.

## Backpressure + Scheduling

Controls:
- `ZCL_NATIVE_MAX_INFLIGHT_PER_STRATEGY`
- `ZCL_NATIVE_MIN_START_INTERVAL_MS`

These are deterministic per strategy and apply before session startup.

## Failure Mapping

Native runtime failures map into `ZCL_E_RUNTIME_*` codes including:
- compatibility, startup, transport, protocol, timeout
- stream_disconnect, crash, auth, rate_limit, listener_failure, env_policy

## Onboarding a New Provider

1. Implement runtime/session interfaces.
2. Publish capabilities in contract (`runtimeSchema`).
3. Add adapter tests + conformance tests.
4. Add docs for unsupported capability/API gaps.
5. Register adapter in native runtime registry and catalog.

`provider_stub` is the reference skeleton for unsupported capability handling.
