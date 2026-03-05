package cli

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

func suiteRunNow() time.Time {
	return time.Date(2026, 2, 16, 12, 0, 0, 0, time.UTC)
}

func TestSuiteRun_OK_EndToEnd(t *testing.T) {
	TestSuiteRun_OK_EndToEndCore(t)
}

func TestSuiteRun_OK_EndToEndCore(t *testing.T) {
	outRoot := t.TempDir()
	suitePath := filepath.Join(t.TempDir(), "suite.json")
	writeSuiteFile(t, suitePath, `{
  "version": 1,
  "suiteId": "suite-run-smoke",
  "defaults": { "mode": "discovery", "timeoutMs": 60000 },
  "missions": [
    { "missionId": "m1", "prompt": "p1", "expects": { "ok": true } },
    { "missionId": "m2", "prompt": "p2", "expects": { "ok": true } }
  ]
}`)

	t.Setenv("ZCL_WANT_SUITE_RUNNER", "1")

	h := newRunnerHarness(t, suiteRunNow())

	code := h.Runner.Run([]string{
		"suite", "run",
		"--file", suitePath,
		"--out-root", outRoot,
		"--json",
		"--",
		os.Args[0], "-test.run=TestHelperSuiteRunnerProcess$", "--", "case=ok",
	})
	if code != 0 {
		t.Fatalf("expected exit code 0, got %d (stderr=%q)", code, h.Stderr.String())
	}

	sum := parseSuiteRunOKEndToEndSummary(t, h.Stdout.Bytes(), h.Stdout.String())
	assertSuiteRunOKEndToEndSummary(t, sum)
	assertSuiteRunOKEndToEndAttempts(t, sum.Attempts)
	assertSuiteRunOKEndToEndStderr(t, h.Stderr.String())
}

type suiteRunOKEndToEndSummary struct {
	OK       bool                       `json:"ok"`
	Passed   int                        `json:"passed"`
	Failed   int                        `json:"failed"`
	Attempts []suiteRunOKEndToEndRecord `json:"attempts"`
}

type suiteRunOKEndToEndRecord struct {
	MissionID      string `json:"missionId"`
	AttemptDir     string `json:"attemptDir"`
	RunnerExitCode *int   `json:"runnerExitCode"`
	Finish         struct {
		OK bool `json:"ok"`
	} `json:"finish"`
	OK bool `json:"ok"`
}

func parseSuiteRunOKEndToEndSummary(t *testing.T, stdout []byte, stdoutText string) suiteRunOKEndToEndSummary {
	t.Helper()
	var sum suiteRunOKEndToEndSummary
	if err := json.Unmarshal(stdout, &sum); err != nil {
		t.Fatalf("unmarshal suite run json: %v (stdout=%q)", err, stdoutText)
	}
	return sum
}

func assertSuiteRunOKEndToEndSummary(t *testing.T, sum suiteRunOKEndToEndSummary) {
	t.Helper()
	if !sum.OK || sum.Passed != 2 || sum.Failed != 0 || len(sum.Attempts) != 2 {
		t.Fatalf("unexpected summary: %+v", sum)
	}
}

func assertSuiteRunOKEndToEndAttempts(t *testing.T, attempts []suiteRunOKEndToEndRecord) {
	t.Helper()
	for _, attempt := range attempts {
		assertSuiteRunOKEndToEndAttempt(t, attempt)
	}
}

func assertSuiteRunOKEndToEndAttempt(t *testing.T, attempt suiteRunOKEndToEndRecord) {
	t.Helper()
	if attempt.RunnerExitCode == nil || *attempt.RunnerExitCode != 0 {
		t.Fatalf("expected runnerExitCode=0, got: %+v", attempt.RunnerExitCode)
	}
	if !attempt.Finish.OK || !attempt.OK {
		t.Fatalf("expected attempt ok, got: %+v", attempt)
	}
	if attempt.AttemptDir == "" {
		t.Fatalf("expected attemptDir in suite run JSON")
	}
	assertSuiteRunRunnerArtifactsExist(t, attempt.AttemptDir)
	assertSuiteRunProcessRuntimeEnvMetadata(t, attempt.AttemptDir)
	assertSuiteRunAttemptReportRuntimeEnvArtifact(t, attempt.AttemptDir)
}

func assertSuiteRunRunnerArtifactsExist(t *testing.T, attemptDir string) {
	t.Helper()
	for _, p := range []string{
		filepath.Join(attemptDir, "runner.command.txt"),
		filepath.Join(attemptDir, "runner.stdout.log"),
		filepath.Join(attemptDir, "runner.stderr.log"),
	} {
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("expected runner artifact %s, got err=%v", p, err)
		}
	}
}

func assertSuiteRunProcessRuntimeEnvMetadata(t *testing.T, attemptDir string) {
	t.Helper()
	runtimeEnvRaw, err := os.ReadFile(filepath.Join(attemptDir, "attempt.runtime.env.json"))
	if err != nil {
		t.Fatalf("read attempt.runtime.env.json: %v", err)
	}
	var runtimeEnv struct {
		Runtime struct {
			NativeMode   bool   `json:"nativeMode"`
			StartCwdMode string `json:"startCwdMode"`
			StartCwd     string `json:"startCwd"`
		} `json:"runtime"`
		Prompt struct {
			SourceKind string `json:"sourceKind"`
			SHA256     string `json:"sha256"`
			Bytes      int64  `json:"bytes"`
		} `json:"prompt"`
		Env struct {
			Explicit      map[string]string `json:"explicit"`
			EffectiveKeys []string          `json:"effectiveKeys"`
		} `json:"env"`
	}
	if err := json.Unmarshal(runtimeEnvRaw, &runtimeEnv); err != nil {
		t.Fatalf("unmarshal attempt.runtime.env.json: %v", err)
	}
	if runtimeEnv.Runtime.NativeMode {
		t.Fatalf("expected process-mode runtime env artifact for this test")
	}
	if runtimeEnv.Runtime.StartCwdMode != "inherit" || strings.TrimSpace(runtimeEnv.Runtime.StartCwd) == "" {
		t.Fatalf("unexpected runtime start cwd metadata: %+v", runtimeEnv.Runtime)
	}
	if runtimeEnv.Prompt.SourceKind != "suite_prompt" || runtimeEnv.Prompt.Bytes <= 0 || len(runtimeEnv.Prompt.SHA256) != 64 {
		t.Fatalf("unexpected prompt metadata: %+v", runtimeEnv.Prompt)
	}
	if strings.TrimSpace(runtimeEnv.Env.Explicit["ZCL_ATTEMPT_ID"]) == "" || len(runtimeEnv.Env.EffectiveKeys) == 0 {
		t.Fatalf("unexpected runtime env payload: %+v", runtimeEnv.Env)
	}
}

func assertSuiteRunAttemptReportRuntimeEnvArtifact(t *testing.T, attemptDir string) {
	t.Helper()
	repRaw, err := os.ReadFile(filepath.Join(attemptDir, "attempt.report.json"))
	if err != nil {
		t.Fatalf("read attempt.report.json: %v", err)
	}
	var rep struct {
		Artifacts struct {
			AttemptRuntimeEnvJSON string `json:"attemptRuntimeEnvJson"`
		} `json:"artifacts"`
	}
	if err := json.Unmarshal(repRaw, &rep); err != nil {
		t.Fatalf("unmarshal attempt.report.json: %v", err)
	}
	if rep.Artifacts.AttemptRuntimeEnvJSON != "attempt.runtime.env.json" {
		t.Fatalf("expected runtime env artifact in report, got %+v", rep.Artifacts)
	}
}

func assertSuiteRunOKEndToEndStderr(t *testing.T, stderr string) {
	t.Helper()
	if !strings.Contains(stderr, "suite run: mission=") {
		t.Fatalf("expected suite run progress lines in stderr, got: %q", stderr)
	}
	if !strings.Contains(stderr, "feedback: OK") {
		t.Fatalf("expected runner feedback output in stderr, got: %q", stderr)
	}
}

func TestSuiteRun_FailsWhenRunnerWritesNoFeedback(t *testing.T) {
	outRoot := t.TempDir()
	suitePath := filepath.Join(t.TempDir(), "suite.json")
	writeSuiteFile(t, suitePath, `{
  "version": 1,
  "suiteId": "suite-run-missing-feedback",
  "defaults": { "mode": "discovery", "timeoutMs": 60000 },
  "missions": [
    { "missionId": "m1", "prompt": "p1", "expects": { "ok": true } }
  ]
}`)

	t.Setenv("ZCL_WANT_SUITE_RUNNER", "1")

	h := newRunnerHarness(t, suiteRunNow())

	code := h.Runner.Run([]string{
		"suite", "run",
		"--file", suitePath,
		"--out-root", outRoot,
		"--json",
		"--",
		os.Args[0], "-test.run=TestHelperSuiteRunnerProcess$", "--", "case=no-feedback",
	})
	if code != 2 {
		t.Fatalf("expected exit code 2, got %d (stderr=%q)", code, h.Stderr.String())
	}

	var sum struct {
		OK       bool `json:"ok"`
		Failed   int  `json:"failed"`
		Attempts []struct {
			AutoFeedback     bool   `json:"autoFeedback"`
			AutoFeedbackCode string `json:"autoFeedbackCode"`
			Finish           struct {
				OK          bool `json:"ok"`
				ReportError *struct {
					Code string `json:"code"`
				} `json:"reportError"`
				Validate struct {
					OK     bool `json:"ok"`
					Errors []struct {
						Code string `json:"code"`
					} `json:"errors"`
				} `json:"validate"`
			} `json:"finish"`
		} `json:"attempts"`
	}
	if err := json.Unmarshal(h.Stdout.Bytes(), &sum); err != nil {
		t.Fatalf("unmarshal suite run json: %v (stdout=%q)", err, h.Stdout.String())
	}
	if sum.OK || sum.Failed != 1 || len(sum.Attempts) != 1 {
		t.Fatalf("unexpected summary: %+v", sum)
	}
	if sum.Attempts[0].Finish.OK {
		t.Fatalf("expected finish ok=false")
	}
	if !sum.Attempts[0].AutoFeedback || sum.Attempts[0].AutoFeedbackCode != "ZCL_E_MISSING_ARTIFACT" {
		t.Fatalf("expected synthetic missing-artifact feedback, got: %+v", sum.Attempts[0])
	}
	if sum.Attempts[0].Finish.ReportError != nil {
		t.Fatalf("expected report to be computable after synthetic feedback, got reportError=%+v", sum.Attempts[0].Finish.ReportError)
	}
	if !sum.Attempts[0].Finish.Validate.OK {
		t.Fatalf("expected validate.ok=true after synthetic feedback, got: %+v", sum.Attempts[0].Finish.Validate)
	}
}

func TestSuiteRun_FailFastSkipsRemainingMissions(t *testing.T) {
	outRoot := t.TempDir()
	suitePath := filepath.Join(t.TempDir(), "suite.json")
	writeSuiteFile(t, suitePath, `{
  "version": 1,
  "suiteId": "suite-run-fail-fast",
  "defaults": { "mode": "discovery", "timeoutMs": 60000 },
  "missions": [
    { "missionId": "m1", "prompt": "p1" },
    { "missionId": "m2", "prompt": "p2" }
  ]
}`)

	t.Setenv("ZCL_WANT_SUITE_RUNNER", "1")

	h := newRunnerHarness(t, suiteRunNow())

	code := h.Runner.Run([]string{
		"suite", "run",
		"--file", suitePath,
		"--out-root", outRoot,
		"--json",
		"--",
		os.Args[0], "-test.run=TestHelperSuiteRunnerProcess$", "--", "case=no-feedback",
	})
	if code != 2 {
		t.Fatalf("expected exit code 2, got %d (stderr=%q)", code, h.Stderr.String())
	}

	var sum struct {
		Failed   int `json:"failed"`
		Attempts []struct {
			MissionID  string `json:"missionId"`
			AttemptID  string `json:"attemptId"`
			Skipped    bool   `json:"skipped"`
			SkipReason string `json:"skipReason"`
		} `json:"attempts"`
	}
	if err := json.Unmarshal(h.Stdout.Bytes(), &sum); err != nil {
		t.Fatalf("unmarshal suite run json: %v (stdout=%q)", err, h.Stdout.String())
	}
	if len(sum.Attempts) != 2 {
		t.Fatalf("expected two attempts in summary, got %+v", sum.Attempts)
	}
	if sum.Attempts[0].MissionID != "m1" || sum.Attempts[0].AttemptID == "" || sum.Attempts[0].Skipped {
		t.Fatalf("unexpected first attempt: %+v", sum.Attempts[0])
	}
	if sum.Attempts[1].MissionID != "m2" || !sum.Attempts[1].Skipped || sum.Attempts[1].SkipReason == "" || sum.Attempts[1].AttemptID != "" {
		t.Fatalf("expected second attempt skipped by fail-fast, got: %+v", sum.Attempts[1])
	}
	if sum.Failed != 2 {
		t.Fatalf("expected failed=2 (failed + skipped), got: %+v", sum)
	}
}

