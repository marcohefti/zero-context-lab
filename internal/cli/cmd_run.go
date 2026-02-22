package cli

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
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
	captureRaw := fs.Bool("capture-raw", false, "capture raw stdout/stderr (unsafe; may contain secrets)")
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
	if *captureRaw && !*capture {
		printRunHelp(r.Stderr)
		return r.failUsage("run: --capture-raw requires --capture")
	}

	env, err := trace.EnvFromProcess()
	if err != nil {
		printRunHelp(r.Stderr)
		return r.failUsage("run: missing ZCL attempt context (run `zcl attempt start --json` and pass the returned env)")
	}
	attemptMeta, err := attempt.ReadAttempt(env.OutDirAbs)
	if err != nil {
		printRunHelp(r.Stderr)
		return r.failUsage("run: missing/invalid attempt.json in ZCL_OUT_DIR (need zcl attempt start context)")
	}
	if attemptMeta.RunID != env.RunID || attemptMeta.SuiteID != env.SuiteID || attemptMeta.MissionID != env.MissionID || attemptMeta.AttemptID != env.AttemptID {
		printRunHelp(r.Stderr)
		return r.failUsage("run: attempt.json ids do not match ZCL_* env (refuse to run)")
	}

	argv := fs.Args()
	if len(argv) >= 1 && argv[0] == "--" {
		argv = argv[1:]
	}
	if len(argv) == 0 {
		printRunHelp(r.Stderr)
		return r.failUsage("run: missing command (use: zcl run -- <cmd> ...)")
	}
	if *captureRaw && (attemptMeta.Mode == "ci" || envBoolish("CI")) && !envBoolish("ZCL_ALLOW_UNSAFE_CAPTURE") {
		printRunHelp(r.Stderr)
		return r.failUsage("run: --capture-raw is disabled in ci/strict environments unless ZCL_ALLOW_UNSAFE_CAPTURE=1")
	}

	if threshold := repeatGuardMaxStreak(); threshold > 0 {
		tracePath := filepath.Join(env.OutDirAbs, "tool.calls.jsonl")
		streak, err := trailingFailedRepeatStreak(tracePath, argv)
		if err != nil {
			fmt.Fprintf(r.Stderr, codeIO+": failed to inspect repeat guard state: %s\n", err.Error())
			return 1
		}
		if streak >= threshold {
			now := r.Now()
			msg := fmt.Sprintf("no-progress guard: refusing repeated failed command after streak=%d (max=%d)", streak, threshold)
			traceRes := trace.ResultForTrace{
				SpawnError: codeToolFailed,
				DurationMs: 0,
				OutBytes:   0,
				ErrBytes:   int64(len(msg)),
				ErrPreview: msg,
			}
			if err := trace.AppendCLIRunEvent(now, env, argv, traceRes); err != nil {
				fmt.Fprintf(r.Stderr, codeIO+": failed to append tool.calls.jsonl: %s\n", err.Error())
				return 1
			}
			fmt.Fprintf(r.Stderr, codeToolFailed+": %s\n", msg)
			return 1
		}
	}

	now := r.Now()
	if _, err := attempt.EnsureTimeoutAnchor(now, env.OutDirAbs); err != nil {
		fmt.Fprintf(r.Stderr, codeIO+": %s\n", err.Error())
		return 1
	}
	ctx, cancel, timedOut := attemptCtxForDeadline(now, env.OutDirAbs)
	if cancel != nil {
		defer cancel()
	}

	var (
		outFull                  io.Writer
		errFull                  io.Writer
		outRel                   string
		errRel                   string
		outBuf                   *boundedBuffer
		errBuf                   *boundedBuffer
		captureRedactionsApplied []string
	)
	if *capture {
		if *captureMaxBytes <= 0 {
			printRunHelp(r.Stderr)
			return r.failUsage("run: --capture-max-bytes must be > 0")
		}
		outBuf = newBoundedBuffer(*captureMaxBytes)
		errBuf = newBoundedBuffer(*captureMaxBytes)
		outFull = outBuf
		errFull = errBuf
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
	if outBuf != nil || errBuf != nil {
		dir := filepath.Join(env.OutDirAbs, "captures", "cli")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			fmt.Fprintf(r.Stderr, codeIO+": %s\n", err.Error())
			return 1
		}
		id := fmt.Sprintf("%d", now.UTC().UnixNano())
		outRel = filepath.Join("captures", "cli", id+".stdout.log")
		errRel = filepath.Join("captures", "cli", id+".stderr.log")
		traceRes.CapturedStdoutPath = outRel
		traceRes.CapturedStderrPath = errRel

		writeCap := func(rel string, buf *boundedBuffer, fallback []byte) (written int64, shaHex string, truncated bool, applied []string, ok bool) {
			if buf == nil {
				return 0, "", false, nil, true
			}
			b := buf.Bytes()
			if len(b) == 0 && len(fallback) > 0 {
				b = fallback
			}
			trunc := buf.Truncated()

			if !*captureRaw {
				red, a := redact.Text(string(b))
				b = []byte(red)
				applied = a.Names
			}
			if *captureMaxBytes > 0 && int64(len(b)) > *captureMaxBytes {
				b = b[:*captureMaxBytes]
				trunc = true
			}
			sum := sha256.Sum256(b)
			sha := hex.EncodeToString(sum[:])

			path := filepath.Join(env.OutDirAbs, rel)
			f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
			if err != nil {
				fmt.Fprintf(r.Stderr, codeIO+": %s\n", err.Error())
				return 0, "", false, nil, false
			}
			if _, err := f.Write(b); err != nil {
				_ = f.Close()
				fmt.Fprintf(r.Stderr, codeIO+": %s\n", err.Error())
				return 0, "", false, nil, false
			}
			_ = f.Sync()
			_ = f.Close()
			return int64(len(b)), sha, trunc, applied, true
		}

		var (
			ok         bool
			outApplied []string
			errApplied []string
			capApplied []string
		)
		traceRes.CapturedStdoutBytes, traceRes.CapturedStdoutSHA256, traceRes.CapturedStdoutTruncated, outApplied, ok = writeCap(outRel, outBuf, []byte(res.OutPreview))
		if !ok {
			return 1
		}
		traceRes.CapturedStderrBytes, traceRes.CapturedStderrSHA256, traceRes.CapturedStderrTruncated, errApplied, ok = writeCap(errRel, errBuf, []byte(res.ErrPreview))
		if !ok {
			return 1
		}

		seen := map[string]bool{}
		for _, s := range append(append([]string(nil), outApplied...), errApplied...) {
			if s == "" || seen[s] {
				continue
			}
			seen[s] = true
			capApplied = append(capApplied, s)
		}
		// Keep deterministic ordering for diffability.
		sort.Strings(capApplied)

		// Stash for captures.jsonl event below.
		captureRedactionsApplied = capApplied
	}
	if timedOut || errors.Is(runErr, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
		traceRes.SpawnError = codeTimeout
	} else if runErr != nil {
		traceRes.SpawnError = codeSpawn
	}
	resultCode := traceRes.SpawnError
	if resultCode == "" && res.ExitCode != 0 {
		resultCode = codeToolFailed
	}
	if err := trace.AppendCLIRunEvent(now, env, argv, traceRes); err != nil {
		fmt.Fprintf(r.Stderr, codeIO+": failed to append tool.calls.jsonl: %s\n", err.Error())
		return 1
	}

	// Secondary evidence index for capture mode.
	if *capture && outRel != "" && errRel != "" {
		redArgv := make([]string, 0, len(argv))
		for _, s := range argv {
			red, _ := redact.Text(s)
			redArgv = append(redArgv, red)
		}
		capApplied := captureRedactionsApplied
		ev := schema.CaptureEventV1{
			V:                 1,
			TS:                now.UTC().Format(time.RFC3339Nano),
			RunID:             env.RunID,
			SuiteID:           env.SuiteID,
			MissionID:         env.MissionID,
			AttemptID:         env.AttemptID,
			AgentID:           env.AgentID,
			Tool:              "cli",
			Op:                "exec",
			Input:             boundedArgvInputJSON(redArgv),
			StdoutPath:        outRel,
			StderrPath:        errRel,
			StdoutBytes:       traceRes.CapturedStdoutBytes,
			StderrBytes:       traceRes.CapturedStderrBytes,
			StdoutSHA256:      traceRes.CapturedStdoutSHA256,
			StderrSHA256:      traceRes.CapturedStderrSHA256,
			StdoutTruncated:   traceRes.CapturedStdoutTruncated,
			StderrTruncated:   traceRes.CapturedStderrTruncated,
			Redacted:          !*captureRaw,
			RedactionsApplied: capApplied,
			MaxBytes:          *captureMaxBytes,
		}
		if err := store.AppendJSONL(filepath.Join(env.OutDirAbs, "captures.jsonl"), ev); err != nil {
			fmt.Fprintf(r.Stderr, codeIO+": failed to append captures.jsonl: %s\n", err.Error())
			return 1
		}
	}
	if timedOut || errors.Is(runErr, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
		fmt.Fprintf(r.Stderr, codeTimeout+": attempt deadline exceeded\n")
		return 1
	}
	if runErr != nil {
		fmt.Fprintf(r.Stderr, codeIO+": run failed: %s\n", runErr.Error())
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
			Code:                    resultCode,
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

const defaultRepeatGuardMaxStreak = int64(50)

func repeatGuardMaxStreak() int64 {
	raw := strings.TrimSpace(os.Getenv("ZCL_REPEAT_GUARD_MAX_STREAK"))
	if raw == "" {
		return defaultRepeatGuardMaxStreak
	}
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return defaultRepeatGuardMaxStreak
	}
	if n <= 0 {
		return 0
	}
	return n
}

