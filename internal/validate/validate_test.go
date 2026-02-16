package validate

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/marcohefti/zero-context-lab/internal/schema"
)

func TestValidate_MissingArtifact_Strict(t *testing.T) {
	dir := t.TempDir()
	_, err := ValidatePath(dir, true)
	if err == nil {
		t.Fatalf("expected error")
	}
	if !IsCliError(err, "ZCL_E_USAGE") && !IsCliError(err, "ZCL_E_MISSING_ARTIFACT") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidate_InvalidJSON_Attempt(t *testing.T) {
	attemptDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(attemptDir, "attempt.json"), []byte("{nope"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	_, err := ValidatePath(attemptDir, true)
	if err == nil {
		t.Fatalf("expected error")
	}
	if !IsCliError(err, "ZCL_E_INVALID_JSON") {
		t.Fatalf("expected ZCL_E_INVALID_JSON, got: %v", err)
	}
}

func TestValidate_SchemaUnsupported(t *testing.T) {
	attemptDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(attemptDir, "attempt.json"), []byte(`{"schemaVersion":999,"runId":"r","suiteId":"s","missionId":"m","attemptId":"`+filepath.Base(attemptDir)+`","mode":"discovery","startedAt":"t"}`), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	_, err := ValidatePath(attemptDir, true)
	if err == nil {
		t.Fatalf("expected error")
	}
	if !IsCliError(err, "ZCL_E_SCHEMA_UNSUPPORTED") {
		t.Fatalf("expected ZCL_E_SCHEMA_UNSUPPORTED, got: %v", err)
	}
}

func TestValidate_BoundsExceeded(t *testing.T) {
	attemptDir := t.TempDir()
	attemptID := filepath.Base(attemptDir)
	if err := os.WriteFile(filepath.Join(attemptDir, "attempt.json"), []byte(`{"schemaVersion":1,"runId":"20260215-180012Z-09c5a6","suiteId":"s","missionId":"m","attemptId":"`+attemptID+`","mode":"discovery","startedAt":"2026-02-15T18:00:00Z"}`), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := os.WriteFile(filepath.Join(attemptDir, "feedback.json"), []byte(`{"schemaVersion":1,"runId":"20260215-180012Z-09c5a6","suiteId":"s","missionId":"m","attemptId":"`+attemptID+`","ok":true,"result":"x","createdAt":"2026-02-15T18:00:00Z"}`), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	tooLong := make([]byte, schema.PreviewMaxBytesV1+1)
	for i := range tooLong {
		tooLong[i] = 'a'
	}
	line := `{"v":1,"ts":"2026-02-15T18:00:01Z","runId":"20260215-180012Z-09c5a6","suiteId":"s","missionId":"m","attemptId":"` + attemptID + `","tool":"t","op":"exec","result":{"ok":true,"durationMs":1},"io":{"outBytes":0,"errBytes":0,"outPreview":"` + string(tooLong) + `","errPreview":""}}`
	if err := os.WriteFile(filepath.Join(attemptDir, "tool.calls.jsonl"), []byte(line+"\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	_, err := ValidatePath(attemptDir, true)
	if err == nil {
		t.Fatalf("expected error")
	}
	if !IsCliError(err, "ZCL_E_BOUNDS") {
		t.Fatalf("expected ZCL_E_BOUNDS, got: %v", err)
	}
}

func TestValidate_FunnelBypass_Strict(t *testing.T) {
	attemptDir := t.TempDir()
	attemptID := filepath.Base(attemptDir)
	if err := os.WriteFile(filepath.Join(attemptDir, "attempt.json"), []byte(`{"schemaVersion":1,"runId":"20260215-180012Z-09c5a6","suiteId":"s","missionId":"m","attemptId":"`+attemptID+`","mode":"ci","startedAt":"2026-02-15T18:00:00Z"}`), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := os.WriteFile(filepath.Join(attemptDir, "feedback.json"), []byte(`{"schemaVersion":1,"runId":"20260215-180012Z-09c5a6","suiteId":"s","missionId":"m","attemptId":"`+attemptID+`","ok":true,"result":"x","createdAt":"2026-02-15T18:00:00Z"}`), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	_, err := ValidatePath(attemptDir, true)
	if err == nil {
		t.Fatalf("expected error")
	}
	if !IsCliError(err, "ZCL_E_FUNNEL_BYPASS") {
		t.Fatalf("expected ZCL_E_FUNNEL_BYPASS, got: %v", err)
	}
}

func TestValidate_SuiteExpectations_Strict(t *testing.T) {
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
	if err := os.WriteFile(filepath.Join(attemptDir, "attempt.json"), []byte(`{"schemaVersion":1,"runId":"`+runID+`","suiteId":"s","missionId":"m","attemptId":"`+attemptID+`","mode":"ci","startedAt":"2026-02-15T18:00:00Z"}`), 0o644); err != nil {
		t.Fatalf("write attempt.json: %v", err)
	}
	if err := os.WriteFile(filepath.Join(attemptDir, "tool.calls.jsonl"), []byte(`{"v":1,"ts":"2026-02-15T18:00:01Z","runId":"`+runID+`","suiteId":"s","missionId":"m","attemptId":"`+attemptID+`","tool":"t","op":"exec","result":{"ok":true,"durationMs":1},"io":{"outBytes":0,"errBytes":0}}`+"\n"), 0o644); err != nil {
		t.Fatalf("write tool.calls.jsonl: %v", err)
	}
	if err := os.WriteFile(filepath.Join(attemptDir, "feedback.json"), []byte(`{"schemaVersion":1,"runId":"`+runID+`","suiteId":"s","missionId":"m","attemptId":"`+attemptID+`","ok":false,"result":"x","createdAt":"2026-02-15T18:00:02Z"}`), 0o644); err != nil {
		t.Fatalf("write feedback.json: %v", err)
	}

	_, err := ValidatePath(attemptDir, true)
	if err == nil {
		t.Fatalf("expected error")
	}
	if !IsCliError(err, "ZCL_E_EXPECTATION_FAILED") {
		t.Fatalf("expected ZCL_E_EXPECTATION_FAILED, got: %v", err)
	}
}
