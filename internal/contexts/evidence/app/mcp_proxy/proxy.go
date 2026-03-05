package mcpproxy

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/marcohefti/zero-context-lab/internal/kernel/artifacts"
	"io"
	"os/exec"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/marcohefti/zero-context-lab/internal/contexts/evidence/app/redact"
	"github.com/marcohefti/zero-context-lab/internal/contexts/evidence/app/trace"
	"github.com/marcohefti/zero-context-lab/internal/kernel/schema"
	"github.com/marcohefti/zero-context-lab/internal/kernel/store"
)

type reqInfo struct {
	start          time.Time
	method         string
	input          json.RawMessage
	inputTruncated bool
	wait           chan struct{}
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
	return ProxyWithOptions(ctx, env, serverArgv, clientIn, clientOut, Options{
		MaxPreviewBytes: maxPreviewBytes,
	})
}

type Options struct {
	MaxPreviewBytes    int
	MaxToolCalls       int64
	IdleTimeoutMs      int64
	ShutdownOnComplete bool
	SequentialRequests bool
}

type proxySession struct {
	cmd       *exec.Cmd
	serverIn  io.WriteCloser
	serverOut io.ReadCloser
	serverErr io.ReadCloser
}

type proxyRuntimeState struct {
	mu       sync.Mutex
	inflight map[string]reqInfo

	activityMu    sync.Mutex
	lastActivity  time.Time
	idleTimedOut  atomic.Bool
	maxCallsHit   atomic.Bool
	forcedStop    atomic.Bool
	toolCallsSeen int64
}

type trackedResponse struct {
	info reqInfo
	msg  map[string]any
}

func ProxyWithOptions(ctx context.Context, env trace.Env, serverArgv []string, clientIn io.Reader, clientOut io.Writer, opts Options) error {
	if len(serverArgv) == 0 {
		return fmt.Errorf("missing server command argv")
	}
	opts, maxPreviewBytes := normalizeProxyOptions(opts)
	proxyCtx, cancelProxy := newProxyContext(ctx, opts)
	defer cancelProxy()

	session, err := startProxySession(proxyCtx, serverArgv)
	if err != nil {
		return err
	}
	waited := false
	defer func() {
		if waited {
			return
		}
		// Ensure we don't leak the child process on early returns (e.g., trace write failures).
		_ = session.cmd.Process.Kill()
		_ = session.cmd.Wait()
	}()

	state := newProxyRuntimeState()
	startIdleTimeoutWatcher(proxyCtx, opts.IdleTimeoutMs, state, cancelProxy)

	tracePath := filepath.Join(env.OutDirAbs, artifacts.ToolCallsJSONL)
	redServerArgv, argvApplied := redactStrings(serverArgv)
	if err := appendSpawnTraceEvent(tracePath, env, redServerArgv, argvApplied); err != nil {
		return err
	}

	errCap := startStderrCapture(session.serverErr, maxPreviewBytes)
	reqDone := startRequestForwarder(proxyCtx, clientIn, session.serverIn, opts, state)
	if err := processServerResponses(proxyCtx, session.serverOut, clientOut, tracePath, env, maxPreviewBytes, opts, state, reqDone, cancelProxy); err != nil {
		return err
	}

	waitErr := waitForProxyExit(proxyCtx, reqDone, session.cmd)
	waited = true

	if err := appendPostRunEvents(tracePath, env, redServerArgv, argvApplied, maxPreviewBytes, proxyCtx, errCap, state); err != nil {
		return err
	}

	return finalizeProxyResult(waitErr, state)
}

func normalizeProxyOptions(opts Options) (Options, int) {
	maxPreviewBytes := opts.MaxPreviewBytes
	if maxPreviewBytes < 0 {
		maxPreviewBytes = 0
	}
	if opts.MaxToolCalls < 0 {
		opts.MaxToolCalls = 0
	}
	if opts.IdleTimeoutMs < 0 {
		opts.IdleTimeoutMs = 0
	}
	return opts, maxPreviewBytes
}

