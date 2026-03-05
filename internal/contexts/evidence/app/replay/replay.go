package replay

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	clifunnel "github.com/marcohefti/zero-context-lab/internal/kernel/cli_funnel"
	"github.com/marcohefti/zero-context-lab/internal/kernel/schema"
	"github.com/marcohefti/zero-context-lab/internal/kernel/store"
	"golang.org/x/sys/execabs"
)

type StepResult struct {
	Index      int             `json:"index"`
	Tool       string          `json:"tool"`
	Op         string          `json:"op"`
	Replayable bool            `json:"replayable"`
	Argv       []string        `json:"argv,omitempty"`
	Method     string          `json:"method,omitempty"`
	Input      json.RawMessage `json:"input,omitempty"`

	OK       *bool  `json:"ok,omitempty"`
	ExitCode *int   `json:"exitCode,omitempty"`
	Error    string `json:"error,omitempty"`
}

type Result struct {
	OK         bool         `json:"ok"`
	AttemptDir string       `json:"attemptDir"`
	DryRun     bool         `json:"dryRun"`
	Steps      []StepResult `json:"steps"`
	StartedAt  string       `json:"startedAt"`
}

type Opts struct {
	Execute   bool
	AllowAll  bool
	AllowCmds map[string]bool // basename allowlist when executing and !AllowAll
	MaxSteps  int
	UseStdin  bool
}

// ReplayAttempt best-effort replays tool.calls.jsonl without mutating attempt artifacts.
// It is dry-run by default; execution requires opts.Execute.
//
// Supported today:
// - tool=cli/op=exec (argv input)
func ReplayAttempt(ctx context.Context, attemptDir string, opts Opts) (Result, error) {
	abs, err := filepath.Abs(attemptDir)
	if err != nil {
		return Result{}, err
	}
	tracePath := filepath.Join(abs, "tool.calls.jsonl")
	f, err := os.Open(tracePath)
	if err != nil {
		return Result{}, err
	}
	defer func() { _ = f.Close() }()

	start := time.Now().UTC()
	runner := newReplayRunner(abs, opts, start)
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	if err := runner.run(ctx, sc); err != nil {
		return Result{}, err
	}
	return runner.out, nil
}

type replayRunner struct {
	opts          Opts
	out           Result
	mcpServerArgv []string
	mcpSess       *mcpSession
}

func newReplayRunner(attemptDir string, opts Opts, start time.Time) *replayRunner {
	if opts.MaxSteps <= 0 {
		opts.MaxSteps = 50
	}
	return &replayRunner{
		opts: opts,
		out: Result{
			OK:         true,
			AttemptDir: attemptDir,
			DryRun:     !opts.Execute,
			StartedAt:  start.Format(time.RFC3339Nano),
		},
	}
}

func (r *replayRunner) run(ctx context.Context, sc *bufio.Scanner) error {
	defer func() {
		if r.mcpSess != nil {
			_ = r.mcpSess.Close()
		}
	}()
	index := 0
	for sc.Scan() {
		line := sc.Bytes()
		if len(bytesTrim(line)) == 0 {
			continue
		}
		if r.maxStepsExceeded(index) {
			break
		}
		ev, err := parseReplayTraceEvent(line)
		if err != nil {
			return err
		}
		step := StepResult{Index: index, Tool: ev.Tool, Op: ev.Op, Input: ev.Input}
		index++
		r.processEvent(ctx, ev, step)
	}
	return sc.Err()
}

func (r *replayRunner) maxStepsExceeded(index int) bool {
	if len(r.out.Steps) < r.opts.MaxSteps {
		return false
	}
	r.out.OK = false
	r.out.Steps = append(r.out.Steps, StepResult{
		Index:      index,
		Tool:       "zcl",
		Op:         "replay",
		Replayable: false,
		Error:      "maxSteps exceeded",
	})
	return true
}

func parseReplayTraceEvent(line []byte) (schema.TraceEventV1, error) {
	var ev schema.TraceEventV1
	if err := json.Unmarshal(line, &ev); err != nil {
		return schema.TraceEventV1{}, fmt.Errorf("invalid trace jsonl: %w", err)
	}
	return ev, nil
}

func (r *replayRunner) processEvent(ctx context.Context, ev schema.TraceEventV1, step StepResult) {
	if r.handleMCPSpawn(ev, &step) {
		r.out.Steps = append(r.out.Steps, step)
		return
	}
	if r.handleCLIExec(ctx, ev, &step) {
		r.out.Steps = append(r.out.Steps, step)
		return
	}
	if r.handleMCPCall(ctx, ev, &step) {
		r.out.Steps = append(r.out.Steps, step)
		return
	}
	step.Replayable = false
	step.Error = "not replayable"
	r.out.Steps = append(r.out.Steps, step)
}

func (r *replayRunner) handleMCPSpawn(ev schema.TraceEventV1, step *StepResult) bool {
	if ev.Tool != "mcp" || ev.Op != "spawn" {
		return false
	}
	var in struct {
		Argv []string `json:"argv"`
	}
	if len(ev.Input) > 0 && json.Unmarshal(ev.Input, &in) == nil && len(in.Argv) > 0 {
		r.mcpServerArgv = append([]string(nil), in.Argv...)
	}
	step.Replayable = false
	return true
}

