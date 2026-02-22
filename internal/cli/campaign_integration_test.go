package cli

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCampaignRun_Status_Report_PublishCheck(t *testing.T) {
	outRoot := t.TempDir()
	specDir := t.TempDir()
	suiteA := filepath.Join(specDir, "suite-a.json")
	suiteB := filepath.Join(specDir, "suite-b.json")
	writeSuiteFile(t, suiteA, `{
  "version": 1,
  "suiteId": "campaign-suite-a",
  "missions": [
    { "missionId": "m1", "prompt": "p1", "expects": { "ok": true } }
  ]
}`)
	writeSuiteFile(t, suiteB, `{
  "version": 1,
  "suiteId": "campaign-suite-b",
  "missions": [
    { "missionId": "m1", "prompt": "p1", "expects": { "ok": true } }
  ]
}`)
	specPath := filepath.Join(specDir, "campaign.yaml")
	if err := os.WriteFile(specPath, []byte(`
schemaVersion: 1
campaignId: cmp-int
totalMissions: 1
semantic:
  enabled: false
flows:
  - flowId: flow-a
    suiteFile: suite-a.json
    runner:
      type: process_cmd
      command: ["`+os.Args[0]+`", "-test.run=TestHelperSuiteRunnerProcess$", "--", "case=ok"]
  - flowId: flow-b
    suiteFile: suite-b.json
    runner:
      type: process_cmd
      command: ["`+os.Args[0]+`", "-test.run=TestHelperSuiteRunnerProcess$", "--", "case=ok"]
`), 0o644); err != nil {
		t.Fatalf("write campaign spec: %v", err)
	}

	t.Setenv("ZCL_WANT_SUITE_RUNNER", "1")

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	r := Runner{
		Version: "0.0.0-dev",
		Now:     func() time.Time { return time.Date(2026, 2, 22, 12, 0, 0, 0, time.UTC) },
		Stdout:  &stdout,
		Stderr:  &stderr,
	}
	code := r.Run([]string{"campaign", "run", "--spec", specPath, "--out-root", outRoot, "--json"})
	if code != 0 {
		t.Fatalf("campaign run expected 0, got %d stderr=%q", code, stderr.String())
	}
	var run struct {
		CampaignID string `json:"campaignId"`
		RunID      string `json:"runId"`
		Status     string `json:"status"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &run); err != nil {
		t.Fatalf("unmarshal campaign run json: %v stdout=%q", err, stdout.String())
	}
	if run.CampaignID != "cmp-int" || run.RunID == "" || run.Status != "valid" {
		t.Fatalf("unexpected campaign run summary: %+v", run)
	}
	if _, err := os.Stat(filepath.Join(outRoot, "campaigns", "cmp-int", "campaign.summary.json")); err != nil {
		t.Fatalf("expected campaign.summary.json: %v", err)
	}
	if _, err := os.Stat(filepath.Join(outRoot, "campaigns", "cmp-int", "RESULTS.md")); err != nil {
		t.Fatalf("expected RESULTS.md: %v", err)
	}

	stdout.Reset()
	stderr.Reset()
	code = r.Run([]string{"campaign", "status", "--campaign-id", "cmp-int", "--out-root", outRoot, "--json"})
	if code != 0 {
		t.Fatalf("campaign status expected 0, got %d stderr=%q", code, stderr.String())
	}
	var status struct {
		RunID  string `json:"runId"`
		Status string `json:"status"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &status); err != nil {
		t.Fatalf("unmarshal campaign status json: %v", err)
	}
	if status.RunID != run.RunID || status.Status != "valid" {
		t.Fatalf("unexpected campaign status: %+v", status)
	}

	stdout.Reset()
	stderr.Reset()
	code = r.Run([]string{"campaign", "report", "--campaign-id", "cmp-int", "--out-root", outRoot, "--json"})
	if code != 0 {
		t.Fatalf("campaign report expected 0, got %d stderr=%q", code, stderr.String())
	}
	var report struct {
		Status      string `json:"status"`
		GatesPassed int    `json:"gatesPassed"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("unmarshal campaign report json: %v", err)
	}
	if report.Status != "valid" || report.GatesPassed != 1 {
		t.Fatalf("unexpected campaign report: %+v", report)
	}

	stdout.Reset()
	stderr.Reset()
	code = r.Run([]string{"campaign", "publish-check", "--campaign-id", "cmp-int", "--out-root", outRoot, "--json"})
	if code != 0 {
		t.Fatalf("campaign publish-check expected 0, got %d stderr=%q", code, stderr.String())
	}
}

func TestCampaignRun_InvalidAndPublishCheckFails(t *testing.T) {
	outRoot := t.TempDir()
	specDir := t.TempDir()
	suitePath := filepath.Join(specDir, "suite.json")
	writeSuiteFile(t, suitePath, `{
  "version": 1,
  "suiteId": "campaign-suite-invalid",
  "missions": [
    { "missionId": "m1", "prompt": "p1" }
  ]
}`)
	specPath := filepath.Join(specDir, "campaign.yaml")
	if err := os.WriteFile(specPath, []byte(`
schemaVersion: 1
campaignId: cmp-invalid
totalMissions: 1
semantic:
  enabled: false
flows:
  - flowId: flow-a
    suiteFile: suite.json
    runner:
      type: process_cmd
      command: ["`+os.Args[0]+`", "-test.run=TestHelperSuiteRunnerProcess$", "--", "case=no-feedback"]
`), 0o644); err != nil {
		t.Fatalf("write campaign spec: %v", err)
	}

	t.Setenv("ZCL_WANT_SUITE_RUNNER", "1")

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	r := Runner{
		Version: "0.0.0-dev",
		Now:     func() time.Time { return time.Date(2026, 2, 22, 12, 0, 0, 0, time.UTC) },
		Stdout:  &stdout,
		Stderr:  &stderr,
	}
	code := r.Run([]string{"campaign", "run", "--spec", specPath, "--out-root", outRoot, "--json"})
	if code != 2 {
		t.Fatalf("campaign run expected 2, got %d stderr=%q", code, stderr.String())
	}
	var run struct {
		Status string `json:"status"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &run); err != nil {
		t.Fatalf("unmarshal campaign run invalid json: %v stdout=%q", err, stdout.String())
	}
	if run.Status != "invalid" && run.Status != "aborted" {
		t.Fatalf("expected invalid/aborted status, got %+v", run)
	}

	stdout.Reset()
	stderr.Reset()
	code = r.Run([]string{"campaign", "publish-check", "--campaign-id", "cmp-invalid", "--out-root", outRoot, "--json"})
	if code != 2 {
		t.Fatalf("publish-check expected 2, got %d stderr=%q", code, stderr.String())
	}
	var check struct {
		OK bool `json:"ok"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &check); err != nil {
		t.Fatalf("unmarshal publish-check json: %v stdout=%q", err, stdout.String())
	}
	if check.OK {
		t.Fatalf("expected publish-check ok=false for invalid campaign")
	}
}

func TestMissionPromptsBuild_MaterializesDeterministically(t *testing.T) {
	outRoot := t.TempDir()
	specDir := t.TempDir()
	suitePath := filepath.Join(specDir, "suite.json")
	writeSuiteFile(t, suitePath, `{
  "version": 1,
  "suiteId": "suite-prompts",
  "missions": [
    { "missionId": "m1", "prompt": "open docs", "tags": ["a","b"] },
    { "missionId": "m2", "prompt": "open changelog", "tags": ["c"] }
  ]
}`)
	specPath := filepath.Join(specDir, "campaign.yaml")
	if err := os.WriteFile(specPath, []byte(`
schemaVersion: 1
campaignId: cmp-prompts
missionSource:
  selection:
    mode: index
    indexes: [1]
flows:
  - flowId: flow-a
    suiteFile: suite.json
    runner:
      type: process_cmd
      command: ["echo","ok"]
`), 0o644); err != nil {
		t.Fatalf("write campaign spec: %v", err)
	}
	templatePath := filepath.Join(specDir, "template.md")
	if err := os.WriteFile(templatePath, []byte("FLOW={{flowId}} MISSION={{missionId}} PROMPT={{prompt}} TAGS={{tagsCsv}}"), 0o644); err != nil {
		t.Fatalf("write template: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	r := Runner{
		Version: "0.0.0-dev",
		Now:     func() time.Time { return time.Date(2026, 2, 22, 12, 30, 0, 0, time.UTC) },
		Stdout:  &stdout,
		Stderr:  &stderr,
	}
	code := r.Run([]string{"mission", "prompts", "build", "--spec", specPath, "--template", templatePath, "--out-root", outRoot, "--json"})
	if code != 0 {
		t.Fatalf("mission prompts build expected 0, got %d stderr=%q", code, stderr.String())
	}
	firstJSON := strings.TrimSpace(stdout.String())
	var res struct {
		CampaignID string `json:"campaignId"`
		CreatedAt  string `json:"createdAt"`
		Prompts    []struct {
			ID           string `json:"id"`
			MissionID    string `json:"missionId"`
			MissionIndex int    `json:"missionIndex"`
			Prompt       string `json:"prompt"`
		} `json:"prompts"`
		OutPath string `json:"outPath"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &res); err != nil {
		t.Fatalf("unmarshal mission prompts json: %v stdout=%q", err, stdout.String())
	}
	if res.CampaignID != "cmp-prompts" || len(res.Prompts) != 1 {
		t.Fatalf("unexpected prompts result: %+v", res)
	}
	if res.Prompts[0].MissionID != "m2" || res.Prompts[0].MissionIndex != 1 {
		t.Fatalf("expected selected mission m2@index1, got %+v", res.Prompts[0])
	}
	if got := res.Prompts[0].Prompt; got != "FLOW=flow-a MISSION=m2 PROMPT=open changelog TAGS=c" {
		t.Fatalf("unexpected materialized prompt: %q", got)
	}
	if strings.TrimSpace(res.Prompts[0].ID) == "" {
		t.Fatalf("expected stable prompt id")
	}
	if _, err := os.Stat(res.OutPath); err != nil {
		t.Fatalf("expected output artifact at %s: %v", res.OutPath, err)
	}

	stdout.Reset()
	stderr.Reset()
	code = r.Run([]string{"mission", "prompts", "build", "--spec", specPath, "--template", templatePath, "--out-root", outRoot, "--json"})
	if code != 0 {
		t.Fatalf("second mission prompts build expected 0, got %d stderr=%q", code, stderr.String())
	}
	secondJSON := strings.TrimSpace(stdout.String())
	if firstJSON != secondJSON {
		t.Fatalf("expected deterministic output bytes across repeated builds")
	}
}

