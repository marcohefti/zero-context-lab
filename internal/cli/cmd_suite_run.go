package cli

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/marcohefti/zero-context-lab/internal/attempt"
	"github.com/marcohefti/zero-context-lab/internal/blind"
	"github.com/marcohefti/zero-context-lab/internal/campaign"
	"github.com/marcohefti/zero-context-lab/internal/config"
	"github.com/marcohefti/zero-context-lab/internal/expect"
	"github.com/marcohefti/zero-context-lab/internal/feedback"
	"github.com/marcohefti/zero-context-lab/internal/ids"
	"github.com/marcohefti/zero-context-lab/internal/planner"
	"github.com/marcohefti/zero-context-lab/internal/redact"
	"github.com/marcohefti/zero-context-lab/internal/report"
	"github.com/marcohefti/zero-context-lab/internal/schema"
	"github.com/marcohefti/zero-context-lab/internal/store"
	"github.com/marcohefti/zero-context-lab/internal/suite"
	"github.com/marcohefti/zero-context-lab/internal/trace"
	"github.com/marcohefti/zero-context-lab/internal/validate"
)

type lockedWriter struct {
	mu *sync.Mutex
	w  io.Writer
}

func (lw *lockedWriter) Write(p []byte) (int, error) {
	if lw == nil || lw.w == nil {
		return len(p), nil
	}
	lw.mu.Lock()
	defer lw.mu.Unlock()
	return lw.w.Write(p)
}

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
	Skipped          bool   `json:"skipped,omitempty"`
	SkipReason       string `json:"skipReason,omitempty"`

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
	// CampaignProfile captures key run-shape controls for comparability across campaigns.
	CampaignProfile suiteRunCampaignProfile `json:"campaignProfile"`
	// ComparabilityKey is a stable hash of CampaignProfile.
	ComparabilityKey string `json:"comparabilityKey"`
	// FeedbackPolicy controls missing feedback behavior.
	FeedbackPolicy string `json:"feedbackPolicy"`
	// CampaignID groups continuity across multiple runs.
	CampaignID string `json:"campaignId,omitempty"`
	// CampaignStatePath points to the canonical campaign state file.
	CampaignStatePath string `json:"campaignStatePath,omitempty"`

	Attempts []suiteRunAttemptResult `json:"attempts"`

	Passed int `json:"passed"`
	Failed int `json:"failed"`

	CreatedAt string `json:"createdAt"`
}

type suiteRunCampaignProfile struct {
	Mode           string   `json:"mode"`
	TimeoutMs      int64    `json:"timeoutMs"`
	TimeoutStart   string   `json:"timeoutStart"`
	IsolationModel string   `json:"isolationModel"`
	FeedbackPolicy string   `json:"feedbackPolicy"`
	Finalization   string   `json:"finalization"`
	ResultChannel  string   `json:"resultChannel"`
	ResultMinTurn  int      `json:"resultMinTurn"`
	Parallel       int      `json:"parallel"`
	Total          int      `json:"total"`
	MissionOffset  int      `json:"missionOffset,omitempty"`
	FailFast       bool     `json:"failFast"`
	Blind          bool     `json:"blind"`
	Shims          []string `json:"shims,omitempty"`
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
	return r.runSuiteRunWithEnv(args, nil)
}

