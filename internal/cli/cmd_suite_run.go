package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/marcohefti/zero-context-lab/internal/attempt"
	"github.com/marcohefti/zero-context-lab/internal/config"
	"github.com/marcohefti/zero-context-lab/internal/expect"
	"github.com/marcohefti/zero-context-lab/internal/planner"
	"github.com/marcohefti/zero-context-lab/internal/redact"
	"github.com/marcohefti/zero-context-lab/internal/report"
	"github.com/marcohefti/zero-context-lab/internal/schema"
	"github.com/marcohefti/zero-context-lab/internal/store"
	"github.com/marcohefti/zero-context-lab/internal/validate"
)

type suiteRunReportErr struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type suiteRunFinishResult struct {
	OK           bool               `json:"ok"`
	Strict       bool               `json:"strict"`
	StrictExpect bool               `json:"strictExpect"`
	AttemptDir   string             `json:"attemptDir"`
	Report       any                `json:"report,omitempty"`
	ReportError  *suiteRunReportErr `json:"reportError,omitempty"`
	Validate     validate.Result    `json:"validate,omitempty"`
	Expect       expect.Result      `json:"expect,omitempty"`
	IOError      string             `json:"ioError,omitempty"`
}

type suiteRunAttemptResult struct {
	MissionID  string `json:"missionId"`
	AttemptID  string `json:"attemptId"`
	AttemptDir string `json:"attemptDir"`

	RunnerExitCode  *int   `json:"runnerExitCode,omitempty"`
	RunnerErrorCode string `json:"runnerErrorCode,omitempty"` // ZCL_E_TIMEOUT|ZCL_E_SPAWN

	Finish suiteRunFinishResult `json:"finish"`

	OK bool `json:"ok"`
}

type suiteRunSummary struct {
	OK      bool   `json:"ok"`
	RunID   string `json:"runId"`
	SuiteID string `json:"suiteId"`
	Mode    string `json:"mode"`
	OutRoot string `json:"outRoot"`

	Attempts []suiteRunAttemptResult `json:"attempts"`

	Passed int `json:"passed"`
	Failed int `json:"failed"`

	CreatedAt string `json:"createdAt"`
}

type stringListFlag []string

func (s *stringListFlag) String() string { return strings.Join([]string(*s), ",") }
func (s *stringListFlag) Set(v string) error {
	v = strings.TrimSpace(v)
	if v == "" {
		return nil
	}
	*s = append(*s, v)
	return nil
}

