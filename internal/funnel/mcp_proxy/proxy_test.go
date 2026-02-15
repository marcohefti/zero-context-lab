package mcpproxy

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
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
	if len(events) != 3 {
		t.Fatalf("expected 3 trace events, got %d", len(events))
	}
	if events[0].Op != "initialize" || events[1].Op != "tools/list" || events[2].Op != "tools/call" {
		t.Fatalf("unexpected ops: %+v", []string{events[0].Op, events[1].Op, events[2].Op})
	}
	for _, ev := range events {
		if ev.Tool != "mcp" {
			t.Fatalf("unexpected tool: %q", ev.Tool)
		}
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
			_, _ = os.Stdout.WriteString(`{"jsonrpc":"2.0","id":` + jsonID(id) + `,"result":{"content":[{"type":"text","text":"ok"}]}}` + "\n")
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
