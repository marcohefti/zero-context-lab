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
	"sync"
	"sync/atomic"
	"time"

	"github.com/marcohefti/zero-context-lab/internal/attempt"
	"github.com/marcohefti/zero-context-lab/internal/blind"
	"github.com/marcohefti/zero-context-lab/internal/config"
	"github.com/marcohefti/zero-context-lab/internal/expect"
	"github.com/marcohefti/zero-context-lab/internal/feedback"
	"github.com/marcohefti/zero-context-lab/internal/planner"
	"github.com/marcohefti/zero-context-lab/internal/redact"
	"github.com/marcohefti/zero-context-lab/internal/report"
	"github.com/marcohefti/zero-context-lab/internal/schema"
	"github.com/marcohefti/zero-context-lab/internal/store"
	"github.com/marcohefti/zero-context-lab/internal/suite"
	"github.com/marcohefti/zero-context-lab/internal/trace"
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
	// IsolationModel records how the fresh session boundary was orchestrated.
	IsolationModel string `json:"isolationModel,omitempty"`

	RunnerExitCode   *int   `json:"runnerExitCode,omitempty"`
	RunnerErrorCode  string `json:"runnerErrorCode,omitempty"` // ZCL_E_TIMEOUT|ZCL_E_SPAWN|ZCL_E_CONTAMINATED_PROMPT
	AutoFeedback     bool   `json:"autoFeedback,omitempty"`
	AutoFeedbackCode string `json:"autoFeedbackCode,omitempty"`

	Finish suiteRunFinishResult `json:"finish"`

	OK bool `json:"ok"`
}

