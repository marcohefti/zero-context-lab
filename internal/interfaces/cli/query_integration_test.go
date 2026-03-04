package cli

import (
	"bytes"
	"encoding/json"
	"testing"
	"time"
)

func TestAttemptListLatestAndRunsList(t *testing.T) {
	outRoot := t.TempDir()
	r := Runner{
		Version: "0.0.0-dev",
		Now:     func() time.Time { return time.Date(2026, 2, 16, 12, 0, 0, 0, time.UTC) },
	}

	start1 := startAttemptForQuery(t, r, outRoot, "", "q-suite", "m-one")
	runAndFeedbackForQuery(t, r, start1.Env, true)

	start2 := startAttemptForQuery(t, r, outRoot, start1.RunID, "q-suite", "m-one")
	runAndFeedbackForQuery(t, r, start2.Env, false)

	start3 := startAttemptForQuery(t, r, outRoot, start1.RunID, "q-suite", "m-two")
	runOnlyForQuery(t, r, start3.Env)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	r.Stdout = &stdout
	r.Stderr = &stderr

	code := r.Run([]string{
		"attempt", "list",
		"--out-root", outRoot,
		"--suite", "q-suite",
		"--mission", "m-one",
		"--status", "fail",
		"--json",
	})
	if code != 0 {
		t.Fatalf("attempt list failed: code=%d stderr=%q", code, stderr.String())
	}
	var listed struct {
		Returned int `json:"returned"`
		Attempts []struct {
			AttemptID string `json:"attemptId"`
			Status    string `json:"status"`
		} `json:"attempts"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &listed); err != nil {
		t.Fatalf("unmarshal attempt list: %v", err)
	}
	if listed.Returned != 1 || len(listed.Attempts) != 1 || listed.Attempts[0].Status != "fail" {
		t.Fatalf("unexpected attempt list result: %+v", listed)
	}

	stdout.Reset()
	stderr.Reset()
	code = r.Run([]string{
		"attempt", "latest",
		"--out-root", outRoot,
		"--suite", "q-suite",
		"--mission", "m-two",
		"--status", "missing_feedback",
		"--json",
	})
	if code != 0 {
		t.Fatalf("attempt latest failed: code=%d stderr=%q", code, stderr.String())
	}
	var latest struct {
		Found   bool `json:"found"`
		Attempt struct {
			AttemptID       string `json:"attemptId"`
			Status          string `json:"status"`
			FeedbackPresent bool   `json:"feedbackPresent"`
		} `json:"attempt"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &latest); err != nil {
		t.Fatalf("unmarshal attempt latest: %v", err)
	}
	if !latest.Found || latest.Attempt.AttemptID == "" || latest.Attempt.Status != "missing_feedback" || latest.Attempt.FeedbackPresent {
		t.Fatalf("unexpected attempt latest result: %+v", latest)
	}

	stdout.Reset()
	stderr.Reset()
	code = r.Run([]string{
		"runs", "list",
		"--out-root", outRoot,
		"--suite", "q-suite",
		"--json",
	})
	if code != 0 {
		t.Fatalf("runs list failed: code=%d stderr=%q", code, stderr.String())
	}
	var runs struct {
		Returned int `json:"returned"`
		Runs     []struct {
			RunID                string `json:"runId"`
			Status               string `json:"status"`
			AttemptsTotal        int    `json:"attemptsTotal"`
			OKTotal              int    `json:"okTotal"`
			FailTotal            int    `json:"failTotal"`
			MissingFeedbackTotal int    `json:"missingFeedbackTotal"`
		} `json:"runs"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &runs); err != nil {
		t.Fatalf("unmarshal runs list: %v", err)
	}
	if runs.Returned != 1 || len(runs.Runs) != 1 {
		t.Fatalf("expected one run row, got %+v", runs)
	}
	row := runs.Runs[0]
	if row.RunID != start1.RunID || row.AttemptsTotal != 3 || row.OKTotal != 1 || row.FailTotal != 1 || row.MissingFeedbackTotal != 1 || row.Status != "missing_feedback" {
		t.Fatalf("unexpected runs row: %+v", row)
	}
}

type queryStart struct {
	RunID string            `json:"runId"`
	Env   map[string]string `json:"env"`
}

func startAttemptForQuery(t *testing.T, r Runner, outRoot string, runID string, suiteID string, missionID string) queryStart {
	t.Helper()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	r.Stdout = &stdout
	r.Stderr = &stderr
	args := []string{
		"attempt", "start",
		"--out-root", outRoot,
		"--suite", suiteID,
		"--mission", missionID,
		"--json",
	}
	if runID != "" {
		args = append(args, "--run-id", runID)
	}
	code := r.Run(args)
	if code != 0 {
		t.Fatalf("attempt start failed: code=%d stderr=%q", code, stderr.String())
	}
	var out queryStart
	if err := json.Unmarshal(stdout.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal attempt start: %v", err)
	}
	if out.RunID == "" || len(out.Env) == 0 {
		t.Fatalf("unexpected attempt start payload: %s", stdout.String())
	}
	return out
}

func setAttemptEnvForQuery(t *testing.T, env map[string]string) {
	t.Helper()
	keys := []string{
		"ZCL_RUN_ID",
		"ZCL_SUITE_ID",
		"ZCL_MISSION_ID",
		"ZCL_ATTEMPT_ID",
		"ZCL_OUT_DIR",
		"ZCL_TMP_DIR",
		"ZCL_AGENT_ID",
		"ZCL_ISOLATION_MODEL",
		"ZCL_PROMPT_PATH",
	}
	for _, k := range keys {
		t.Setenv(k, "")
	}
	for k, v := range env {
		t.Setenv(k, v)
	}
}

func runOnlyForQuery(t *testing.T, r Runner, env map[string]string) {
	t.Helper()
	setAttemptEnvForQuery(t, env)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	r.Stdout = &stdout
	r.Stderr = &stderr
	if code := r.Run([]string{"run", "--", "echo", "hi"}); code != 0 {
		t.Fatalf("zcl run failed: code=%d stderr=%q", code, stderr.String())
	}
}

func runAndFeedbackForQuery(t *testing.T, r Runner, env map[string]string, ok bool) {
	t.Helper()
	runOnlyForQuery(t, r, env)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	r.Stdout = &stdout
	r.Stderr = &stderr
	var args []string
	if ok {
		args = []string{"feedback", "--ok", "--result", "done"}
	} else {
		args = []string{"feedback", "--fail", "--result", "done"}
	}
	if code := r.Run(args); code != 0 {
		t.Fatalf("zcl feedback failed: code=%d stderr=%q", code, stderr.String())
	}
}

func TestFeedbackHintForMissingTrace(t *testing.T) {
	outRoot := t.TempDir()
	r := Runner{
		Version: "0.0.0-dev",
		Now:     func() time.Time { return time.Date(2026, 2, 16, 12, 0, 0, 0, time.UTC) },
	}
	start := startAttemptForQuery(t, r, outRoot, "", "hint-suite", "hint-mission")
	setAttemptEnvForQuery(t, start.Env)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	r.Stdout = &stdout
	r.Stderr = &stderr
	if code := r.Run([]string{"feedback", "--ok", "--result", "x"}); code != 2 {
		t.Fatalf("expected feedback usage failure, got %d", code)
	}
	if !bytes.Contains(stderr.Bytes(), []byte("hint: record at least one funnel action first")) {
		t.Fatalf("expected actionable feedback hint, got stderr=%q", stderr.String())
	}
}

func TestVersionAliasCLI(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	r := Runner{
		Version: "v0.test",
		Now:     func() time.Time { return time.Date(2026, 2, 16, 12, 0, 0, 0, time.UTC) },
		Stdout:  &stdout,
		Stderr:  &stderr,
	}
	if code := r.Run([]string{"--version"}); code != 0 {
		t.Fatalf("expected --version exit 0, got %d stderr=%q", code, stderr.String())
	}
	if stdout.String() != "v0.test\n" {
		t.Fatalf("unexpected --version output: %q", stdout.String())
	}
}

func TestRunsListStatusFilter(t *testing.T) {
	outRoot := t.TempDir()
	r := Runner{
		Version: "0.0.0-dev",
		Now:     func() time.Time { return time.Date(2026, 2, 16, 12, 0, 0, 0, time.UTC) },
	}
	start := startAttemptForQuery(t, r, outRoot, "", "status-suite", "m")
	runAndFeedbackForQuery(t, r, start.Env, false)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	r.Stdout = &stdout
	r.Stderr = &stderr
	code := r.Run([]string{
		"runs", "list",
		"--out-root", outRoot,
		"--status", "fail",
		"--json",
	})
	if code != 0 {
		t.Fatalf("runs list status filter failed: code=%d stderr=%q", code, stderr.String())
	}
	var out struct {
		Returned int `json:"returned"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal runs list: %v", err)
	}
	if out.Returned != 1 {
		t.Fatalf("expected one failing run row, got %+v", out)
	}
}
