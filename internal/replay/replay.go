package replay

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/marcohefti/zero-context-lab/internal/funnel/cli_funnel"
	"github.com/marcohefti/zero-context-lab/internal/schema"
)

type StepResult struct {
	Index      int      `json:"index"`
	Tool       string   `json:"tool"`
	Op         string   `json:"op"`
	Replayable bool     `json:"replayable"`
	Argv       []string `json:"argv,omitempty"`

	OK       *bool  `json:"ok,omitempty"`
	ExitCode *int   `json:"exitCode,omitempty"`
	Error    string `json:"error,omitempty"`
}

type Result struct {
	OK         bool         `json:"ok"`
	AttemptDir string       `json:"attemptDir"`
	Steps      []StepResult `json:"steps"`
	StartedAt  string       `json:"startedAt"`
}

// ReplayAttempt best-effort replays tool.calls.jsonl without mutating attempt artifacts.
// Today it supports: tool=cli/op=exec (argv input). Everything else is marked non-replayable.
func ReplayAttempt(ctx context.Context, attemptDir string) (Result, error) {
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
	out := Result{OK: true, AttemptDir: abs, StartedAt: start.Format(time.RFC3339Nano)}

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)

	i := 0
	for sc.Scan() {
		line := sc.Bytes()
		if len(bytesTrim(line)) == 0 {
			continue
		}
		var ev schema.TraceEventV1
		if err := json.Unmarshal(line, &ev); err != nil {
			return Result{}, fmt.Errorf("invalid trace jsonl: %w", err)
		}

		step := StepResult{Index: i, Tool: ev.Tool, Op: ev.Op}
		i++

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

			res, err := clifunnel.Run(ctx, in.Argv, io.Discard, io.Discard, schema.PreviewMaxBytesV1)
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

		step.Replayable = false
		step.Error = "not replayable"
		out.Steps = append(out.Steps, step)
	}
	if err := sc.Err(); err != nil {
		return Result{}, err
	}
	return out, nil
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
