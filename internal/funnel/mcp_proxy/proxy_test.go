package mcpproxy

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/marcohefti/zero-context-lab/internal/schema"
	"github.com/marcohefti/zero-context-lab/internal/trace"
)

func TestProxy_TracesInitializeToolsListToolsCall(t *testing.T) {
	outDir := t.TempDir()
	env := trace.Env{
		RunID:     "20260215-180012Z-09c5a6",
		SuiteID:   "heftiweb-smoke",
		MissionID: "latest-blog-title",
		AttemptID: "001-latest-blog-title-r1",
		OutDirAbs: outDir,
	}

	reqs := strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"echo","arguments":{"text":"hi"}}}`,
	}, "\n") + "\n"

	t.Setenv("GO_WANT_MCP_SERVER_HELPER", "1")

	var clientOut bytes.Buffer
	serverArgv := []string{os.Args[0], "-test.run=TestMCPServerHelper"}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := Proxy(ctx, env, serverArgv, bytes.NewBufferString(reqs), &clientOut, 16*1024); err != nil {
		t.Fatalf("Proxy: %v", err)
	}

	// Ensure server responses were forwarded.
	if got := clientOut.String(); !strings.Contains(got, `"result"`) {
		t.Fatalf("expected forwarded responses, got: %q", got)
	}

	// Validate trace events.
	events := readAllTraceEvents(t, filepath.Join(outDir, "tool.calls.jsonl"))
	if len(events) != 4 {
		t.Fatalf("expected 4 trace events, got %d", len(events))
	}
	if events[0].Op != "spawn" || events[1].Op != "initialize" || events[2].Op != "tools/list" || events[3].Op != "tools/call" {
		t.Fatalf("unexpected ops: %+v", []string{events[0].Op, events[1].Op, events[2].Op, events[3].Op})
	}
	for _, ev := range events {
		if ev.Tool != "mcp" {
			t.Fatalf("unexpected tool: %q", ev.Tool)
		}
	}
}

func TestProxy_RedactsSecretsInTraceButNotPassthrough(t *testing.T) {
	outDir := t.TempDir()
	env := trace.Env{
		RunID:     "20260215-180012Z-09c5a6",
		SuiteID:   "heftiweb-smoke",
		MissionID: "latest-blog-title",
		AttemptID: "001-latest-blog-title-r1",
		OutDirAbs: outDir,
	}

	openAIKey := "sk-1234567890ABCDEF"
	reqs := strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"echo","arguments":{"text":"` + openAIKey + `"}}}`,
	}, "\n") + "\n"

	t.Setenv("GO_WANT_MCP_SERVER_HELPER", "1")

	var clientOut bytes.Buffer
	serverArgv := []string{os.Args[0], "-test.run=TestMCPServerHelper"}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := Proxy(ctx, env, serverArgv, bytes.NewBufferString(reqs), &clientOut, 16*1024); err != nil {
		t.Fatalf("Proxy: %v", err)
	}

	// Passthrough should not be redacted.
	if got := clientOut.String(); !strings.Contains(got, openAIKey) {
		t.Fatalf("expected raw key in forwarded response, got: %q", got)
	}

	events := readAllTraceEvents(t, filepath.Join(outDir, "tool.calls.jsonl"))
	if len(events) != 3 {
		t.Fatalf("expected 3 trace events, got %d", len(events))
	}
	ev := events[2]
	if strings.Contains(string(ev.Input), openAIKey) || strings.Contains(ev.IO.OutPreview, openAIKey) {
		t.Fatalf("expected redaction in trace, got input=%q out=%q", string(ev.Input), ev.IO.OutPreview)
	}
	if !strings.Contains(string(ev.Input), "[REDACTED:OPENAI_KEY]") {
		t.Fatalf("expected input redaction, got: %q", string(ev.Input))
	}
	if !strings.Contains(ev.IO.OutPreview, "[REDACTED:OPENAI_KEY]") {
		t.Fatalf("expected outPreview redaction, got: %q", ev.IO.OutPreview)
	}
	found := false
	for _, n := range ev.RedactionsApplied {
		if n == "openai_key" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected redactionsApplied to include openai_key, got: %+v", ev.RedactionsApplied)
	}
}