func TestSuiteRun_BlindRejectsContaminatedPrompt(t *testing.T) {
	outRoot := t.TempDir()
	suitePath := filepath.Join(t.TempDir(), "suite.json")
	writeSuiteFile(t, suitePath, `{
  "version": 1,
  "suiteId": "suite-run-blind",
  "defaults": { "mode": "discovery", "timeoutMs": 60000 },
  "missions": [
    { "missionId": "m1", "prompt": "Use zcl feedback to report result" }
  ]
}`)

	t.Setenv("ZCL_WANT_SUITE_RUNNER", "1")

	h := newRunnerHarness(t, suiteRunNow())

	code := h.Runner.Run([]string{
		"suite", "run",
		"--file", suitePath,
		"--out-root", outRoot,
		"--blind", "on",
		"--json",
		"--",
		os.Args[0], "-test.run=TestHelperSuiteRunnerProcess$", "--", "case=ok",
	})
	if code != 2 {
		t.Fatalf("expected exit code 2, got %d (stderr=%q)", code, h.Stderr.String())
	}

	var sum struct {
		OK       bool `json:"ok"`
		Attempts []struct {
			RunnerErrorCode string `json:"runnerErrorCode"`
			AttemptDir      string `json:"attemptDir"`
			Finish          struct {
				OK     bool `json:"ok"`
				Report struct {
					Integrity struct {
						PromptContaminated bool `json:"promptContaminated"`
					} `json:"integrity"`
				} `json:"report"`
			} `json:"finish"`
		} `json:"attempts"`
	}
	if err := json.Unmarshal(h.Stdout.Bytes(), &sum); err != nil {
		t.Fatalf("unmarshal suite run json: %v (stdout=%q)", err, h.Stdout.String())
	}
	if sum.OK || len(sum.Attempts) != 1 {
		t.Fatalf("unexpected summary: %+v", sum)
	}
	if sum.Attempts[0].RunnerErrorCode != "ZCL_E_CONTAMINATED_PROMPT" {
		t.Fatalf("expected contamination code, got: %s", sum.Attempts[0].RunnerErrorCode)
	}
	if !sum.Attempts[0].Finish.Report.Integrity.PromptContaminated {
		t.Fatalf("expected report integrity.promptContaminated=true")
	}
}

func TestSuiteRun_ParallelTotal_JITAllocation(t *testing.T) {
	outRoot := t.TempDir()
	suitePath := filepath.Join(t.TempDir(), "suite.json")
	writeSuiteFile(t, suitePath, `{
  "version": 1,
  "suiteId": "suite-run-parallel-total",
  "defaults": { "mode": "discovery", "timeoutMs": 60000 },
  "missions": [
    { "missionId": "m1", "prompt": "p1", "expects": { "ok": true } },
    { "missionId": "m2", "prompt": "p2", "expects": { "ok": true } }
  ]
}`)

	t.Setenv("ZCL_WANT_SUITE_RUNNER", "1")

	h := newRunnerHarness(t, suiteRunNow())

	code := h.Runner.Run([]string{
		"suite", "run",
		"--file", suitePath,
		"--out-root", outRoot,
		"--parallel", "2",
		"--total", "5",
		"--json",
		"--",
		os.Args[0], "-test.run=TestHelperSuiteRunnerProcess$", "--", "case=ok",
	})
	if code != 0 {
		t.Fatalf("expected exit code 0, got %d (stderr=%q)", code, h.Stderr.String())
	}

	var sum struct {
		OK       bool   `json:"ok"`
		RunID    string `json:"runId"`
		Passed   int    `json:"passed"`
		Failed   int    `json:"failed"`
		Attempts []struct {
			AttemptID string `json:"attemptId"`
			MissionID string `json:"missionId"`
			OK        bool   `json:"ok"`
		} `json:"attempts"`
	}
	if err := json.Unmarshal(h.Stdout.Bytes(), &sum); err != nil {
		t.Fatalf("unmarshal suite run json: %v (stdout=%q)", err, h.Stdout.String())
	}
	if !sum.OK || sum.Passed != 5 || sum.Failed != 0 || len(sum.Attempts) != 5 {
		t.Fatalf("unexpected summary: %+v", sum)
	}
	if sum.RunID == "" {
		t.Fatalf("expected runId in summary")
	}
}

func TestSuiteRun_RefusesImplicitProcessFallbackWhenHostIsNativeCapable(t *testing.T) {
	outRoot := t.TempDir()
	suitePath := filepath.Join(t.TempDir(), "suite.json")
	writeSuiteFile(t, suitePath, `{
  "version": 1,
  "suiteId": "suite-run-native-capability-guard",
  "defaults": { "mode": "discovery", "timeoutMs": 60000 },
  "missions": [
    { "missionId": "m1", "prompt": "p1", "expects": { "ok": true } }
  ]
}`)

	t.Setenv("ZCL_WANT_SUITE_RUNNER", "1")
	t.Setenv("ZCL_HOST_NATIVE_SPAWN", "1")

	h := newRunnerHarness(t, suiteRunNow())

	code := h.Runner.Run([]string{
		"suite", "run",
		"--file", suitePath,
		"--out-root", outRoot,
		"--json",
		"--",
		os.Args[0], "-test.run=TestHelperSuiteRunnerProcess$", "--", "case=ok",
	})
	if code != 2 {
		t.Fatalf("expected exit code 2, got %d (stderr=%q)", code, h.Stderr.String())
	}
	if !strings.Contains(h.Stderr.String(), "native runtime mode does not accept -- <runner-cmd> arguments") {
		t.Fatalf("expected native-capability guard error, got stderr=%q", h.Stderr.String())
	}
}

func TestSuiteRun_ExplicitProcessAllowedWhenHostIsNativeCapable(t *testing.T) {
	outRoot := t.TempDir()
	suitePath := filepath.Join(t.TempDir(), "suite.json")
	writeSuiteFile(t, suitePath, `{
  "version": 1,
  "suiteId": "suite-run-native-capability-explicit-process",
  "defaults": { "mode": "discovery", "timeoutMs": 60000 },
  "missions": [
    { "missionId": "m1", "prompt": "p1", "expects": { "ok": true } }
  ]
}`)

	t.Setenv("ZCL_WANT_SUITE_RUNNER", "1")
	t.Setenv("ZCL_HOST_NATIVE_SPAWN", "1")

	h := newRunnerHarness(t, suiteRunNow())

	code := h.Runner.Run([]string{
		"suite", "run",
		"--file", suitePath,
		"--out-root", outRoot,
		"--session-isolation", "process",
		"--json",
		"--",
		os.Args[0], "-test.run=TestHelperSuiteRunnerProcess$", "--", "case=ok",
	})
	if code != 0 {
		t.Fatalf("expected exit code 0, got %d (stderr=%q)", code, h.Stderr.String())
	}

	var sum struct {
		OK                        bool   `json:"ok"`
		SessionIsolation          string `json:"sessionIsolation"`
		SessionIsolationRequested string `json:"sessionIsolationRequested"`
		HostNativeSpawnCapable    bool   `json:"hostNativeSpawnCapable"`
	}
	if err := json.Unmarshal(h.Stdout.Bytes(), &sum); err != nil {
		t.Fatalf("unmarshal suite run json: %v (stdout=%q)", err, h.Stdout.String())
	}
	if !sum.OK {
		t.Fatalf("expected ok=true summary, got %+v", sum)
	}
	if sum.SessionIsolationRequested != "process" || sum.SessionIsolation != "process_runner" {
		t.Fatalf("unexpected isolation fields: %+v", sum)
	}
	if !sum.HostNativeSpawnCapable {
		t.Fatalf("expected hostNativeSpawnCapable=true")
	}
}

func TestSuiteRun_NativeRuntimeEndToEnd(t *testing.T) {
	TestSuiteRun_NativeRuntimeEndToEndCore(t)
}

func TestSuiteRun_NativeRuntimeEndToEndCore(t *testing.T) {
	outRoot := t.TempDir()
	suitePath := filepath.Join(t.TempDir(), "suite.json")
	writeSuiteFile(t, suitePath, `{
  "version": 1,
  "suiteId": "suite-run-native-runtime-e2e",
  "defaults": { "mode": "discovery", "timeoutMs": 60000 },
  "missions": [
    { "missionId": "m1", "prompt": "native prompt", "expects": { "ok": true } }
  ]
}`)

	t.Setenv("ZCL_CODEX_APP_SERVER_CMD", os.Args[0]+" -test.run=TestHelperSuiteNativeAppServer$")
	t.Setenv("ZCL_HELPER_PROCESS", "1")
	t.Setenv("ZCL_HELPER_MODE", "smoke")

	h := newRunnerHarness(t, suiteRunNow())

	code := h.Runner.Run([]string{
		"suite", "run",
		"--file", suitePath,
		"--out-root", outRoot,
		"--session-isolation", "native",
		"--json",
	})
	if code != 0 {
		t.Fatalf("expected exit code 0, got %d (stderr=%q)", code, h.Stderr.String())
	}

	sum := parseSuiteRunNativeRuntimeSummary(t, h.Stdout.Bytes(), h.Stdout.String())
	attemptDir := assertSuiteRunNativeRuntimeSummary(t, sum)
	assertSuiteRunNativeRuntimeArtifacts(t, attemptDir)
}

type suiteRunNativeRuntimeSummary struct {
	OK                      bool                               `json:"ok"`
	RuntimeStrategySelected string                             `json:"runtimeStrategySelected"`
	Attempts                []suiteRunNativeRuntimeSummaryItem `json:"attempts"`
}

type suiteRunNativeRuntimeSummaryItem struct {
	AttemptDir      string `json:"attemptDir"`
	RunnerErrorCode string `json:"runnerErrorCode"`
	OK              bool   `json:"ok"`
}

func parseSuiteRunNativeRuntimeSummary(t *testing.T, stdout []byte, stdoutText string) suiteRunNativeRuntimeSummary {
	t.Helper()
	var sum suiteRunNativeRuntimeSummary
	if err := json.Unmarshal(stdout, &sum); err != nil {
		t.Fatalf("unmarshal suite run json: %v (stdout=%q)", err, stdoutText)
	}
	return sum
}

func assertSuiteRunNativeRuntimeSummary(t *testing.T, sum suiteRunNativeRuntimeSummary) string {
	t.Helper()
	if !sum.OK || len(sum.Attempts) != 1 || !sum.Attempts[0].OK {
		t.Fatalf("unexpected summary: %+v", sum)
	}
	if sum.RuntimeStrategySelected != "codex_app_server" {
		t.Fatalf("unexpected runtime strategy selected: %+v", sum)
	}
	if sum.Attempts[0].RunnerErrorCode != "" {
		t.Fatalf("unexpected runner error: %+v", sum.Attempts[0])
	}
	if sum.Attempts[0].AttemptDir == "" {
		t.Fatalf("expected attemptDir")
	}
	return sum.Attempts[0].AttemptDir
}

func assertSuiteRunNativeRuntimeArtifacts(t *testing.T, attemptDir string) {
	t.Helper()
	assertSuiteRunNativeRuntimeFilesExist(t, attemptDir)
	assertSuiteRunNativeRunnerRef(t, attemptDir)
	assertSuiteRunNativeFeedback(t, attemptDir)
	assertSuiteRunNativeAttemptMetadata(t, attemptDir)
	assertSuiteRunNativeRuntimeEnvMetadata(t, attemptDir)
	assertSuiteRunNativeReportMetadata(t, attemptDir)
}

func assertSuiteRunNativeRuntimeFilesExist(t *testing.T, attemptDir string) {
	t.Helper()
	for _, rel := range []string{"feedback.json", "tool.calls.jsonl"} {
		if _, err := os.Stat(filepath.Join(attemptDir, rel)); err != nil {
			t.Fatalf("expected %s: %v", rel, err)
		}
	}
}

func assertSuiteRunNativeRunnerRef(t *testing.T, attemptDir string) {
	t.Helper()
	refRaw, err := os.ReadFile(filepath.Join(attemptDir, "runner.ref.json"))
	if err != nil {
		t.Fatalf("read runner.ref.json: %v", err)
	}
	var ref struct {
		RuntimeID string `json:"runtimeId"`
		SessionID string `json:"sessionId"`
	}
	if err := json.Unmarshal(refRaw, &ref); err != nil {
		t.Fatalf("unmarshal runner.ref.json: %v", err)
	}
	if ref.RuntimeID != "codex_app_server" || strings.TrimSpace(ref.SessionID) == "" {
		t.Fatalf("unexpected runner ref: %+v", ref)
	}
}

func assertSuiteRunNativeFeedback(t *testing.T, attemptDir string) {
	t.Helper()
	fbRaw, err := os.ReadFile(filepath.Join(attemptDir, "feedback.json"))
	if err != nil {
		t.Fatalf("read feedback.json: %v", err)
	}
	var fb struct {
		Result string `json:"result"`
	}
	if err := json.Unmarshal(fbRaw, &fb); err != nil {
		t.Fatalf("unmarshal feedback.json: %v", err)
	}
	if fb.Result != "native-result" {
		t.Fatalf("expected native delta fallback result, got %q", fb.Result)
	}
}

func assertSuiteRunNativeAttemptMetadata(t *testing.T, attemptDir string) {
	t.Helper()
	attemptRaw, err := os.ReadFile(filepath.Join(attemptDir, "attempt.json"))
	if err != nil {
		t.Fatalf("read attempt.json: %v", err)
	}
	var attempt struct {
		NativeResult struct {
			ResultSource string `json:"resultSource"`
			PhaseAware   bool   `json:"phaseAware"`
		} `json:"nativeResult"`
	}
	if err := json.Unmarshal(attemptRaw, &attempt); err != nil {
		t.Fatalf("unmarshal attempt.json: %v", err)
	}
	if attempt.NativeResult.ResultSource != "delta_fallback" || attempt.NativeResult.PhaseAware {
		t.Fatalf("unexpected attempt nativeResult: %+v", attempt.NativeResult)
	}
}

func assertSuiteRunNativeRuntimeEnvMetadata(t *testing.T, attemptDir string) {
	t.Helper()
	runtimeEnvRaw, err := os.ReadFile(filepath.Join(attemptDir, "attempt.runtime.env.json"))
	if err != nil {
		t.Fatalf("read attempt.runtime.env.json: %v", err)
	}
	var runtimeEnv struct {
		Runtime struct {
			NativeMode   bool   `json:"nativeMode"`
			RuntimeID    string `json:"runtimeId"`
			StartCwdMode string `json:"startCwdMode"`
			StartCwd     string `json:"startCwd"`
		} `json:"runtime"`
		Prompt struct {
			SourceKind string `json:"sourceKind"`
			SHA256     string `json:"sha256"`
		} `json:"prompt"`
	}
	if err := json.Unmarshal(runtimeEnvRaw, &runtimeEnv); err != nil {
		t.Fatalf("unmarshal attempt.runtime.env.json: %v", err)
	}
	if !runtimeEnv.Runtime.NativeMode || runtimeEnv.Runtime.RuntimeID != "codex_app_server" {
		t.Fatalf("unexpected native runtime metadata: %+v", runtimeEnv.Runtime)
	}
	if runtimeEnv.Runtime.StartCwdMode != "inherit" || strings.TrimSpace(runtimeEnv.Runtime.StartCwd) == "" {
		t.Fatalf("unexpected native start cwd metadata: %+v", runtimeEnv.Runtime)
	}
	if runtimeEnv.Prompt.SourceKind != "suite_prompt" || len(runtimeEnv.Prompt.SHA256) != 64 {
		t.Fatalf("unexpected native prompt metadata: %+v", runtimeEnv.Prompt)
	}
}

