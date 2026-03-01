package campaign

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/marcohefti/zero-context-lab/internal/suite"
)

func TestParseSpecFile_ResolvesRelativePathsDefaultsAndSelection(t *testing.T) {
	dir := t.TempDir()
	suitePath := filepath.Join(dir, "suite.json")
	if err := os.WriteFile(suitePath, []byte(`{
  "version": 1,
  "suiteId": "suite-a",
  "missions": [
    { "missionId": "m1", "prompt": "p1" },
    { "missionId": "m2", "prompt": "p2" },
    { "missionId": "m3", "prompt": "p3" }
  ]
}`), 0o644); err != nil {
		t.Fatalf("write suite: %v", err)
	}
	specPath := filepath.Join(dir, "campaign.yaml")
	if err := os.WriteFile(specPath, []byte(`
schemaVersion: 1
campaignId: cmp-main
missionSource:
  path: missions/
  selection:
    mode: range
    range:
      start: 1
      end: 2
semantic:
  enabled: true
  rulesPath: rules.yaml
output:
  reportPath: out/report.json
timeouts:
  defaultAttemptTimeoutMs: 1234
pairGate:
  stopOnFirstMissionFailure: true
flows:
  - flowId: flow-a
    suiteFile: suite.json
    runner:
      type: process_cmd
      command: ["bash","-lc","echo ok"]
`), 0o644); err != nil {
		t.Fatalf("write spec: %v", err)
	}

	ps, err := ParseSpecFile(specPath)
	if err != nil {
		t.Fatalf("ParseSpecFile: %v", err)
	}
	absSpec, _ := filepath.Abs(specPath)
	if ps.SpecPath != absSpec {
		t.Fatalf("expected absolute specPath, got %q want %q", ps.SpecPath, absSpec)
	}
	absSuite, _ := filepath.Abs(suitePath)
	if ps.Spec.Flows[0].SuiteFile != absSuite {
		t.Fatalf("expected absolute suite path, got %q want %q", ps.Spec.Flows[0].SuiteFile, absSuite)
	}
	if ps.Spec.Semantic.RulesPath != filepath.Join(dir, "rules.yaml") {
		t.Fatalf("expected rulesPath resolved relative to spec dir, got %q", ps.Spec.Semantic.RulesPath)
	}
	if ps.Spec.MissionSource.Path != filepath.Join(dir, "missions") {
		t.Fatalf("expected missionSource.path resolved relative to spec dir, got %q", ps.Spec.MissionSource.Path)
	}
	if ps.Spec.Output.ReportPath != filepath.Join(dir, "out", "report.json") {
		t.Fatalf("expected reportPath resolved relative to spec dir, got %q", ps.Spec.Output.ReportPath)
	}
	if !ps.Spec.PairGateEnabled() {
		t.Fatalf("expected pairGate enabled default true")
	}
	if ps.Spec.Flows[0].Runner.TimeoutMs != 1234 {
		t.Fatalf("expected flow timeout inherited from defaults, got %d", ps.Spec.Flows[0].Runner.TimeoutMs)
	}
	if !reflect.DeepEqual(ps.MissionIndexes, []int{1, 2}) {
		t.Fatalf("unexpected mission indexes: %+v", ps.MissionIndexes)
	}
	if ps.Spec.TotalMissions != 2 {
		t.Fatalf("expected totalMissions defaulted to selected count 2, got %d", ps.Spec.TotalMissions)
	}
}

func TestParseSpecFile_RejectsInvalidRunnerType(t *testing.T) {
	dir := t.TempDir()
	suitePath := filepath.Join(dir, "suite.json")
	if err := os.WriteFile(suitePath, []byte(`{
  "version": 1,
  "suiteId": "suite-a",
  "missions": [
    { "missionId": "m1", "prompt": "p1" }
  ]
}`), 0o644); err != nil {
		t.Fatalf("write suite: %v", err)
	}
	specPath := filepath.Join(dir, "campaign.json")
	if err := os.WriteFile(specPath, []byte(`{
  "schemaVersion": 1,
  "campaignId": "cmp-main",
  "flows": [
    {
      "flowId": "flow-a",
      "suiteFile": "suite.json",
      "runner": {
        "type": "unknown",
        "command": ["echo", "ok"]
      }
    }
  ]
}`), 0o644); err != nil {
		t.Fatalf("write spec: %v", err)
	}
	if _, err := ParseSpecFile(specPath); err == nil {
		t.Fatalf("expected error for invalid runner.type")
	}
}

func TestParseSpecFile_StrictUnknownFieldsAndXExtensions(t *testing.T) {
	dir := t.TempDir()
	suitePath := filepath.Join(dir, "suite.json")
	if err := os.WriteFile(suitePath, []byte(`{
  "version": 1,
  "suiteId": "suite-a",
  "missions": [
    { "missionId": "m1", "prompt": "p1" }
  ]
}`), 0o644); err != nil {
		t.Fatalf("write suite: %v", err)
	}

	t.Run("json unknown field fails", func(t *testing.T) {
		specPath := filepath.Join(dir, "bad.json")
		if err := os.WriteFile(specPath, []byte(`{
  "schemaVersion": 1,
  "campaignId": "cmp-main",
  "unknownField": true,
  "flows": [{"flowId":"flow-a","suiteFile":"suite.json","runner":{"type":"process_cmd","command":["echo","ok"]}}]
}`), 0o644); err != nil {
			t.Fatalf("write spec: %v", err)
		}
		if _, err := ParseSpecFile(specPath); err == nil {
			t.Fatalf("expected unknown-field parse error")
		}
	})

	t.Run("yaml x extension allowed", func(t *testing.T) {
		specPath := filepath.Join(dir, "ext.yaml")
		if err := os.WriteFile(specPath, []byte(`
schemaVersion: 1
campaignId: cmp-main
x-note:
  owner: ops
flows:
  - flowId: flow-a
    suiteFile: suite.json
    runner:
      type: process_cmd
      command: ["echo","ok"]
`), 0o644); err != nil {
			t.Fatalf("write spec: %v", err)
		}
		ps, err := ParseSpecFile(specPath)
		if err != nil {
			t.Fatalf("ParseSpecFile: %v", err)
		}
		if ps.Spec.Extensions["x-note"] == nil {
			t.Fatalf("expected x-note extension captured")
		}
	})
}

