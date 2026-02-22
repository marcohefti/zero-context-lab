---
name: zcl
description: Orchestrator workflow for running ZeroContext Lab (ZCL) attempts/suites with deterministic artifacts, trace-backed evidence, and fast post-mortems (shim support for "agent only types tool name").
---

# ZCL Orchestrator (Codex Skill)

This skill is for the orchestrator (you), not the spawned mission agent.

## Native Runtime Boundary (Current)

- ZCL has first-class native runtime execution in `zcl suite run` and campaign flows via `runner.type=codex_app_server`.
- Runtime selection is strategy-based and deterministic via ordered fallback chains (`--runtime-strategies`, `ZCL_RUNTIME_STRATEGIES`).
- Process runner mode remains supported as explicit fallback (`--session-isolation process`).
- Native runtime strategy health/failure classes are typed and surfaced in artifacts/JSON outputs.

## Capability Matrix

| Capability | Implemented | Enforced | Notes |
| --- | --- | --- | --- |
| Native suite execution (`--session-isolation native`) | yes | yes | `suite run` forbids process runner command in native mode. |
| Runtime strategy fallback chain | yes | yes | Ordered strategy IDs, capability checks, typed per-strategy failures. |
| Native campaign runner (`runner.type=codex_app_server`) | yes | yes | No per-flow `runner.command` required. |
| Native event trace mapping (`tool=native`) | yes | yes | Redaction + bounds + integrity flags preserved. |
| Mission-only result-channel finalization | yes | yes | 3-turn workflows via `minResultTurn`. |
| Provider onboarding structure | yes | partial | `provider_stub` demonstrates unsupported-capability behavior. |
| Runtime failure taxonomy (auth/rate-limit/stream/crash/listener) | yes | yes | Typed `ZCL_E_RUNTIME_*` codes in summaries + feedback failure payloads. |

Primary evidence:
- `.zcl/.../tool.calls.jsonl`
- `.zcl/.../feedback.json`

## Operator Invocation Story

When asked to "run this through ZCL", use this order:

1. `zcl init`
2. Optional preflight:
   - `zcl update status --json`
   - `zcl doctor --json`
3. Optional single-attempt allocation path:
   - `zcl attempt start --suite <suiteId> --mission <missionId> --prompt <text> --isolation-model native_spawn --json`
4. Preferred native suite path:
   - `zcl suite run --file <suite.(yaml|yml|json)> --session-isolation native --runtime-strategies codex_app_server --feedback-policy auto_fail --finalization-mode auto_from_result_json --result-channel file_json --result-min-turn 3 --campaign-id <campaignId> --progress-jsonl <path|-> --json`
5. Process fallback path:
   - `zcl suite run --file <suite.(yaml|yml|json)> --session-isolation process --feedback-policy auto_fail --finalization-mode auto_from_result_json --result-channel file_json --result-min-turn 3 --campaign-id <campaignId> --progress-jsonl <path|-> --shim tool-cli --json -- <runner-cmd> [args...]`
6. First-class campaign path:
   - `zcl campaign lint --spec <campaign.(yaml|yml|json)> --json`
   - `zcl campaign canary --spec <campaign.(yaml|yml|json)> --missions 3 --json`
   - `zcl campaign run --spec <campaign.(yaml|yml|json)> --json`
   - `zcl campaign resume --campaign-id <id> --json`
   - `zcl campaign status --campaign-id <id> --json`
   - `zcl campaign report --campaign-id <id> --json`
   - `zcl campaign publish-check --campaign-id <id> --json`
7. Validate/report from artifacts:
   - `zcl report --strict <attemptDir|runDir>`
   - `zcl validate --strict <attemptDir|runDir>`
   - `zcl attempt explain --json <attemptDir>`

Explicit finalization path for harness-aware prompts:
- `zcl feedback --ok --result <text>` or `zcl feedback --fail --result-json <json>`

## Campaign Guidance

Recommended native Codex flow:

```yaml
flows:
  - flowId: codex-native
    runner:
      type: codex_app_server
      sessionIsolation: native
      runtimeStrategies: ["codex_app_server"]
      feedbackPolicy: auto_fail
      freshAgentPerAttempt: true
```

Mission-only recommended variant:

```yaml
promptMode: mission_only
flows:
  - flowId: codex-native
    runner:
      type: codex_app_server
      sessionIsolation: native
      runtimeStrategies: ["codex_app_server"]
      finalization:
        mode: auto_from_result_json
        minResultTurn: 3
        resultChannel:
          kind: file_json
          path: mission.result.json
```

## Prompt Policy

Use exactly one mode per campaign:

- `promptMode: mission_only` (preferred): mission intent + output contract only; no harness terms.
- `promptMode: default`: harness-aware prompts permitted.

## Native Recommendation Criteria (Measured)

Codex native runtime is recommended when both hold in CI/nightly checks:

1. Reliability: native 20-attempt parallel smoke run success rate >= 95%.
2. Throughput: same run completes in <= 30 seconds on CI worker baseline.

Guard test path:
- `internal/cli/suite_run_integration_test.go` (`TestSuiteRun_NativeParallelUniqueSessions`, `TestSuiteRun_NativeSchedulerRateLimitIsDeterministic`).

## Templates

- `examples/campaign.canonical.yaml`
- `examples/campaign.no-context.comparison.yaml`
- `examples/campaign.no-context.codex-exec.yaml`
- `examples/campaign.no-context.codex-subagent.yaml`
- `examples/campaign.no-context.claude-subagent.yaml`
- `examples/campaign.native.codex.minimal.yaml`
- `examples/campaign.native.codex.advanced.yaml`
- `docs/migration/shell-adapter-to-native-codex.md`