func TestProxy_FailsIfTraceCannotBeWritten(t *testing.T) {
	outDir := t.TempDir()
	if err := os.Chmod(outDir, 0o555); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	env := trace.Env{
		RunID:     "20260215-180012Z-09c5a6",
		SuiteID:   "heftiweb-smoke",
		MissionID: "latest-blog-title",
		AttemptID: "001-latest-blog-title-r1",
		OutDirAbs: outDir,
	}

	reqs := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}` + "\n"
	t.Setenv("GO_WANT_MCP_SERVER_HELPER", "1")

	var clientOut bytes.Buffer
	serverArgv := []string{os.Args[0], "-test.run=TestMCPServerHelper"}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := Proxy(ctx, env, serverArgv, bytes.NewBufferString(reqs), &clientOut, 16*1024); err == nil {
		t.Fatalf("expected error")
	}
}

func TestProxyWithOptions_MaxToolCallsStopsProxy(t *testing.T) {
	outDir := t.TempDir()
	env := trace.Env{
		RunID:     "20260215-180012Z-09c5a6",
		SuiteID:   "heftiweb-smoke",
		MissionID: "latest-blog-title",
		AttemptID: "001-latest-blog-title-r1",
		OutDirAbs: outDir,
	}

	reqs := strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"echo","arguments":{"text":"a"}}}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"echo","arguments":{"text":"b"}}}`,
	}, "\n") + "\n"

	t.Setenv("GO_WANT_MCP_SERVER_HELPER", "1")

	var clientOut bytes.Buffer
	serverArgv := []string{os.Args[0], "-test.run=TestMCPServerHelper"}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err := ProxyWithOptions(ctx, env, serverArgv, bytes.NewBufferString(reqs), &clientOut, Options{
		MaxPreviewBytes: 16 * 1024,
		MaxToolCalls:    1,
	})
	if err == nil {
		t.Fatalf("expected max-tool-calls error")
	}

	events := readAllTraceEvents(t, filepath.Join(outDir, "tool.calls.jsonl"))
	foundLimit := false
	for _, ev := range events {
		if ev.Op == "limit" && ev.Result.Code == "ZCL_E_MCP_MAX_TOOL_CALLS" {
			foundLimit = true
			break
		}
	}
	if !foundLimit {
		t.Fatalf("expected limit event in trace, got %+v", events)
	}
}

func TestProxyWithOptions_IdleTimeoutStopsProxy(t *testing.T) {
	outDir := t.TempDir()
	env := trace.Env{
		RunID:     "20260215-180012Z-09c5a6",
		SuiteID:   "heftiweb-smoke",
		MissionID: "latest-blog-title",
		AttemptID: "001-latest-blog-title-r1",
		OutDirAbs: outDir,
	}

	t.Setenv("GO_WANT_MCP_SERVER_HELPER", "1")
	serverArgv := []string{os.Args[0], "-test.run=TestMCPServerHelper"}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	pr, pw := io.Pipe()
	defer func() { _ = pw.Close() }()

	var clientOut bytes.Buffer
	err := ProxyWithOptions(ctx, env, serverArgv, pr, &clientOut, Options{
		MaxPreviewBytes: 16 * 1024,
		IdleTimeoutMs:   150,
	})
	if err == nil {
		t.Fatalf("expected idle-timeout error")
	}

	events := readAllTraceEvents(t, filepath.Join(outDir, "tool.calls.jsonl"))
	foundTimeout := false
	for _, ev := range events {
		if ev.Op == "idle-timeout" && ev.Result.Code == "ZCL_E_TIMEOUT" {
			foundTimeout = true
			break
		}
	}
	if !foundTimeout {
		t.Fatalf("expected idle-timeout event in trace, got %+v", events)
	}
}

func TestMCPServerHelper(t *testing.T) {
	if os.Getenv("GO_WANT_MCP_SERVER_HELPER") != "1" {
		return
	}
	sc := bufio.NewScanner(os.Stdin)
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		var msg map[string]any
		if err := json.Unmarshal(line, &msg); err != nil {
			continue
		}
		id := msg["id"]
		method, _ := msg["method"].(string)
		switch method {
		case "initialize":
			_, _ = os.Stdout.WriteString(`{"jsonrpc":"2.0","id":` + jsonID(id) + `,"result":{"capabilities":{}}}` + "\n")
		case "tools/list":
			_, _ = os.Stdout.WriteString(`{"jsonrpc":"2.0","id":` + jsonID(id) + `,"result":{"tools":[{"name":"echo"}]}}` + "\n")
		case "tools/call":
			// Echo the requested text so tests can assert redaction behavior in the proxy trace.
			text := "ok"
			if params, ok := msg["params"].(map[string]any); ok {
				if args, ok := params["arguments"].(map[string]any); ok {
					if t, ok := args["text"].(string); ok && t != "" {
						text = t
					}
				}
			}
			b, _ := json.Marshal(text)
			_, _ = os.Stdout.WriteString(`{"jsonrpc":"2.0","id":` + jsonID(id) + `,"result":{"content":[{"type":"text","text":` + string(b) + `}]}}` + "\n")
		default:
			_, _ = os.Stdout.WriteString(`{"jsonrpc":"2.0","id":` + jsonID(id) + `,"error":{"code":-32601,"message":"method not found"}}` + "\n")
		}
	}
	os.Exit(0)
}

func jsonID(v any) string {
	switch x := v.(type) {
	case float64:
		return strconv.FormatInt(int64(x), 10)
	case string:
		b, _ := json.Marshal(x)
		return string(b)
	default:
		return "0"
	}
}

func readAllTraceEvents(t *testing.T, path string) []schema.TraceEventV1 {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open trace: %v", err)
	}
	defer func() { _ = f.Close() }()

	var out []schema.TraceEventV1
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var ev schema.TraceEventV1
		if err := json.Unmarshal(sc.Bytes(), &ev); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		out = append(out, ev)
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("scan: %v", err)
	}
	return out
}
