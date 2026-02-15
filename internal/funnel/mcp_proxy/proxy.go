package mcpproxy

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"sync"
	"time"

	"github.com/marcohefti/zero-context-lab/internal/redact"
	"github.com/marcohefti/zero-context-lab/internal/schema"
	"github.com/marcohefti/zero-context-lab/internal/store"
	"github.com/marcohefti/zero-context-lab/internal/trace"
)

type reqInfo struct {
	start  time.Time
	method string
	input  []byte
}

func Proxy(ctx context.Context, env trace.Env, serverArgv []string, clientIn io.Reader, clientOut io.Writer, maxPreviewBytes int) error {
	if len(serverArgv) == 0 {
		return fmt.Errorf("missing server command argv")
	}
	if maxPreviewBytes < 0 {
		maxPreviewBytes = 0
	}

	cmd := exec.CommandContext(ctx, serverArgv[0], serverArgv[1:]...)
	serverIn, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	serverOut, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	serverErr, err := cmd.StderrPipe()
	if err != nil {
		return err
	}

	if err := cmd.Start(); err != nil {
		return err
	}

	var (
		mu       sync.Mutex
		inflight = map[string]reqInfo{}
	)

	tracePath := filepath.Join(env.OutDirAbs, "tool.calls.jsonl")

	// Drain stderr to avoid deadlocks; currently we don't trace it.
	go func() { _, _ = io.Copy(io.Discard, serverErr) }()

	// Client -> Server (requests)
	reqDone := make(chan struct{})
	go func() {
		defer close(reqDone)
		sc := bufio.NewScanner(clientIn)
		sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
		for sc.Scan() {
			line := bytesTrim(sc.Bytes())
			if len(line) == 0 {
				continue
			}
			// Forward as-is.
			_, _ = serverIn.Write(append(line, '\n'))

			var msg map[string]any
			if err := json.Unmarshal(line, &msg); err != nil {
				continue
			}
			method, _ := msg["method"].(string)
			id := jsonRPCID(msg["id"])
			if id == "" {
				continue
			}
			mu.Lock()
			inflight[id] = reqInfo{start: time.Now(), method: method, input: append([]byte(nil), line...)}
			mu.Unlock()
		}
		_ = serverIn.Close()
	}()

	// Server -> Client (responses)
	sc := bufio.NewScanner(serverOut)
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for sc.Scan() {
		line := bytesTrim(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		_, _ = clientOut.Write(append(line, '\n'))

		var msg map[string]any
		if err := json.Unmarshal(line, &msg); err != nil {
			continue
		}
		id := jsonRPCID(msg["id"])
		if id == "" {
			continue
		}

		mu.Lock()
		info, ok := inflight[id]
		if ok {
			delete(inflight, id)
		}
		mu.Unlock()
		if !ok {
			continue
		}

		op := normalizeMCPMethod(info.method)
		if op == "" {
			continue
		}

		okRes := msg["error"] == nil
		code := ""
		if !okRes {
			code = "MCP_ERROR"
		}

		input := info.input
		if len(input) > maxPreviewBytes {
			input = input[:maxPreviewBytes]
		}
		outPreview := line
		if len(outPreview) > maxPreviewBytes {
			outPreview = outPreview[:maxPreviewBytes]
		}
		outStr, a := redact.Text(string(outPreview))

		duration := time.Since(info.start).Milliseconds()
		ev := schema.TraceEventV1{
			V:         schema.TraceSchemaV1,
			TS:        info.start.UTC().Format(time.RFC3339Nano),
			RunID:     env.RunID,
			SuiteID:   env.SuiteID,
			MissionID: env.MissionID,
			AttemptID: env.AttemptID,
			AgentID:   env.AgentID,
			Tool:      "mcp",
			Op:        op,
			Input:     input,
			Result: schema.TraceResultV1{
				OK:         okRes,
				Code:       code,
				DurationMs: duration,
			},
			IO: schema.TraceIOV1{
				OutBytes:   int64(len(line)),
				ErrBytes:   0,
				OutPreview: outStr,
			},
			RedactionsApplied: a.Names,
		}
		_ = store.AppendJSONL(tracePath, ev)
	}
	if err := sc.Err(); err != nil {
		return err
	}

	<-reqDone
	waitErr := cmd.Wait()
	if waitErr != nil {
		var ee *exec.ExitError
		if errors.As(waitErr, &ee) {
			// Treat server exit as OK if it exited cleanly after EOF; keep error for non-zero.
			if ee.ExitCode() != 0 {
				return waitErr
			}
			return nil
		}
		return waitErr
	}
	return nil
}

func normalizeMCPMethod(method string) string {
	switch method {
	case "initialize":
		return "initialize"
	case "tools/list":
		return "tools/list"
	case "tools/call":
		return "tools/call"
	default:
		return ""
	}
}

func jsonRPCID(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case float64:
		// JSON numbers decode as float64.
		if x == float64(int64(x)) {
			return fmt.Sprintf("%d", int64(x))
		}
		return fmt.Sprintf("%v", x)
	default:
		return ""
	}
}

func bytesTrim(b []byte) []byte {
	i := 0
	j := len(b)
	for i < j {
		c := b[i]
		if c != ' ' && c != '\n' && c != '\t' && c != '\r' {
			break
		}
		i++
	}
	for j > i {
		c := b[j-1]
		if c != ' ' && c != '\n' && c != '\t' && c != '\r' {
			break
		}
		j--
	}
	return b[i:j]
}