func TestParseSpecFile_MissionOrderParity(t *testing.T) {
	dir := t.TempDir()
	suiteA := filepath.Join(dir, "suite-a.json")
	suiteB := filepath.Join(dir, "suite-b.json")
	if err := os.WriteFile(suiteA, []byte(`{
  "version": 1,
  "suiteId": "suite-a",
  "missions": [
    { "missionId": "m1", "prompt": "p1" },
    { "missionId": "m2", "prompt": "p2" }
  ]
}`), 0o644); err != nil {
		t.Fatalf("write suite-a: %v", err)
	}
	if err := os.WriteFile(suiteB, []byte(`{
  "version": 1,
  "suiteId": "suite-b",
  "missions": [
    { "missionId": "m2", "prompt": "p2" },
    { "missionId": "m1", "prompt": "p1" }
  ]
}`), 0o644); err != nil {
		t.Fatalf("write suite-b: %v", err)
	}
	specPath := filepath.Join(dir, "campaign.yaml")
	if err := os.WriteFile(specPath, []byte(`
schemaVersion: 1
campaignId: cmp-main
flows:
  - flowId: flow-a
    suiteFile: suite-a.json
    runner:
      type: process_cmd
      command: ["echo","ok"]
  - flowId: flow-b
    suiteFile: suite-b.json
    runner:
      type: process_cmd
      command: ["echo","ok"]
`), 0o644); err != nil {
		t.Fatalf("write spec: %v", err)
	}
	_, err := ParseSpecFile(specPath)
	if err == nil {
		t.Fatalf("expected mission order mismatch error")
	}
	if !strings.Contains(err.Error(), "mission order mismatch") {
		t.Fatalf("expected mission order mismatch error, got %v", err)
	}
}

func TestResolveMissionIndexesAndWindow(t *testing.T) {
	sf := suite.SuiteFileV1{Missions: []suite.MissionV1{
		{MissionID: "m1"},
		{MissionID: "m2"},
		{MissionID: "m3"},
	}}
	idx, err := ResolveMissionIndexes(sf, MissionSelectionSpec{Mode: SelectionModeMissionID, MissionIDs: []string{"m3", "m1", "m3"}})
	if err != nil {
		t.Fatalf("ResolveMissionIndexes: %v", err)
	}
	if !reflect.DeepEqual(idx, []int{2, 0}) {
		t.Fatalf("unexpected indexes: %+v", idx)
	}
	win, err := WindowMissionIndexes(idx, 1, 1)
	if err != nil {
		t.Fatalf("WindowMissionIndexes: %v", err)
	}
	if !reflect.DeepEqual(win, []int{0}) {
		t.Fatalf("unexpected window: %+v", win)
	}
}

func TestParseSpecFile_PairGateDisable(t *testing.T) {
	dir := t.TempDir()
	suitePath := filepath.Join(dir, "suite.json")
	if err := os.WriteFile(suitePath, []byte(`{
  "version": 1,
  "suiteId": "suite-a",
  "missions": [
    { "missionId": "m1", "prompt": "p1" }
  ]
}`), 0o644); err != nil {
		t.Fatalf("write suite: %v", err)
	}
	specPath := filepath.Join(dir, "campaign.yaml")
	if err := os.WriteFile(specPath, []byte(`
schemaVersion: 1
campaignId: cmp-main
pairGate:
  enabled: false
flows:
  - flowId: flow-a
    suiteFile: suite.json
    runner:
      type: process_cmd
      command: ["echo","ok"]
`), 0o644); err != nil {
		t.Fatalf("write spec: %v", err)
	}
	ps, err := ParseSpecFile(specPath)
	if err != nil {
		t.Fatalf("ParseSpecFile: %v", err)
	}
	if ps.Spec.PairGateEnabled() {
		t.Fatalf("expected pairGate enabled=false to be honored")
	}
}

func TestParseSpecFile_MissionPackWithoutSuiteFile(t *testing.T) {
	dir := t.TempDir()
	missionDir := filepath.Join(dir, "missions")
	if err := os.MkdirAll(missionDir, 0o755); err != nil {
		t.Fatalf("mkdir missions: %v", err)
	}
	if err := os.WriteFile(filepath.Join(missionDir, "01-login.md"), []byte("log in"), 0o644); err != nil {
		t.Fatalf("write mission 1: %v", err)
	}
	if err := os.WriteFile(filepath.Join(missionDir, "02-search.md"), []byte("search"), 0o644); err != nil {
		t.Fatalf("write mission 2: %v", err)
	}
	specPath := filepath.Join(dir, "campaign.yaml")
	if err := os.WriteFile(specPath, []byte(`
schemaVersion: 1
campaignId: cmp-pack
missionSource:
  path: missions
flows:
  - flowId: flow-a
    runner:
      type: process_cmd
      command: ["echo","ok"]
`), 0o644); err != nil {
		t.Fatalf("write spec: %v", err)
	}
	ps, err := ParseSpecFile(specPath)
	if err != nil {
		t.Fatalf("ParseSpecFile: %v", err)
	}
	if len(ps.FlowSuites["flow-a"].Suite.Missions) != 2 {
		t.Fatalf("expected mission-pack suite with 2 missions, got %d", len(ps.FlowSuites["flow-a"].Suite.Missions))
	}
	if ps.FlowSuites["flow-a"].Suite.Missions[0].MissionID != "01-login" {
		t.Fatalf("unexpected mission ordering: %+v", ps.FlowSuites["flow-a"].Suite.Missions)
	}
	if !reflect.DeepEqual(ps.MissionIndexes, []int{0, 1}) {
		t.Fatalf("unexpected mission indexes: %+v", ps.MissionIndexes)
	}
}