type traceGuardEvent struct {
	Tool  string `json:"tool"`
	Op    string `json:"op"`
	Input struct {
		Argv []string `json:"argv"`
	} `json:"input"`
	Result struct {
		OK bool `json:"ok"`
	} `json:"result"`
}

func trailingFailedRepeatStreak(tracePath string, argv []string) (int64, error) {
	f, err := os.Open(tracePath)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}
	defer func() { _ = f.Close() }()

	events := make([]traceGuardEvent, 0, 64)
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var ev traceGuardEvent
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			continue
		}
		if ev.Tool != "cli" || ev.Op != "exec" {
			continue
		}
		events = append(events, ev)
	}
	if err := sc.Err(); err != nil {
		return 0, err
	}

	target := strings.Join(argv, "\x00")
	var streak int64
	for i := len(events) - 1; i >= 0; i-- {
		ev := events[i]
		if strings.Join(ev.Input.Argv, "\x00") != target || ev.Result.OK {
			break
		}
		streak++
	}
	return streak, nil
}

func attemptCtxForDeadline(now time.Time, attemptDir string) (context.Context, context.CancelFunc, bool) {
	a, err := attempt.ReadAttempt(attemptDir)
	if err != nil {
		return context.Background(), nil, false
	}
	if a.TimeoutMs <= 0 || strings.TrimSpace(a.StartedAt) == "" {
		return context.Background(), nil, false
	}
	startAt := strings.TrimSpace(a.StartedAt)
	timeoutStart := strings.TrimSpace(a.TimeoutStart)
	if timeoutStart == "" {
		timeoutStart = schema.TimeoutStartAttemptStartV1
	}
	if timeoutStart == schema.TimeoutStartFirstToolCallV1 {
		if strings.TrimSpace(a.TimeoutStartedAt) == "" {
			return context.Background(), nil, false
		}
		startAt = strings.TrimSpace(a.TimeoutStartedAt)
	}
	start, err := time.Parse(time.RFC3339Nano, startAt)
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
  zcl run [--capture [--capture-raw] --capture-max-bytes N] -- <cmd> [args...]
  zcl run --envelope --json [--capture [--capture-raw] --capture-max-bytes N] -- <cmd> [args...]
`)
}
