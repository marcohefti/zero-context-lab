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

	"github.com/marcohefti/zero-context-lab/internal/contexts/evidence/app/redact"
	"github.com/marcohefti/zero-context-lab/internal/contexts/evidence/app/trace"
	"github.com/marcohefti/zero-context-lab/internal/contexts/execution/app/attempt"
	clifunnel "github.com/marcohefti/zero-context-lab/internal/kernel/cli_funnel"
	"github.com/marcohefti/zero-context-lab/internal/kernel/schema"
	"github.com/marcohefti/zero-context-lab/internal/kernel/store"
)

type runOptions struct {
	capture         bool
	captureMaxBytes int64
	captureRaw      bool
	envelope        bool
	argv            []string
}

type runCaptureState struct {
	outFull io.Writer
	errFull io.Writer

	outBuf *boundedBuffer
	errBuf *boundedBuffer

	outRel            string
	errRel            string
	redactionsApplied []string
}

type runCaptureWrite struct {
	bytes     int64
	sha256    string
	truncated bool
	applied   []string
}

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

func (r Runner) runRun(args []string) int {
	opts, exit, done := r.parseRunOptions(args)
	if done {
		return exit
	}

	env, attemptMeta, exit, done := r.loadRunAttemptContext()
	if done {
		return exit
	}
	if exit, done := r.validateRunCaptureSafety(opts, attemptMeta); done {
		return exit
	}
	if exit, done := r.applyRepeatGuard(env, opts.argv); done {
		return exit
	}

	now := r.Now()
	ctx, cancel, timedOut, exit, done := r.prepareRunContext(now, env.OutDirAbs)
	if done {
		return exit
	}
	if cancel != nil {
		defer cancel()
	}

	captureState, exit, done := r.prepareRunCapture(opts)
	if done {
		return exit
	}
	res, runErr := r.executeRunCommand(ctx, opts, captureState, timedOut)
	traceRes := baseRunTraceResult(opts.captureMaxBytes, res)
	if exit := r.persistRunCaptureArtifacts(now, env, opts, &traceRes, &captureState, res); exit != 0 {
		return exit
	}

	applyRunSpawnError(&traceRes, timedOut, runErr, ctx)
	resultCode := runTraceResultCode(traceRes, res.ExitCode)

	if exit := r.appendRunTraceEvent(now, env, opts.argv, traceRes); exit != 0 {
		return exit
	}
	if exit := r.appendRunCaptureEvent(now, env, opts, captureState, traceRes); exit != 0 {
		return exit
	}

	if exit, done := r.handleRunExecutionError(timedOut, runErr, ctx); done {
		return exit
	}
	if opts.envelope {
		if exit := r.writeRunEnvelope(env, opts, captureState, res, traceRes, resultCode); exit != 0 {
			return exit
		}
	}
	if res.ExitCode != 0 {
		return res.ExitCode
	}
	return 0
}

func (r Runner) parseRunOptions(args []string) (runOptions, int, bool) {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	capture := fs.Bool("capture", false, "capture full stdout/stderr to files under the attempt dir (in addition to bounded previews in tool.calls.jsonl)")
	captureMaxBytes := fs.Int64("capture-max-bytes", schema.CaptureMaxBytesV1, "max bytes to capture per stream when using --capture")
	captureRaw := fs.Bool("capture-raw", false, "capture raw stdout/stderr (unsafe; may contain secrets)")
	envelope := fs.Bool("envelope", false, "print a JSON envelope instead of passthrough tool output (requires --json)")
	jsonOut := fs.Bool("json", false, "print JSON output (required with --envelope)")
	help := fs.Bool("help", false, "show help")

	if err := fs.Parse(args); err != nil {
		return runOptions{}, r.failUsage("run: invalid flags"), true
	}
	if *help {
		printRunHelp(r.Stdout)
		return runOptions{}, 0, true
	}
	if *envelope && !*jsonOut {
		printRunHelp(r.Stderr)
		return runOptions{}, r.failUsage("run: --envelope requires --json"), true
	}
	if !*envelope && *jsonOut {
		printRunHelp(r.Stderr)
		return runOptions{}, r.failUsage("run: --json is only supported with --envelope"), true
	}
	if *captureRaw && !*capture {
		printRunHelp(r.Stderr)
		return runOptions{}, r.failUsage("run: --capture-raw requires --capture"), true
	}

	argv := fs.Args()
	if len(argv) >= 1 && argv[0] == "--" {
		argv = argv[1:]
	}
	if len(argv) == 0 {
		printRunHelp(r.Stderr)
		return runOptions{}, r.failUsage("run: missing command (use: zcl run -- <cmd> ...)"), true
	}
	return runOptions{
		capture:         *capture,
		captureMaxBytes: *captureMaxBytes,
		captureRaw:      *captureRaw,
		envelope:        *envelope,
		argv:            argv,
	}, 0, false
}