func TestParseSpecFile_TraceProfileAndFlowMode(t *testing.T) {
	dir := t.TempDir()
	suitePath := filepath.Join(dir, "suite.json")
	if err := os.WriteFile(suitePath, []byte(`{
  "version": 1,
  "suiteId": "suite-a",
  "missions": [
    { "missionId": "m1", "prompt": "p1" }
  ]
}`), 0o644); err != nil {
		t.Fatalf("write suite: %v", err)
	}
	specPath := filepath.Join(dir, "campaign.yaml")
	if err := os.WriteFile(specPath, []byte(`
schemaVersion: 1
campaignId: cmp-mode
execution:
  flowMode: parallel
pairGate:
  traceProfile: mcp_required
flows:
  - flowId: flow-a
    suiteFile: suite.json
    runner:
      type: process_cmd
      command: ["echo","ok"]
`), 0o644); err != nil {
		t.Fatalf("write spec: %v", err)
	}
	ps, err := ParseSpecFile(specPath)
	if err != nil {
		t.Fatalf("ParseSpecFile: %v", err)
	}
	if ps.Spec.Execution.FlowMode != FlowModeParallel {
		t.Fatalf("expected parallel flow mode, got %q", ps.Spec.Execution.FlowMode)
	}
	if ps.Spec.PairGate.TraceProfile != TraceProfileMCPRequired {
		t.Fatalf("expected trace profile mcp_required, got %q", ps.Spec.PairGate.TraceProfile)
	}
}

func TestParseSpecFile_MissionOnlyRejectsHarnessPromptTerms(t *testing.T) {
	dir := t.TempDir()
	suitePath := filepath.Join(dir, "suite.json")
	if err := os.WriteFile(suitePath, []byte(`{
  "version": 1,
  "suiteId": "suite-a",
  "missions": [
    { "missionId": "m1", "prompt": "Do task then call zcl feedback with result." }
  ]
}`), 0o644); err != nil {
		t.Fatalf("write suite: %v", err)
	}
	specPath := filepath.Join(dir, "campaign.yaml")
	if err := os.WriteFile(specPath, []byte(`
schemaVersion: 1
campaignId: cmp-mission-only
promptMode: mission_only
flows:
  - flowId: flow-a
    suiteFile: suite.json
    runner:
      type: process_cmd
      command: ["echo","ok"]
      toolDriver:
        kind: shell
      finalization:
        mode: auto_from_result_json
        resultChannel:
          kind: file_json
`), 0o644); err != nil {
		t.Fatalf("write spec: %v", err)
	}
	if _, err := ParseSpecFile(specPath); err == nil {
		t.Fatalf("expected mission-only prompt contamination error")
	} else {
		var promptErr *PromptModeViolationError
		if !errors.As(err, &promptErr) {
			t.Fatalf("expected typed prompt violation error, got %v", err)
		}
		if promptErr.PromptMode != PromptModeMissionOnly {
			t.Fatalf("expected prompt mode %q, got %q", PromptModeMissionOnly, promptErr.PromptMode)
		}
		if len(promptErr.Violations) != 1 || promptErr.Violations[0].Term != "zcl feedback" {
			t.Fatalf("unexpected prompt violations: %+v", promptErr.Violations)
		}
	}
}

func TestParseSpecFile_MissionOnlyRequiresAutoResultFinalization(t *testing.T) {
	dir := t.TempDir()
	suitePath := filepath.Join(dir, "suite.json")
	if err := os.WriteFile(suitePath, []byte(`{
  "version": 1,
  "suiteId": "suite-a",
  "missions": [
    { "missionId": "m1", "prompt": "Solve mission and return proof." }
  ]
}`), 0o644); err != nil {
		t.Fatalf("write suite: %v", err)
	}
	specPath := filepath.Join(dir, "campaign.yaml")
	if err := os.WriteFile(specPath, []byte(`
schemaVersion: 1
campaignId: cmp-mission-only
promptMode: mission_only
flows:
  - flowId: flow-a
    suiteFile: suite.json
    runner:
      type: process_cmd
      command: ["echo","ok"]
      finalization:
        mode: auto_fail
`), 0o644); err != nil {
		t.Fatalf("write spec: %v", err)
	}
	if _, err := ParseSpecFile(specPath); err == nil || !strings.Contains(err.Error(), "promptMode=mission_only requires runner.finalization.mode=auto_from_result_json") {
		t.Fatalf("expected mission-only finalization error, got %v", err)
	}
}

func TestParseSpecFile_MissionOnlyCLIFunnelRequiresShimsTyped(t *testing.T) {
	dir := t.TempDir()
	suitePath := filepath.Join(dir, "suite.json")
	if err := os.WriteFile(suitePath, []byte(`{
  "version": 1,
  "suiteId": "suite-a",
  "missions": [
    { "missionId": "m1", "prompt": "Solve mission and return proof." }
  ]
}`), 0o644); err != nil {
		t.Fatalf("write suite: %v", err)
	}
	specPath := filepath.Join(dir, "campaign.yaml")
	if err := os.WriteFile(specPath, []byte(`
schemaVersion: 1
campaignId: cmp-mission-only
promptMode: mission_only
flows:
  - flowId: flow-a
    suiteFile: suite.json
    runner:
      type: process_cmd
      command: ["echo","ok"]
      toolDriver:
        kind: cli_funnel
      finalization:
        mode: auto_from_result_json
        resultChannel:
          kind: file_json
`), 0o644); err != nil {
		t.Fatalf("write spec: %v", err)
	}
	if _, err := ParseSpecFile(specPath); err == nil {
		t.Fatalf("expected cli_funnel shim requirement error")
	} else {
		var shimErr *ToolDriverShimRequirementError
		if !errors.As(err, &shimErr) {
			t.Fatalf("expected typed shim requirement error, got %v", err)
		}
		if shimErr.Code != ReasonToolDriverShim {
			t.Fatalf("expected code %q, got %q", ReasonToolDriverShim, shimErr.Code)
		}
		if shimErr.Violation.FlowID != "flow-a" {
			t.Fatalf("expected flow-a, got %+v", shimErr.Violation)
		}
		if !strings.Contains(shimErr.Violation.Snippet, "runner.toolDriver.shims") {
			t.Fatalf("expected actionable snippet, got %+v", shimErr.Violation)
		}
	}
}

