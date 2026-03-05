package cli

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"github.com/marcohefti/zero-context-lab/internal/kernel/artifacts"
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

	"github.com/marcohefti/zero-context-lab/internal/contexts/evaluation/app/expect"
	"github.com/marcohefti/zero-context-lab/internal/contexts/evaluation/app/report"
	"github.com/marcohefti/zero-context-lab/internal/contexts/evaluation/app/validate"
	"github.com/marcohefti/zero-context-lab/internal/contexts/evidence/app/feedback"
	"github.com/marcohefti/zero-context-lab/internal/contexts/evidence/app/redact"
	"github.com/marcohefti/zero-context-lab/internal/contexts/evidence/app/trace"
	"github.com/marcohefti/zero-context-lab/internal/contexts/execution/app/attempt"
	"github.com/marcohefti/zero-context-lab/internal/contexts/execution/app/campaign"
	"github.com/marcohefti/zero-context-lab/internal/contexts/execution/app/planner"
	"github.com/marcohefti/zero-context-lab/internal/contexts/runtime/ports/native"
	"github.com/marcohefti/zero-context-lab/internal/contexts/spec/ports/suite"
	"github.com/marcohefti/zero-context-lab/internal/kernel/blind"
	"github.com/marcohefti/zero-context-lab/internal/kernel/config"
	"github.com/marcohefti/zero-context-lab/internal/kernel/ids"
	"github.com/marcohefti/zero-context-lab/internal/kernel/schema"
	"github.com/marcohefti/zero-context-lab/internal/kernel/store"
)

const (
	suiteRunEnvRunnerCwdMode     = "ZCL_RUNNER_CWD_MODE"
	suiteRunEnvRunnerCwdBasePath = "ZCL_RUNNER_CWD_BASE_PATH"
	suiteRunEnvRunnerCwdRetain   = "ZCL_RUNNER_CWD_RETAIN"
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
	// RuntimeStrategyChain is the ordered native runtime fallback chain.
	RuntimeStrategyChain []string `json:"runtimeStrategyChain,omitempty"`
	// RuntimeStrategySelected is the resolved native runtime strategy when native mode is used.
	RuntimeStrategySelected string `json:"runtimeStrategySelected,omitempty"`
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
	Mode            string   `json:"mode"`
	TimeoutMs       int64    `json:"timeoutMs"`
	TimeoutStart    string   `json:"timeoutStart"`
	IsolationModel  string   `json:"isolationModel"`
	FeedbackPolicy  string   `json:"feedbackPolicy"`
	Finalization    string   `json:"finalization"`
	ResultChannel   string   `json:"resultChannel"`
	ResultMinTurn   int      `json:"resultMinTurn"`
	RuntimeStrategy string   `json:"runtimeStrategy,omitempty"`
	NativeModel     string   `json:"nativeModel,omitempty"`
	ReasoningEffort string   `json:"reasoningEffort,omitempty"`
	ReasoningPolicy string   `json:"reasoningPolicy,omitempty"`
	Parallel        int      `json:"parallel"`
	Total           int      `json:"total"`
	MissionOffset   int      `json:"missionOffset,omitempty"`
	FailFast        bool     `json:"failFast"`
	Blind           bool     `json:"blind"`
	Shims           []string `json:"shims,omitempty"`
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
	return r.runSuiteRunWithEnvImpl(args, extraAttemptEnv)
}

func (r Runner) runSuiteRunWithEnvImpl(args []string, extraAttemptEnv map[string]string) int {
	return r.runSuiteRunWithEnvCore(args, extraAttemptEnv)
}

func (r Runner) runSuiteRunWithEnvCore(args []string, extraAttemptEnv map[string]string) int {
	input, ok := r.parseSuiteRunCLIInput(args)
	if !ok {
		return r.failUsage("suite run: invalid flags")
	}
	if done, code := r.handleSuiteRunCLIImmediate(input); done {
		return code
	}
	host, ok, code := r.resolveSuiteRunHostConfig(input, extraAttemptEnv)
	if !ok {
		return code
	}
	exec, ok, code := r.resolveSuiteRunExecutionPlan(input, host, extraAttemptEnv)
	if !ok {
		return code
	}
	return r.runSuiteRunExecution(exec)
}

type suiteRunCLIInput struct {
	file                       string
	runID                      string
	mode                       string
	timeoutMs                  int64
	timeoutStart               string
	feedbackPolicy             string
	finalizationMode           string
	resultChannel              string
	resultFile                 string
	resultMarker               string
	resultMinTurn              int
	blindOverride              string
	blindTermsCSV              string
	sessionIsolation           string
	runtimeStrategiesCSV       string
	nativeModel                string
	nativeModelReasoningEffort string
	nativeModelReasoningPolicy string
	parallel                   int
	total                      int
	missionOffset              int
	campaignID                 string
	campaignStatePath          string
	progressJSONL              string
	outRoot                    string
	failFast                   bool
	strict                     bool
	strictExpect               bool
	captureRunnerIO            bool
	runnerIOMaxBytes           int64
	runnerIORaw                bool
	shims                      []string
	jsonOut                    bool
	help                       bool
	argv                       []string
}

type suiteRunHostConfig struct {
	merged                        config.Merged
	hostNativeCapable             bool
	requestedIsolation            string
	effectiveIsolation            string
	nativeMode                    bool
	runtimeStrategyChain          []string
	nativeRuntimeSelection        native.ResolveResult
	resolvedNativeModel           string
	resolvedNativeReasoningEffort string
	resolvedNativeReasoningPolicy string
	runnerCwdPolicy               suiteRunRunnerCwdPolicy
}

type suiteRunSuiteSettings struct {
	mode             string
	feedbackPolicy   string
	finalizationMode string
	resultChannel    suiteRunResultChannel
	timeoutMs        int64
	timeoutStart     string
	blind            bool
	blindTerms       []string
	total            int
	missions         []suite.MissionV1
}

type suiteRunExecutionPlan struct {
	input        suiteRunCLIInput
	host         suiteRunHostConfig
	parsed       suite.ParsedSuite
	settings     suiteRunSuiteSettings
	summary      suiteRunSummary
	execOpts     suiteRunExecOpts
	initialRunID string
}

func (r Runner) parseSuiteRunCLIInput(args []string) (suiteRunCLIInput, bool) {
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
	runtimeStrategiesCSV := fs.String("runtime-strategies", "", "ordered native runtime strategy chain (comma-separated; default from config/env)")
	nativeModel := fs.String("native-model", "", "native thread/start model override")
	nativeModelReasoningEffort := fs.String("native-model-reasoning-effort", "", "native thread/start model reasoning effort hint: none|minimal|low|medium|high|xhigh")
	nativeModelReasoningPolicy := fs.String("native-model-reasoning-policy", "", "native reasoning policy when effort is unsupported: best_effort|required")
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
		return suiteRunCLIInput{}, false
	}
	argv := fs.Args()
	if len(argv) > 0 && argv[0] == "--" {
		argv = argv[1:]
	}
	return suiteRunCLIInput{
		file:                       *file,
		runID:                      *runID,
		mode:                       *mode,
		timeoutMs:                  *timeoutMs,
		timeoutStart:               *timeoutStart,
		feedbackPolicy:             *feedbackPolicy,
		finalizationMode:           *finalizationMode,
		resultChannel:              *resultChannel,
		resultFile:                 *resultFile,
		resultMarker:               *resultMarker,
		resultMinTurn:              *resultMinTurn,
		blindOverride:              *blindOverride,
		blindTermsCSV:              *blindTermsCSV,
		sessionIsolation:           *sessionIsolation,
		runtimeStrategiesCSV:       *runtimeStrategiesCSV,
		nativeModel:                *nativeModel,
		nativeModelReasoningEffort: *nativeModelReasoningEffort,
		nativeModelReasoningPolicy: *nativeModelReasoningPolicy,
		parallel:                   *parallel,
		total:                      *total,
		missionOffset:              *missionOffset,
		campaignID:                 *campaignID,
		campaignStatePath:          *campaignStatePath,
		progressJSONL:              *progressJSONL,
		outRoot:                    *outRoot,
		failFast:                   *failFast,
		strict:                     *strict,
		strictExpect:               *strictExpect,
		captureRunnerIO:            *captureRunnerIO,
		runnerIOMaxBytes:           *runnerIOMaxBytes,
		runnerIORaw:                *runnerIORaw,
		shims:                      []string(shims),
		jsonOut:                    *jsonOut,
		help:                       *help,
		argv:                       argv,
	}, true
}

