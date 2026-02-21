package feedback

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/marcohefti/zero-context-lab/internal/schema"
	"github.com/marcohefti/zero-context-lab/internal/trace"
)

func TestWrite_ResultStringRedactsAndBounds(t *testing.T) {
	t.Parallel()

	outDir := t.TempDir()
	env := trace.Env{
		RunID:     "20260215-180012Z-09c5a6",
		SuiteID:   "heftiweb-smoke",
		MissionID: "latest-blog-title",
		AttemptID: "001-latest-blog-title-r1",
		OutDirAbs: outDir,
	}
	writeAttemptJSON(t, outDir, env, "discovery")
	writeDummyTrace(t, outDir, env)

	now := time.Date(2026, 2, 15, 18, 0, 0, 0, time.UTC)
	if err := Write(now, env, WriteOpts{
		OK:     true,
		Result: "token=ghp_ABCDEF1234567890",
	}); err != nil {
		t.Fatalf("Write: %v", err)
	}

	raw, err := os.ReadFile(filepath.Join(outDir, "feedback.json"))
	if err != nil {
		t.Fatalf("read feedback.json: %v", err)
	}
	var fb schema.FeedbackJSONV1
	if err := json.Unmarshal(raw, &fb); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if fb.SchemaVersion != 1 || fb.AttemptID != env.AttemptID || !fb.OK {
		t.Fatalf("unexpected feedback: %+v", fb)
	}
	if fb.Result == "" || fb.ResultJSON != nil {
		t.Fatalf("expected string result only: %+v", fb)
	}
	if fb.Result != "token=[REDACTED:GITHUB_TOKEN]" {
		t.Fatalf("expected redaction, got: %q", fb.Result)
	}
	if len(fb.RedactionsApplied) == 0 {
		t.Fatalf("expected redactionsApplied to be non-empty")
	}
}

func TestWrite_ResultJSONCanonicalizes(t *testing.T) {
	t.Parallel()

	outDir := t.TempDir()
	env := trace.Env{
		RunID:     "20260215-180012Z-09c5a6",
		SuiteID:   "heftiweb-smoke",
		MissionID: "latest-blog-title",
		AttemptID: "001-latest-blog-title-r1",
		OutDirAbs: outDir,
	}
	writeAttemptJSON(t, outDir, env, "discovery")
	writeDummyTrace(t, outDir, env)

	now := time.Date(2026, 2, 15, 18, 0, 0, 0, time.UTC)
	if err := Write(now, env, WriteOpts{
		OK:         false,
		ResultJSON: "{\"b\":2,\"a\":1}",
	}); err != nil {
		t.Fatalf("Write: %v", err)
	}

	raw, err := os.ReadFile(filepath.Join(outDir, "feedback.json"))
	if err != nil {
		t.Fatalf("read feedback.json: %v", err)
	}
	var fb schema.FeedbackJSONV1
	if err := json.Unmarshal(raw, &fb); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if fb.Result != "" || fb.ResultJSON == nil {
		t.Fatalf("expected json result only: %+v", fb)
	}
	var got any
	if err := json.Unmarshal(fb.ResultJSON, &got); err != nil {
		t.Fatalf("unmarshal resultJson: %v", err)
	}
	// Semantic check: key ordering/whitespace can vary due to pretty-printing feedback.json.
	want := map[string]any{"a": float64(1), "b": float64(2)}
	if m, ok := got.(map[string]any); !ok || len(m) != 2 || m["a"] != want["a"] || m["b"] != want["b"] {
		t.Fatalf("unexpected resultJson: %#v", got)
	}
}