func (r *replayRunner) handleCLIExec(ctx context.Context, ev schema.TraceEventV1, step *StepResult) bool {
	if ev.Tool != "cli" || ev.Op != "exec" {
		return false
	}
	var in struct {
		Argv []string `json:"argv"`
	}
	if len(ev.Input) == 0 || json.Unmarshal(ev.Input, &in) != nil || len(in.Argv) == 0 {
		step.Replayable = false
		step.Error = "missing/invalid argv input"
		r.out.OK = false
		return true
	}
	step.Replayable = true
	step.Argv = append([]string(nil), in.Argv...)
	if !r.opts.Execute {
		return true
	}
	if !r.isAllowedCommand(in.Argv[0], false, step) {
		return true
	}
	stdin := replayStdin(r.opts.UseStdin)
	res, err := clifunnel.Run(ctx, in.Argv, stdin, io.Discard, io.Discard, nil, nil, schema.PreviewMaxBytesV1)
	if err != nil {
		step.Error = err.Error()
		ok := false
		step.OK = &ok
		r.out.OK = false
		return true
	}
	ok := res.ExitCode == 0
	step.OK = &ok
	ec := res.ExitCode
	step.ExitCode = &ec
	if !ok {
		r.out.OK = false
	}
	return true
}

func replayStdin(useStdin bool) io.Reader {
	if useStdin {
		return os.Stdin
	}
	return strings.NewReader("")
}

func (r *replayRunner) isAllowedCommand(command string, mcp bool, step *StepResult) bool {
	if r.opts.AllowAll {
		return true
	}
	base := filepath.Base(command)
	if r.opts.AllowCmds != nil && r.opts.AllowCmds[base] {
		return true
	}
	label := "command not allowed (use --allow " + base + " or --allow-all)"
	if mcp {
		label = "mcp server command not allowed (use --allow " + base + " or --allow-all)"
	}
	step.Error = label
	ok := false
	step.OK = &ok
	r.out.OK = false
	return false
}

func (r *replayRunner) handleMCPCall(ctx context.Context, ev schema.TraceEventV1, step *StepResult) bool {
	if ev.Tool != "mcp" || !isReplayableMCPOp(ev.Op) {
		return false
	}
	in, method, ok := parseMCPCallInput(ev.Input)
	if !ok {
		step.Replayable = false
		step.Error = "missing/invalid jsonrpc input"
		r.out.OK = false
		return true
	}
	step.Method = method
	if strings.TrimSpace(method) == "" {
		step.Replayable = false
		step.Error = "missing method"
		r.out.OK = false
		return true
	}
	if len(r.mcpServerArgv) == 0 {
		step.Replayable = false
		step.Error = "missing mcp spawn argv (need tool=mcp op=spawn trace event)"
		r.out.OK = false
		return true
	}
	step.Replayable = true
	if !r.opts.Execute {
		return true
	}
	if r.mcpSess == nil {
		if !r.isAllowedCommand(r.mcpServerArgv[0], true, step) {
			return true
		}
		sess, err := startMCPSession(ctx, r.mcpServerArgv)
		if err != nil {
			step.Error = err.Error()
			ok := false
			step.OK = &ok
			r.out.OK = false
			return true
		}
		r.mcpSess = sess
	}
	okRes := r.mcpSess.Call(in)
	okBool := okRes
	step.OK = &okBool
	if !okBool {
		r.out.OK = false
		step.Error = "mcp call failed"
	}
	return true
}

func isReplayableMCPOp(op string) bool {
	return op == "initialize" || op == "tools/list" || op == "tools/call"
}

func parseMCPCallInput(input json.RawMessage) (map[string]any, string, bool) {
	var in map[string]any
	if len(input) == 0 || json.Unmarshal(input, &in) != nil {
		return nil, "", false
	}
	method, _ := in["method"].(string)
	return in, method, true
}

type mcpSession struct {
	cmd   *execabs.Cmd
	stdin io.WriteCloser
	out   *bufio.Scanner
}

func startMCPSession(ctx context.Context, argv []string) (*mcpSession, error) {
	if len(argv) == 0 {
		return nil, fmt.Errorf("missing mcp argv")
	}
	cmd := execabs.CommandContext(ctx, argv[0], argv[1:]...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	// Drain stderr to avoid deadlocks.
	go func() { _, _ = io.Copy(io.Discard, stderr) }()

	sc := bufio.NewScanner(stdout)
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	return &mcpSession{cmd: cmd, stdin: stdin, out: sc}, nil
}

func (s *mcpSession) Close() error {
	if s == nil {
		return nil
	}
	_ = s.stdin.Close()
	return s.cmd.Wait()
}

func (s *mcpSession) Call(in map[string]any) bool {
	if s == nil {
		return false
	}
	method, _ := in["method"].(string)
	id := in["id"]
	params := in["params"]

	req := map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"method":  method,
	}
	if params != nil {
		req["params"] = params
	}
	b, err := store.CanonicalJSON(req)
	if err != nil {
		return false
	}
	if _, err := s.stdin.Write(append(b, '\n')); err != nil {
		return false
	}

	wantID := jsonRPCID(id)
	if wantID == "" {
		// Best-effort: read one response line.
		return s.out.Scan()
	}
	for s.out.Scan() {
		line := bytesTrim(s.out.Bytes())
		if len(line) == 0 {
			continue
		}
		var msg map[string]any
		if json.Unmarshal(line, &msg) != nil {
			continue
		}
		if jsonRPCID(msg["id"]) == wantID {
			return msg["error"] == nil
		}
	}
	return false
}

func jsonRPCID(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case float64:
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