func assertSuiteRunNativeReportMetadata(t *testing.T, attemptDir string) {
	t.Helper()
	repRaw, err := os.ReadFile(filepath.Join(attemptDir, "attempt.report.json"))
	if err != nil {
		t.Fatalf("read attempt.report.json: %v", err)
	}
	var rep struct {
		NativeResult struct {
			ResultSource string `json:"resultSource"`
			PhaseAware   bool   `json:"phaseAware"`
		} `json:"nativeResult"`
	}
	if err := json.Unmarshal(repRaw, &rep); err != nil {
		t.Fatalf("unmarshal attempt.report.json: %v", err)
	}
	if rep.NativeResult.ResultSource != "delta_fallback" || rep.NativeResult.PhaseAware {
		t.Fatalf("unexpected report nativeResult: %+v", rep.NativeResult)
	}
}

func TestSuiteRun_NativeRunnerCwdTempEmptyPerAttempt(t *testing.T) {
	outRoot := t.TempDir()
	basePath := filepath.Join(t.TempDir(), "runner-cwd")
	suitePath := filepath.Join(t.TempDir(), "suite.json")
	writeSuiteFile(t, suitePath, `{
  "version": 1,
  "suiteId": "suite-run-native-cwd-temp",
  "defaults": { "mode": "discovery", "timeoutMs": 60000 },
  "missions": [
    { "missionId": "m1", "prompt": "native prompt", "expects": { "ok": true } }
  ]
}`)

	t.Setenv("ZCL_CODEX_APP_SERVER_CMD", os.Args[0]+" -test.run=TestHelperSuiteNativeAppServer$")
	t.Setenv("ZCL_HELPER_PROCESS", "1")
	t.Setenv("ZCL_HELPER_MODE", "smoke")
	t.Setenv("ZCL_HELPER_EXPECT_CWD_PREFIX", filepath.Clean(basePath)+string(os.PathSeparator))

	h := newRunnerHarness(t, time.Date(2026, 3, 1, 15, 0, 0, 0, time.UTC))

	code := h.Runner.runSuiteRunWithEnv([]string{
		"--file", suitePath,
		"--out-root", outRoot,
		"--session-isolation", "native",
		"--json",
	}, map[string]string{
		suiteRunEnvRunnerCwdMode:     "temp_empty_per_attempt",
		suiteRunEnvRunnerCwdBasePath: basePath,
	})
	if code != 0 {
		t.Fatalf("expected exit code 0, got %d (stderr=%q)", code, h.Stderr.String())
	}

	var sum struct {
		OK       bool `json:"ok"`
		Attempts []struct {
			AttemptDir string `json:"attemptDir"`
		} `json:"attempts"`
	}
	if err := json.Unmarshal(h.Stdout.Bytes(), &sum); err != nil {
		t.Fatalf("unmarshal suite run json: %v (stdout=%q)", err, h.Stdout.String())
	}
	if !sum.OK || len(sum.Attempts) != 1 {
		t.Fatalf("unexpected summary: %+v", sum)
	}
	attemptDir := sum.Attempts[0].AttemptDir
	runtimeEnvRaw, err := os.ReadFile(filepath.Join(attemptDir, "attempt.runtime.env.json"))
	if err != nil {
		t.Fatalf("read attempt.runtime.env.json: %v", err)
	}
	var runtimeEnv struct {
		Runtime struct {
			StartCwdMode   string `json:"startCwdMode"`
			StartCwd       string `json:"startCwd"`
			StartCwdRetain string `json:"startCwdRetain"`
		} `json:"runtime"`
	}
	if err := json.Unmarshal(runtimeEnvRaw, &runtimeEnv); err != nil {
		t.Fatalf("unmarshal attempt.runtime.env.json: %v", err)
	}
	if runtimeEnv.Runtime.StartCwdMode != "temp_empty_per_attempt" {
		t.Fatalf("unexpected start cwd mode: %+v", runtimeEnv.Runtime)
	}
	if !strings.HasPrefix(runtimeEnv.Runtime.StartCwd, filepath.Clean(basePath)+string(os.PathSeparator)) {
		t.Fatalf("start cwd %q not under %q", runtimeEnv.Runtime.StartCwd, basePath)
	}
	if runtimeEnv.Runtime.StartCwdRetain != "never" {
		t.Fatalf("unexpected start cwd retain policy: %+v", runtimeEnv.Runtime)
	}
	if _, err := os.Stat(runtimeEnv.Runtime.StartCwd); !os.IsNotExist(err) {
		t.Fatalf("expected temp start cwd cleanup, stat err=%v path=%q", err, runtimeEnv.Runtime.StartCwd)
	}
}

func TestSuiteRun_NativeFinalResultPrefersTaskCompleteLastAgentMessage(t *testing.T) {
	outRoot := t.TempDir()
	suitePath := filepath.Join(t.TempDir(), "suite.json")
	writeSuiteFile(t, suitePath, `{
  "version": 1,
  "suiteId": "suite-run-native-task-complete-preferred",
  "defaults": { "mode": "discovery", "timeoutMs": 60000 },
  "missions": [
    { "missionId": "m1", "prompt": "native prompt", "expects": { "ok": true } }
  ]
}`)

	t.Setenv("ZCL_CODEX_APP_SERVER_CMD", os.Args[0]+" -test.run=TestHelperSuiteNativeAppServer$")
	t.Setenv("ZCL_HELPER_PROCESS", "1")
	t.Setenv("ZCL_HELPER_MODE", "task_complete_preferred")

	h := newRunnerHarness(t, suiteRunNow())

	code := h.Runner.Run([]string{
		"suite", "run",
		"--file", suitePath,
		"--out-root", outRoot,
		"--session-isolation", "native",
		"--json",
	})
	if code != 0 {
		t.Fatalf("expected exit code 0, got %d (stderr=%q)", code, h.Stderr.String())
	}

	var sum struct {
		OK       bool `json:"ok"`
		Attempts []struct {
			AttemptDir string `json:"attemptDir"`
		} `json:"attempts"`
	}
	if err := json.Unmarshal(h.Stdout.Bytes(), &sum); err != nil {
		t.Fatalf("unmarshal suite run json: %v (stdout=%q)", err, h.Stdout.String())
	}
	if !sum.OK || len(sum.Attempts) != 1 {
		t.Fatalf("unexpected summary: %+v", sum)
	}
	attemptDir := sum.Attempts[0].AttemptDir

	fbRaw, err := os.ReadFile(filepath.Join(attemptDir, "feedback.json"))
	if err != nil {
		t.Fatalf("read feedback.json: %v", err)
	}
	var fb struct {
		Result string `json:"result"`
	}
	if err := json.Unmarshal(fbRaw, &fb); err != nil {
		t.Fatalf("unmarshal feedback.json: %v", err)
	}
	if fb.Result != "TASK_COMPLETE_FINAL" {
		t.Fatalf("expected task_complete result, got %q", fb.Result)
	}

	attemptRaw, err := os.ReadFile(filepath.Join(attemptDir, "attempt.json"))
	if err != nil {
		t.Fatalf("read attempt.json: %v", err)
	}
	var attempt struct {
		NativeResult struct {
			ResultSource               string `json:"resultSource"`
			PhaseAware                 bool   `json:"phaseAware"`
			CommentaryMessagesObserved int64  `json:"commentaryMessagesObserved"`
			ReasoningItemsObserved     int64  `json:"reasoningItemsObserved"`
		} `json:"nativeResult"`
	}
	if err := json.Unmarshal(attemptRaw, &attempt); err != nil {
		t.Fatalf("unmarshal attempt.json: %v", err)
	}
	if attempt.NativeResult.ResultSource != "task_complete_last_agent_message" {
		t.Fatalf("unexpected result source: %+v", attempt.NativeResult)
	}
	if !attempt.NativeResult.PhaseAware || attempt.NativeResult.CommentaryMessagesObserved < 1 || attempt.NativeResult.ReasoningItemsObserved < 1 {
		t.Fatalf("unexpected provenance counters: %+v", attempt.NativeResult)
	}
}

func TestSuiteRun_NativeFinalResultUsesPhaseFinalAnswer(t *testing.T) {
	outRoot := t.TempDir()
	suitePath := filepath.Join(t.TempDir(), "suite.json")
	writeSuiteFile(t, suitePath, `{
  "version": 1,
  "suiteId": "suite-run-native-phase-final-answer",
  "defaults": { "mode": "discovery", "timeoutMs": 60000 },
  "missions": [
    { "missionId": "m1", "prompt": "native prompt", "expects": { "ok": true } }
  ]
}`)

	t.Setenv("ZCL_CODEX_APP_SERVER_CMD", os.Args[0]+" -test.run=TestHelperSuiteNativeAppServer$")
	t.Setenv("ZCL_HELPER_PROCESS", "1")
	t.Setenv("ZCL_HELPER_MODE", "phase_final_answer")

	h := newRunnerHarness(t, suiteRunNow())

	code := h.Runner.Run([]string{
		"suite", "run",
		"--file", suitePath,
		"--out-root", outRoot,
		"--session-isolation", "native",
		"--json",
	})
	if code != 0 {
		t.Fatalf("expected exit code 0, got %d (stderr=%q)", code, h.Stderr.String())
	}

	var sum struct {
		OK       bool `json:"ok"`
		Attempts []struct {
			AttemptDir string `json:"attemptDir"`
		} `json:"attempts"`
	}
	if err := json.Unmarshal(h.Stdout.Bytes(), &sum); err != nil {
		t.Fatalf("unmarshal suite run json: %v (stdout=%q)", err, h.Stdout.String())
	}
	if !sum.OK || len(sum.Attempts) != 1 {
		t.Fatalf("unexpected summary: %+v", sum)
	}
	attemptDir := sum.Attempts[0].AttemptDir

	fbRaw, err := os.ReadFile(filepath.Join(attemptDir, "feedback.json"))
	if err != nil {
		t.Fatalf("read feedback.json: %v", err)
	}
	var fb struct {
		Result string `json:"result"`
	}
	if err := json.Unmarshal(fbRaw, &fb); err != nil {
		t.Fatalf("unmarshal feedback.json: %v", err)
	}
	if fb.Result != "PHASE_FINAL_ANSWER" {
		t.Fatalf("expected phase-final result, got %q", fb.Result)
	}

	attemptRaw, err := os.ReadFile(filepath.Join(attemptDir, "attempt.json"))
	if err != nil {
		t.Fatalf("read attempt.json: %v", err)
	}
	var attempt struct {
		NativeResult struct {
			ResultSource               string `json:"resultSource"`
			PhaseAware                 bool   `json:"phaseAware"`
			CommentaryMessagesObserved int64  `json:"commentaryMessagesObserved"`
			ReasoningItemsObserved     int64  `json:"reasoningItemsObserved"`
		} `json:"nativeResult"`
	}
	if err := json.Unmarshal(attemptRaw, &attempt); err != nil {
		t.Fatalf("unmarshal attempt.json: %v", err)
	}
	if attempt.NativeResult.ResultSource != "phase_final_answer" || !attempt.NativeResult.PhaseAware {
		t.Fatalf("unexpected result source/provenance: %+v", attempt.NativeResult)
	}
	if attempt.NativeResult.CommentaryMessagesObserved < 1 || attempt.NativeResult.ReasoningItemsObserved < 1 {
		t.Fatalf("expected commentary/reasoning counters, got %+v", attempt.NativeResult)
	}
}

func TestSuiteRun_NativeMissingFinalAnswerGetsTypedFailure(t *testing.T) {
	TestSuiteRun_NativeMissingFinalAnswerGetsTypedFailureCore(t)
}

func TestSuiteRun_NativeMissingFinalAnswerGetsTypedFailureCore(t *testing.T) {
	outRoot := t.TempDir()
	suitePath := filepath.Join(t.TempDir(), "suite.json")
	writeSuiteFile(t, suitePath, `{
  "version": 1,
  "suiteId": "suite-run-native-final-answer-missing",
  "defaults": { "mode": "ci", "timeoutMs": 60000 },
  "missions": [
    { "missionId": "m1", "prompt": "native prompt" }
  ]
}`)

	t.Setenv("ZCL_CODEX_APP_SERVER_CMD", os.Args[0]+" -test.run=TestHelperSuiteNativeAppServer$")
	t.Setenv("ZCL_HELPER_PROCESS", "1")
	t.Setenv("ZCL_HELPER_MODE", "phase_without_final_answer")

	h := newRunnerHarness(t, suiteRunNow())

	code := h.Runner.Run([]string{
		"suite", "run",
		"--file", suitePath,
		"--out-root", outRoot,
		"--session-isolation", "native",
		"--json",
	})
	if code == 1 {
		t.Fatalf("expected typed runtime failure, got harness error code=%d stderr=%q", code, h.Stderr.String())
	}

	attempt := parseSuiteRunNativeMissingFinalAnswerAttempt(t, h.Stdout.Bytes(), h.Stdout.String())
	assertSuiteRunNativeMissingFinalAnswerSummary(t, attempt)
	assertSuiteRunNativeMissingFinalAnswerFeedback(t, attempt.AttemptDir)
	assertSuiteRunNativeMissingFinalAnswerAttemptMetadata(t, attempt.AttemptDir)
}

type suiteRunNativeMissingFinalAnswerSummary struct {
	Attempts []suiteRunNativeMissingFinalAnswerAttempt `json:"attempts"`
}