func newProxyContext(ctx context.Context, opts Options) (context.Context, context.CancelFunc) {
	if opts.MaxToolCalls <= 0 && opts.IdleTimeoutMs <= 0 && !opts.ShutdownOnComplete {
		return ctx, func() {}
	}
	return context.WithCancel(ctx)
}

func startProxySession(ctx context.Context, serverArgv []string) (proxySession, error) {
	cmd := exec.CommandContext(ctx, serverArgv[0], serverArgv[1:]...)
	serverIn, err := cmd.StdinPipe()
	if err != nil {
		return proxySession{}, err
	}
	serverOut, err := cmd.StdoutPipe()
	if err != nil {
		return proxySession{}, err
	}
	serverErr, err := cmd.StderrPipe()
	if err != nil {
		return proxySession{}, err
	}
	if err := cmd.Start(); err != nil {
		return proxySession{}, err
	}
	return proxySession{
		cmd:       cmd,
		serverIn:  serverIn,
		serverOut: serverOut,
		serverErr: serverErr,
	}, nil
}

func newProxyRuntimeState() *proxyRuntimeState {
	s := &proxyRuntimeState{inflight: map[string]reqInfo{}}
	s.touch()
	return s
}

func (s *proxyRuntimeState) touch() {
	s.activityMu.Lock()
	s.lastActivity = time.Now()
	s.activityMu.Unlock()
}

func (s *proxyRuntimeState) idleDuration() time.Duration {
	s.activityMu.Lock()
	defer s.activityMu.Unlock()
	return time.Since(s.lastActivity)
}

func (s *proxyRuntimeState) setInflight(id string, info reqInfo) {
	s.mu.Lock()
	s.inflight[id] = info
	s.mu.Unlock()
}

func (s *proxyRuntimeState) popInflight(id string) (reqInfo, bool) {
	s.mu.Lock()
	info, ok := s.inflight[id]
	if ok {
		delete(s.inflight, id)
	}
	s.mu.Unlock()
	return info, ok
}

func (s *proxyRuntimeState) inflightCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.inflight)
}

func (s *proxyRuntimeState) incToolCallsSeen() int64 {
	s.toolCallsSeen++
	return s.toolCallsSeen
}

func startIdleTimeoutWatcher(proxyCtx context.Context, idleTimeoutMs int64, state *proxyRuntimeState, cancelProxy context.CancelFunc) {
	if idleTimeoutMs <= 0 {
		return
	}
	go func() {
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
		limit := time.Duration(idleTimeoutMs) * time.Millisecond
		for {
			select {
			case <-proxyCtx.Done():
				return
			case <-ticker.C:
				if state.idleDuration() < limit {
					continue
				}
				state.idleTimedOut.Store(true)
				cancelProxy()
				return
			}
		}
	}()
}

func appendSpawnTraceEvent(tracePath string, env trace.Env, redServerArgv, argvApplied []string) error {
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
		Op:        "spawn",
		Input:     inRaw,
		Result: schema.TraceResultV1{
			OK:         true,
			DurationMs: 0,
		},
		IO: schema.TraceIOV1{
			OutBytes: 0,
			ErrBytes: 0,
		},
		RedactionsApplied: argvApplied,
	}
	return store.AppendJSONL(tracePath, ev)
}

func startStderrCapture(serverErr io.Reader, maxPreviewBytes int) *boundedCapture {
	errCap := &boundedCapture{max: maxPreviewBytes}
	// Drain stderr to avoid deadlocks; capture a bounded preview for evidence.
	go func() { _, _ = io.Copy(errCap, serverErr) }()
	return errCap
}

func startRequestForwarder(proxyCtx context.Context, clientIn io.Reader, serverIn io.WriteCloser, opts Options, state *proxyRuntimeState) <-chan struct{} {
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
			state.touch()
			wait := trackInflightRequest(line, opts, state)
			_, _ = serverIn.Write(append(line, '\n'))
			if wait == nil {
				continue
			}
			select {
			case <-wait:
			case <-proxyCtx.Done():
				return
			}
		}
		_ = serverIn.Close()
	}()
	return reqDone
}