type suiteRunSummary struct {
	SchemaVersion int    `json:"schemaVersion"`
	OK            bool   `json:"ok"`
	RunID         string `json:"runId"`
	SuiteID       string `json:"suiteId"`
	Mode          string `json:"mode"`
	OutRoot       string `json:"outRoot"`
	// SessionIsolationRequested is the CLI selection (auto|process|native).
	SessionIsolationRequested string `json:"sessionIsolationRequested"`
	// SessionIsolation is the effective attempt isolation model.
	SessionIsolation string `json:"sessionIsolation"`
	// HostNativeSpawnCapable reflects ZCL_HOST_NATIVE_SPAWN parsing (informational).
	HostNativeSpawnCapable bool `json:"hostNativeSpawnCapable"`

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
	timeoutStart := fs.String("timeout-start", "", "optional timeout anchor override: attempt_start|first_tool_call")
	blindOverride := fs.String("blind", "", "optional blind-mode override: on|off")
	blindTermsCSV := fs.String("blind-terms", "", "optional comma-separated blind harness terms override")
	sessionIsolation := fs.String("session-isolation", "auto", "session isolation strategy: auto|process|native")
	parallel := fs.Int("parallel", 1, "max concurrent attempt waves (just-in-time allocation)")
	total := fs.Int("total", 0, "total attempts to run (default = number of suite missions)")
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
	if *parallel <= 0 {
		return r.failUsage("suite run: --parallel must be > 0")
	}
	if *total < 0 {
		return r.failUsage("suite run: --total must be >= 0")
	}
	if !schema.IsValidTimeoutStartV1(strings.TrimSpace(*timeoutStart)) {
		return r.failUsage("suite run: invalid --timeout-start (expected attempt_start|first_tool_call)")
	}

	hostNativeCapable := envBoolish("ZCL_HOST_NATIVE_SPAWN")
	requestedIsolation := strings.ToLower(strings.TrimSpace(*sessionIsolation))
	if requestedIsolation == "" {
		requestedIsolation = "auto"
	}
	effectiveIsolation := schema.IsolationModelProcessRunnerV1
	switch requestedIsolation {
	case "auto":
		if hostNativeCapable {
			printSuiteRunHelp(r.Stderr)
			return r.failUsage("suite run: host advertises native spawning (ZCL_HOST_NATIVE_SPAWN=1); refusing implicit process fallback. Use `zcl suite plan --json`/`zcl attempt start --json` with native spawn, or pass --session-isolation process to force process orchestration")
		}
	case "process":
		effectiveIsolation = schema.IsolationModelProcessRunnerV1
	case "native":
		printSuiteRunHelp(r.Stderr)
		return r.failUsage("suite run: native isolation is host-orchestrated (not process-spawned by suite run). Use `zcl suite plan --json` and spawn one fresh native session per attempt")
	default:
		return r.failUsage("suite run: invalid --session-isolation (expected auto|process|native)")
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

	parsed, err := suite.ParseFile(strings.TrimSpace(*file))
	if err != nil {
		fmt.Fprintf(r.Stderr, "ZCL_E_USAGE: %s\n", err.Error())
		return 2
	}

	resolvedMode := strings.TrimSpace(*mode)
	if resolvedMode == "" {
		resolvedMode = parsed.Suite.Defaults.Mode
	}
	if resolvedMode == "" {
		resolvedMode = "discovery"
	}
	if resolvedMode != "discovery" && resolvedMode != "ci" {
		return r.failUsage("suite run: invalid --mode (expected discovery|ci)")
	}

	resolvedTimeoutMs := *timeoutMs
	if resolvedTimeoutMs == 0 {
		resolvedTimeoutMs = parsed.Suite.Defaults.TimeoutMs
	}
	resolvedTimeoutStart := strings.TrimSpace(*timeoutStart)
	if resolvedTimeoutStart == "" {
		resolvedTimeoutStart = strings.TrimSpace(parsed.Suite.Defaults.TimeoutStart)
	}
	if !schema.IsValidTimeoutStartV1(resolvedTimeoutStart) {
		return r.failUsage("suite run: invalid timeoutStart in suite defaults")
	}

	resolvedBlind := parsed.Suite.Defaults.Blind
	switch strings.ToLower(strings.TrimSpace(*blindOverride)) {
	case "":
		// Use suite defaults.
	case "on", "true", "1", "yes":
		resolvedBlind = true
	case "off", "false", "0", "no":
		resolvedBlind = false
	default:
		return r.failUsage("suite run: invalid --blind (expected on|off)")
	}
	resolvedBlindTerms := append([]string(nil), parsed.Suite.Defaults.BlindTerms...)
	if strings.TrimSpace(*blindTermsCSV) != "" {
		resolvedBlindTerms = blind.ParseTermsCSV(*blindTermsCSV)
	}
	if resolvedBlind && len(resolvedBlindTerms) == 0 {
		resolvedBlindTerms = blind.DefaultHarnessTermsV1()
	}

	resolvedTotal := *total
	if resolvedTotal == 0 {
		resolvedTotal = len(parsed.Suite.Missions)
	}
	if resolvedTotal <= 0 {
		return r.failUsage("suite run: no missions to run")
	}

	missions := make([]suite.MissionV1, 0, resolvedTotal)
	for i := 0; i < resolvedTotal; i++ {
		missions = append(missions, parsed.Suite.Missions[i%len(parsed.Suite.Missions)])
	}

	summary := suiteRunSummary{
		SchemaVersion:             1,
		OK:                        true,
		RunID:                     strings.TrimSpace(*runID),
		SuiteID:                   parsed.Suite.SuiteID,
		Mode:                      resolvedMode,
		OutRoot:                   m.OutRoot,
		SessionIsolationRequested: requestedIsolation,
		SessionIsolation:          effectiveIsolation,
		HostNativeSpawnCapable:    hostNativeCapable,
		CreatedAt:                 r.Now().UTC().Format(time.RFC3339Nano),
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

	results := make([]suiteRunAttemptResult, len(missions))
	var (
		startMu      sync.Mutex
		harnessErr   atomic.Bool
		currentRunID = strings.TrimSpace(*runID)
	)
	execOpts := suiteRunExecOpts{
		RunnerCmd:        runnerCmd,
		RunnerArgs:       runnerArgs,
		Strict:           *strict,
		StrictExpect:     *strictExpect,
		CaptureRunnerIO:  *captureRunnerIO,
		RunnerIOMaxBytes: *runnerIOMaxBytes,
		RunnerIORaw:      *runnerIORaw,
		Shims:            []string(shims),
		ZCLExe:           zclExe,
		Blind:            resolvedBlind,
		BlindTerms:       resolvedBlindTerms,
		IsolationModel:   effectiveIsolation,
	}

	wave := *parallel
	if wave > len(missions) {
		wave = len(missions)
	}
	for start := 0; start < len(missions); start += wave {
		end := start + wave
		if end > len(missions) {
			end = len(missions)
		}
		var wg sync.WaitGroup
		for idx := start; idx < end; idx++ {
			idx := idx
			wg.Add(1)
			go func() {
				defer wg.Done()
				mission := missions[idx]

				startMu.Lock()
				started, err := attempt.Start(r.Now(), attempt.StartOpts{
					OutRoot:        m.OutRoot,
					RunID:          currentRunID,
					SuiteID:        parsed.Suite.SuiteID,
					MissionID:      mission.MissionID,
					IsolationModel: effectiveIsolation,
					Mode:           resolvedMode,
					Retry:          1,
					Prompt:         mission.Prompt,
					TimeoutMs:      resolvedTimeoutMs,
					TimeoutStart:   resolvedTimeoutStart,
					Blind:          resolvedBlind,
					BlindTerms:     resolvedBlindTerms,
					SuiteSnapshot:  parsed.CanonicalJSON,
				})
				if err == nil {
					currentRunID = started.RunID
				}
				startMu.Unlock()

				if err != nil {
					harnessErr.Store(true)
					fmt.Fprintf(r.Stderr, "ZCL_E_USAGE: suite run: %s\n", err.Error())
					results[idx] = suiteRunAttemptResult{
						MissionID:      mission.MissionID,
						IsolationModel: effectiveIsolation,
						Finish: suiteRunFinishResult{
							OK:           false,
							Strict:       *strict,
							StrictExpect: *strictExpect,
						},
						RunnerErrorCode: "ZCL_E_USAGE",
						OK:              false,
					}
					return
				}

				pm := planner.PlannedMission{
					MissionID: mission.MissionID,
					Prompt:    mission.Prompt,
					AttemptID: started.AttemptID,
					OutDir:    started.OutDir,
					OutDirAbs: started.OutDirAbs,
					Env:       started.Env,
				}
				ar, hard := r.executeSuiteRunMission(pm, execOpts)
				ar.IsolationModel = effectiveIsolation
				if hard {
					harnessErr.Store(true)
				}
				results[idx] = ar
			}()
		}
		wg.Wait()
	}

	summary.RunID = currentRunID
	for _, ar := range results {
		if ar.OK {
			summary.Passed++
		} else {
			summary.Failed++
			summary.OK = false
		}
		summary.Attempts = append(summary.Attempts, ar)
	}
	if summary.RunID != "" {
		_ = store.WriteJSONAtomic(filepath.Join(summary.OutRoot, "runs", summary.RunID, "suite.run.summary.json"), summary)
	}

	enc := json.NewEncoder(r.Stdout)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	if err := enc.Encode(summary); err != nil {
		fmt.Fprintf(r.Stderr, "ZCL_E_IO: failed to encode json\n")
		return 1
	}

	if harnessErr.Load() {
		return 1
	}
	if summary.OK {
		return 0
	}
	return 2
}

type suiteRunExecOpts struct {
	RunnerCmd        string
	RunnerArgs       []string
	Strict           bool
	StrictExpect     bool
	CaptureRunnerIO  bool
	RunnerIOMaxBytes int64
	RunnerIORaw      bool
	Shims            []string
	ZCLExe           string
	Blind            bool
	BlindTerms       []string
	IsolationModel   string
}

func (r Runner) executeSuiteRunMission(pm planner.PlannedMission, opts suiteRunExecOpts) (suiteRunAttemptResult, bool) {
	ar := suiteRunAttemptResult{
		MissionID:      pm.MissionID,
		AttemptID:      pm.AttemptID,
		AttemptDir:     pm.OutDirAbs,
		IsolationModel: opts.IsolationModel,
		Finish: suiteRunFinishResult{
			OK:           false,
			Strict:       opts.Strict,
			StrictExpect: opts.StrictExpect,
			AttemptDir:   pm.OutDirAbs,
		},
		OK: false,
	}
	harnessErr := false

	env := map[string]string{}
	for k, v := range pm.Env {
		env[k] = v
	}
	if p := filepath.Join(pm.OutDirAbs, "prompt.txt"); fileExists(p) {
		env["ZCL_PROMPT_PATH"] = p
	}
	if opts.ZCLExe != "" {
		env["ZCL_SHIM_ZCL_PATH"] = opts.ZCLExe
	}

	var shimBinDir string
	if len(opts.Shims) > 0 {
		dir, err := installAttemptShims(pm.OutDirAbs, opts.Shims)
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
			if _, ok := env["SURFWRIGHT_STATE_DIR"]; !ok && strings.Contains(" "+strings.Join(opts.Shims, " ")+" ", " surfwright ") && env["ZCL_TMP_DIR"] != "" {
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
	if opts.CaptureRunnerIO {
		if opts.RunnerIOMaxBytes <= 0 {
			harnessErr = true
			ar.RunnerErrorCode = "ZCL_E_USAGE"
			fmt.Fprintf(r.Stderr, "ZCL_E_USAGE: suite run: --runner-io-max-bytes must be > 0\n")
		} else {
			stdoutTB = newTailBuffer(opts.RunnerIOMaxBytes)
			stderrTB = newTailBuffer(opts.RunnerIOMaxBytes)
			_ = writeRunnerCommandFile(pm.OutDirAbs, opts.RunnerCmd, opts.RunnerArgs, env, shimBinDir)
			logW = &runnerLogWriter{
				AttemptDir: pm.OutDirAbs,
				StdoutTB:   stdoutTB,
				StderrTB:   stderrTB,
				Raw:        opts.RunnerIORaw,
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
		_ = writeRunnerCommandFile(pm.OutDirAbs, opts.RunnerCmd, opts.RunnerArgs, env, shimBinDir)
	}

	if err := verifyAttemptMatchesEnv(pm.OutDirAbs, env); err != nil {
		harnessErr = true
		ar.RunnerErrorCode = "ZCL_E_USAGE"
		fmt.Fprintf(r.Stderr, "ZCL_E_USAGE: suite run: %s\n", err.Error())
		stopRunnerLogs()
	} else if opts.Blind {
		found := promptContamination(pm.OutDirAbs, opts.BlindTerms)
		if len(found) > 0 {
			ar.RunnerErrorCode = "ZCL_E_CONTAMINATED_PROMPT"
			msg := "prompt contamination detected: " + strings.Join(found, ",")
			envTrace := trace.Env{
				RunID:     env["ZCL_RUN_ID"],
				SuiteID:   env["ZCL_SUITE_ID"],
				MissionID: env["ZCL_MISSION_ID"],
				AttemptID: env["ZCL_ATTEMPT_ID"],
				AgentID:   env["ZCL_AGENT_ID"],
				OutDirAbs: env["ZCL_OUT_DIR"],
				TmpDirAbs: env["ZCL_TMP_DIR"],
			}
			if err := trace.AppendCLIRunEvent(r.Now(), envTrace, []string{"zcl", "blind-check"}, trace.ResultForTrace{
				SpawnError: "ZCL_E_CONTAMINATED_PROMPT",
				DurationMs: 0,
				OutBytes:   0,
				ErrBytes:   int64(len(msg)),
				ErrPreview: msg,
			}); err != nil {
				harnessErr = true
				ar.RunnerErrorCode = "ZCL_E_IO"
				fmt.Fprintf(r.Stderr, "ZCL_E_IO: suite run: %s\n", err.Error())
			} else if err := feedback.Write(r.Now(), envTrace, feedback.WriteOpts{
				OK:           false,
				Result:       "CONTAMINATED_PROMPT",
				DecisionTags: []string{schema.DecisionTagBlocked, schema.DecisionTagContaminatedPrompt},
			}); err != nil {
				harnessErr = true
				ar.RunnerErrorCode = "ZCL_E_IO"
				fmt.Fprintf(r.Stderr, "ZCL_E_IO: suite run: %s\n", err.Error())
			}
			stopRunnerLogs()
		} else {
			harnessErr = runSuiteRunner(r, pm, env, opts.RunnerCmd, opts.RunnerArgs, stdoutTB, stderrTB, &ar) || harnessErr
			stopRunnerLogs()
		}
	} else {
		harnessErr = runSuiteRunner(r, pm, env, opts.RunnerCmd, opts.RunnerArgs, stdoutTB, stderrTB, &ar) || harnessErr
		stopRunnerLogs()
	}
	if err := maybeWriteAutoFailureFeedback(r.Now(), env, &ar); err != nil {
		harnessErr = true
		fmt.Fprintf(r.Stderr, "ZCL_E_IO: suite run: %s\n", err.Error())
	}

	ar.Finish = finishAttempt(r.Now(), pm.OutDirAbs, opts.Strict, opts.StrictExpect)
	runnerOK := ar.RunnerErrorCode == "" && ar.RunnerExitCode != nil && *ar.RunnerExitCode == 0
	ar.OK = runnerOK && ar.Finish.OK
	return ar, harnessErr
}

func runSuiteRunner(r Runner, pm planner.PlannedMission, env map[string]string, runnerCmd string, runnerArgs []string, stdoutTB *tailBuffer, stderrTB *tailBuffer, ar *suiteRunAttemptResult) bool {
	now := r.Now()
	ctx, cancel, timedOut := attemptCtxForDeadline(now, pm.OutDirAbs)
	if cancel != nil {
		defer cancel()
	}
	if timedOut {
		ar.RunnerErrorCode = "ZCL_E_TIMEOUT"
		return false
	}

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
			return true
		}
		if isStartFailure(err) {
			ar.RunnerErrorCode = "ZCL_E_SPAWN"
			return true
		}
		// Process exited non-zero: treat as harness error (runner is expected to encode mission outcome in feedback.json).
		return true
	}
	if ctx.Err() != nil && errors.Is(ctx.Err(), context.DeadlineExceeded) {
		// Defensive: CommandContext can return nil error while ctx is done in some edge cases.
		ar.RunnerErrorCode = "ZCL_E_TIMEOUT"
		return true
	}
	return false
}

func promptContamination(attemptDir string, terms []string) []string {
	b, err := os.ReadFile(filepath.Join(attemptDir, "prompt.txt"))
	if err != nil {
		return nil
	}
	return blind.FindContaminationTerms(string(b), terms)
}

func printSuiteRunHelp(w io.Writer) {
	fmt.Fprint(w, `Usage:
  zcl suite run --file <suite.(yaml|yml|json)> [--run-id <runId>] [--mode discovery|ci] [--timeout-ms N] [--timeout-start attempt_start|first_tool_call] [--blind on|off] [--blind-terms a,b,c] [--session-isolation auto|process|native] [--parallel N] [--total M] [--out-root .zcl] [--strict] [--strict-expect] [--shim <bin>] [--capture-runner-io] --json -- <runner-cmd> [args...]

Notes:
  - Requires --json (stdout is reserved for JSON; runner stdout/stderr is streamed to stderr).
  - --session-isolation=auto refuses process fallback when host advertises native spawn via ZCL_HOST_NATIVE_SPAWN=1.
  - Attempts are allocated just-in-time, in waves (--parallel), to avoid pre-expiry before execution.
  - When --shim is used, ZCL prepends an attempt-local bin dir to PATH so the agent can type the tool name (e.g. surfwright) and still have invocations traced via zcl run.
  - In blind mode, contaminated prompts are rejected and recorded with typed evidence.
  - After the runner exits, ZCL finishes each attempt (report + validate + expect).
`)
}

func envBoolish(name string) bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv(name)))
	switch v {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func maybeWriteAutoFailureFeedback(now time.Time, env map[string]string, ar *suiteRunAttemptResult) error {
	if !shouldAutoFailureFeedback(*ar) {
		return nil
	}
	outDir := strings.TrimSpace(env["ZCL_OUT_DIR"])
	if outDir == "" {
		return fmt.Errorf("suite run: missing ZCL_OUT_DIR for auto-feedback")
	}
	feedbackPath := filepath.Join(outDir, "feedback.json")
	if fileExists(feedbackPath) {
		return nil
	}

	envTrace := trace.Env{
		RunID:     env["ZCL_RUN_ID"],
		SuiteID:   env["ZCL_SUITE_ID"],
		MissionID: env["ZCL_MISSION_ID"],
		AttemptID: env["ZCL_ATTEMPT_ID"],
		AgentID:   env["ZCL_AGENT_ID"],
		OutDirAbs: outDir,
		TmpDirAbs: env["ZCL_TMP_DIR"],
	}
	code := autoFailureCode(*ar)
	msg := "suite runner failed before canonical feedback was written"
	if ar.RunnerErrorCode != "" {
		msg += " runnerErrorCode=" + ar.RunnerErrorCode
	}
	if ar.RunnerExitCode != nil {
		msg += fmt.Sprintf(" runnerExitCode=%d", *ar.RunnerExitCode)
	}

	tracePath := filepath.Join(outDir, "tool.calls.jsonl")
	nonEmpty, err := store.JSONLHasNonEmptyLine(tracePath)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	if !nonEmpty {
		if err := trace.AppendCLIRunEvent(now, envTrace, []string{"zcl", "suite-runner-auto-feedback"}, trace.ResultForTrace{
			SpawnError: code,
			DurationMs: 0,
			OutBytes:   0,
			ErrBytes:   int64(len(msg)),
			ErrPreview: msg,
		}); err != nil {
			return err
		}
	}

	result := map[string]any{
		"kind":   "infra_failure",
		"source": "suite_run",
		"code":   code,
	}
	if ar.RunnerErrorCode != "" {
		result["runnerErrorCode"] = ar.RunnerErrorCode
	}
	if ar.RunnerExitCode != nil {
		result["runnerExitCode"] = *ar.RunnerExitCode
	}
	b, err := json.Marshal(result)
	if err != nil {
		return err
	}

	decisionTags := []string{schema.DecisionTagBlocked}
	if code == "ZCL_E_TIMEOUT" || ar.RunnerErrorCode == "ZCL_E_TIMEOUT" {
		decisionTags = append(decisionTags, schema.DecisionTagTimeout)
	}
	if err := feedback.Write(now, envTrace, feedback.WriteOpts{
		OK:           false,
		ResultJSON:   string(b),
		DecisionTags: decisionTags,
	}); err != nil {
		return err
	}
	ar.AutoFeedback = true
	ar.AutoFeedbackCode = code
	return nil
}

func shouldAutoFailureFeedback(ar suiteRunAttemptResult) bool {
	if ar.RunnerErrorCode == "ZCL_E_TIMEOUT" || ar.RunnerErrorCode == "ZCL_E_SPAWN" || ar.RunnerErrorCode == "ZCL_E_IO" {
		return true
	}
	return ar.RunnerExitCode != nil && *ar.RunnerExitCode != 0
}

func autoFailureCode(ar suiteRunAttemptResult) string {
	if ar.RunnerErrorCode != "" {
		return ar.RunnerErrorCode
	}
	if ar.RunnerExitCode != nil && *ar.RunnerExitCode != 0 {
		return "ZCL_E_TOOL_FAILED"
	}
	return "ZCL_E_TOOL_FAILED"
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