func (r Runner) handleSuiteRunCLIImmediate(input suiteRunCLIInput) (bool, int) {
	if input.help {
		printSuiteRunHelp(r.Stdout)
		return true, 0
	}
	if !input.jsonOut {
		printSuiteRunHelp(r.Stderr)
		return true, r.failUsage("suite run: require --json for stable output")
	}
	if msg := validateSuiteRunCLIInput(input); msg != "" {
		return true, r.failUsage(msg)
	}
	return false, 0
}

func validateSuiteRunCLIInput(input suiteRunCLIInput) string {
	if input.parallel <= 0 {
		return "suite run: --parallel must be > 0"
	}
	if input.total < 0 {
		return "suite run: --total must be >= 0"
	}
	if input.missionOffset < 0 {
		return "suite run: --mission-offset must be >= 0"
	}
	if input.resultMinTurn < 1 {
		return "suite run: --result-min-turn must be >= 1"
	}
	if !schema.IsValidTimeoutStartV1(strings.TrimSpace(input.timeoutStart)) {
		return "suite run: invalid --timeout-start (expected attempt_start|first_tool_call)"
	}
	return ""
}

func (r Runner) resolveSuiteRunHostConfig(input suiteRunCLIInput, extraAttemptEnv map[string]string) (suiteRunHostConfig, bool, int) {
	merged, err := config.LoadMerged(input.outRoot)
	if err != nil {
		fmt.Fprintf(r.Stderr, codeIO+": %s\n", err.Error())
		return suiteRunHostConfig{}, false, 1
	}
	hostNativeCapable := envBoolish("ZCL_HOST_NATIVE_SPAWN")
	requestedIsolation, effectiveIsolation, nativeMode, ok := resolveSuiteRunIsolation(input.sessionIsolation, hostNativeCapable)
	if !ok {
		return suiteRunHostConfig{}, false, r.failUsage("suite run: invalid --session-isolation (expected auto|process|native)")
	}
	if nativeMode && len(input.argv) > 0 {
		return suiteRunHostConfig{}, false, r.failUsage("suite run: native runtime mode does not accept -- <runner-cmd> arguments")
	}
	if !nativeMode && len(input.argv) == 0 {
		printSuiteRunHelp(r.Stderr)
		return suiteRunHostConfig{}, false, r.failUsage("suite run: missing runner command (use: zcl suite run ... -- <runner-cmd> ...)")
	}
	model, effort, policy, ok, msg := resolveSuiteRunNativeModelConfig(input, nativeMode)
	if !ok {
		return suiteRunHostConfig{}, false, r.failUsage(msg)
	}
	runnerCwdPolicy, err := resolveSuiteRunRunnerCwdPolicy(extraAttemptEnv)
	if err != nil {
		return suiteRunHostConfig{}, false, r.failUsage("suite run: " + err.Error())
	}
	if runnerCwdPolicy.Mode != campaign.RunnerCwdModeInherit && !nativeMode {
		return suiteRunHostConfig{}, false, r.failUsage("suite run: runner cwd policy requires --session-isolation native")
	}
	runtimeStrategyChain := config.ParseRuntimeStrategyCSV(input.runtimeStrategiesCSV)
	if len(runtimeStrategyChain) == 0 {
		runtimeStrategyChain = append([]string(nil), merged.RuntimeStrategyChain...)
	}
	nativeRuntimeSelection, ok, code := r.resolveSuiteRunNativeSelection(nativeMode, runtimeStrategyChain)
	if !ok {
		return suiteRunHostConfig{}, false, code
	}
	return suiteRunHostConfig{
		merged:                        merged,
		hostNativeCapable:             hostNativeCapable,
		requestedIsolation:            requestedIsolation,
		effectiveIsolation:            effectiveIsolation,
		nativeMode:                    nativeMode,
		runtimeStrategyChain:          runtimeStrategyChain,
		nativeRuntimeSelection:        nativeRuntimeSelection,
		resolvedNativeModel:           model,
		resolvedNativeReasoningEffort: effort,
		resolvedNativeReasoningPolicy: policy,
		runnerCwdPolicy:               runnerCwdPolicy,
	}, true, 0
}

func resolveSuiteRunIsolation(raw string, hostNativeCapable bool) (string, string, bool, bool) {
	requested := strings.ToLower(strings.TrimSpace(raw))
	if requested == "" {
		requested = "auto"
	}
	switch requested {
	case "auto":
		if hostNativeCapable {
			return requested, schema.IsolationModelNativeSpawnV1, true, true
		}
		return requested, schema.IsolationModelProcessRunnerV1, false, true
	case "process":
		return requested, schema.IsolationModelProcessRunnerV1, false, true
	case "native":
		return requested, schema.IsolationModelNativeSpawnV1, true, true
	default:
		return requested, "", false, false
	}
}

func resolveSuiteRunNativeModelConfig(input suiteRunCLIInput, nativeMode bool) (string, string, string, bool, string) {
	model := strings.TrimSpace(input.nativeModel)
	effort := strings.ToLower(strings.TrimSpace(input.nativeModelReasoningEffort))
	policy := strings.ToLower(strings.TrimSpace(input.nativeModelReasoningPolicy))
	if effort == "" && policy != "" {
		return "", "", "", false, "suite run: --native-model-reasoning-policy requires --native-model-reasoning-effort"
	}
	if effort != "" {
		switch effort {
		case campaign.ModelReasoningEffortNone, campaign.ModelReasoningEffortMinimal, campaign.ModelReasoningEffortLow, campaign.ModelReasoningEffortMedium, campaign.ModelReasoningEffortHigh, campaign.ModelReasoningEffortXHigh:
		default:
			return "", "", "", false, "suite run: invalid --native-model-reasoning-effort (expected none|minimal|low|medium|high|xhigh)"
		}
		if policy == "" {
			policy = campaign.ModelReasoningPolicyBestEffort
		}
	}
	if policy != "" {
		switch policy {
		case campaign.ModelReasoningPolicyBestEffort, campaign.ModelReasoningPolicyRequired:
		default:
			return "", "", "", false, "suite run: invalid --native-model-reasoning-policy (expected best_effort|required)"
		}
	}
	if !nativeMode && (model != "" || effort != "" || policy != "") {
		return "", "", "", false, "suite run: native model flags require --session-isolation native"
	}
	return model, effort, policy, true, ""
}

func (r Runner) resolveSuiteRunNativeSelection(nativeMode bool, runtimeStrategyChain []string) (native.ResolveResult, bool, int) {
	if !nativeMode {
		return native.ResolveResult{}, true, 0
	}
	registry := buildNativeRuntimeRegistry()
	selection, selErr := native.Resolve(context.Background(), registry, native.ResolveInput{
		StrategyChain: native.NormalizeStrategyChain(runtimeStrategyChain),
		RequiredCapabilities: []native.Capability{
			native.CapabilityThreadStart,
			native.CapabilityEventStream,
			native.CapabilityInterrupt,
		},
	})
	if selErr == nil {
		return selection, true, 0
	}
	if nerr, ok := native.AsError(selErr); ok {
		fmt.Fprintf(r.Stderr, "%s: suite run native runtime selection failed: %s\n", nerr.Code, nerr.Message)
		for _, f := range nerr.Failures {
			fmt.Fprintf(r.Stderr, "  %s %s: %s\n", f.Code, f.Strategy, f.Message)
		}
		return native.ResolveResult{}, false, 2
	}
	fmt.Fprintf(r.Stderr, codeIO+": suite run native runtime selection failed: %s\n", selErr.Error())
	return native.ResolveResult{}, false, 1
}