func TestCampaignLint_ValidatesSpecStrictly(t *testing.T) {
	specDir := t.TempDir()
	suitePath := filepath.Join(specDir, "suite.json")
	writeSuiteFile(t, suitePath, `{
  "version": 1,
  "suiteId": "suite-lint",
  "missions": [
    { "missionId": "m1", "prompt": "p1" }
  ]
}`)
	specPath := filepath.Join(specDir, "campaign.yaml")
	if err := os.WriteFile(specPath, []byte(`
schemaVersion: 1
campaignId: cmp-lint
x-owner: qa
flows:
  - flowId: flow-a
    suiteFile: suite.json
    runner:
      type: process_cmd
      command: ["echo","ok"]
`), 0o644); err != nil {
		t.Fatalf("write campaign spec: %v", err)
	}
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	r := Runner{
		Version: "0.0.0-dev",
		Now:     func() time.Time { return time.Date(2026, 2, 22, 12, 0, 0, 0, time.UTC) },
		Stdout:  &stdout,
		Stderr:  &stderr,
	}
	code := r.Run([]string{"campaign", "lint", "--spec", specPath, "--json"})
	if code != 0 {
		t.Fatalf("campaign lint expected 0, got %d stderr=%q", code, stderr.String())
	}
	var out struct {
		OK         bool           `json:"ok"`
		Extensions map[string]any `json:"extensions"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal campaign lint output: %v", err)
	}
	if !out.OK || out.Extensions["x-owner"] == nil {
		t.Fatalf("unexpected campaign lint output: %+v", out)
	}
}

func TestCampaignResume_DoesNotDuplicateAttempts(t *testing.T) {
	outRoot := t.TempDir()
	specDir := t.TempDir()
	suitePath := filepath.Join(specDir, "suite.json")
	writeSuiteFile(t, suitePath, `{
  "version": 1,
  "suiteId": "campaign-suite-resume",
  "missions": [
    { "missionId": "m1", "prompt": "p1", "expects": { "ok": true } },
    { "missionId": "m2", "prompt": "p2", "expects": { "ok": true } }
  ]
}`)
	specPath := filepath.Join(specDir, "campaign.yaml")
	if err := os.WriteFile(specPath, []byte(`
schemaVersion: 1
campaignId: cmp-resume
semantic:
  enabled: false
flows:
  - flowId: flow-a
    suiteFile: suite.json
    runner:
      type: process_cmd
      command: ["`+os.Args[0]+`", "-test.run=TestHelperSuiteRunnerProcess$", "--", "case=ok"]
`), 0o644); err != nil {
		t.Fatalf("write campaign spec: %v", err)
	}
	t.Setenv("ZCL_WANT_SUITE_RUNNER", "1")
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	r := Runner{
		Version: "0.0.0-dev",
		Now:     func() time.Time { return time.Date(2026, 2, 22, 13, 0, 0, 0, time.UTC) },
		Stdout:  &stdout,
		Stderr:  &stderr,
	}
	code := r.Run([]string{"campaign", "run", "--spec", specPath, "--out-root", outRoot, "--json"})
	if code != 0 {
		t.Fatalf("campaign run expected 0, got %d stderr=%q", code, stderr.String())
	}
	progressPath := filepath.Join(outRoot, "campaigns", "cmp-resume", "campaign.progress.jsonl")
	beforeCount := countJSONLLines(t, progressPath)

	stdout.Reset()
	stderr.Reset()
	code = r.Run([]string{"campaign", "resume", "--campaign-id", "cmp-resume", "--out-root", outRoot, "--json"})
	if code != 0 {
		t.Fatalf("campaign resume expected 0, got %d stderr=%q", code, stderr.String())
	}
	afterCount := countJSONLLines(t, progressPath)
	if beforeCount != afterCount {
		t.Fatalf("expected no new progress events on resume when complete, before=%d after=%d", beforeCount, afterCount)
	}
}

func TestCampaignRun_LockContentionReturnsAborted(t *testing.T) {
	outRoot := t.TempDir()
	specDir := t.TempDir()
	suitePath := filepath.Join(specDir, "suite.json")
	writeSuiteFile(t, suitePath, `{
  "version": 1,
  "suiteId": "campaign-suite-lock",
  "missions": [
    { "missionId": "m1", "prompt": "p1" }
  ]
}`)
	specPath := filepath.Join(specDir, "campaign.yaml")
	if err := os.WriteFile(specPath, []byte(`
schemaVersion: 1
campaignId: cmp-lock
flows:
  - flowId: flow-a
    suiteFile: suite.json
    runner:
      type: process_cmd
      command: ["echo","ok"]
`), 0o644); err != nil {
		t.Fatalf("write campaign spec: %v", err)
	}
	lockDir := filepath.Join(outRoot, "campaigns", "cmp-lock", "campaign.lock")
	if err := os.MkdirAll(lockDir, 0o755); err != nil {
		t.Fatalf("create lock dir: %v", err)
	}
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	r := Runner{
		Version: "0.0.0-dev",
		Now:     func() time.Time { return time.Date(2026, 2, 22, 14, 0, 0, 0, time.UTC) },
		Stdout:  &stdout,
		Stderr:  &stderr,
	}
	code := r.Run([]string{"campaign", "run", "--spec", specPath, "--out-root", outRoot, "--json"})
	if code != 1 {
		t.Fatalf("campaign run expected lock failure exit 1, got %d stderr=%q", code, stderr.String())
	}
	var run struct {
		Status      string   `json:"status"`
		ReasonCodes []string `json:"reasonCodes"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &run); err != nil {
		t.Fatalf("unmarshal campaign run lock output: %v stdout=%q", err, stdout.String())
	}
	if run.Status != "aborted" {
		t.Fatalf("expected aborted status on lock contention, got %+v", run)
	}
}

