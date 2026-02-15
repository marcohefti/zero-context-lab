package enrich

import (
	"os"
	"path/filepath"
	"testing"
)

func TestEnrichCodexAttempt_WritesArtifacts(t *testing.T) {
	dir := t.TempDir()
	attemptDir := filepath.Join(dir, "attempt")
	if err := os.MkdirAll(attemptDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(attemptDir, "attempt.json"), []byte(`{"schemaVersion":1,"runId":"20260215-180012Z-09c5a6","suiteId":"s","missionId":"m","attemptId":"a","mode":"discovery","startedAt":"2026-02-15T18:00:00Z"}`), 0o644); err != nil {
		t.Fatalf("write attempt.json: %v", err)
	}

	rollout := filepath.Join(dir, "rollout.jsonl")
	// Minimal token_count-like event shape used by our tolerant parser.
	if err := os.WriteFile(rollout, []byte(`{"msg":{"type":"token_count","info":{"total_token_usage":{"totalTokens":10,"inputTokens":1,"outputTokens":2,"cachedInputTokens":0,"reasoningOutputTokens":3}}},"model":"gpt-5.1"}`+"\n"), 0o644); err != nil {
		t.Fatalf("write rollout: %v", err)
	}

	if err := EnrichCodexAttempt(attemptDir, rollout); err != nil {
		t.Fatalf("EnrichCodexAttempt: %v", err)
	}
	if _, err := os.Stat(filepath.Join(attemptDir, "runner.ref.json")); err != nil {
		t.Fatalf("missing runner.ref.json: %v", err)
	}
	if _, err := os.Stat(filepath.Join(attemptDir, "runner.metrics.json")); err != nil {
		t.Fatalf("missing runner.metrics.json: %v", err)
	}
}