func TestParseSpecFile_FinalizationAndToolDriverNormalization(t *testing.T) {
	dir := t.TempDir()
	suitePath := filepath.Join(dir, "suite.json")
	if err := os.WriteFile(suitePath, []byte(`{
  "version": 1,
  "suiteId": "suite-a",
  "missions": [
    { "missionId": "m1", "prompt": "Solve mission and return proof." }
  ]
}`), 0o644); err != nil {
		t.Fatalf("write suite: %v", err)
	}
	specPath := filepath.Join(dir, "campaign.yaml")
	if err := os.WriteFile(specPath, []byte(`
schemaVersion: 1
campaignId: cmp-driver
flows:
  - flowId: flow-a
    suiteFile: suite.json
    runner:
      type: process_cmd
      command: ["echo","ok"]
      shims: ["tool-a"]
      toolDriver:
        kind: cli_funnel
        shims: ["tool-b"]
      finalization:
        mode: auto_from_result_json
        resultChannel:
          kind: stdout_json
`), 0o644); err != nil {
		t.Fatalf("write spec: %v", err)
	}
	ps, err := ParseSpecFile(specPath)
	if err != nil {
		t.Fatalf("ParseSpecFile: %v", err)
	}
	gotShims := ps.Spec.Flows[0].Runner.Shims
	if !reflect.DeepEqual(gotShims, []string{"tool-a", "tool-b"}) {
		t.Fatalf("expected merged shims, got %+v", gotShims)
	}
	if ps.Spec.Flows[0].Runner.Finalization.Mode != FinalizationModeAutoFromResultJSON {
		t.Fatalf("expected finalization mode auto_from_result_json, got %q", ps.Spec.Flows[0].Runner.Finalization.Mode)
	}
	if ps.Spec.Flows[0].Runner.Finalization.ResultChannel.Kind != ResultChannelStdoutJSON {
		t.Fatalf("expected stdout_json result channel, got %q", ps.Spec.Flows[0].Runner.Finalization.ResultChannel.Kind)
	}
	if ps.Spec.Flows[0].Runner.Finalization.ResultChannel.Marker != DefaultResultChannelMarker {
		t.Fatalf("expected default stdout marker, got %q", ps.Spec.Flows[0].Runner.Finalization.ResultChannel.Marker)
	}
	if ps.Spec.Flows[0].Runner.Finalization.MinResultTurn != DefaultMinResultTurn {
		t.Fatalf("expected default min result turn %d, got %d", DefaultMinResultTurn, ps.Spec.Flows[0].Runner.Finalization.MinResultTurn)
	}
}

func TestParseSpecFile_NativeModelAndReasoningDefaults(t *testing.T) {
	dir := t.TempDir()
	suitePath := filepath.Join(dir, "suite.json")
	if err := os.WriteFile(suitePath, []byte(`{
  "version": 1,
  "suiteId": "suite-a",
  "missions": [
    { "missionId": "m1", "prompt": "Solve mission and return proof." }
  ]
}`), 0o644); err != nil {
		t.Fatalf("write suite: %v", err)
	}
	specPath := filepath.Join(dir, "campaign.yaml")
	if err := os.WriteFile(specPath, []byte(`
schemaVersion: 1
campaignId: cmp-native-model
flows:
  - flowId: flow-a
    suiteFile: suite.json
    runner:
      type: codex_app_server
      model: " gpt-5.3-codex-spark "
      modelReasoningEffort: " MEDIUM "
`), 0o644); err != nil {
		t.Fatalf("write spec: %v", err)
	}
	ps, err := ParseSpecFile(specPath)
	if err != nil {
		t.Fatalf("ParseSpecFile: %v", err)
	}
	if ps.Spec.Flows[0].Runner.Model != "gpt-5.3-codex-spark" {
		t.Fatalf("expected trimmed model, got %q", ps.Spec.Flows[0].Runner.Model)
	}
	if ps.Spec.Flows[0].Runner.ModelReasoningEffort != ModelReasoningEffortMedium {
		t.Fatalf("expected normalized reasoning effort, got %q", ps.Spec.Flows[0].Runner.ModelReasoningEffort)
	}
	if ps.Spec.Flows[0].Runner.ModelReasoningPolicy != ModelReasoningPolicyBestEffort {
		t.Fatalf("expected default reasoning policy %q, got %q", ModelReasoningPolicyBestEffort, ps.Spec.Flows[0].Runner.ModelReasoningPolicy)
	}
}

func TestParseSpecFile_ModelReasoningPolicyRequiresEffort(t *testing.T) {
	dir := t.TempDir()
	suitePath := filepath.Join(dir, "suite.json")
	if err := os.WriteFile(suitePath, []byte(`{
  "version": 1,
  "suiteId": "suite-a",
  "missions": [
    { "missionId": "m1", "prompt": "p1" }
  ]
}`), 0o644); err != nil {
		t.Fatalf("write suite: %v", err)
	}
	specPath := filepath.Join(dir, "campaign.yaml")
	if err := os.WriteFile(specPath, []byte(`
schemaVersion: 1
campaignId: cmp-native-model
flows:
  - flowId: flow-a
    suiteFile: suite.json
    runner:
      type: codex_app_server
      modelReasoningPolicy: required
`), 0o644); err != nil {
		t.Fatalf("write spec: %v", err)
	}
	if _, err := ParseSpecFile(specPath); err == nil || !strings.Contains(err.Error(), "runner.modelReasoningPolicy requires runner.modelReasoningEffort") {
		t.Fatalf("expected modelReasoningPolicy validation error, got %v", err)
	}
}

func TestParseSpecFile_ModelFieldsRequireCodexAppServer(t *testing.T) {
	dir := t.TempDir()
	suitePath := filepath.Join(dir, "suite.json")
	if err := os.WriteFile(suitePath, []byte(`{
  "version": 1,
  "suiteId": "suite-a",
  "missions": [
    { "missionId": "m1", "prompt": "p1" }
  ]
}`), 0o644); err != nil {
		t.Fatalf("write suite: %v", err)
	}
	specPath := filepath.Join(dir, "campaign.yaml")
	if err := os.WriteFile(specPath, []byte(`
schemaVersion: 1
campaignId: cmp-native-model
flows:
  - flowId: flow-a
    suiteFile: suite.json
    runner:
      type: process_cmd
      command: ["echo","ok"]
      model: gpt-5.3-codex-spark
`), 0o644); err != nil {
		t.Fatalf("write spec: %v", err)
	}
	if _, err := ParseSpecFile(specPath); err == nil || !strings.Contains(err.Error(), "supported only for runner.type=codex_app_server") {
		t.Fatalf("expected runner.type validation error, got %v", err)
	}
}

