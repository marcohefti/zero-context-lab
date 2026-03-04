package enrich

import (
	"os"
	"path/filepath"
	"strings"
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

func TestEnrichClaudeAttempt_WritesArtifacts(t *testing.T) {
	dir := t.TempDir()
	attemptDir := filepath.Join(dir, "attempt")
	if err := os.MkdirAll(attemptDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(attemptDir, "attempt.json"), []byte(`{"schemaVersion":1,"runId":"20260215-180012Z-09c5a6","suiteId":"s","missionId":"m","attemptId":"a","mode":"discovery","startedAt":"2026-02-15T18:00:00Z"}`), 0o644); err != nil {
		t.Fatalf("write attempt.json: %v", err)
	}

	sessionPath := filepath.Join(dir, "session.jsonl")
	if err := os.WriteFile(sessionPath, []byte(strings.Join([]string{
		`{"type":"user","message":"hi"}`,
		`{"type":"assistant","model":"claude-4.1","usage":{"input_tokens":12,"output_tokens":18,"cache_creation_input_tokens":4,"total_tokens":34}}`,
	}, "\n")), 0o644); err != nil {
		t.Fatalf("write session: %v", err)
	}

	if err := EnrichClaudeAttempt(attemptDir, sessionPath); err != nil {
		t.Fatalf("EnrichClaudeAttempt: %v", err)
	}
	if _, err := os.Stat(filepath.Join(attemptDir, "runner.ref.json")); err != nil {
		t.Fatalf("missing runner.ref.json: %v", err)
	}
	if _, err := os.Stat(filepath.Join(attemptDir, "runner.metrics.json")); err != nil {
		t.Fatalf("missing runner.metrics.json: %v", err)
	}
}

func TestEnrichClaudeAttempt_ReturnsMissingEvidenceError(t *testing.T) {
	dir := t.TempDir()
	attemptDir := filepath.Join(dir, "attempt")
	if err := os.MkdirAll(attemptDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(attemptDir, "attempt.json"), []byte(`{"schemaVersion":1,"runId":"20260215-180012Z-09c5a6","suiteId":"s","missionId":"m","attemptId":"a","mode":"discovery","startedAt":"2026-02-15T18:00:00Z"}`), 0o644); err != nil {
		t.Fatalf("write attempt.json: %v", err)
	}

	sessionPath := filepath.Join(dir, "session.jsonl")
	if err := os.WriteFile(sessionPath, []byte(`{"type":"assistant","content":"hi"}`), 0o644); err != nil {
		t.Fatalf("write session: %v", err)
	}

	err := EnrichClaudeAttempt(attemptDir, sessionPath)
	if err == nil {
		t.Fatalf("expected error for missing data")
	}
	if !IsCliError(err, "ZCL_E_MISSING_EVIDENCE") {
		t.Fatalf("expected ZCL_E_MISSING_EVIDENCE, got: %v", err)
	}
	if msg := err.Error(); !strings.Contains(msg, "parsed=1") {
		t.Fatalf("expected parse telemetry in error, got: %v", msg)
	}
}
