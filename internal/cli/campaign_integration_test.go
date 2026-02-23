package cli

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
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
	if err := os.WriteFile(specPath, []byte(fmt.Sprintf(`
schemaVersion: 1
campaignId: cmp-int
outRoot: %q
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
`, outRoot)), 0o644); err != nil {
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

	stdout.Reset()
	stderr.Reset()
	code = r.Run([]string{"campaign", "report", "--spec", specPath, "--json"})
	if code != 0 {
		t.Fatalf("campaign report --spec expected 0, got %d stderr=%q", code, stderr.String())
	}

	stdout.Reset()
	stderr.Reset()
	code = r.Run([]string{"campaign", "publish-check", "--spec", specPath, "--json"})
	if code != 0 {
		t.Fatalf("campaign publish-check --spec expected 0, got %d stderr=%q", code, stderr.String())
	}
}

func TestCampaignRun_NativeCodexAppServerFlow(t *testing.T) {
	outRoot := t.TempDir()
	specDir := t.TempDir()
	suitePath := filepath.Join(specDir, "suite-native.json")
	writeSuiteFile(t, suitePath, `{
  "version": 1,
  "suiteId": "campaign-suite-native",
  "missions": [
    { "missionId": "m1", "prompt": "p1", "expects": { "ok": true } }
  ]
}`)

	specPath := filepath.Join(specDir, "campaign-native.yaml")
	if err := os.WriteFile(specPath, []byte(fmt.Sprintf(`
schemaVersion: 1
campaignId: cmp-native
outRoot: %q
totalMissions: 1
semantic:
  enabled: false
flows:
  - flowId: flow-native
    suiteFile: suite-native.json
    runner:
      type: codex_app_server
      sessionIsolation: native
      runtimeStrategies: ["codex_app_server"]
`, outRoot)), 0o644); err != nil {
		t.Fatalf("write native campaign spec: %v", err)
	}

	t.Setenv("ZCL_CODEX_APP_SERVER_CMD", os.Args[0]+" -test.run=TestHelperSuiteNativeAppServer$")
	t.Setenv("ZCL_HELPER_PROCESS", "1")
	t.Setenv("ZCL_HELPER_MODE", "smoke")

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	r := Runner{
		Version: "0.0.0-dev",
		Now:     func() time.Time { return time.Date(2026, 2, 22, 13, 0, 0, 0, time.UTC) },
		Stdout:  &stdout,
		Stderr:  &stderr,
	}

	if code := r.Run([]string{"campaign", "lint", "--spec", specPath, "--out-root", outRoot, "--json"}); code != 0 {
		t.Fatalf("campaign lint expected 0, got %d stderr=%q", code, stderr.String())
	}

	stdout.Reset()
	stderr.Reset()
	if code := r.Run([]string{"campaign", "doctor", "--spec", specPath, "--out-root", outRoot, "--json"}); code != 0 {
		t.Fatalf("campaign doctor expected 0, got %d stderr=%q", code, stderr.String())
	}

	stdout.Reset()
	stderr.Reset()
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
	if run.CampaignID != "cmp-native" || run.RunID == "" || run.Status != "valid" {
		t.Fatalf("unexpected campaign run summary: %+v", run)
	}

	stdout.Reset()
	stderr.Reset()
	if code := r.Run([]string{"campaign", "canary", "--spec", specPath, "--out-root", outRoot, "--missions", "1", "--json"}); code != 0 {
		t.Fatalf("campaign canary expected 0, got %d stderr=%q", code, stderr.String())
	}

	stdout.Reset()
	stderr.Reset()
	if code := r.Run([]string{"campaign", "status", "--campaign-id", "cmp-native", "--out-root", outRoot, "--json"}); code != 0 {
		t.Fatalf("campaign status expected 0, got %d stderr=%q", code, stderr.String())
	}

	stdout.Reset()
	stderr.Reset()
	if code := r.Run([]string{"campaign", "report", "--campaign-id", "cmp-native", "--out-root", outRoot, "--json"}); code != 0 {
		t.Fatalf("campaign report expected 0, got %d stderr=%q", code, stderr.String())
	}

	stdout.Reset()
	stderr.Reset()
	if code := r.Run([]string{"campaign", "resume", "--campaign-id", "cmp-native", "--out-root", outRoot, "--json"}); code != 0 {
		t.Fatalf("campaign resume expected 0, got %d stderr=%q", code, stderr.String())
	}

	stdout.Reset()
	stderr.Reset()
	if code := r.Run([]string{"campaign", "publish-check", "--campaign-id", "cmp-native", "--out-root", outRoot, "--json"}); code != 0 {
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

func TestCampaignLint_MissionOnlyViolationReturnsTypedCode(t *testing.T) {
	specDir := t.TempDir()
	suitePath := filepath.Join(specDir, "suite.json")
	writeSuiteFile(t, suitePath, `{
  "version": 1,
  "suiteId": "suite-lint-mission-only",
  "missions": [
    { "missionId": "m1", "prompt": "Solve mission and call zcl feedback." }
  ]
}`)
	specPath := filepath.Join(specDir, "campaign.yaml")
	if err := os.WriteFile(specPath, []byte(`
schemaVersion: 1
campaignId: cmp-lint-mission-only
promptMode: mission_only
flows:
  - flowId: flow-a
    suiteFile: suite.json
    runner:
      type: process_cmd
      command: ["echo","ok"]
      finalization:
        mode: auto_from_result_json
        resultChannel:
          kind: file_json
`), 0o644); err != nil {
		t.Fatalf("write campaign spec: %v", err)
	}
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	r := Runner{
		Version: "0.0.0-dev",
		Now:     func() time.Time { return time.Date(2026, 2, 22, 18, 0, 0, 0, time.UTC) },
		Stdout:  &stdout,
		Stderr:  &stderr,
	}
	code := r.Run([]string{"campaign", "lint", "--spec", specPath, "--json"})
	if code != 2 {
		t.Fatalf("campaign lint expected 2, got %d stderr=%q", code, stderr.String())
	}
	var out struct {
		OK         bool   `json:"ok"`
		Code       string `json:"code"`
		PromptMode string `json:"promptMode"`
		Violations []struct {
			FlowID string `json:"flowId"`
			Term   string `json:"term"`
		} `json:"violations"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal campaign lint output: %v stdout=%q", err, stdout.String())
	}
	if out.OK || out.Code != "ZCL_E_CAMPAIGN_PROMPT_MODE_VIOLATION" || out.PromptMode != "mission_only" || len(out.Violations) == 0 {
		t.Fatalf("unexpected campaign lint policy output: %+v", out)
	}
}

func TestCampaignLint_CLIFunnelShimViolationStructured(t *testing.T) {
	specDir := t.TempDir()
	suitePath := filepath.Join(specDir, "suite.json")
	writeSuiteFile(t, suitePath, `{
  "version": 1,
  "suiteId": "suite-lint-shim",
  "missions": [
    { "missionId": "m1", "prompt": "Solve mission and return proof." }
  ]
}`)
	specPath := filepath.Join(specDir, "campaign.yaml")
	if err := os.WriteFile(specPath, []byte(`
schemaVersion: 1
campaignId: cmp-lint-shim
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
		t.Fatalf("write campaign spec: %v", err)
	}
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	r := Runner{
		Version: "0.0.0-dev",
		Now:     func() time.Time { return time.Date(2026, 2, 22, 18, 5, 0, 0, time.UTC) },
		Stdout:  &stdout,
		Stderr:  &stderr,
	}
	code := r.Run([]string{"campaign", "lint", "--spec", specPath, "--json"})
	if code != 2 {
		t.Fatalf("campaign lint expected 2, got %d stderr=%q", code, stderr.String())
	}
	var out struct {
		OK        bool   `json:"ok"`
		Code      string `json:"code"`
		Violation struct {
			FlowID        string   `json:"flowId"`
			RequiredOneOf []string `json:"requiredOneOf"`
			Snippet       string   `json:"snippet"`
		} `json:"violation"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal campaign lint output: %v stdout=%q", err, stdout.String())
	}
	if out.OK || out.Code != "ZCL_E_CAMPAIGN_TOOL_DRIVER_SHIM_REQUIRED" {
		t.Fatalf("unexpected campaign lint shim output: %+v", out)
	}
	if out.Violation.FlowID != "flow-a" || len(out.Violation.RequiredOneOf) != 2 || !strings.Contains(out.Violation.Snippet, "runner.toolDriver.shims") {
		t.Fatalf("unexpected shim violation payload: %+v", out.Violation)
	}
}

func TestCampaignLint_ToolPolicyConfigViolationStructured(t *testing.T) {
	specDir := t.TempDir()
	suitePath := filepath.Join(specDir, "suite.json")
	writeSuiteFile(t, suitePath, `{
  "version": 1,
  "suiteId": "suite-lint-tool-policy",
  "missions": [
    { "missionId": "m1", "prompt": "p1" }
  ]
}`)
	specPath := filepath.Join(specDir, "campaign.yaml")
	if err := os.WriteFile(specPath, []byte(`
schemaVersion: 1
campaignId: cmp-lint-tool-policy
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
		t.Fatalf("write campaign spec: %v", err)
	}
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	r := Runner{
		Version: "0.0.0-dev",
		Now:     func() time.Time { return time.Date(2026, 2, 22, 18, 7, 0, 0, time.UTC) },
		Stdout:  &stdout,
		Stderr:  &stderr,
	}
	code := r.Run([]string{"campaign", "lint", "--spec", specPath, "--json"})
	if code != 2 {
		t.Fatalf("campaign lint expected 2, got %d stderr=%q", code, stderr.String())
	}
	var out struct {
		OK        bool   `json:"ok"`
		Code      string `json:"code"`
		Violation struct {
			FlowID      string `json:"flowId"`
			Description string `json:"description"`
		} `json:"violation"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal campaign lint output: %v stdout=%q", err, stdout.String())
	}
	if out.OK || out.Code != "ZCL_E_CAMPAIGN_TOOL_POLICY_INVALID" {
		t.Fatalf("unexpected campaign lint toolPolicy output: %+v", out)
	}
	if out.Violation.FlowID != "flow-a" || !strings.Contains(out.Violation.Description, "namespace and/or prefix") {
		t.Fatalf("unexpected toolPolicy violation payload: %+v", out.Violation)
	}
}

func TestCampaignLint_ExecutionModeAdapterScript(t *testing.T) {
	specDir := t.TempDir()
	suitePath := filepath.Join(specDir, "suite.json")
	writeSuiteFile(t, suitePath, `{
  "version": 1,
  "suiteId": "suite-lint-execution-mode",
  "missions": [
    { "missionId": "m1", "prompt": "p1" }
  ]
}`)
	scriptPath := filepath.Join(specDir, "runner.sh")
	if err := os.WriteFile(scriptPath, []byte("#!/usr/bin/env bash\necho ok\n"), 0o755); err != nil {
		t.Fatalf("write script: %v", err)
	}
	specPath := filepath.Join(specDir, "campaign.yaml")
	if err := os.WriteFile(specPath, []byte(`
schemaVersion: 1
campaignId: cmp-lint-execution-mode
promptMode: mission_only
flows:
  - flowId: flow-a
    suiteFile: suite.json
    runner:
      type: process_cmd
      command: ["bash", "-lc", "./runner.sh"]
      finalization:
        mode: auto_from_result_json
        resultChannel:
          kind: file_json
`), 0o644); err != nil {
		t.Fatalf("write campaign spec: %v", err)
	}
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	r := Runner{
		Version: "0.0.0-dev",
		Now:     func() time.Time { return time.Date(2026, 2, 22, 18, 8, 0, 0, time.UTC) },
		Stdout:  &stdout,
		Stderr:  &stderr,
	}
	code := r.Run([]string{"campaign", "lint", "--spec", specPath, "--json"})
	if code != 0 {
		t.Fatalf("campaign lint expected 0, got %d stderr=%q", code, stderr.String())
	}
	var out struct {
		ExecutionMode struct {
			Mode               string   `json:"mode"`
			AdapterScriptFlows []string `json:"adapterScriptFlows"`
		} `json:"executionMode"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal campaign lint execution mode output: %v stdout=%q", err, stdout.String())
	}
	if out.ExecutionMode.Mode != "adapter_script" {
		t.Fatalf("expected executionMode=adapter_script, got %+v", out.ExecutionMode)
	}
	if len(out.ExecutionMode.AdapterScriptFlows) != 1 || out.ExecutionMode.AdapterScriptFlows[0] != "flow-a" {
		t.Fatalf("unexpected adapter script flows: %+v", out.ExecutionMode.AdapterScriptFlows)
	}
}

func TestCampaignRun_MissionOnlyViolationReturnsTypedCode(t *testing.T) {
	specDir := t.TempDir()
	suitePath := filepath.Join(specDir, "suite.json")
	writeSuiteFile(t, suitePath, `{
  "version": 1,
  "suiteId": "suite-run-mission-only",
  "missions": [
    { "missionId": "m1", "prompt": "Solve mission and call zcl feedback." }
  ]
}`)
	specPath := filepath.Join(specDir, "campaign.yaml")
	if err := os.WriteFile(specPath, []byte(`
schemaVersion: 1
campaignId: cmp-run-mission-only
promptMode: mission_only
flows:
  - flowId: flow-a
    suiteFile: suite.json
    runner:
      type: process_cmd
      command: ["echo","ok"]
      finalization:
        mode: auto_from_result_json
        resultChannel:
          kind: file_json
`), 0o644); err != nil {
		t.Fatalf("write campaign spec: %v", err)
	}
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	r := Runner{
		Version: "0.0.0-dev",
		Now:     func() time.Time { return time.Date(2026, 2, 22, 18, 10, 0, 0, time.UTC) },
		Stdout:  &stdout,
		Stderr:  &stderr,
	}
	code := r.Run([]string{"campaign", "run", "--spec", specPath, "--json"})
	if code != 2 {
		t.Fatalf("campaign run expected 2, got %d stderr=%q", code, stderr.String())
	}
	var out struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal campaign run policy output: %v stdout=%q", err, stdout.String())
	}
	if out.Code != "ZCL_E_CAMPAIGN_PROMPT_MODE_VIOLATION" {
		t.Fatalf("expected prompt mode policy code, got %+v", out)
	}
}

func TestCampaignDoctor_DetectsLockAndReportsPreflight(t *testing.T) {
	outRoot := t.TempDir()
	specDir := t.TempDir()
	suitePath := filepath.Join(specDir, "suite.json")
	writeSuiteFile(t, suitePath, `{
  "version": 1,
  "suiteId": "campaign-suite-doctor",
  "missions": [
    { "missionId": "m1", "prompt": "p1" }
  ]
}`)
	specPath := filepath.Join(specDir, "campaign.yaml")
	if err := os.WriteFile(specPath, []byte(fmt.Sprintf(`
schemaVersion: 1
campaignId: cmp-doctor
outRoot: %q
flows:
  - flowId: flow-a
    suiteFile: suite.json
    runner:
      type: process_cmd
      command: ["%s", "-test.run=TestHelperSuiteRunnerProcess$", "--", "case=ok"]
`, outRoot, os.Args[0])), 0o644); err != nil {
		t.Fatalf("write campaign spec: %v", err)
	}
	lockDir := filepath.Join(outRoot, "campaigns", "cmp-doctor", "campaign.lock")
	if err := os.MkdirAll(lockDir, 0o755); err != nil {
		t.Fatalf("mkdir lock dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(lockDir, "owner.json"), []byte(`{"v":1,"pid":12345}`), 0o644); err != nil {
		t.Fatalf("write lock owner: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	r := Runner{
		Version: "0.0.0-dev",
		Now:     func() time.Time { return time.Date(2026, 2, 22, 19, 0, 0, 0, time.UTC) },
		Stdout:  &stdout,
		Stderr:  &stderr,
	}
	code := r.Run([]string{"campaign", "doctor", "--spec", specPath, "--json"})
	if code != 2 {
		t.Fatalf("campaign doctor expected 2 with lock present, got %d stderr=%q", code, stderr.String())
	}
	var out struct {
		OK     bool `json:"ok"`
		Checks []struct {
			ID string `json:"id"`
			OK bool   `json:"ok"`
		} `json:"checks"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal campaign doctor output: %v stdout=%q", err, stdout.String())
	}
	if out.OK {
		t.Fatalf("expected campaign doctor ok=false when lock exists")
	}
	foundLockFail := false
	for _, c := range out.Checks {
		if c.ID == "campaign_lock" && !c.OK {
			foundLockFail = true
			break
		}
	}
	if !foundLockFail {
		t.Fatalf("expected campaign_lock check failure, got %+v", out.Checks)
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

func TestCampaignRun_MissionWindowAppliedOnce(t *testing.T) {
	outRoot := t.TempDir()
	specDir := t.TempDir()
	suitePath := filepath.Join(specDir, "suite.json")
	writeSuiteFile(t, suitePath, `{
  "version": 1,
  "suiteId": "campaign-suite-window",
  "missions": [
    { "missionId": "m1", "prompt": "p1", "expects": { "ok": true } },
    { "missionId": "m2", "prompt": "p2", "expects": { "ok": true } },
    { "missionId": "m3", "prompt": "p3", "expects": { "ok": true } },
    { "missionId": "m4", "prompt": "p4", "expects": { "ok": true } },
    { "missionId": "m5", "prompt": "p5", "expects": { "ok": true } },
    { "missionId": "m6", "prompt": "p6", "expects": { "ok": true } },
    { "missionId": "m7", "prompt": "p7", "expects": { "ok": true } },
    { "missionId": "m8", "prompt": "p8", "expects": { "ok": true } },
    { "missionId": "m9", "prompt": "p9", "expects": { "ok": true } },
    { "missionId": "m10", "prompt": "p10", "expects": { "ok": true } }
  ]
}`)
	specPath := filepath.Join(specDir, "campaign.yaml")
	if err := os.WriteFile(specPath, []byte(`
schemaVersion: 1
campaignId: cmp-window
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
		Now:     func() time.Time { return time.Date(2026, 2, 22, 14, 30, 0, 0, time.UTC) },
		Stdout:  &stdout,
		Stderr:  &stderr,
	}
	code := r.Run([]string{
		"campaign", "run",
		"--spec", specPath,
		"--out-root", outRoot,
		"--mission-offset", "6",
		"--missions", "3",
		"--json",
	})
	if code != 0 {
		t.Fatalf("campaign run expected 0, got %d stderr=%q", code, stderr.String())
	}
	var run struct {
		Status string `json:"status"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &run); err != nil {
		t.Fatalf("unmarshal campaign run window json: %v stdout=%q", err, stdout.String())
	}
	if run.Status != "valid" {
		t.Fatalf("expected valid status, got %+v", run)
	}

	stateRaw, err := os.ReadFile(filepath.Join(outRoot, "campaigns", "cmp-window", "campaign.run.state.json"))
	if err != nil {
		t.Fatalf("read campaign run state: %v", err)
	}
	var st struct {
		TotalMissions int `json:"totalMissions"`
		MissionGates  []struct {
			MissionIndex int `json:"missionIndex"`
		} `json:"missionGates"`
	}
	if err := json.Unmarshal(stateRaw, &st); err != nil {
		t.Fatalf("unmarshal campaign run state: %v", err)
	}
	if st.TotalMissions != 3 {
		t.Fatalf("expected totalMissions=3, got %d", st.TotalMissions)
	}
	if len(st.MissionGates) != 3 {
		t.Fatalf("expected 3 mission gates, got %d", len(st.MissionGates))
	}
	got := []int{st.MissionGates[0].MissionIndex, st.MissionGates[1].MissionIndex, st.MissionGates[2].MissionIndex}
	want := []int{6, 7, 8}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("mission gate indexes mismatch: got=%v want=%v", got, want)
		}
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

func TestCampaignRun_RuntimeEnvPromptMetadataPerFlow(t *testing.T) {
	outRoot := t.TempDir()
	specDir := t.TempDir()
	missionDefault := filepath.Join(specDir, "missions-default")
	missionFlow := filepath.Join(specDir, "missions-flow")
	for _, p := range []string{missionDefault, missionFlow} {
		if err := os.MkdirAll(p, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", p, err)
		}
	}
	if err := os.WriteFile(filepath.Join(missionDefault, "m1.md"), []byte("default prompt"), 0o644); err != nil {
		t.Fatalf("write default prompt: %v", err)
	}
	if err := os.WriteFile(filepath.Join(missionFlow, "m1.md"), []byte("flow prompt"), 0o644); err != nil {
		t.Fatalf("write flow prompt: %v", err)
	}
	templatePath := filepath.Join(specDir, "prompt-template.txt")
	if err := os.WriteFile(templatePath, []byte("flow={{flowId}} prompt={{prompt}}"), 0o644); err != nil {
		t.Fatalf("write template: %v", err)
	}
	specPath := filepath.Join(specDir, "campaign.yaml")
	if err := os.WriteFile(specPath, []byte(`
schemaVersion: 1
campaignId: cmp-runtime-env-prompt-meta
missionSource:
  path: missions-default
pairGate:
  enabled: true
semantic:
  enabled: false
flows:
  - flowId: flow-a
    promptSource:
      path: missions-flow
    promptTemplate:
      path: prompt-template.txt
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
		Now:     func() time.Time { return time.Date(2026, 2, 22, 15, 20, 0, 0, time.UTC) },
		Stdout:  &stdout,
		Stderr:  &stderr,
	}
	code := r.Run([]string{"campaign", "run", "--spec", specPath, "--out-root", outRoot, "--json"})
	if code != 0 {
		t.Fatalf("campaign run expected 0, got %d stderr=%q", code, stderr.String())
	}
	stateRaw, err := os.ReadFile(filepath.Join(outRoot, "campaigns", "cmp-runtime-env-prompt-meta", "campaign.run.state.json"))
	if err != nil {
		t.Fatalf("read campaign state: %v", err)
	}
	var st struct {
		Status   string `json:"status"`
		FlowRuns []struct {
			FlowID   string `json:"flowId"`
			Attempts []struct {
				AttemptDir string `json:"attemptDir"`
			} `json:"attempts"`
		} `json:"flowRuns"`
	}
	if err := json.Unmarshal(stateRaw, &st); err != nil {
		t.Fatalf("unmarshal campaign state: %v", err)
	}
	if st.Status != "valid" || len(st.FlowRuns) != 1 || len(st.FlowRuns[0].Attempts) != 1 {
		t.Fatalf("unexpected campaign state: %+v", st)
	}
	attemptDir := st.FlowRuns[0].Attempts[0].AttemptDir
	if strings.TrimSpace(attemptDir) == "" {
		t.Fatalf("expected attemptDir in campaign state")
	}
	runtimeEnvRaw, err := os.ReadFile(filepath.Join(attemptDir, "attempt.runtime.env.json"))
	if err != nil {
		t.Fatalf("read attempt.runtime.env.json: %v", err)
	}
	var runtimeEnv struct {
		Prompt struct {
			SourceKind   string `json:"sourceKind"`
			SourcePath   string `json:"sourcePath"`
			TemplatePath string `json:"templatePath"`
		} `json:"prompt"`
		Env struct {
			Explicit map[string]string `json:"explicit"`
		} `json:"env"`
	}
	if err := json.Unmarshal(runtimeEnvRaw, &runtimeEnv); err != nil {
		t.Fatalf("unmarshal attempt.runtime.env.json: %v", err)
	}
	if runtimeEnv.Prompt.SourceKind != "flow_prompt_template" {
		t.Fatalf("unexpected prompt source kind: %+v", runtimeEnv.Prompt)
	}
	if !strings.HasSuffix(runtimeEnv.Prompt.SourcePath, filepath.Join("missions-flow")) {
		t.Fatalf("unexpected prompt source path: %+v", runtimeEnv.Prompt)
	}
	if !strings.HasSuffix(runtimeEnv.Prompt.TemplatePath, filepath.Join("prompt-template.txt")) {
		t.Fatalf("unexpected prompt template path: %+v", runtimeEnv.Prompt)
	}
	if runtimeEnv.Env.Explicit["ZCL_FLOW_ID"] != "flow-a" {
		t.Fatalf("expected ZCL_FLOW_ID in runtime env explicit payload, got %+v", runtimeEnv.Env.Explicit)
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

func TestCampaignRun_ToolPolicyViolationFailsWithoutPairGate(t *testing.T) {
	outRoot := t.TempDir()
	specDir := t.TempDir()
	suitePath := filepath.Join(specDir, "suite.json")
	writeSuiteFile(t, suitePath, `{
  "version": 1,
  "suiteId": "suite-tool-policy-hard",
  "missions": [
    { "missionId": "m1", "prompt": "p1", "expects": { "ok": true } }
  ]
}`)
	specPath := filepath.Join(specDir, "campaign.yaml")
	if err := os.WriteFile(specPath, []byte(`
schemaVersion: 1
campaignId: cmp-tool-policy-hard
pairGate:
  enabled: false
semantic:
  enabled: false
flows:
  - flowId: flow-a
    suiteFile: suite.json
    toolPolicy:
      allow:
        - namespace: mcp
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
		Now:     func() time.Time { return time.Date(2026, 2, 22, 16, 5, 0, 0, time.UTC) },
		Stdout:  &stdout,
		Stderr:  &stderr,
	}
	code := r.Run([]string{"campaign", "run", "--spec", specPath, "--out-root", outRoot, "--json"})
	if code != 2 {
		t.Fatalf("expected invalid campaign exit 2, got %d stderr=%q", code, stderr.String())
	}
	stateRaw, err := os.ReadFile(filepath.Join(outRoot, "campaigns", "cmp-tool-policy-hard", "campaign.run.state.json"))
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
	if !strings.Contains(strings.Join(st.MissionGates[0].Reasons, ","), "ZCL_E_CAMPAIGN_TOOL_POLICY_VIOLATION") {
		t.Fatalf("expected tool policy gate reason, got %+v", st.MissionGates[0].Reasons)
	}
}

func TestCampaignRun_AdapterParityConformance(t *testing.T) {
	outRoot := t.TempDir()
	specDir := t.TempDir()
	suitePath := filepath.Join(specDir, "suite.json")
	writeSuiteFile(t, suitePath, `{
  "version": 1,
  "suiteId": "suite-adapter-parity",
  "missions": [
    { "missionId": "m1", "prompt": "p1", "expects": { "ok": true } }
  ]
}`)
	specPath := filepath.Join(specDir, "campaign.yaml")
	if err := os.WriteFile(specPath, []byte(`
schemaVersion: 1
campaignId: cmp-adapter-parity
semantic:
  enabled: false
flows:
  - flowId: flow-process
    suiteFile: suite.json
    runner:
      type: process_cmd
      command: ["`+os.Args[0]+`", "-test.run=TestHelperSuiteRunnerProcess$", "--", "case=ok"]
  - flowId: flow-codex-exec
    suiteFile: suite.json
    runner:
      type: codex_exec
      command: ["`+os.Args[0]+`", "-test.run=TestHelperSuiteRunnerProcess$", "--", "case=ok"]
  - flowId: flow-codex-sub
    suiteFile: suite.json
    runner:
      type: codex_subagent
      command: ["`+os.Args[0]+`", "-test.run=TestHelperSuiteRunnerProcess$", "--", "case=ok"]
  - flowId: flow-claude-sub
    suiteFile: suite.json
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
		Now:     func() time.Time { return time.Date(2026, 2, 22, 16, 30, 0, 0, time.UTC) },
		Stdout:  &stdout,
		Stderr:  &stderr,
	}
	code := r.Run([]string{"campaign", "run", "--spec", specPath, "--out-root", outRoot, "--json"})
	if code != 0 {
		t.Fatalf("campaign run expected 0, got %d stderr=%q", code, stderr.String())
	}
	stateRaw, err := os.ReadFile(filepath.Join(outRoot, "campaigns", "cmp-adapter-parity", "campaign.run.state.json"))
	if err != nil {
		t.Fatalf("read campaign state: %v", err)
	}
	var st struct {
		Status   string `json:"status"`
		FlowRuns []struct {
			RunnerType string `json:"runnerType"`
			Attempts   []struct {
				Status string   `json:"status"`
				Errors []string `json:"errors"`
			} `json:"attempts"`
		} `json:"flowRuns"`
	}
	if err := json.Unmarshal(stateRaw, &st); err != nil {
		t.Fatalf("unmarshal campaign state: %v", err)
	}
	if st.Status != "valid" || len(st.FlowRuns) != 4 {
		t.Fatalf("unexpected campaign state: %+v", st)
	}
	signatures := map[string]int{}
	for _, fr := range st.FlowRuns {
		if len(fr.Attempts) != 1 {
			t.Fatalf("expected one attempt per flow, got %+v", fr)
		}
		sig := fr.Attempts[0].Status + "|" + strings.Join(fr.Attempts[0].Errors, ",")
		signatures[sig]++
	}
	if len(signatures) != 1 {
		t.Fatalf("expected identical adapter outcome signatures, got %+v", signatures)
	}
	for sig := range signatures {
		if sig != "valid|" {
			t.Fatalf("expected valid adapter signature, got %q", sig)
		}
	}
}

func TestCampaignPublishCheck_FailsMissionOnlyPromptLeak(t *testing.T) {
	outRoot := t.TempDir()
	specDir := t.TempDir()
	suitePath := filepath.Join(specDir, "suite.json")
	writeSuiteFile(t, suitePath, `{
  "version": 1,
  "suiteId": "suite-no-context",
  "missions": [
    { "missionId": "m1", "prompt": "Solve the mission and return proof JSON." }
  ]
}`)
	specPath := filepath.Join(specDir, "campaign.yaml")
	if err := os.WriteFile(specPath, []byte(`
schemaVersion: 1
campaignId: cmp-no-context
promptMode: mission_only
flows:
  - flowId: flow-a
    suiteFile: suite.json
    runner:
      type: process_cmd
      command: ["`+os.Args[0]+`", "-test.run=TestHelperSuiteRunnerProcess$", "--", "case=result-file-ok"]
      toolDriver:
        kind: shell
      finalization:
        mode: auto_from_result_json
        resultChannel:
          kind: file_json
`), 0o644); err != nil {
		t.Fatalf("write campaign spec: %v", err)
	}
	t.Setenv("ZCL_WANT_SUITE_RUNNER", "1")

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	r := Runner{
		Version: "0.0.0-dev",
		Now:     func() time.Time { return time.Date(2026, 2, 22, 16, 40, 0, 0, time.UTC) },
		Stdout:  &stdout,
		Stderr:  &stderr,
	}
	code := r.Run([]string{"campaign", "run", "--spec", specPath, "--out-root", outRoot, "--json"})
	if code != 0 {
		t.Fatalf("campaign run expected 0, got %d stderr=%q", code, stderr.String())
	}

	// Simulate accidental harness leakage introduced after run.
	writeSuiteFile(t, suitePath, `{
  "version": 1,
  "suiteId": "suite-no-context",
  "missions": [
    { "missionId": "m1", "prompt": "Solve mission and call zcl feedback with result." }
  ]
}`)

	stdout.Reset()
	stderr.Reset()
	code = r.Run([]string{"campaign", "publish-check", "--campaign-id", "cmp-no-context", "--out-root", outRoot, "--json"})
	if code != 2 {
		t.Fatalf("expected publish-check failure, got %d stderr=%q", code, stderr.String())
	}
	var out struct {
		OK          bool     `json:"ok"`
		ReasonCodes []string `json:"reasonCodes"`
		Compliance  struct {
			OK   bool   `json:"ok"`
			Code string `json:"code"`
		} `json:"promptModeCompliance"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal publish-check output: %v stdout=%q", err, stdout.String())
	}
	if out.OK || out.Compliance.OK {
		t.Fatalf("expected mission-only prompt compliance failure, got %+v", out)
	}
	if out.Compliance.Code != "ZCL_E_CAMPAIGN_PROMPT_MODE_VIOLATION" {
		t.Fatalf("expected prompt mode compliance code, got %+v", out.Compliance)
	}
	if !strings.Contains(strings.Join(out.ReasonCodes, ","), "ZCL_E_CAMPAIGN_PROMPT_MODE_VIOLATION") {
		t.Fatalf("expected prompt mode reason code, got %+v", out.ReasonCodes)
	}
}

func TestCampaignLint_ExamModeRequiresEvaluatorTyped(t *testing.T) {
	specDir := t.TempDir()
	promptDir := filepath.Join(specDir, "prompts")
	oracleDir := filepath.Join(specDir, "oracles")
	if err := os.MkdirAll(promptDir, 0o755); err != nil {
		t.Fatalf("mkdir prompts: %v", err)
	}
	if err := os.MkdirAll(oracleDir, 0o755); err != nil {
		t.Fatalf("mkdir oracles: %v", err)
	}
	if err := os.WriteFile(filepath.Join(promptDir, "m1.md"), []byte("Do task and return result JSON."), 0o644); err != nil {
		t.Fatalf("write prompt: %v", err)
	}
	if err := os.WriteFile(filepath.Join(oracleDir, "m1.md"), []byte("oracle content"), 0o644); err != nil {
		t.Fatalf("write oracle: %v", err)
	}
	specPath := filepath.Join(specDir, "campaign.yaml")
	if err := os.WriteFile(specPath, []byte(`
schemaVersion: 1
campaignId: cmp-exam-lint
promptMode: exam
missionSource:
  promptSource:
    path: prompts
  oracleSource:
    path: oracles
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
		t.Fatalf("write campaign spec: %v", err)
	}
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	r := Runner{
		Version: "0.0.0-dev",
		Now:     func() time.Time { return time.Date(2026, 2, 22, 20, 0, 0, 0, time.UTC) },
		Stdout:  &stdout,
		Stderr:  &stderr,
	}
	code := r.Run([]string{"campaign", "lint", "--spec", specPath, "--json"})
	if code != 2 {
		t.Fatalf("campaign lint expected 2, got %d stderr=%q", code, stderr.String())
	}
	var out struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal campaign lint output: %v stdout=%q", err, stdout.String())
	}
	if out.Code != "ZCL_E_CAMPAIGN_ORACLE_EVALUATOR_REQUIRED" {
		t.Fatalf("unexpected code: %+v", out)
	}
}

func TestCampaignRun_ExamModeOracleEvaluatorFailureInvalidatesAttempt(t *testing.T) {
	outRoot := t.TempDir()
	specDir := t.TempDir()
	promptDir := filepath.Join(specDir, "prompts")
	oracleDir := filepath.Join(specDir, "oracles")
	if err := os.MkdirAll(promptDir, 0o755); err != nil {
		t.Fatalf("mkdir prompts: %v", err)
	}
	if err := os.MkdirAll(oracleDir, 0o755); err != nil {
		t.Fatalf("mkdir oracles: %v", err)
	}
	if err := os.WriteFile(filepath.Join(promptDir, "m1.md"), []byte("Solve the task and return proof JSON."), 0o644); err != nil {
		t.Fatalf("write prompt: %v", err)
	}
	if err := os.WriteFile(filepath.Join(oracleDir, "m1.md"), []byte("expected title: hello"), 0o644); err != nil {
		t.Fatalf("write oracle: %v", err)
	}
	specPath := filepath.Join(specDir, "campaign.yaml")
	if err := os.WriteFile(specPath, []byte(`
schemaVersion: 1
campaignId: cmp-exam-run
promptMode: exam
missionSource:
  promptSource:
    path: prompts
  oracleSource:
    path: oracles
    visibility: workspace
evaluation:
  mode: oracle
  evaluator:
    kind: script
    command: ["`+os.Args[0]+`", "-test.run=TestHelperCampaignOracleEvaluator$", "--", "case=fail"]
flows:
  - flowId: flow-a
    runner:
      type: process_cmd
      command: ["`+os.Args[0]+`", "-test.run=TestHelperSuiteRunnerProcess$", "--", "case=result-file-ok"]
      finalization:
        mode: auto_from_result_json
        resultChannel:
          kind: file_json
`), 0o644); err != nil {
		t.Fatalf("write campaign spec: %v", err)
	}
	t.Setenv("ZCL_WANT_SUITE_RUNNER", "1")
	t.Setenv("ZCL_WANT_CAMPAIGN_ORACLE_EVAL", "1")

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	r := Runner{
		Version: "0.0.0-dev",
		Now:     func() time.Time { return time.Date(2026, 2, 22, 20, 10, 0, 0, time.UTC) },
		Stdout:  &stdout,
		Stderr:  &stderr,
	}
	code := r.Run([]string{"campaign", "run", "--spec", specPath, "--out-root", outRoot, "--json"})
	if code != 2 {
		t.Fatalf("campaign run expected 2 for oracle fail, got %d stderr=%q", code, stderr.String())
	}
	statePath := filepath.Join(outRoot, "campaigns", "cmp-exam-run", "campaign.run.state.json")
	stateRaw, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatalf("read state: %v", err)
	}
	var st struct {
		Status       string `json:"status"`
		MissionGates []struct {
			Reasons []string `json:"reasons"`
		} `json:"missionGates"`
		FlowRuns []struct {
			Attempts []struct {
				AttemptDir string   `json:"attemptDir"`
				Status     string   `json:"status"`
				Errors     []string `json:"errors"`
			} `json:"attempts"`
		} `json:"flowRuns"`
	}
	if err := json.Unmarshal(stateRaw, &st); err != nil {
		t.Fatalf("unmarshal state: %v", err)
	}
	if st.Status != "invalid" {
		t.Fatalf("expected invalid campaign status, got %+v", st)
	}
	if len(st.MissionGates) != 1 || !strings.Contains(strings.Join(st.MissionGates[0].Reasons, ","), "ZCL_E_CAMPAIGN_ORACLE_EVALUATION_FAILED") {
		t.Fatalf("expected oracle evaluation failure reason, got %+v", st.MissionGates)
	}
	if len(st.FlowRuns) == 0 || len(st.FlowRuns[0].Attempts) == 0 {
		t.Fatalf("expected flow attempt data in state: %+v", st)
	}
	attemptDir := st.FlowRuns[0].Attempts[0].AttemptDir
	if attemptDir == "" {
		t.Fatalf("expected attemptDir in run state: %+v", st.FlowRuns[0].Attempts[0])
	}
	verdictRaw, err := os.ReadFile(filepath.Join(attemptDir, "oracle.verdict.json"))
	if err != nil {
		t.Fatalf("read oracle verdict artifact: %v", err)
	}
	var verdict struct {
		OK          bool     `json:"ok"`
		ReasonCodes []string `json:"reasonCodes"`
	}
	if err := json.Unmarshal(verdictRaw, &verdict); err != nil {
		t.Fatalf("unmarshal oracle verdict: %v", err)
	}
	if verdict.OK || !strings.Contains(strings.Join(verdict.ReasonCodes, ","), "ZCL_E_CAMPAIGN_ORACLE_EVALUATION_FAILED") {
		t.Fatalf("unexpected oracle verdict: %+v", verdict)
	}
}

func TestCampaignRun_ExamModeFormatOnlyMismatchCanWarnNonGating(t *testing.T) {
	outRoot := t.TempDir()
	specDir := t.TempDir()
	promptDir := filepath.Join(specDir, "prompts")
	oracleDir := filepath.Join(specDir, "oracles")
	if err := os.MkdirAll(promptDir, 0o755); err != nil {
		t.Fatalf("mkdir prompts: %v", err)
	}
	if err := os.MkdirAll(oracleDir, 0o755); err != nil {
		t.Fatalf("mkdir oracles: %v", err)
	}
	if err := os.WriteFile(filepath.Join(promptDir, "m1.md"), []byte("Solve the task and return proof JSON."), 0o644); err != nil {
		t.Fatalf("write prompt: %v", err)
	}
	if err := os.WriteFile(filepath.Join(oracleDir, "m1.md"), []byte("expected"), 0o644); err != nil {
		t.Fatalf("write oracle: %v", err)
	}
	specPath := filepath.Join(specDir, "campaign.yaml")
	if err := os.WriteFile(specPath, []byte(`
schemaVersion: 1
campaignId: cmp-exam-format-warn
promptMode: exam
missionSource:
  promptSource:
    path: prompts
  oracleSource:
    path: oracles
    visibility: workspace
evaluation:
  mode: oracle
  oraclePolicy:
    mode: normalized
    formatMismatch: warn
  evaluator:
    kind: script
    command: ["`+os.Args[0]+`", "-test.run=TestHelperCampaignOracleEvaluator$", "--", "case=fail_format"]
flows:
  - flowId: flow-a
    runner:
      type: process_cmd
      command: ["`+os.Args[0]+`", "-test.run=TestHelperSuiteRunnerProcess$", "--", "case=result-file-ok"]
      finalization:
        mode: auto_from_result_json
        resultChannel:
          kind: file_json
`), 0o644); err != nil {
		t.Fatalf("write campaign spec: %v", err)
	}
	t.Setenv("ZCL_WANT_SUITE_RUNNER", "1")
	t.Setenv("ZCL_WANT_CAMPAIGN_ORACLE_EVAL", "1")

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	r := Runner{
		Version: "0.0.0-dev",
		Now:     func() time.Time { return time.Date(2026, 2, 22, 20, 20, 0, 0, time.UTC) },
		Stdout:  &stdout,
		Stderr:  &stderr,
	}
	code := r.Run([]string{"campaign", "run", "--spec", specPath, "--out-root", outRoot, "--json"})
	if code != 0 {
		t.Fatalf("campaign run expected 0 for format-only warn, got %d stderr=%q", code, stderr.String())
	}
	var st struct {
		Status   string `json:"status"`
		FlowRuns []struct {
			Attempts []struct {
				AttemptDir string `json:"attemptDir"`
			} `json:"attempts"`
		} `json:"flowRuns"`
		ReasonCodes []string `json:"reasonCodes"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &st); err != nil {
		t.Fatalf("unmarshal run json: %v stdout=%q", err, stdout.String())
	}
	if st.Status != "valid" {
		t.Fatalf("expected valid status for non-gating format mismatch, got %+v", st)
	}
	attemptDir := st.FlowRuns[0].Attempts[0].AttemptDir
	verdictRaw, err := os.ReadFile(filepath.Join(attemptDir, "oracle.verdict.json"))
	if err != nil {
		t.Fatalf("read oracle verdict artifact: %v", err)
	}
	var verdict struct {
		OK                bool   `json:"ok"`
		PolicyDisposition string `json:"policyDisposition"`
		Mismatches        []struct {
			MismatchClass string `json:"mismatchClass"`
		} `json:"mismatches"`
	}
	if err := json.Unmarshal(verdictRaw, &verdict); err != nil {
		t.Fatalf("unmarshal oracle verdict: %v", err)
	}
	if verdict.OK {
		t.Fatalf("expected evaluator raw failure preserved")
	}
	if verdict.PolicyDisposition != "warn" {
		t.Fatalf("expected warn policy disposition, got %+v", verdict)
	}
	if len(verdict.Mismatches) == 0 || verdict.Mismatches[0].MismatchClass != "format" {
		t.Fatalf("expected format mismatch details, got %+v", verdict)
	}
}

func TestHelperCampaignOracleEvaluator(t *testing.T) {
	if os.Getenv("ZCL_WANT_CAMPAIGN_ORACLE_EVAL") != "1" {
		return
	}
	args := os.Args
	idx := 0
	for i := range args {
		if args[i] == "--" {
			idx = i + 1
			break
		}
	}
	kind := "ok"
	for _, a := range args[idx:] {
		if strings.HasPrefix(a, "case=") {
			kind = strings.TrimPrefix(a, "case=")
		}
	}
	switch kind {
	case "fail":
		_, _ = os.Stdout.WriteString(`{"ok":false,"reasonCodes":["ZCL_E_CAMPAIGN_ORACLE_EVALUATION_FAILED"],"message":"oracle mismatch"}` + "\n")
		os.Exit(0)
	case "fail_format":
		_, _ = os.Stdout.WriteString(`{"ok":false,"reasonCodes":["ZCL_E_CAMPAIGN_ORACLE_EVALUATION_FAILED"],"message":"blogUrl expected \"https://blog.heftiweb.ch\" got \"https://blog.heftiweb.ch/\""}` + "\n")
		os.Exit(0)
	case "error":
		os.Exit(9)
	default:
		_, _ = os.Stdout.WriteString(`{"ok":true}` + "\n")
		os.Exit(0)
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
