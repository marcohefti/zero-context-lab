package feedback

import (
	"encoding/json"
	"os"
	"path/filepath"
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

func writeAttemptJSON(t *testing.T, outDir string, env trace.Env, mode string) {
	t.Helper()
	now := time.Date(2026, 2, 15, 18, 0, 0, 0, time.UTC)
	payload := schema.AttemptJSONV1{
		SchemaVersion: schema.ArtifactSchemaV1,
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
