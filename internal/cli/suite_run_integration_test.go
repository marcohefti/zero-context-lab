package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
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
			Finish struct {
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
	if sum.Attempts[0].Finish.ReportError == nil || sum.Attempts[0].Finish.ReportError.Code != "ZCL_E_MISSING_ARTIFACT" {
		t.Fatalf("expected reportError ZCL_E_MISSING_ARTIFACT, got: %+v", sum.Attempts[0].Finish.ReportError)
	}
	foundMissingFeedback := false
	for _, e := range sum.Attempts[0].Finish.Validate.Errors {
		if e.Code == "ZCL_E_MISSING_ARTIFACT" {
			foundMissingFeedback = true
			break
		}
	}
	if !foundMissingFeedback {
		t.Fatalf("expected validate to include ZCL_E_MISSING_ARTIFACT, got: %+v", sum.Attempts[0].Finish.Validate.Errors)
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
	default:
		os.Exit(103)
	}
}

func writeSuiteFile(t *testing.T, path string, contents string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatalf("write suite file: %v", err)
	}
}