func (r Runner) loadRunAttemptContext() (trace.Env, schema.AttemptJSONV1, int, bool) {
	env, err := trace.EnvFromProcess()
	if err != nil {
		printRunHelp(r.Stderr)
		return trace.Env{}, schema.AttemptJSONV1{}, r.failUsage("run: missing ZCL attempt context (run `zcl attempt start --json` and pass the returned env)"), true
	}
	attemptMeta, err := attempt.ReadAttempt(env.OutDirAbs)
	if err != nil {
		printRunHelp(r.Stderr)
		return trace.Env{}, schema.AttemptJSONV1{}, r.failUsage("run: missing/invalid attempt.json in ZCL_OUT_DIR (need zcl attempt start context)"), true
	}
	if attemptMeta.RunID != env.RunID || attemptMeta.SuiteID != env.SuiteID || attemptMeta.MissionID != env.MissionID || attemptMeta.AttemptID != env.AttemptID {
		printRunHelp(r.Stderr)
		return trace.Env{}, schema.AttemptJSONV1{}, r.failUsage("run: attempt.json ids do not match ZCL_* env (refuse to run)"), true
	}
	return env, attemptMeta, 0, false
}

func (r Runner) validateRunCaptureSafety(opts runOptions, attemptMeta schema.AttemptJSONV1) (int, bool) {
	if opts.captureRaw && (attemptMeta.Mode == "ci" || envBoolish("CI")) && !envBoolish("ZCL_ALLOW_UNSAFE_CAPTURE") {
		printRunHelp(r.Stderr)
		return r.failUsage("run: --capture-raw is disabled in ci/strict environments unless ZCL_ALLOW_UNSAFE_CAPTURE=1"), true
	}
	return 0, false
}

func (r Runner) applyRepeatGuard(env trace.Env, argv []string) (int, bool) {
	threshold := repeatGuardMaxStreak()
	if threshold <= 0 {
		return 0, false
	}
	tracePath := filepath.Join(env.OutDirAbs, "tool.calls.jsonl")
	streak, err := trailingFailedRepeatStreak(tracePath, argv)
	if err != nil {
		fmt.Fprintf(r.Stderr, codeIO+": failed to inspect repeat guard state: %s\n", err.Error())
		return 1, true
	}
	if streak < threshold {
		return 0, false
	}
	return r.blockRepeatGuardedRun(env, argv, streak, threshold), true
}