func TestParseSpecFile_RunnerCwdDefaults(t *testing.T) {
	dir := t.TempDir()
	suitePath := filepath.Join(dir, "suite.json")
	if err := os.WriteFile(suitePath, []byte(`{
  "version": 1,
  "suiteId": "suite-a",
  "missions": [
    { "missionId": "m1", "prompt": "p1" }
  ]
}`), 0o644); err != nil {
		t.Fatalf("write suite: %v", err)
	}
	specPath := filepath.Join(dir, "campaign.yaml")
	if err := os.WriteFile(specPath, []byte(`
schemaVersion: 1
campaignId: cmp-runner-cwd-defaults
flows:
  - flowId: flow-a
    suiteFile: suite.json
    runner:
      type: codex_app_server
`), 0o644); err != nil {
		t.Fatalf("write spec: %v", err)
	}
	ps, err := ParseSpecFile(specPath)
	if err != nil {
		t.Fatalf("ParseSpecFile: %v", err)
	}
	got := ps.Spec.Flows[0].Runner.Cwd
	if got.Mode != RunnerCwdModeInherit || got.Retain != RunnerCwdRetainNever || got.BasePath != "" {
		t.Fatalf("unexpected runner cwd defaults: %+v", got)
	}
}

func TestParseSpecFile_RunnerCwdTempEmptyPerAttemptNormalized(t *testing.T) {
	dir := t.TempDir()
	suitePath := filepath.Join(dir, "suite.json")
	if err := os.WriteFile(suitePath, []byte(`{
  "version": 1,
  "suiteId": "suite-a",
  "missions": [
    { "missionId": "m1", "prompt": "p1" }
  ]
}`), 0o644); err != nil {
		t.Fatalf("write suite: %v", err)
	}
	specPath := filepath.Join(dir, "campaign.yaml")
	if err := os.WriteFile(specPath, []byte(`
schemaVersion: 1
campaignId: cmp-runner-cwd-normalized
flows:
  - flowId: flow-a
    suiteFile: suite.json
    runner:
      type: codex_app_server
      cwd:
        mode: " TEMP_EMPTY_PER_ATTEMPT "
        basePath: ".tmp/cwd"
        retain: " ON_FAILURE "
`), 0o644); err != nil {
		t.Fatalf("write spec: %v", err)
	}
	ps, err := ParseSpecFile(specPath)
	if err != nil {
		t.Fatalf("ParseSpecFile: %v", err)
	}
	got := ps.Spec.Flows[0].Runner.Cwd
	if got.Mode != RunnerCwdModeTempEmptyPerAttempt {
		t.Fatalf("expected cwd mode %q, got %q", RunnerCwdModeTempEmptyPerAttempt, got.Mode)
	}
	if got.Retain != RunnerCwdRetainOnFailure {
		t.Fatalf("expected cwd retain %q, got %q", RunnerCwdRetainOnFailure, got.Retain)
	}
	wantBase := filepath.Join(dir, ".tmp", "cwd")
	if got.BasePath != wantBase {
		t.Fatalf("expected cwd basePath %q, got %q", wantBase, got.BasePath)
	}
}

func TestParseSpecFile_RunnerCwdModeRequiresCodexAppServer(t *testing.T) {
	dir := t.TempDir()
	suitePath := filepath.Join(dir, "suite.json")
	if err := os.WriteFile(suitePath, []byte(`{
  "version": 1,
  "suiteId": "suite-a",
  "missions": [
    { "missionId": "m1", "prompt": "p1" }
  ]
}`), 0o644); err != nil {
		t.Fatalf("write suite: %v", err)
	}
	specPath := filepath.Join(dir, "campaign.yaml")
	if err := os.WriteFile(specPath, []byte(`
schemaVersion: 1
campaignId: cmp-runner-cwd-unsupported
flows:
  - flowId: flow-a
    suiteFile: suite.json
    runner:
      type: process_cmd
      command: ["echo","ok"]
      cwd:
        mode: temp_empty_per_attempt
`), 0o644); err != nil {
		t.Fatalf("write spec: %v", err)
	}
	if _, err := ParseSpecFile(specPath); err == nil || !strings.Contains(err.Error(), "supported only for runner.type=codex_app_server") {
		t.Fatalf("expected runner cwd runner-type validation error, got %v", err)
	}
}

func TestParseSpecFile_RunnerCwdRetainRequiresTempMode(t *testing.T) {
	dir := t.TempDir()
	suitePath := filepath.Join(dir, "suite.json")
	if err := os.WriteFile(suitePath, []byte(`{
  "version": 1,
  "suiteId": "suite-a",
  "missions": [
    { "missionId": "m1", "prompt": "p1" }
  ]
}`), 0o644); err != nil {
		t.Fatalf("write suite: %v", err)
	}
	specPath := filepath.Join(dir, "campaign.yaml")
	if err := os.WriteFile(specPath, []byte(`
schemaVersion: 1
campaignId: cmp-runner-cwd-retain
flows:
  - flowId: flow-a
    suiteFile: suite.json
    runner:
      type: codex_app_server
      cwd:
        retain: always
`), 0o644); err != nil {
		t.Fatalf("write spec: %v", err)
	}
	if _, err := ParseSpecFile(specPath); err == nil || !strings.Contains(err.Error(), "runner.cwd.retain requires runner.cwd.mode=temp_empty_per_attempt") {
		t.Fatalf("expected runner cwd retain-mode validation error, got %v", err)
	}
}

func TestParseSpecFile_ExamModeSplitSourcesAndEvaluator(t *testing.T) {
	dir := t.TempDir()
	promptDir := filepath.Join(dir, "prompts")
	oracleDir := filepath.Join(dir, "oracles-host")
	if err := os.MkdirAll(promptDir, 0o755); err != nil {
		t.Fatalf("mkdir prompts: %v", err)
	}
	if err := os.MkdirAll(oracleDir, 0o755); err != nil {
		t.Fatalf("mkdir oracles: %v", err)
	}
	if err := os.WriteFile(filepath.Join(promptDir, "m1.md"), []byte("Complete the task and return JSON proof."), 0o644); err != nil {
		t.Fatalf("write prompt: %v", err)
	}
	if err := os.WriteFile(filepath.Join(oracleDir, "m1.md"), []byte("oracle content"), 0o644); err != nil {
		t.Fatalf("write oracle: %v", err)
	}
	specPath := filepath.Join(dir, "campaign.yaml")
	if err := os.WriteFile(specPath, []byte(`
schemaVersion: 1
campaignId: cmp-exam
promptMode: exam
missionSource:
  promptSource:
    path: prompts
  oracleSource:
    path: oracles-host
    visibility: workspace
evaluation:
  mode: oracle
  evaluator:
    kind: script
    command: ["node", "./scripts/eval-mission.mjs"]
flows:
  - flowId: flow-a
    runner:
      type: process_cmd
      command: ["echo","ok"]
      finalization:
        mode: auto_from_result_json
        resultChannel:
          kind: file_json
`), 0o644); err != nil {
		t.Fatalf("write spec: %v", err)
	}
	ps, err := ParseSpecFile(specPath)
	if err != nil {
		t.Fatalf("ParseSpecFile: %v", err)
	}
	if ps.Spec.PromptMode != PromptModeExam {
		t.Fatalf("expected promptMode=%q got %q", PromptModeExam, ps.Spec.PromptMode)
	}
	if len(ps.FlowSuites["flow-a"].Suite.Missions) != 1 {
		t.Fatalf("expected one mission, got %d", len(ps.FlowSuites["flow-a"].Suite.Missions))
	}
	if got := ps.OracleByMissionID["m1"]; got == "" {
		t.Fatalf("expected oracle mapping for m1, got empty map: %+v", ps.OracleByMissionID)
	}
	if len(ps.Spec.NoContext.ForbiddenPromptTerms) == 0 {
		t.Fatalf("expected exam default forbidden prompt terms")
	}
}