func (r Runner) resolveSuiteRunExecutionPlan(input suiteRunCLIInput, host suiteRunHostConfig, extraAttemptEnv map[string]string) (suiteRunExecutionPlan, bool, int) {
	parsed, err := suite.ParseFile(strings.TrimSpace(input.file))
	if err != nil {
		fmt.Fprintf(r.Stderr, codeUsage+": %s\n", err.Error())
		return suiteRunExecutionPlan{}, false, 2
	}
	settings, ok, code := r.resolveSuiteRunSuiteSettings(input, parsed)
	if !ok {
		return suiteRunExecutionPlan{}, false, code
	}
	summary, ok, code := r.buildSuiteRunSummary(input, host, parsed, settings)
	if !ok {
		return suiteRunExecutionPlan{}, false, code
	}
	runnerCmd, runnerArgs := splitSuiteRunRunnerCommand(input.argv)
	execOpts := suiteRunExecOpts{
		RunnerCmd:        runnerCmd,
		RunnerArgs:       runnerArgs,
		NativeMode:       host.nativeMode,
		NativeSelection:  host.nativeRuntimeSelection,
		NativeScheduler:  buildNativeAttemptScheduler(host.nativeRuntimeSelection.Selected, input.parallel),
		NativeModel:      host.resolvedNativeModel,
		ReasoningEffort:  host.resolvedNativeReasoningEffort,
		ReasoningPolicy:  host.resolvedNativeReasoningPolicy,
		FeedbackPolicy:   settings.feedbackPolicy,
		FinalizationMode: settings.finalizationMode,
		ResultChannel:    settings.resultChannel,
		Strict:           input.strict,
		StrictExpect:     input.strictExpect,
		CaptureRunnerIO:  input.captureRunnerIO,
		RunnerIOMaxBytes: input.runnerIOMaxBytes,
		RunnerIORaw:      input.runnerIORaw,
		Shims:            append([]string(nil), input.shims...),
		ZCLExe:           resolveSuiteRunZCLExecutable(),
		Blind:            settings.blind,
		BlindTerms:       append([]string(nil), settings.blindTerms...),
		IsolationModel:   host.effectiveIsolation,
		ExtraEnv:         copyStringMap(extraAttemptEnv),
		RunnerCwdPolicy:  host.runnerCwdPolicy,
	}
	return suiteRunExecutionPlan{
		input:        input,
		host:         host,
		parsed:       parsed,
		settings:     settings,
		summary:      summary,
		execOpts:     execOpts,
		initialRunID: strings.TrimSpace(input.runID),
	}, true, 0
}

func (r Runner) resolveSuiteRunSuiteSettings(input suiteRunCLIInput, parsed suite.ParsedSuite) (suiteRunSuiteSettings, bool, int) {
	mode := strings.TrimSpace(input.mode)
	if mode == "" {
		mode = parsed.Suite.Defaults.Mode
	}
	if mode == "" {
		mode = "discovery"
	}
	if mode != "discovery" && mode != "ci" {
		return suiteRunSuiteSettings{}, false, r.failUsage("suite run: invalid --mode (expected discovery|ci)")
	}
	feedbackPolicy := schema.NormalizeFeedbackPolicyV1(parsed.Suite.Defaults.FeedbackPolicy)
	if strings.TrimSpace(input.feedbackPolicy) != "" {
		feedbackPolicy = schema.NormalizeFeedbackPolicyV1(input.feedbackPolicy)
	}
	if !schema.IsValidFeedbackPolicyV1(feedbackPolicy) {
		return suiteRunSuiteSettings{}, false, r.failUsage("suite run: invalid --feedback-policy (expected strict|auto_fail)")
	}
	finalizationMode := normalizeSuiteRunFinalizationMode(input.finalizationMode, feedbackPolicy)
	if !isValidSuiteRunFinalizationMode(finalizationMode) {
		return suiteRunSuiteSettings{}, false, r.failUsage("suite run: invalid --finalization-mode (expected strict|auto_fail|auto_from_result_json)")
	}
	resultChannel, ok, code := r.resolveSuiteRunResultChannel(input, finalizationMode)
	if !ok {
		return suiteRunSuiteSettings{}, false, code
	}
	timeoutMs := input.timeoutMs
	if timeoutMs == 0 {
		timeoutMs = parsed.Suite.Defaults.TimeoutMs
	}
	timeoutStart := strings.TrimSpace(input.timeoutStart)
	if timeoutStart == "" {
		timeoutStart = strings.TrimSpace(parsed.Suite.Defaults.TimeoutStart)
	}
	if !schema.IsValidTimeoutStartV1(timeoutStart) {
		return suiteRunSuiteSettings{}, false, r.failUsage("suite run: invalid timeoutStart in suite defaults")
	}
	blind, blindTerms, ok, code := r.resolveSuiteRunBlindSettings(input, parsed)
	if !ok {
		return suiteRunSuiteSettings{}, false, code
	}
	total := input.total
	if total == 0 {
		total = len(parsed.Suite.Missions)
	}
	if total <= 0 {
		return suiteRunSuiteSettings{}, false, r.failUsage("suite run: no missions to run")
	}
	return suiteRunSuiteSettings{
		mode:             mode,
		feedbackPolicy:   feedbackPolicy,
		finalizationMode: finalizationMode,
		resultChannel:    resultChannel,
		timeoutMs:        timeoutMs,
		timeoutStart:     timeoutStart,
		blind:            blind,
		blindTerms:       blindTerms,
		total:            total,
		missions:         selectSuiteRunMissions(parsed.Suite.Missions, total, input.missionOffset),
	}, true, 0
}

func (r Runner) resolveSuiteRunResultChannel(input suiteRunCLIInput, finalizationMode string) (suiteRunResultChannel, bool, int) {
	resultChannel := suiteRunResultChannel{
		Kind:         normalizeSuiteRunResultChannelKind(input.resultChannel),
		Path:         strings.TrimSpace(input.resultFile),
		Marker:       strings.TrimSpace(input.resultMarker),
		MinFinalTurn: input.resultMinTurn,
	}
	resultChannel.Kind = defaultSuiteRunResultChannelKind(resultChannel.Kind, finalizationMode)
	if !isValidSuiteRunResultChannelKind(resultChannel.Kind) {
		return suiteRunResultChannel{}, false, r.failUsage("suite run: invalid --result-channel (expected none|file_json|stdout_json)")
	}
	normalized, err := normalizeSuiteRunResultChannel(resultChannel)
	if err != nil {
		return suiteRunResultChannel{}, false, r.failUsage(err.Error())
	}
	resultChannel = normalized
	if finalizationMode == campaign.FinalizationModeAutoFromResultJSON && resultChannel.Kind == campaign.ResultChannelNone {
		return suiteRunResultChannel{}, false, r.failUsage("suite run: --finalization-mode auto_from_result_json requires --result-channel file_json|stdout_json")
	}
	resultChannel.MinFinalTurn = normalizeSuiteRunResultMinTurn(resultChannel.MinFinalTurn, finalizationMode)
	return resultChannel, true, 0
}

func defaultSuiteRunResultChannelKind(kind string, finalizationMode string) string {
	if strings.TrimSpace(kind) != "" {
		return kind
	}
	if finalizationMode == campaign.FinalizationModeAutoFromResultJSON {
		return campaign.ResultChannelFileJSON
	}
	return campaign.ResultChannelNone
}

func normalizeSuiteRunResultChannel(ch suiteRunResultChannel) (suiteRunResultChannel, error) {
	switch ch.Kind {
	case campaign.ResultChannelFileJSON:
		if ch.Path == "" {
			ch.Path = campaign.DefaultResultChannelPath
		}
		if filepath.IsAbs(ch.Path) {
			return suiteRunResultChannel{}, fmt.Errorf("suite run: --result-file must be attempt-relative")
		}
		ch.Marker = ""
	case campaign.ResultChannelStdoutJSON:
		if ch.Marker == "" {
			ch.Marker = campaign.DefaultResultChannelMarker
		}
		ch.Path = ""
	default:
		ch.Path = ""
		ch.Marker = ""
	}
	return ch, nil
}

func normalizeSuiteRunResultMinTurn(minTurn int, finalizationMode string) int {
	if minTurn <= 0 || finalizationMode != campaign.FinalizationModeAutoFromResultJSON {
		return campaign.DefaultMinResultTurn
	}
	return minTurn
}

