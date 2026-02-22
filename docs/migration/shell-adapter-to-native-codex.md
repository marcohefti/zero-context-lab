# Migration: Shell Adapter Campaign -> Native Codex Runtime

This guide migrates a flow from shell adapter execution (`runner.command`) to first-class native runtime execution (`runner.type=codex_app_server`).

## Why Migrate

- Removes per-flow shell wrapper scripts.
- Uses typed runtime strategy selection with fallback chain support.
- Preserves canonical artifacts (`tool.calls.jsonl`, `feedback.json`, `attempt.report.json`) and campaign gates.

## Before (Process Adapter)

```yaml
flows:
  - flowId: codex-flow
    runner:
      type: codex_exec
      command: ["codex", "exec", "--", "./scripts/runner_codex.sh"]
      sessionIsolation: process
      feedbackPolicy: auto_fail
```

## After (Native Runtime)

```yaml
flows:
  - flowId: codex-flow
    runner:
      type: codex_app_server
      sessionIsolation: native
      runtimeStrategies: ["codex_app_server"]
      feedbackPolicy: auto_fail
      freshAgentPerAttempt: true
      finalization:
        mode: auto_fail
        resultChannel:
          kind: none
```

## Required Checks

1. `zcl campaign lint --spec <campaign.yaml> --json`
2. `zcl campaign doctor --spec <campaign.yaml> --json`
3. `zcl campaign canary --spec <campaign.yaml> --missions 3 --json`
4. `zcl campaign run --spec <campaign.yaml> --json`
5. `zcl campaign publish-check --campaign-id <id> --json`

## Mission-Only Recommended Variant

For no-context workflows:

- `promptMode: mission_only`
- `runner.finalization.mode: auto_from_result_json`
- `runner.finalization.resultChannel.kind: file_json`
- `runner.finalization.minResultTurn: 3`

This removes harness instructions from mission prompts while preserving deterministic finalization.

## Rollback

Switch flow back to process mode by restoring `runner.command`, setting `runner.type` to `codex_exec|codex_subagent`, and `sessionIsolation: process`.