func TestWrite_EnforcesSuiteResultShapeWhenConfigured(t *testing.T) {
	t.Parallel()

	base := t.TempDir()
	runDir := filepath.Join(base, "runs", "20260215-180012Z-09c5a6")
	outDir := filepath.Join(runDir, "attempts", "001-latest-blog-title-r1")
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		t.Fatalf("mkdir attempt dir: %v", err)
	}
	env := trace.Env{
		RunID:     "20260215-180012Z-09c5a6",
		SuiteID:   "heftiweb-smoke",
		MissionID: "latest-blog-title",
		AttemptID: "001-latest-blog-title-r1",
		OutDirAbs: outDir,
	}
	writeAttemptJSON(t, outDir, env, "discovery")
	writeDummyTrace(t, outDir, env)
	if err := os.WriteFile(filepath.Join(runDir, "run.json"), []byte(`{"schemaVersion":1,"artifactLayoutVersion":1,"runId":"20260215-180012Z-09c5a6","suiteId":"heftiweb-smoke","createdAt":"2026-02-15T18:00:00Z"}`), 0o644); err != nil {
		t.Fatalf("write run.json: %v", err)
	}
	if err := os.WriteFile(filepath.Join(runDir, "suite.json"), []byte(`{
  "version":1,
  "suiteId":"heftiweb-smoke",
  "missions":[{"missionId":"latest-blog-title","expects":{"result":{"type":"json","requiredJsonPointers":["/proof/value"]}}}]
}`), 0o644); err != nil {
		t.Fatalf("write suite.json: %v", err)
	}

	now := time.Date(2026, 2, 15, 18, 0, 0, 0, time.UTC)
	err := Write(now, env, WriteOpts{
		OK:         true,
		ResultJSON: `{"value":1}`,
	})
	if err == nil {
		t.Fatalf("expected shape enforcement error")
	}
	if !strings.Contains(err.Error(), "missing required resultJson pointer /proof/value") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestWrite_SkipSuiteResultShapeForSyntheticFeedback(t *testing.T) {
	t.Parallel()

	base := t.TempDir()
	runDir := filepath.Join(base, "runs", "20260215-180012Z-09c5a6")
	outDir := filepath.Join(runDir, "attempts", "001-latest-blog-title-r1")
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		t.Fatalf("mkdir attempt dir: %v", err)
	}
	env := trace.Env{
		RunID:     "20260215-180012Z-09c5a6",
		SuiteID:   "heftiweb-smoke",
		MissionID: "latest-blog-title",
		AttemptID: "001-latest-blog-title-r1",
		OutDirAbs: outDir,
	}
	writeAttemptJSON(t, outDir, env, "discovery")
	writeDummyTrace(t, outDir, env)
	if err := os.WriteFile(filepath.Join(runDir, "run.json"), []byte(`{"schemaVersion":1,"artifactLayoutVersion":1,"runId":"20260215-180012Z-09c5a6","suiteId":"heftiweb-smoke","createdAt":"2026-02-15T18:00:00Z"}`), 0o644); err != nil {
		t.Fatalf("write run.json: %v", err)
	}
	if err := os.WriteFile(filepath.Join(runDir, "suite.json"), []byte(`{
  "version":1,
  "suiteId":"heftiweb-smoke",
  "missions":[{"missionId":"latest-blog-title","expects":{"result":{"type":"json","requiredJsonPointers":["/proof/value"]}}}]
}`), 0o644); err != nil {
		t.Fatalf("write suite.json: %v", err)
	}

	now := time.Date(2026, 2, 15, 18, 0, 0, 0, time.UTC)
	if err := Write(now, env, WriteOpts{
		OK:                   false,
		Result:               "CONTAMINATED_PROMPT",
		SkipSuiteResultShape: true,
	}); err != nil {
		t.Fatalf("Write with skip should succeed: %v", err)
	}
}

func writeAttemptJSON(t *testing.T, outDir string, env trace.Env, mode string) {
	t.Helper()
	now := time.Date(2026, 2, 15, 18, 0, 0, 0, time.UTC)
	payload := schema.AttemptJSONV1{
		SchemaVersion: schema.AttemptSchemaV1,
		RunID:         env.RunID,
		SuiteID:       env.SuiteID,
		MissionID:     env.MissionID,
		AttemptID:     env.AttemptID,
		Mode:          mode,
		StartedAt:     now.UTC().Format(time.RFC3339Nano),
	}
	b, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal attempt.json: %v", err)
	}
	if err := os.WriteFile(filepath.Join(outDir, "attempt.json"), b, 0o644); err != nil {
		t.Fatalf("write attempt.json: %v", err)
	}
}

func writeDummyTrace(t *testing.T, outDir string, env trace.Env) {
	t.Helper()
	// Minimal valid trace line so feedback can't be written without evidence.
	line := `{"v":1,"ts":"2026-02-15T18:00:01Z","runId":"` + env.RunID + `","suiteId":"` + env.SuiteID + `","missionId":"` + env.MissionID + `","attemptId":"` + env.AttemptID + `","tool":"cli","op":"exec","input":{"argv":["echo","x"]},"result":{"ok":true,"durationMs":1},"io":{"outBytes":0,"errBytes":0}}` + "\n"
	if err := os.WriteFile(filepath.Join(outDir, "tool.calls.jsonl"), []byte(line), 0o644); err != nil {
		t.Fatalf("write tool.calls.jsonl: %v", err)
	}
}