func (r Runner) resolveSuiteRunBlindSettings(input suiteRunCLIInput, parsed suite.ParsedSuite) (bool, []string, bool, int) {
	blindMode := parsed.Suite.Defaults.Blind
	switch strings.ToLower(strings.TrimSpace(input.blindOverride)) {
	case "":
	case "on", "true", "1", "yes":
		blindMode = true
	case "off", "false", "0", "no":
		blindMode = false
	default:
		return false, nil, false, r.failUsage("suite run: invalid --blind (expected on|off)")
	}
	blindTerms := append([]string(nil), parsed.Suite.Defaults.BlindTerms...)
	if strings.TrimSpace(input.blindTermsCSV) != "" {
		blindTerms = blind.ParseTermsCSV(input.blindTermsCSV)
	}
	if blindMode && len(blindTerms) == 0 {
		blindTerms = blind.DefaultHarnessTermsV1()
	}
	return blindMode, blindTerms, true, 0
}

func selectSuiteRunMissions(all []suite.MissionV1, total int, missionOffset int) []suite.MissionV1 {
	missions := make([]suite.MissionV1, 0, total)
	for i := 0; i < total; i++ {
		idx := (missionOffset + i) % len(all)
		missions = append(missions, all[idx])
	}
	return missions
}

func (r Runner) buildSuiteRunSummary(input suiteRunCLIInput, host suiteRunHostConfig, parsed suite.ParsedSuite, settings suiteRunSuiteSettings) (suiteRunSummary, bool, int) {
	summary := suiteRunSummary{
		SchemaVersion:             1,
		OK:                        true,
		RunID:                     strings.TrimSpace(input.runID),
		SuiteID:                   parsed.Suite.SuiteID,
		Mode:                      settings.mode,
		OutRoot:                   host.merged.OutRoot,
		SessionIsolationRequested: host.requestedIsolation,
		SessionIsolation:          host.effectiveIsolation,
		HostNativeSpawnCapable:    host.hostNativeCapable,
		RuntimeStrategyChain:      append([]string(nil), host.runtimeStrategyChain...),
		FeedbackPolicy:            settings.feedbackPolicy,
		CreatedAt:                 r.Now().UTC().Format(time.RFC3339Nano),
	}
	if host.nativeMode {
		summary.RuntimeStrategySelected = string(host.nativeRuntimeSelection.Selected)
	}
	summary.CampaignProfile = suiteRunCampaignProfile{
		Mode:            settings.mode,
		TimeoutMs:       settings.timeoutMs,
		TimeoutStart:    settings.timeoutStart,
		IsolationModel:  host.effectiveIsolation,
		FeedbackPolicy:  settings.feedbackPolicy,
		Finalization:    settings.finalizationMode,
		ResultChannel:   settings.resultChannel.Kind,
		ResultMinTurn:   settings.resultChannel.MinFinalTurn,
		RuntimeStrategy: string(host.nativeRuntimeSelection.Selected),
		NativeModel:     host.resolvedNativeModel,
		ReasoningEffort: host.resolvedNativeReasoningEffort,
		ReasoningPolicy: host.resolvedNativeReasoningPolicy,
		Parallel:        input.parallel,
		Total:           settings.total,
		MissionOffset:   input.missionOffset,
		FailFast:        input.failFast,
		Blind:           settings.blind,
		Shims:           dedupeSortedStrings(input.shims),
	}
	summary.ComparabilityKey = suiteRunComparabilityKey(summary.CampaignProfile)
	summary.CampaignID = ids.SanitizeComponent(strings.TrimSpace(input.campaignID))
	if summary.CampaignID == "" {
		summary.CampaignID = parsed.Suite.SuiteID
	}
	if summary.CampaignID == "" {
		return suiteRunSummary{}, false, r.failUsage("suite run: invalid --campaign-id (no usable characters)")
	}
	if strings.TrimSpace(input.campaignStatePath) == "" {
		summary.CampaignStatePath = campaign.DefaultStatePath(host.merged.OutRoot, summary.CampaignID)
	} else {
		summary.CampaignStatePath = strings.TrimSpace(input.campaignStatePath)
	}
	return summary, true, 0
}

func splitSuiteRunRunnerCommand(argv []string) (string, []string) {
	if len(argv) == 0 {
		return "", nil
	}
	if len(argv) == 1 {
		return argv[0], nil
	}
	return argv[0], argv[1:]
}

func resolveSuiteRunZCLExecutable() string {
	zclExe, _ := os.Executable()
	if zclExe == "" {
		return ""
	}
	base := strings.ToLower(filepath.Base(zclExe))
	if base == "zcl" || base == "zcl.exe" {
		return zclExe
	}
	return ""
}

func (r Runner) runSuiteRunExecution(plan suiteRunExecutionPlan) int {
	progress, err := newSuiteRunProgressEmitter(strings.TrimSpace(plan.input.progressJSONL), r.Stderr)
	if err != nil {
		fmt.Fprintf(r.Stderr, codeIO+": %s\n", err.Error())
		return 1
	}
	defer func() {
		if progress != nil {
			_ = progress.Close()
		}
	}()
	errWriter := &lockedWriter{mu: &sync.Mutex{}, w: r.Stderr}
	plan.execOpts.Progress = progress
	plan.execOpts.StderrWriter = errWriter
	if err := emitSuiteRunStarted(r, progress, plan.summary); err != nil {
		fmt.Fprintf(r.Stderr, codeIO+": suite run progress: %s\n", err.Error())
		return 1
	}
	results, currentRunID, harnessErr := r.executeSuiteRunMissions(plan, errWriter)
	plan.summary = finalizeSuiteRunSummary(plan.summary, results, currentRunID)
	harnessErr = updateSuiteRunCampaignState(r, &plan.summary, harnessErr)
	harnessErr = emitSuiteRunFinished(r, progress, &plan.summary, harnessErr)
	if err := encodeSuiteRunSummary(r.Stdout, plan.summary); err != nil {
		fmt.Fprintf(r.Stderr, codeIO+": failed to encode json\n")
		return 1
	}
	return finalizeSuiteRunExitCode(plan.summary.OK, harnessErr)
}

func emitSuiteRunStarted(r Runner, progress *suiteRunProgressEmitter, summary suiteRunSummary) error {
	if progress == nil {
		return nil
	}
	return progress.Emit(suiteRunProgressEvent{
		TS:         r.Now().UTC().Format(time.RFC3339Nano),
		Kind:       "run_started",
		RunID:      summary.RunID,
		SuiteID:    summary.SuiteID,
		Mode:       summary.Mode,
		OutRoot:    summary.OutRoot,
		CampaignID: summary.CampaignID,
		Details: map[string]any{
			"feedbackPolicy": summary.FeedbackPolicy,
			"parallel":       summary.CampaignProfile.Parallel,
			"total":          summary.CampaignProfile.Total,
			"failFast":       summary.CampaignProfile.FailFast,
		},
	})
}

func (r Runner) executeSuiteRunMissions(plan suiteRunExecutionPlan, errWriter io.Writer) ([]suiteRunAttemptResult, string, bool) {
	results := initializeSuiteRunResults(plan.settings.missions, plan.host.effectiveIsolation, plan.input.strict, plan.input.strictExpect)
	var (
		startMu      sync.Mutex
		harnessErr   atomic.Bool
		currentRunID = plan.initialRunID
	)
	runState := &suiteRunMissionRunState{
		startMu:      &startMu,
		harnessErr:   &harnessErr,
		currentRunID: &currentRunID,
		results:      results,
		errWriter:    errWriter,
	}
	waveSize := plan.input.parallel
	if waveSize > len(plan.settings.missions) {
		waveSize = len(plan.settings.missions)
	}
	for start := 0; start < len(plan.settings.missions); start += waveSize {
		if plan.input.failFast && hasFailedAttempt(results) {
			markSkippedAttempts(results, start, "fail_fast_prior_failure")
			break
		}
		end := start + waveSize
		if end > len(plan.settings.missions) {
			end = len(plan.settings.missions)
		}
		r.executeSuiteRunWave(plan, runState, start, end)
		if plan.input.failFast && hasFailedAttempt(results[start:end]) {
			markSkippedAttempts(results, end, "fail_fast_prior_failure")
			break
		}
	}
	return results, currentRunID, harnessErr.Load()
}

