package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/marcohefti/zero-context-lab/internal/attempt"
	clifunnel "github.com/marcohefti/zero-context-lab/internal/funnel/cli_funnel"
	"github.com/marcohefti/zero-context-lab/internal/redact"
	"github.com/marcohefti/zero-context-lab/internal/schema"
	"github.com/marcohefti/zero-context-lab/internal/store"
	"github.com/marcohefti/zero-context-lab/internal/trace"
)

func (r Runner) runRun(args []string) int {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	capture := fs.Bool("capture", false, "capture full stdout/stderr to files under the attempt dir (in addition to bounded previews in tool.calls.jsonl)")
	captureMaxBytes := fs.Int64("capture-max-bytes", schema.CaptureMaxBytesV1, "max bytes to capture per stream when using --capture")
	envelope := fs.Bool("envelope", false, "print a JSON envelope instead of passthrough tool output (requires --json)")
	jsonOut := fs.Bool("json", false, "print JSON output (required with --envelope)")
	help := fs.Bool("help", false, "show help")
	if err := fs.Parse(args); err != nil {
		return r.failUsage("run: invalid flags")
	}
	if *help {
		printRunHelp(r.Stdout)
		return 0
	}
	if *envelope && !*jsonOut {
		printRunHelp(r.Stderr)
		return r.failUsage("run: --envelope requires --json")
	}
	if !*envelope && *jsonOut {
		printRunHelp(r.Stderr)
		return r.failUsage("run: --json is only supported with --envelope")
	}

	env, err := trace.EnvFromProcess()
	if err != nil {
		printRunHelp(r.Stderr)
		return r.failUsage("run: missing ZCL attempt context (run `zcl attempt start --json` and pass the returned env)")
	}

	argv := fs.Args()
	if len(argv) >= 1 && argv[0] == "--" {
		argv = argv[1:]
	}
	if len(argv) == 0 {
		printRunHelp(r.Stderr)
		return r.failUsage("run: missing command (use: zcl run -- <cmd> ...)")
	}

	now := r.Now()
	ctx, cancel, timedOut := attemptCtxForDeadline(now, env.OutDirAbs)
	if cancel != nil {
		defer cancel()
	}

	var (
		outFull io.Writer
		errFull io.Writer
		outRel  string
		errRel  string
		outF    *os.File
		errF    *os.File
		outBW   *boundedHashWriter
		errBW   *boundedHashWriter
	)
	if *capture {
		if *captureMaxBytes <= 0 {
			printRunHelp(r.Stderr)
			return r.failUsage("run: --capture-max-bytes must be > 0")
		}
		dir := filepath.Join(env.OutDirAbs, "captures", "cli")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			fmt.Fprintf(r.Stderr, "ZCL_E_IO: %s\n", err.Error())
			return 1
		}
		id := fmt.Sprintf("%d", now.UTC().UnixNano())
		outRel = filepath.Join("captures", "cli", id+".stdout.log")
		errRel = filepath.Join("captures", "cli", id+".stderr.log")

		outPath := filepath.Join(env.OutDirAbs, outRel)
		errPath := filepath.Join(env.OutDirAbs, errRel)

		outF, err = os.OpenFile(outPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if err != nil {
			fmt.Fprintf(r.Stderr, "ZCL_E_IO: %s\n", err.Error())
			return 1
		}
		errF, err = os.OpenFile(errPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if err != nil {
			_ = outF.Close()
			fmt.Fprintf(r.Stderr, "ZCL_E_IO: %s\n", err.Error())
			return 1
		}
		defer func() {
			_ = outF.Sync()
			_ = errF.Sync()
			_ = outF.Close()
			_ = errF.Close()
		}()
		outBW = newBoundedHashWriter(outF, *captureMaxBytes)
		errBW = newBoundedHashWriter(errF, *captureMaxBytes)
		outFull = outBW
		errFull = errBW
	}

	var (
		res    clifunnel.Result
		runErr error
	)
	if timedOut {
		runErr = context.DeadlineExceeded
	} else {
		var toolStdout io.Writer = r.Stdout
		var toolStderr io.Writer = r.Stderr
		if *envelope {
			toolStdout = io.Discard
			toolStderr = io.Discard
		}
		res, runErr = clifunnel.Run(ctx, argv, nil, toolStdout, toolStderr, outFull, errFull, schema.PreviewMaxBytesV1)
	}

	traceRes := trace.ResultForTrace{
		ExitCode:           res.ExitCode,
		DurationMs:         res.DurationMs,
		OutBytes:           res.OutBytes,
		ErrBytes:           res.ErrBytes,
		OutPreview:         res.OutPreview,
		ErrPreview:         res.ErrPreview,
		OutTruncated:       res.OutTruncated,
		ErrTruncated:       res.ErrTruncated,
		CapturedStdoutPath: outRel,
		CapturedStderrPath: errRel,
		CaptureMaxBytes:    *captureMaxBytes,
	}
	if outBW != nil {
		traceRes.CapturedStdoutBytes = outBW.WrittenBytes()
		traceRes.CapturedStdoutSHA256 = outBW.SumHex()
		traceRes.CapturedStdoutTruncated = outBW.Truncated()
	}
	if errBW != nil {
		traceRes.CapturedStderrBytes = errBW.WrittenBytes()
		traceRes.CapturedStderrSHA256 = errBW.SumHex()
		traceRes.CapturedStderrTruncated = errBW.Truncated()
	}
	if timedOut || errors.Is(runErr, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
		traceRes.SpawnError = "ZCL_E_TIMEOUT"
	} else if runErr != nil {
		traceRes.SpawnError = "ZCL_E_SPAWN"
	}
	if err := trace.AppendCLIRunEvent(r.Now(), env, argv, traceRes); err != nil {
		fmt.Fprintf(r.Stderr, "ZCL_E_IO: failed to append tool.calls.jsonl: %s\n", err.Error())
		return 1
	}

	// Secondary evidence index for capture mode.
	if *capture {
		redArgv := make([]string, 0, len(argv))
		for _, s := range argv {
			red, _ := redact.Text(s)
			redArgv = append(redArgv, red)
		}
		ev := schema.CaptureEventV1{
			V:               1,
			TS:              now.UTC().Format(time.RFC3339Nano),
			RunID:           env.RunID,
			SuiteID:         env.SuiteID,
			MissionID:       env.MissionID,
			AttemptID:       env.AttemptID,
			AgentID:         env.AgentID,
			Tool:            "cli",
			Op:              "exec",
			Input:           boundedArgvInputJSON(redArgv),
			StdoutPath:      outRel,
			StderrPath:      errRel,
			StdoutBytes:     traceRes.CapturedStdoutBytes,
			StderrBytes:     traceRes.CapturedStderrBytes,
			StdoutSHA256:    traceRes.CapturedStdoutSHA256,
			StderrSHA256:    traceRes.CapturedStderrSHA256,
			StdoutTruncated: traceRes.CapturedStdoutTruncated,
			StderrTruncated: traceRes.CapturedStderrTruncated,
			MaxBytes:        *captureMaxBytes,
		}
		if err := store.AppendJSONL(filepath.Join(env.OutDirAbs, "captures.jsonl"), ev); err != nil {
			fmt.Fprintf(r.Stderr, "ZCL_E_IO: failed to append captures.jsonl: %s\n", err.Error())
			return 1
		}
	}
	if timedOut || errors.Is(runErr, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
		fmt.Fprintf(r.Stderr, "ZCL_E_TIMEOUT: attempt deadline exceeded\n")
		return 1
	}
	if runErr != nil {
		fmt.Fprintf(r.Stderr, "ZCL_E_IO: run failed: %s\n", runErr.Error())
		return 1
	}

	if *envelope {
		type runEnvelopeJSON struct {
			OK       bool   `json:"ok"`
			Code     string `json:"code,omitempty"`
			ExitCode int    `json:"exitCode"`

			DurationMs int64 `json:"durationMs"`
			OutBytes   int64 `json:"outBytes"`
			ErrBytes   int64 `json:"errBytes"`

			OutPreview   string `json:"outPreview,omitempty"`
			ErrPreview   string `json:"errPreview,omitempty"`
			OutTruncated bool   `json:"outTruncated,omitempty"`
			ErrTruncated bool   `json:"errTruncated,omitempty"`

			CapturedStdoutPath      string `json:"capturedStdoutPath,omitempty"`
			CapturedStderrPath      string `json:"capturedStderrPath,omitempty"`
			CapturedStdoutBytes     int64  `json:"capturedStdoutBytes,omitempty"`
			CapturedStderrBytes     int64  `json:"capturedStderrBytes,omitempty"`
			CapturedStdoutSHA256    string `json:"capturedStdoutSha256,omitempty"`
			CapturedStderrSHA256    string `json:"capturedStderrSha256,omitempty"`
			CapturedStdoutTruncated bool   `json:"capturedStdoutTruncated,omitempty"`
			CapturedStderrTruncated bool   `json:"capturedStderrTruncated,omitempty"`
			CaptureMaxBytes         int64  `json:"captureMaxBytes,omitempty"`

			RunID     string `json:"runId"`
			SuiteID   string `json:"suiteId"`
			MissionID string `json:"missionId"`
			AttemptID string `json:"attemptId"`
		}
		envOut := runEnvelopeJSON{
			OK:                      traceRes.SpawnError == "" && res.ExitCode == 0,
			Code:                    traceRes.SpawnError,
			ExitCode:                res.ExitCode,
			DurationMs:              res.DurationMs,
			OutBytes:                res.OutBytes,
			ErrBytes:                res.ErrBytes,
			OutPreview:              res.OutPreview,
			ErrPreview:              res.ErrPreview,
			OutTruncated:            res.OutTruncated,
			ErrTruncated:            res.ErrTruncated,
			CapturedStdoutPath:      outRel,
			CapturedStderrPath:      errRel,
			CapturedStdoutBytes:     traceRes.CapturedStdoutBytes,
			CapturedStderrBytes:     traceRes.CapturedStderrBytes,
			CapturedStdoutSHA256:    traceRes.CapturedStdoutSHA256,
			CapturedStderrSHA256:    traceRes.CapturedStderrSHA256,
			CapturedStdoutTruncated: traceRes.CapturedStdoutTruncated,
			CapturedStderrTruncated: traceRes.CapturedStderrTruncated,
			CaptureMaxBytes:         *captureMaxBytes,
			RunID:                   env.RunID,
			SuiteID:                 env.SuiteID,
			MissionID:               env.MissionID,
			AttemptID:               env.AttemptID,
		}
		if exit := r.writeJSON(envOut); exit != 0 {
			return exit
		}
	}

	// Preserve the wrapped command's exit code for operator parity.
	if res.ExitCode != 0 {
		return res.ExitCode
	}
	return 0
}

func attemptCtxForDeadline(now time.Time, attemptDir string) (context.Context, context.CancelFunc, bool) {
	a, err := attempt.ReadAttempt(attemptDir)
	if err != nil {
		return context.Background(), nil, false
	}
	if a.TimeoutMs <= 0 || strings.TrimSpace(a.StartedAt) == "" {
		return context.Background(), nil, false
	}
	start, err := time.Parse(time.RFC3339Nano, a.StartedAt)
	if err != nil {
		return context.Background(), nil, false
	}
	deadline := start.Add(time.Duration(a.TimeoutMs) * time.Millisecond)
	remaining := deadline.Sub(now)
	if remaining <= 0 {
		return context.Background(), nil, true
	}
	ctx, cancel := context.WithTimeout(context.Background(), remaining)
	return ctx, cancel, false
}

func printRunHelp(w io.Writer) {
	fmt.Fprint(w, `Usage:
  zcl run [--capture --capture-max-bytes N] -- <cmd> [args...]
  zcl run --envelope --json [--capture --capture-max-bytes N] -- <cmd> [args...]
`)
}
