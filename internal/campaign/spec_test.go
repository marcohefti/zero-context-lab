package campaign

import (
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
