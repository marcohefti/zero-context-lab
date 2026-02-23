package campaign

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/marcohefti/zero-context-lab/internal/schema"
)

func TestEvaluateToolPolicy_AllowNamespacePasses(t *testing.T) {
	attemptDir := t.TempDir()
	writeToolPolicyTraceEvent(t, attemptDir, schema.TraceEventV1{
		V: 1, TS: "2026-02-22T00:00:00Z", RunID: "r", MissionID: "m", AttemptID: "a", Tool: "mcp", Op: "tools/call",
		Input: mustJSON(t, map[string]any{
			"params": map[string]any{"name": "chrome-devtools__click"},
		}),
	})
	reasons, err := EvaluateToolPolicy(ToolPolicySpec{
		Allow: []ToolPolicyRuleSpec{{Namespace: "mcp"}},
	}, attemptDir)
	if err != nil {
		t.Fatalf("EvaluateToolPolicy: %v", err)
	}
	if len(reasons) != 0 {
		t.Fatalf("expected pass, got reasons=%+v", reasons)
	}
}

func TestEvaluateToolPolicy_DenyNamespaceFails(t *testing.T) {
	attemptDir := t.TempDir()
	writeToolPolicyTraceEvent(t, attemptDir, schema.TraceEventV1{
		V: 1, TS: "2026-02-22T00:00:00Z", RunID: "r", MissionID: "m", AttemptID: "a", Tool: "cli", Op: "exec",
		Input: mustJSON(t, map[string]any{
			"argv": []string{"bash", "-lc", "echo hi"},
		}),
	})
	reasons, err := EvaluateToolPolicy(ToolPolicySpec{
		Deny: []ToolPolicyRuleSpec{{Namespace: "cli"}},
	}, attemptDir)
	if err != nil {
		t.Fatalf("EvaluateToolPolicy: %v", err)
	}
	if len(reasons) != 1 || reasons[0] != ReasonToolPolicy {
		t.Fatalf("expected tool policy violation, got reasons=%+v", reasons)
	}
}

func TestEvaluateToolPolicy_AllowPrefixWithAliasPasses(t *testing.T) {
	attemptDir := t.TempDir()
	writeToolPolicyTraceEvent(t, attemptDir, schema.TraceEventV1{
		V: 1, TS: "2026-02-22T00:00:00Z", RunID: "r", MissionID: "m", AttemptID: "a", Tool: "mcp", Op: "tools/call",
		Input: mustJSON(t, map[string]any{
			"params": map[string]any{"name": "chrome-devtools__click"},
		}),
	})
	reasons, err := EvaluateToolPolicy(ToolPolicySpec{
		Allow: []ToolPolicyRuleSpec{{Namespace: "mcp", Prefix: "chrome"}},
		Aliases: map[string][]string{
			"chrome": {"chrome-devtools__", "chrome-mcp__"},
		},
	}, attemptDir)
	if err != nil {
		t.Fatalf("EvaluateToolPolicy: %v", err)
	}
	if len(reasons) != 0 {
		t.Fatalf("expected pass, got reasons=%+v", reasons)
	}
}

func TestEvaluateToolPolicy_AllowRulesRequireMatch(t *testing.T) {
	attemptDir := t.TempDir()
	writeToolPolicyTraceEvent(t, attemptDir, schema.TraceEventV1{
		V: 1, TS: "2026-02-22T00:00:00Z", RunID: "r", MissionID: "m", AttemptID: "a", Tool: "http", Op: "request",
		Input: mustJSON(t, map[string]any{
			"url": "https://example.test/",
		}),
	})
	reasons, err := EvaluateToolPolicy(ToolPolicySpec{
		Allow: []ToolPolicyRuleSpec{{Namespace: "mcp"}},
	}, attemptDir)
	if err != nil {
		t.Fatalf("EvaluateToolPolicy: %v", err)
	}
	if len(reasons) != 1 || reasons[0] != ReasonToolPolicy {
		t.Fatalf("expected allow-list failure, got reasons=%+v", reasons)
	}
}

func writeToolPolicyTraceEvent(t *testing.T, attemptDir string, ev schema.TraceEventV1) {
	t.Helper()
	path := filepath.Join(attemptDir, "tool.calls.jsonl")
	b, err := json.Marshal(ev)
	if err != nil {
		t.Fatalf("marshal trace: %v", err)
	}
	if err := os.WriteFile(path, append(b, '\n'), 0o644); err != nil {
		t.Fatalf("write trace: %v", err)
	}
}

func mustJSON(t *testing.T, v any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal json: %v", err)
	}
	return b
}