func TestParseSpecFile_ExamModeBuiltinEvaluatorWithoutCommand(t *testing.T) {
	dir := t.TempDir()
	promptDir := filepath.Join(dir, "prompts")
	oracleDir := filepath.Join(dir, "oracles-host")
	if err := os.MkdirAll(promptDir, 0o755); err != nil {
		t.Fatalf("mkdir prompts: %v", err)
	}
	if err := os.MkdirAll(oracleDir, 0o755); err != nil {
		t.Fatalf("mkdir oracles: %v", err)
	}
	if err := os.WriteFile(filepath.Join(promptDir, "m1.md"), []byte("Complete the task and return JSON proof."), 0o644); err != nil {
		t.Fatalf("write prompt: %v", err)
	}
	if err := os.WriteFile(filepath.Join(oracleDir, "m1.json"), []byte(`{"schemaVersion":1,"missionId":"m1","rules":[{"field":"ok","op":"eq","value":true}]}`), 0o644); err != nil {
		t.Fatalf("write oracle: %v", err)
	}
	specPath := filepath.Join(dir, "campaign.yaml")
	if err := os.WriteFile(specPath, []byte(`
schemaVersion: 1
campaignId: cmp-exam-builtin
promptMode: exam
missionSource:
  promptSource:
    path: prompts
  oracleSource:
    path: oracles-host
evaluation:
  mode: oracle
  evaluator:
    kind: builtin_rules
flows:
  - flowId: flow-a
    runner:
      type: process_cmd
      command: ["echo","ok"]
      finalization:
        mode: auto_from_result_json
        resultChannel:
          kind: file_json
`), 0o644); err != nil {
		t.Fatalf("write spec: %v", err)
	}
	ps, err := ParseSpecFile(specPath)
	if err != nil {
		t.Fatalf("ParseSpecFile: %v", err)
	}
	if ps.Spec.Evaluation.Evaluator.Kind != EvaluatorKindBuiltin {
		t.Fatalf("expected builtin evaluator, got %q", ps.Spec.Evaluation.Evaluator.Kind)
	}
	if ps.Spec.Evaluation.OraclePolicy.Mode != OraclePolicyModeStrict {
		t.Fatalf("expected default oracle policy mode strict, got %q", ps.Spec.Evaluation.OraclePolicy.Mode)
	}
	if ps.Spec.Evaluation.OraclePolicy.FormatMismatch != OracleFormatMismatchFail {
		t.Fatalf("expected default format mismatch policy fail, got %q", ps.Spec.Evaluation.OraclePolicy.FormatMismatch)
	}
}

func TestParseSpecFile_InvalidOraclePolicyAndWatchdogFields(t *testing.T) {
	dir := t.TempDir()
	suitePath := filepath.Join(dir, "suite.json")
	if err := os.WriteFile(suitePath, []byte(`{
  "version": 1,
  "suiteId": "suite-a",
  "missions": [
    { "missionId": "m1", "prompt": "p1" }
  ]
}`), 0o644); err != nil {
		t.Fatalf("write suite: %v", err)
	}
	specPath := filepath.Join(dir, "campaign.yaml")
	if err := os.WriteFile(specPath, []byte(`
schemaVersion: 1
campaignId: cmp-invalid-policy
evaluation:
  mode: oracle
  oraclePolicy:
    mode: invalid
timeouts:
  missionEnvelopeMs: -1
flows:
  - flowId: flow-a
    suiteFile: suite.json
    runner:
      type: process_cmd
      command: ["echo","ok"]
`), 0o644); err != nil {
		t.Fatalf("write spec: %v", err)
	}
	if _, err := ParseSpecFile(specPath); err == nil {
		t.Fatalf("expected parse error")
	}
}

func TestParseSpecFile_ExamModeRejectsPromptOracleLeak(t *testing.T) {
	dir := t.TempDir()
	promptDir := filepath.Join(dir, "prompts")
	oracleDir := filepath.Join(dir, "oracles")
	if err := os.MkdirAll(promptDir, 0o755); err != nil {
		t.Fatalf("mkdir prompts: %v", err)
	}
	if err := os.MkdirAll(oracleDir, 0o755); err != nil {
		t.Fatalf("mkdir oracles: %v", err)
	}
	if err := os.WriteFile(filepath.Join(promptDir, "m1.md"), []byte("Solve it.\n\nSuccess Check:\n- Return exact value."), 0o644); err != nil {
		t.Fatalf("write prompt: %v", err)
	}
	if err := os.WriteFile(filepath.Join(oracleDir, "m1.md"), []byte("oracle content"), 0o644); err != nil {
		t.Fatalf("write oracle: %v", err)
	}
	specPath := filepath.Join(dir, "campaign.yaml")
	if err := os.WriteFile(specPath, []byte(`
schemaVersion: 1
campaignId: cmp-exam-leak
promptMode: exam
missionSource:
  promptSource:
    path: prompts
  oracleSource:
    path: oracles
evaluation:
  mode: oracle
  evaluator:
    kind: script
    command: ["node", "./scripts/eval-mission.mjs"]
flows:
  - flowId: flow-a
    runner:
      type: process_cmd
      command: ["echo","ok"]
      finalization:
        mode: auto_from_result_json
        resultChannel:
          kind: file_json
`), 0o644); err != nil {
		t.Fatalf("write spec: %v", err)
	}
	_, err := ParseSpecFile(specPath)
	if err == nil {
		t.Fatalf("expected exam prompt contamination violation")
	}
	var promptErr *PromptModeViolationError
	if !errors.As(err, &promptErr) {
		t.Fatalf("expected typed prompt violation error, got %v", err)
	}
	if promptErr.Code != ReasonExamPromptPolicy {
		t.Fatalf("expected code %q, got %q", ReasonExamPromptPolicy, promptErr.Code)
	}
	if len(promptErr.Violations) == 0 || strings.ToLower(promptErr.Violations[0].Term) != "success check" {
		t.Fatalf("unexpected violations: %+v", promptErr.Violations)
	}
}

