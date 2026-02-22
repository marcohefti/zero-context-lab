package codexappserver

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/marcohefti/zero-context-lab/internal/native"
)

const (
	defaultStartupTimeout  = 8 * time.Second
	defaultRequestTimeout  = 10 * time.Second
	defaultShutdownTimeout = 3 * time.Second
)

type Config struct {
	Command []string

	StartupTimeout  time.Duration
	RequestTimeout  time.Duration
	ShutdownTimeout time.Duration

	ProtocolContract native.ProtocolContract
	EnvPolicy        native.EnvPolicy
	ExtraEnv         map[string]string
}

type Runtime struct {
	cfg Config
}

func NewRuntime(cfg Config) *Runtime {
	if cfg.StartupTimeout <= 0 {
		cfg.StartupTimeout = defaultStartupTimeout
	}
	if cfg.RequestTimeout <= 0 {
		cfg.RequestTimeout = defaultRequestTimeout
	}
	if cfg.ShutdownTimeout <= 0 {
		cfg.ShutdownTimeout = defaultShutdownTimeout
	}
	if len(cfg.Command) == 0 {
		cfg.Command = []string{"codex", "app-server", "--listen", "stdio://"}
	}
	if cfg.ProtocolContract.MinimumProtocolMajor <= 0 {
		cfg.ProtocolContract = native.ProtocolContract{
			RuntimeName:           "codex_app_server",
			MinimumProtocolMajor:  2,
			MinimumProtocolMinor:  0,
			MinimumRuntimeVersion: "",
		}
	}
	if len(cfg.EnvPolicy.AllowedExact) == 0 && len(cfg.EnvPolicy.AllowedPrefixes) == 0 {
		cfg.EnvPolicy = native.DefaultEnvPolicy()
	}
	return &Runtime{cfg: cfg}
}

func (r *Runtime) ID() native.StrategyID {
	return native.StrategyCodexAppServer
}

func (r *Runtime) Capabilities() native.Capabilities {
	return native.Capabilities{
		SupportsThreadStart:      true,
		SupportsTurnSteer:        true,
		SupportsInterrupt:        true,
		SupportsEventStream:      true,
		SupportsParallelSessions: true,
	}
}

func (r *Runtime) Probe(ctx context.Context) error {
	if len(r.cfg.Command) == 0 {
		return native.NewError(native.ErrorStartup, "codex app-server command is empty")
	}
	bin := strings.TrimSpace(r.cfg.Command[0])
	if bin == "" {
		return native.NewError(native.ErrorStartup, "codex app-server binary is empty")
	}
	if strings.ContainsRune(bin, os.PathSeparator) {
		if _, err := os.Stat(bin); err != nil {
			return native.WrapError(native.ErrorStartup, "codex app-server binary is not accessible", err)
		}
		return nil
	}
	if _, err := exec.LookPath(bin); err != nil {
		return native.WrapError(native.ErrorStartup, "codex app-server binary is not on PATH", err)
	}
	_ = ctx
	return nil
}