func trackInflightRequest(line []byte, opts Options, state *proxyRuntimeState) chan struct{} {
	var msg map[string]any
	if err := json.Unmarshal(line, &msg); err != nil {
		return nil
	}
	method, _ := msg["method"].(string)
	id := jsonRPCID(msg["id"])
	if id == "" {
		return nil
	}
	inputRaw, truncated := boundedJSONRPCInput(msg, schema.ToolInputMaxBytesV1)
	var wait chan struct{}
	if opts.SequentialRequests {
		wait = make(chan struct{})
	}
	state.setInflight(id, reqInfo{
		start:          time.Now(),
		method:         method,
		input:          inputRaw,
		inputTruncated: truncated,
		wait:           wait,
	})
	return wait
}

func processServerResponses(
	proxyCtx context.Context,
	serverOut io.Reader,
	clientOut io.Writer,
	tracePath string,
	env trace.Env,
	maxPreviewBytes int,
	opts Options,
	state *proxyRuntimeState,
	reqDone <-chan struct{},
	cancelProxy context.CancelFunc,
) error {
	sc := bufio.NewScanner(serverOut)
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for sc.Scan() {
		stop, err := handleServerResponseLine(sc.Bytes(), clientOut, tracePath, env, maxPreviewBytes, opts, state, reqDone, cancelProxy)
		if err != nil {
			return err
		}
		if stop {
			break
		}
	}
	return sc.Err()
}

func handleServerResponseLine(
	rawLine []byte,
	clientOut io.Writer,
	tracePath string,
	env trace.Env,
	maxPreviewBytes int,
	opts Options,
	state *proxyRuntimeState,
	reqDone <-chan struct{},
	cancelProxy context.CancelFunc,
) (bool, error) {
	line := bytesTrim(rawLine)
	if len(line) == 0 {
		return false, nil
	}
	state.touch()
	_, _ = clientOut.Write(append(line, '\n'))

	resp, ok := parseTrackedResponse(line, state)
	if !ok {
		return false, nil
	}
	ev, op := buildResponseTraceEvent(env, resp, line, maxPreviewBytes)
	if err := store.AppendJSONL(tracePath, ev); err != nil {
		return false, err
	}
	if shouldStopAfterResponse(op, opts, state, reqDone, cancelProxy) {
		return true, nil
	}
	return false, nil
}

func parseTrackedResponse(line []byte, state *proxyRuntimeState) (trackedResponse, bool) {
	var msg map[string]any
	if err := json.Unmarshal(line, &msg); err != nil {
		return trackedResponse{}, false
	}
	id := jsonRPCID(msg["id"])
	if id == "" {
		return trackedResponse{}, false
	}
	info, ok := state.popInflight(id)
	if !ok {
		return trackedResponse{}, false
	}
	if info.wait != nil {
		close(info.wait)
	}
	return trackedResponse{info: info, msg: msg}, true
}

func buildResponseTraceEvent(env trace.Env, resp trackedResponse, line []byte, maxPreviewBytes int) (schema.TraceEventV1, string) {
	op, unknownMethod, okRes, code, enrichment := deriveResponseOutcome(resp.info.method, resp.msg)
	input, inApplied, inCapped := redactTraceInput(resp.info.input)
	outStr, outApplied, outTruncated, outCapped := redactTraceOutput(line, maxPreviewBytes)
	ev := schema.TraceEventV1{
		V:         schema.TraceSchemaV1,
		TS:        resp.info.start.UTC().Format(time.RFC3339Nano),
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
			DurationMs: time.Since(resp.info.start).Milliseconds(),
		},
		IO: schema.TraceIOV1{
			OutBytes:   int64(len(line)),
			ErrBytes:   0,
			OutPreview: outStr,
		},
		RedactionsApplied: unionStrings(inApplied, outApplied),
		Warnings:          responseWarnings(resp.info.inputTruncated, inCapped, unknownMethod),
		Integrity: &schema.TraceIntegrityV1{
			Truncated: resp.info.inputTruncated || inCapped || outTruncated || outCapped,
		},
	}
	appendResponseEnrichment(&ev, enrichment)
	return ev, op
}