type suiteRunNativeMissingFinalAnswerAttempt struct {
	AttemptDir       string `json:"attemptDir"`
	RunnerErrorCode  string `json:"runnerErrorCode"`
	AutoFeedbackCode string `json:"autoFeedbackCode"`
	AutoFeedback     bool   `json:"autoFeedback"`
}

func parseSuiteRunNativeMissingFinalAnswerAttempt(t *testing.T, stdout []byte, stdoutText string) suiteRunNativeMissingFinalAnswerAttempt {
	t.Helper()
	var sum suiteRunNativeMissingFinalAnswerSummary
	if err := json.Unmarshal(stdout, &sum); err != nil {
		t.Fatalf("unmarshal suite run output: %v (stdout=%q)", err, stdoutText)
	}
	if len(sum.Attempts) != 1 {
		t.Fatalf("expected one attempt, got %+v", sum.Attempts)
	}
	return sum.Attempts[0]
}

func assertSuiteRunNativeMissingFinalAnswerSummary(t *testing.T, attempt suiteRunNativeMissingFinalAnswerAttempt) {
	t.Helper()
	if attempt.RunnerErrorCode != codeRuntimeFinalAnswerNotFound || attempt.AutoFeedbackCode != codeRuntimeFinalAnswerNotFound || !attempt.AutoFeedback {
		t.Fatalf("expected missing-final-answer typed failure, got %+v", attempt)
	}
}

func assertSuiteRunNativeMissingFinalAnswerFeedback(t *testing.T, attemptDir string) {
	t.Helper()
	fbRaw, err := os.ReadFile(filepath.Join(attemptDir, "feedback.json"))
	if err != nil {
		t.Fatalf("read feedback.json: %v", err)
	}
	var fb struct {
		OK         bool `json:"ok"`
		ResultJSON struct {
			Kind string `json:"kind"`
			Code string `json:"code"`
		} `json:"resultJson"`
	}
	if err := json.Unmarshal(fbRaw, &fb); err != nil {
		t.Fatalf("unmarshal feedback.json: %v", err)
	}
	if fb.OK || fb.ResultJSON.Kind != "runtime_failure" || fb.ResultJSON.Code != codeRuntimeFinalAnswerNotFound {
		t.Fatalf("unexpected failure feedback payload: %+v", fb)
	}
}

func assertSuiteRunNativeMissingFinalAnswerAttemptMetadata(t *testing.T, attemptDir string) {
	t.Helper()
	attemptRaw, err := os.ReadFile(filepath.Join(attemptDir, "attempt.json"))
	if err != nil {
		t.Fatalf("read attempt.json: %v", err)
	}
	var attempt struct {
		NativeResult struct {
			ResultSource               string `json:"resultSource"`
			PhaseAware                 bool   `json:"phaseAware"`
			CommentaryMessagesObserved int64  `json:"commentaryMessagesObserved"`
			ReasoningItemsObserved     int64  `json:"reasoningItemsObserved"`
		} `json:"nativeResult"`
	}
	if err := json.Unmarshal(attemptRaw, &attempt); err != nil {
		t.Fatalf("unmarshal attempt.json: %v", err)
	}
	if attempt.NativeResult.ResultSource != "" || !attempt.NativeResult.PhaseAware || attempt.NativeResult.CommentaryMessagesObserved < 1 {
		t.Fatalf("unexpected provenance for missing final answer: %+v", attempt.NativeResult)
	}
}

func TestSuiteRun_NativeModelForwardedToThreadStart(t *testing.T) {
	outRoot := t.TempDir()
	suitePath := filepath.Join(t.TempDir(), "suite.json")
	writeSuiteFile(t, suitePath, `{
  "version": 1,
  "suiteId": "suite-run-native-model-forwarded",
  "defaults": { "mode": "discovery", "timeoutMs": 60000 },
  "missions": [
    { "missionId": "m1", "prompt": "native prompt", "expects": { "ok": true } }
  ]
}`)

	t.Setenv("ZCL_CODEX_APP_SERVER_CMD", os.Args[0]+" -test.run=TestHelperSuiteNativeAppServer$")
	t.Setenv("ZCL_HELPER_PROCESS", "1")
	t.Setenv("ZCL_HELPER_MODE", "assert_model_forwarded")
	t.Setenv("ZCL_HELPER_EXPECT_MODEL", "gpt-5.3-codex-spark")
	t.Setenv("ZCL_HELPER_EXPECT_REASONING_EFFORT", "medium")

	h := newRunnerHarness(t, time.Date(2026, 2, 16, 12, 1, 0, 0, time.UTC))

	code := h.Runner.Run([]string{
		"suite", "run",
		"--file", suitePath,
		"--out-root", outRoot,
		"--session-isolation", "native",
		"--native-model", "gpt-5.3-codex-spark",
		"--native-model-reasoning-effort", "medium",
		"--json",
	})
	if code != 0 {
		t.Fatalf("expected exit code 0, got %d (stderr=%q)", code, h.Stderr.String())
	}

	var sum struct {
		OK       bool `json:"ok"`
		Attempts []struct {
			RunnerErrorCode string `json:"runnerErrorCode"`
			OK              bool   `json:"ok"`
		} `json:"attempts"`
	}
	if err := json.Unmarshal(h.Stdout.Bytes(), &sum); err != nil {
		t.Fatalf("unmarshal suite run json: %v (stdout=%q)", err, h.Stdout.String())
	}
	if !sum.OK || len(sum.Attempts) != 1 || !sum.Attempts[0].OK || sum.Attempts[0].RunnerErrorCode != "" {
		t.Fatalf("unexpected summary: %+v", sum)
	}
}

func TestSuiteRun_NativeInvalidModelFailureIsTyped(t *testing.T) {
	outRoot := t.TempDir()
	suitePath := filepath.Join(t.TempDir(), "suite.json")
	writeSuiteFile(t, suitePath, `{
  "version": 1,
  "suiteId": "suite-run-native-invalid-model",
  "defaults": { "mode": "ci", "timeoutMs": 60000 },
  "missions": [
    { "missionId": "m1", "prompt": "invalid model mission" }
  ]
}`)

	t.Setenv("ZCL_CODEX_APP_SERVER_CMD", os.Args[0]+" -test.run=TestHelperSuiteNativeAppServer$")
	t.Setenv("ZCL_HELPER_PROCESS", "1")
	t.Setenv("ZCL_HELPER_MODE", "invalid_model")

	h := newRunnerHarnessNowFunc(t, time.Now)

	code := h.Runner.Run([]string{
		"suite", "run",
		"--file", suitePath,
		"--out-root", outRoot,
		"--session-isolation", "native",
		"--native-model", "invalid-model-id",
		"--json",
	})
	if code == 1 {
		t.Fatalf("expected typed runtime failure, got harness error code=%d stderr=%q", code, h.Stderr.String())
	}

	var sum struct {
		Attempts []struct {
			RunnerErrorCode string `json:"runnerErrorCode"`
		} `json:"attempts"`
	}
	if err := json.Unmarshal(h.Stdout.Bytes(), &sum); err != nil {
		t.Fatalf("unmarshal suite run output: %v", err)
	}
	if len(sum.Attempts) != 1 {
		t.Fatalf("expected one attempt, got %+v", sum.Attempts)
	}
	if sum.Attempts[0].RunnerErrorCode != codeRuntimeProtocol {
		t.Fatalf("expected protocol runtime code for invalid model, got %+v", sum.Attempts[0])
	}
}

func TestSuiteRun_NativeReasoningUnsupportedBestEffortFallsBack(t *testing.T) {
	outRoot := t.TempDir()
	suitePath := filepath.Join(t.TempDir(), "suite.json")
	writeSuiteFile(t, suitePath, `{
  "version": 1,
  "suiteId": "suite-run-native-reasoning-best-effort",
  "defaults": { "mode": "discovery", "timeoutMs": 60000 },
  "missions": [
    { "missionId": "m1", "prompt": "reasoning fallback mission", "expects": { "ok": true } }
  ]
}`)

	t.Setenv("ZCL_CODEX_APP_SERVER_CMD", os.Args[0]+" -test.run=TestHelperSuiteNativeAppServer$")
	t.Setenv("ZCL_HELPER_PROCESS", "1")
	t.Setenv("ZCL_HELPER_MODE", "reasoning_unsupported")

	h := newRunnerHarnessNowFunc(t, time.Now)

	code := h.Runner.Run([]string{
		"suite", "run",
		"--file", suitePath,
		"--out-root", outRoot,
		"--session-isolation", "native",
		"--native-model", "gpt-5.3-codex-spark",
		"--native-model-reasoning-effort", "medium",
		"--json",
	})
	if code != 0 {
		t.Fatalf("expected success with best-effort fallback, got code=%d stderr=%q", code, h.Stderr.String())
	}

	var sum struct {
		OK       bool `json:"ok"`
		Attempts []struct {
			RunnerErrorCode string `json:"runnerErrorCode"`
			OK              bool   `json:"ok"`
		} `json:"attempts"`
	}
	if err := json.Unmarshal(h.Stdout.Bytes(), &sum); err != nil {
		t.Fatalf("unmarshal suite run json: %v", err)
	}
	if !sum.OK || len(sum.Attempts) != 1 || !sum.Attempts[0].OK || sum.Attempts[0].RunnerErrorCode != "" {
		t.Fatalf("expected successful fallback summary, got %+v", sum)
	}
}

func TestSuiteRun_NativeProcessParity(t *testing.T) {
	TestSuiteRun_NativeProcessParityCore(t)
}

func TestSuiteRun_NativeProcessParityCore(t *testing.T) {
	baseDir := t.TempDir()
	suitePath := filepath.Join(baseDir, "suite.json")
	writeSuiteFile(t, suitePath, `{
  "version": 1,
  "suiteId": "suite-run-parity",
  "defaults": { "mode": "discovery", "timeoutMs": 60000 },
  "missions": [
    { "missionId": "m1", "prompt": "parity prompt", "expects": { "ok": true } }
	  ]
	}`)

	processAttemptDir := runSuiteProcessParityMode(t, suitePath, filepath.Join(baseDir, "process"))
	nativeAttemptDir := runSuiteNativeParityMode(t, suitePath, filepath.Join(baseDir, "native"))
	assertSuiteRunParityArtifacts(t, processAttemptDir, nativeAttemptDir)
}

func runSuiteProcessParityMode(t *testing.T, suitePath string, outRoot string) string {
	t.Helper()
	t.Setenv("ZCL_WANT_SUITE_RUNNER", "1")
	h := newRunnerHarness(t, time.Date(2026, 2, 16, 12, 10, 0, 0, time.UTC))
	code := h.Runner.Run([]string{
		"suite", "run",
		"--file", suitePath,
		"--out-root", outRoot,
		"--session-isolation", "process",
		"--json",
		"--",
		os.Args[0], "-test.run=TestHelperSuiteRunnerProcess$", "--", "case=ok",
	})
	if code != 0 {
		t.Fatalf("process suite run expected 0, got %d stderr=%q", code, h.Stderr.String())
	}
	return parseSuiteRunSingleAttemptDir(t, h.Stdout.Bytes(), "process summary")
}

func runSuiteNativeParityMode(t *testing.T, suitePath string, outRoot string) string {
	t.Helper()
	t.Setenv("ZCL_CODEX_APP_SERVER_CMD", os.Args[0]+" -test.run=TestHelperSuiteNativeAppServer$")
	t.Setenv("ZCL_HELPER_PROCESS", "1")
	t.Setenv("ZCL_HELPER_MODE", "smoke")
	h := newRunnerHarness(t, time.Date(2026, 2, 16, 12, 11, 0, 0, time.UTC))
	code := h.Runner.Run([]string{
		"suite", "run",
		"--file", suitePath,
		"--out-root", outRoot,
		"--session-isolation", "native",
		"--json",
	})
	if code != 0 {
		t.Fatalf("native suite run expected 0, got %d stderr=%q", code, h.Stderr.String())
	}
	return parseSuiteRunSingleAttemptDir(t, h.Stdout.Bytes(), "native summary")
}

func parseSuiteRunSingleAttemptDir(t *testing.T, stdout []byte, label string) string {
	t.Helper()
	var sum struct {
		OK       bool `json:"ok"`
		Attempts []struct {
			AttemptDir string `json:"attemptDir"`
		} `json:"attempts"`
	}
	if err := json.Unmarshal(stdout, &sum); err != nil {
		t.Fatalf("unmarshal %s: %v", label, err)
	}
	if !sum.OK || len(sum.Attempts) != 1 || strings.TrimSpace(sum.Attempts[0].AttemptDir) == "" {
		t.Fatalf("unexpected %s payload: %+v", label, sum)
	}
	return sum.Attempts[0].AttemptDir
}

func assertSuiteRunParityArtifacts(t *testing.T, processAttemptDir string, nativeAttemptDir string) {
	t.Helper()
	if !readSuiteRunFeedbackOK(t, processAttemptDir) || !readSuiteRunFeedbackOK(t, nativeAttemptDir) {
		t.Fatalf("expected process/native parity on feedback.ok")
	}
	processReport := readSuiteRunReportState(t, processAttemptDir)
	nativeReport := readSuiteRunReportState(t, nativeAttemptDir)
	if !processReport.OK || !nativeReport.OK {
		t.Fatalf("expected both reports ok=true, process=%v native=%v", processReport.OK, nativeReport.OK)
	}
	if processReport.ToolCallsTotal == 0 || nativeReport.ToolCallsTotal == 0 {
		t.Fatalf("expected non-zero tool calls in both reports, process=%d native=%d", processReport.ToolCallsTotal, nativeReport.ToolCallsTotal)
	}
}

