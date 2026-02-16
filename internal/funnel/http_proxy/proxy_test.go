package httpproxy

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/marcohefti/zero-context-lab/internal/schema"
	"github.com/marcohefti/zero-context-lab/internal/trace"
)

func TestProxy_ForwardsAndEmitsTrace(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/hello" || r.URL.RawQuery != "q=1" {
			http.Error(w, "bad path", http.StatusBadRequest)
			return
		}
		w.Header().Set("X-Upstream", "1")
		_, _ = w.Write([]byte("ok\n"))
	}))
	defer up.Close()

	outDir := t.TempDir()
	env := trace.Env{
		RunID:     "20260215-180012Z-09c5a6",
		SuiteID:   "heftiweb-smoke",
		MissionID: "latest-blog-title",
		AttemptID: "001-latest-blog-title-r1",
		OutDirAbs: outDir,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	h, err := Start(ctx, env, "127.0.0.1:0", up.URL, 1024, 1)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = h.Close() }()

	resp, err := http.Get("http://" + h.ListenAddr + "/hello?q=1")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.Header.Get("X-Upstream") != "1" {
		t.Fatalf("missing forwarded header")
	}

	b, _ := io.ReadAll(resp.Body)
	if string(b) != "ok\n" {
		t.Fatalf("unexpected response body: %q", string(b))
	}

	ev := readSingleTraceEvent(t, filepath.Join(outDir, "tool.calls.jsonl"))
	if ev.Tool != "http" || ev.Op != "request" {
		t.Fatalf("unexpected tool/op: %+v", ev)
	}
	if ev.Result.OK != true {
		t.Fatalf("expected ok=true, got: %+v", ev.Result)
	}
	if !strings.Contains(ev.IO.OutPreview, "ok") {
		t.Fatalf("expected outPreview to include body, got: %q", ev.IO.OutPreview)
	}

	if err := h.Wait(); err != nil {
		t.Fatalf("wait: %v", err)
	}
}

func readSingleTraceEvent(t *testing.T, path string) schema.TraceEventV1 {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open trace: %v", err)
	}
	defer func() { _ = f.Close() }()

	sc := bufio.NewScanner(f)
	if !sc.Scan() {
		t.Fatalf("expected one trace line")
	}
	line := sc.Bytes()
	if err := sc.Err(); err != nil {
		t.Fatalf("scan: %v", err)
	}

	var ev schema.TraceEventV1
	if err := json.Unmarshal(line, &ev); err != nil {
		t.Fatalf("unmarshal trace: %v", err)
	}
	return ev
}