func deriveResponseOutcome(method string, msg map[string]any) (string, bool, bool, string, any) {
	op := normalizeMCPMethod(method)
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
		em, ok := msg["error"].(map[string]any)
		if ok {
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
		enrichment = withMCPMethod(enrichment, method)
	}
	return op, unknownMethod, okRes, code, enrichment
}

func withMCPMethod(enrichment any, method string) any {
	if enrichment == nil {
		enrichment = map[string]any{}
	}
	if m, ok := enrichment.(map[string]any); ok {
		m["mcpMethod"] = method
	}
	return enrichment
}

func redactTraceInput(input json.RawMessage) ([]byte, []string, bool) {
	inStr, inApplied := redact.Text(string(input))
	out := []byte(inStr)
	if len(out) <= schema.ToolInputMaxBytesV1 {
		return out, inApplied.Names, false
	}
	// Fall back to a minimal shape rather than emitting invalid JSON.
	return []byte(`{"method":"[TRUNCATED]"}`), inApplied.Names, true
}

func redactTraceOutput(line []byte, maxPreviewBytes int) (string, []string, bool, bool) {
	outPreview := line
	outTruncated := false
	if len(outPreview) > maxPreviewBytes {
		outPreview = outPreview[:maxPreviewBytes]
		outTruncated = true
	}
	outStr, a := redact.Text(string(outPreview))
	outStr, outCapped := capStringBytes(outStr, maxPreviewBytes)
	return outStr, a.Names, outTruncated, outCapped
}

func responseWarnings(inputTruncated, inputCapped, unknownMethod bool) []schema.TraceWarningV1 {
	var w []schema.TraceWarningV1
	if inputTruncated || inputCapped {
		w = append(w, schema.TraceWarningV1{Code: "ZCL_W_INPUT_TRUNCATED", Message: "tool input truncated to fit bounds"})
	}
	if unknownMethod {
		w = append(w, schema.TraceWarningV1{Code: "ZCL_W_MCP_UNKNOWN_METHOD", Message: "unrecognized MCP method; recorded as op=unknown"})
	}
	if len(w) == 0 {
		return nil
	}
	return w
}

func appendResponseEnrichment(ev *schema.TraceEventV1, enrichment any) {
	if enrichment == nil {
		return
	}
	b, err := store.CanonicalJSON(enrichment)
	if err != nil {
		return
	}
	if len(b) <= schema.EnrichmentMaxBytesV1 {
		ev.Enrichment = b
		return
	}
	ev.Warnings = append(ev.Warnings, schema.TraceWarningV1{Code: "ZCL_W_ENRICHMENT_TRUNCATED", Message: "trace enrichment omitted to fit bounds"})
	if ev.Integrity == nil {
		ev.Integrity = &schema.TraceIntegrityV1{}
	}
	ev.Integrity.Truncated = true
}

func shouldStopAfterResponse(op string, opts Options, state *proxyRuntimeState, reqDone <-chan struct{}, cancelProxy context.CancelFunc) bool {
	if op == "tools/call" && opts.MaxToolCalls > 0 {
		if state.incToolCallsSeen() > opts.MaxToolCalls {
			state.maxCallsHit.Store(true)
			cancelProxy()
			return true
		}
	}
	if !opts.ShutdownOnComplete || state.inflightCount() != 0 {
		return false
	}
	select {
	case <-reqDone:
		state.forcedStop.Store(true)
		cancelProxy()
		return false
	default:
		return false
	}
}

func waitForProxyExit(proxyCtx context.Context, reqDone <-chan struct{}, cmd *exec.Cmd) error {
	select {
	case <-reqDone:
	case <-proxyCtx.Done():
	}
	return cmd.Wait()
}