func readSuiteRunFeedbackOK(t *testing.T, attemptDir string) bool {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(attemptDir, "feedback.json"))
	if err != nil {
		t.Fatalf("read feedback: %v", err)
	}
	var payload struct {
		OK bool `json:"ok"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatalf("unmarshal feedback: %v", err)
	}
	return payload.OK
}

type suiteRunReportState struct {
	OK             bool
	ToolCallsTotal int64
}

func readSuiteRunReportState(t *testing.T, attemptDir string) suiteRunReportState {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(attemptDir, "attempt.report.json"))
	if err != nil {
		t.Fatalf("read attempt.report.json: %v", err)
	}
	var payload struct {
		OK      *bool `json:"ok"`
		Metrics struct {
			ToolCallsTotal int64 `json:"toolCallsTotal"`
		} `json:"metrics"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatalf("unmarshal attempt.report.json: %v", err)
	}
	if payload.OK == nil {
		t.Fatalf("expected report ok field")
	}
	return suiteRunReportState{
		OK:             *payload.OK,
		ToolCallsTotal: payload.Metrics.ToolCallsTotal,
	}
}

func TestSuiteRun_NativeParallelUniqueSessions(t *testing.T) {
	TestSuiteRun_NativeParallelUniqueSessionsCore(t)
}

func TestSuiteRun_NativeParallelUniqueSessionsCore(t *testing.T) {
	outRoot := t.TempDir()
	suitePath := filepath.Join(t.TempDir(), "suite.json")
	writeSuiteFile(t, suitePath, buildSuiteRunNativeParallelSuiteJSON(t, 20))

	t.Setenv("ZCL_CODEX_APP_SERVER_CMD", os.Args[0]+" -test.run=TestHelperSuiteNativeAppServer$")
	t.Setenv("ZCL_HELPER_PROCESS", "1")
	t.Setenv("ZCL_HELPER_MODE", "smoke")

	h := newRunnerHarness(t, time.Date(2026, 2, 16, 12, 20, 0, 0, time.UTC))

	code := h.Runner.Run([]string{
		"suite", "run",
		"--file", suitePath,
		"--out-root", outRoot,
		"--session-isolation", "native",
		"--parallel", "20",
		"--total", "20",
		"--json",
	})
	if code != 0 {
		t.Fatalf("expected exit code 0, got %d stderr=%q", code, h.Stderr.String())
	}

	sum := parseSuiteRunNativeParallelSummary(t, h.Stdout.Bytes())
	assertSuiteRunNativeParallelSummary(t, sum, 20)
	assertSuiteRunNativeParallelSessionsUnique(t, sum.Attempts)
}

func buildSuiteRunNativeParallelSuiteJSON(t *testing.T, missionCount int) string {
	t.Helper()
	missions := make([]map[string]any, 0, missionCount)
	for i := 1; i <= missionCount; i++ {
		missions = append(missions, map[string]any{
			"missionId": fmt.Sprintf("m%d", i),
			"prompt":    fmt.Sprintf("parallel prompt %d", i),
			"expects":   map[string]any{"ok": true},
		})
	}
	suiteObj := map[string]any{
		"version": 1,
		"suiteId": "suite-run-native-parallel",
		"defaults": map[string]any{
			"mode":      "discovery",
			"timeoutMs": 60000,
		},
		"missions": missions,
	}
	rawSuite, err := json.Marshal(suiteObj)
	if err != nil {
		t.Fatalf("marshal suite json: %v", err)
	}
	return string(rawSuite)
}

type suiteRunNativeParallelSummary struct {
	OK       bool                            `json:"ok"`
	Attempts []suiteRunNativeParallelAttempt `json:"attempts"`
}

type suiteRunNativeParallelAttempt struct {
	AttemptDir string `json:"attemptDir"`
	OK         bool   `json:"ok"`
}

func parseSuiteRunNativeParallelSummary(t *testing.T, stdout []byte) suiteRunNativeParallelSummary {
	t.Helper()
	var sum suiteRunNativeParallelSummary
	if err := json.Unmarshal(stdout, &sum); err != nil {
		t.Fatalf("unmarshal suite run json: %v", err)
	}
	return sum
}

func assertSuiteRunNativeParallelSummary(t *testing.T, sum suiteRunNativeParallelSummary, missionCount int) {
	t.Helper()
	if !sum.OK || len(sum.Attempts) != missionCount {
		t.Fatalf("unexpected summary: %+v", sum)
	}
}

func assertSuiteRunNativeParallelSessionsUnique(t *testing.T, attempts []suiteRunNativeParallelAttempt) {
	t.Helper()
	seenSessions := map[string]bool{}
	for _, attempt := range attempts {
		if !attempt.OK {
			t.Fatalf("expected attempt ok=true: %+v", attempt)
		}
		sessionID := readSuiteRunNativeSessionID(t, attempt.AttemptDir)
		if seenSessions[sessionID] {
			t.Fatalf("duplicate sessionId detected: %s", sessionID)
		}
		seenSessions[sessionID] = true
	}
}

func readSuiteRunNativeSessionID(t *testing.T, attemptDir string) string {
	t.Helper()
	refRaw, err := os.ReadFile(filepath.Join(attemptDir, "runner.ref.json"))
	if err != nil {
		t.Fatalf("read runner.ref.json: %v", err)
	}
	var ref struct {
		SessionID string `json:"sessionId"`
	}
	if err := json.Unmarshal(refRaw, &ref); err != nil {
		t.Fatalf("unmarshal runner.ref.json: %v", err)
	}
	if strings.TrimSpace(ref.SessionID) == "" {
		t.Fatalf("expected non-empty sessionId in runner.ref.json")
	}
	return ref.SessionID
}

func TestSuiteRun_NativeTimeoutDoesNotAbortSiblingAttempts(t *testing.T) {
	outRoot := t.TempDir()
	suitePath := filepath.Join(t.TempDir(), "suite.json")
	writeSuiteFile(t, suitePath, `{
  "version": 1,
  "suiteId": "suite-run-native-timeout-isolation",
  "defaults": { "mode": "ci", "timeoutMs": 250, "timeoutStart": "attempt_start" },
  "missions": [
    { "missionId": "slow", "prompt": "slow mission", "expects": { "ok": true } },
    { "missionId": "fast", "prompt": "fast mission", "expects": { "ok": true } }
  ]
}`)

	t.Setenv("ZCL_CODEX_APP_SERVER_CMD", os.Args[0]+" -test.run=TestHelperSuiteNativeAppServer$")
	t.Setenv("ZCL_HELPER_PROCESS", "1")
	t.Setenv("ZCL_HELPER_MODE", "smoke")

	h := newRunnerHarness(t, time.Date(2026, 2, 16, 12, 25, 0, 0, time.UTC))
	code := h.Runner.Run([]string{
		"suite", "run",
		"--file", suitePath,
		"--out-root", outRoot,
		"--session-isolation", "native",
		"--parallel", "2",
		"--fail-fast=false",
		"--json",
	})
	if code == 1 {
		t.Fatalf("expected no harness failure, got code=%d stderr=%q", code, h.Stderr.String())
	}

	var sum struct {
		Attempts []struct {
			MissionID       string `json:"missionId"`
			RunnerErrorCode string `json:"runnerErrorCode"`
			OK              bool   `json:"ok"`
		} `json:"attempts"`
	}
	if err := json.Unmarshal(h.Stdout.Bytes(), &sum); err != nil {
		t.Fatalf("unmarshal suite run json: %v", err)
	}
	if len(sum.Attempts) != 2 {
		t.Fatalf("expected 2 attempts, got %+v", sum.Attempts)
	}
	var sawTimeout bool
	var sawSuccess bool
	for _, a := range sum.Attempts {
		if a.RunnerErrorCode == codeRuntimeStall {
			sawTimeout = true
		}
		if a.OK {
			sawSuccess = true
		}
	}
	if !sawTimeout || !sawSuccess {
		t.Fatalf("expected one timeout and one success, got %+v", sum.Attempts)
	}
}

func TestSuiteRun_NativeMapsRateLimitFailure(t *testing.T) {
	outRoot := t.TempDir()
	suitePath := filepath.Join(t.TempDir(), "suite.json")
	writeSuiteFile(t, suitePath, `{
  "version": 1,
  "suiteId": "suite-run-native-rate-limit",
  "defaults": { "mode": "ci", "timeoutMs": 60000 },
  "missions": [
    { "missionId": "m1", "prompt": "rate-limit mission" }
  ]
}`)

	t.Setenv("ZCL_CODEX_APP_SERVER_CMD", os.Args[0]+" -test.run=TestHelperSuiteNativeAppServer$")
	t.Setenv("ZCL_HELPER_PROCESS", "1")
	t.Setenv("ZCL_HELPER_MODE", "rate_limit_failure")

	h := newRunnerHarnessNowFunc(t, time.Now)

	code := h.Runner.Run([]string{
		"suite", "run",
		"--file", suitePath,
		"--out-root", outRoot,
		"--session-isolation", "native",
		"--json",
	})
	if code == 1 {
		t.Fatalf("expected non-harness failure path, got code=%d stderr=%q", code, h.Stderr.String())
	}

	var sum struct {
		Attempts []struct {
			RunnerErrorCode  string `json:"runnerErrorCode"`
			AutoFeedbackCode string `json:"autoFeedbackCode"`
		} `json:"attempts"`
	}
	if err := json.Unmarshal(h.Stdout.Bytes(), &sum); err != nil {
		t.Fatalf("unmarshal suite run output: %v", err)
	}
	if len(sum.Attempts) != 1 {
		t.Fatalf("expected one attempt, got %+v", sum.Attempts)
	}
	if sum.Attempts[0].RunnerErrorCode != codeRuntimeRateLimit || sum.Attempts[0].AutoFeedbackCode != codeRuntimeRateLimit {
		t.Fatalf("expected rate-limit code, got %+v", sum.Attempts[0])
	}
}

func TestSuiteRun_NativeMapsAuthFailure(t *testing.T) {
	outRoot := t.TempDir()
	suitePath := filepath.Join(t.TempDir(), "suite.json")
	writeSuiteFile(t, suitePath, `{
  "version": 1,
  "suiteId": "suite-run-native-auth",
  "defaults": { "mode": "ci", "timeoutMs": 60000 },
  "missions": [
    { "missionId": "m1", "prompt": "auth mission" }
  ]
}`)

	t.Setenv("ZCL_CODEX_APP_SERVER_CMD", os.Args[0]+" -test.run=TestHelperSuiteNativeAppServer$")
	t.Setenv("ZCL_HELPER_PROCESS", "1")
	t.Setenv("ZCL_HELPER_MODE", "auth_failure")

	h := newRunnerHarnessNowFunc(t, time.Now)

	code := h.Runner.Run([]string{
		"suite", "run",
		"--file", suitePath,
		"--out-root", outRoot,
		"--session-isolation", "native",
		"--json",
	})
	if code == 1 {
		t.Fatalf("expected non-harness failure path, got code=%d stderr=%q", code, h.Stderr.String())
	}

	var sum struct {
		Attempts []struct {
			RunnerErrorCode  string `json:"runnerErrorCode"`
			AutoFeedbackCode string `json:"autoFeedbackCode"`
		} `json:"attempts"`
	}
	if err := json.Unmarshal(h.Stdout.Bytes(), &sum); err != nil {
		t.Fatalf("unmarshal suite run output: %v", err)
	}
	if len(sum.Attempts) != 1 {
		t.Fatalf("expected one attempt, got %+v", sum.Attempts)
	}
	if sum.Attempts[0].RunnerErrorCode != codeRuntimeAuth || sum.Attempts[0].AutoFeedbackCode != codeRuntimeAuth {
		t.Fatalf("expected auth code, got %+v", sum.Attempts[0])
	}
}

func TestSuiteRun_NativeCrashDuringTurnIsTyped(t *testing.T) {
	outRoot := t.TempDir()
	suitePath := filepath.Join(t.TempDir(), "suite.json")
	writeSuiteFile(t, suitePath, `{
  "version": 1,
  "suiteId": "suite-run-native-crash",
  "defaults": { "mode": "ci", "timeoutMs": 60000 },
  "missions": [
    { "missionId": "m1", "prompt": "crash mission" }
  ]
}`)

	t.Setenv("ZCL_CODEX_APP_SERVER_CMD", os.Args[0]+" -test.run=TestHelperSuiteNativeAppServer$")
	t.Setenv("ZCL_HELPER_PROCESS", "1")
	t.Setenv("ZCL_HELPER_MODE", "crash_during_turn")

	h := newRunnerHarnessNowFunc(t, time.Now)

	code := h.Runner.Run([]string{
		"suite", "run",
		"--file", suitePath,
		"--out-root", outRoot,
		"--session-isolation", "native",
		"--json",
	})
	if code == 1 {
		t.Fatalf("expected non-harness failure path, got code=%d stderr=%q", code, h.Stderr.String())
	}

	var sum struct {
		Attempts []struct {
			RunnerErrorCode string `json:"runnerErrorCode"`
		} `json:"attempts"`
	}
	if err := json.Unmarshal(h.Stdout.Bytes(), &sum); err != nil {
		t.Fatalf("unmarshal suite run output: %v", err)
	}
	if len(sum.Attempts) != 1 {
		t.Fatalf("expected one attempt, got %+v", sum.Attempts)
	}
	if sum.Attempts[0].RunnerErrorCode != codeRuntimeCrash {
		t.Fatalf("expected runtime crash code, got %+v", sum.Attempts[0])
	}
}

