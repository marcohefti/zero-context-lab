package trace

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/marcohefti/zero-context-lab/internal/kernel/schema"
)

func TestAppendCLIRunEvent_BoundsInputAndSignalsTruncation(t *testing.T) {
	t.Parallel()

	outDir, env, argv := traceBoundsFixture(t)
	now := time.Date(2026, 2, 15, 18, 0, 0, 0, time.UTC)
	if err := AppendCLIRunEvent(now, env, argv, ResultForTrace{ExitCode: 0, DurationMs: 1}); err != nil {
		t.Fatalf("AppendCLIRunEvent: %v", err)
	}

	ev := readFirstTraceEvent(t, filepath.Join(outDir, "tool.calls.jsonl"))
	assertBoundedCLITraceEvent(t, ev)
}

func TestBoundedToolInputJSON_UnknownOversizedUsesPlaceholder(t *testing.T) {
	t.Parallel()

	huge := map[string]any{
		"payload": strings.Repeat("x", schema.ToolInputMaxBytesV1*3),
	}
	got, truncated, warnings, err := boundedToolInputJSON(huge, schema.ToolInputMaxBytesV1)
	if err != nil {
		t.Fatalf("boundedToolInputJSON: %v", err)
	}
	if !truncated {
		t.Fatalf("expected truncated=true")
	}
	if len(got) == 0 || !json.Valid(got) {
		t.Fatalf("expected non-empty valid json placeholder input, got=%q", string(got))
	}
	containsWarning := false
	for _, w := range warnings {
		if w.Code == "ZCL_W_INPUT_TRUNCATED" {
			containsWarning = true
			break
		}
	}
	if !containsWarning {
		t.Fatalf("expected ZCL_W_INPUT_TRUNCATED warning, got=%+v", warnings)
	}
}

func traceBoundsFixture(t *testing.T) (string, Env, []string) {
	t.Helper()
	outDir := t.TempDir()
	env := Env{
		RunID:     "20260215-180012Z-09c5a6",
		SuiteID:   "heftiweb-smoke",
		MissionID: "latest-blog-title",
		AttemptID: "001-latest-blog-title-r1",
		OutDirAbs: outDir,
	}
	argv := []string{"echo"}
	for i := 0; i < 5000; i++ {
		argv = append(argv, "a="+strings.Repeat("x", 50))
	}
	return outDir, env, argv
}

func readFirstTraceEvent(t *testing.T, tracePath string) schema.TraceEventV1 {
	t.Helper()
	f, err := os.Open(tracePath)
	if err != nil {
		t.Fatalf("open trace: %v", err)
	}
	defer func() { _ = f.Close() }()

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 2*1024*1024)
	if !sc.Scan() {
		if err := sc.Err(); err != nil {
			t.Fatalf("scan: %v", err)
		}
		t.Fatalf("expected one trace line")
	}
	var ev schema.TraceEventV1
	if err := json.Unmarshal(sc.Bytes(), &ev); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return ev
}

func assertBoundedCLITraceEvent(t *testing.T, ev schema.TraceEventV1) {
	t.Helper()
	if ev.Tool != "cli" || ev.Op != "exec" {
		t.Fatalf("unexpected tool/op: %q %q", ev.Tool, ev.Op)
	}
	if len(ev.Input) > schema.ToolInputMaxBytesV1 {
		t.Fatalf("input exceeds bounds: %d > %d", len(ev.Input), schema.ToolInputMaxBytesV1)
	}
	if ev.Integrity == nil || !ev.Integrity.Truncated {
		t.Fatalf("expected integrity.truncated")
	}
	if !hasWarningCode(ev.Warnings, "ZCL_W_INPUT_TRUNCATED") {
		t.Fatalf("expected ZCL_W_INPUT_TRUNCATED warning, got: %+v", ev.Warnings)
	}
}

func hasWarningCode(warnings []schema.TraceWarningV1, code string) bool {
	for _, w := range warnings {
		if w.Code == code {
			return true
		}
	}
	return false
}