func appendPostRunEvents(
	tracePath string,
	env trace.Env,
	redServerArgv []string,
	argvApplied []string,
	maxPreviewBytes int,
	proxyCtx context.Context,
	errCap *boundedCapture,
	state *proxyRuntimeState,
) error {
	if err := appendTimeoutTraceEvent(tracePath, env, redServerArgv, argvApplied, proxyCtx, state.idleTimedOut.Load()); err != nil {
		return err
	}
	if err := appendMaxToolCallsTraceEvent(tracePath, env, redServerArgv, argvApplied, state.maxCallsHit.Load()); err != nil {
		return err
	}
	return appendStderrTraceEvent(tracePath, env, redServerArgv, argvApplied, maxPreviewBytes, errCap)
}

func appendTimeoutTraceEvent(tracePath string, env trace.Env, redServerArgv, argvApplied []string, proxyCtx context.Context, idleTimedOut bool) error {
	if !errors.Is(proxyCtx.Err(), context.DeadlineExceeded) && !idleTimedOut {
		return nil
	}
	op := "timeout"
	msg := "attempt deadline exceeded"
	if idleTimedOut {
		op = "idle-timeout"
		msg = "mcp idle timeout exceeded"
	}
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
		Op:        op,
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
		Warnings: []schema.TraceWarningV1{{
			Code:    "ZCL_W_MCP_TIMEOUT",
			Message: msg,
		}},
	}
	return store.AppendJSONL(tracePath, ev)
}

func appendMaxToolCallsTraceEvent(tracePath string, env trace.Env, redServerArgv, argvApplied []string, maxCallsHit bool) error {
	if !maxCallsHit {
		return nil
	}
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
		Op:        "limit",
		Input:     inRaw,
		Result: schema.TraceResultV1{
			OK:         false,
			Code:       "ZCL_E_MCP_MAX_TOOL_CALLS",
			DurationMs: 0,
		},
		IO: schema.TraceIOV1{
			OutBytes: 0,
			ErrBytes: 0,
		},
		RedactionsApplied: argvApplied,
		Warnings: []schema.TraceWarningV1{{
			Code:    "ZCL_W_MCP_MAX_TOOL_CALLS",
			Message: "mcp max tool calls reached",
		}},
	}
	return store.AppendJSONL(tracePath, ev)
}

func appendStderrTraceEvent(
	tracePath string,
	env trace.Env,
	redServerArgv, argvApplied []string,
	maxPreviewBytes int,
	errCap *boundedCapture,
) error {
	prev, total, trunc := errCap.snapshot()
	if total == 0 && prev == "" {
		return nil
	}
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
	return store.AppendJSONL(tracePath, ev)
}

func finalizeProxyResult(waitErr error, state *proxyRuntimeState) error {
	if waitErr != nil {
		if state.forcedStop.Load() && !state.maxCallsHit.Load() && !state.idleTimedOut.Load() {
			return nil
		}
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
	if state.maxCallsHit.Load() {
		return fmt.Errorf("ZCL_E_MCP_MAX_TOOL_CALLS: reached configured max tool calls")
	}
	if state.idleTimedOut.Load() {
		return fmt.Errorf("ZCL_E_TIMEOUT: mcp idle timeout exceeded")
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

func boundedJSONRPCInput(msg map[string]any, maxBytes int) (json.RawMessage, bool) {
	if maxBytes < 0 {
		maxBytes = 0
	}
	method, _ := msg["method"].(string)
	id := msg["id"]
	params := msg["params"]

	candidates := []map[string]any{
		{"method": method, "id": id, "params": params},
		{"method": method, "id": id},
		{"method": method},
	}

	for i, c := range candidates {
		b, err := store.CanonicalJSON(c)
		if err != nil {
			continue
		}
		if len(b) <= maxBytes {
			return b, i != 0
		}
	}

	// Last resort: always valid JSON.
	return json.RawMessage(`{}`), true
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
