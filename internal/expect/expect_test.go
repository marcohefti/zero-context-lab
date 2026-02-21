package expect

import (
	"os"
	"path/filepath"
	"testing"
)

func TestExpect_AttemptExpectationsFail_Strict(t *testing.T) {
	dir := t.TempDir()
	runID := "20260215-180012Z-09c5a6"
	runDir := filepath.Join(dir, "runs", runID)
	attemptID := "001-m-r1"
	attemptDir := filepath.Join(runDir, "attempts", attemptID)
	if err := os.MkdirAll(attemptDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	// Suite expects ok=true, but feedback will have ok=false.
	if err := os.WriteFile(filepath.Join(runDir, "suite.json"), []byte(`{"version":1,"suiteId":"s","missions":[{"missionId":"m","expects":{"ok":true}}]}`), 0o644); err != nil {
		t.Fatalf("write suite.json: %v", err)
	}
	if err := os.WriteFile(filepath.Join(runDir, "run.json"), []byte(`{"schemaVersion":1,"artifactLayoutVersion":1,"runId":"`+runID+`","suiteId":"s","createdAt":"2026-02-15T18:00:00Z"}`), 0o644); err != nil {
		t.Fatalf("write run.json: %v", err)
	}
	if err := os.WriteFile(filepath.Join(attemptDir, "attempt.json"), []byte(`{"schemaVersion":1,"runId":"`+runID+`","suiteId":"s","missionId":"m","attemptId":"`+attemptID+`","mode":"ci","startedAt":"2026-02-15T18:00:00Z"}`), 0o644); err != nil {
		t.Fatalf("write attempt.json: %v", err)
	}
	if err := os.WriteFile(filepath.Join(attemptDir, "feedback.json"), []byte(`{"schemaVersion":1,"runId":"`+runID+`","suiteId":"s","missionId":"m","attemptId":"`+attemptID+`","ok":false,"result":"x","createdAt":"2026-02-15T18:00:02Z"}`), 0o644); err != nil {
		t.Fatalf("write feedback.json: %v", err)
	}

	res, err := ExpectPath(attemptDir, true)
	if err != nil {
		t.Fatalf("ExpectPath: %v", err)
	}
	if res.OK {
		t.Fatalf("expected ok=false")
	}
	if len(res.Failures) == 0 || res.Failures[0].Code != "ZCL_E_EXPECTATION_FAILED" {
		t.Fatalf("expected ZCL_E_EXPECTATION_FAILED, got: %+v", res.Failures)
	}
}

func TestExpect_ResultRequiredJSONPointers_FailsWhenMissing(t *testing.T) {
	dir := t.TempDir()
	runID := "20260215-180012Z-09c5a6"
	runDir := filepath.Join(dir, "runs", runID)
	attemptID := "001-m-r1"
	attemptDir := filepath.Join(runDir, "attempts", attemptID)
	if err := os.MkdirAll(attemptDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	if err := os.WriteFile(filepath.Join(runDir, "suite.json"), []byte(`{
  "version":1,
  "suiteId":"s",
  "missions":[{"missionId":"m","expects":{"result":{"type":"json","requiredJsonPointers":["/proof/value"]}}}]
}`), 0o644); err != nil {
		t.Fatalf("write suite.json: %v", err)
	}
	if err := os.WriteFile(filepath.Join(runDir, "run.json"), []byte(`{"schemaVersion":1,"artifactLayoutVersion":1,"runId":"`+runID+`","suiteId":"s","createdAt":"2026-02-15T18:00:00Z"}`), 0o644); err != nil {
		t.Fatalf("write run.json: %v", err)
	}
	if err := os.WriteFile(filepath.Join(attemptDir, "attempt.json"), []byte(`{"schemaVersion":1,"runId":"`+runID+`","suiteId":"s","missionId":"m","attemptId":"`+attemptID+`","mode":"ci","startedAt":"2026-02-15T18:00:00Z"}`), 0o644); err != nil {
		t.Fatalf("write attempt.json: %v", err)
	}
	if err := os.WriteFile(filepath.Join(attemptDir, "feedback.json"), []byte(`{"schemaVersion":1,"runId":"`+runID+`","suiteId":"s","missionId":"m","attemptId":"`+attemptID+`","ok":true,"resultJson":{"value":1},"createdAt":"2026-02-15T18:00:02Z"}`), 0o644); err != nil {
		t.Fatalf("write feedback.json: %v", err)
	}

	res, err := ExpectPath(attemptDir, true)
	if err != nil {
		t.Fatalf("ExpectPath: %v", err)
	}
	if res.OK {
		t.Fatalf("expected ok=false")
	}
	if len(res.Failures) == 0 || res.Failures[0].Code != "ZCL_E_EXPECTATION_FAILED" {
		t.Fatalf("expected ZCL_E_EXPECTATION_FAILED, got: %+v", res.Failures)
	}
}