func TestParseSpecFile_ExamModeHostOnlyRejectsWorkspaceOraclePath(t *testing.T) {
	dir := t.TempDir()
	promptDir := filepath.Join(dir, "prompts")
	oracleDir := filepath.Join(dir, "oracles")
	if err := os.MkdirAll(promptDir, 0o755); err != nil {
		t.Fatalf("mkdir prompts: %v", err)
	}
	if err := os.MkdirAll(oracleDir, 0o755); err != nil {
		t.Fatalf("mkdir oracles: %v", err)
	}
	if err := os.WriteFile(filepath.Join(promptDir, "m1.md"), []byte("Do task."), 0o644); err != nil {
		t.Fatalf("write prompt: %v", err)
	}
	if err := os.WriteFile(filepath.Join(oracleDir, "m1.md"), []byte("oracle content"), 0o644); err != nil {
		t.Fatalf("write oracle: %v", err)
	}
	specPath := filepath.Join(dir, "campaign.yaml")
	if err := os.WriteFile(specPath, []byte(`
schemaVersion: 1
campaignId: cmp-exam-host-only
promptMode: exam
missionSource:
  promptSource:
    path: prompts
  oracleSource:
    path: oracles
    visibility: host_only
evaluation:
  mode: oracle
  evaluator:
    kind: script
    command: ["node", "./scripts/eval-mission.mjs"]
flows:
  - flowId: flow-a
    runner:
      type: process_cmd
      command: ["echo","ok"]
      finalization:
        mode: auto_from_result_json
        resultChannel:
          kind: file_json
`), 0o644); err != nil {
		t.Fatalf("write spec: %v", err)
	}
	_, err := ParseSpecFile(specPath)
	if err == nil {
		t.Fatalf("expected host_only oracle visibility violation")
	}
	var policyErr *OraclePolicyViolationError
	if !errors.As(err, &policyErr) {
		t.Fatalf("expected typed oracle policy violation, got %v", err)
	}
	if policyErr.Code != ReasonOracleVisibility {
		t.Fatalf("expected code %q, got %q", ReasonOracleVisibility, policyErr.Code)
	}
	if policyErr.Violation.Visibility != OracleVisibilityHostOnly {
		t.Fatalf("expected visibility host_only, got %+v", policyErr.Violation)
	}
}

func TestParseSpecFile_FlowGateAlias(t *testing.T) {
	dir := t.TempDir()
	suitePath := filepath.Join(dir, "suite.json")
	if err := os.WriteFile(suitePath, []byte(`{
  "version": 1,
  "suiteId": "suite-a",
  "missions": [
    { "missionId": "m1", "prompt": "p1" }
  ]
}`), 0o644); err != nil {
		t.Fatalf("write suite: %v", err)
	}
	specPath := filepath.Join(dir, "campaign.yaml")
	if err := os.WriteFile(specPath, []byte(`
schemaVersion: 1
campaignId: cmp-flow-gate
flowGate:
  enabled: false
  traceProfile: mcp_required
flows:
  - flowId: flow-a
    suiteFile: suite.json
    runner:
      type: process_cmd
      command: ["echo","ok"]
`), 0o644); err != nil {
		t.Fatalf("write spec: %v", err)
	}
	ps, err := ParseSpecFile(specPath)
	if err != nil {
		t.Fatalf("ParseSpecFile: %v", err)
	}
	if ps.Spec.PairGateEnabled() {
		t.Fatalf("expected flowGate.enabled=false to disable pair gate")
	}
	if ps.Spec.PairGate.TraceProfile != TraceProfileMCPRequired {
		t.Fatalf("expected trace profile from flowGate alias, got %q", ps.Spec.PairGate.TraceProfile)
	}
}

func TestParseSpecFile_FlowGateAliasRejectsConflictingValues(t *testing.T) {
	dir := t.TempDir()
	suitePath := filepath.Join(dir, "suite.json")
	if err := os.WriteFile(suitePath, []byte(`{
  "version": 1,
  "suiteId": "suite-a",
  "missions": [
    { "missionId": "m1", "prompt": "p1" }
  ]
}`), 0o644); err != nil {
		t.Fatalf("write suite: %v", err)
	}
	specPath := filepath.Join(dir, "campaign.yaml")
	if err := os.WriteFile(specPath, []byte(`
schemaVersion: 1
campaignId: cmp-flow-gate-conflict
pairGate:
  enabled: true
flowGate:
  enabled: false
flows:
  - flowId: flow-a
    suiteFile: suite.json
    runner:
      type: process_cmd
      command: ["echo","ok"]
`), 0o644); err != nil {
		t.Fatalf("write spec: %v", err)
	}
	if _, err := ParseSpecFile(specPath); err == nil || !strings.Contains(err.Error(), "pairGate and flowGate both set but differ") {
		t.Fatalf("expected pairGate/flowGate conflict error, got %v", err)
	}
}

