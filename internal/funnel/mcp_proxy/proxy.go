package mcpproxy

import (
	"bufio"
	"bytes"
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

type boundedCapture struct {
	max       int
	buf       bytes.Buffer
	total     int64
	truncated bool
	mu        sync.Mutex
}

func (c *boundedCapture) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.total += int64(len(p))
	remaining := c.max - c.buf.Len()
	if remaining <= 0 {
		c.truncated = true
		return len(p), nil
	}
	if len(p) > remaining {
		_, _ = c.buf.Write(p[:remaining])
		c.truncated = true
		return len(p), nil
	}
	_, _ = c.buf.Write(p)
	return len(p), nil
}

func (c *boundedCapture) snapshot() (preview string, total int64, truncated bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.buf.String(), c.total, c.truncated
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
	waited := false
	defer func() {
		if waited {
			return
		}
		// Ensure we don't leak the child process on early returns (e.g., trace write failures).
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	}()

	var (
		mu       sync.Mutex
		inflight = map[string]reqInfo{}
	)

	tracePath := filepath.Join(env.OutDirAbs, "tool.calls.jsonl")
	redServerArgv, argvApplied := redactStrings(serverArgv)

	var errCap boundedCapture
	errCap.max = maxPreviewBytes
	// Drain stderr to avoid deadlocks; capture a bounded preview for evidence.
	go func() { _, _ = io.Copy(&errCap, serverErr) }()

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
		unknownMethod := false
		if op == "" {
			op = "unknown"
			unknownMethod = true
		}

		okRes := msg["error"] == nil
		code := ""
		var enrichment any
		if !okRes {
			code = "MCP_ERROR"
			if em, ok := msg["error"].(map[string]any); ok {
				if c, ok := em["code"].(float64); ok {
					code = fmt.Sprintf("MCP_%d", int64(c))
				}
				enrichment = map[string]any{
					"mcpError": map[string]any{
						"code":    em["code"],
						"message": em["message"],
					},
				}
			}
		}
		if unknownMethod {
			if enrichment == nil {
				enrichment = map[string]any{}
			}
			if m, ok := enrichment.(map[string]any); ok {
				m["mcpMethod"] = info.method
			}
		}

		input := info.input
		inputTruncated := false
		// Tool input is bounded by ToolInputMaxBytesV1 (independent of preview caps).
		inputMax := schema.ToolInputMaxBytesV1
		if len(input) > inputMax {
			input = input[:inputMax]
			inputTruncated = true
		}

		inStr, inApplied := redact.Text(string(input))
		inStr, inCapped := capStringBytes(inStr, inputMax)
		input = []byte(inStr)

		outPreview := line
		outTruncated := false
		if len(outPreview) > maxPreviewBytes {
			outPreview = outPreview[:maxPreviewBytes]
			outTruncated = true
		}
		outStr, a := redact.Text(string(outPreview))
		outStr, outCapped := capStringBytes(outStr, maxPreviewBytes)

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
			RedactionsApplied: unionStrings(inApplied.Names, a.Names),
			Warnings: func() []schema.TraceWarningV1 {
				if !unknownMethod {
					return nil
				}
				return []schema.TraceWarningV1{{Code: "ZCL_W_MCP_UNKNOWN_METHOD", Message: "unrecognized MCP method; recorded as op=unknown"}}
			}(),
			Integrity: &schema.TraceIntegrityV1{
				Truncated: inputTruncated || inCapped || outTruncated || outCapped,
			},
		}
		if enrichment != nil {
			if b, err := store.CanonicalJSON(enrichment); err == nil {
				ev.Enrichment = b
			}
		}
		if err := store.AppendJSONL(tracePath, ev); err != nil {
			return err
		}
	}
	if err := sc.Err(); err != nil {
		return err
	}

	<-reqDone
	waitErr := cmd.Wait()
	waited = true

	// If the attempt has a deadline and we hit it, record a timeout event.
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		in := map[string]any{"argv": redServerArgv}
		inRaw, _ := store.CanonicalJSON(in)
		ev := schema.TraceEventV1{
			V:         schema.TraceSchemaV1,
			TS:        time.Now().UTC().Format(time.RFC3339Nano),
			RunID:     env.RunID,
			SuiteID:   env.SuiteID,
			MissionID: env.MissionID,
			AttemptID: env.AttemptID,
			AgentID:   env.AgentID,
			Tool:      "mcp",
			Op:        "timeout",
			Input:     inRaw,
			Result: schema.TraceResultV1{
				OK:         false,
				Code:       "ZCL_E_TIMEOUT",
				DurationMs: 0,
			},
			IO: schema.TraceIOV1{
				OutBytes: 0,
				ErrBytes: 0,
			},
			RedactionsApplied: argvApplied,
		}
		if err := store.AppendJSONL(tracePath, ev); err != nil {
			return err
		}
	}

	// Emit one stderr evidence event at the end (bounded).
	if prev, total, trunc := errCap.snapshot(); total > 0 || prev != "" {
		prevRed, applied := redact.Text(prev)
		prevRed, capped := capStringBytes(prevRed, maxPreviewBytes)

		in := map[string]any{"argv": redServerArgv}
		inRaw, _ := store.CanonicalJSON(in)
		ev := schema.TraceEventV1{
			V:         schema.TraceSchemaV1,
			TS:        time.Now().UTC().Format(time.RFC3339Nano),
			RunID:     env.RunID,
			SuiteID:   env.SuiteID,
			MissionID: env.MissionID,
			AttemptID: env.AttemptID,
			AgentID:   env.AgentID,
			Tool:      "mcp",
			Op:        "stderr",
			Input:     inRaw,
			Result: schema.TraceResultV1{
				OK:         true,
				DurationMs: 0,
			},
			IO: schema.TraceIOV1{
				OutBytes:   0,
				ErrBytes:   total,
				ErrPreview: prevRed,
			},
			RedactionsApplied: unionStrings(argvApplied, applied.Names),
			Integrity: &schema.TraceIntegrityV1{
				Truncated: trunc || capped,
			},
		}
		if trunc || capped {
			ev.Warnings = []schema.TraceWarningV1{{Code: "ZCL_W_STDERR_TRUNCATED", Message: "mcp server stderr preview truncated to fit bounds"}}
		}
		if err := store.AppendJSONL(tracePath, ev); err != nil {
			return err
		}
	}

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

func capStringBytes(s string, maxBytes int) (string, bool) {
	if maxBytes < 0 {
		maxBytes = 0
	}
	if len(s) <= maxBytes {
		return s, false
	}
	return s[:maxBytes], true
}

func unionStrings(parts ...[]string) []string {
	out := make([]string, 0, 4)
	seen := map[string]bool{}
	for _, p := range parts {
		for _, s := range p {
			if s == "" || seen[s] {
				continue
			}
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

func redactStrings(in []string) ([]string, []string) {
	if len(in) == 0 {
		return nil, nil
	}
	out := make([]string, 0, len(in))
	var applied []string
	for _, s := range in {
		red, a := redact.Text(s)
		out = append(out, red)
		applied = append(applied, a.Names...)
	}
	return out, unionStrings(applied)
}