func (r Runner) blockRepeatGuardedRun(env trace.Env, argv []string, streak, threshold int64) int {
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

func (r Runner) prepareRunContext(now time.Time, attemptDir string) (context.Context, context.CancelFunc, bool, int, bool) {
	if _, err := attempt.EnsureTimeoutAnchor(now, attemptDir); err != nil {
		fmt.Fprintf(r.Stderr, codeIO+": %s\n", err.Error())
		return context.Background(), nil, false, 1, true
	}
	ctx, cancel, timedOut := attemptCtxForDeadline(now, attemptDir)
	return ctx, cancel, timedOut, 0, false
}

func (r Runner) prepareRunCapture(opts runOptions) (runCaptureState, int, bool) {
	if !opts.capture {
		return runCaptureState{}, 0, false
	}
	if opts.captureMaxBytes <= 0 {
		printRunHelp(r.Stderr)
		return runCaptureState{}, r.failUsage("run: --capture-max-bytes must be > 0"), true
	}
	outBuf := newBoundedBuffer(opts.captureMaxBytes)
	errBuf := newBoundedBuffer(opts.captureMaxBytes)
	return runCaptureState{
		outFull: outBuf,
		errFull: errBuf,
		outBuf:  outBuf,
		errBuf:  errBuf,
	}, 0, false
}

func (r Runner) executeRunCommand(ctx context.Context, opts runOptions, captureState runCaptureState, timedOut bool) (clifunnel.Result, error) {
	if timedOut {
		return clifunnel.Result{}, context.DeadlineExceeded
	}
	var toolStdout io.Writer = r.Stdout
	var toolStderr io.Writer = r.Stderr
	if opts.envelope {
		toolStdout = io.Discard
		toolStderr = io.Discard
	}
	return clifunnel.Run(ctx, opts.argv, nil, toolStdout, toolStderr, captureState.outFull, captureState.errFull, schema.PreviewMaxBytesV1)
}

func baseRunTraceResult(captureMaxBytes int64, res clifunnel.Result) trace.ResultForTrace {
	return trace.ResultForTrace{
		ExitCode:        res.ExitCode,
		DurationMs:      res.DurationMs,
		OutBytes:        res.OutBytes,
		ErrBytes:        res.ErrBytes,
		OutPreview:      res.OutPreview,
		ErrPreview:      res.ErrPreview,
		OutTruncated:    res.OutTruncated,
		ErrTruncated:    res.ErrTruncated,
		CaptureMaxBytes: captureMaxBytes,
	}
}

func (r Runner) persistRunCaptureArtifacts(now time.Time, env trace.Env, opts runOptions, traceRes *trace.ResultForTrace, captureState *runCaptureState, res clifunnel.Result) int {
	if captureState.outBuf == nil && captureState.errBuf == nil {
		return 0
	}
	dir := filepath.Join(env.OutDirAbs, "captures", "cli")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		fmt.Fprintf(r.Stderr, codeIO+": %s\n", err.Error())
		return 1
	}
	id := fmt.Sprintf("%d", now.UTC().UnixNano())
	captureState.outRel = filepath.Join("captures", "cli", id+".stdout.log")
	captureState.errRel = filepath.Join("captures", "cli", id+".stderr.log")
	traceRes.CapturedStdoutPath = captureState.outRel
	traceRes.CapturedStderrPath = captureState.errRel

	outWritten, ok := r.writeRunCaptureFile(env.OutDirAbs, captureState.outRel, captureState.outBuf, []byte(res.OutPreview), opts.captureMaxBytes, opts.captureRaw)
	if !ok {
		return 1
	}
	errWritten, ok := r.writeRunCaptureFile(env.OutDirAbs, captureState.errRel, captureState.errBuf, []byte(res.ErrPreview), opts.captureMaxBytes, opts.captureRaw)
	if !ok {
		return 1
	}
	traceRes.CapturedStdoutBytes = outWritten.bytes
	traceRes.CapturedStdoutSHA256 = outWritten.sha256
	traceRes.CapturedStdoutTruncated = outWritten.truncated
	traceRes.CapturedStderrBytes = errWritten.bytes
	traceRes.CapturedStderrSHA256 = errWritten.sha256
	traceRes.CapturedStderrTruncated = errWritten.truncated
	captureState.redactionsApplied = sortedUniqueStrings(append(append([]string{}, outWritten.applied...), errWritten.applied...))
	return 0
}

func (r Runner) writeRunCaptureFile(outDirAbs, rel string, buf *boundedBuffer, fallback []byte, captureMaxBytes int64, captureRaw bool) (runCaptureWrite, bool) {
	if buf == nil {
		return runCaptureWrite{}, true
	}
	b := buf.Bytes()
	if len(b) == 0 && len(fallback) > 0 {
		b = fallback
	}
	trunc := buf.Truncated()

	var applied []string
	if !captureRaw {
		red, a := redact.Text(string(b))
		b = []byte(red)
		applied = a.Names
	}
	if captureMaxBytes > 0 && int64(len(b)) > captureMaxBytes {
		b = b[:captureMaxBytes]
		trunc = true
	}
	sum := sha256.Sum256(b)
	sha := hex.EncodeToString(sum[:])

	path := filepath.Join(outDirAbs, rel)
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		fmt.Fprintf(r.Stderr, codeIO+": %s\n", err.Error())
		return runCaptureWrite{}, false
	}
	if _, err := f.Write(b); err != nil {
		_ = f.Close()
		fmt.Fprintf(r.Stderr, codeIO+": %s\n", err.Error())
		return runCaptureWrite{}, false
	}
	_ = f.Sync()
	_ = f.Close()
	return runCaptureWrite{
		bytes:     int64(len(b)),
		sha256:    sha,
		truncated: trunc,
		applied:   applied,
	}, true
}