func (r *Runtime) StartSession(ctx context.Context, opts native.SessionOptions) (native.Session, error) {
	if err := r.Probe(ctx); err != nil {
		return nil, err
	}

	startupCtx, cancel := context.WithTimeout(ctx, r.cfg.StartupTimeout)
	defer cancel()

	cmd := exec.CommandContext(context.Background(), r.cfg.Command[0], r.cfg.Command[1:]...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, native.WrapError(native.ErrorTransport, "open app-server stdin pipe", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, native.WrapError(native.ErrorTransport, "open app-server stdout pipe", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, native.WrapError(native.ErrorTransport, "open app-server stderr pipe", err)
	}

	mergedEnv, blocked, err := buildRuntimeEnv(r.cfg.EnvPolicy, opts.Env, r.cfg.ExtraEnv)
	if err != nil {
		return nil, err
	}
	if blockedExplicitVars(blocked, opts.Env, r.cfg.ExtraEnv) {
		safe := strings.Join(blocked, ",")
		return nil, native.NewError(native.ErrorEnvPolicy, "runtime env policy blocked explicitly configured variables: "+safe)
	}
	cmd.Env = mapToEnviron(mergedEnv)

	if err := cmd.Start(); err != nil {
		return nil, native.WrapError(native.ErrorStartup, "start codex app-server process", err)
	}

	s := &session{
		runtimeID:       native.StrategyCodexAppServer,
		requestTimeout:  r.cfg.RequestTimeout,
		shutdownTimeout: r.cfg.ShutdownTimeout,
		cmd:             cmd,
		stdin:           stdin,
		stdout:          stdout,
		stderr:          stderr,
		done:            make(chan struct{}),
		pending:         map[string]chan rpcResponse{},
		listeners:       map[string]native.EventListener{},
	}

	go s.readLoop()
	go io.Copy(io.Discard, stderr)

	initCtx, initCancel := context.WithTimeout(startupCtx, r.cfg.StartupTimeout)
	defer initCancel()
	if err := s.initialize(initCtx); err != nil {
		_ = s.Close(context.Background())
		return nil, err
	}
	ua, err := s.initializeResponseUserAgent()
	if err != nil {
		_ = s.Close(context.Background())
		return nil, err
	}
	if err := s.compatibilityProbe(initCtx, r.cfg.ProtocolContract, ua); err != nil {
		_ = s.Close(context.Background())
		return nil, err
	}
	native.RecordHealth(native.StrategyCodexAppServer, native.HealthSessionStart)
	return s, nil
}

type session struct {
	runtimeID native.StrategyID

	requestTimeout  time.Duration
	shutdownTimeout time.Duration

	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout io.ReadCloser
	stderr io.ReadCloser

	writeMu sync.Mutex

	pendingMu sync.Mutex
	pending   map[string]chan rpcResponse

	listenerMu      sync.RWMutex
	listeners       map[string]native.EventListener
	listenerCounter uint64

	reqCounter uint64

	threadID string

	initUserAgent string

	closing atomic.Bool

	terminalErrMu sync.RWMutex
	terminalErr   error
	done          chan struct{}
	doneOnce      sync.Once
}

func (s *session) RuntimeID() native.StrategyID {
	return s.runtimeID
}

func (s *session) SessionID() string {
	return fmt.Sprintf("pid:%d", s.cmd.Process.Pid)
}

func (s *session) ThreadID() string {
	return strings.TrimSpace(s.threadID)
}

func (s *session) AddListener(listener native.EventListener) (string, error) {
	if listener == nil {
		return "", native.NewError(native.ErrorProtocol, "listener is nil")
	}
	id := strconv.FormatUint(atomic.AddUint64(&s.listenerCounter, 1), 10)
	s.listenerMu.Lock()
	s.listeners[id] = listener
	s.listenerMu.Unlock()
	return id, nil
}

func (s *session) RemoveListener(listenerID string) error {
	listenerID = strings.TrimSpace(listenerID)
	if listenerID == "" {
		return native.NewError(native.ErrorProtocol, "listener id is empty")
	}
	s.listenerMu.Lock()
	delete(s.listeners, listenerID)
	s.listenerMu.Unlock()
	return nil
}

func (s *session) Close(ctx context.Context) error {
	if s == nil {
		return nil
	}
	if s.closing.CompareAndSwap(false, true) {
		defer native.RecordHealth(s.runtimeID, native.HealthSessionClosed)
		s.listenerMu.Lock()
		s.listeners = map[string]native.EventListener{}
		s.listenerMu.Unlock()
		_ = s.stdin.Close()
	}

	waitCh := make(chan error, 1)
	go func() {
		waitCh <- s.cmd.Wait()
	}()

	timeout := s.shutdownTimeout
	if deadline, ok := ctx.Deadline(); ok {
		remaining := time.Until(deadline)
		if remaining > 0 && remaining < timeout {
			timeout = remaining
		}
	}
	if timeout <= 0 {
		timeout = s.shutdownTimeout
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()

	select {
	case err := <-waitCh:
		s.signalDone(err)
		if err == nil {
			return nil
		}
		if isExitError(err) {
			return nil
		}
		return native.WrapError(native.ErrorTransport, "wait for codex app-server shutdown", err)
	case <-timer.C:
		_ = s.cmd.Process.Kill()
		select {
		case err := <-waitCh:
			s.signalDone(err)
			if err == nil || isExitError(err) {
				return native.NewError(native.ErrorTimeout, "forced codex app-server teardown on shutdown timeout")
			}
			return native.WrapError(native.ErrorTimeout, "forced codex app-server teardown after shutdown timeout", err)
		case <-time.After(750 * time.Millisecond):
			return native.NewError(native.ErrorTimeout, "codex app-server did not exit after forced teardown")
		}
	case <-ctx.Done():
		return native.WrapError(native.ErrorTimeout, "shutdown context cancelled", ctx.Err())
	}
}

func (s *session) initialize(ctx context.Context) error {
	res, err := s.call(ctx, "initialize", map[string]any{
		"clientInfo": map[string]any{
			"name":    "zcl",
			"title":   "Zero Context Lab",
			"version": "1",
		},
		"capabilities": map[string]any{
			"optOutNotificationMethods": []string{},
		},
	})
	if err != nil {
		return err
	}
	var parsed struct {
		UserAgent string `json:"userAgent"`
	}
	if err := json.Unmarshal(res, &parsed); err != nil {
		return native.WrapError(native.ErrorProtocol, "decode initialize response", err)
	}
	s.initUserAgent = strings.TrimSpace(parsed.UserAgent)
	if _, err := s.notify(ctx, "initialized", map[string]any{}); err != nil {
		return err
	}
	return nil
}

func (s *session) initializeResponseUserAgent() (string, error) {
	ua := strings.TrimSpace(s.initUserAgent)
	if ua == "" {
		return "", native.NewError(native.ErrorProtocol, "initialize response missing userAgent")
	}
	return ua, nil
}

func (s *session) compatibilityProbe(ctx context.Context, contract native.ProtocolContract, userAgent string) error {
	probeCtx, cancel := context.WithTimeout(ctx, s.requestTimeout)
	defer cancel()
	_, err := s.call(probeCtx, "model/list", map[string]any{})
	if err != nil {
		nerr, ok := native.AsError(err)
		if ok && nerr.Kind == native.ErrorProtocol {
			msg := strings.ToLower(nerr.Message)
			if strings.Contains(msg, "method not found") {
				return native.NewError(native.ErrorCompatibility, "codex app-server does not support required v2 methods (model/list)")
			}
		}
		return err
	}
	if err := contract.Validate("2.0", parseVersionFromUserAgent(userAgent)); err != nil {
		return err
	}
	return nil
}

func parseVersionFromUserAgent(ua string) string {
	ua = strings.TrimSpace(ua)
	if ua == "" {
		return ""
	}
	idx := strings.LastIndex(ua, "/")
	if idx < 0 || idx+1 >= len(ua) {
		return ""
	}
	ver := strings.TrimSpace(ua[idx+1:])
	if ver == "" {
		return ""
	}
	return strings.TrimPrefix(ver, "v")
}

func (s *session) StartThread(ctx context.Context, req native.ThreadStartRequest) (native.ThreadHandle, error) {
	params := map[string]any{}
	if strings.TrimSpace(req.Model) != "" {
		params["model"] = strings.TrimSpace(req.Model)
	}
	if strings.TrimSpace(req.Cwd) != "" {
		params["cwd"] = strings.TrimSpace(req.Cwd)
	}
	if strings.TrimSpace(req.ApprovalPolicy) != "" {
		params["approvalPolicy"] = strings.TrimSpace(req.ApprovalPolicy)
	}
	if strings.TrimSpace(req.Sandbox) != "" {
		params["sandbox"] = strings.TrimSpace(req.Sandbox)
	}
	if strings.TrimSpace(req.Personality) != "" {
		params["personality"] = strings.TrimSpace(req.Personality)
	}

	res, err := s.callWithDefaultTimeout(ctx, "thread/start", params)
	if err != nil {
		return native.ThreadHandle{}, err
	}
	threadID, err := decodeThreadID(res)
	if err != nil {
		return native.ThreadHandle{}, err
	}
	s.threadID = threadID
	return native.ThreadHandle{ThreadID: threadID}, nil
}

func (s *session) ResumeThread(ctx context.Context, req native.ThreadResumeRequest) (native.ThreadHandle, error) {
	threadID := strings.TrimSpace(req.ThreadID)
	if threadID == "" {
		return native.ThreadHandle{}, native.NewError(native.ErrorProtocol, "thread resume requires threadId")
	}
	res, err := s.callWithDefaultTimeout(ctx, "thread/resume", map[string]any{"threadId": threadID})
	if err != nil {
		return native.ThreadHandle{}, err
	}
	resolved, err := decodeThreadID(res)
	if err != nil {
		return native.ThreadHandle{}, err
	}
	s.threadID = resolved
	return native.ThreadHandle{ThreadID: resolved}, nil
}

func (s *session) StartTurn(ctx context.Context, req native.TurnStartRequest) (native.TurnHandle, error) {
	threadID := strings.TrimSpace(req.ThreadID)
	if threadID == "" {
		threadID = strings.TrimSpace(s.threadID)
	}
	if threadID == "" {
		return native.TurnHandle{}, native.NewError(native.ErrorProtocol, "turn start requires threadId")
	}
	input := encodeInput(req.Input)
	if len(input) == 0 {
		return native.TurnHandle{}, native.NewError(native.ErrorProtocol, "turn start requires at least one input item")
	}
	res, err := s.callWithDefaultTimeout(ctx, "turn/start", map[string]any{
		"threadId": threadID,
		"input":    input,
	})
	if err != nil {
		return native.TurnHandle{}, err
	}
	return decodeTurnHandle(res)
}

func (s *session) SteerTurn(ctx context.Context, req native.TurnSteerRequest) (native.TurnHandle, error) {
	threadID := strings.TrimSpace(req.ThreadID)
	if threadID == "" {
		threadID = strings.TrimSpace(s.threadID)
	}
	if threadID == "" || strings.TrimSpace(req.TurnID) == "" {
		return native.TurnHandle{}, native.NewError(native.ErrorProtocol, "turn steer requires threadId and turnId")
	}
	input := encodeInput(req.Input)
	if len(input) == 0 {
		return native.TurnHandle{}, native.NewError(native.ErrorProtocol, "turn steer requires at least one input item")
	}
	res, err := s.callWithDefaultTimeout(ctx, "turn/steer", map[string]any{
		"threadId": threadID,
		"turnId":   strings.TrimSpace(req.TurnID),
		"input":    input,
	})
	if err != nil {
		return native.TurnHandle{}, err
	}
	return decodeTurnHandle(res)
}

func (s *session) InterruptTurn(ctx context.Context, req native.TurnInterruptRequest) error {
	threadID := strings.TrimSpace(req.ThreadID)
	if threadID == "" {
		threadID = strings.TrimSpace(s.threadID)
	}
	if threadID == "" || strings.TrimSpace(req.TurnID) == "" {
		return native.NewError(native.ErrorProtocol, "turn interrupt requires threadId and turnId")
	}
	_, err := s.callWithDefaultTimeout(ctx, "turn/interrupt", map[string]any{
		"threadId": threadID,
		"turnId":   strings.TrimSpace(req.TurnID),
	})
	return err
}

func (s *session) callWithDefaultTimeout(ctx context.Context, method string, params any) (json.RawMessage, error) {
	if _, ok := ctx.Deadline(); ok {
		return s.call(ctx, method, params)
	}
	callCtx, cancel := context.WithTimeout(ctx, s.requestTimeout)
	defer cancel()
	return s.call(callCtx, method, params)
}

func (s *session) call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	id := strconv.FormatUint(atomic.AddUint64(&s.reqCounter, 1), 10)
	native.RecordHealth(s.runtimeID, native.HealthRequestSent)
	payload := map[string]any{
		"id":     id,
		"method": strings.TrimSpace(method),
		"params": params,
	}
	respCh := make(chan rpcResponse, 1)
	s.pendingMu.Lock()
	s.pending[id] = respCh
	s.pendingMu.Unlock()

	if err := s.writeJSON(payload); err != nil {
		s.pendingMu.Lock()
		delete(s.pending, id)
		s.pendingMu.Unlock()
		return nil, err
	}

	select {
	case <-ctx.Done():
		s.pendingMu.Lock()
		delete(s.pending, id)
		s.pendingMu.Unlock()
		native.RecordHealth(s.runtimeID, native.HealthRequestFail)
		return nil, native.WrapError(native.ErrorTimeout, "runtime request timed out", ctx.Err())
	case <-s.done:
		if termErr := s.terminalError(); termErr != nil {
			native.RecordHealth(s.runtimeID, native.HealthRequestFail)
			return nil, termErr
		}
		native.RecordHealth(s.runtimeID, native.HealthRequestFail)
		return nil, native.NewError(native.ErrorStreamDisconnect, "runtime stream closed before response")
	case resp := <-respCh:
		if resp.Err != nil {
			native.RecordHealth(s.runtimeID, native.HealthRequestFail)
			return nil, resp.Err
		}
		return resp.Result, nil
	}
}

func (s *session) notify(ctx context.Context, method string, params any) (json.RawMessage, error) {
	payload := map[string]any{
		"method": strings.TrimSpace(method),
		"params": params,
	}
	if err := s.writeJSON(payload); err != nil {
		return nil, err
	}
	_ = ctx
	return nil, nil
}

func (s *session) writeJSON(payload any) error {
	b, err := json.Marshal(payload)
	if err != nil {
		return native.WrapError(native.ErrorProtocol, "marshal runtime request", err)
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if _, err := s.stdin.Write(append(b, '\n')); err != nil {
		return native.WrapError(native.ErrorTransport, "write runtime request", err)
	}
	return nil
}

func (s *session) readLoop() {
	scanner := bufio.NewScanner(s.stdout)
	scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		msg, err := parseRPCMessage([]byte(line))
		if err != nil {
			s.signalDone(native.WrapError(native.ErrorProtocol, "decode runtime message", err))
			return
		}
		if msg.Method != "" && strings.TrimSpace(msg.ID) == "" {
			s.dispatchEvent(msg.Method, msg.Params)
			continue
		}
		if strings.TrimSpace(msg.ID) == "" {
			continue
		}
		s.pendingMu.Lock()
		ch, ok := s.pending[msg.ID]
		if ok {
			delete(s.pending, msg.ID)
		}
		s.pendingMu.Unlock()
		if !ok {
			continue
		}
		if msg.RPCError != nil {
			ch <- rpcResponse{Err: mapRPCError(msg.Method, msg.RPCError)}
			continue
		}
		ch <- rpcResponse{Result: msg.Result}
	}
	if err := scanner.Err(); err != nil {
		native.RecordHealth(s.runtimeID, native.HealthStreamDisconnect)
		s.signalDone(native.WrapError(native.ErrorStreamDisconnect, "runtime stream disconnected", err))
		return
	}
	if s.closing.Load() {
		s.signalDone(nil)
		return
	}
	native.RecordHealth(s.runtimeID, native.HealthRuntimeCrash)
	s.signalDone(native.NewError(native.ErrorCrash, "runtime process exited (stream closed)"))
}

func (s *session) dispatchEvent(method string, params json.RawMessage) {
	ev := decodeEvent(method, params)
	s.listenerMu.RLock()
	listeners := make([]native.EventListener, 0, len(s.listeners))
	for _, l := range s.listeners {
		listeners = append(listeners, l)
	}
	s.listenerMu.RUnlock()
	for _, l := range listeners {
		func(listener native.EventListener) {
			defer func() {
				if rec := recover(); rec != nil {
					native.RecordHealth(s.runtimeID, native.HealthListenerFailure)
					s.signalDone(native.NewError(native.ErrorListenerFailure, fmt.Sprintf("listener panic: %v", rec)))
				}
			}()
			listener(ev)
		}(l)
	}
}

func (s *session) signalDone(err error) {
	s.terminalErrMu.Lock()
	if err != nil && s.terminalErr == nil {
		s.terminalErr = err
	}
	s.terminalErrMu.Unlock()
	if err != nil && !s.closing.Load() && !isListenerFailure(err) {
		s.dispatchSyntheticTerminalEvent(err)
	}
	s.doneOnce.Do(func() {
		s.pendingMu.Lock()
		defer s.pendingMu.Unlock()
		for id, ch := range s.pending {
			delete(s.pending, id)
			if err != nil {
				ch <- rpcResponse{Err: err}
			} else {
				ch <- rpcResponse{Err: native.NewError(native.ErrorStreamDisconnect, "runtime stream closed")}
			}
		}
		close(s.done)
	})
}

func isListenerFailure(err error) bool {
	nerr, ok := native.AsError(err)
	if !ok {
		return false
	}
	return nerr.Kind == native.ErrorListenerFailure
}

func (s *session) dispatchSyntheticTerminalEvent(err error) {
	if s == nil || err == nil {
		return
	}
	name := "codex/event/runtime_error"
	code := errorCodeFromError(err)
	switch code {
	case native.ErrorCodeForKind(native.ErrorStreamDisconnect):
		name = "codex/event/stream_disconnected"
	case native.ErrorCodeForKind(native.ErrorCrash):
		name = "codex/event/runtime_crashed"
	}
	payload, _ := json.Marshal(map[string]any{
		"code":    code,
		"message": strings.TrimSpace(err.Error()),
	})
	s.dispatchEvent(strings.TrimPrefix(name, "codex/event/"), payload)
}

func errorCodeFromError(err error) string {
	if nerr, ok := native.AsError(err); ok {
		return nerr.Code
	}
	return native.ErrorCodeForKind(native.ErrorProtocol)
}

func (s *session) terminalError() error {
	s.terminalErrMu.RLock()
	defer s.terminalErrMu.RUnlock()
	return s.terminalErr
}

type rpcMessage struct {
	ID       string          `json:"id,omitempty"`
	Method   string          `json:"method,omitempty"`
	Params   json.RawMessage `json:"params,omitempty"`
	Result   json.RawMessage `json:"result,omitempty"`
	RPCError *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int64  `json:"code"`
	Message string `json:"message"`
}

type rpcResponse struct {
	Result json.RawMessage
	Err    error
}

func parseRPCMessage(line []byte) (rpcMessage, error) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(line, &raw); err != nil {
		return rpcMessage{}, err
	}
	msg := rpcMessage{}
	if v, ok := raw["method"]; ok {
		_ = json.Unmarshal(v, &msg.Method)
	}
	if v, ok := raw["params"]; ok {
		msg.Params = append(json.RawMessage(nil), v...)
	}
	if v, ok := raw["result"]; ok {
		msg.Result = append(json.RawMessage(nil), v...)
	}
	if v, ok := raw["error"]; ok {
		var rpcErr rpcError
		if err := json.Unmarshal(v, &rpcErr); err != nil {
			return rpcMessage{}, err
		}
		msg.RPCError = &rpcErr
	}
	if v, ok := raw["id"]; ok {
		msg.ID = decodeID(v)
	}
	return msg, nil
}

func decodeID(raw json.RawMessage) string {
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	var i int64
	if err := json.Unmarshal(raw, &i); err == nil {
		return strconv.FormatInt(i, 10)
	}
	var f float64
	if err := json.Unmarshal(raw, &f); err == nil {
		return strconv.FormatInt(int64(f), 10)
	}
	return ""
}

func mapRPCError(method string, e *rpcError) error {
	if e == nil {
		return native.NewError(native.ErrorProtocol, "runtime request failed")
	}
	msg := strings.TrimSpace(e.Message)
	if msg == "" {
		msg = "runtime request failed"
	}
	method = strings.TrimSpace(method)
	if method != "" {
		msg = method + ": " + msg
	}
	lower := strings.ToLower(msg)
	if isRateLimitText(lower) {
		native.RecordHealth(native.StrategyCodexAppServer, native.HealthRateLimited)
		return native.NewError(native.ErrorRateLimit, msg)
	}
	if isAuthText(lower) {
		native.RecordHealth(native.StrategyCodexAppServer, native.HealthAuthFail)
		return native.NewError(native.ErrorAuth, msg)
	}
	if e.Code == -32601 {
		return native.NewError(native.ErrorProtocol, msg+" (method not found)")
	}
	return native.NewError(native.ErrorProtocol, msg)
}

func isRateLimitText(lower string) bool {
	return strings.Contains(lower, "rate limit") ||
		strings.Contains(lower, "usage limit") ||
		strings.Contains(lower, "quota") ||
		strings.Contains(lower, "429")
}

func isAuthText(lower string) bool {
	return strings.Contains(lower, "unauthorized") ||
		strings.Contains(lower, "forbidden") ||
		strings.Contains(lower, "authentication") ||
		strings.Contains(lower, "401") ||
		strings.Contains(lower, "403")
}

func decodeThreadID(raw json.RawMessage) (string, error) {
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		return "", native.WrapError(native.ErrorProtocol, "decode thread response", err)
	}
	thread, ok := payload["thread"].(map[string]any)
	if !ok {
		return "", native.NewError(native.ErrorProtocol, "thread response missing thread object")
	}
	threadID, _ := thread["id"].(string)
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return "", native.NewError(native.ErrorProtocol, "thread response missing thread id")
	}
	return threadID, nil
}