func (r Runner) runSuiteRun(args []string) int {
	fs := flag.NewFlagSet("suite run", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	file := fs.String("file", "", "suite file path (.json|.yaml|.yml) (required)")
	runID := fs.String("run-id", "", "existing run id (optional)")
	mode := fs.String("mode", "", "optional mode override: discovery|ci (default from suite file)")
	timeoutMs := fs.Int64("timeout-ms", 0, "optional attempt timeout override in ms (default from suite defaults.timeoutMs)")
	outRoot := fs.String("out-root", "", "project output root (default from config/env, else .zcl)")
	strict := fs.Bool("strict", true, "run finish in strict mode (enforces evidence + contract)")
	strictExpect := fs.Bool("strict-expect", true, "strict mode for expect (missing suite.json/feedback.json fails)")
	captureRunnerIO := fs.Bool("capture-runner-io", true, "capture runner stdout/stderr to runner.* logs under the attempt dir")
	runnerIOMaxBytes := fs.Int64("runner-io-max-bytes", schema.CaptureMaxBytesV1, "max bytes to keep per runner stream when using --capture-runner-io (tail)")
	runnerIORaw := fs.Bool("runner-io-raw", false, "capture raw runner stdout/stderr (unsafe; may contain secrets)")

	var shims stringListFlag
	fs.Var(&shims, "shim", "install attempt-local shims for tool binaries (repeatable; e.g. --shim surfwright)")

	jsonOut := fs.Bool("json", false, "print JSON output (required)")
	help := fs.Bool("help", false, "show help")

	if err := fs.Parse(args); err != nil {
		return r.failUsage("suite run: invalid flags")
	}
	if *help {
		printSuiteRunHelp(r.Stdout)
		return 0
	}
	if !*jsonOut {
		printSuiteRunHelp(r.Stderr)
		return r.failUsage("suite run: require --json for stable output")
	}

	argv := fs.Args()
	if len(argv) >= 1 && argv[0] == "--" {
		argv = argv[1:]
	}
	if len(argv) == 0 {
		printSuiteRunHelp(r.Stderr)
		return r.failUsage("suite run: missing runner command (use: zcl suite run ... -- <runner-cmd> ...)")
	}

	m, err := config.LoadMerged(*outRoot)
	if err != nil {
		fmt.Fprintf(r.Stderr, "ZCL_E_IO: %s\n", err.Error())
		return 1
	}

	plan, err := planner.PlanSuite(r.Now(), planner.SuitePlanOpts{
		OutRoot:   m.OutRoot,
		RunID:     strings.TrimSpace(*runID),
		SuiteFile: strings.TrimSpace(*file),
		Mode:      strings.TrimSpace(*mode),
		TimeoutMs: *timeoutMs,
	})
	if err != nil {
		fmt.Fprintf(r.Stderr, "ZCL_E_USAGE: %s\n", err.Error())
		return 2
	}

	summary := suiteRunSummary{
		OK:        true,
		RunID:     plan.RunID,
		SuiteID:   plan.SuiteID,
		Mode:      plan.Mode,
		OutRoot:   plan.OutRoot,
		CreatedAt: r.Now().UTC().Format(time.RFC3339Nano),
	}

	// Keep stdout reserved for JSON; runner output is streamed to stderr.
	runnerCmd := argv[0]
	runnerArgs := argv[1:]
	zclExe, _ := os.Executable()
	if zclExe != "" {
		base := strings.ToLower(filepath.Base(zclExe))
		if base != "zcl" && base != "zcl.exe" {
			// suite run is expected to be invoked via the zcl binary; avoid wiring a misleading path.
			zclExe = ""
		}
	}

	harnessErr := false
	for _, pm := range plan.Missions {
		ar := suiteRunAttemptResult{
			MissionID:  pm.MissionID,
			AttemptID:  pm.AttemptID,
			AttemptDir: pm.OutDirAbs,
			Finish: suiteRunFinishResult{
				OK:           false,
				Strict:       *strict,
				StrictExpect: *strictExpect,
				AttemptDir:   pm.OutDirAbs,
			},
			OK: false,
		}

		env := map[string]string{}
		for k, v := range pm.Env {
			env[k] = v
		}
		if p := filepath.Join(pm.OutDirAbs, "prompt.txt"); fileExists(p) {
			env["ZCL_PROMPT_PATH"] = p
		}
		if zclExe != "" {
			env["ZCL_SHIM_ZCL_PATH"] = zclExe
		}

		var shimBinDir string
		if len(shims) > 0 {
			dir, err := installAttemptShims(pm.OutDirAbs, []string(shims))
			if err != nil {
				harnessErr = true
				ar.RunnerErrorCode = "ZCL_E_USAGE"
				fmt.Fprintf(r.Stderr, "ZCL_E_USAGE: suite run: %s\n", err.Error())
			} else {
				shimBinDir = dir
				env["ZCL_SHIM_BIN_DIR"] = shimBinDir
				// Prepend to PATH so the agent can type the tool name and still be traced.
				env["PATH"] = shimBinDir + ":" + os.Getenv("PATH")
				// SurfWright state isolation (attempt-local) by default when shimming.
				if _, ok := env["SURFWRIGHT_STATE_DIR"]; !ok && strings.Contains(" "+strings.Join([]string(shims), " ")+" ", " surfwright ") && env["ZCL_TMP_DIR"] != "" {
					env["SURFWRIGHT_STATE_DIR"] = filepath.Join(env["ZCL_TMP_DIR"], "surfwright-state")
				}
			}
		}

		// Runner IO capture buffers (tail) + paths.
		var (
			stdoutTB *tailBuffer
			stderrTB *tailBuffer
		)
		var logW *runnerLogWriter
		var stopLogs chan struct{}
		var logErrCh chan error
		stopRunnerLogs := func() {
			if stopLogs == nil {
				return
			}
			close(stopLogs)
			stopLogs = nil
			if logErrCh != nil {
				if lerr := <-logErrCh; lerr != nil {
					harnessErr = true
					ar.RunnerErrorCode = "ZCL_E_IO"
					fmt.Fprintf(r.Stderr, "ZCL_E_IO: suite run: %s\n", lerr.Error())
				}
				logErrCh = nil
			}
		}
		if *captureRunnerIO {
			if *runnerIOMaxBytes <= 0 {
				harnessErr = true
				ar.RunnerErrorCode = "ZCL_E_USAGE"
				fmt.Fprintf(r.Stderr, "ZCL_E_USAGE: suite run: --runner-io-max-bytes must be > 0\n")
			} else {
				stdoutTB = newTailBuffer(*runnerIOMaxBytes)
				stderrTB = newTailBuffer(*runnerIOMaxBytes)
				_ = writeRunnerCommandFile(pm.OutDirAbs, runnerCmd, runnerArgs, env, shimBinDir)
				logW = &runnerLogWriter{
					AttemptDir: pm.OutDirAbs,
					StdoutTB:   stdoutTB,
					StderrTB:   stderrTB,
					Raw:        *runnerIORaw,
				}
				// Create initial log artifacts so post-mortems always have the files.
				if err := logW.Flush(true); err != nil {
					harnessErr = true
					ar.RunnerErrorCode = "ZCL_E_IO"
					fmt.Fprintf(r.Stderr, "ZCL_E_IO: suite run: %s\n", err.Error())
				} else {
					stopLogs = make(chan struct{})
					logErrCh = make(chan error, 1)
					go func() {
						t := time.NewTicker(250 * time.Millisecond)
						defer t.Stop()
						for {
							select {
							case <-t.C:
								if err := logW.Flush(false); err != nil {
									logErrCh <- err
									return
								}
							case <-stopLogs:
								logErrCh <- logW.Flush(true)
								return
							}
						}
					}()
				}
			}
		} else {
			_ = writeRunnerCommandFile(pm.OutDirAbs, runnerCmd, runnerArgs, env, shimBinDir)
		}

		if err := verifyAttemptMatchesEnv(pm.OutDirAbs, env); err != nil {
			harnessErr = true
			ar.RunnerErrorCode = "ZCL_E_USAGE"
			fmt.Fprintf(r.Stderr, "ZCL_E_USAGE: suite run: %s\n", err.Error())
			stopRunnerLogs()
		} else {
			now := r.Now()
			ctx, cancel, timedOut := attemptCtxForDeadline(now, pm.OutDirAbs)
			if timedOut {
				ar.RunnerErrorCode = "ZCL_E_TIMEOUT"
				stopRunnerLogs()
			} else {
				fmt.Fprintf(r.Stderr, "suite run: mission=%s attempt=%s runner=%s\n", pm.MissionID, pm.AttemptID, filepath.Base(runnerCmd))

				cmd := exec.CommandContext(ctx, runnerCmd, runnerArgs...)
				cmd.Env = mergeEnviron(os.Environ(), env)
				cmd.Stdin = os.Stdin
				if stdoutTB != nil && stderrTB != nil {
					cmd.Stdout = io.MultiWriter(r.Stderr, stdoutTB)
					cmd.Stderr = io.MultiWriter(r.Stderr, stderrTB)
				} else {
					cmd.Stdout = r.Stderr
					cmd.Stderr = r.Stderr
				}

				err := cmd.Run()
				if cmd.ProcessState != nil {
					ec := cmd.ProcessState.ExitCode()
					ar.RunnerExitCode = &ec
				} else if err == nil {
					ec := 0
					ar.RunnerExitCode = &ec
				}

				if err != nil {
					if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
						ar.RunnerErrorCode = "ZCL_E_TIMEOUT"
						harnessErr = true
					} else if isStartFailure(err) {
						ar.RunnerErrorCode = "ZCL_E_SPAWN"
						harnessErr = true
					} else {
						// Process exited non-zero: treat as harness error (runner is expected to encode mission outcome in feedback.json).
						harnessErr = true
					}
				} else if ctx.Err() != nil && errors.Is(ctx.Err(), context.DeadlineExceeded) {
					// Defensive: CommandContext can return nil error while ctx is done in some edge cases.
					ar.RunnerErrorCode = "ZCL_E_TIMEOUT"
					harnessErr = true
				}
				stopRunnerLogs()
			}
			if cancel != nil {
				cancel()
			}
		}

		ar.Finish = finishAttempt(r.Now(), pm.OutDirAbs, *strict, *strictExpect)
		runnerOK := ar.RunnerErrorCode == "" && ar.RunnerExitCode != nil && *ar.RunnerExitCode == 0
		ar.OK = runnerOK && ar.Finish.OK

		if ar.OK {
			summary.Passed++
		} else {
			summary.Failed++
			summary.OK = false
		}
		summary.Attempts = append(summary.Attempts, ar)
	}

	enc := json.NewEncoder(r.Stdout)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	if err := enc.Encode(summary); err != nil {
		fmt.Fprintf(r.Stderr, "ZCL_E_IO: failed to encode json\n")
		return 1
	}

	if harnessErr {
		return 1
	}
	if summary.OK {
		return 0
	}
	return 2
}