type suiteRunMissionRunState struct {
	startMu      *sync.Mutex
	harnessErr   *atomic.Bool
	currentRunID *string
	results      []suiteRunAttemptResult
	errWriter    io.Writer
}

func initializeSuiteRunResults(missions []suite.MissionV1, isolationModel string, strict bool, strictExpect bool) []suiteRunAttemptResult {
	results := make([]suiteRunAttemptResult, len(missions))
	for i, mission := range missions {
		results[i] = suiteRunAttemptResult{
			MissionID:      mission.MissionID,
			IsolationModel: isolationModel,
			Finish: suiteRunFinishResult{
				OK:           false,
				Strict:       strict,
				StrictExpect: strictExpect,
			},
			OK: false,
		}
	}
	return results
}

func (r Runner) executeSuiteRunWave(plan suiteRunExecutionPlan, state *suiteRunMissionRunState, start int, end int) {
	var wg sync.WaitGroup
	for idx := start; idx < end; idx++ {
		idx := idx
		wg.Add(1)
		go func() {
			defer wg.Done()
			r.executeSuiteRunMissionIndex(plan, state, idx)
		}()
	}
	wg.Wait()
}

func (r Runner) executeSuiteRunMissionIndex(plan suiteRunExecutionPlan, state *suiteRunMissionRunState, idx int) {
	mission := plan.settings.missions[idx]
	started, ok := startSuiteRunAttempt(r, plan, state, mission, idx)
	if !ok {
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
	emitSuiteRunAttemptStarted(r, plan.execOpts.Progress, started, mission, state)
	ar, hard := r.executeSuiteRunMission(pm, plan.execOpts)
	ar.IsolationModel = plan.host.effectiveIsolation
	if hard {
		state.harnessErr.Store(true)
	}
	state.results[idx] = ar
}

func startSuiteRunAttempt(r Runner, plan suiteRunExecutionPlan, state *suiteRunMissionRunState, mission suite.MissionV1, idx int) (*attempt.StartResult, bool) {
	state.startMu.Lock()
	started, err := attempt.Start(r.Now(), attempt.StartOpts{
		OutRoot:        plan.host.merged.OutRoot,
		RunID:          *state.currentRunID,
		SuiteID:        plan.parsed.Suite.SuiteID,
		MissionID:      mission.MissionID,
		IsolationModel: plan.host.effectiveIsolation,
		Mode:           plan.settings.mode,
		Retry:          1,
		Prompt:         mission.Prompt,
		TimeoutMs:      plan.settings.timeoutMs,
		TimeoutStart:   plan.settings.timeoutStart,
		Blind:          plan.settings.blind,
		BlindTerms:     plan.settings.blindTerms,
		SuiteSnapshot:  plan.parsed.CanonicalJSON,
	})
	if err == nil {
		*state.currentRunID = started.RunID
	}
	state.startMu.Unlock()
	if err == nil {
		return started, true
	}
	state.harnessErr.Store(true)
	fmt.Fprintf(state.errWriter, codeUsage+": suite run: %s\n", err.Error())
	state.results[idx].RunnerErrorCode = codeUsage
	state.results[idx].OK = false
	return nil, false
}

func emitSuiteRunAttemptStarted(r Runner, progress *suiteRunProgressEmitter, started *attempt.StartResult, mission suite.MissionV1, state *suiteRunMissionRunState) {
	if progress == nil {
		return
	}
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
		state.harnessErr.Store(true)
		fmt.Fprintf(state.errWriter, codeIO+": suite run progress: %s\n", err.Error())
	}
}

func finalizeSuiteRunSummary(summary suiteRunSummary, results []suiteRunAttemptResult, runID string) suiteRunSummary {
	summary.RunID = runID
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
		_ = store.WriteJSONAtomic(filepath.Join(summary.OutRoot, "runs", summary.RunID, artifacts.SuiteRunSummaryJSON), summary)
	}
	return summary
}

func updateSuiteRunCampaignState(r Runner, summary *suiteRunSummary, harnessErr bool) bool {
	if summary.RunID == "" || summary.CampaignStatePath == "" {
		return harnessErr
	}
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
		fmt.Fprintf(r.Stderr, codeIO+": suite run campaign state: %s\n", err.Error())
		summary.OK = false
		return true
	}
	return harnessErr
}

func emitSuiteRunFinished(r Runner, progress *suiteRunProgressEmitter, summary *suiteRunSummary, harnessErr bool) bool {
	if progress == nil {
		return harnessErr
	}
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
		fmt.Fprintf(r.Stderr, codeIO+": suite run progress: %s\n", err.Error())
		summary.OK = false
		return true
	}
	return harnessErr
}

func encodeSuiteRunSummary(w io.Writer, summary suiteRunSummary) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	return enc.Encode(summary)
}

func finalizeSuiteRunExitCode(summaryOK bool, harnessErr bool) int {
	if harnessErr {
		return 1
	}
	if summaryOK {
		return 0
	}
	return 2
}

type suiteRunExecOpts struct {
	RunnerCmd        string
	RunnerArgs       []string
	NativeMode       bool
	NativeSelection  native.ResolveResult
	NativeScheduler  *nativeAttemptScheduler
	NativeModel      string
	ReasoningEffort  string
	ReasoningPolicy  string
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
	RunnerCwdPolicy  suiteRunRunnerCwdPolicy
}

type suiteRunResultChannel struct {
	Kind         string
	Path         string
	Marker       string
	MinFinalTurn int
}

type suiteRunRunnerCwdPolicy struct {
	Mode     string
	BasePath string
	Retain   string
}

type suiteRunAttemptRuntimeContext struct {
	StartCwdMode   string
	StartCwd       string
	StartCwdRetain string
}

func (r Runner) executeSuiteRunMission(pm planner.PlannedMission, opts suiteRunExecOpts) (suiteRunAttemptResult, bool) {
	return r.executeSuiteRunMissionImpl(pm, opts)
}

func (r Runner) executeSuiteRunMissionImpl(pm planner.PlannedMission, opts suiteRunExecOpts) (suiteRunAttemptResult, bool) {
	return r.executeSuiteRunMissionCore(pm, opts)
}

func (r Runner) executeSuiteRunMissionCore(pm planner.PlannedMission, opts suiteRunExecOpts) (suiteRunAttemptResult, bool) {
	errWriter := suiteRunAttemptErrWriter(r, opts)
	ar := newSuiteRunAttemptResult(pm, opts)
	runtimeCtx, cleanupRunnerCwd, err := prepareSuiteRunAttemptStartCwd(pm, opts.RunnerCwdPolicy)
	if err != nil {
		ar.RunnerErrorCode = codeIO
		fmt.Fprintf(errWriter, codeIO+": suite run: %s\n", err.Error())
		return ar, true
	}
	env := buildSuiteRunMissionEnv(pm, opts)

	harnessErr := false
	shouldFinish := true
	if opts.NativeMode {
		harnessErr, shouldFinish = r.runSuiteMissionNativePath(pm, opts, runtimeCtx, env, &ar, errWriter)
	} else {
		harnessErr, shouldFinish = r.runSuiteMissionProcessPath(pm, opts, runtimeCtx, env, &ar, errWriter)
	}
	if shouldFinish {
		finalizeSuiteRunAttemptResult(r, pm, opts, env, &ar)
		emitSuiteRunAttemptFinished(r, opts, env, pm, ar)
	}
	applySuiteRunRunnerCwdCleanup(cleanupRunnerCwd, &harnessErr, &ar, errWriter)
	return ar, harnessErr
}

func suiteRunAttemptErrWriter(r Runner, opts suiteRunExecOpts) io.Writer {
	if opts.StderrWriter != nil {
		return opts.StderrWriter
	}
	return r.Stderr
}