func TestSuiteRun_NativeSchedulerRateLimitIsDeterministic(t *testing.T) {
	outRoot := t.TempDir()
	suitePath := filepath.Join(t.TempDir(), "suite.json")
	writeSuiteFile(t, suitePath, `{
  "version": 1,
  "suiteId": "suite-run-native-scheduler-rate-limit",
  "defaults": { "mode": "discovery", "timeoutMs": 60000 },
  "missions": [
    { "missionId": "m1", "prompt": "p1", "expects": { "ok": true } },
    { "missionId": "m2", "prompt": "p2", "expects": { "ok": true } },
    { "missionId": "m3", "prompt": "p3", "expects": { "ok": true } }
  ]
}`)

	t.Setenv("ZCL_CODEX_APP_SERVER_CMD", os.Args[0]+" -test.run=TestHelperSuiteNativeAppServer$")
	t.Setenv("ZCL_HELPER_PROCESS", "1")
	t.Setenv("ZCL_HELPER_MODE", "smoke")
	t.Setenv("ZCL_NATIVE_MIN_START_INTERVAL_MS", "220")

	h := newRunnerHarnessNowFunc(t, time.Now)

	start := time.Now()
	code := h.Runner.Run([]string{
		"suite", "run",
		"--file", suitePath,
		"--out-root", outRoot,
		"--session-isolation", "native",
		"--parallel", "3",
		"--total", "3",
		"--json",
	})
	elapsed := time.Since(start)
	if code != 0 {
		t.Fatalf("expected success, got code=%d stderr=%q", code, h.Stderr.String())
	}
	if elapsed < 200*time.Millisecond {
		t.Fatalf("expected scheduler delay from rate limit, elapsed=%s", elapsed)
	}
}

func TestSuiteRun_AutoFeedbackOnTimeout(t *testing.T) {
	outRoot := t.TempDir()
	suitePath := filepath.Join(t.TempDir(), "suite.json")
	writeSuiteFile(t, suitePath, `{
  "version": 1,
  "suiteId": "suite-run-timeout-auto-feedback",
  "defaults": { "mode": "ci", "timeoutMs": 40, "timeoutStart": "attempt_start" },
  "missions": [
    { "missionId": "m1", "prompt": "p1" }
  ]
}`)

	t.Setenv("ZCL_WANT_SUITE_RUNNER", "1")

	h := newRunnerHarness(t, suiteRunNow())

	code := h.Runner.Run([]string{
		"suite", "run",
		"--file", suitePath,
		"--out-root", outRoot,
		"--json",
		"--",
		os.Args[0], "-test.run=TestHelperSuiteRunnerProcess$", "--", "case=sleep",
	})
	if code != 1 {
		t.Fatalf("expected exit code 1 for timeout harness error, got %d (stderr=%q)", code, h.Stderr.String())
	}

	var sum struct {
		Attempts []struct {
			AttemptDir       string `json:"attemptDir"`
			AutoFeedback     bool   `json:"autoFeedback"`
			AutoFeedbackCode string `json:"autoFeedbackCode"`
			Finish           struct {
				ReportError *struct {
					Code string `json:"code"`
				} `json:"reportError"`
			} `json:"finish"`
		} `json:"attempts"`
	}
	if err := json.Unmarshal(h.Stdout.Bytes(), &sum); err != nil {
		t.Fatalf("unmarshal suite run json: %v (stdout=%q)", err, h.Stdout.String())
	}
	if len(sum.Attempts) != 1 {
		t.Fatalf("expected one attempt, got %+v", sum.Attempts)
	}
	a := sum.Attempts[0]
	if !a.AutoFeedback || a.AutoFeedbackCode != "ZCL_E_TIMEOUT" {
		t.Fatalf("expected auto timeout feedback, got %+v", a)
	}
	if a.Finish.ReportError != nil && a.Finish.ReportError.Code == "ZCL_E_MISSING_ARTIFACT" {
		t.Fatalf("expected timeout attempt not to fail with missing feedback artifact: %+v", a.Finish.ReportError)
	}

	fbPath := filepath.Join(a.AttemptDir, "feedback.json")
	fbBytes, err := os.ReadFile(fbPath)
	if err != nil {
		t.Fatalf("read feedback.json: %v", err)
	}
	var fb struct {
		OK         bool `json:"ok"`
		ResultJSON struct {
			Kind string `json:"kind"`
			Code string `json:"code"`
		} `json:"resultJson"`
	}
	if err := json.Unmarshal(fbBytes, &fb); err != nil {
		t.Fatalf("unmarshal feedback.json: %v", err)
	}
	if fb.OK || fb.ResultJSON.Kind != "infra_failure" || fb.ResultJSON.Code != "ZCL_E_TIMEOUT" {
		t.Fatalf("unexpected synthetic feedback payload: %+v", fb)
	}
}

func TestSuiteRun_FeedbackPolicyStrict_DoesNotAutoFinalize(t *testing.T) {
	outRoot := t.TempDir()
	suitePath := filepath.Join(t.TempDir(), "suite.json")
	writeSuiteFile(t, suitePath, `{
  "version": 1,
  "suiteId": "suite-run-feedback-policy-strict",
  "defaults": { "mode": "discovery", "timeoutMs": 60000 },
  "missions": [
    { "missionId": "m1", "prompt": "p1" }
  ]
}`)

	t.Setenv("ZCL_WANT_SUITE_RUNNER", "1")

	h := newRunnerHarness(t, suiteRunNow())

	code := h.Runner.Run([]string{
		"suite", "run",
		"--file", suitePath,
		"--out-root", outRoot,
		"--feedback-policy", "strict",
		"--json",
		"--",
		os.Args[0], "-test.run=TestHelperSuiteRunnerProcess$", "--", "case=no-feedback",
	})
	if code != 2 {
		t.Fatalf("expected exit code 2, got %d (stderr=%q)", code, h.Stderr.String())
	}

	var sum struct {
		FeedbackPolicy string `json:"feedbackPolicy"`
		Attempts       []struct {
			AutoFeedback bool `json:"autoFeedback"`
			Finish       struct {
				ReportError *struct {
					Code string `json:"code"`
				} `json:"reportError"`
			} `json:"finish"`
		} `json:"attempts"`
	}
	if err := json.Unmarshal(h.Stdout.Bytes(), &sum); err != nil {
		t.Fatalf("unmarshal suite run json: %v (stdout=%q)", err, h.Stdout.String())
	}
	if sum.FeedbackPolicy != "strict" {
		t.Fatalf("expected feedbackPolicy=strict, got %+v", sum)
	}
	if len(sum.Attempts) != 1 {
		t.Fatalf("expected one attempt, got %+v", sum.Attempts)
	}
	if sum.Attempts[0].AutoFeedback {
		t.Fatalf("expected no auto feedback in strict policy")
	}
	if sum.Attempts[0].Finish.ReportError == nil || sum.Attempts[0].Finish.ReportError.Code != "ZCL_E_MISSING_ARTIFACT" {
		t.Fatalf("expected missing artifact report error, got %+v", sum.Attempts[0].Finish.ReportError)
	}
}

func TestSuiteRun_WritesCampaignStateAndProgress(t *testing.T) {
	outRoot := t.TempDir()
	progressPath := filepath.Join(t.TempDir(), "suite.progress.jsonl")
	suitePath := filepath.Join(t.TempDir(), "suite.json")
	writeSuiteFile(t, suitePath, `{
  "version": 1,
  "suiteId": "suite-run-campaign",
  "defaults": { "mode": "discovery", "timeoutMs": 60000 },
  "missions": [
    { "missionId": "m1", "prompt": "p1", "expects": { "ok": true } }
  ]
}`)

	t.Setenv("ZCL_WANT_SUITE_RUNNER", "1")

	h := newRunnerHarness(t, suiteRunNow())

	code := h.Runner.Run([]string{
		"suite", "run",
		"--file", suitePath,
		"--out-root", outRoot,
		"--campaign-id", "campaign-alpha",
		"--progress-jsonl", progressPath,
		"--json",
		"--",
		os.Args[0], "-test.run=TestHelperSuiteRunnerProcess$", "--", "case=ok",
	})
	if code != 0 {
		t.Fatalf("expected exit code 0, got %d (stderr=%q)", code, h.Stderr.String())
	}

	var sum struct {
		RunID             string `json:"runId"`
		CampaignID        string `json:"campaignId"`
		CampaignStatePath string `json:"campaignStatePath"`
	}
	if err := json.Unmarshal(h.Stdout.Bytes(), &sum); err != nil {
		t.Fatalf("unmarshal suite run json: %v (stdout=%q)", err, h.Stdout.String())
	}
	if sum.RunID == "" || sum.CampaignID != "campaign-alpha" {
		t.Fatalf("unexpected summary campaign fields: %+v", sum)
	}

	progressBytes, err := os.ReadFile(progressPath)
	if err != nil {
		t.Fatalf("read progress jsonl: %v", err)
	}
	if !strings.Contains(string(progressBytes), `"kind":"run_started"`) || !strings.Contains(string(progressBytes), `"kind":"run_finished"`) {
		t.Fatalf("expected run_started/run_finished events, got %s", string(progressBytes))
	}

	statePath := sum.CampaignStatePath
	if statePath == "" {
		statePath = filepath.Join(outRoot, "campaigns", "campaign-alpha", "campaign.state.json")
	}
	raw, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatalf("read campaign state: %v", err)
	}
	var st struct {
		SchemaVersion int    `json:"schemaVersion"`
		CampaignID    string `json:"campaignId"`
		SuiteID       string `json:"suiteId"`
		LatestRunID   string `json:"latestRunId"`
	}
	if err := json.Unmarshal(raw, &st); err != nil {
		t.Fatalf("unmarshal campaign state: %v", err)
	}
	if st.SchemaVersion != 1 || st.CampaignID != "campaign-alpha" || st.SuiteID != "suite-run-campaign" || st.LatestRunID != sum.RunID {
		t.Fatalf("unexpected campaign state: %+v", st)
	}
}

func TestSuiteRun_FinalizationAutoFromResultFileJSON(t *testing.T) {
	outRoot := t.TempDir()
	suitePath := filepath.Join(t.TempDir(), "suite.json")
	writeSuiteFile(t, suitePath, `{
  "version": 1,
  "suiteId": "suite-run-result-file",
  "missions": [
    { "missionId": "m1", "prompt": "p1", "expects": { "ok": true } }
  ]
}`)

	t.Setenv("ZCL_WANT_SUITE_RUNNER", "1")

	h := newRunnerHarness(t, time.Date(2026, 2, 22, 20, 0, 0, 0, time.UTC))

	code := h.Runner.Run([]string{
		"suite", "run",
		"--file", suitePath,
		"--out-root", outRoot,
		"--finalization-mode", "auto_from_result_json",
		"--result-channel", "file_json",
		"--json",
		"--",
		os.Args[0], "-test.run=TestHelperSuiteRunnerProcess$", "--", "case=result-file-ok",
	})
	if code != 0 {
		t.Fatalf("expected exit code 0, got %d (stderr=%q)", code, h.Stderr.String())
	}

	var sum struct {
		OK       bool `json:"ok"`
		Attempts []struct {
			AttemptDir       string `json:"attemptDir"`
			AutoFeedback     bool   `json:"autoFeedback"`
			AutoFeedbackCode string `json:"autoFeedbackCode"`
			Finish           struct {
				OK bool `json:"ok"`
			} `json:"finish"`
		} `json:"attempts"`
	}
	if err := json.Unmarshal(h.Stdout.Bytes(), &sum); err != nil {
		t.Fatalf("unmarshal suite run json: %v (stdout=%q)", err, h.Stdout.String())
	}
	if !sum.OK || len(sum.Attempts) != 1 || !sum.Attempts[0].Finish.OK {
		t.Fatalf("unexpected summary: %+v", sum)
	}
	if !sum.Attempts[0].AutoFeedback || sum.Attempts[0].AutoFeedbackCode != "" {
		t.Fatalf("expected auto feedback from result channel without infra code, got %+v", sum.Attempts[0])
	}

	fbBytes, err := os.ReadFile(filepath.Join(sum.Attempts[0].AttemptDir, "feedback.json"))
	if err != nil {
		t.Fatalf("read feedback.json: %v", err)
	}
	var fb struct {
		OK         bool `json:"ok"`
		ResultJSON struct {
			Proof string `json:"proof"`
		} `json:"resultJson"`
	}
	if err := json.Unmarshal(fbBytes, &fb); err != nil {
		t.Fatalf("unmarshal feedback.json: %v", err)
	}
	if !fb.OK || fb.ResultJSON.Proof != "file-channel-ok" {
		t.Fatalf("unexpected feedback payload: %+v", fb)
	}
}

func TestSuiteRun_FinalizationAutoFromResultStdoutJSON(t *testing.T) {
	outRoot := t.TempDir()
	suitePath := filepath.Join(t.TempDir(), "suite.json")
	writeSuiteFile(t, suitePath, `{
  "version": 1,
  "suiteId": "suite-run-result-stdout",
  "missions": [
    { "missionId": "m1", "prompt": "p1", "expects": { "ok": true } }
  ]
}`)

	t.Setenv("ZCL_WANT_SUITE_RUNNER", "1")

	h := newRunnerHarness(t, time.Date(2026, 2, 22, 20, 5, 0, 0, time.UTC))

	code := h.Runner.Run([]string{
		"suite", "run",
		"--file", suitePath,
		"--out-root", outRoot,
		"--capture-runner-io=false",
		"--finalization-mode", "auto_from_result_json",
		"--result-channel", "stdout_json",
		"--json",
		"--",
		os.Args[0], "-test.run=TestHelperSuiteRunnerProcess$", "--", "case=result-stdout-ok",
	})
	if code != 0 {
		t.Fatalf("expected exit code 0, got %d (stderr=%q)", code, h.Stderr.String())
	}

	var sum struct {
		OK       bool `json:"ok"`
		Attempts []struct {
			AutoFeedbackCode string `json:"autoFeedbackCode"`
			Finish           struct {
				OK bool `json:"ok"`
			} `json:"finish"`
		} `json:"attempts"`
	}
	if err := json.Unmarshal(h.Stdout.Bytes(), &sum); err != nil {
		t.Fatalf("unmarshal suite run json: %v (stdout=%q)", err, h.Stdout.String())
	}
	if !sum.OK || len(sum.Attempts) != 1 || !sum.Attempts[0].Finish.OK || sum.Attempts[0].AutoFeedbackCode != "" {
		t.Fatalf("unexpected summary: %+v", sum)
	}
}

