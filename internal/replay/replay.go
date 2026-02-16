package replay

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	clifunnel "github.com/marcohefti/zero-context-lab/internal/funnel/cli_funnel"
	"github.com/marcohefti/zero-context-lab/internal/schema"
	"github.com/marcohefti/zero-context-lab/internal/store"
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
	if opts.MaxSteps <= 0 {
		opts.MaxSteps = 50
	}
	out := Result{
		OK:         true,
		AttemptDir: abs,
		DryRun:     !opts.Execute,
		StartedAt:  start.Format(time.RFC3339Nano),
	}

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)

	var mcpServerArgv []string
	var mcpSess *mcpSession
	defer func() {
		if mcpSess != nil {
			_ = mcpSess.Close()
		}
	}()

	i := 0
	for sc.Scan() {
		line := sc.Bytes()
		if len(bytesTrim(line)) == 0 {
			continue
		}
		if len(out.Steps) >= opts.MaxSteps {
			out.OK = false
			out.Steps = append(out.Steps, StepResult{
				Index:      i,
				Tool:       "zcl",
				Op:         "replay",
				Replayable: false,
				Error:      "maxSteps exceeded",
			})
			break
		}

		var ev schema.TraceEventV1
		if err := json.Unmarshal(line, &ev); err != nil {
			return Result{}, fmt.Errorf("invalid trace jsonl: %w", err)
		}

		step := StepResult{Index: i, Tool: ev.Tool, Op: ev.Op}
		step.Input = ev.Input
		i++

		if ev.Tool == "mcp" && ev.Op == "spawn" {
			// Record the server argv for later replay.
			var in struct {
				Argv []string `json:"argv"`
			}
			if len(ev.Input) > 0 && json.Unmarshal(ev.Input, &in) == nil && len(in.Argv) > 0 {
				mcpServerArgv = append([]string(nil), in.Argv...)
			}
			step.Replayable = false
			out.Steps = append(out.Steps, step)
			continue
		}

		if ev.Tool == "cli" && ev.Op == "exec" {
			var in struct {
				Argv []string `json:"argv"`
			}
			if len(ev.Input) == 0 || json.Unmarshal(ev.Input, &in) != nil || len(in.Argv) == 0 {
				step.Replayable = false
				step.Error = "missing/invalid argv input"
				out.OK = false
				out.Steps = append(out.Steps, step)
				continue
			}
			step.Replayable = true
			step.Argv = append([]string(nil), in.Argv...)

			// Dry-run: record what would be executed.
			if !opts.Execute {
				out.Steps = append(out.Steps, step)
				continue
			}

			base := filepath.Base(in.Argv[0])
			if !opts.AllowAll {
				if opts.AllowCmds == nil || !opts.AllowCmds[base] {
					step.Error = "command not allowed (use --allow " + base + " or --allow-all)"
					ok := false
					step.OK = &ok
					out.OK = false
					out.Steps = append(out.Steps, step)
					continue
				}
			}

			var stdin io.Reader
			if opts.UseStdin {
				stdin = os.Stdin
			} else {
				stdin = strings.NewReader("")
			}

			res, err := clifunnel.Run(ctx, in.Argv, stdin, io.Discard, io.Discard, nil, nil, schema.PreviewMaxBytesV1)
			if err != nil {
				step.Error = err.Error()
				ok := false
				step.OK = &ok
				out.OK = false
			} else {
				ok := res.ExitCode == 0
				step.OK = &ok
				ec := res.ExitCode
				step.ExitCode = &ec
				if !ok {
					out.OK = false
				}
			}

			out.Steps = append(out.Steps, step)
			continue
		}

		if ev.Tool == "mcp" && (ev.Op == "initialize" || ev.Op == "tools/list" || ev.Op == "tools/call") {
			var in map[string]any
			if len(ev.Input) == 0 || json.Unmarshal(ev.Input, &in) != nil {
				step.Replayable = false
				step.Error = "missing/invalid jsonrpc input"
				out.OK = false
				out.Steps = append(out.Steps, step)
				continue
			}
			method, _ := in["method"].(string)
			step.Method = method
			if strings.TrimSpace(method) == "" {
				step.Replayable = false
				step.Error = "missing method"
				out.OK = false
				out.Steps = append(out.Steps, step)
				continue
			}
			if len(mcpServerArgv) == 0 {
				step.Replayable = false
				step.Error = "missing mcp spawn argv (need tool=mcp op=spawn trace event)"
				out.OK = false
				out.Steps = append(out.Steps, step)
				continue
			}
			step.Replayable = true

			if !opts.Execute {
				out.Steps = append(out.Steps, step)
				continue
			}

			// Start MCP server once, on-demand.
			if mcpSess == nil {
				base := filepath.Base(mcpServerArgv[0])
				if !opts.AllowAll {
					if opts.AllowCmds == nil || !opts.AllowCmds[base] {
						step.Error = "mcp server command not allowed (use --allow " + base + " or --allow-all)"
						ok := false
						step.OK = &ok
						out.OK = false
						out.Steps = append(out.Steps, step)
						continue
					}
				}
				sess, err := startMCPSession(ctx, mcpServerArgv)
				if err != nil {
					step.Error = err.Error()
					ok := false
					step.OK = &ok
					out.OK = false
					out.Steps = append(out.Steps, step)
					continue
				}
				mcpSess = sess
			}

			okRes := mcpSess.Call(in)
			ok := okRes
			step.OK = &ok
			if !ok {
				out.OK = false
				step.Error = "mcp call failed"
			}
			out.Steps = append(out.Steps, step)
			continue
		}

		step.Replayable = false
		step.Error = "not replayable"
		out.Steps = append(out.Steps, step)
	}
	if err := sc.Err(); err != nil {
		return Result{}, err
	}
	return out, nil
}

type mcpSession struct {
	cmd   *exec.Cmd
	stdin io.WriteCloser
	out   *bufio.Scanner
}

func startMCPSession(ctx context.Context, argv []string) (*mcpSession, error) {
	if len(argv) == 0 {
		return nil, fmt.Errorf("missing mcp argv")
	}
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
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
