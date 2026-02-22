package trace

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAppendNativeRuntimeEvent_WritesCanonicalTrace(t *testing.T) {
	outDir := t.TempDir()
	env := Env{RunID: "run", SuiteID: "suite", MissionID: "mission", AttemptID: "attempt", OutDirAbs: outDir}

	if err := AppendNativeRuntimeEvent(time.Date(2026, 2, 22, 14, 0, 0, 0, time.UTC), env, NativeRuntimeEvent{
		RuntimeID: "codex_app_server",
		SessionID: "session-1",
		ThreadID:  "thread-1",
		TurnID:    "turn-1",
		CallID:    "call-1",
		EventName: "codex/event/turn_completed",
		Payload:   json.RawMessage(`{"delta":"ok"}`),
	}); err != nil {
		t.Fatalf("append native event: %v", err)
	}

	raw, err := os.ReadFile(filepath.Join(outDir, "tool.calls.jsonl"))
	if err != nil {
		t.Fatalf("read trace: %v", err)
	}
	line := strings.TrimSpace(string(raw))
	if line == "" {
		t.Fatalf("expected non-empty trace")
	}
	var row map[string]any
	if err := json.Unmarshal([]byte(line), &row); err != nil {
		t.Fatalf("unmarshal trace row: %v", err)
	}
	if row["tool"] != "native" || row["op"] != "turn_completed" {
		t.Fatalf("unexpected tool/op: %+v", row)
	}
	result, _ := row["result"].(map[string]any)
	if ok, _ := result["ok"].(bool); !ok {
		t.Fatalf("expected result.ok=true, got %+v", result)
	}
}

func TestAppendNativeRuntimeEvent_PartialMarksIntegrity(t *testing.T) {
	outDir := t.TempDir()
	env := Env{RunID: "run", SuiteID: "suite", MissionID: "mission", AttemptID: "attempt", OutDirAbs: outDir}

	if err := AppendNativeRuntimeEvent(time.Date(2026, 2, 22, 14, 1, 0, 0, time.UTC), env, NativeRuntimeEvent{
		RuntimeID: "codex_app_server",
		EventName: "codex/event/stream_disconnect",
		Code:      "ZCL_E_RUNTIME_STREAM_DISCONNECT",
		Partial:   true,
	}); err != nil {
		t.Fatalf("append native event: %v", err)
	}

	raw, err := os.ReadFile(filepath.Join(outDir, "tool.calls.jsonl"))
	if err != nil {
		t.Fatalf("read trace: %v", err)
	}
	line := strings.TrimSpace(string(raw))
	var row map[string]any
	if err := json.Unmarshal([]byte(line), &row); err != nil {
		t.Fatalf("unmarshal trace row: %v", err)
	}
	result, _ := row["result"].(map[string]any)
	if ok, _ := result["ok"].(bool); ok {
		t.Fatalf("expected result.ok=false for partial event")
	}
	if code, _ := result["code"].(string); code != "ZCL_E_RUNTIME_STREAM_DISCONNECT" {
		t.Fatalf("unexpected result.code=%q", code)
	}
	integrity, _ := row["integrity"].(map[string]any)
	if truncated, _ := integrity["truncated"].(bool); !truncated {
		t.Fatalf("expected integrity.truncated=true")
	}
}

func TestAppendNativeRuntimeEvent_RedactsPayloadSecrets(t *testing.T) {
	outDir := t.TempDir()
	env := Env{RunID: "run", SuiteID: "suite", MissionID: "mission", AttemptID: "attempt", OutDirAbs: outDir}

	if err := AppendNativeRuntimeEvent(time.Date(2026, 2, 22, 14, 2, 0, 0, time.UTC), env, NativeRuntimeEvent{
		RuntimeID: "codex_app_server",
		EventName: "codex/event/turn_failed",
		Payload:   json.RawMessage(`{"error":{"message":"bad key sk-1234567890abcdef","codexErrorInfo":"UsageLimitExceeded"}}`),
	}); err != nil {
		t.Fatalf("append native event: %v", err)
	}

	raw, err := os.ReadFile(filepath.Join(outDir, "tool.calls.jsonl"))
	if err != nil {
		t.Fatalf("read trace: %v", err)
	}
	var row map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(string(raw))), &row); err != nil {
		t.Fatalf("unmarshal trace row: %v", err)
	}
	redactions, _ := row["redactionsApplied"].([]any)
	if len(redactions) == 0 {
		t.Fatalf("expected redactionsApplied for secret payload")
	}
	input, _ := row["input"].(map[string]any)
	inputRaw, _ := json.Marshal(input)
	if strings.Contains(string(inputRaw), "sk-1234567890abcdef") {
		t.Fatalf("expected payload secret to be redacted, got %s", string(inputRaw))
	}
}