func TestCampaignRun_MinimalMissionPackMode(t *testing.T) {
	outRoot := t.TempDir()
	specDir := t.TempDir()
	missionDir := filepath.Join(specDir, "missions")
	if err := os.MkdirAll(missionDir, 0o755); err != nil {
		t.Fatalf("mkdir mission dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(missionDir, "01-a.md"), []byte("mission a"), 0o644); err != nil {
		t.Fatalf("write mission a: %v", err)
	}
	if err := os.WriteFile(filepath.Join(missionDir, "02-b.md"), []byte("mission b"), 0o644); err != nil {
		t.Fatalf("write mission b: %v", err)
	}
	specPath := filepath.Join(specDir, "campaign.yaml")
	if err := os.WriteFile(specPath, []byte(`
schemaVersion: 1
campaignId: cmp-pack
missionSource:
  path: missions
execution:
  flowMode: parallel
pairGate:
  traceProfile: strict_browser_comparison
flows:
  - flowId: flow-a
    runner:
      type: codex_exec
      command: ["`+os.Args[0]+`", "-test.run=TestHelperSuiteRunnerProcess$", "--", "case=ok"]
  - flowId: flow-b
    runner:
      type: claude_subagent
      command: ["`+os.Args[0]+`", "-test.run=TestHelperSuiteRunnerProcess$", "--", "case=ok"]
`), 0o644); err != nil {
		t.Fatalf("write campaign spec: %v", err)
	}
	t.Setenv("ZCL_WANT_SUITE_RUNNER", "1")

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	r := Runner{
		Version: "0.0.0-dev",
		Now:     func() time.Time { return time.Date(2026, 2, 22, 15, 0, 0, 0, time.UTC) },
		Stdout:  &stdout,
		Stderr:  &stderr,
	}
	code := r.Run([]string{"campaign", "run", "--spec", specPath, "--out-root", outRoot, "--json"})
	if code != 0 {
		t.Fatalf("campaign run expected 0, got %d stderr=%q", code, stderr.String())
	}
	var run struct {
		Status string `json:"status"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &run); err != nil {
		t.Fatalf("unmarshal campaign run output: %v", err)
	}
	if run.Status != "valid" {
		t.Fatalf("expected valid run, got %+v", run)
	}
}

func TestCampaignRun_TraceProfileMCPRequiredFails(t *testing.T) {
	outRoot := t.TempDir()
	specDir := t.TempDir()
	suitePath := filepath.Join(specDir, "suite.json")
	writeSuiteFile(t, suitePath, `{
  "version": 1,
  "suiteId": "suite-mcp-profile",
  "missions": [
    { "missionId": "m1", "prompt": "p1", "expects": { "ok": true } }
  ]
}`)
	specPath := filepath.Join(specDir, "campaign.yaml")
	if err := os.WriteFile(specPath, []byte(`
schemaVersion: 1
campaignId: cmp-profile
pairGate:
  traceProfile: mcp_required
flows:
  - flowId: flow-a
    suiteFile: suite.json
    runner:
      type: process_cmd
      command: ["`+os.Args[0]+`", "-test.run=TestHelperSuiteRunnerProcess$", "--", "case=ok"]
`), 0o644); err != nil {
		t.Fatalf("write campaign spec: %v", err)
	}
	t.Setenv("ZCL_WANT_SUITE_RUNNER", "1")

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	r := Runner{
		Version: "0.0.0-dev",
		Now:     func() time.Time { return time.Date(2026, 2, 22, 16, 0, 0, 0, time.UTC) },
		Stdout:  &stdout,
		Stderr:  &stderr,
	}
	code := r.Run([]string{"campaign", "run", "--spec", specPath, "--out-root", outRoot, "--json"})
	if code != 2 {
		t.Fatalf("expected invalid campaign exit 2, got %d stderr=%q", code, stderr.String())
	}
	stateRaw, err := os.ReadFile(filepath.Join(outRoot, "campaigns", "cmp-profile", "campaign.run.state.json"))
	if err != nil {
		t.Fatalf("read campaign state: %v", err)
	}
	var st struct {
		Status       string `json:"status"`
		MissionGates []struct {
			Reasons []string `json:"reasons"`
		} `json:"missionGates"`
	}
	if err := json.Unmarshal(stateRaw, &st); err != nil {
		t.Fatalf("unmarshal campaign state: %v", err)
	}
	if st.Status != "invalid" || len(st.MissionGates) == 0 {
		t.Fatalf("unexpected campaign state: %+v", st)
	}
	if !strings.Contains(strings.Join(st.MissionGates[0].Reasons, ","), "ZCL_E_CAMPAIGN_TRACE_PROFILE_MCP_REQUIRED") {
		t.Fatalf("expected mcp trace profile gate reason, got %+v", st.MissionGates[0].Reasons)
	}
}

func countJSONLLines(t *testing.T, path string) int {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	defer func() { _ = f.Close() }()
	sc := bufio.NewScanner(f)
	count := 0
	for sc.Scan() {
		if strings.TrimSpace(sc.Text()) != "" {
			count++
		}
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("scan %s: %v", path, err)
	}
	return count
}