func sortedUniqueStrings(parts []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(parts))
	for _, s := range parts {
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

func applyRunSpawnError(traceRes *trace.ResultForTrace, timedOut bool, runErr error, ctx context.Context) {
	if timedOut || errors.Is(runErr, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
		traceRes.SpawnError = codeTimeout
		return
	}
	if runErr != nil {
		traceRes.SpawnError = codeSpawn
	}
}

func runTraceResultCode(traceRes trace.ResultForTrace, exitCode int) string {
	if traceRes.SpawnError != "" {
		return traceRes.SpawnError
	}
	if exitCode != 0 {
		return codeToolFailed
	}
	return ""
}

func (r Runner) appendRunTraceEvent(now time.Time, env trace.Env, argv []string, traceRes trace.ResultForTrace) int {
	if err := trace.AppendCLIRunEvent(now, env, argv, traceRes); err != nil {
		fmt.Fprintf(r.Stderr, codeIO+": failed to append tool.calls.jsonl: %s\n", err.Error())
		return 1
	}
	return 0
}

func (r Runner) appendRunCaptureEvent(now time.Time, env trace.Env, opts runOptions, captureState runCaptureState, traceRes trace.ResultForTrace) int {
	if !opts.capture || captureState.outRel == "" || captureState.errRel == "" {
		return 0
	}
	redArgv := make([]string, 0, len(opts.argv))
	for _, s := range opts.argv {
		red, _ := redact.Text(s)
		redArgv = append(redArgv, red)
	}
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
		StdoutPath:        captureState.outRel,
		StderrPath:        captureState.errRel,
		StdoutBytes:       traceRes.CapturedStdoutBytes,
		StderrBytes:       traceRes.CapturedStderrBytes,
		StdoutSHA256:      traceRes.CapturedStdoutSHA256,
		StderrSHA256:      traceRes.CapturedStderrSHA256,
		StdoutTruncated:   traceRes.CapturedStdoutTruncated,
		StderrTruncated:   traceRes.CapturedStderrTruncated,
		Redacted:          !opts.captureRaw,
		RedactionsApplied: captureState.redactionsApplied,
		MaxBytes:          opts.captureMaxBytes,
	}
	if err := store.AppendJSONL(filepath.Join(env.OutDirAbs, "captures.jsonl"), ev); err != nil {
		fmt.Fprintf(r.Stderr, codeIO+": failed to append captures.jsonl: %s\n", err.Error())
		return 1
	}
	return 0
}

func (r Runner) handleRunExecutionError(timedOut bool, runErr error, ctx context.Context) (int, bool) {
	if timedOut || errors.Is(runErr, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
		fmt.Fprintf(r.Stderr, codeTimeout+": attempt deadline exceeded\n")
		return 1, true
	}
	if runErr != nil {
		fmt.Fprintf(r.Stderr, codeIO+": run failed: %s\n", runErr.Error())
		return 1, true
	}
	return 0, false
}

func (r Runner) writeRunEnvelope(
	env trace.Env,
	opts runOptions,
	captureState runCaptureState,
	res clifunnel.Result,
	traceRes trace.ResultForTrace,
	resultCode string,
) int {
	out := runEnvelopeJSON{
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
		CapturedStdoutPath:      captureState.outRel,
		CapturedStderrPath:      captureState.errRel,
		CapturedStdoutBytes:     traceRes.CapturedStdoutBytes,
		CapturedStderrBytes:     traceRes.CapturedStderrBytes,
		CapturedStdoutSHA256:    traceRes.CapturedStdoutSHA256,
		CapturedStderrSHA256:    traceRes.CapturedStderrSHA256,
		CapturedStdoutTruncated: traceRes.CapturedStdoutTruncated,
		CapturedStderrTruncated: traceRes.CapturedStderrTruncated,
		CaptureMaxBytes:         opts.captureMaxBytes,
		RunID:                   env.RunID,
		SuiteID:                 env.SuiteID,
		MissionID:               env.MissionID,
		AttemptID:               env.AttemptID,
	}
	return r.writeJSON(out)
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
	events, err := readTraceGuardEvents(tracePath)
	if err != nil {
		return 0, err
	}
	return countFailedRepeatStreak(events, argv), nil
}

func readTraceGuardEvents(tracePath string) ([]traceGuardEvent, error) {
	f, err := os.Open(tracePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
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
		return nil, err
	}
	return events, nil
}

func countFailedRepeatStreak(events []traceGuardEvent, argv []string) int64 {
	target := strings.Join(argv, "\x00")
	var streak int64
	for i := len(events) - 1; i >= 0; i-- {
		ev := events[i]
		if strings.Join(ev.Input.Argv, "\x00") != target || ev.Result.OK {
			break
		}
		streak++
	}
	return streak
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