func (r Runner) runSuiteRunWithEnv(args []string, extraAttemptEnv map[string]string) int {
	fs := flag.NewFlagSet("suite run", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	file := fs.String("file", "", "suite file path (.json|.yaml|.yml) (required)")
	runID := fs.String("run-id", "", "existing run id (optional)")
	mode := fs.String("mode", "", "optional mode override: discovery|ci (default from suite file)")
	timeoutMs := fs.Int64("timeout-ms", 0, "optional attempt timeout override in ms (default from suite defaults.timeoutMs)")
	timeoutStart := fs.String("timeout-start", "", "optional timeout anchor override: attempt_start|first_tool_call")
	feedbackPolicy := fs.String("feedback-policy", "", "missing feedback policy override: strict|auto_fail (default from suite defaults, else auto_fail)")
	finalizationMode := fs.String("finalization-mode", "", "attempt finalization override: strict|auto_fail|auto_from_result_json")
	resultChannel := fs.String("result-channel", "", "mission result channel: none|file_json|stdout_json")
	resultFile := fs.String("result-file", "", "attempt-relative path for result channel file json (used with --result-channel=file_json)")
	resultMarker := fs.String("result-marker", "", "stdout marker prefix for result channel json (used with --result-channel=stdout_json)")
	resultMinTurn := fs.Int("result-min-turn", campaign.DefaultMinResultTurn, "minimum turn index accepted for auto result finalization (default 1)")
	blindOverride := fs.String("blind", "", "optional blind-mode override: on|off")
	blindTermsCSV := fs.String("blind-terms", "", "optional comma-separated blind harness terms override")
	sessionIsolation := fs.String("session-isolation", "auto", "session isolation strategy: auto|process|native")
	parallel := fs.Int("parallel", 1, "max concurrent attempt waves (just-in-time allocation)")
	total := fs.Int("total", 0, "total attempts to run (default = number of suite missions)")
	missionOffset := fs.Int("mission-offset", 0, "0-based mission offset before scheduling (for campaign resume/canary windows)")
	campaignID := fs.String("campaign-id", "", "campaign id for cross-run continuity (default suiteId)")
	campaignStatePath := fs.String("campaign-state", "", "path to campaign.state.json (default <outRoot>/campaigns/<campaignId>/campaign.state.json)")
	progressJSONL := fs.String("progress-jsonl", "", "write structured progress events to path or '-' (stderr)")
	outRoot := fs.String("out-root", "", "project output root (default from config/env, else .zcl)")
	failFast := fs.Bool("fail-fast", true, "stop scheduling new missions after the first failed attempt and mark the remainder as skipped")
	strict := fs.Bool("strict", true, "run finish in strict mode (enforces evidence + contract)")
	strictExpect := fs.Bool("strict-expect", true, "strict mode for expect (missing suite.json/feedback.json fails)")
	captureRunnerIO := fs.Bool("capture-runner-io", true, "capture runner stdout/stderr to runner.* logs under the attempt dir")
	runnerIOMaxBytes := fs.Int64("runner-io-max-bytes", schema.CaptureMaxBytesV1, "max bytes to keep per runner stream when using --capture-runner-io (tail)")
	runnerIORaw := fs.Bool("runner-io-raw", false, "capture raw runner stdout/stderr (unsafe; may contain secrets)")

	var shims stringListFlag
	fs.Var(&shims, "shim", "install attempt-local shims for tool binaries (repeatable; e.g. --shim tool-cli)")

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
	if *missionOffset < 0 {
		return r.failUsage("suite run: --mission-offset must be >= 0")
	}
	if *resultMinTurn < 1 {
		return r.failUsage("suite run: --result-min-turn must be >= 1")
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
		fmt.Fprintf(r.Stderr, codeIO+": %s\n", err.Error())
		return 1
	}

	parsed, err := suite.ParseFile(strings.TrimSpace(*file))
	if err != nil {
		fmt.Fprintf(r.Stderr, codeUsage+": %s\n", err.Error())
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
	resolvedFeedbackPolicy := schema.NormalizeFeedbackPolicyV1(parsed.Suite.Defaults.FeedbackPolicy)
	if strings.TrimSpace(*feedbackPolicy) != "" {
		resolvedFeedbackPolicy = schema.NormalizeFeedbackPolicyV1(*feedbackPolicy)
	}
	if !schema.IsValidFeedbackPolicyV1(resolvedFeedbackPolicy) {
		return r.failUsage("suite run: invalid --feedback-policy (expected strict|auto_fail)")
	}
	resolvedFinalizationMode := normalizeSuiteRunFinalizationMode(*finalizationMode, resolvedFeedbackPolicy)
	if !isValidSuiteRunFinalizationMode(resolvedFinalizationMode) {
		return r.failUsage("suite run: invalid --finalization-mode (expected strict|auto_fail|auto_from_result_json)")
	}
	resolvedResultChannel := suiteRunResultChannel{
		Kind:         normalizeSuiteRunResultChannelKind(*resultChannel),
		Path:         strings.TrimSpace(*resultFile),
		Marker:       strings.TrimSpace(*resultMarker),
		MinFinalTurn: *resultMinTurn,
	}
	if resolvedResultChannel.Kind == "" {
		if resolvedFinalizationMode == campaign.FinalizationModeAutoFromResultJSON {
			resolvedResultChannel.Kind = campaign.ResultChannelFileJSON
		} else {
			resolvedResultChannel.Kind = campaign.ResultChannelNone
		}
	}
	if !isValidSuiteRunResultChannelKind(resolvedResultChannel.Kind) {
		return r.failUsage("suite run: invalid --result-channel (expected none|file_json|stdout_json)")
	}
	switch resolvedResultChannel.Kind {
	case campaign.ResultChannelFileJSON:
		if resolvedResultChannel.Path == "" {
			resolvedResultChannel.Path = campaign.DefaultResultChannelPath
		}
		if filepath.IsAbs(resolvedResultChannel.Path) {
			return r.failUsage("suite run: --result-file must be attempt-relative")
		}
		resolvedResultChannel.Marker = ""
	case campaign.ResultChannelStdoutJSON:
		if resolvedResultChannel.Marker == "" {
			resolvedResultChannel.Marker = campaign.DefaultResultChannelMarker
		}
		resolvedResultChannel.Path = ""
	default:
		resolvedResultChannel.Path = ""
		resolvedResultChannel.Marker = ""
	}
	if resolvedFinalizationMode == campaign.FinalizationModeAutoFromResultJSON && resolvedResultChannel.Kind == campaign.ResultChannelNone {
		return r.failUsage("suite run: --finalization-mode auto_from_result_json requires --result-channel file_json|stdout_json")
	}
	if resolvedResultChannel.MinFinalTurn <= 0 {
		resolvedResultChannel.MinFinalTurn = campaign.DefaultMinResultTurn
	}
	if resolvedFinalizationMode != campaign.FinalizationModeAutoFromResultJSON {
		resolvedResultChannel.MinFinalTurn = campaign.DefaultMinResultTurn
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
		idx := (*missionOffset + i) % len(parsed.Suite.Missions)
		missions = append(missions, parsed.Suite.Missions[idx])
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
		FeedbackPolicy:            resolvedFeedbackPolicy,
		CreatedAt:                 r.Now().UTC().Format(time.RFC3339Nano),
	}
	summary.CampaignProfile = suiteRunCampaignProfile{
		Mode:           resolvedMode,
		TimeoutMs:      resolvedTimeoutMs,
		TimeoutStart:   resolvedTimeoutStart,
		IsolationModel: effectiveIsolation,
		FeedbackPolicy: resolvedFeedbackPolicy,
		Finalization:   resolvedFinalizationMode,
		ResultChannel:  resolvedResultChannel.Kind,
		ResultMinTurn:  resolvedResultChannel.MinFinalTurn,
		Parallel:       *parallel,
		Total:          resolvedTotal,
		MissionOffset:  *missionOffset,
		FailFast:       *failFast,
		Blind:          resolvedBlind,
		Shims:          dedupeSortedStrings([]string(shims)),
	}
	summary.ComparabilityKey = suiteRunComparabilityKey(summary.CampaignProfile)
	summary.CampaignID = ids.SanitizeComponent(strings.TrimSpace(*campaignID))
	if summary.CampaignID == "" {
		summary.CampaignID = parsed.Suite.SuiteID
	}
	if summary.CampaignID == "" {
		return r.failUsage("suite run: invalid --campaign-id (no usable characters)")
	}
	if strings.TrimSpace(*campaignStatePath) == "" {
		summary.CampaignStatePath = campaign.DefaultStatePath(m.OutRoot, summary.CampaignID)
	} else {
		summary.CampaignStatePath = strings.TrimSpace(*campaignStatePath)
	}

	progress, err := newSuiteRunProgressEmitter(strings.TrimSpace(*progressJSONL), r.Stderr)
	if err != nil {
		fmt.Fprintf(r.Stderr, codeIO+": %s\n", err.Error())
		return 1
	}
	defer func() {
		if progress != nil {
			_ = progress.Close()
		}
	}()

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
	for i, mission := range missions {
		results[i] = suiteRunAttemptResult{
			MissionID:      mission.MissionID,
			IsolationModel: effectiveIsolation,
			Finish: suiteRunFinishResult{
				OK:           false,
				Strict:       *strict,
				StrictExpect: *strictExpect,
			},
			OK: false,
		}
	}
	var (
		startMu      sync.Mutex
		harnessErr   atomic.Bool
		currentRunID = strings.TrimSpace(*runID)
	)
	errWriter := &lockedWriter{
		mu: &sync.Mutex{},
		w:  r.Stderr,
	}
	execOpts := suiteRunExecOpts{
		RunnerCmd:        runnerCmd,
		RunnerArgs:       runnerArgs,
		FeedbackPolicy:   resolvedFeedbackPolicy,
		FinalizationMode: resolvedFinalizationMode,
		ResultChannel:    resolvedResultChannel,
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
		StderrWriter:     errWriter,
		Progress:         progress,
		ExtraEnv:         copyStringMap(extraAttemptEnv),
	}
	if progress != nil {
		if err := progress.Emit(suiteRunProgressEvent{
			TS:         r.Now().UTC().Format(time.RFC3339Nano),
			Kind:       "run_started",
			RunID:      summary.RunID,
			SuiteID:    summary.SuiteID,
			Mode:       summary.Mode,
			OutRoot:    summary.OutRoot,
			CampaignID: summary.CampaignID,
			Details: map[string]any{
				"feedbackPolicy": resolvedFeedbackPolicy,
				"parallel":       *parallel,
				"total":          resolvedTotal,
				"failFast":       *failFast,
			},
		}); err != nil {
			fmt.Fprintf(r.Stderr, codeIO+": suite run progress: %s\n", err.Error())
			return 1
		}
	}

	wave := *parallel
	if wave > len(missions) {
		wave = len(missions)
	}
	for start := 0; start < len(missions); start += wave {
		if *failFast && hasFailedAttempt(results) {
			markSkippedAttempts(results, start, "fail_fast_prior_failure")
			break
		}
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
					fmt.Fprintf(errWriter, codeUsage+": suite run: %s\n", err.Error())
					results[idx].RunnerErrorCode = codeUsage
					results[idx].OK = false
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
				if progress != nil {
					if err := progress.Emit(suiteRunProgressEvent{
						TS:        r.Now().UTC().Format(time.RFC3339Nano),
						Kind:      "attempt_started",
						RunID:     started.RunID,
						SuiteID:   started.SuiteID,
						MissionID: mission.MissionID,
						AttemptID: started.AttemptID,
						Mode:      started.Mode,
						OutDir:    started.OutDirAbs,
						Details: map[string]any{
							"tags": mission.Tags,
						},
					}); err != nil {
						harnessErr.Store(true)
						fmt.Fprintf(errWriter, codeIO+": suite run progress: %s\n", err.Error())
					}
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
		if *failFast && hasFailedAttempt(results[start:end]) {
			markSkippedAttempts(results, end, "fail_fast_prior_failure")
			break
		}
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
	if summary.RunID != "" && summary.CampaignStatePath != "" {
		if _, err := campaign.UpdateState(summary.CampaignStatePath, campaign.UpdateInput{
			Now:              r.Now(),
			CampaignID:       summary.CampaignID,
			SuiteID:          summary.SuiteID,
			RunID:            summary.RunID,
			CreatedAt:        summary.CreatedAt,
			Mode:             summary.Mode,
			OutRoot:          summary.OutRoot,
			SessionIsolation: summary.SessionIsolation,
			ComparabilityKey: summary.ComparabilityKey,
			FeedbackPolicy:   summary.FeedbackPolicy,
			Parallel:         summary.CampaignProfile.Parallel,
			Total:            summary.CampaignProfile.Total,
			FailFast:         summary.CampaignProfile.FailFast,
			Passed:           summary.Passed,
			Failed:           summary.Failed,
		}); err != nil {
			harnessErr.Store(true)
			summary.OK = false
			fmt.Fprintf(r.Stderr, codeIO+": suite run campaign state: %s\n", err.Error())
		}
	}
	if progress != nil {
		if err := progress.Emit(suiteRunProgressEvent{
			TS:         r.Now().UTC().Format(time.RFC3339Nano),
			Kind:       "run_finished",
			RunID:      summary.RunID,
			SuiteID:    summary.SuiteID,
			Mode:       summary.Mode,
			CampaignID: summary.CampaignID,
			Details: map[string]any{
				"ok":     summary.OK,
				"passed": summary.Passed,
				"failed": summary.Failed,
			},
		}); err != nil {
			harnessErr.Store(true)
			summary.OK = false
			fmt.Fprintf(r.Stderr, codeIO+": suite run progress: %s\n", err.Error())
		}
	}

	enc := json.NewEncoder(r.Stdout)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	if err := enc.Encode(summary); err != nil {
		fmt.Fprintf(r.Stderr, codeIO+": failed to encode json\n")
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
	FeedbackPolicy   string
	FinalizationMode string
	ResultChannel    suiteRunResultChannel
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
	StderrWriter     io.Writer
	Progress         *suiteRunProgressEmitter
	ExtraEnv         map[string]string
}

type suiteRunResultChannel struct {
	Kind         string
	Path         string
	Marker       string
	MinFinalTurn int
}

func (r Runner) executeSuiteRunMission(pm planner.PlannedMission, opts suiteRunExecOpts) (suiteRunAttemptResult, bool) {
	errWriter := opts.StderrWriter
	if errWriter == nil {
		errWriter = r.Stderr
	}
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
	for k, v := range opts.ExtraEnv {
		key := strings.TrimSpace(k)
		if key == "" {
			continue
		}
		env[key] = v
	}
	env["ZCL_FINALIZATION_MODE"] = strings.TrimSpace(opts.FinalizationMode)
	env["ZCL_RESULT_CHANNEL_KIND"] = strings.TrimSpace(opts.ResultChannel.Kind)
	env["ZCL_RESULT_MIN_TURN"] = strconv.Itoa(opts.ResultChannel.MinFinalTurn)
	switch opts.ResultChannel.Kind {
	case campaign.ResultChannelFileJSON:
		if strings.TrimSpace(opts.ResultChannel.Path) != "" {
			env["ZCL_MISSION_RESULT_PATH"] = filepath.Join(pm.OutDirAbs, opts.ResultChannel.Path)
		}
	case campaign.ResultChannelStdoutJSON:
		if strings.TrimSpace(opts.ResultChannel.Marker) != "" {
			env["ZCL_MISSION_RESULT_MARKER"] = strings.TrimSpace(opts.ResultChannel.Marker)
		}
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
			ar.RunnerErrorCode = codeUsage
			fmt.Fprintf(errWriter, codeUsage+": suite run: %s\n", err.Error())
		} else {
			shimBinDir = dir
			env["ZCL_SHIM_BIN_DIR"] = shimBinDir
			// Prepend to PATH so the agent can type the tool name and still be traced.
			env["PATH"] = shimBinDir + ":" + os.Getenv("PATH")
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
	var stopLogsOnce sync.Once
	stopRunnerLogs := func() {
		if stopLogs == nil {
			return
		}
		stopLogsOnce.Do(func() {
			close(stopLogs)
			if logErrCh != nil {
				if lerr := <-logErrCh; lerr != nil {
					harnessErr = true
					ar.RunnerErrorCode = codeIO
					fmt.Fprintf(errWriter, codeIO+": suite run: %s\n", lerr.Error())
				}
			}
		})
	}
	if opts.CaptureRunnerIO {
		if opts.RunnerIOMaxBytes <= 0 {
			harnessErr = true
			ar.RunnerErrorCode = codeUsage
			fmt.Fprintf(errWriter, codeUsage+": suite run: --runner-io-max-bytes must be > 0\n")
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
				ar.RunnerErrorCode = codeIO
				fmt.Fprintf(errWriter, codeIO+": suite run: %s\n", err.Error())
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
	if opts.ResultChannel.Kind == campaign.ResultChannelStdoutJSON {
		maxBytes := opts.RunnerIOMaxBytes
		if maxBytes <= 0 {
			maxBytes = schema.CaptureMaxBytesV1
		}
		if stdoutTB == nil {
			stdoutTB = newTailBuffer(maxBytes)
		}
		if stderrTB == nil {
			stderrTB = newTailBuffer(maxBytes)
		}
	}

	if err := verifyAttemptMatchesEnv(pm.OutDirAbs, env); err != nil {
		harnessErr = true
		ar.RunnerErrorCode = codeUsage
		fmt.Fprintf(errWriter, codeUsage+": suite run: %s\n", err.Error())
		stopRunnerLogs()
	} else if opts.Blind {
		found := promptContamination(pm.OutDirAbs, opts.BlindTerms)
		if len(found) > 0 {
			ar.RunnerErrorCode = codeContaminatedPrompt
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
				SpawnError: codeContaminatedPrompt,
				DurationMs: 0,
				OutBytes:   0,
				ErrBytes:   int64(len(msg)),
				ErrPreview: msg,
			}); err != nil {
				harnessErr = true
				ar.RunnerErrorCode = codeIO
				fmt.Fprintf(errWriter, codeIO+": suite run: %s\n", err.Error())
			} else if err := feedback.Write(r.Now(), envTrace, feedback.WriteOpts{
				OK:                   false,
				Result:               "CONTAMINATED_PROMPT",
				DecisionTags:         []string{schema.DecisionTagBlocked, schema.DecisionTagContaminatedPrompt},
				SkipSuiteResultShape: true,
			}); err != nil {
				harnessErr = true
				ar.RunnerErrorCode = codeIO
				fmt.Fprintf(errWriter, codeIO+": suite run: %s\n", err.Error())
			}
			stopRunnerLogs()
		} else {
			harnessErr = runSuiteRunner(r, pm, env, opts.RunnerCmd, opts.RunnerArgs, stdoutTB, stderrTB, &ar, errWriter) || harnessErr
			stopRunnerLogs()
		}
	} else {
		harnessErr = runSuiteRunner(r, pm, env, opts.RunnerCmd, opts.RunnerArgs, stdoutTB, stderrTB, &ar, errWriter) || harnessErr
		stopRunnerLogs()
	}
	if err := maybeFinalizeSuiteFeedback(r.Now(), env, &ar, opts.FinalizationMode, opts.FeedbackPolicy, opts.ResultChannel, stdoutTB); err != nil {
		harnessErr = true
		fmt.Fprintf(errWriter, codeIO+": suite run: %s\n", err.Error())
	}

	ar.Finish = finishAttempt(r.Now(), pm.OutDirAbs, opts.Strict, opts.StrictExpect)
	runnerOK := ar.RunnerErrorCode == "" && ar.RunnerExitCode != nil && *ar.RunnerExitCode == 0
	ar.OK = runnerOK && ar.Finish.OK
	if opts.Progress != nil {
		_ = opts.Progress.Emit(suiteRunProgressEvent{
			TS:        r.Now().UTC().Format(time.RFC3339Nano),
			Kind:      "attempt_finished",
			RunID:     env["ZCL_RUN_ID"],
			SuiteID:   env["ZCL_SUITE_ID"],
			MissionID: env["ZCL_MISSION_ID"],
			AttemptID: env["ZCL_ATTEMPT_ID"],
			OutDir:    pm.OutDirAbs,
			Details: map[string]any{
				"ok":               ar.OK,
				"runnerErrorCode":  ar.RunnerErrorCode,
				"autoFeedback":     ar.AutoFeedback,
				"autoFeedbackCode": ar.AutoFeedbackCode,
				"finishOk":         ar.Finish.OK,
			},
		})
	}
	return ar, harnessErr
}

func copyStringMap(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func runSuiteRunner(r Runner, pm planner.PlannedMission, env map[string]string, runnerCmd string, runnerArgs []string, stdoutTB *tailBuffer, stderrTB *tailBuffer, ar *suiteRunAttemptResult, errWriter io.Writer) bool {
	if errWriter == nil {
		errWriter = r.Stderr
	}
	now := r.Now()
	ctx, cancel, timedOut := attemptCtxForDeadline(now, pm.OutDirAbs)
	if cancel != nil {
		defer cancel()
	}
	if timedOut {
		ar.RunnerErrorCode = codeTimeout
		return false
	}

	fmt.Fprintf(errWriter, "suite run: mission=%s attempt=%s runner=%s\n", pm.MissionID, pm.AttemptID, filepath.Base(runnerCmd))

	cmd := exec.CommandContext(ctx, runnerCmd, runnerArgs...)
	cmd.Env = mergeEnviron(os.Environ(), env)
	cmd.Stdin = os.Stdin
	if stdoutTB != nil && stderrTB != nil {
		cmd.Stdout = io.MultiWriter(errWriter, stdoutTB)
		cmd.Stderr = io.MultiWriter(errWriter, stderrTB)
	} else {
		cmd.Stdout = errWriter
		cmd.Stderr = errWriter
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
			ar.RunnerErrorCode = codeTimeout
			return true
		}
		if isStartFailure(err) {
			ar.RunnerErrorCode = codeSpawn
			return true
		}
		// Process exited non-zero: treat as harness error (runner is expected to encode mission outcome in feedback.json).
		return true
	}
	if ctx.Err() != nil && errors.Is(ctx.Err(), context.DeadlineExceeded) {
		// Defensive: CommandContext can return nil error while ctx is done in some edge cases.
		ar.RunnerErrorCode = codeTimeout
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
  zcl suite run --file <suite.(yaml|yml|json)> [--run-id <runId>] [--mode discovery|ci] [--timeout-ms N] [--timeout-start attempt_start|first_tool_call] [--feedback-policy strict|auto_fail] [--finalization-mode strict|auto_fail|auto_from_result_json] [--result-channel none|file_json|stdout_json] [--result-file <attempt-relative-path>] [--result-marker <prefix>] [--result-min-turn N] [--campaign-id <id>] [--campaign-state <path>] [--progress-jsonl <path|->] [--blind on|off] [--blind-terms a,b,c] [--session-isolation auto|process|native] [--parallel N] [--total M] [--mission-offset N] [--out-root .zcl] [--fail-fast] [--strict] [--strict-expect] [--shim <bin>] [--capture-runner-io] --json -- <runner-cmd> [args...]

Notes:
  - Requires --json (stdout is reserved for JSON; runner stdout/stderr is streamed to stderr).
  - --session-isolation=auto refuses process fallback when host advertises native spawn via ZCL_HOST_NATIVE_SPAWN=1.
  - --feedback-policy controls default finalization behavior when --finalization-mode is omitted.
  - --feedback-policy=auto_fail writes canonical infra-failure feedback when runners exit without feedback.
  - --feedback-policy=strict leaves missing feedback as a failing contract condition unless --finalization-mode overrides it.
  - --finalization-mode=auto_from_result_json consumes mission result JSON from the configured result channel and writes feedback.json automatically.
  - --result-channel=file_json reads attempt-relative JSON from --result-file (default mission.result.json); --result-channel=stdout_json scans runner stdout for --result-marker (default ZCL_RESULT_JSON:).
  - --result-min-turn N requires mission result payload field "turn" to be >= N before auto finalization accepts it (default 1).
  - --progress-jsonl writes machine-readable run progress events for dashboard automation.
  - campaign.state.json is updated after run completion for cross-run continuity.
  - Attempts are allocated just-in-time, in waves (--parallel), to avoid pre-expiry before execution.
  - --mission-offset shifts scheduling start point (useful for campaign resume/canary slices).
  - When --shim is used, ZCL prepends an attempt-local bin dir to PATH so the agent can type the tool name directly and still have invocations traced via zcl run.
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

func normalizeSuiteRunFinalizationMode(mode string, feedbackPolicy string) string {
	mode = strings.TrimSpace(strings.ToLower(mode))
	if mode != "" {
		return mode
	}
	switch schema.NormalizeFeedbackPolicyV1(feedbackPolicy) {
	case schema.FeedbackPolicyStrictV1:
		return campaign.FinalizationModeStrict
	default:
		return campaign.FinalizationModeAutoFail
	}
}

func isValidSuiteRunFinalizationMode(mode string) bool {
	switch strings.TrimSpace(strings.ToLower(mode)) {
	case campaign.FinalizationModeStrict, campaign.FinalizationModeAutoFail, campaign.FinalizationModeAutoFromResultJSON:
		return true
	default:
		return false
	}
}

func normalizeSuiteRunResultChannelKind(kind string) string {
	return strings.TrimSpace(strings.ToLower(kind))
}

func isValidSuiteRunResultChannelKind(kind string) bool {
	switch normalizeSuiteRunResultChannelKind(kind) {
	case campaign.ResultChannelNone, campaign.ResultChannelFileJSON, campaign.ResultChannelStdoutJSON:
		return true
	default:
		return false
	}
}

func maybeFinalizeSuiteFeedback(now time.Time, env map[string]string, ar *suiteRunAttemptResult, finalizationMode string, feedbackPolicy string, resultChannel suiteRunResultChannel, stdoutTB *tailBuffer) error {
	mode := normalizeSuiteRunFinalizationMode(finalizationMode, feedbackPolicy)
	switch mode {
	case campaign.FinalizationModeAutoFromResultJSON:
		return maybeWriteAutoResultFeedback(now, env, ar, resultChannel, stdoutTB)
	case campaign.FinalizationModeAutoFail:
		return maybeWriteAutoFailureFeedback(now, env, ar, schema.FeedbackPolicyAutoFailV1)
	default:
		return maybeWriteAutoFailureFeedback(now, env, ar, schema.FeedbackPolicyStrictV1)
	}
}

func maybeWriteAutoResultFeedback(now time.Time, env map[string]string, ar *suiteRunAttemptResult, resultChannel suiteRunResultChannel, stdoutTB *tailBuffer) error {
	outDir := strings.TrimSpace(env["ZCL_OUT_DIR"])
	if outDir == "" {
		return fmt.Errorf("suite run: missing ZCL_OUT_DIR for auto result finalization")
	}
	feedbackPath := filepath.Join(outDir, "feedback.json")
	if fileExists(feedbackPath) {
		return nil
	}
	if ar.RunnerErrorCode != "" {
		return maybeWriteAutoFailureFeedback(now, env, ar, schema.FeedbackPolicyAutoFailV1)
	}
	if ar.RunnerExitCode != nil && *ar.RunnerExitCode != 0 {
		return maybeWriteAutoFailureFeedback(now, env, ar, schema.FeedbackPolicyAutoFailV1)
	}

	raw, err := readSuiteResultChannel(outDir, resultChannel, stdoutTB)
	if err != nil {
		return maybeWriteResultChannelFailureFeedback(now, env, ar, codeMissionResultMissing, err)
	}
	writeOpts, err := decodeSuiteResultFeedback(raw, resultChannel.MinFinalTurn)
	if err != nil {
		var turnErr *missionResultTurnTooEarlyError
		if errors.As(err, &turnErr) {
			return maybeWriteResultChannelFailureFeedback(now, env, ar, codeMissionResultTurnTooEarly, err)
		}
		return maybeWriteResultChannelFailureFeedback(now, env, ar, codeMissionResultInvalid, err)
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
	if err := ensureAutoFeedbackTrace(now, envTrace, "suite-runner-result-channel", "", "auto finalization from mission result channel"); err != nil {
		return err
	}
	if err := feedback.Write(now, envTrace, writeOpts); err != nil {
		return err
	}
	ar.AutoFeedback = true
	ar.AutoFeedbackCode = ""
	return nil
}

func readSuiteResultChannel(outDir string, resultChannel suiteRunResultChannel, stdoutTB *tailBuffer) ([]byte, error) {
	kind := normalizeSuiteRunResultChannelKind(resultChannel.Kind)
	switch kind {
	case campaign.ResultChannelFileJSON:
		rel := strings.TrimSpace(resultChannel.Path)
		if rel == "" {
			rel = campaign.DefaultResultChannelPath
		}
		if filepath.IsAbs(rel) {
			return nil, fmt.Errorf("result channel file path must be attempt-relative")
		}
		path := filepath.Join(outDir, rel)
		b, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		return b, nil
	case campaign.ResultChannelStdoutJSON:
		if stdoutTB == nil {
			return nil, fmt.Errorf("stdout result channel requires runner stdout capture")
		}
		buf, _, _ := stdoutTB.Snapshot()
		if len(buf) == 0 {
			return nil, fmt.Errorf("stdout result channel is empty")
		}
		marker := strings.TrimSpace(resultChannel.Marker)
		if marker == "" {
			marker = campaign.DefaultResultChannelMarker
		}
		return extractSuiteResultJSONFromStdout(buf, marker)
	default:
		return nil, fmt.Errorf("unsupported result channel kind %q", kind)
	}
}

func extractSuiteResultJSONFromStdout(buf []byte, marker string) ([]byte, error) {
	text := strings.TrimSpace(string(buf))
	if text == "" {
		return nil, fmt.Errorf("stdout result channel is empty")
	}
	lines := strings.Split(text, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, marker) {
			raw := strings.TrimSpace(strings.TrimPrefix(line, marker))
			if raw == "" {
				return nil, fmt.Errorf("stdout result marker found but payload is empty")
			}
			return []byte(raw), nil
		}
	}
	return nil, fmt.Errorf("stdout result marker %q not found", marker)
}

type missionResultTurnTooEarlyError struct {
	RequiredMin int
	Actual      int
	Missing     bool
}

func (e *missionResultTurnTooEarlyError) Error() string {
	if e == nil {
		return "mission result turn is below required minimum"
	}
	if e.Missing {
		return fmt.Sprintf("mission result requires integer field \"turn\" >= %d", e.RequiredMin)
	}
	return fmt.Sprintf("mission result turn %d is below required minimum %d", e.Actual, e.RequiredMin)
}

func decodeSuiteResultFeedback(raw []byte, minFinalTurn int) (feedback.WriteOpts, error) {
	if minFinalTurn <= 0 {
		minFinalTurn = campaign.DefaultMinResultTurn
	}
	var obj map[string]any
	if err := json.Unmarshal(raw, &obj); err != nil {
		return feedback.WriteOpts{}, fmt.Errorf("invalid mission result json: %w", err)
	}
	rawOK, ok := obj["ok"]
	if !ok {
		return feedback.WriteOpts{}, fmt.Errorf("mission result requires boolean field \"ok\"")
	}
	okVal, ok := rawOK.(bool)
	if !ok {
		return feedback.WriteOpts{}, fmt.Errorf("mission result field \"ok\" must be boolean")
	}

	turnVal, hasTurn, err := parseMissionResultTurn(obj)
	if err != nil {
		return feedback.WriteOpts{}, err
	}
	if minFinalTurn > campaign.DefaultMinResultTurn {
		if !hasTurn {
			return feedback.WriteOpts{}, &missionResultTurnTooEarlyError{RequiredMin: minFinalTurn, Missing: true}
		}
		if turnVal < minFinalTurn {
			return feedback.WriteOpts{}, &missionResultTurnTooEarlyError{RequiredMin: minFinalTurn, Actual: turnVal}
		}
	}

	opts := feedback.WriteOpts{OK: okVal}
	if tags, present := obj["decisionTags"]; present {
		parsedTags, err := toStringSlice(tags)
		if err != nil {
			return feedback.WriteOpts{}, fmt.Errorf("mission result field \"decisionTags\" must be string array")
		}
		opts.DecisionTags = parsedTags
	}

	if rawResult, present := obj["result"]; present {
		resultText, ok := rawResult.(string)
		if !ok {
			return feedback.WriteOpts{}, fmt.Errorf("mission result field \"result\" must be string")
		}
		opts.Result = resultText
	}
	if rawResultJSON, present := obj["resultJson"]; present {
		b, err := store.CanonicalJSON(rawResultJSON)
		if err != nil {
			return feedback.WriteOpts{}, fmt.Errorf("mission result field \"resultJson\" must be valid json")
		}
		opts.ResultJSON = string(b)
	}
	if opts.Result != "" && opts.ResultJSON != "" {
		return feedback.WriteOpts{}, fmt.Errorf("mission result cannot include both \"result\" and \"resultJson\"")
	}
	if opts.Result == "" && opts.ResultJSON == "" {
		payload := map[string]any{}
		for k, v := range obj {
			switch strings.TrimSpace(k) {
			case "ok", "decisionTags", "turn":
				continue
			default:
				payload[k] = v
			}
		}
		if len(payload) == 0 {
			return feedback.WriteOpts{}, fmt.Errorf("mission result must include \"result\", \"resultJson\", or additional proof fields")
		}
		b, err := store.CanonicalJSON(payload)
		if err != nil {
			return feedback.WriteOpts{}, err
		}
		opts.ResultJSON = string(b)
	}
	return opts, nil
}

func parseMissionResultTurn(obj map[string]any) (int, bool, error) {
	rawTurn, present := obj["turn"]
	if !present {
		return 0, false, nil
	}
	switch v := rawTurn.(type) {
	case float64:
		if v != float64(int(v)) {
			return 0, false, fmt.Errorf("mission result field \"turn\" must be integer")
		}
		if int(v) < 1 {
			return 0, false, fmt.Errorf("mission result field \"turn\" must be >= 1")
		}
		return int(v), true, nil
	default:
		return 0, false, fmt.Errorf("mission result field \"turn\" must be integer")
	}
}

func toStringSlice(v any) ([]string, error) {
	items, ok := v.([]any)
	if !ok {
		return nil, fmt.Errorf("not an array")
	}
	out := make([]string, 0, len(items))
	for _, it := range items {
		s, ok := it.(string)
		if !ok {
			return nil, fmt.Errorf("non-string entry")
		}
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		out = append(out, s)
	}
	return out, nil
}

func maybeWriteResultChannelFailureFeedback(now time.Time, env map[string]string, ar *suiteRunAttemptResult, code string, cause error) error {
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
	msg := strings.TrimSpace(cause.Error())
	if msg == "" {
		msg = "mission result channel error"
	}
	if err := ensureAutoFeedbackTrace(now, envTrace, "suite-runner-result-channel", code, msg); err != nil {
		return err
	}
	result := map[string]any{
		"kind":   "infra_failure",
		"source": "result_channel",
		"code":   strings.TrimSpace(code),
		"error":  msg,
	}
	b, err := json.Marshal(result)
	if err != nil {
		return err
	}
	if err := feedback.Write(now, envTrace, feedback.WriteOpts{
		OK:                   false,
		ResultJSON:           string(b),
		DecisionTags:         []string{schema.DecisionTagBlocked},
		SkipSuiteResultShape: true,
	}); err != nil {
		return err
	}
	ar.AutoFeedback = true
	ar.AutoFeedbackCode = strings.TrimSpace(code)
	return nil
}

func ensureAutoFeedbackTrace(now time.Time, envTrace trace.Env, op string, code string, msg string) error {
	tracePath := filepath.Join(envTrace.OutDirAbs, "tool.calls.jsonl")
	nonEmpty, err := store.JSONLHasNonEmptyLine(tracePath)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	if nonEmpty {
		return nil
	}
	argv := []string{"zcl", strings.TrimSpace(op)}
	res := trace.ResultForTrace{
		ExitCode:   0,
		DurationMs: 0,
		OutBytes:   int64(len(msg)),
		OutPreview: msg,
	}
	if strings.TrimSpace(code) != "" {
		res.SpawnError = strings.TrimSpace(code)
		res.OutBytes = 0
		res.OutPreview = ""
		res.ErrBytes = int64(len(msg))
		res.ErrPreview = msg
	}
	return trace.AppendCLIRunEvent(now, envTrace, argv, res)
}

func maybeWriteAutoFailureFeedback(now time.Time, env map[string]string, ar *suiteRunAttemptResult, feedbackPolicy string) error {
	outDir := strings.TrimSpace(env["ZCL_OUT_DIR"])
	if outDir == "" {
		return fmt.Errorf("suite run: missing ZCL_OUT_DIR for auto-feedback")
	}
	feedbackPath := filepath.Join(outDir, "feedback.json")
	if fileExists(feedbackPath) {
		return nil
	}
	if schema.NormalizeFeedbackPolicyV1(feedbackPolicy) == schema.FeedbackPolicyStrictV1 {
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
	msg := "canonical feedback missing after suite runner completion"
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
	if code == codeTimeout || ar.RunnerErrorCode == codeTimeout {
		decisionTags = append(decisionTags, schema.DecisionTagTimeout)
	}
	if err := feedback.Write(now, envTrace, feedback.WriteOpts{
		OK:                   false,
		ResultJSON:           string(b),
		DecisionTags:         decisionTags,
		SkipSuiteResultShape: true,
	}); err != nil {
		return err
	}
	ar.AutoFeedback = true
	ar.AutoFeedbackCode = code
	return nil
}

func autoFailureCode(ar suiteRunAttemptResult) string {
	if ar.RunnerErrorCode != "" {
		return ar.RunnerErrorCode
	}
	if ar.RunnerExitCode != nil && *ar.RunnerExitCode == 0 {
		return codeMissingArtifact
	}
	if ar.RunnerExitCode != nil && *ar.RunnerExitCode != 0 {
		return codeToolFailed
	}
	return codeMissingArtifact
}

func suiteRunComparabilityKey(p suiteRunCampaignProfile) string {
	b, err := store.CanonicalJSON(p)
	if err != nil {
		// Deterministic fallback shape for error paths.
		b = []byte(fmt.Sprintf("%s|%d|%s|%s|%t", p.Mode, p.TimeoutMs, p.TimeoutStart, p.IsolationModel, p.Blind))
	}
	sum := sha256.Sum256(b)
	return "cp-" + hex.EncodeToString(sum[:8])
}

func dedupeSortedStrings(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	seen := map[string]bool{}
	out := make([]string, 0, len(in))
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	sort.Strings(out)
	if len(out) == 0 {
		return nil
	}
	return out
}

func hasFailedAttempt(items []suiteRunAttemptResult) bool {
	for _, it := range items {
		if it.AttemptID == "" && it.RunnerErrorCode == "" && !it.Skipped {
			continue
		}
		if !it.OK && !it.Skipped {
			return true
		}
	}
	return false
}

func markSkippedAttempts(results []suiteRunAttemptResult, start int, reason string) {
	for i := start; i < len(results); i++ {
		if results[i].AttemptID != "" || results[i].Skipped {
			continue
		}
		results[i].Skipped = true
		results[i].SkipReason = reason
	}
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
	return errors.As(err, &pe)
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

exec "$ZCL" run --capture -- "%s" "$@"
`, bin)
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
