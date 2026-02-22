package campaign

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/marcohefti/zero-context-lab/internal/schema"
)

func TestEvaluateTraceProfile_MCPRequired(t *testing.T) {
	attemptDir := t.TempDir()
	writeTraceEvent(t, attemptDir, schema.TraceEventV1{
		V: 1, TS: "2026-02-22T00:00:00Z", RunID: "r", MissionID: "m", AttemptID: "a", Tool: "cli", Op: "exec",
	})
	reasons, err := EvaluateTraceProfile(TraceProfileMCPRequired, attemptDir)
	if err != nil {
		t.Fatalf("EvaluateTraceProfile: %v", err)
	}
	if len(reasons) != 1 || reasons[0] != ReasonTraceProfileMCPRequired {
		t.Fatalf("unexpected reasons: %+v", reasons)
	}
}

func TestEvaluateTraceProfile_StrictBrowserBootstrapOnly(t *testing.T) {
	attemptDir := t.TempDir()
	writeTraceEvent(t, attemptDir, schema.TraceEventV1{
		V: 1, TS: "2026-02-22T00:00:00Z", RunID: "r", MissionID: "m", AttemptID: "a", Tool: "mcp", Op: "initialize",
	})
	reasons, err := EvaluateTraceProfile(TraceProfileStrictBrowserComp, attemptDir)
	if err != nil {
		t.Fatalf("EvaluateTraceProfile: %v", err)
	}
	if len(reasons) == 0 {
		t.Fatalf("expected strict profile failure")
	}
}

func writeTraceEvent(t *testing.T, attemptDir string, ev schema.TraceEventV1) {
	t.Helper()
	path := filepath.Join(attemptDir, "tool.calls.jsonl")
	b, err := json.Marshal(ev)
	if err != nil {
		t.Fatalf("marshal trace event: %v", err)
	}
	if err := os.WriteFile(path, append(b, '\n'), 0o644); err != nil {
		t.Fatalf("write trace: %v", err)
	}
}
