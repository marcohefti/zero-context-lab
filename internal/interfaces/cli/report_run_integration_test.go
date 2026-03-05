package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/marcohefti/zero-context-lab/internal/contexts/execution/app/attempt"
)

func TestReport_RunJSONAggregates(t *testing.T) {
	outRoot := t.TempDir()
	now := time.Date(2026, 2, 18, 9, 0, 0, 0, time.UTC)

	a1, a2 := startReportRunAttempts(t, outRoot, now)

	t.Setenv(helperProcessEnv, "1")

	r := Runner{
		Version: "0.0.0-dev",
		Now:     func() time.Time { return now.Add(5 * time.Second) },
		Stdout:  ioDiscard{},
		Stderr:  ioDiscard{},
	}
	runAndFeedbackReportAttempt(t, &r, a1.Env, helperProcessConfig{Stdout: "ok\n", Exit: 0}, 0, []string{"feedback", "--ok", "--result", "ok"}, "a1")
	runAndFeedbackReportAttempt(t, &r, a2.Env, helperProcessConfig{Stderr: "bad\n", Exit: 1}, 1, []string{"feedback", "--fail", "--result", "bad", "--decision-tag", "blocked"}, "a2")

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	r2 := Runner{
		Version: "0.0.0-dev",
		Now:     func() time.Time { return now.Add(10 * time.Second) },
		Stdout:  &stdout,
		Stderr:  &stderr,
	}
	runDir := filepath.Join(outRoot, "runs", a1.RunID)
	out := runReportJSONForRunDir(t, r2, runDir, &stdout, &stderr)
	assertRunReportAggregates(t, out, a1.RunID)
}

func startReportRunAttempts(t *testing.T, outRoot string, now time.Time) (*attempt.StartResult, *attempt.StartResult) {
	t.Helper()
	a1, err := attempt.Start(now, attempt.StartOpts{
		OutRoot:   outRoot,
		SuiteID:   "suite",
		MissionID: "m1",
		Mode:      "discovery",
		Retry:     1,
	})
	if err != nil {
		t.Fatalf("attempt.Start a1: %v", err)
	}
	a2, err := attempt.Start(now, attempt.StartOpts{
		OutRoot:   outRoot,
		RunID:     a1.RunID,
		SuiteID:   "suite",
		MissionID: "m2",
		Mode:      "discovery",
		Retry:     1,
	})
	if err != nil {
		t.Fatalf("attempt.Start a2: %v", err)
	}
	return a1, a2
}

func runAndFeedbackReportAttempt(t *testing.T, r *Runner, env map[string]string, cfg helperProcessConfig, expectedRunExit int, feedbackArgs []string, label string) {
	t.Helper()
	setEnvFromMap(t, env)
	if code := r.Run(helperRunCommand(t, cfg)); code != expectedRunExit {
		t.Fatalf("run %s expected exit %d, got %d", label, expectedRunExit, code)
	}
	if code := r.Run(feedbackArgs); code != 0 {
		t.Fatalf("feedback %s: code=%d", label, code)
	}
}

type reportRunOut struct {
	OK        bool       `json:"ok"`
	Target    string     `json:"target"`
	RunID     string     `json:"runId"`
	Attempts  []struct{} `json:"attempts"`
	Aggregate struct {
		AttemptsTotal        int              `json:"attemptsTotal"`
		Passed               int              `json:"passed"`
		Failed               int              `json:"failed"`
		FailureCodeHistogram map[string]int64 `json:"failureCodeHistogram"`
		Task                 struct {
			Passed  int `json:"passed"`
			Failed  int `json:"failed"`
			Unknown int `json:"unknown"`
		} `json:"task"`
		Evidence struct {
			Complete   int `json:"complete"`
			Incomplete int `json:"incomplete"`
		} `json:"evidence"`
		Orchestration struct {
			Healthy            int              `json:"healthy"`
			InfraFailed        int              `json:"infraFailed"`
			InfraFailureByCode map[string]int64 `json:"infraFailureByCode"`
		} `json:"orchestration"`
		TokenEstimates *struct {
			TotalTokens *int64 `json:"totalTokens"`
		} `json:"tokenEstimates"`
	} `json:"aggregate"`
}

func runReportJSONForRunDir(t *testing.T, r Runner, runDir string, stdout *bytes.Buffer, stderr *bytes.Buffer) reportRunOut {
	t.Helper()
	if code := r.Run([]string{"report", "--json", runDir}); code != 0 {
		t.Fatalf("report runDir: code=%d stderr=%q", code, stderr.String())
	}
	if _, err := os.Stat(filepath.Join(runDir, "run.report.json")); err != nil {
		t.Fatalf("expected run.report.json to be written: %v", err)
	}
	var out reportRunOut
	if err := json.Unmarshal(stdout.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal run report json: %v (stdout=%q)", err, stdout.String())
	}
	return out
}

func assertRunReportAggregates(t *testing.T, out reportRunOut, runID string) {
	t.Helper()
	assertRunReportHeaderAndCounts(t, out, runID)
	assertRunReportAxes(t, out)
}

func assertRunReportHeaderAndCounts(t *testing.T, out reportRunOut, runID string) {
	t.Helper()
	if out.Target != "run" || out.RunID != runID {
		t.Fatalf("unexpected run report header: %+v", out)
	}
	if out.Aggregate.AttemptsTotal != 2 || out.Aggregate.Passed != 1 || out.Aggregate.Failed != 1 || len(out.Attempts) != 2 {
		t.Fatalf("unexpected aggregate counts: %+v", out.Aggregate)
	}
	if out.Aggregate.FailureCodeHistogram["ZCL_E_TOOL_FAILED"] == 0 {
		t.Fatalf("expected typed tool failure code histogram, got: %+v", out.Aggregate.FailureCodeHistogram)
	}
	if out.Aggregate.FailureCodeHistogram["UNKNOWN"] != 0 {
		t.Fatalf("expected UNKNOWN bucket to be empty, got: %+v", out.Aggregate.FailureCodeHistogram)
	}
}

func assertRunReportAxes(t *testing.T, out reportRunOut) {
	t.Helper()
	if out.Aggregate.Task.Passed != 1 || out.Aggregate.Task.Failed != 1 || out.Aggregate.Task.Unknown != 0 {
		t.Fatalf("unexpected task axis: %+v", out.Aggregate.Task)
	}
	if out.Aggregate.Evidence.Complete != 2 || out.Aggregate.Evidence.Incomplete != 0 {
		t.Fatalf("unexpected evidence axis: %+v", out.Aggregate.Evidence)
	}
	if out.Aggregate.Orchestration.Healthy != 2 || out.Aggregate.Orchestration.InfraFailed != 0 {
		t.Fatalf("unexpected orchestration axis: %+v", out.Aggregate.Orchestration)
	}
	if out.Aggregate.TokenEstimates == nil || out.Aggregate.TokenEstimates.TotalTokens == nil {
		t.Fatalf("expected aggregate token estimates")
	}
}

type ioDiscard struct{}

func (ioDiscard) Write(p []byte) (int, error) { return len(p), nil }

func setEnvFromMap(t *testing.T, env map[string]string) {
	t.Helper()
	for k, v := range env {
		t.Setenv(k, v)
	}
}