func newSuiteRunAttemptResult(pm planner.PlannedMission, opts suiteRunExecOpts) suiteRunAttemptResult {
	return suiteRunAttemptResult{
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
}

func buildSuiteRunMissionEnv(pm planner.PlannedMission, opts suiteRunExecOpts) map[string]string {
	env := copyStringMap(pm.Env)
	if env == nil {
		env = map[string]string{}
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
	applySuiteRunResultChannelEnv(env, pm.OutDirAbs, opts.ResultChannel)
	applySuiteRunOptionalEnvPaths(env, pm.OutDirAbs, opts.ZCLExe)
	return env
}

func applySuiteRunResultChannelEnv(env map[string]string, outDirAbs string, resultChannel suiteRunResultChannel) {
	switch resultChannel.Kind {
	case campaign.ResultChannelFileJSON:
		if strings.TrimSpace(resultChannel.Path) == "" {
			return
		}
		env["ZCL_MISSION_RESULT_PATH"] = filepath.Join(outDirAbs, resultChannel.Path)
	case campaign.ResultChannelStdoutJSON:
		if strings.TrimSpace(resultChannel.Marker) == "" {
			return
		}
		env["ZCL_MISSION_RESULT_MARKER"] = strings.TrimSpace(resultChannel.Marker)
	}
}

func applySuiteRunOptionalEnvPaths(env map[string]string, outDirAbs string, zclExe string) {
	if p := filepath.Join(outDirAbs, artifacts.PromptTXT); fileExists(p) {
		env["ZCL_PROMPT_PATH"] = p
	}
	if strings.TrimSpace(zclExe) != "" {
		env["ZCL_SHIM_ZCL_PATH"] = zclExe
	}
}

func (r Runner) runSuiteMissionNativePath(pm planner.PlannedMission, opts suiteRunExecOpts, runtimeCtx suiteRunAttemptRuntimeContext, env map[string]string, ar *suiteRunAttemptResult, errWriter io.Writer) (bool, bool) {
	if err := writeAttemptRuntimeEnvArtifact(r.Now(), pm, env, opts, runtimeCtx); err != nil {
		ar.RunnerErrorCode = codeIO
		fmt.Fprintf(errWriter, codeIO+": suite run: %s\n", err.Error())
		return true, false
	}
	harnessErr := runSuiteNativeRuntime(r, pm, env, opts, runtimeCtx, ar, errWriter)
	if err := maybeWriteAutoFailureFeedback(r.Now(), env, ar, schema.FeedbackPolicyAutoFailV1); err != nil {
		harnessErr = true
		fmt.Fprintf(errWriter, codeIO+": suite run: %s\n", err.Error())
	}
	return harnessErr, true
}

type suiteRunProcessPathContext struct {
	stdoutTB      *tailBuffer
	stderrTB      *tailBuffer
	stopRunnerLog func(harnessErr *bool, ar *suiteRunAttemptResult)
}

func (r Runner) runSuiteMissionProcessPath(pm planner.PlannedMission, opts suiteRunExecOpts, runtimeCtx suiteRunAttemptRuntimeContext, env map[string]string, ar *suiteRunAttemptResult, errWriter io.Writer) (bool, bool) {
	harnessErr, shimBinDir := installSuiteRunProcessShims(pm.OutDirAbs, opts, env, ar, errWriter)
	if err := writeAttemptRuntimeEnvArtifact(r.Now(), pm, env, opts, runtimeCtx); err != nil {
		ar.RunnerErrorCode = codeIO
		fmt.Fprintf(errWriter, codeIO+": suite run: %s\n", err.Error())
		return true, false
	}
	pathCtx := prepareSuiteRunProcessPath(pm, opts, env, shimBinDir, ar, errWriter, &harnessErr)
	harnessErr = executeSuiteRunProcessRunner(r, pm, opts, env, pathCtx.stdoutTB, pathCtx.stderrTB, ar, errWriter) || harnessErr
	pathCtx.stopRunnerLog(&harnessErr, ar)
	if err := maybeFinalizeSuiteFeedback(r.Now(), env, ar, opts.FinalizationMode, opts.FeedbackPolicy, opts.ResultChannel, pathCtx.stdoutTB); err != nil {
		harnessErr = true
		fmt.Fprintf(errWriter, codeIO+": suite run: %s\n", err.Error())
	}
	return harnessErr, true
}

func installSuiteRunProcessShims(attemptDir string, opts suiteRunExecOpts, env map[string]string, ar *suiteRunAttemptResult, errWriter io.Writer) (bool, string) {
	if len(opts.Shims) == 0 {
		return false, ""
	}
	dir, err := installAttemptShims(attemptDir, opts.Shims)
	if err != nil {
		ar.RunnerErrorCode = codeUsage
		fmt.Fprintf(errWriter, codeUsage+": suite run: %s\n", err.Error())
		return true, ""
	}
	env["ZCL_SHIM_BIN_DIR"] = dir
	// Prepend to PATH so the agent can type the tool name and still be traced.
	env["PATH"] = dir + ":" + os.Getenv("PATH")
	return false, dir
}

func prepareSuiteRunProcessPath(pm planner.PlannedMission, opts suiteRunExecOpts, env map[string]string, shimBinDir string, ar *suiteRunAttemptResult, errWriter io.Writer, harnessErr *bool) suiteRunProcessPathContext {
	ctx := suiteRunProcessPathContext{
		stopRunnerLog: func(_ *bool, _ *suiteRunAttemptResult) {},
	}
	stdoutTB, stderrTB, stopRunnerLogs := initSuiteRunRunnerLogs(pm.OutDirAbs, opts, env, shimBinDir, ar, errWriter, harnessErr)
	ctx.stdoutTB = stdoutTB
	ctx.stderrTB = stderrTB
	ctx.stopRunnerLog = stopRunnerLogs
	ensureSuiteRunResultStdoutBuffers(opts, &ctx)
	return ctx
}

func initSuiteRunRunnerLogs(attemptDir string, opts suiteRunExecOpts, env map[string]string, shimBinDir string, ar *suiteRunAttemptResult, errWriter io.Writer, harnessErr *bool) (*tailBuffer, *tailBuffer, func(harnessErr *bool, ar *suiteRunAttemptResult)) {
	var (
		stdoutTB *tailBuffer
		stderrTB *tailBuffer
	)
	stopNoop := func(_ *bool, _ *suiteRunAttemptResult) {}
	if !opts.CaptureRunnerIO {
		_ = writeRunnerCommandFile(attemptDir, opts.RunnerCmd, opts.RunnerArgs, env, shimBinDir)
		return stdoutTB, stderrTB, stopNoop
	}
	if opts.RunnerIOMaxBytes <= 0 {
		*harnessErr = true
		ar.RunnerErrorCode = codeUsage
		fmt.Fprintf(errWriter, codeUsage+": suite run: --runner-io-max-bytes must be > 0\n")
		return stdoutTB, stderrTB, stopNoop
	}
	stdoutTB = newTailBuffer(opts.RunnerIOMaxBytes)
	stderrTB = newTailBuffer(opts.RunnerIOMaxBytes)
	_ = writeRunnerCommandFile(attemptDir, opts.RunnerCmd, opts.RunnerArgs, env, shimBinDir)
	logW := &runnerLogWriter{
		AttemptDir: attemptDir,
		StdoutTB:   stdoutTB,
		StderrTB:   stderrTB,
		Raw:        opts.RunnerIORaw,
	}
	if err := logW.Flush(true); err != nil {
		*harnessErr = true
		ar.RunnerErrorCode = codeIO
		fmt.Fprintf(errWriter, codeIO+": suite run: %s\n", err.Error())
		return stdoutTB, stderrTB, stopNoop
	}
	stopLogs := make(chan struct{})
	logErrCh := make(chan error, 1)
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
	var stopLogsOnce sync.Once
	stopWithWait := func(localHarnessErr *bool, localAR *suiteRunAttemptResult) {
		stopLogsOnce.Do(func() {
			close(stopLogs)
			if lerr := <-logErrCh; lerr != nil {
				*localHarnessErr = true
				localAR.RunnerErrorCode = codeIO
				fmt.Fprintf(errWriter, codeIO+": suite run: %s\n", lerr.Error())
			}
		})
	}
	return stdoutTB, stderrTB, stopWithWait
}

func ensureSuiteRunResultStdoutBuffers(opts suiteRunExecOpts, pathCtx *suiteRunProcessPathContext) {
	if opts.ResultChannel.Kind != campaign.ResultChannelStdoutJSON {
		return
	}
	maxBytes := opts.RunnerIOMaxBytes
	if maxBytes <= 0 {
		maxBytes = schema.CaptureMaxBytesV1
	}
	if pathCtx.stdoutTB == nil {
		pathCtx.stdoutTB = newTailBuffer(maxBytes)
	}
	if pathCtx.stderrTB == nil {
		pathCtx.stderrTB = newTailBuffer(maxBytes)
	}
}

func executeSuiteRunProcessRunner(r Runner, pm planner.PlannedMission, opts suiteRunExecOpts, env map[string]string, stdoutTB *tailBuffer, stderrTB *tailBuffer, ar *suiteRunAttemptResult, errWriter io.Writer) bool {
	if err := verifyAttemptMatchesEnv(pm.OutDirAbs, env); err != nil {
		ar.RunnerErrorCode = codeUsage
		fmt.Fprintf(errWriter, codeUsage+": suite run: %s\n", err.Error())
		return true
	}
	if !opts.Blind {
		return runSuiteRunner(r, pm, env, opts.RunnerCmd, opts.RunnerArgs, stdoutTB, stderrTB, ar, errWriter)
	}
	return executeSuiteRunBlindRunner(r, pm, opts, env, stdoutTB, stderrTB, ar, errWriter)
}

func executeSuiteRunBlindRunner(r Runner, pm planner.PlannedMission, opts suiteRunExecOpts, env map[string]string, stdoutTB *tailBuffer, stderrTB *tailBuffer, ar *suiteRunAttemptResult, errWriter io.Writer) bool {
	found := promptContamination(pm.OutDirAbs, opts.BlindTerms)
	if len(found) == 0 {
		return runSuiteRunner(r, pm, env, opts.RunnerCmd, opts.RunnerArgs, stdoutTB, stderrTB, ar, errWriter)
	}
	ar.RunnerErrorCode = codeContaminatedPrompt
	msg := "prompt contamination detected: " + strings.Join(found, ",")
	envTrace := suiteRunTraceEnv(env, pm.OutDirAbs)
	if err := trace.AppendCLIRunEvent(r.Now(), envTrace, []string{"zcl", "blind-check"}, trace.ResultForTrace{
		SpawnError: codeContaminatedPrompt,
		DurationMs: 0,
		OutBytes:   0,
		ErrBytes:   int64(len(msg)),
		ErrPreview: msg,
	}); err != nil {
		ar.RunnerErrorCode = codeIO
		fmt.Fprintf(errWriter, codeIO+": suite run: %s\n", err.Error())
		return true
	}
	if err := feedback.Write(r.Now(), envTrace, feedback.WriteOpts{
		OK:                   false,
		Result:               "CONTAMINATED_PROMPT",
		DecisionTags:         []string{schema.DecisionTagBlocked, schema.DecisionTagContaminatedPrompt},
		SkipSuiteResultShape: true,
	}); err != nil {
		ar.RunnerErrorCode = codeIO
		fmt.Fprintf(errWriter, codeIO+": suite run: %s\n", err.Error())
		return true
	}
	return false
}

func finalizeSuiteRunAttemptResult(r Runner, pm planner.PlannedMission, opts suiteRunExecOpts, env map[string]string, ar *suiteRunAttemptResult) {
	ar.Finish = finishAttempt(r.Now(), pm.OutDirAbs, opts.Strict, opts.StrictExpect)
	runnerOK := ar.RunnerErrorCode == "" && ar.RunnerExitCode != nil && *ar.RunnerExitCode == 0
	ar.OK = runnerOK && ar.Finish.OK
	_ = env
}

func emitSuiteRunAttemptFinished(r Runner, opts suiteRunExecOpts, env map[string]string, pm planner.PlannedMission, ar suiteRunAttemptResult) {
	if opts.Progress == nil {
		return
	}
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

func applySuiteRunRunnerCwdCleanup(cleanupRunnerCwd func(bool) error, harnessErr *bool, ar *suiteRunAttemptResult, errWriter io.Writer) {
	if cleanupRunnerCwd == nil {
		return
	}
	if err := cleanupRunnerCwd(ar.OK); err != nil {
		*harnessErr = true
		if ar.RunnerErrorCode == "" {
			ar.RunnerErrorCode = codeIO
		}
		fmt.Fprintf(errWriter, codeIO+": suite run: %s\n", err.Error())
	}
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

func writeAttemptRuntimeEnvArtifact(now time.Time, pm planner.PlannedMission, explicitEnv map[string]string, opts suiteRunExecOpts, runtimeCtx suiteRunAttemptRuntimeContext) error {
	outDir := strings.TrimSpace(pm.OutDirAbs)
	if outDir == "" {
		return fmt.Errorf("missing attempt out dir for runtime env artifact")
	}
	envPolicy := native.DefaultEnvPolicy()
	explicit := copyStringMap(explicitEnv)
	explicit = envPolicy.RedactForLog(explicit)

	merged := mergeEnvironMap(os.Environ(), explicitEnv)
	effective := merged
	var blocked []string
	if opts.NativeMode {
		allowed, blockedKeys := envPolicy.Filter(merged)
		effective = allowed
		blocked = blockedKeys
	}

	promptRaw := strings.TrimSpace(pm.Prompt)
	sum := sha256.Sum256([]byte(promptRaw))
	promptSourceKind := strings.TrimSpace(explicitEnv["ZCL_PROMPT_SOURCE_KIND"])
	if promptSourceKind == "" {
		promptSourceKind = "suite_prompt"
	}
	artifact := schema.AttemptRuntimeEnvJSONV1{
		SchemaVersion: schema.ArtifactSchemaV1,
		RunID:         strings.TrimSpace(explicitEnv["ZCL_RUN_ID"]),
		SuiteID:       strings.TrimSpace(explicitEnv["ZCL_SUITE_ID"]),
		MissionID:     strings.TrimSpace(explicitEnv["ZCL_MISSION_ID"]),
		AttemptID:     strings.TrimSpace(explicitEnv["ZCL_ATTEMPT_ID"]),
		AgentID:       strings.TrimSpace(explicitEnv["ZCL_AGENT_ID"]),
		CreatedAt:     now.UTC().Format(time.RFC3339Nano),
		Runtime: schema.AttemptRuntimeContextV1{
			IsolationModel: strings.TrimSpace(opts.IsolationModel),
			ToolDriverKind: strings.TrimSpace(explicitEnv["ZCL_TOOL_DRIVER_KIND"]),
			RuntimeID:      string(opts.NativeSelection.Selected),
			NativeMode:     opts.NativeMode,
			StartCwdMode:   strings.TrimSpace(runtimeCtx.StartCwdMode),
			StartCwd:       strings.TrimSpace(runtimeCtx.StartCwd),
			StartCwdRetain: strings.TrimSpace(runtimeCtx.StartCwdRetain),
		},
		Prompt: schema.AttemptPromptMetadataV1{
			SourceKind:   promptSourceKind,
			SourcePath:   strings.TrimSpace(explicitEnv["ZCL_PROMPT_SOURCE_PATH"]),
			TemplatePath: strings.TrimSpace(explicitEnv["ZCL_PROMPT_TEMPLATE_PATH"]),
			SHA256:       hex.EncodeToString(sum[:]),
			Bytes:        int64(len(pm.Prompt)),
		},
		Env: schema.AttemptRuntimeEnvironmentV1{
			Explicit:      explicit,
			EffectiveKeys: sortedEnvKeys(effective),
			BlockedKeys:   append([]string(nil), blocked...),
		},
	}
	return store.WriteJSONAtomic(filepath.Join(outDir, schema.AttemptRuntimeEnvFileNameV1), artifact)
}

func mergeEnvironMap(base []string, overrides map[string]string) map[string]string {
	out := map[string]string{}
	for _, kv := range base {
		parts := strings.SplitN(kv, "=", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		if key == "" {
			continue
		}
		out[key] = parts[1]
	}
	for k, v := range overrides {
		key := strings.TrimSpace(k)
		if key == "" {
			continue
		}
		out[key] = v
	}
	return out
}

func sortedEnvKeys(m map[string]string) []string {
	if len(m) == 0 {
		return nil
	}
	keys := make([]string, 0, len(m))
	for key := range m {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		keys = append(keys, key)
	}
	sort.Strings(keys)
	if len(keys) == 0 {
		return nil
	}
	return keys
}

func runSuiteRunner(r Runner, pm planner.PlannedMission, env map[string]string, runnerCmd string, runnerArgs []string, stdoutTB *tailBuffer, stderrTB *tailBuffer, ar *suiteRunAttemptResult, errWriter io.Writer) bool {
	return runSuiteRunnerImpl(r, pm, env, runnerCmd, runnerArgs, stdoutTB, stderrTB, ar, errWriter)
}

func runSuiteRunnerImpl(r Runner, pm planner.PlannedMission, env map[string]string, runnerCmd string, runnerArgs []string, stdoutTB *tailBuffer, stderrTB *tailBuffer, ar *suiteRunAttemptResult, errWriter io.Writer) bool {
	return runSuiteRunnerCore(r, pm, env, runnerCmd, runnerArgs, stdoutTB, stderrTB, ar, errWriter)
}

func runSuiteRunnerCore(r Runner, pm planner.PlannedMission, env map[string]string, runnerCmd string, runnerArgs []string, stdoutTB *tailBuffer, stderrTB *tailBuffer, ar *suiteRunAttemptResult, errWriter io.Writer) bool {
	errWriter = defaultSuiteRunErrWriter(errWriter, r.Stderr)
	ctx, cancel, timedOut := attemptCtxForDeadline(r.Now(), pm.OutDirAbs)
	if cancel != nil {
		defer cancel()
	}
	if timedOut {
		ar.RunnerErrorCode = codeTimeout
		return false
	}
	fmt.Fprintf(errWriter, "suite run: mission=%s attempt=%s runner=%s\n", pm.MissionID, pm.AttemptID, filepath.Base(runnerCmd))

	cmd := buildSuiteRunRunnerCommand(ctx, env, runnerCmd, runnerArgs, errWriter, stdoutTB, stderrTB)
	err := cmd.Run()
	setSuiteRunRunnerExitCode(ar, cmd, err)
	return classifySuiteRunRunnerExecution(err, ctx, ar)
}

func defaultSuiteRunErrWriter(errWriter io.Writer, fallback io.Writer) io.Writer {
	if errWriter != nil {
		return errWriter
	}
	return fallback
}

func buildSuiteRunRunnerCommand(ctx context.Context, env map[string]string, runnerCmd string, runnerArgs []string, errWriter io.Writer, stdoutTB *tailBuffer, stderrTB *tailBuffer) *exec.Cmd {
	cmd := exec.CommandContext(ctx, runnerCmd, runnerArgs...)
	cmd.Env = mergeEnviron(os.Environ(), env)
	cmd.Stdin = os.Stdin
	if stdoutTB != nil && stderrTB != nil {
		cmd.Stdout = io.MultiWriter(errWriter, stdoutTB)
		cmd.Stderr = io.MultiWriter(errWriter, stderrTB)
		return cmd
	}
	cmd.Stdout = errWriter
	cmd.Stderr = errWriter
	return cmd
}

func setSuiteRunRunnerExitCode(ar *suiteRunAttemptResult, cmd *exec.Cmd, runErr error) {
	if cmd.ProcessState != nil {
		ec := cmd.ProcessState.ExitCode()
		ar.RunnerExitCode = &ec
		return
	}
	if runErr == nil {
		ec := 0
		ar.RunnerExitCode = &ec
	}
}

func classifySuiteRunRunnerExecution(runErr error, ctx context.Context, ar *suiteRunAttemptResult) bool {
	if runErr != nil {
		if errors.Is(runErr, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
			ar.RunnerErrorCode = codeTimeout
			return true
		}
		if isStartFailure(runErr) {
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
	b, err := os.ReadFile(filepath.Join(attemptDir, artifacts.PromptTXT))
	if err != nil {
		return nil
	}
	return blind.FindContaminationTerms(string(b), terms)
}

func printSuiteRunHelp(w io.Writer) {
	fmt.Fprint(w, `Usage:
  zcl suite run --file <suite.(yaml|yml|json)> [--run-id <runId>] [--mode discovery|ci] [--timeout-ms N] [--timeout-start attempt_start|first_tool_call] [--feedback-policy strict|auto_fail] [--finalization-mode strict|auto_fail|auto_from_result_json] [--result-channel none|file_json|stdout_json] [--result-file <attempt-relative-path>] [--result-marker <prefix>] [--result-min-turn N] [--campaign-id <id>] [--campaign-state <path>] [--progress-jsonl <path|->] [--blind on|off] [--blind-terms a,b,c] [--session-isolation auto|process|native] [--runtime-strategies <csv>] [--native-model <slug>] [--native-model-reasoning-effort none|minimal|low|medium|high|xhigh] [--native-model-reasoning-policy best_effort|required] [--parallel N] [--total M] [--mission-offset N] [--out-root .zcl] [--fail-fast] [--strict] [--strict-expect] [--shim <bin>] [--capture-runner-io] --json [-- <runner-cmd> [args...]]

Notes:
  - Requires --json (stdout is reserved for JSON; runner stdout/stderr is streamed to stderr).
  - process mode requires -- <runner-cmd>; native mode forbids it.
  - --session-isolation=auto chooses native mode when ZCL_HOST_NATIVE_SPAWN=1, otherwise process mode.
  - --runtime-strategies controls ordered native runtime fallback chain (default from config/env).
  - --native-model and --native-model-reasoning-* apply only in native mode and are forwarded to thread/start.
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
	return finishAttemptImpl(now, attemptDir, strict, strictExpect)
}

func finishAttemptImpl(now time.Time, attemptDir string, strict bool, strictExpect bool) suiteRunFinishResult {
	return finishAttemptCore(now, attemptDir, strict, strictExpect)
}

func finishAttemptCore(now time.Time, attemptDir string, strict bool, strictExpect bool) suiteRunFinishResult {
	out := suiteRunFinishResult{
		OK:           false,
		Strict:       strict,
		StrictExpect: strictExpect,
		AttemptDir:   attemptDir,
	}

	rep, repErr, reportErr, ioErr := buildSuiteRunFinishReport(now, attemptDir, strict)
	if ioErr != nil {
		out.IOError = ioErr.Error()
		return out
	}
	out.Report = rep
	out.ReportError = reportErr

	valRes, expRes, err := evaluateSuiteRunFinish(attemptDir, strict, strictExpect)
	if err != nil {
		out.IOError = err.Error()
		return out
	}
	out.Validate = valRes
	out.Expect = expRes

	ok := valRes.OK && expRes.OK
	if repErr == nil && rep.OK != nil && !*rep.OK {
		ok = false
	}
	out.OK = ok && out.ReportError == nil
	return out
}

func buildSuiteRunFinishReport(now time.Time, attemptDir string, strict bool) (schema.AttemptReportJSONV1, error, *suiteRunReportErr, error) {
	rep, repErr := report.BuildAttemptReport(now, attemptDir, strict)
	if repErr == nil {
		return rep, nil, nil, writeSuiteRunFinishReport(attemptDir, rep)
	}
	var ce *report.CliError
	if !errors.As(repErr, &ce) {
		return schema.AttemptReportJSONV1{}, repErr, nil, repErr
	}
	reportErr := &suiteRunReportErr{Code: ce.Code, Message: ce.Message}
	fallback, ferr := report.BuildAttemptReport(now, attemptDir, false)
	if ferr == nil {
		if err := writeSuiteRunFinishReport(attemptDir, fallback); err != nil {
			return fallback, repErr, reportErr, err
		}
		return fallback, repErr, reportErr, nil
	}
	return schema.AttemptReportJSONV1{}, repErr, reportErr, nil
}

func writeSuiteRunFinishReport(attemptDir string, rep schema.AttemptReportJSONV1) error {
	return report.WriteAttemptReportAtomic(filepath.Join(attemptDir, artifacts.AttemptReportJSON), rep)
}

func evaluateSuiteRunFinish(attemptDir string, strict bool, strictExpect bool) (validate.Result, expect.Result, error) {
	valRes, err := validate.ValidatePath(attemptDir, strict)
	if err != nil {
		return validate.Result{}, expect.Result{}, err
	}
	expRes, err := expect.ExpectPath(attemptDir, strictExpect)
	if err != nil {
		return validate.Result{}, expect.Result{}, err
	}
	return valRes, expRes, nil
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
  echo "%s: missing ZCL_SHIM_BIN_DIR" >&2
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
`, codeShim, bin)
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