func TestParseSpecFile_ExamModeFlowPromptSourceOverrides(t *testing.T) {
	dir := t.TempDir()
	promptA := filepath.Join(dir, "prompts-a")
	promptB := filepath.Join(dir, "prompts-b")
	oracleDir := filepath.Join(dir, "oracles")
	for _, p := range []string{promptA, promptB, oracleDir} {
		if err := os.MkdirAll(p, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", p, err)
		}
	}
	if err := os.WriteFile(filepath.Join(promptA, "m1.md"), []byte("prompt a"), 0o644); err != nil {
		t.Fatalf("write prompt a: %v", err)
	}
	if err := os.WriteFile(filepath.Join(promptB, "m1.md"), []byte("prompt b"), 0o644); err != nil {
		t.Fatalf("write prompt b: %v", err)
	}
	if err := os.WriteFile(filepath.Join(oracleDir, "m1.md"), []byte("oracle content"), 0o644); err != nil {
		t.Fatalf("write oracle: %v", err)
	}
	specPath := filepath.Join(dir, "campaign.yaml")
	if err := os.WriteFile(specPath, []byte(`
schemaVersion: 1
campaignId: cmp-exam-flow-prompts
promptMode: exam
missionSource:
  oracleSource:
    path: oracles
evaluation:
  mode: oracle
  evaluator:
    kind: script
    command: ["node", "./scripts/eval-mission.mjs"]
flows:
  - flowId: flow-a
    promptSource:
      path: prompts-a
    runner:
      type: process_cmd
      command: ["echo","ok"]
      finalization:
        mode: auto_from_result_json
        resultChannel:
          kind: file_json
  - flowId: flow-b
    promptSource:
      path: prompts-b
    runner:
      type: process_cmd
      command: ["echo","ok"]
      finalization:
        mode: auto_from_result_json
        resultChannel:
          kind: file_json
`), 0o644); err != nil {
		t.Fatalf("write spec: %v", err)
	}
	ps, err := ParseSpecFile(specPath)
	if err != nil {
		t.Fatalf("ParseSpecFile: %v", err)
	}
	if got := strings.TrimSpace(ps.FlowSuites["flow-a"].Suite.Missions[0].Prompt); got != "prompt a" {
		t.Fatalf("unexpected flow-a prompt: %q", got)
	}
	if got := strings.TrimSpace(ps.FlowSuites["flow-b"].Suite.Missions[0].Prompt); got != "prompt b" {
		t.Fatalf("unexpected flow-b prompt: %q", got)
	}
	if ps.OracleByMissionID["m1"] == "" {
		t.Fatalf("expected oracle mapping for mission m1")
	}
}

func TestParseSpecFile_FlowPromptTemplateRendersAtParseTime(t *testing.T) {
	dir := t.TempDir()
	suitePath := filepath.Join(dir, "suite.json")
	templatePath := filepath.Join(dir, "prompt-template.txt")
	if err := os.WriteFile(suitePath, []byte(`{
  "version": 1,
  "suiteId": "suite-a",
  "missions": [
    { "missionId": "m1", "prompt": "base prompt" }
  ]
}`), 0o644); err != nil {
		t.Fatalf("write suite: %v", err)
	}
	if err := os.WriteFile(templatePath, []byte("flow={{flowId}}\nmission={{missionId}}\nbase={{prompt}}\nroute={{runnerEnv.ZCL_ROUTER}}\n"), 0o644); err != nil {
		t.Fatalf("write template: %v", err)
	}
	specPath := filepath.Join(dir, "campaign.yaml")
	if err := os.WriteFile(specPath, []byte(`
schemaVersion: 1
campaignId: cmp-template
flows:
  - flowId: flow-a
    suiteFile: suite.json
    promptTemplate:
      path: prompt-template.txt
      allowRunnerEnvKeys: ["ZCL_ROUTER"]
    runner:
      type: process_cmd
      command: ["echo","ok"]
      env:
        ZCL_ROUTER: chrome-mcp
`), 0o644); err != nil {
		t.Fatalf("write spec: %v", err)
	}
	ps, err := ParseSpecFile(specPath)
	if err != nil {
		t.Fatalf("ParseSpecFile: %v", err)
	}
	want := "flow=flow-a\nmission=m1\nbase=base prompt\nroute=chrome-mcp"
	if got := strings.TrimSpace(ps.FlowSuites["flow-a"].Suite.Missions[0].Prompt); got != want {
		t.Fatalf("unexpected rendered prompt:\n%s", got)
	}
}

func TestParseSpecFile_FlowPromptTemplateRejectsUnresolvedToken(t *testing.T) {
	dir := t.TempDir()
	suitePath := filepath.Join(dir, "suite.json")
	templatePath := filepath.Join(dir, "prompt-template.txt")
	if err := os.WriteFile(suitePath, []byte(`{
  "version": 1,
  "suiteId": "suite-a",
  "missions": [
    { "missionId": "m1", "prompt": "base prompt" }
  ]
}`), 0o644); err != nil {
		t.Fatalf("write suite: %v", err)
	}
	if err := os.WriteFile(templatePath, []byte("{{flowId}} {{missingToken}}"), 0o644); err != nil {
		t.Fatalf("write template: %v", err)
	}
	specPath := filepath.Join(dir, "campaign.yaml")
	if err := os.WriteFile(specPath, []byte(`
schemaVersion: 1
campaignId: cmp-template
flows:
  - flowId: flow-a
    suiteFile: suite.json
    promptTemplate:
      path: prompt-template.txt
    runner:
      type: process_cmd
      command: ["echo","ok"]
`), 0o644); err != nil {
		t.Fatalf("write spec: %v", err)
	}
	if _, err := ParseSpecFile(specPath); err == nil || !strings.Contains(err.Error(), "unresolved token") {
		t.Fatalf("expected unresolved token error, got %v", err)
	}
}

func TestParseSpecFile_ToolPolicyRuleRequiresNamespaceOrPrefix(t *testing.T) {
	dir := t.TempDir()
	suitePath := filepath.Join(dir, "suite.json")
	if err := os.WriteFile(suitePath, []byte(`{
  "version": 1,
  "suiteId": "suite-a",
  "missions": [
    { "missionId": "m1", "prompt": "p1" }
  ]
}`), 0o644); err != nil {
		t.Fatalf("write suite: %v", err)
	}
	specPath := filepath.Join(dir, "campaign.yaml")
	if err := os.WriteFile(specPath, []byte(`
schemaVersion: 1
campaignId: cmp-tool-policy
flows:
  - flowId: flow-a
    suiteFile: suite.json
    toolPolicy:
      allow:
        - {}
    runner:
      type: process_cmd
      command: ["echo","ok"]
`), 0o644); err != nil {
		t.Fatalf("write spec: %v", err)
	}
	if _, err := ParseSpecFile(specPath); err == nil || !strings.Contains(err.Error(), ReasonToolPolicyConfig) {
		t.Fatalf("expected typed toolPolicy config error, got %v", err)
	}
}