func printSuiteRunHelp(w io.Writer) {
	fmt.Fprint(w, `Usage:
  zcl suite run --file <suite.(yaml|yml|json)> [--run-id <runId>] [--mode discovery|ci] [--timeout-ms N] [--out-root .zcl] [--strict] [--strict-expect] [--shim <bin>] [--capture-runner-io] --json -- <runner-cmd> [args...]

Notes:
  - Requires --json (stdout is reserved for JSON; runner stdout/stderr is streamed to stderr).
  - The runner is spawned once per planned mission attempt with ZCL_* env set (from suite plan / attempt start).
  - When --shim is used, ZCL prepends an attempt-local bin dir to PATH so the agent can type the tool name (e.g. surfwright) and still have invocations traced via zcl run.
  - After the runner exits, ZCL finishes each attempt (report + validate + expect).
`)
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func verifyAttemptMatchesEnv(attemptDir string, env map[string]string) error {
	a, err := attempt.ReadAttempt(attemptDir)
	if err != nil {
		return fmt.Errorf("suite run: missing/invalid attempt.json in attemptDir=%s", attemptDir)
	}
	if a.RunID != env["ZCL_RUN_ID"] || a.SuiteID != env["ZCL_SUITE_ID"] || a.MissionID != env["ZCL_MISSION_ID"] || a.AttemptID != env["ZCL_ATTEMPT_ID"] {
		return fmt.Errorf("suite run: attempt.json ids do not match planned ZCL_* env (refuse to run) attemptDir=%s", attemptDir)
	}
	if od := env["ZCL_OUT_DIR"]; od != "" && od != attemptDir {
		return fmt.Errorf("suite run: ZCL_OUT_DIR mismatch (env=%s attemptDir=%s)", od, attemptDir)
	}
	return nil
}

func finishAttempt(now time.Time, attemptDir string, strict bool, strictExpect bool) suiteRunFinishResult {
	out := suiteRunFinishResult{
		OK:           false,
		Strict:       strict,
		StrictExpect: strictExpect,
		AttemptDir:   attemptDir,
	}

	rep, repErr := report.BuildAttemptReport(now, attemptDir, strict)
	if repErr == nil {
		out.Report = rep
		if err := report.WriteAttemptReportAtomic(filepath.Join(attemptDir, "attempt.report.json"), rep); err != nil {
			out.IOError = err.Error()
			return out
		}
	} else {
		var ce *report.CliError
		if errors.As(repErr, &ce) {
			out.ReportError = &suiteRunReportErr{Code: ce.Code, Message: ce.Message}
		} else {
			out.IOError = repErr.Error()
			return out
		}
	}

	valRes, err := validate.ValidatePath(attemptDir, strict)
	if err != nil {
		out.IOError = err.Error()
		return out
	}
	out.Validate = valRes

	expRes, err := expect.ExpectPath(attemptDir, strictExpect)
	if err != nil {
		out.IOError = err.Error()
		return out
	}
	out.Expect = expRes

	ok := valRes.OK && expRes.OK
	if repErr == nil && rep.OK != nil && !*rep.OK {
		ok = false
	}
	out.OK = ok && out.ReportError == nil
	return out
}

func isStartFailure(err error) bool {
	// exec.ExitError indicates the process was started; treat that separately.
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return false
	}
	var exErr *exec.Error
	if errors.As(err, &exErr) {
		return true
	}
	var pe *os.PathError
	if errors.As(err, &pe) {
		return true
	}
	return false
}

