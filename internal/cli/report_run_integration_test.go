package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/marcohefti/zero-context-lab/internal/attempt"
)

func TestReport_RunJSONAggregates(t *testing.T) {
	outRoot := t.TempDir()
	now := time.Date(2026, 2, 18, 9, 0, 0, 0, time.UTC)

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

	t.Setenv("GO_WANT_HELPER_PROCESS", "1")

	r := Runner{
		Version: "0.0.0-dev",
		Now:     func() time.Time { return now.Add(5 * time.Second) },
		Stdout:  ioDiscard{},
		Stderr:  ioDiscard{},
	}

	setEnvFromMap(t, a1.Env)
	if code := r.Run([]string{"run", "--", os.Args[0], "-test.run=TestHelperProcess", "--", "stdout=ok\n", "exit=0"}); code != 0 {
		t.Fatalf("run a1: code=%d", code)
	}
	if code := r.Run([]string{"feedback", "--ok", "--result", "ok"}); code != 0 {
		t.Fatalf("feedback a1: code=%d", code)
	}

	setEnvFromMap(t, a2.Env)
	if code := r.Run([]string{"run", "--", os.Args[0], "-test.run=TestHelperProcess", "--", "stderr=bad\n", "exit=1"}); code != 1 {
		t.Fatalf("run a2 expected exit 1, got %d", code)
	}
	if code := r.Run([]string{"feedback", "--fail", "--result", "bad", "--decision-tag", "blocked"}); code != 0 {
		t.Fatalf("feedback a2: code=%d", code)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	r2 := Runner{
		Version: "0.0.0-dev",
		Now:     func() time.Time { return now.Add(10 * time.Second) },
		Stdout:  &stdout,
		Stderr:  &stderr,
	}
	runDir := filepath.Join(outRoot, "runs", a1.RunID)
	if code := r2.Run([]string{"report", "--json", runDir}); code != 0 {
		t.Fatalf("report runDir: code=%d stderr=%q", code, stderr.String())
	}

	var out struct {
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
	if err := json.Unmarshal(stdout.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal run report json: %v (stdout=%q)", err, stdout.String())
	}
	if out.Target != "run" || out.RunID != a1.RunID {
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