func decodeTurnHandle(raw json.RawMessage) (native.TurnHandle, error) {
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		return native.TurnHandle{}, native.WrapError(native.ErrorProtocol, "decode turn response", err)
	}
	if turnID, ok := payload["turnId"].(string); ok {
		turnID = strings.TrimSpace(turnID)
		if turnID == "" {
			return native.TurnHandle{}, native.NewError(native.ErrorProtocol, "turn response has empty turnId")
		}
		return native.TurnHandle{TurnID: turnID}, nil
	}
	turn, ok := payload["turn"].(map[string]any)
	if !ok {
		return native.TurnHandle{}, native.NewError(native.ErrorProtocol, "turn response missing turn object")
	}
	turnID, _ := turn["id"].(string)
	turnID = strings.TrimSpace(turnID)
	if turnID == "" {
		return native.TurnHandle{}, native.NewError(native.ErrorProtocol, "turn response missing turn id")
	}
	status, _ := turn["status"].(string)
	threadID, _ := turn["threadId"].(string)
	return native.TurnHandle{TurnID: turnID, Status: strings.TrimSpace(status), ThreadID: strings.TrimSpace(threadID)}, nil
}

func encodeInput(items []native.InputItem) []map[string]any {
	if len(items) == 0 {
		return nil
	}
	out := make([]map[string]any, 0, len(items))
	for _, it := range items {
		typ := strings.TrimSpace(it.Type)
		switch typ {
		case "text", "":
			if strings.TrimSpace(it.Text) == "" {
				continue
			}
			out = append(out, map[string]any{"type": "text", "text": it.Text})
		case "image":
			if strings.TrimSpace(it.ImageURL) == "" {
				continue
			}
			out = append(out, map[string]any{"type": "image", "url": it.ImageURL})
		case "localImage":
			if strings.TrimSpace(it.Path) == "" {
				continue
			}
			out = append(out, map[string]any{"type": "localImage", "path": it.Path})
		case "skill":
			if strings.TrimSpace(it.Name) == "" || strings.TrimSpace(it.Path) == "" {
				continue
			}
			out = append(out, map[string]any{"type": "skill", "name": it.Name, "path": it.Path})
		case "mention":
			if strings.TrimSpace(it.Name) == "" || strings.TrimSpace(it.Path) == "" {
				continue
			}
			out = append(out, map[string]any{"type": "mention", "name": it.Name, "path": it.Path})
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func decodeEvent(method string, params json.RawMessage) native.Event {
	ev := native.Event{
		Name:       normalizeEventName(method),
		ReceivedAt: time.Now().UTC(),
		Payload:    params,
	}
	var body map[string]any
	if err := json.Unmarshal(params, &body); err != nil {
		return ev
	}
	ev.ThreadID = firstString(body, "threadId", "thread_id")
	ev.TurnID = firstString(body, "turnId", "turn_id")
	ev.ItemID = firstString(body, "itemId", "item_id")
	ev.CallID = firstString(body, "callId", "call_id", "id")
	return ev
}

func normalizeEventName(method string) string {
	method = strings.TrimSpace(method)
	if method == "" {
		return "codex/event/unknown"
	}
	if strings.HasPrefix(method, "codex/event/") {
		return method
	}
	return "codex/event/" + strings.ReplaceAll(method, "/", "_")
}

func firstString(body map[string]any, keys ...string) string {
	for _, key := range keys {
		if v, ok := body[key].(string); ok {
			v = strings.TrimSpace(v)
			if v != "" {
				return v
			}
		}
	}
	return ""
}

func buildRuntimeEnv(policy native.EnvPolicy, attemptEnv map[string]string, extra map[string]string) (map[string]string, []string, error) {
	merged := map[string]string{}
	for _, kv := range os.Environ() {
		parts := strings.SplitN(kv, "=", 2)
		if len(parts) != 2 {
			continue
		}
		merged[parts[0]] = parts[1]
	}
	for k, v := range attemptEnv {
		merged[k] = v
	}
	for k, v := range extra {
		merged[k] = v
	}
	allowed, blocked := policy.Filter(merged)
	if len(allowed) == 0 {
		return nil, blocked, native.NewError(native.ErrorEnvPolicy, "runtime env policy removed all variables")
	}
	return allowed, blocked, nil
}

func blockedExplicitVars(blocked []string, attemptEnv map[string]string, extra map[string]string) bool {
	if len(blocked) == 0 {
		return false
	}
	explicit := map[string]bool{}
	for k := range attemptEnv {
		k = strings.ToUpper(strings.TrimSpace(k))
		if k != "" {
			explicit[k] = true
		}
	}
	for k := range extra {
		k = strings.ToUpper(strings.TrimSpace(k))
		if k != "" {
			explicit[k] = true
		}
	}
	if len(explicit) == 0 {
		return false
	}
	for _, b := range blocked {
		if explicit[b] {
			return true
		}
	}
	return false
}

func mapToEnviron(m map[string]string) []string {
	if len(m) == 0 {
		return nil
	}
	out := make([]string, 0, len(m))
	for k, v := range m {
		if strings.TrimSpace(k) == "" {
			continue
		}
		out = append(out, k+"="+v)
	}
	return out
}

func isExitError(err error) bool {
	if err == nil {
		return false
	}
	_, ok := err.(*exec.ExitError)
	return ok
}

func Register(reg *native.Registry, cfg Config) error {
	if reg == nil {
		return fmt.Errorf("runtime registry is nil")
	}
	return reg.Register(NewRuntime(cfg))
}

func DefaultCommandFromEnv() []string {
	if raw := strings.TrimSpace(os.Getenv("ZCL_CODEX_APP_SERVER_CMD")); raw != "" {
		parts := strings.Fields(raw)
		if len(parts) > 0 {
			return parts
		}
	}
	bin := strings.TrimSpace(os.Getenv("ZCL_CODEX_BIN"))
	if bin == "" {
		bin = "codex"
	}
	if strings.ContainsRune(bin, os.PathSeparator) {
		bin = filepath.Clean(bin)
	}
	return []string{bin, "app-server", "--listen", "stdio://"}
}