func mergeEnviron(base []string, overlay map[string]string) []string {
	m := map[string]string{}
	for _, kv := range base {
		k, v, ok := strings.Cut(kv, "=")
		if !ok {
			continue
		}
		m[k] = v
	}
	for k, v := range overlay {
		m[k] = v
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]string, 0, len(keys))
	for _, k := range keys {
		out = append(out, k+"="+m[k])
	}
	return out
}

func installAttemptShims(attemptDir string, bins []string) (string, error) {
	if len(bins) == 0 {
		return "", nil
	}
	dir := filepath.Join(attemptDir, "bin")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	for _, b := range bins {
		b = strings.TrimSpace(b)
		if b == "" {
			continue
		}
		// Minimal safety: reject path separators.
		if strings.Contains(b, "/") || strings.Contains(b, string(os.PathSeparator)) {
			return "", fmt.Errorf("invalid --shim %q (must be a bare command name)", b)
		}
		wrapper := shimWrapperScript(b)
		path := filepath.Join(dir, b)
		if err := os.WriteFile(path, []byte(wrapper), 0o755); err != nil {
			return "", err
		}
	}
	return dir, nil
}

func shimWrapperScript(bin string) string {
	// Keep this POSIX sh compatible.
	// It removes the shim dir from PATH to avoid recursion, then runs the logical command name through zcl run.
	return fmt.Sprintf(`#!/usr/bin/env sh
set -eu

if [ -z "${ZCL_SHIM_BIN_DIR:-}" ]; then
  echo "ZCL_E_SHIM: missing ZCL_SHIM_BIN_DIR" >&2
  exit 127
fi

# Prefer an explicit zcl path when provided, otherwise rely on PATH.
ZCL="${ZCL_SHIM_ZCL_PATH:-zcl}"

# Drop shim dir from PATH (it is expected to be first).
case "$PATH" in
  "$ZCL_SHIM_BIN_DIR":*) PATH="${PATH#${ZCL_SHIM_BIN_DIR}:}" ;;
esac
export PATH

%s

exec "$ZCL" run --capture -- "%s" "$@"
`, shimSurfwrightStateBlock(bin), bin)
}