func TestSuiteRun_FinalizationAutoFromResultInvalidWritesTypedFailure(t *testing.T) {
	outRoot := t.TempDir()
	suitePath := filepath.Join(t.TempDir(), "suite.json")
	writeSuiteFile(t, suitePath, `{
  "version": 1,
  "suiteId": "suite-run-result-invalid",
  "missions": [
    { "missionId": "m1", "prompt": "p1" }
  ]
}`)

	t.Setenv("ZCL_WANT_SUITE_RUNNER", "1")

	h := newRunnerHarness(t, time.Date(2026, 2, 22, 20, 10, 0, 0, time.UTC))

	code := h.Runner.Run([]string{
		"suite", "run",
		"--file", suitePath,
		"--out-root", outRoot,
		"--finalization-mode", "auto_from_result_json",
		"--result-channel", "file_json",
		"--json",
		"--",
		os.Args[0], "-test.run=TestHelperSuiteRunnerProcess$", "--", "case=result-file-invalid",
	})
	if code != 2 {
		t.Fatalf("expected exit code 2, got %d (stderr=%q)", code, h.Stderr.String())
	}
	var sum struct {
		Attempts []struct {
			AutoFeedbackCode string `json:"autoFeedbackCode"`
		} `json:"attempts"`
	}
	if err := json.Unmarshal(h.Stdout.Bytes(), &sum); err != nil {
		t.Fatalf("unmarshal suite run json: %v (stdout=%q)", err, h.Stdout.String())
	}
	if len(sum.Attempts) != 1 || sum.Attempts[0].AutoFeedbackCode != "ZCL_E_MISSION_RESULT_INVALID" {
		t.Fatalf("expected typed result-channel invalid code, got %+v", sum)
	}
}

func TestSuiteRun_FinalizationAutoFromResultNoTraceStillProducesEvidence(t *testing.T) {
	outRoot := t.TempDir()
	suitePath := filepath.Join(t.TempDir(), "suite.json")
	writeSuiteFile(t, suitePath, `{
  "version": 1,
  "suiteId": "suite-run-result-no-trace",
  "missions": [
    { "missionId": "m1", "prompt": "p1", "expects": { "ok": true } }
  ]
}`)

	t.Setenv("ZCL_WANT_SUITE_RUNNER", "1")

	h := newRunnerHarness(t, time.Date(2026, 2, 22, 20, 20, 0, 0, time.UTC))

	code := h.Runner.Run([]string{
		"suite", "run",
		"--file", suitePath,
		"--out-root", outRoot,
		"--finalization-mode", "auto_from_result_json",
		"--result-channel", "file_json",
		"--json",
		"--",
		os.Args[0], "-test.run=TestHelperSuiteRunnerProcess$", "--", "case=result-file-no-trace-ok",
	})
	if code != 0 {
		t.Fatalf("expected exit code 0, got %d (stderr=%q)", code, h.Stderr.String())
	}
	var sum struct {
		Attempts []struct {
			AttemptDir string `json:"attemptDir"`
		} `json:"attempts"`
	}
	if err := json.Unmarshal(h.Stdout.Bytes(), &sum); err != nil {
		t.Fatalf("unmarshal suite run json: %v (stdout=%q)", err, h.Stdout.String())
	}
	if len(sum.Attempts) != 1 {
		t.Fatalf("expected one attempt, got %+v", sum)
	}
	tracePath := filepath.Join(sum.Attempts[0].AttemptDir, "tool.calls.jsonl")
	traceBytes, err := os.ReadFile(tracePath)
	if err != nil {
		t.Fatalf("read tool.calls.jsonl: %v", err)
	}
	if !strings.Contains(string(traceBytes), "suite-runner-result-channel") {
		t.Fatalf("expected synthetic result-channel trace event, got %s", string(traceBytes))
	}
}

func TestSuiteRun_FinalizationAutoFromResultMinTurnRejectsEarlyTurn(t *testing.T) {
	outRoot := t.TempDir()
	suitePath := filepath.Join(t.TempDir(), "suite.json")
	writeSuiteFile(t, suitePath, `{
  "version": 1,
  "suiteId": "suite-run-result-min-turn",
  "missions": [
    { "missionId": "m1", "prompt": "p1" }
  ]
}`)

	t.Setenv("ZCL_WANT_SUITE_RUNNER", "1")

	h := newRunnerHarness(t, time.Date(2026, 2, 22, 20, 30, 0, 0, time.UTC))

	code := h.Runner.Run([]string{
		"suite", "run",
		"--file", suitePath,
		"--out-root", outRoot,
		"--finalization-mode", "auto_from_result_json",
		"--result-channel", "file_json",
		"--result-min-turn", "3",
		"--json",
		"--",
		os.Args[0], "-test.run=TestHelperSuiteRunnerProcess$", "--", "case=result-file-turn-2",
	})
	if code != 2 {
		t.Fatalf("expected exit code 2, got %d (stderr=%q)", code, h.Stderr.String())
	}
	var sum struct {
		Attempts []struct {
			AutoFeedbackCode string `json:"autoFeedbackCode"`
		} `json:"attempts"`
	}
	if err := json.Unmarshal(h.Stdout.Bytes(), &sum); err != nil {
		t.Fatalf("unmarshal suite run json: %v (stdout=%q)", err, h.Stdout.String())
	}
	if len(sum.Attempts) != 1 || sum.Attempts[0].AutoFeedbackCode != "ZCL_E_MISSION_RESULT_TURN_TOO_EARLY" {
		t.Fatalf("expected turn-too-early code, got %+v", sum)
	}
}

func TestSuiteRun_FinalizationAutoFromResultMinTurnAcceptsFinalTurn(t *testing.T) {
	outRoot := t.TempDir()
	suitePath := filepath.Join(t.TempDir(), "suite.json")
	writeSuiteFile(t, suitePath, `{
  "version": 1,
  "suiteId": "suite-run-result-min-turn-ok",
  "missions": [
    { "missionId": "m1", "prompt": "p1", "expects": { "ok": true } }
  ]
}`)

	t.Setenv("ZCL_WANT_SUITE_RUNNER", "1")

	h := newRunnerHarness(t, time.Date(2026, 2, 22, 20, 35, 0, 0, time.UTC))

	code := h.Runner.Run([]string{
		"suite", "run",
		"--file", suitePath,
		"--out-root", outRoot,
		"--finalization-mode", "auto_from_result_json",
		"--result-channel", "file_json",
		"--result-min-turn", "3",
		"--json",
		"--",
		os.Args[0], "-test.run=TestHelperSuiteRunnerProcess$", "--", "case=result-file-turn-3",
	})
	if code != 0 {
		t.Fatalf("expected exit code 0, got %d (stderr=%q)", code, h.Stderr.String())
	}
	var sum struct {
		OK       bool `json:"ok"`
		Attempts []struct {
			AutoFeedbackCode string `json:"autoFeedbackCode"`
			Finish           struct {
				OK bool `json:"ok"`
			} `json:"finish"`
		} `json:"attempts"`
	}
	if err := json.Unmarshal(h.Stdout.Bytes(), &sum); err != nil {
		t.Fatalf("unmarshal suite run json: %v (stdout=%q)", err, h.Stdout.String())
	}
	if !sum.OK || len(sum.Attempts) != 1 || !sum.Attempts[0].Finish.OK || sum.Attempts[0].AutoFeedbackCode != "" {
		t.Fatalf("unexpected summary: %+v", sum)
	}
}

func TestHelperSuiteRunnerProcess(t *testing.T) {
	TestHelperSuiteRunnerProcessCore(t)
}

func TestHelperSuiteRunnerProcessCore(t *testing.T) {
	if os.Getenv("ZCL_WANT_SUITE_RUNNER") != "1" {
		return
	}

	kind, exitCode := parseSuiteRunnerProcessArgs(os.Args)
	runSuiteRunnerProcessCase(kind, exitCode, suiteRunnerProcessTestRunner())
}

func parseSuiteRunnerProcessArgs(args []string) (string, int) {
	idx := indexAfterArgSeparator(args)
	kind := "ok"
	exitCode := 0
	for _, a := range args[idx:] {
		if strings.HasPrefix(a, "case=") {
			kind = strings.TrimPrefix(a, "case=")
			continue
		}
		if strings.HasPrefix(a, "exit=") {
			n, _ := strconv.Atoi(strings.TrimPrefix(a, "exit="))
			exitCode = n
		}
	}
	return kind, exitCode
}

func indexAfterArgSeparator(args []string) int {
	for i := range args {
		if args[i] == "--" {
			return i + 1
		}
	}
	return 0
}

func suiteRunnerProcessTestRunner() Runner {
	return Runner{
		Version: "0.0.0-dev",
		Now:     func() time.Time { return time.Date(2026, 2, 16, 12, 0, 0, 0, time.UTC) },
		Stdout:  os.Stdout,
		Stderr:  os.Stderr,
	}
}

func runSuiteRunnerProcessCase(kind string, exitCode int, r Runner) {
	switch kind {
	case "ok":
		runSuiteRunnerProcessCaseOK(r, exitCode)
	case "no-feedback":
		runSuiteRunnerProcessCaseNoFeedback(r, exitCode)
	case "result-file-ok":
		runSuiteRunnerProcessCaseWriteResultFile(r, exitCode, `{"ok":true,"resultJson":{"proof":"file-channel-ok"}}`, 104, 105)
	case "result-file-no-trace-ok":
		runSuiteRunnerProcessCaseWriteResultFileNoRun(exitCode, `{"ok":true,"resultJson":{"proof":"file-channel-no-trace"}}`, 106, 107)
	case "result-file-invalid":
		runSuiteRunnerProcessCaseWriteResultFile(r, exitCode, `{"ok":`, 108, 109)
	case "result-file-turn-2":
		runSuiteRunnerProcessCaseWriteResultFile(r, exitCode, `{"ok":true,"turn":2,"resultJson":{"proof":"turn-2"}}`, 110, 111)
	case "result-file-turn-3":
		runSuiteRunnerProcessCaseWriteResultFile(r, exitCode, `{"ok":true,"turn":3,"resultJson":{"proof":"turn-3"}}`, 112, 113)
	case "result-stdout-ok":
		runSuiteRunnerProcessCaseResultStdout(r, exitCode)
	case "infra-feedback-only":
		runSuiteRunnerProcessCaseInfraFeedbackOnly(r, exitCode)
	case "sleep":
		time.Sleep(3 * time.Second)
		os.Exit(exitCode)
	default:
		os.Exit(103)
	}
}

func runSuiteRunnerProcessCaseOK(r Runner, exitCode int) {
	if code := r.Run([]string{"run", "--", "echo", "hi"}); code != 0 {
		os.Exit(101)
	}
	if code := r.Run([]string{"feedback", "--ok", "--result", "ok"}); code != 0 {
		os.Exit(102)
	}
	os.Exit(exitCode)
}

func runSuiteRunnerProcessCaseNoFeedback(r Runner, exitCode int) {
	_ = r.Run([]string{"run", "--", "echo", "hi"})
	os.Exit(exitCode)
}

func runSuiteRunnerProcessCaseWriteResultFile(r Runner, exitCode int, payload string, missingPathExit int, writeExit int) {
	_ = r.Run([]string{"run", "--", "echo", "hi"})
	runSuiteRunnerProcessWriteResultFile(exitCode, payload, missingPathExit, writeExit)
}

func runSuiteRunnerProcessCaseWriteResultFileNoRun(exitCode int, payload string, missingPathExit int, writeExit int) {
	runSuiteRunnerProcessWriteResultFile(exitCode, payload, missingPathExit, writeExit)
}

func runSuiteRunnerProcessWriteResultFile(exitCode int, payload string, missingPathExit int, writeExit int) {
	path := strings.TrimSpace(os.Getenv("ZCL_MISSION_RESULT_PATH"))
	if path == "" {
		os.Exit(missingPathExit)
	}
	if err := os.WriteFile(path, []byte(payload), 0o644); err != nil {
		os.Exit(writeExit)
	}
	os.Exit(exitCode)
}

func runSuiteRunnerProcessCaseResultStdout(r Runner, exitCode int) {
	_ = r.Run([]string{"run", "--", "echo", "hi"})
	marker := strings.TrimSpace(os.Getenv("ZCL_MISSION_RESULT_MARKER"))
	if marker == "" {
		marker = "ZCL_RESULT_JSON:"
	}
	_, _ = os.Stdout.WriteString(marker + `{"ok":true,"resultJson":{"proof":"stdout-channel-ok"}}` + "\n")
	os.Exit(exitCode)
}

func runSuiteRunnerProcessCaseInfraFeedbackOnly(r Runner, exitCode int) {
	_ = r.Run([]string{"run", "--", "echo", "hi"})
	if code := r.Run([]string{"feedback", "--fail", "--result-json", `{"kind":"infra_failure","code":"ZCL_E_RUNTIME_TIMEOUT","source":"suite_run"}`}); code != 0 {
		os.Exit(114)
	}
	os.Exit(exitCode)
}

func TestHelperSuiteNativeAppServer(t *testing.T) {
	if os.Getenv("ZCL_HELPER_PROCESS") != "1" {
		return
	}
	mode := strings.TrimSpace(os.Getenv("ZCL_HELPER_MODE"))
	if mode == "" {
		mode = "smoke"
	}
	runSuiteNativeHelper(mode)
	os.Exit(0)
}

func runSuiteNativeHelper(mode string) {
	runSuiteNativeHelperCore(mode)
}

func runSuiteNativeHelperCore(mode string) {
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	ctx := suiteNativeHelperContext{
		mode:     mode,
		threadID: "thr_native_1",
		turnID:   "turn_native_1",
		writer:   &suiteNativeHelperWriter{},
	}
	for scanner.Scan() {
		if stop := handleSuiteNativeHelperLine(strings.TrimSpace(scanner.Text()), &ctx); stop {
			return
		}
	}
}

type suiteNativeHelperContext struct {
	mode     string
	threadID string
	turnID   string
	writer   *suiteNativeHelperWriter
}

type suiteNativeHelperWriter struct {
	mu sync.Mutex
}

func (w *suiteNativeHelperWriter) writeJSON(v any) {
	b, _ := json.Marshal(v)
	w.mu.Lock()
	defer w.mu.Unlock()
	_, _ = os.Stdout.Write(append(b, '\n'))
}

