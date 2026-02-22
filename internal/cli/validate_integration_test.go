package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/marcohefti/zero-context-lab/internal/validate"
)

func TestValidate_SemanticMode_UsesSuiteSemanticRules(t *testing.T) {
	runDir := t.TempDir()
	attemptDir := filepath.Join(runDir, "attempts", "001-mission-a-r1")
	if err := os.MkdirAll(attemptDir, 0o755); err != nil {
		t.Fatalf("mkdir attempt dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(runDir, "suite.json"), []byte(`{
  "version": 1,
  "suiteId": "suite-a",
  "missions": [
    {
      "missionId": "mission-a",
      "expects": {
        "semantic": {
          "nonEmptyJsonPointers": ["/title"],
          "placeholderValues": ["n/a"]
        }
      }
    }
  ]
}`), 0o644); err != nil {
		t.Fatalf("write suite.json: %v", err)
	}
	if err := os.WriteFile(filepath.Join(attemptDir, "attempt.json"), []byte(`{
  "schemaVersion": 1,
  "runId": "20260222-101010Z-abc123",
  "suiteId": "suite-a",
  "missionId": "mission-a",
  "attemptId": "001-mission-a-r1",
  "mode": "discovery",
  "startedAt": "2026-02-22T10:10:10Z"
}`), 0o644); err != nil {
		t.Fatalf("write attempt.json: %v", err)
	}
	if err := os.WriteFile(filepath.Join(attemptDir, "feedback.json"), []byte(`{
  "schemaVersion": 1,
  "runId": "20260222-101010Z-abc123",
  "suiteId": "suite-a",
  "missionId": "mission-a",
  "attemptId": "001-mission-a-r1",
  "ok": true,
  "resultJson": {"title":"n/a"},
  "createdAt": "2026-02-22T10:10:15Z"
}`), 0o644); err != nil {
		t.Fatalf("write feedback.json: %v", err)
	}
	if err := os.WriteFile(filepath.Join(attemptDir, "tool.calls.jsonl"), []byte(`{"v":1,"ts":"2026-02-22T10:10:12Z","runId":"20260222-101010Z-abc123","missionId":"mission-a","attemptId":"001-mission-a-r1","tool":"cli","op":"exec","input":{"argv":["echo","hi"]},"result":{"ok":true,"durationMs":1},"io":{"outBytes":2,"errBytes":0}}`+"\n"), 0o644); err != nil {
		t.Fatalf("write tool.calls.jsonl: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	r := Runner{
		Version: "0.0.0-dev",
		Now:     func() time.Time { return time.Date(2026, 2, 22, 11, 0, 0, 0, time.UTC) },
		Stdout:  &stdout,
		Stderr:  &stderr,
	}
	code := r.Run([]string{"validate", "--semantic", "--json", attemptDir})
	if code != 2 {
		t.Fatalf("expected semantic failure exit 2, got %d stderr=%q", code, stderr.String())
	}
	var res struct {
		OK        bool `json:"ok"`
		Evaluated bool `json:"evaluated"`
		Failures  []struct {
			Code string `json:"code"`
		} `json:"failures"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &res); err != nil {
		t.Fatalf("unmarshal semantic validate json: %v stdout=%q", err, stdout.String())
	}
	if res.OK || !res.Evaluated || len(res.Failures) == 0 {
		t.Fatalf("unexpected semantic validate result: %+v", res)
	}
}

func TestValidate_JSONStableOnFailureAndExitCodes(t *testing.T) {
	// Usage-style failure: directory exists but is not an attemptDir or runDir.
	{
		dir := t.TempDir()
		var stdout, stderr bytes.Buffer
		r := Runner{
			Version: "0.0.0-dev",
			Now:     func() time.Time { return time.Date(2026, 2, 16, 18, 0, 0, 0, time.UTC) },
			Stdout:  &stdout,
			Stderr:  &stderr,
		}

		code := r.Run([]string{"validate", "--json", dir})
		if code != 2 {
			t.Fatalf("expected exit 2 for usage failure, got %d (stderr=%q)", code, stderr.String())
		}
		var res validate.Result
		if err := json.Unmarshal(stdout.Bytes(), &res); err != nil {
			t.Fatalf("expected json output, unmarshal failed: %v (stdout=%q)", err, stdout.String())
		}
		if res.OK {
			t.Fatalf("expected ok=false, got ok=true")
		}
		found := false
		for _, f := range res.Errors {
			if f.Code == "ZCL_E_USAGE" {
				found = true
			}
		}
		if !found {
			t.Fatalf("expected ZCL_E_USAGE in errors, got: %+v", res.Errors)
		}
	}

	// IO-style failure: directory does not exist.
	{
		parent := t.TempDir()
		missing := filepath.Join(parent, "does-not-exist")
		var stdout, stderr bytes.Buffer
		r := Runner{
			Version: "0.0.0-dev",
			Now:     func() time.Time { return time.Date(2026, 2, 16, 18, 0, 0, 0, time.UTC) },
			Stdout:  &stdout,
			Stderr:  &stderr,
		}

		code := r.Run([]string{"validate", "--json", missing})
		if code != 1 {
			t.Fatalf("expected exit 1 for io failure, got %d (stderr=%q)", code, stderr.String())
		}
		var res validate.Result
		if err := json.Unmarshal(stdout.Bytes(), &res); err != nil {
			t.Fatalf("expected json output, unmarshal failed: %v (stdout=%q)", err, stdout.String())
		}
		if res.OK {
			t.Fatalf("expected ok=false, got ok=true")
		}
		found := false
		for _, f := range res.Errors {
			if f.Code == "ZCL_E_IO" {
				found = true
			}
		}
		if !found {
			t.Fatalf("expected ZCL_E_IO in errors, got: %+v", res.Errors)
		}
	}
}