func shimSurfwrightStateBlock(bin string) string {
	if bin != "surfwright" {
		return ""
	}
	return `# SurfWright state isolation (attempt-local) when not already set.
if [ -n "${ZCL_TMP_DIR:-}" ] && [ -z "${SURFWRIGHT_STATE_DIR:-}" ]; then
  SURFWRIGHT_STATE_DIR="${ZCL_TMP_DIR}/surfwright-state"
  export SURFWRIGHT_STATE_DIR
fi
`
}

func writeRunnerCommandFile(attemptDir string, runnerCmd string, runnerArgs []string, env map[string]string, shimBinDir string) error {
	path := filepath.Join(attemptDir, "runner.command.txt")
	// Best-effort: don't fail suite execution because this is secondary evidence.
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()

	fmt.Fprintf(f, "runner=%s\n", runnerCmd)
	if len(runnerArgs) > 0 {
		fmt.Fprintf(f, "args=%s\n", strings.Join(runnerArgs, " "))
	} else {
		fmt.Fprintf(f, "args=\n")
	}
	if shimBinDir != "" {
		fmt.Fprintf(f, "shimBinDir=%s\n", shimBinDir)
	}
	// Include the ZCL attempt ids for quick copy/paste in post-mortems.
	fmt.Fprintf(f, "ZCL_RUN_ID=%s\n", env["ZCL_RUN_ID"])
	fmt.Fprintf(f, "ZCL_SUITE_ID=%s\n", env["ZCL_SUITE_ID"])
	fmt.Fprintf(f, "ZCL_MISSION_ID=%s\n", env["ZCL_MISSION_ID"])
	fmt.Fprintf(f, "ZCL_ATTEMPT_ID=%s\n", env["ZCL_ATTEMPT_ID"])
	fmt.Fprintf(f, "ZCL_OUT_DIR=%s\n", env["ZCL_OUT_DIR"])
	fmt.Fprintf(f, "ZCL_TMP_DIR=%s\n", env["ZCL_TMP_DIR"])
	if v := env["SURFWRIGHT_STATE_DIR"]; v != "" {
		fmt.Fprintf(f, "SURFWRIGHT_STATE_DIR=%s\n", v)
	}
	if v := env["PATH"]; v != "" {
		fmt.Fprintf(f, "PATH=%s\n", v)
	}
	return nil
}

type runnerLogWriter struct {
	AttemptDir string
	StdoutTB   *tailBuffer
	StderrTB   *tailBuffer
	Raw        bool

	lastOutSeq uint64
	lastErrSeq uint64
}

func (w *runnerLogWriter) Flush(force bool) error {
	if w == nil || w.AttemptDir == "" || w.StdoutTB == nil || w.StderrTB == nil {
		return nil
	}

	stdoutPath := filepath.Join(w.AttemptDir, "runner.stdout.log")
	stderrPath := filepath.Join(w.AttemptDir, "runner.stderr.log")

	writeOne := func(path string, tb *tailBuffer, lastSeq *uint64) error {
		b, truncated, seq := tb.Snapshot()
		if !force && seq == *lastSeq {
			return nil
		}
		*lastSeq = seq

		s := string(b)
		if !w.Raw {
			red, _ := redact.Text(s)
			s = red
		}
		if truncated {
			if !strings.HasSuffix(s, "\n") {
				s += "\n"
			}
			s += "[ZCL_TRUNCATED]\n"
		}

		// Write atomically so a hard kill won't leave a partially-written log.
		return store.WriteFileAtomic(path, []byte(s))
	}

	if err := writeOne(stdoutPath, w.StdoutTB, &w.lastOutSeq); err != nil {
		return err
	}
	if err := writeOne(stderrPath, w.StderrTB, &w.lastErrSeq); err != nil {
		return err
	}
	return nil
}
