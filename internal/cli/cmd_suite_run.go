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
	"github.com/marcohefti/zero-context-lab/internal/report"
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

		if err := verifyAttemptMatchesEnv(pm.OutDirAbs, env); err != nil {
			harnessErr = true
			ar.RunnerErrorCode = "ZCL_E_USAGE"
			fmt.Fprintf(r.Stderr, "ZCL_E_USAGE: suite run: %s\n", err.Error())
		} else {
			now := r.Now()
			ctx, cancel, timedOut := attemptCtxForDeadline(now, pm.OutDirAbs)
			if timedOut {
				ar.RunnerErrorCode = "ZCL_E_TIMEOUT"
			} else {
				fmt.Fprintf(r.Stderr, "suite run: mission=%s attempt=%s runner=%s\n", pm.MissionID, pm.AttemptID, filepath.Base(runnerCmd))

				cmd := exec.CommandContext(ctx, runnerCmd, runnerArgs...)
				cmd.Env = mergeEnviron(os.Environ(), env)
				cmd.Stdin = os.Stdin
				cmd.Stdout = r.Stderr
				cmd.Stderr = r.Stderr

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
  zcl suite run --file <suite.(yaml|yml|json)> [--run-id <runId>] [--mode discovery|ci] [--timeout-ms N] [--out-root .zcl] [--strict] [--strict-expect] --json -- <runner-cmd> [args...]

Notes:
  - Requires --json (stdout is reserved for JSON; runner stdout/stderr is streamed to stderr).
  - The runner is spawned once per planned mission attempt with ZCL_* env set (from suite plan / attempt start).
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
