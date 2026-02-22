package cli

import (
	"bufio"
	"bytes"
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

func TestSuiteRun_OK_EndToEnd(t *testing.T) {
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

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	r := Runner{
		Version: "0.0.0-dev",
		Now:     func() time.Time { return time.Date(2026, 2, 16, 12, 0, 0, 0, time.UTC) },
		Stdout:  &stdout,
		Stderr:  &stderr,
	}

	code := r.Run([]string{
		"suite", "run",
		"--file", suitePath,
		"--out-root", outRoot,
		"--json",
		"--",
		os.Args[0], "-test.run=TestHelperSuiteRunnerProcess$", "--", "case=ok",
	})
	if code != 0 {
		t.Fatalf("expected exit code 0, got %d (stderr=%q)", code, stderr.String())
	}

	var sum struct {
		OK       bool `json:"ok"`
		Passed   int  `json:"passed"`
		Failed   int  `json:"failed"`
		Attempts []struct {
			MissionID      string `json:"missionId"`
			AttemptDir     string `json:"attemptDir"`
			RunnerExitCode *int   `json:"runnerExitCode"`
			Finish         struct {
				OK bool `json:"ok"`
			} `json:"finish"`
			OK bool `json:"ok"`
		} `json:"attempts"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &sum); err != nil {
		t.Fatalf("unmarshal suite run json: %v (stdout=%q)", err, stdout.String())
	}
	if !sum.OK || sum.Passed != 2 || sum.Failed != 0 || len(sum.Attempts) != 2 {
		t.Fatalf("unexpected summary: %+v", sum)
	}
	for _, a := range sum.Attempts {
		if a.RunnerExitCode == nil || *a.RunnerExitCode != 0 {
			t.Fatalf("expected runnerExitCode=0, got: %+v", a.RunnerExitCode)
		}
		if !a.Finish.OK || !a.OK {
			t.Fatalf("expected attempt ok, got: %+v", a)
		}
		// Runner IO artifacts should exist for post-mortems.
		if a.AttemptDir == "" {
			t.Fatalf("expected attemptDir in suite run JSON")
		}
		for _, p := range []string{
			filepath.Join(a.AttemptDir, "runner.command.txt"),
			filepath.Join(a.AttemptDir, "runner.stdout.log"),
			filepath.Join(a.AttemptDir, "runner.stderr.log"),
		} {
			if _, err := os.Stat(p); err != nil {
				t.Fatalf("expected runner artifact %s, got err=%v", p, err)
			}
		}
	}

	// Runner output should be visible (but streamed to stderr, keeping stdout JSON clean).
	if !strings.Contains(stderr.String(), "suite run: mission=") {
		t.Fatalf("expected suite run progress lines in stderr, got: %q", stderr.String())
	}
	if !strings.Contains(stderr.String(), "feedback: OK") {
		t.Fatalf("expected runner feedback output in stderr, got: %q", stderr.String())
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

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	r := Runner{
		Version: "0.0.0-dev",
		Now:     func() time.Time { return time.Date(2026, 2, 16, 12, 0, 0, 0, time.UTC) },
		Stdout:  &stdout,
		Stderr:  &stderr,
	}

	code := r.Run([]string{
		"suite", "run",
		"--file", suitePath,
		"--out-root", outRoot,
		"--json",
		"--",
		os.Args[0], "-test.run=TestHelperSuiteRunnerProcess$", "--", "case=no-feedback",
	})
	if code != 2 {
		t.Fatalf("expected exit code 2, got %d (stderr=%q)", code, stderr.String())
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
	if err := json.Unmarshal(stdout.Bytes(), &sum); err != nil {
		t.Fatalf("unmarshal suite run json: %v (stdout=%q)", err, stdout.String())
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

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	r := Runner{
		Version: "0.0.0-dev",
		Now:     func() time.Time { return time.Date(2026, 2, 16, 12, 0, 0, 0, time.UTC) },
		Stdout:  &stdout,
		Stderr:  &stderr,
	}

	code := r.Run([]string{
		"suite", "run",
		"--file", suitePath,
		"--out-root", outRoot,
		"--json",
		"--",
		os.Args[0], "-test.run=TestHelperSuiteRunnerProcess$", "--", "case=no-feedback",
	})
	if code != 2 {
		t.Fatalf("expected exit code 2, got %d (stderr=%q)", code, stderr.String())
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
	if err := json.Unmarshal(stdout.Bytes(), &sum); err != nil {
		t.Fatalf("unmarshal suite run json: %v (stdout=%q)", err, stdout.String())
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

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	r := Runner{
		Version: "0.0.0-dev",
		Now:     func() time.Time { return time.Date(2026, 2, 16, 12, 0, 0, 0, time.UTC) },
		Stdout:  &stdout,
		Stderr:  &stderr,
	}

	code := r.Run([]string{
		"suite", "run",
		"--file", suitePath,
		"--out-root", outRoot,
		"--blind", "on",
		"--json",
		"--",
		os.Args[0], "-test.run=TestHelperSuiteRunnerProcess$", "--", "case=ok",
	})
	if code != 2 {
		t.Fatalf("expected exit code 2, got %d (stderr=%q)", code, stderr.String())
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
	if err := json.Unmarshal(stdout.Bytes(), &sum); err != nil {
		t.Fatalf("unmarshal suite run json: %v (stdout=%q)", err, stdout.String())
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

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	r := Runner{
		Version: "0.0.0-dev",
		Now:     func() time.Time { return time.Date(2026, 2, 16, 12, 0, 0, 0, time.UTC) },
		Stdout:  &stdout,
		Stderr:  &stderr,
	}

	code := r.Run([]string{
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
		t.Fatalf("expected exit code 0, got %d (stderr=%q)", code, stderr.String())
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
	if err := json.Unmarshal(stdout.Bytes(), &sum); err != nil {
		t.Fatalf("unmarshal suite run json: %v (stdout=%q)", err, stdout.String())
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

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	r := Runner{
		Version: "0.0.0-dev",
		Now:     func() time.Time { return time.Date(2026, 2, 16, 12, 0, 0, 0, time.UTC) },
		Stdout:  &stdout,
		Stderr:  &stderr,
	}

	code := r.Run([]string{
		"suite", "run",
		"--file", suitePath,
		"--out-root", outRoot,
		"--json",
		"--",
		os.Args[0], "-test.run=TestHelperSuiteRunnerProcess$", "--", "case=ok",
	})
	if code != 2 {
		t.Fatalf("expected exit code 2, got %d (stderr=%q)", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "native runtime mode does not accept -- <runner-cmd> arguments") {
		t.Fatalf("expected native-capability guard error, got stderr=%q", stderr.String())
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

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	r := Runner{
		Version: "0.0.0-dev",
		Now:     func() time.Time { return time.Date(2026, 2, 16, 12, 0, 0, 0, time.UTC) },
		Stdout:  &stdout,
		Stderr:  &stderr,
	}

	code := r.Run([]string{
		"suite", "run",
		"--file", suitePath,
		"--out-root", outRoot,
		"--session-isolation", "process",
		"--json",
		"--",
		os.Args[0], "-test.run=TestHelperSuiteRunnerProcess$", "--", "case=ok",
	})
	if code != 0 {
		t.Fatalf("expected exit code 0, got %d (stderr=%q)", code, stderr.String())
	}

	var sum struct {
		OK                        bool   `json:"ok"`
		SessionIsolation          string `json:"sessionIsolation"`
		SessionIsolationRequested string `json:"sessionIsolationRequested"`
		HostNativeSpawnCapable    bool   `json:"hostNativeSpawnCapable"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &sum); err != nil {
		t.Fatalf("unmarshal suite run json: %v (stdout=%q)", err, stdout.String())
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

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	r := Runner{
		Version: "0.0.0-dev",
		Now:     func() time.Time { return time.Date(2026, 2, 16, 12, 0, 0, 0, time.UTC) },
		Stdout:  &stdout,
		Stderr:  &stderr,
	}

	code := r.Run([]string{
		"suite", "run",
		"--file", suitePath,
		"--out-root", outRoot,
		"--session-isolation", "native",
		"--json",
	})
	if code != 0 {
		t.Fatalf("expected exit code 0, got %d (stderr=%q)", code, stderr.String())
	}

	var sum struct {
		OK                      bool   `json:"ok"`
		RuntimeStrategySelected string `json:"runtimeStrategySelected"`
		Attempts                []struct {
			AttemptDir      string `json:"attemptDir"`
			RunnerErrorCode string `json:"runnerErrorCode"`
			OK              bool   `json:"ok"`
		} `json:"attempts"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &sum); err != nil {
		t.Fatalf("unmarshal suite run json: %v (stdout=%q)", err, stdout.String())
	}
	if !sum.OK || len(sum.Attempts) != 1 || !sum.Attempts[0].OK {
		t.Fatalf("unexpected summary: %+v", sum)
	}
	if sum.RuntimeStrategySelected != "codex_app_server" {
		t.Fatalf("unexpected runtime strategy selected: %+v", sum)
	}
	if sum.Attempts[0].RunnerErrorCode != "" {
		t.Fatalf("unexpected runner error: %+v", sum.Attempts[0])
	}
	attemptDir := sum.Attempts[0].AttemptDir
	if attemptDir == "" {
		t.Fatalf("expected attemptDir")
	}
	if _, err := os.Stat(filepath.Join(attemptDir, "feedback.json")); err != nil {
		t.Fatalf("expected feedback.json: %v", err)
	}
	if _, err := os.Stat(filepath.Join(attemptDir, "tool.calls.jsonl")); err != nil {
		t.Fatalf("expected tool.calls.jsonl: %v", err)
	}
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

func TestSuiteRun_NativeProcessParity(t *testing.T) {
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

	// Process path.
	t.Setenv("ZCL_WANT_SUITE_RUNNER", "1")
	var procStdout bytes.Buffer
	var procStderr bytes.Buffer
	rProc := Runner{
		Version: "0.0.0-dev",
		Now:     func() time.Time { return time.Date(2026, 2, 16, 12, 10, 0, 0, time.UTC) },
		Stdout:  &procStdout,
		Stderr:  &procStderr,
	}
	processOutRoot := filepath.Join(baseDir, "process")
	if code := rProc.Run([]string{
		"suite", "run",
		"--file", suitePath,
		"--out-root", processOutRoot,
		"--session-isolation", "process",
		"--json",
		"--",
		os.Args[0], "-test.run=TestHelperSuiteRunnerProcess$", "--", "case=ok",
	}); code != 0 {
		t.Fatalf("process suite run expected 0, got %d stderr=%q", code, procStderr.String())
	}

	// Native path.
	t.Setenv("ZCL_CODEX_APP_SERVER_CMD", os.Args[0]+" -test.run=TestHelperSuiteNativeAppServer$")
	t.Setenv("ZCL_HELPER_PROCESS", "1")
	t.Setenv("ZCL_HELPER_MODE", "smoke")
	var nativeStdout bytes.Buffer
	var nativeStderr bytes.Buffer
	rNative := Runner{
		Version: "0.0.0-dev",
		Now:     func() time.Time { return time.Date(2026, 2, 16, 12, 11, 0, 0, time.UTC) },
		Stdout:  &nativeStdout,
		Stderr:  &nativeStderr,
	}
	nativeOutRoot := filepath.Join(baseDir, "native")
	if code := rNative.Run([]string{
		"suite", "run",
		"--file", suitePath,
		"--out-root", nativeOutRoot,
		"--session-isolation", "native",
		"--json",
	}); code != 0 {
		t.Fatalf("native suite run expected 0, got %d stderr=%q", code, nativeStderr.String())
	}

	var procSum struct {
		OK       bool `json:"ok"`
		Attempts []struct {
			AttemptDir string `json:"attemptDir"`
		} `json:"attempts"`
	}
	if err := json.Unmarshal(procStdout.Bytes(), &procSum); err != nil {
		t.Fatalf("unmarshal process summary: %v", err)
	}
	var nativeSum struct {
		OK       bool `json:"ok"`
		Attempts []struct {
			AttemptDir string `json:"attemptDir"`
		} `json:"attempts"`
	}
	if err := json.Unmarshal(nativeStdout.Bytes(), &nativeSum); err != nil {
		t.Fatalf("unmarshal native summary: %v", err)
	}
	if !procSum.OK || !nativeSum.OK || len(procSum.Attempts) != 1 || len(nativeSum.Attempts) != 1 {
		t.Fatalf("unexpected summaries process=%+v native=%+v", procSum, nativeSum)
	}

	readFeedbackOK := func(path string) bool {
		raw, err := os.ReadFile(filepath.Join(path, "feedback.json"))
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
	if !readFeedbackOK(procSum.Attempts[0].AttemptDir) || !readFeedbackOK(nativeSum.Attempts[0].AttemptDir) {
		t.Fatalf("expected process/native parity on feedback.ok")
	}

	readReport := func(path string) (ok bool, toolCalls int64) {
		raw, err := os.ReadFile(filepath.Join(path, "attempt.report.json"))
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
		return *payload.OK, payload.Metrics.ToolCallsTotal
	}
	procOK, procCalls := readReport(procSum.Attempts[0].AttemptDir)
	nativeOK, nativeCalls := readReport(nativeSum.Attempts[0].AttemptDir)
	if !procOK || !nativeOK {
		t.Fatalf("expected both reports ok=true, process=%v native=%v", procOK, nativeOK)
	}
	if procCalls == 0 || nativeCalls == 0 {
		t.Fatalf("expected non-zero tool calls in both reports, process=%d native=%d", procCalls, nativeCalls)
	}
}

func TestSuiteRun_NativeParallelUniqueSessions(t *testing.T) {
	outRoot := t.TempDir()
	suitePath := filepath.Join(t.TempDir(), "suite.json")
	missions := make([]map[string]any, 0, 20)
	for i := 1; i <= 20; i++ {
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
	rawSuite, _ := json.Marshal(suiteObj)
	writeSuiteFile(t, suitePath, string(rawSuite))

	t.Setenv("ZCL_CODEX_APP_SERVER_CMD", os.Args[0]+" -test.run=TestHelperSuiteNativeAppServer$")
	t.Setenv("ZCL_HELPER_PROCESS", "1")
	t.Setenv("ZCL_HELPER_MODE", "smoke")

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	r := Runner{
		Version: "0.0.0-dev",
		Now:     func() time.Time { return time.Date(2026, 2, 16, 12, 20, 0, 0, time.UTC) },
		Stdout:  &stdout,
		Stderr:  &stderr,
	}

	code := r.Run([]string{
		"suite", "run",
		"--file", suitePath,
		"--out-root", outRoot,
		"--session-isolation", "native",
		"--parallel", "20",
		"--total", "20",
		"--json",
	})
	if code != 0 {
		t.Fatalf("expected exit code 0, got %d stderr=%q", code, stderr.String())
	}

	var sum struct {
		OK       bool `json:"ok"`
		Attempts []struct {
			AttemptDir string `json:"attemptDir"`
			OK         bool   `json:"ok"`
		} `json:"attempts"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &sum); err != nil {
		t.Fatalf("unmarshal suite run json: %v", err)
	}
	if !sum.OK || len(sum.Attempts) != 20 {
		t.Fatalf("unexpected summary: %+v", sum)
	}

	seenSessions := map[string]bool{}
	for _, attempt := range sum.Attempts {
		if !attempt.OK {
			t.Fatalf("expected attempt ok=true: %+v", attempt)
		}
		refRaw, err := os.ReadFile(filepath.Join(attempt.AttemptDir, "runner.ref.json"))
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
		if seenSessions[ref.SessionID] {
			t.Fatalf("duplicate sessionId detected: %s", ref.SessionID)
		}
		seenSessions[ref.SessionID] = true
	}
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

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	r := Runner{
		Version: "0.0.0-dev",
		Now:     func() time.Time { return time.Date(2026, 2, 16, 12, 25, 0, 0, time.UTC) },
		Stdout:  &stdout,
		Stderr:  &stderr,
	}
	code := r.Run([]string{
		"suite", "run",
		"--file", suitePath,
		"--out-root", outRoot,
		"--session-isolation", "native",
		"--parallel", "2",
		"--fail-fast=false",
		"--json",
	})
	if code == 1 {
		t.Fatalf("expected no harness failure, got code=%d stderr=%q", code, stderr.String())
	}

	var sum struct {
		Attempts []struct {
			MissionID       string `json:"missionId"`
			RunnerErrorCode string `json:"runnerErrorCode"`
			OK              bool   `json:"ok"`
		} `json:"attempts"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &sum); err != nil {
		t.Fatalf("unmarshal suite run json: %v", err)
	}
	if len(sum.Attempts) != 2 {
		t.Fatalf("expected 2 attempts, got %+v", sum.Attempts)
	}
	var sawTimeout bool
	var sawSuccess bool
	for _, a := range sum.Attempts {
		if a.RunnerErrorCode == "ZCL_E_TIMEOUT" {
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

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	r := Runner{
		Version: "0.0.0-dev",
		Now:     time.Now,
		Stdout:  &stdout,
		Stderr:  &stderr,
	}

	code := r.Run([]string{
		"suite", "run",
		"--file", suitePath,
		"--out-root", outRoot,
		"--session-isolation", "native",
		"--json",
	})
	if code == 1 {
		t.Fatalf("expected non-harness failure path, got code=%d stderr=%q", code, stderr.String())
	}

	var sum struct {
		Attempts []struct {
			RunnerErrorCode  string `json:"runnerErrorCode"`
			AutoFeedbackCode string `json:"autoFeedbackCode"`
		} `json:"attempts"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &sum); err != nil {
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

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	r := Runner{
		Version: "0.0.0-dev",
		Now:     time.Now,
		Stdout:  &stdout,
		Stderr:  &stderr,
	}

	code := r.Run([]string{
		"suite", "run",
		"--file", suitePath,
		"--out-root", outRoot,
		"--session-isolation", "native",
		"--json",
	})
	if code == 1 {
		t.Fatalf("expected non-harness failure path, got code=%d stderr=%q", code, stderr.String())
	}

	var sum struct {
		Attempts []struct {
			RunnerErrorCode  string `json:"runnerErrorCode"`
			AutoFeedbackCode string `json:"autoFeedbackCode"`
		} `json:"attempts"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &sum); err != nil {
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

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	r := Runner{
		Version: "0.0.0-dev",
		Now:     time.Now,
		Stdout:  &stdout,
		Stderr:  &stderr,
	}

	code := r.Run([]string{
		"suite", "run",
		"--file", suitePath,
		"--out-root", outRoot,
		"--session-isolation", "native",
		"--json",
	})
	if code == 1 {
		t.Fatalf("expected non-harness failure path, got code=%d stderr=%q", code, stderr.String())
	}

	var sum struct {
		Attempts []struct {
			RunnerErrorCode string `json:"runnerErrorCode"`
		} `json:"attempts"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &sum); err != nil {
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

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	r := Runner{
		Version: "0.0.0-dev",
		Now:     time.Now,
		Stdout:  &stdout,
		Stderr:  &stderr,
	}

	start := time.Now()
	code := r.Run([]string{
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
		t.Fatalf("expected success, got code=%d stderr=%q", code, stderr.String())
	}
	if elapsed < 400*time.Millisecond {
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

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	r := Runner{
		Version: "0.0.0-dev",
		Now:     func() time.Time { return time.Date(2026, 2, 16, 12, 0, 0, 0, time.UTC) },
		Stdout:  &stdout,
		Stderr:  &stderr,
	}

	code := r.Run([]string{
		"suite", "run",
		"--file", suitePath,
		"--out-root", outRoot,
		"--json",
		"--",
		os.Args[0], "-test.run=TestHelperSuiteRunnerProcess$", "--", "case=sleep",
	})
	if code != 1 {
		t.Fatalf("expected exit code 1 for timeout harness error, got %d (stderr=%q)", code, stderr.String())
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
	if err := json.Unmarshal(stdout.Bytes(), &sum); err != nil {
		t.Fatalf("unmarshal suite run json: %v (stdout=%q)", err, stdout.String())
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

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	r := Runner{
		Version: "0.0.0-dev",
		Now:     func() time.Time { return time.Date(2026, 2, 16, 12, 0, 0, 0, time.UTC) },
		Stdout:  &stdout,
		Stderr:  &stderr,
	}

	code := r.Run([]string{
		"suite", "run",
		"--file", suitePath,
		"--out-root", outRoot,
		"--feedback-policy", "strict",
		"--json",
		"--",
		os.Args[0], "-test.run=TestHelperSuiteRunnerProcess$", "--", "case=no-feedback",
	})
	if code != 2 {
		t.Fatalf("expected exit code 2, got %d (stderr=%q)", code, stderr.String())
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
	if err := json.Unmarshal(stdout.Bytes(), &sum); err != nil {
		t.Fatalf("unmarshal suite run json: %v (stdout=%q)", err, stdout.String())
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

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	r := Runner{
		Version: "0.0.0-dev",
		Now:     func() time.Time { return time.Date(2026, 2, 16, 12, 0, 0, 0, time.UTC) },
		Stdout:  &stdout,
		Stderr:  &stderr,
	}

	code := r.Run([]string{
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
		t.Fatalf("expected exit code 0, got %d (stderr=%q)", code, stderr.String())
	}

	var sum struct {
		RunID             string `json:"runId"`
		CampaignID        string `json:"campaignId"`
		CampaignStatePath string `json:"campaignStatePath"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &sum); err != nil {
		t.Fatalf("unmarshal suite run json: %v (stdout=%q)", err, stdout.String())
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

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	r := Runner{
		Version: "0.0.0-dev",
		Now:     func() time.Time { return time.Date(2026, 2, 22, 20, 0, 0, 0, time.UTC) },
		Stdout:  &stdout,
		Stderr:  &stderr,
	}

	code := r.Run([]string{
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
		t.Fatalf("expected exit code 0, got %d (stderr=%q)", code, stderr.String())
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
	if err := json.Unmarshal(stdout.Bytes(), &sum); err != nil {
		t.Fatalf("unmarshal suite run json: %v (stdout=%q)", err, stdout.String())
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

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	r := Runner{
		Version: "0.0.0-dev",
		Now:     func() time.Time { return time.Date(2026, 2, 22, 20, 5, 0, 0, time.UTC) },
		Stdout:  &stdout,
		Stderr:  &stderr,
	}

	code := r.Run([]string{
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
		t.Fatalf("expected exit code 0, got %d (stderr=%q)", code, stderr.String())
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
	if err := json.Unmarshal(stdout.Bytes(), &sum); err != nil {
		t.Fatalf("unmarshal suite run json: %v (stdout=%q)", err, stdout.String())
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

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	r := Runner{
		Version: "0.0.0-dev",
		Now:     func() time.Time { return time.Date(2026, 2, 22, 20, 10, 0, 0, time.UTC) },
		Stdout:  &stdout,
		Stderr:  &stderr,
	}

	code := r.Run([]string{
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
		t.Fatalf("expected exit code 2, got %d (stderr=%q)", code, stderr.String())
	}
	var sum struct {
		Attempts []struct {
			AutoFeedbackCode string `json:"autoFeedbackCode"`
		} `json:"attempts"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &sum); err != nil {
		t.Fatalf("unmarshal suite run json: %v (stdout=%q)", err, stdout.String())
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

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	r := Runner{
		Version: "0.0.0-dev",
		Now:     func() time.Time { return time.Date(2026, 2, 22, 20, 20, 0, 0, time.UTC) },
		Stdout:  &stdout,
		Stderr:  &stderr,
	}

	code := r.Run([]string{
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
		t.Fatalf("expected exit code 0, got %d (stderr=%q)", code, stderr.String())
	}
	var sum struct {
		Attempts []struct {
			AttemptDir string `json:"attemptDir"`
		} `json:"attempts"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &sum); err != nil {
		t.Fatalf("unmarshal suite run json: %v (stdout=%q)", err, stdout.String())
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

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	r := Runner{
		Version: "0.0.0-dev",
		Now:     func() time.Time { return time.Date(2026, 2, 22, 20, 30, 0, 0, time.UTC) },
		Stdout:  &stdout,
		Stderr:  &stderr,
	}

	code := r.Run([]string{
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
		t.Fatalf("expected exit code 2, got %d (stderr=%q)", code, stderr.String())
	}
	var sum struct {
		Attempts []struct {
			AutoFeedbackCode string `json:"autoFeedbackCode"`
		} `json:"attempts"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &sum); err != nil {
		t.Fatalf("unmarshal suite run json: %v (stdout=%q)", err, stdout.String())
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

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	r := Runner{
		Version: "0.0.0-dev",
		Now:     func() time.Time { return time.Date(2026, 2, 22, 20, 35, 0, 0, time.UTC) },
		Stdout:  &stdout,
		Stderr:  &stderr,
	}

	code := r.Run([]string{
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
		t.Fatalf("expected exit code 0, got %d (stderr=%q)", code, stderr.String())
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
	if err := json.Unmarshal(stdout.Bytes(), &sum); err != nil {
		t.Fatalf("unmarshal suite run json: %v (stdout=%q)", err, stdout.String())
	}
	if !sum.OK || len(sum.Attempts) != 1 || !sum.Attempts[0].Finish.OK || sum.Attempts[0].AutoFeedbackCode != "" {
		t.Fatalf("unexpected summary: %+v", sum)
	}
}

func TestHelperSuiteRunnerProcess(t *testing.T) {
	if os.Getenv("ZCL_WANT_SUITE_RUNNER") != "1" {
		return
	}

	// Find args after "--".
	args := os.Args
	idx := 0
	for i := range args {
		if args[i] == "--" {
			idx = i + 1
			break
		}
	}
	kind := "ok"
	exit := 0
	for _, a := range args[idx:] {
		if strings.HasPrefix(a, "case=") {
			kind = strings.TrimPrefix(a, "case=")
		} else if strings.HasPrefix(a, "exit=") {
			n, _ := strconv.Atoi(strings.TrimPrefix(a, "exit="))
			exit = n
		}
	}

	r := Runner{
		Version: "0.0.0-dev",
		Now:     func() time.Time { return time.Date(2026, 2, 16, 12, 0, 0, 0, time.UTC) },
		Stdout:  os.Stdout,
		Stderr:  os.Stderr,
	}

	switch kind {
	case "ok":
		if code := r.Run([]string{"run", "--", "echo", "hi"}); code != 0 {
			os.Exit(101)
		}
		if code := r.Run([]string{"feedback", "--ok", "--result", "ok"}); code != 0 {
			os.Exit(102)
		}
		os.Exit(exit)
	case "no-feedback":
		_ = r.Run([]string{"run", "--", "echo", "hi"})
		os.Exit(exit)
	case "result-file-ok":
		_ = r.Run([]string{"run", "--", "echo", "hi"})
		path := strings.TrimSpace(os.Getenv("ZCL_MISSION_RESULT_PATH"))
		if path == "" {
			os.Exit(104)
		}
		if err := os.WriteFile(path, []byte(`{"ok":true,"resultJson":{"proof":"file-channel-ok"}}`), 0o644); err != nil {
			os.Exit(105)
		}
		os.Exit(exit)
	case "result-file-no-trace-ok":
		path := strings.TrimSpace(os.Getenv("ZCL_MISSION_RESULT_PATH"))
		if path == "" {
			os.Exit(106)
		}
		if err := os.WriteFile(path, []byte(`{"ok":true,"resultJson":{"proof":"file-channel-no-trace"}}`), 0o644); err != nil {
			os.Exit(107)
		}
		os.Exit(exit)
	case "result-file-invalid":
		_ = r.Run([]string{"run", "--", "echo", "hi"})
		path := strings.TrimSpace(os.Getenv("ZCL_MISSION_RESULT_PATH"))
		if path == "" {
			os.Exit(108)
		}
		if err := os.WriteFile(path, []byte(`{"ok":`), 0o644); err != nil {
			os.Exit(109)
		}
		os.Exit(exit)
	case "result-file-turn-2":
		_ = r.Run([]string{"run", "--", "echo", "hi"})
		path := strings.TrimSpace(os.Getenv("ZCL_MISSION_RESULT_PATH"))
		if path == "" {
			os.Exit(110)
		}
		if err := os.WriteFile(path, []byte(`{"ok":true,"turn":2,"resultJson":{"proof":"turn-2"}}`), 0o644); err != nil {
			os.Exit(111)
		}
		os.Exit(exit)
	case "result-file-turn-3":
		_ = r.Run([]string{"run", "--", "echo", "hi"})
		path := strings.TrimSpace(os.Getenv("ZCL_MISSION_RESULT_PATH"))
		if path == "" {
			os.Exit(112)
		}
		if err := os.WriteFile(path, []byte(`{"ok":true,"turn":3,"resultJson":{"proof":"turn-3"}}`), 0o644); err != nil {
			os.Exit(113)
		}
		os.Exit(exit)
	case "result-stdout-ok":
		_ = r.Run([]string{"run", "--", "echo", "hi"})
		marker := strings.TrimSpace(os.Getenv("ZCL_MISSION_RESULT_MARKER"))
		if marker == "" {
			marker = "ZCL_RESULT_JSON:"
		}
		_, _ = os.Stdout.WriteString(marker + `{"ok":true,"resultJson":{"proof":"stdout-channel-ok"}}` + "\n")
		os.Exit(exit)
	case "sleep":
		time.Sleep(3 * time.Second)
		os.Exit(exit)
	default:
		os.Exit(103)
	}
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
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)

	var mu sync.Mutex
	writeJSON := func(v any) {
		b, _ := json.Marshal(v)
		mu.Lock()
		defer mu.Unlock()
		_, _ = os.Stdout.Write(append(b, '\n'))
	}

	threadID := "thr_native_1"
	turnID := "turn_native_1"

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var msg map[string]any
		if err := json.Unmarshal([]byte(line), &msg); err != nil {
			continue
		}
		method, _ := msg["method"].(string)
		id, hasID := msg["id"]
		if !hasID {
			continue
		}

		respond := func(result any) {
			writeJSON(map[string]any{"id": id, "result": result})
		}
		respondErr := func(code int, message string) {
			writeJSON(map[string]any{"id": id, "error": map[string]any{"code": code, "message": message}})
		}

		switch method {
		case "initialize":
			respond(map[string]any{"userAgent": "codex-cli/1.4.0"})
		case "model/list":
			switch mode {
			case "compat_missing_method":
				respondErr(-32601, "method not found")
			case "timeout_on_model_list":
				time.Sleep(2 * time.Second)
			default:
				respond(map[string]any{"data": []any{}})
			}
		case "thread/start":
			respond(map[string]any{"thread": map[string]any{"id": threadID}})
			writeJSON(map[string]any{"method": "thread/started", "params": map[string]any{"threadId": threadID}})
		case "thread/resume":
			respond(map[string]any{"thread": map[string]any{"id": threadID}})
		case "turn/start":
			slow := false
			if params, ok := msg["params"].(map[string]any); ok {
				if input, ok := params["input"].([]any); ok {
					for _, item := range input {
						obj, _ := item.(map[string]any)
						text, _ := obj["text"].(string)
						if strings.Contains(strings.ToLower(text), "slow") {
							slow = true
							break
						}
					}
				}
			}
			respond(map[string]any{"turn": map[string]any{"id": turnID, "status": "inProgress", "items": []any{}}})
			writeJSON(map[string]any{"method": "turn/started", "params": map[string]any{"threadId": threadID, "turnId": turnID}})
			switch mode {
			case "rate_limit_failure":
				writeJSON(map[string]any{
					"method": "turn/failed",
					"params": map[string]any{
						"threadId": threadID,
						"turnId":   turnID,
						"turn": map[string]any{
							"id":     turnID,
							"status": "failed",
							"error": map[string]any{
								"message":        "usage limit exceeded",
								"codexErrorInfo": "UsageLimitExceeded",
							},
						},
					},
				})
			case "auth_failure":
				writeJSON(map[string]any{
					"method": "turn/failed",
					"params": map[string]any{
						"threadId": threadID,
						"turnId":   turnID,
						"turn": map[string]any{
							"id":     turnID,
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
			case "crash_during_turn":
				os.Exit(137)
			case "disconnect_during_turn":
				return
			default:
				if slow {
					time.Sleep(900 * time.Millisecond)
				}
				writeJSON(map[string]any{"method": "item/agentMessage/delta", "params": map[string]any{"threadId": threadID, "turnId": turnID, "itemId": "itm_native_1", "delta": "native-result"}})
				writeJSON(map[string]any{"method": "turn/completed", "params": map[string]any{"threadId": threadID, "turnId": turnID}})
			}
		case "turn/steer":
			respond(map[string]any{"turnId": turnID})
		case "turn/interrupt":
			respond(map[string]any{})
		default:
			respondErr(-32601, fmt.Sprintf("method not found: %s", method))
		}
	}
}

func writeSuiteFile(t *testing.T, path string, contents string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatalf("write suite file: %v", err)
	}
}