func (w *suiteNativeHelperWriter) respond(id any, result any) {
	w.writeJSON(map[string]any{"id": id, "result": result})
}

func (w *suiteNativeHelperWriter) respondErr(id any, code int, message string) {
	w.writeJSON(map[string]any{"id": id, "error": map[string]any{"code": code, "message": message}})
}

func handleSuiteNativeHelperLine(line string, ctx *suiteNativeHelperContext) bool {
	if line == "" {
		return false
	}
	msg := parseSuiteNativeHelperMessage(line)
	if msg == nil {
		return false
	}
	id, hasID := msg["id"]
	if !hasID {
		return false
	}
	method, _ := msg["method"].(string)
	switch method {
	case "initialize":
		ctx.writer.respond(id, map[string]any{"userAgent": "codex-cli/1.4.0"})
	case "model/list":
		handleSuiteNativeHelperModelList(id, ctx)
	case "thread/start":
		handleSuiteNativeHelperThreadStart(msg, id, ctx)
	case "thread/resume":
		ctx.writer.respond(id, map[string]any{"thread": map[string]any{"id": ctx.threadID}})
	case "turn/start":
		if stop := handleSuiteNativeHelperTurnStart(msg, id, ctx); stop {
			return true
		}
	case "turn/steer":
		ctx.writer.respond(id, map[string]any{"turnId": ctx.turnID})
	case "turn/interrupt":
		ctx.writer.respond(id, map[string]any{})
	default:
		ctx.writer.respondErr(id, -32601, fmt.Sprintf("method not found: %s", method))
	}
	return false
}

func parseSuiteNativeHelperMessage(line string) map[string]any {
	var msg map[string]any
	if err := json.Unmarshal([]byte(line), &msg); err != nil {
		return nil
	}
	return msg
}

func handleSuiteNativeHelperModelList(id any, ctx *suiteNativeHelperContext) {
	switch ctx.mode {
	case "compat_missing_method":
		ctx.writer.respondErr(id, -32601, "method not found")
	case "timeout_on_model_list":
		time.Sleep(2 * time.Second)
	case "assert_model_forwarded":
		ctx.writer.respond(id, map[string]any{
			"data": []any{
				map[string]any{
					"id":          "gpt-5.3-codex-spark",
					"model":       "gpt-5.3-codex-spark",
					"isDefault":   true,
					"displayName": "Spark",
					"supportedReasoningEfforts": []any{
						map[string]any{"reasoningEffort": "medium"},
						map[string]any{"reasoningEffort": "high"},
					},
					"defaultReasoningEffort": "medium",
				},
			},
		})
	case "reasoning_unsupported":
		ctx.writer.respond(id, map[string]any{
			"data": []any{
				map[string]any{
					"id":          "gpt-5.3-codex-spark",
					"model":       "gpt-5.3-codex-spark",
					"isDefault":   true,
					"displayName": "Spark",
					"supportedReasoningEfforts": []any{
						map[string]any{"reasoningEffort": "low"},
					},
					"defaultReasoningEffort": "low",
				},
			},
		})
	default:
		ctx.writer.respond(id, map[string]any{"data": []any{}})
	}
}

func handleSuiteNativeHelperThreadStart(msg map[string]any, id any, ctx *suiteNativeHelperContext) {
	params, _ := msg["params"].(map[string]any)
	if !validateSuiteNativeThreadStartCwd(params, id, ctx) {
		return
	}
	if !validateSuiteNativeThreadStartMode(params, id, ctx) {
		return
	}
	ctx.writer.respond(id, map[string]any{"thread": map[string]any{"id": ctx.threadID}})
	ctx.writer.writeJSON(map[string]any{"method": "thread/started", "params": map[string]any{"threadId": ctx.threadID}})
}

func validateSuiteNativeThreadStartCwd(params map[string]any, id any, ctx *suiteNativeHelperContext) bool {
	gotCwd, _ := params["cwd"].(string)
	expectedCwd := strings.TrimSpace(os.Getenv("ZCL_HELPER_EXPECT_CWD"))
	if expectedCwd != "" && filepath.Clean(strings.TrimSpace(gotCwd)) != filepath.Clean(expectedCwd) {
		ctx.writer.respondErr(id, -32000, fmt.Sprintf("thread/start cwd mismatch got=%q want=%q", gotCwd, expectedCwd))
		return false
	}
	expectedCwdPrefix := strings.TrimSpace(os.Getenv("ZCL_HELPER_EXPECT_CWD_PREFIX"))
	if expectedCwdPrefix != "" && !strings.HasPrefix(strings.TrimSpace(gotCwd), expectedCwdPrefix) {
		ctx.writer.respondErr(id, -32000, fmt.Sprintf("thread/start cwd prefix mismatch got=%q wantPrefix=%q", gotCwd, expectedCwdPrefix))
		return false
	}
	return true
}

func validateSuiteNativeThreadStartMode(params map[string]any, id any, ctx *suiteNativeHelperContext) bool {
	switch ctx.mode {
	case "invalid_model":
		gotModel, _ := params["model"].(string)
		if strings.TrimSpace(gotModel) == "invalid-model-id" {
			ctx.writer.respondErr(id, -32000, "thread/start: unknown model invalid-model-id")
			return false
		}
	case "assert_model_forwarded":
		if !validateSuiteNativeThreadStartModel(params, id, ctx) {
			return false
		}
		if !validateSuiteNativeThreadStartReasoning(params, id, ctx) {
			return false
		}
	case "reasoning_unsupported":
		cfg, _ := params["config"].(map[string]any)
		if effort, _ := cfg["model_reasoning_effort"].(string); strings.TrimSpace(effort) != "" {
			ctx.writer.respondErr(id, -32000, "thread/start: reasoning effort unsupported for selected model")
			return false
		}
	}
	return true
}

func validateSuiteNativeThreadStartModel(params map[string]any, id any, ctx *suiteNativeHelperContext) bool {
	expectedModel := strings.TrimSpace(os.Getenv("ZCL_HELPER_EXPECT_MODEL"))
	if expectedModel == "" {
		return true
	}
	gotModel, _ := params["model"].(string)
	if strings.TrimSpace(gotModel) != expectedModel {
		ctx.writer.respondErr(id, -32000, fmt.Sprintf("thread/start model mismatch got=%q want=%q", gotModel, expectedModel))
		return false
	}
	return true
}

func validateSuiteNativeThreadStartReasoning(params map[string]any, id any, ctx *suiteNativeHelperContext) bool {
	expectedEffort := strings.TrimSpace(os.Getenv("ZCL_HELPER_EXPECT_REASONING_EFFORT"))
	if expectedEffort == "" {
		return true
	}
	cfg, _ := params["config"].(map[string]any)
	gotEffort, _ := cfg["model_reasoning_effort"].(string)
	if strings.TrimSpace(gotEffort) != expectedEffort {
		ctx.writer.respondErr(id, -32000, fmt.Sprintf("thread/start reasoning mismatch got=%q want=%q", gotEffort, expectedEffort))
		return false
	}
	return true
}

func handleSuiteNativeHelperTurnStart(msg map[string]any, id any, ctx *suiteNativeHelperContext) bool {
	ctx.writer.respond(id, map[string]any{"turn": map[string]any{"id": ctx.turnID, "status": "inProgress", "items": []any{}}})
	ctx.writer.writeJSON(map[string]any{"method": "turn/started", "params": map[string]any{"threadId": ctx.threadID, "turnId": ctx.turnID}})
	slow := suiteNativeHelperTurnIsSlow(msg)
	switch ctx.mode {
	case "rate_limit_failure":
		emitSuiteNativeRateLimitFailure(ctx)
	case "auth_failure":
		emitSuiteNativeAuthFailure(ctx)
	case "crash_during_turn":
		os.Exit(137)
	case "disconnect_during_turn":
		return true
	case "task_complete_preferred":
		emitSuiteNativeTaskCompletePreferred(ctx)
	case "phase_final_answer":
		emitSuiteNativePhaseFinalAnswer(ctx)
	case "phase_without_final_answer":
		emitSuiteNativePhaseWithoutFinalAnswer(ctx)
	default:
		emitSuiteNativeDefaultTurnResult(ctx, slow)
	}
	return false
}

func suiteNativeHelperTurnIsSlow(msg map[string]any) bool {
	params, ok := msg["params"].(map[string]any)
	if !ok {
		return false
	}
	input, ok := params["input"].([]any)
	if !ok {
		return false
	}
	for _, item := range input {
		obj, _ := item.(map[string]any)
		text, _ := obj["text"].(string)
		if strings.Contains(strings.ToLower(text), "slow") {
			return true
		}
	}
	return false
}

func emitSuiteNativeRateLimitFailure(ctx *suiteNativeHelperContext) {
	ctx.writer.writeJSON(map[string]any{
		"method": "turn/failed",
		"params": map[string]any{
			"threadId": ctx.threadID,
			"turnId":   ctx.turnID,
			"turn": map[string]any{
				"id":     ctx.turnID,
				"status": "failed",
				"error": map[string]any{
					"message":        "usage limit exceeded",
					"codexErrorInfo": "UsageLimitExceeded",
				},
			},
		},
	})
}

func emitSuiteNativeAuthFailure(ctx *suiteNativeHelperContext) {
	ctx.writer.writeJSON(map[string]any{
		"method": "turn/failed",
		"params": map[string]any{
			"threadId": ctx.threadID,
			"turnId":   ctx.turnID,
			"turn": map[string]any{
				"id":     ctx.turnID,
				"status": "failed",
				"error": map[string]any{
					"message": "unauthorized",
					"codexErrorInfo": map[string]any{
						"kind":           "HttpConnectionFailed",
						"httpStatusCode": "401",
					},
				},
			},
		},
	})
}

func emitSuiteNativeTaskCompletePreferred(ctx *suiteNativeHelperContext) {
	ctx.writer.writeJSON(suiteNativeItemCompletedMessage(ctx.threadID, ctx.turnID, "msg-commentary-1", "AgentMessage", "commentary", "Commentary message"))
	ctx.writer.writeJSON(suiteNativeReasoningCompletedMessage(ctx.threadID, ctx.turnID, "reasoning-1"))
	ctx.writer.writeJSON(suiteNativeDeltaMessage(ctx.threadID, ctx.turnID, "ignored-delta"))
	ctx.writer.writeJSON(map[string]any{
		"method": "codex/event/task_complete",
		"params": map[string]any{
			"msg": map[string]any{
				"type":               "task_complete",
				"turn_id":            ctx.turnID,
				"last_agent_message": "TASK_COMPLETE_FINAL",
			},
		},
	})
	ctx.writer.writeJSON(suiteNativeTurnCompletedMessage(ctx.threadID, ctx.turnID))
}

func emitSuiteNativePhaseFinalAnswer(ctx *suiteNativeHelperContext) {
	ctx.writer.writeJSON(suiteNativeItemCompletedMessage(ctx.threadID, ctx.turnID, "msg-commentary-2", "AgentMessage", "commentary", "Working..."))
	ctx.writer.writeJSON(suiteNativeItemCompletedMessage(ctx.threadID, ctx.turnID, "msg-final-2", "AgentMessage", "final_answer", "PHASE_FINAL_ANSWER"))
	ctx.writer.writeJSON(suiteNativeReasoningCompletedMessage(ctx.threadID, ctx.turnID, "reasoning-2"))
	ctx.writer.writeJSON(suiteNativeDeltaMessage(ctx.threadID, ctx.turnID, "ignored-delta"))
	ctx.writer.writeJSON(suiteNativeTurnCompletedMessage(ctx.threadID, ctx.turnID))
}

func emitSuiteNativePhaseWithoutFinalAnswer(ctx *suiteNativeHelperContext) {
	ctx.writer.writeJSON(suiteNativeItemCompletedMessage(ctx.threadID, ctx.turnID, "msg-commentary-3", "AgentMessage", "commentary", "Still working..."))
	ctx.writer.writeJSON(suiteNativeReasoningCompletedMessage(ctx.threadID, ctx.turnID, "reasoning-3"))
	ctx.writer.writeJSON(suiteNativeTurnCompletedMessage(ctx.threadID, ctx.turnID))
}

func emitSuiteNativeDefaultTurnResult(ctx *suiteNativeHelperContext, slow bool) {
	if slow {
		time.Sleep(900 * time.Millisecond)
	}
	ctx.writer.writeJSON(suiteNativeDeltaMessage(ctx.threadID, ctx.turnID, "native-result"))
	ctx.writer.writeJSON(suiteNativeTurnCompletedMessage(ctx.threadID, ctx.turnID))
}

func suiteNativeItemCompletedMessage(threadID string, turnID string, itemID string, itemType string, phase string, text string) map[string]any {
	return map[string]any{
		"method": "item/completed",
		"params": map[string]any{
			"threadId": threadID,
			"turnId":   turnID,
			"item": map[string]any{
				"id":    itemID,
				"type":  itemType,
				"phase": phase,
				"content": []any{
					map[string]any{"type": "Text", "text": text},
				},
			},
		},
	}
}

func suiteNativeReasoningCompletedMessage(threadID string, turnID string, itemID string) map[string]any {
	return map[string]any{
		"method": "item/completed",
		"params": map[string]any{
			"threadId": threadID,
			"turnId":   turnID,
			"item": map[string]any{
				"id":   itemID,
				"type": "Reasoning",
				"summary_text": []any{
					"reasoning summary",
				},
			},
		},
	}
}

func suiteNativeDeltaMessage(threadID string, turnID string, delta string) map[string]any {
	return map[string]any{"method": "item/agentMessage/delta", "params": map[string]any{"threadId": threadID, "turnId": turnID, "itemId": "itm_native_1", "delta": delta}}
}

func suiteNativeTurnCompletedMessage(threadID string, turnID string) map[string]any {
	return map[string]any{"method": "turn/completed", "params": map[string]any{"threadId": threadID, "turnId": turnID}}
}

func writeSuiteFile(t *testing.T, path string, contents string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatalf("write suite file: %v", err)
	}
}
