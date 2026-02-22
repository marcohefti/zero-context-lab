package campaign

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/marcohefti/zero-context-lab/internal/codes"
	"github.com/marcohefti/zero-context-lab/internal/ids"
	"github.com/marcohefti/zero-context-lab/internal/schema"
	"github.com/marcohefti/zero-context-lab/internal/suite"
	"gopkg.in/yaml.v3"
)

const (
	RunnerTypeProcessCmd   = "process_cmd"
	RunnerTypeCodexExec    = "codex_exec"
	RunnerTypeCodexSub     = "codex_subagent"
	RunnerTypeClaudeSub    = "claude_subagent"
	RunnerTypeCodexAppSrv  = "codex_app_server"
	PromptModeDefault      = "default"
	PromptModeMissionOnly  = "mission_only"
	PromptModeExam         = "exam"
	RunStatusValid         = "valid"
	RunStatusInvalid       = "invalid"
	RunStatusAborted       = "aborted"
	RunStatusRunning       = "running"
	ReasonGateFailed       = codes.CampaignGateFailed
	ReasonFirstMissionGate = codes.CampaignFirstMissionGateFailed
	ReasonFlowFailed       = codes.CampaignFlowFailed
	ReasonAborted          = codes.CampaignAborted
	ReasonSemanticFailed   = codes.CampaignSemanticFailed
	ReasonPromptModePolicy = codes.CampaignPromptModeViolation
	ReasonExamPromptPolicy = codes.CampaignExamPromptViolation
	ReasonToolDriverShim   = codes.CampaignToolDriverShimRequired
	ReasonOracleVisibility = codes.CampaignOracleVisibility
	ReasonOracleEvaluator  = codes.CampaignOracleEvaluatorMissing
	ReasonOracleEvalFailed = codes.CampaignOracleEvalFailed
	ReasonOracleEvalError  = codes.CampaignOracleEvalError

	SelectionModeAll       = "all"
	SelectionModeMissionID = "mission_id"
	SelectionModeIndex     = "index"
	SelectionModeRange     = "range"

	FlowModeSequence = "sequence"
	FlowModeParallel = "parallel"

	TraceProfileNone              = "none"
	TraceProfileStrictBrowserComp = "strict_browser_comparison"
	TraceProfileMCPRequired       = "mcp_required"

	ToolDriverShell     = "shell"
	ToolDriverCLIFunnel = "cli_funnel"
	ToolDriverMCPProxy  = "mcp_proxy"
	ToolDriverHTTPProxy = "http_proxy"

	FinalizationModeStrict             = "strict"
	FinalizationModeAutoFail           = "auto_fail"
	FinalizationModeAutoFromResultJSON = "auto_from_result_json"

	ModelReasoningEffortNone    = "none"
	ModelReasoningEffortMinimal = "minimal"
	ModelReasoningEffortLow     = "low"
	ModelReasoningEffortMedium  = "medium"
	ModelReasoningEffortHigh    = "high"
	ModelReasoningEffortXHigh   = "xhigh"

	ModelReasoningPolicyBestEffort = "best_effort"
	ModelReasoningPolicyRequired   = "required"

	ResultChannelNone       = "none"
	ResultChannelFileJSON   = "file_json"
	ResultChannelStdoutJSON = "stdout_json"

	OracleVisibilityWorkspace = "workspace"
	OracleVisibilityHostOnly  = "host_only"

	EvaluationModeNone   = "none"
	EvaluationModeOracle = "oracle"
	EvaluatorKindScript  = "script"

	DefaultResultChannelPath   = "mission.result.json"
	DefaultResultChannelMarker = "ZCL_RESULT_JSON:"
	DefaultMinResultTurn       = 1
)

type SpecV1 struct {
	SchemaVersion int    `json:"schemaVersion" yaml:"schemaVersion"`
	CampaignID    string `json:"campaignId" yaml:"campaignId"`
	OutRoot       string `json:"outRoot,omitempty" yaml:"outRoot,omitempty"`
	PromptMode    string `json:"promptMode,omitempty" yaml:"promptMode,omitempty"` // default|mission_only|exam

	TotalMissions  int  `json:"totalMissions,omitempty" yaml:"totalMissions,omitempty"`
	CanaryMissions int  `json:"canaryMissions,omitempty" yaml:"canaryMissions,omitempty"`
	FailFast       bool `json:"failFast" yaml:"failFast"`

	MissionSource MissionSourceSpec `json:"missionSource,omitempty" yaml:"missionSource,omitempty"`
	Evaluation    EvaluationSpec    `json:"evaluation,omitempty" yaml:"evaluation,omitempty"`
	Execution     ExecutionSpec     `json:"execution,omitempty" yaml:"execution,omitempty"`
	PairGate      PairGateSpec      `json:"pairGate,omitempty" yaml:"pairGate,omitempty"`
	Semantic      SemanticGateSpec  `json:"semantic,omitempty" yaml:"semantic,omitempty"`
	Cleanup       CleanupSpec       `json:"cleanup,omitempty" yaml:"cleanup,omitempty"`
	Timeouts      TimeoutsSpec      `json:"timeouts,omitempty" yaml:"timeouts,omitempty"`
	Output        OutputPolicySpec  `json:"output,omitempty" yaml:"output,omitempty"`
	NoContext     NoContextSpec     `json:"noContext,omitempty" yaml:"noContext,omitempty"`

	InvalidRunPolicy InvalidRunPolicySpec `json:"invalidRunPolicy,omitempty" yaml:"invalidRunPolicy,omitempty"`

	Flows []FlowSpec `json:"flows" yaml:"flows"`

	Extensions map[string]any `json:"-" yaml:"-"`
}

type MissionSourceSpec struct {
	Path         string               `json:"path,omitempty" yaml:"path,omitempty"`
	PromptSource PromptSourceSpec     `json:"promptSource,omitempty" yaml:"promptSource,omitempty"`
	OracleSource OracleSourceSpec     `json:"oracleSource,omitempty" yaml:"oracleSource,omitempty"`
	Selection    MissionSelectionSpec `json:"selection,omitempty" yaml:"selection,omitempty"`
}

type PromptSourceSpec struct {
	Path string `json:"path,omitempty" yaml:"path,omitempty"`
}

type OracleSourceSpec struct {
	Path       string `json:"path,omitempty" yaml:"path,omitempty"`
	Visibility string `json:"visibility,omitempty" yaml:"visibility,omitempty"` // workspace|host_only
}

type EvaluationSpec struct {
	Mode      string        `json:"mode,omitempty" yaml:"mode,omitempty"` // none|oracle
	Evaluator EvaluatorSpec `json:"evaluator,omitempty" yaml:"evaluator,omitempty"`
}

type EvaluatorSpec struct {
	Kind    string   `json:"kind,omitempty" yaml:"kind,omitempty"` // script
	Command []string `json:"command,omitempty" yaml:"command,omitempty"`
}

type MissionSelectionSpec struct {
	Mode       string             `json:"mode,omitempty" yaml:"mode,omitempty"`
	MissionIDs []string           `json:"missionIds,omitempty" yaml:"missionIds,omitempty"`
	Indexes    []int              `json:"indexes,omitempty" yaml:"indexes,omitempty"`
	Range      MissionRangeWindow `json:"range,omitempty" yaml:"range,omitempty"`
}

type MissionRangeWindow struct {
	Start int `json:"start" yaml:"start"`
	End   int `json:"end" yaml:"end"`
}

type PairGateSpec struct {
	Enabled                   *bool  `json:"enabled,omitempty" yaml:"enabled,omitempty"`
	StopOnFirstMissionFailure bool   `json:"stopOnFirstMissionFailure" yaml:"stopOnFirstMissionFailure"`
	TraceProfile              string `json:"traceProfile,omitempty" yaml:"traceProfile,omitempty"`
}

type ExecutionSpec struct {
	FlowMode string `json:"flowMode,omitempty" yaml:"flowMode,omitempty"` // sequence|parallel
}

type SemanticGateSpec struct {
	Enabled   bool   `json:"enabled" yaml:"enabled"`
	RulesPath string `json:"rulesPath,omitempty" yaml:"rulesPath,omitempty"`
}

type CleanupSpec struct {
	BeforeMission []string `json:"beforeMission,omitempty" yaml:"beforeMission,omitempty"`
	AfterMission  []string `json:"afterMission,omitempty" yaml:"afterMission,omitempty"`
	OnFailure     []string `json:"onFailure,omitempty" yaml:"onFailure,omitempty"`

	// Backward-compatible aliases.
	PreMission  []string `json:"preMission,omitempty" yaml:"preMission,omitempty"`
	PostMission []string `json:"postMission,omitempty" yaml:"postMission,omitempty"`
}

type TimeoutsSpec struct {
	CampaignGlobalTimeoutMs int64  `json:"campaignGlobalTimeoutMs,omitempty" yaml:"campaignGlobalTimeoutMs,omitempty"`
	DefaultAttemptTimeoutMs int64  `json:"defaultAttemptTimeoutMs,omitempty" yaml:"defaultAttemptTimeoutMs,omitempty"`
	CleanupHookTimeoutMs    int64  `json:"cleanupHookTimeoutMs,omitempty" yaml:"cleanupHookTimeoutMs,omitempty"`
	TimeoutStart            string `json:"timeoutStart,omitempty" yaml:"timeoutStart,omitempty"`
}

type NoContextSpec struct {
	ForbiddenPromptTerms []string `json:"forbiddenPromptTerms,omitempty" yaml:"forbiddenPromptTerms,omitempty"`
}

type OutputPolicySpec struct {
	ReportPath    string `json:"reportPath,omitempty" yaml:"reportPath,omitempty"`
	SummaryPath   string `json:"summaryPath,omitempty" yaml:"summaryPath,omitempty"`
	ResultsMDPath string `json:"resultsMdPath,omitempty" yaml:"resultsMdPath,omitempty"`
	PublishCheck  string `json:"publishCheck,omitempty" yaml:"publishCheck,omitempty"`
	ProgressJSONL string `json:"progressJsonl,omitempty" yaml:"progressJsonl,omitempty"`
}

type InvalidRunPolicySpec struct {
	Statuses             []string `json:"statuses,omitempty" yaml:"statuses,omitempty"`
	PublishRequiresValid *bool    `json:"publishRequiresValid,omitempty" yaml:"publishRequiresValid,omitempty"`
	ForceFlag            string   `json:"forceFlag,omitempty" yaml:"forceFlag,omitempty"`
}

type FlowSpec struct {
	FlowID           string              `json:"flowId" yaml:"flowId"`
	SuiteFile        string              `json:"suiteFile,omitempty" yaml:"suiteFile,omitempty"`
	Runner           RunnerAdapterSpec   `json:"runner" yaml:"runner"`
	AdapterContract  AdapterContractSpec `json:"adapterContract,omitempty" yaml:"adapterContract,omitempty"`
	RunnerExtensions map[string]any      `json:"-" yaml:"-"`
}

type AdapterContractSpec struct {
	RequiredOutputFields []string `json:"requiredOutputFields,omitempty" yaml:"requiredOutputFields,omitempty"`
}

type RunnerAdapterSpec struct {
	Type string `json:"type" yaml:"type"`
	// Command is argv form: ["bash","-lc","./runner.sh"].
	Command []string          `json:"command" yaml:"command"`
	Env     map[string]string `json:"env,omitempty" yaml:"env,omitempty"`
	Shims   []string          `json:"shims,omitempty" yaml:"shims,omitempty"`
	// RuntimeStrategies is an ordered native runtime fallback chain used when sessionIsolation=native.
	RuntimeStrategies []string `json:"runtimeStrategies,omitempty" yaml:"runtimeStrategies,omitempty"`
	// Model is forwarded to native thread/start for codex_app_server flows.
	Model string `json:"model,omitempty" yaml:"model,omitempty"`
	// ModelReasoningEffort is forwarded as a per-thread config hint.
	ModelReasoningEffort string `json:"modelReasoningEffort,omitempty" yaml:"modelReasoningEffort,omitempty"`
	// ModelReasoningPolicy controls behavior when reasoning effort is unsupported.
	ModelReasoningPolicy string `json:"modelReasoningPolicy,omitempty" yaml:"modelReasoningPolicy,omitempty"`

	SessionIsolation string           `json:"sessionIsolation,omitempty" yaml:"sessionIsolation,omitempty"` // auto|process|native
	FeedbackPolicy   string           `json:"feedbackPolicy,omitempty" yaml:"feedbackPolicy,omitempty"`     // strict|auto_fail
	Mode             string           `json:"mode,omitempty" yaml:"mode,omitempty"`                         // discovery|ci
	TimeoutMs        int64            `json:"timeoutMs,omitempty" yaml:"timeoutMs,omitempty"`
	TimeoutStart     string           `json:"timeoutStart,omitempty" yaml:"timeoutStart,omitempty"` // attempt_start|first_tool_call
	Strict           *bool            `json:"strict,omitempty" yaml:"strict,omitempty"`
	StrictExpect     *bool            `json:"strictExpect,omitempty" yaml:"strictExpect,omitempty"`
	ToolDriver       ToolDriverSpec   `json:"toolDriver,omitempty" yaml:"toolDriver,omitempty"`
	Finalization     FinalizationSpec `json:"finalization,omitempty" yaml:"finalization,omitempty"`

	MCP MCPLifecycleSpec `json:"mcp,omitempty" yaml:"mcp,omitempty"`

	// FreshAgentPerAttempt defaults to true. Hidden session reuse is never implicit.
	FreshAgentPerAttempt *bool `json:"freshAgentPerAttempt,omitempty" yaml:"freshAgentPerAttempt,omitempty"`
}

type MCPLifecycleSpec struct {
	MaxToolCalls       int64 `json:"maxToolCalls,omitempty" yaml:"maxToolCalls,omitempty"`
	IdleTimeoutMs      int64 `json:"idleTimeoutMs,omitempty" yaml:"idleTimeoutMs,omitempty"`
	ShutdownOnComplete bool  `json:"shutdownOnComplete,omitempty" yaml:"shutdownOnComplete,omitempty"`
}

type ToolDriverSpec struct {
	Kind  string   `json:"kind,omitempty" yaml:"kind,omitempty"` // shell|cli_funnel|mcp_proxy|http_proxy
	Shims []string `json:"shims,omitempty" yaml:"shims,omitempty"`
}

type FinalizationSpec struct {
	Mode          string            `json:"mode,omitempty" yaml:"mode,omitempty"` // strict|auto_fail|auto_from_result_json
	MinResultTurn int               `json:"minResultTurn,omitempty" yaml:"minResultTurn,omitempty"`
	ResultChannel ResultChannelSpec `json:"resultChannel,omitempty" yaml:"resultChannel,omitempty"`
}

type ResultChannelSpec struct {
	Kind   string `json:"kind,omitempty" yaml:"kind,omitempty"`     // none|file_json|stdout_json
	Path   string `json:"path,omitempty" yaml:"path,omitempty"`     // required for file_json (relative to attempt dir)
	Marker string `json:"marker,omitempty" yaml:"marker,omitempty"` // stdout_json marker prefix
}

type ParsedSpec struct {
	SpecPath string
	Spec     SpecV1
	// First flow suite drives canonical mission index/order for pair gating.
	BaseSuite suite.ParsedSuite
	// FlowSuites are parsed suites per flowId.
	FlowSuites map[string]suite.ParsedSuite
	// MissionIndexes is the canonical campaign selection/order after missionSource.selection.
	MissionIndexes []int
	// OracleByMissionID maps mission ids to host-side oracle file paths in exam mode.
	OracleByMissionID map[string]string
}

type PromptModeViolation struct {
	FlowID       string `json:"flowId"`
	MissionID    string `json:"missionId"`
	MissionIndex int    `json:"missionIndex"`
	Term         string `json:"term"`
}

type ExecutionModeSummary struct {
	Mode               string   `json:"mode"`
	AdapterScriptFlows []string `json:"adapterScriptFlows,omitempty"`
}

type PromptModeViolationError struct {
	Code       string                `json:"code,omitempty"`
	PromptMode string                `json:"promptMode"`
	Violations []PromptModeViolation `json:"violations"`
}

func (e *PromptModeViolationError) Error() string {
	if e == nil || len(e.Violations) == 0 {
		return "prompt mode violation"
	}
	v := e.Violations[0]
	return fmt.Sprintf("prompt mode violation: mode=%s flow=%s mission=%s index=%d term=%q", e.PromptMode, v.FlowID, v.MissionID, v.MissionIndex, v.Term)
}

type ToolDriverShimRequirement struct {
	FlowID         string   `json:"flowId"`
	PromptMode     string   `json:"promptMode,omitempty"`
	ToolDriverKind string   `json:"toolDriverKind"`
	RequiredOneOf  []string `json:"requiredOneOf"`
	Snippet        string   `json:"snippet"`
}

type ToolDriverShimRequirementError struct {
	Code      string                    `json:"code"`
	Violation ToolDriverShimRequirement `json:"violation"`
}

func (e *ToolDriverShimRequirementError) Error() string {
	if e == nil {
		return "toolDriver shim requirement violated"
	}
	v := e.Violation
	return fmt.Sprintf(
		"flow %q: promptMode=%s with toolDriver.kind=%s requires shims; set one of %s (example: %s)",
		v.FlowID,
		v.PromptMode,
		v.ToolDriverKind,
		strings.Join(v.RequiredOneOf, " or "),
		v.Snippet,
	)
}

type OraclePolicyViolation struct {
	Field       string `json:"field"`
	PromptMode  string `json:"promptMode,omitempty"`
	Visibility  string `json:"visibility,omitempty"`
	OraclePath  string `json:"oraclePath,omitempty"`
	AgentRoot   string `json:"agentRoot,omitempty"`
	Description string `json:"description"`
}

type OraclePolicyViolationError struct {
	Code      string                `json:"code"`
	Violation OraclePolicyViolation `json:"violation"`
}

func (e *OraclePolicyViolationError) Error() string {
	if e == nil {
		return "oracle policy violation"
	}
	v := e.Violation
	msg := strings.TrimSpace(v.Description)
	if msg == "" {
		msg = "oracle policy violation"
	}
	return msg
}

func ParseSpecFile(path string) (ParsedSpec, error) {
	absPath, err := filepath.Abs(strings.TrimSpace(path))
	if err != nil {
		return ParsedSpec{}, err
	}
	raw, err := os.ReadFile(absPath)
	if err != nil {
		return ParsedSpec{}, err
	}

	spec, err := decodeSpecStrict(absPath, raw)
	if err != nil {
		return ParsedSpec{}, err
	}

	if spec.SchemaVersion == 0 {
		spec.SchemaVersion = 1
	}
	if spec.SchemaVersion != 1 {
		return ParsedSpec{}, fmt.Errorf("unsupported campaign schemaVersion (expected 1)")
	}
	spec.CampaignID = ids.SanitizeComponent(strings.TrimSpace(spec.CampaignID))
	if spec.CampaignID == "" {
		return ParsedSpec{}, fmt.Errorf("missing/invalid campaignId")
	}
	if spec.TotalMissions < 0 {
		return ParsedSpec{}, fmt.Errorf("totalMissions must be >= 0")
	}
	if spec.CanaryMissions < 0 {
		return ParsedSpec{}, fmt.Errorf("canaryMissions must be >= 0")
	}
	spec.PromptMode = strings.ToLower(strings.TrimSpace(spec.PromptMode))
	if spec.PromptMode == "" {
		spec.PromptMode = PromptModeDefault
	}
	if !isValidPromptMode(spec.PromptMode) {
		return ParsedSpec{}, fmt.Errorf("invalid promptMode (expected %s|%s|%s)", PromptModeDefault, PromptModeMissionOnly, PromptModeExam)
	}
	spec.NoContext.ForbiddenPromptTerms = normalizeTerms(spec.NoContext.ForbiddenPromptTerms)
	if spec.PromptMode == PromptModeMissionOnly && len(spec.NoContext.ForbiddenPromptTerms) == 0 {
		spec.NoContext.ForbiddenPromptTerms = defaultMissionOnlyForbiddenTerms()
	}
	if spec.PromptMode == PromptModeExam && len(spec.NoContext.ForbiddenPromptTerms) == 0 {
		spec.NoContext.ForbiddenPromptTerms = defaultExamForbiddenTerms()
	}
	spec.MissionSource.Path = strings.TrimSpace(spec.MissionSource.Path)
	if spec.MissionSource.Path != "" && !filepath.IsAbs(spec.MissionSource.Path) {
		spec.MissionSource.Path = filepath.Clean(filepath.Join(filepath.Dir(absPath), spec.MissionSource.Path))
	}
	spec.MissionSource.PromptSource.Path = strings.TrimSpace(spec.MissionSource.PromptSource.Path)
	if spec.MissionSource.PromptSource.Path != "" && !filepath.IsAbs(spec.MissionSource.PromptSource.Path) {
		spec.MissionSource.PromptSource.Path = filepath.Clean(filepath.Join(filepath.Dir(absPath), spec.MissionSource.PromptSource.Path))
	}
	spec.MissionSource.OracleSource.Path = strings.TrimSpace(spec.MissionSource.OracleSource.Path)
	if spec.MissionSource.OracleSource.Path != "" && !filepath.IsAbs(spec.MissionSource.OracleSource.Path) {
		spec.MissionSource.OracleSource.Path = filepath.Clean(filepath.Join(filepath.Dir(absPath), spec.MissionSource.OracleSource.Path))
	}
	spec.MissionSource.OracleSource.Visibility = strings.ToLower(strings.TrimSpace(spec.MissionSource.OracleSource.Visibility))
	if spec.MissionSource.OracleSource.Visibility == "" {
		spec.MissionSource.OracleSource.Visibility = OracleVisibilityWorkspace
	}
	if !isValidOracleVisibility(spec.MissionSource.OracleSource.Visibility) {
		return ParsedSpec{}, fmt.Errorf("invalid missionSource.oracleSource.visibility (expected %s|%s)", OracleVisibilityWorkspace, OracleVisibilityHostOnly)
	}
	spec.Evaluation.Mode = strings.ToLower(strings.TrimSpace(spec.Evaluation.Mode))
	if spec.Evaluation.Mode == "" {
		spec.Evaluation.Mode = EvaluationModeNone
	}
	if !isValidEvaluationMode(spec.Evaluation.Mode) {
		return ParsedSpec{}, fmt.Errorf("invalid evaluation.mode (expected %s|%s)", EvaluationModeNone, EvaluationModeOracle)
	}
	spec.Evaluation.Evaluator.Kind = strings.ToLower(strings.TrimSpace(spec.Evaluation.Evaluator.Kind))
	if spec.Evaluation.Mode == EvaluationModeOracle && spec.Evaluation.Evaluator.Kind == "" {
		spec.Evaluation.Evaluator.Kind = EvaluatorKindScript
	}
	if spec.Evaluation.Evaluator.Kind != "" && !isValidEvaluatorKind(spec.Evaluation.Evaluator.Kind) {
		return ParsedSpec{}, fmt.Errorf("invalid evaluation.evaluator.kind (expected %s)", EvaluatorKindScript)
	}
	spec.Evaluation.Evaluator.Command = normalizeCommand(spec.Evaluation.Evaluator.Command)
	spec.Semantic.RulesPath = strings.TrimSpace(spec.Semantic.RulesPath)
	if spec.Semantic.RulesPath != "" && !filepath.IsAbs(spec.Semantic.RulesPath) {
		spec.Semantic.RulesPath = filepath.Clean(filepath.Join(filepath.Dir(absPath), spec.Semantic.RulesPath))
	}
	spec.Output.ReportPath = strings.TrimSpace(spec.Output.ReportPath)
	if spec.Output.ReportPath != "" && !filepath.IsAbs(spec.Output.ReportPath) {
		spec.Output.ReportPath = filepath.Clean(filepath.Join(filepath.Dir(absPath), spec.Output.ReportPath))
	}
	spec.Output.SummaryPath = strings.TrimSpace(spec.Output.SummaryPath)
	if spec.Output.SummaryPath != "" && !filepath.IsAbs(spec.Output.SummaryPath) {
		spec.Output.SummaryPath = filepath.Clean(filepath.Join(filepath.Dir(absPath), spec.Output.SummaryPath))
	}
	spec.Output.ResultsMDPath = strings.TrimSpace(spec.Output.ResultsMDPath)
	if spec.Output.ResultsMDPath != "" && !filepath.IsAbs(spec.Output.ResultsMDPath) {
		spec.Output.ResultsMDPath = filepath.Clean(filepath.Join(filepath.Dir(absPath), spec.Output.ResultsMDPath))
	}
	spec.Output.ProgressJSONL = strings.TrimSpace(spec.Output.ProgressJSONL)
	if spec.Output.ProgressJSONL != "" && spec.Output.ProgressJSONL != "-" && !filepath.IsAbs(spec.Output.ProgressJSONL) {
		spec.Output.ProgressJSONL = filepath.Clean(filepath.Join(filepath.Dir(absPath), spec.Output.ProgressJSONL))
	}
	if spec.Timeouts.CampaignGlobalTimeoutMs < 0 || spec.Timeouts.DefaultAttemptTimeoutMs < 0 || spec.Timeouts.CleanupHookTimeoutMs < 0 {
		return ParsedSpec{}, fmt.Errorf("timeouts fields must be >= 0")
	}
	if strings.TrimSpace(spec.Timeouts.TimeoutStart) != "" && !schema.IsValidTimeoutStartV1(spec.Timeouts.TimeoutStart) {
		return ParsedSpec{}, fmt.Errorf("invalid timeouts.timeoutStart")
	}
	spec.Execution.FlowMode = strings.ToLower(strings.TrimSpace(spec.Execution.FlowMode))
	if spec.Execution.FlowMode == "" {
		spec.Execution.FlowMode = FlowModeSequence
	}
	switch spec.Execution.FlowMode {
	case FlowModeSequence, FlowModeParallel:
	default:
		return ParsedSpec{}, fmt.Errorf("invalid execution.flowMode (expected %s|%s)", FlowModeSequence, FlowModeParallel)
	}
	spec.PairGate.TraceProfile = strings.ToLower(strings.TrimSpace(spec.PairGate.TraceProfile))
	if spec.PairGate.TraceProfile == "" {
		spec.PairGate.TraceProfile = TraceProfileNone
	}
	if !isValidTraceProfile(spec.PairGate.TraceProfile) {
		return ParsedSpec{}, fmt.Errorf("invalid pairGate.traceProfile (expected %s|%s|%s)", TraceProfileNone, TraceProfileStrictBrowserComp, TraceProfileMCPRequired)
	}
	spec.Cleanup.BeforeMission = normalizeCommand(append(append([]string{}, spec.Cleanup.BeforeMission...), spec.Cleanup.PreMission...))
	spec.Cleanup.AfterMission = normalizeCommand(append(append([]string{}, spec.Cleanup.AfterMission...), spec.Cleanup.PostMission...))
	spec.Cleanup.OnFailure = normalizeCommand(spec.Cleanup.OnFailure)
	spec.Cleanup.PreMission = nil
	spec.Cleanup.PostMission = nil
	if len(spec.Flows) == 0 {
		return ParsedSpec{}, fmt.Errorf("campaign requires at least one flow")
	}

	needsMissionPack := false
	allFlowsOmitSuiteFile := true
	for i := range spec.Flows {
		if strings.TrimSpace(spec.Flows[i].SuiteFile) == "" {
			needsMissionPack = true
			continue
		}
		allFlowsOmitSuiteFile = false
	}
	if spec.PromptMode == PromptModeExam {
		if strings.TrimSpace(spec.MissionSource.Path) != "" {
			return ParsedSpec{}, &OraclePolicyViolationError{
				Code: ReasonExamPromptPolicy,
				Violation: OraclePolicyViolation{
					Field:       "missionSource.path",
					PromptMode:  spec.PromptMode,
					Description: "promptMode=exam requires missionSource.promptSource.path and forbids missionSource.path",
				},
			}
		}
		if strings.TrimSpace(spec.MissionSource.PromptSource.Path) == "" {
			return ParsedSpec{}, &OraclePolicyViolationError{
				Code: ReasonExamPromptPolicy,
				Violation: OraclePolicyViolation{
					Field:       "missionSource.promptSource.path",
					PromptMode:  spec.PromptMode,
					Description: "promptMode=exam requires missionSource.promptSource.path",
				},
			}
		}
		if strings.TrimSpace(spec.MissionSource.OracleSource.Path) == "" {
			return ParsedSpec{}, &OraclePolicyViolationError{
				Code: ReasonOracleEvaluator,
				Violation: OraclePolicyViolation{
					Field:       "missionSource.oracleSource.path",
					PromptMode:  spec.PromptMode,
					Description: "promptMode=exam requires missionSource.oracleSource.path",
				},
			}
		}
		if spec.Evaluation.Mode != EvaluationModeOracle {
			return ParsedSpec{}, &OraclePolicyViolationError{
				Code: ReasonOracleEvaluator,
				Violation: OraclePolicyViolation{
					Field:       "evaluation.mode",
					PromptMode:  spec.PromptMode,
					Description: "promptMode=exam requires evaluation.mode=oracle",
				},
			}
		}
		if spec.Evaluation.Evaluator.Kind != EvaluatorKindScript || len(spec.Evaluation.Evaluator.Command) == 0 {
			return ParsedSpec{}, &OraclePolicyViolationError{
				Code: ReasonOracleEvaluator,
				Violation: OraclePolicyViolation{
					Field:       "evaluation.evaluator.command",
					PromptMode:  spec.PromptMode,
					Description: "promptMode=exam requires evaluation.evaluator.kind=script and non-empty evaluation.evaluator.command",
				},
			}
		}
		if !allFlowsOmitSuiteFile {
			return ParsedSpec{}, &OraclePolicyViolationError{
				Code: ReasonExamPromptPolicy,
				Violation: OraclePolicyViolation{
					Field:       "flows[].suiteFile",
					PromptMode:  spec.PromptMode,
					Description: "promptMode=exam requires missionSource prompt/oracle split; remove flows[].suiteFile",
				},
			}
		}
		if spec.MissionSource.OracleSource.Visibility == OracleVisibilityHostOnly {
			agentRoot := resolveAgentReadableRoot(absPath)
			within, cerr := pathWithinRoot(agentRoot, spec.MissionSource.OracleSource.Path)
			if cerr != nil {
				return ParsedSpec{}, cerr
			}
			if within {
				return ParsedSpec{}, &OraclePolicyViolationError{
					Code: ReasonOracleVisibility,
					Violation: OraclePolicyViolation{
						Field:       "missionSource.oracleSource.path",
						PromptMode:  spec.PromptMode,
						Visibility:  spec.MissionSource.OracleSource.Visibility,
						OraclePath:  spec.MissionSource.OracleSource.Path,
						AgentRoot:   agentRoot,
						Description: "oracleSource.visibility=host_only requires missionSource.oracleSource.path outside the agent-readable workspace root",
					},
				}
			}
		}
		needsMissionPack = true
	}

	var missionPackSuite *suite.ParsedSuite
	oracleByMissionID := map[string]string{}
	if needsMissionPack {
		if spec.PromptMode == PromptModeExam {
			loaded, err := LoadMissionPackSplit(spec.MissionSource.PromptSource.Path, spec.MissionSource.OracleSource.Path, spec.CampaignID)
			if err != nil {
				return ParsedSpec{}, err
			}
			missionPackSuite = &loaded.Parsed
			oracleByMissionID = loaded.OracleByMissionID
		} else {
			if strings.TrimSpace(spec.MissionSource.Path) == "" {
				return ParsedSpec{}, fmt.Errorf("missionSource.path is required when any flow omits suiteFile")
			}
			loaded, err := LoadMissionPack(spec.MissionSource.Path, spec.CampaignID)
			if err != nil {
				return ParsedSpec{}, err
			}
			missionPackSuite = &loaded
		}
	}

	flowSuites := make(map[string]suite.ParsedSuite, len(spec.Flows))
	flowIDs := make([]string, 0, len(spec.Flows))
	for i := range spec.Flows {
		f := &spec.Flows[i]
		f.FlowID = ids.SanitizeComponent(strings.TrimSpace(f.FlowID))
		if f.FlowID == "" {
			return ParsedSpec{}, fmt.Errorf("flow[%d]: missing/invalid flowId", i)
		}
		if _, ok := flowSuites[f.FlowID]; ok {
			return ParsedSpec{}, fmt.Errorf("duplicate flowId %q", f.FlowID)
		}
		f.SuiteFile = strings.TrimSpace(f.SuiteFile)
		hasInlineMissionPack := false
		if f.SuiteFile == "" {
			if missionPackSuite == nil {
				return ParsedSpec{}, fmt.Errorf("flow %q: missing suiteFile (set flows[].suiteFile or missionSource.path for mission-pack mode)", f.FlowID)
			}
			hasInlineMissionPack = true
		} else if !filepath.IsAbs(f.SuiteFile) {
			f.SuiteFile = filepath.Clean(filepath.Join(filepath.Dir(absPath), f.SuiteFile))
		}
		f.Runner.Type = strings.TrimSpace(strings.ToLower(f.Runner.Type))
		if f.Runner.Type == "" {
			f.Runner.Type = RunnerTypeProcessCmd
		}
		if !isValidRunnerType(f.Runner.Type) {
			return ParsedSpec{}, fmt.Errorf("flow %q: invalid runner.type (expected %s|%s|%s|%s|%s)", f.FlowID, RunnerTypeProcessCmd, RunnerTypeCodexExec, RunnerTypeCodexSub, RunnerTypeClaudeSub, RunnerTypeCodexAppSrv)
		}
		f.Runner.Command = normalizeCommand(f.Runner.Command)
		f.Runner.RuntimeStrategies = normalizeLowerTerms(f.Runner.RuntimeStrategies)
		f.Runner.Model = strings.TrimSpace(f.Runner.Model)
		f.Runner.ModelReasoningEffort = strings.ToLower(strings.TrimSpace(f.Runner.ModelReasoningEffort))
		f.Runner.ModelReasoningPolicy = strings.ToLower(strings.TrimSpace(f.Runner.ModelReasoningPolicy))
		if len(f.Runner.Command) == 0 && f.Runner.Type != RunnerTypeCodexAppSrv {
			return ParsedSpec{}, fmt.Errorf("flow %q: runner.command is required", f.FlowID)
		}
		if f.Runner.Type != RunnerTypeCodexAppSrv {
			if f.Runner.Model != "" || f.Runner.ModelReasoningEffort != "" || f.Runner.ModelReasoningPolicy != "" {
				return ParsedSpec{}, fmt.Errorf("flow %q: runner.model and runner.modelReasoning* are supported only for runner.type=%s", f.FlowID, RunnerTypeCodexAppSrv)
			}
		}
		if f.Runner.ModelReasoningEffort == "" && f.Runner.ModelReasoningPolicy != "" {
			return ParsedSpec{}, fmt.Errorf("flow %q: runner.modelReasoningPolicy requires runner.modelReasoningEffort", f.FlowID)
		}
		if f.Runner.ModelReasoningEffort != "" {
			if !isValidModelReasoningEffort(f.Runner.ModelReasoningEffort) {
				return ParsedSpec{}, fmt.Errorf("flow %q: invalid runner.modelReasoningEffort (expected %s|%s|%s|%s|%s|%s)", f.FlowID, ModelReasoningEffortNone, ModelReasoningEffortMinimal, ModelReasoningEffortLow, ModelReasoningEffortMedium, ModelReasoningEffortHigh, ModelReasoningEffortXHigh)
			}
			if f.Runner.ModelReasoningPolicy == "" {
				f.Runner.ModelReasoningPolicy = ModelReasoningPolicyBestEffort
			}
		}
		if f.Runner.ModelReasoningPolicy != "" && !isValidModelReasoningPolicy(f.Runner.ModelReasoningPolicy) {
			return ParsedSpec{}, fmt.Errorf("flow %q: invalid runner.modelReasoningPolicy (expected %s|%s)", f.FlowID, ModelReasoningPolicyBestEffort, ModelReasoningPolicyRequired)
		}
		if strings.TrimSpace(f.Runner.SessionIsolation) == "" {
			if f.Runner.Type == RunnerTypeCodexAppSrv {
				f.Runner.SessionIsolation = "native"
			} else {
				f.Runner.SessionIsolation = "process"
			}
		}
		if strings.TrimSpace(f.Runner.FeedbackPolicy) == "" {
			f.Runner.FeedbackPolicy = schema.FeedbackPolicyAutoFailV1
		}
		if !schema.IsValidFeedbackPolicyV1(f.Runner.FeedbackPolicy) {
			return ParsedSpec{}, fmt.Errorf("flow %q: invalid runner.feedbackPolicy", f.FlowID)
		}
		f.Runner.ToolDriver.Kind = strings.ToLower(strings.TrimSpace(f.Runner.ToolDriver.Kind))
		if f.Runner.ToolDriver.Kind == "" {
			f.Runner.ToolDriver.Kind = ToolDriverShell
		}
		if !isValidToolDriverKind(f.Runner.ToolDriver.Kind) {
			return ParsedSpec{}, fmt.Errorf("flow %q: invalid runner.toolDriver.kind (expected %s|%s|%s|%s)", f.FlowID, ToolDriverShell, ToolDriverCLIFunnel, ToolDriverMCPProxy, ToolDriverHTTPProxy)
		}
		f.Runner.ToolDriver.Shims = normalizeCommand(f.Runner.ToolDriver.Shims)
		if strings.TrimSpace(f.Runner.TimeoutStart) == "" && strings.TrimSpace(spec.Timeouts.TimeoutStart) != "" {
			f.Runner.TimeoutStart = spec.Timeouts.TimeoutStart
		}
		if !schema.IsValidTimeoutStartV1(f.Runner.TimeoutStart) {
			return ParsedSpec{}, fmt.Errorf("flow %q: invalid runner.timeoutStart", f.FlowID)
		}
		if f.Runner.TimeoutMs <= 0 && spec.Timeouts.DefaultAttemptTimeoutMs > 0 {
			f.Runner.TimeoutMs = spec.Timeouts.DefaultAttemptTimeoutMs
		}
		if f.Runner.MCP.MaxToolCalls < 0 || f.Runner.MCP.IdleTimeoutMs < 0 {
			return ParsedSpec{}, fmt.Errorf("flow %q: runner.mcp fields must be >= 0", f.FlowID)
		}
		f.Runner.Shims = dedupeStringsStable(normalizeCommand(append(append([]string{}, f.Runner.Shims...), f.Runner.ToolDriver.Shims...)))
		f.Runner.Finalization.Mode = strings.ToLower(strings.TrimSpace(f.Runner.Finalization.Mode))
		if f.Runner.Finalization.Mode == "" {
			switch schema.NormalizeFeedbackPolicyV1(f.Runner.FeedbackPolicy) {
			case schema.FeedbackPolicyStrictV1:
				f.Runner.Finalization.Mode = FinalizationModeStrict
			default:
				f.Runner.Finalization.Mode = FinalizationModeAutoFail
			}
		}
		if !isValidFinalizationMode(f.Runner.Finalization.Mode) {
			return ParsedSpec{}, fmt.Errorf("flow %q: invalid runner.finalization.mode (expected %s|%s|%s)", f.FlowID, FinalizationModeStrict, FinalizationModeAutoFail, FinalizationModeAutoFromResultJSON)
		}
		if f.Runner.Finalization.MinResultTurn < 0 {
			return ParsedSpec{}, fmt.Errorf("flow %q: runner.finalization.minResultTurn must be >= 1 when set", f.FlowID)
		}
		if f.Runner.Finalization.MinResultTurn == 0 {
			f.Runner.Finalization.MinResultTurn = DefaultMinResultTurn
		}
		f.Runner.Finalization.ResultChannel.Kind = strings.ToLower(strings.TrimSpace(f.Runner.Finalization.ResultChannel.Kind))
		if f.Runner.Finalization.ResultChannel.Kind == "" {
			if f.Runner.Finalization.Mode == FinalizationModeAutoFromResultJSON {
				f.Runner.Finalization.ResultChannel.Kind = ResultChannelFileJSON
			} else {
				f.Runner.Finalization.ResultChannel.Kind = ResultChannelNone
			}
		}
		if !isValidResultChannelKind(f.Runner.Finalization.ResultChannel.Kind) {
			return ParsedSpec{}, fmt.Errorf("flow %q: invalid runner.finalization.resultChannel.kind (expected %s|%s|%s)", f.FlowID, ResultChannelNone, ResultChannelFileJSON, ResultChannelStdoutJSON)
		}
		switch f.Runner.Finalization.ResultChannel.Kind {
		case ResultChannelFileJSON:
			f.Runner.Finalization.ResultChannel.Path = strings.TrimSpace(f.Runner.Finalization.ResultChannel.Path)
			if f.Runner.Finalization.ResultChannel.Path == "" {
				f.Runner.Finalization.ResultChannel.Path = DefaultResultChannelPath
			}
			if filepath.IsAbs(f.Runner.Finalization.ResultChannel.Path) {
				return ParsedSpec{}, fmt.Errorf("flow %q: runner.finalization.resultChannel.path must be attempt-relative", f.FlowID)
			}
			f.Runner.Finalization.ResultChannel.Marker = ""
		case ResultChannelStdoutJSON:
			f.Runner.Finalization.ResultChannel.Marker = strings.TrimSpace(f.Runner.Finalization.ResultChannel.Marker)
			if f.Runner.Finalization.ResultChannel.Marker == "" {
				f.Runner.Finalization.ResultChannel.Marker = DefaultResultChannelMarker
			}
			f.Runner.Finalization.ResultChannel.Path = ""
		default:
			f.Runner.Finalization.ResultChannel.Path = ""
			f.Runner.Finalization.ResultChannel.Marker = ""
		}
		if f.Runner.Finalization.Mode == FinalizationModeAutoFromResultJSON && f.Runner.Finalization.ResultChannel.Kind == ResultChannelNone {
			return ParsedSpec{}, fmt.Errorf("flow %q: runner.finalization.mode=%s requires runner.finalization.resultChannel.kind", f.FlowID, FinalizationModeAutoFromResultJSON)
		}
		if spec.PromptMode == PromptModeMissionOnly || spec.PromptMode == PromptModeExam {
			if f.Runner.Finalization.Mode != FinalizationModeAutoFromResultJSON {
				return ParsedSpec{}, fmt.Errorf("flow %q: promptMode=%s requires runner.finalization.mode=%s", f.FlowID, spec.PromptMode, FinalizationModeAutoFromResultJSON)
			}
			if f.Runner.ToolDriver.Kind == ToolDriverCLIFunnel && len(f.Runner.Shims) == 0 {
				return ParsedSpec{}, &ToolDriverShimRequirementError{
					Code: ReasonToolDriverShim,
					Violation: ToolDriverShimRequirement{
						FlowID:         f.FlowID,
						PromptMode:     spec.PromptMode,
						ToolDriverKind: f.Runner.ToolDriver.Kind,
						RequiredOneOf:  []string{"runner.shims", "runner.toolDriver.shims"},
						Snippet:        "runner.shims: [\"tool-cli\"]\n# or\nrunner.toolDriver.shims: [\"tool-cli\"]",
					},
				}
			}
		}
		if f.Runner.FreshAgentPerAttempt == nil {
			def := true
			f.Runner.FreshAgentPerAttempt = &def
		}
		if !*f.Runner.FreshAgentPerAttempt {
			return ParsedSpec{}, fmt.Errorf("flow %q: runner.freshAgentPerAttempt=false is not supported (fresh sessions are required)", f.FlowID)
		}
		f.AdapterContract.RequiredOutputFields = normalizeCommand(f.AdapterContract.RequiredOutputFields)
		if len(f.AdapterContract.RequiredOutputFields) == 0 {
			f.AdapterContract.RequiredOutputFields = []string{"attemptDir", "status", "errors"}
		}
		if !containsAllRequiredFields(f.AdapterContract.RequiredOutputFields) {
			return ParsedSpec{}, fmt.Errorf("flow %q: adapterContract.requiredOutputFields must include attemptDir,status,errors", f.FlowID)
		}

		var ps suite.ParsedSuite
		if hasInlineMissionPack {
			ps = *missionPackSuite
		} else {
			var err error
			ps, err = suite.ParseFile(f.SuiteFile)
			if err != nil {
				return ParsedSpec{}, fmt.Errorf("flow %q: parse suite: %w", f.FlowID, err)
			}
		}
		flowSuites[f.FlowID] = ps
		flowIDs = append(flowIDs, f.FlowID)
	}

	sort.Strings(flowIDs)
	baseFlow := spec.Flows[0].FlowID
	base := flowSuites[baseFlow]
	baseMissionCount := len(base.Suite.Missions)
	if baseMissionCount == 0 {
		return ParsedSpec{}, fmt.Errorf("base flow suite has no missions")
	}

	for _, id := range flowIDs {
		cur := flowSuites[id]
		if len(cur.Suite.Missions) != baseMissionCount {
			return ParsedSpec{}, fmt.Errorf("flow %q mission count %d does not match base flow %d", id, len(cur.Suite.Missions), baseMissionCount)
		}
		for i := 0; i < baseMissionCount; i++ {
			baseID := strings.TrimSpace(base.Suite.Missions[i].MissionID)
			curID := strings.TrimSpace(cur.Suite.Missions[i].MissionID)
			if baseID != curID {
				return ParsedSpec{}, fmt.Errorf("flow %q mission order mismatch at index %d (base=%q flow=%q)", id, i, baseID, curID)
			}
		}
	}

	indexes, err := ResolveMissionIndexes(base.Suite, spec.MissionSource.Selection)
	if err != nil {
		return ParsedSpec{}, err
	}
	if len(indexes) == 0 {
		return ParsedSpec{}, fmt.Errorf("campaign selection resolved to zero missions")
	}

	if spec.TotalMissions == 0 {
		spec.TotalMissions = len(indexes)
	}
	if spec.TotalMissions > len(indexes) {
		spec.TotalMissions = len(indexes)
	}
	if spec.TotalMissions > 0 && spec.TotalMissions < 1 {
		return ParsedSpec{}, fmt.Errorf("totalMissions must be >= 1 when set")
	}
	parsed := ParsedSpec{
		SpecPath:          absPath,
		Spec:              spec,
		BaseSuite:         base,
		FlowSuites:        flowSuites,
		MissionIndexes:    indexes,
		OracleByMissionID: oracleByMissionID,
	}
	if spec.PromptMode == PromptModeMissionOnly || spec.PromptMode == PromptModeExam {
		violations := EvaluatePromptModeViolations(parsed)
		if len(violations) > 0 {
			code := ReasonPromptModePolicy
			if spec.PromptMode == PromptModeExam {
				code = ReasonExamPromptPolicy
			}
			return ParsedSpec{}, &PromptModeViolationError{
				Code:       code,
				PromptMode: spec.PromptMode,
				Violations: violations,
			}
		}
	}
	return parsed, nil
}

func (s SpecV1) PairGateEnabled() bool {
	if s.PairGate.Enabled == nil {
		return true
	}
	return *s.PairGate.Enabled
}

func ResolveMissionIndexes(sf suite.SuiteFileV1, sel MissionSelectionSpec) ([]int, error) {
	missionCount := len(sf.Missions)
	if missionCount == 0 {
		return nil, fmt.Errorf("suite has no missions")
	}
	mode := strings.TrimSpace(strings.ToLower(sel.Mode))
	if mode == "" {
		mode = SelectionModeAll
	}

	out := make([]int, 0, missionCount)
	appendIndex := func(idx int) error {
		if idx < 0 || idx >= missionCount {
			return fmt.Errorf("mission selection index out of range: %d", idx)
		}
		out = append(out, idx)
		return nil
	}

	switch mode {
	case SelectionModeAll:
		for i := 0; i < missionCount; i++ {
			out = append(out, i)
		}
	case SelectionModeMissionID:
		if len(sel.MissionIDs) == 0 {
			return nil, fmt.Errorf("mission selection mode %q requires missionIds", mode)
		}
		indexByID := map[string]int{}
		for i := range sf.Missions {
			indexByID[sf.Missions[i].MissionID] = i
		}
		for _, raw := range sel.MissionIDs {
			id := strings.TrimSpace(raw)
			idx, ok := indexByID[id]
			if !ok {
				return nil, fmt.Errorf("mission selection id not found: %q", id)
			}
			out = append(out, idx)
		}
	case SelectionModeIndex:
		if len(sel.Indexes) == 0 {
			return nil, fmt.Errorf("mission selection mode %q requires indexes", mode)
		}
		for _, idx := range sel.Indexes {
			if err := appendIndex(idx); err != nil {
				return nil, err
			}
		}
	case SelectionModeRange:
		if sel.Range.End < sel.Range.Start {
			return nil, fmt.Errorf("mission selection range end must be >= start")
		}
		for idx := sel.Range.Start; idx <= sel.Range.End; idx++ {
			if err := appendIndex(idx); err != nil {
				return nil, err
			}
		}
	default:
		return nil, fmt.Errorf("invalid mission selection mode %q (expected all|mission_id|index|range)", mode)
	}

	out = dedupeIntsStable(out)
	if len(out) == 0 {
		return nil, fmt.Errorf("mission selection resolved to zero missions")
	}
	return out, nil
}

func WindowMissionIndexes(indexes []int, missionOffset int, totalMissions int) ([]int, error) {
	if missionOffset < 0 {
		return nil, fmt.Errorf("mission offset must be >= 0")
	}
	if totalMissions < 0 {
		return nil, fmt.Errorf("total missions must be >= 0")
	}
	if missionOffset > len(indexes) {
		return nil, fmt.Errorf("mission offset %d exceeds selected missions %d", missionOffset, len(indexes))
	}
	window := indexes[missionOffset:]
	if totalMissions > 0 && totalMissions < len(window) {
		window = window[:totalMissions]
	}
	out := make([]int, len(window))
	copy(out, window)
	return out, nil
}

func EvaluatePromptModeViolations(parsed ParsedSpec) []PromptModeViolation {
	if parsed.Spec.PromptMode != PromptModeMissionOnly && parsed.Spec.PromptMode != PromptModeExam {
		return nil
	}
	terms := parsed.Spec.NoContext.ForbiddenPromptTerms
	if len(terms) == 0 {
		if parsed.Spec.PromptMode == PromptModeExam {
			terms = defaultExamForbiddenTerms()
		} else {
			terms = defaultMissionOnlyForbiddenTerms()
		}
	}
	if len(terms) == 0 || len(parsed.MissionIndexes) == 0 || len(parsed.FlowSuites) == 0 {
		return nil
	}
	seen := map[string]bool{}
	out := make([]PromptModeViolation, 0, 8)
	for _, flow := range parsed.Spec.Flows {
		ps, ok := parsed.FlowSuites[flow.FlowID]
		if !ok {
			continue
		}
		for _, idx := range parsed.MissionIndexes {
			if idx < 0 || idx >= len(ps.Suite.Missions) {
				continue
			}
			m := ps.Suite.Missions[idx]
			promptLower := strings.ToLower(m.Prompt)
			for _, term := range terms {
				term = strings.TrimSpace(term)
				if term == "" {
					continue
				}
				needle := strings.ToLower(term)
				if !strings.Contains(promptLower, needle) {
					continue
				}
				key := flow.FlowID + "|" + strconv.Itoa(idx) + "|" + m.MissionID + "|" + needle
				if seen[key] {
					continue
				}
				seen[key] = true
				out = append(out, PromptModeViolation{
					FlowID:       flow.FlowID,
					MissionID:    m.MissionID,
					MissionIndex: idx,
					Term:         term,
				})
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].FlowID != out[j].FlowID {
			return out[i].FlowID < out[j].FlowID
		}
		if out[i].MissionIndex != out[j].MissionIndex {
			return out[i].MissionIndex < out[j].MissionIndex
		}
		if out[i].MissionID != out[j].MissionID {
			return out[i].MissionID < out[j].MissionID
		}
		return out[i].Term < out[j].Term
	})
	return out
}

func defaultMissionOnlyForbiddenTerms() []string {
	return []string{
		"zcl run",
		"zcl mcp proxy",
		"zcl http proxy",
		"zcl feedback",
		"zcl attempt finish",
		"tool.calls.jsonl",
		"feedback.json",
	}
}

func defaultExamForbiddenTerms() []string {
	return []string{
		"success check",
		"expected",
		"oracle",
		"answer key",
		"validation logic",
		"golden answer",
	}
}

func ResolveExecutionMode(parsed ParsedSpec) ExecutionModeSummary {
	out := ExecutionModeSummary{Mode: "config_driven"}
	if parsed.Spec.PromptMode != PromptModeMissionOnly && parsed.Spec.PromptMode != PromptModeExam {
		return out
	}
	for _, flow := range parsed.Spec.Flows {
		if len(flow.Runner.Command) == 0 {
			continue
		}
		if flow.Runner.ToolDriver.Kind == ToolDriverShell {
			out.AdapterScriptFlows = append(out.AdapterScriptFlows, flow.FlowID)
		}
	}
	if len(out.AdapterScriptFlows) > 0 {
		out.Mode = "adapter_script"
	}
	return out
}

func DefaultMissionOnlyForbiddenTerms() []string {
	terms := defaultMissionOnlyForbiddenTerms()
	out := make([]string, len(terms))
	copy(out, terms)
	return out
}

func DefaultExamForbiddenTerms() []string {
	terms := defaultExamForbiddenTerms()
	out := make([]string, len(terms))
	copy(out, terms)
	return out
}

func decodeSpecStrict(absPath string, raw []byte) (SpecV1, error) {
	ext := strings.ToLower(filepath.Ext(absPath))
	switch ext {
	case ".yaml", ".yml":
		return decodeYAMLStrict(raw)
	default:
		return decodeJSONStrict(raw)
	}
}

func decodeJSONStrict(raw []byte) (SpecV1, error) {
	var in map[string]json.RawMessage
	if err := json.Unmarshal(raw, &in); err != nil {
		return SpecV1{}, fmt.Errorf("invalid campaign json: %w", err)
	}
	if len(in) == 0 {
		return SpecV1{}, fmt.Errorf("invalid campaign json: empty object")
	}
	clean := map[string]json.RawMessage{}
	ext := map[string]any{}
	for k, v := range in {
		if strings.HasPrefix(strings.ToLower(strings.TrimSpace(k)), "x-") {
			var decoded any
			if err := json.Unmarshal(v, &decoded); err == nil {
				ext[k] = decoded
			}
			continue
		}
		clean[k] = v
	}
	b, err := json.Marshal(clean)
	if err != nil {
		return SpecV1{}, fmt.Errorf("invalid campaign json: %w", err)
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	var spec SpecV1
	if err := dec.Decode(&spec); err != nil {
		return SpecV1{}, fmt.Errorf("invalid campaign json: %w", err)
	}
	if err := consumeJSONEOF(dec); err != nil {
		return SpecV1{}, fmt.Errorf("invalid campaign json: %w", err)
	}
	if len(ext) > 0 {
		spec.Extensions = ext
	}
	return spec, nil
}

func decodeYAMLStrict(raw []byte) (SpecV1, error) {
	var top map[string]any
	if err := yaml.Unmarshal(raw, &top); err != nil {
		return SpecV1{}, fmt.Errorf("invalid campaign yaml: %w", err)
	}
	if len(top) == 0 {
		return SpecV1{}, fmt.Errorf("invalid campaign yaml: empty object")
	}
	clean := map[string]any{}
	ext := map[string]any{}
	for k, v := range top {
		if strings.HasPrefix(strings.ToLower(strings.TrimSpace(k)), "x-") {
			ext[k] = v
			continue
		}
		clean[k] = v
	}
	b, err := yaml.Marshal(clean)
	if err != nil {
		return SpecV1{}, fmt.Errorf("invalid campaign yaml: %w", err)
	}
	dec := yaml.NewDecoder(bytes.NewReader(b))
	dec.KnownFields(true)
	var spec SpecV1
	if err := dec.Decode(&spec); err != nil {
		return SpecV1{}, fmt.Errorf("invalid campaign yaml: %w", err)
	}
	if len(ext) > 0 {
		spec.Extensions = ext
	}
	return spec, nil
}

func consumeJSONEOF(dec *json.Decoder) error {
	if dec == nil {
		return nil
	}
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		if err == nil {
			return fmt.Errorf("unexpected extra content")
		}
		return err
	}
	return nil
}

func dedupeIntsStable(in []int) []int {
	if len(in) == 0 {
		return nil
	}
	seen := map[int]bool{}
	out := make([]int, 0, len(in))
	for _, v := range in {
		if seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	return out
}

func containsAllRequiredFields(in []string) bool {
	want := map[string]bool{"attemptDir": false, "status": false, "errors": false}
	for _, raw := range in {
		v := strings.TrimSpace(raw)
		if _, ok := want[v]; ok {
			want[v] = true
		}
	}
	for _, ok := range want {
		if !ok {
			return false
		}
	}
	return true
}

func normalizeCommand(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	out := make([]string, 0, len(in))
	for _, part := range in {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		out = append(out, part)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func normalizeTerms(in []string) []string {
	in = normalizeCommand(in)
	if len(in) == 0 {
		return nil
	}
	return dedupeStringsStable(in)
}

func normalizeLowerTerms(in []string) []string {
	in = normalizeCommand(in)
	if len(in) == 0 {
		return nil
	}
	out := make([]string, 0, len(in))
	for _, term := range in {
		term = strings.ToLower(strings.TrimSpace(term))
		if term == "" {
			continue
		}
		out = append(out, term)
	}
	return dedupeStringsStable(out)
}

func dedupeStringsStable(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	seen := map[string]bool{}
	out := make([]string, 0, len(in))
	for _, v := range in {
		key := strings.TrimSpace(v)
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, key)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func isValidRunnerType(v string) bool {
	switch strings.TrimSpace(strings.ToLower(v)) {
	case RunnerTypeProcessCmd, RunnerTypeCodexExec, RunnerTypeCodexSub, RunnerTypeClaudeSub, RunnerTypeCodexAppSrv:
		return true
	default:
		return false
	}
}

func isValidPromptMode(v string) bool {
	switch strings.TrimSpace(strings.ToLower(v)) {
	case PromptModeDefault, PromptModeMissionOnly, PromptModeExam:
		return true
	default:
		return false
	}
}

func isValidOracleVisibility(v string) bool {
	switch strings.TrimSpace(strings.ToLower(v)) {
	case OracleVisibilityWorkspace, OracleVisibilityHostOnly:
		return true
	default:
		return false
	}
}

func isValidEvaluationMode(v string) bool {
	switch strings.TrimSpace(strings.ToLower(v)) {
	case EvaluationModeNone, EvaluationModeOracle:
		return true
	default:
		return false
	}
}

func isValidEvaluatorKind(v string) bool {
	switch strings.TrimSpace(strings.ToLower(v)) {
	case EvaluatorKindScript:
		return true
	default:
		return false
	}
}

func isValidToolDriverKind(v string) bool {
	switch strings.TrimSpace(strings.ToLower(v)) {
	case ToolDriverShell, ToolDriverCLIFunnel, ToolDriverMCPProxy, ToolDriverHTTPProxy:
		return true
	default:
		return false
	}
}

func isValidFinalizationMode(v string) bool {
	switch strings.TrimSpace(strings.ToLower(v)) {
	case FinalizationModeStrict, FinalizationModeAutoFail, FinalizationModeAutoFromResultJSON:
		return true
	default:
		return false
	}
}

func isValidResultChannelKind(v string) bool {
	switch strings.TrimSpace(strings.ToLower(v)) {
	case ResultChannelNone, ResultChannelFileJSON, ResultChannelStdoutJSON:
		return true
	default:
		return false
	}
}

func isValidModelReasoningEffort(v string) bool {
	switch strings.TrimSpace(strings.ToLower(v)) {
	case ModelReasoningEffortNone, ModelReasoningEffortMinimal, ModelReasoningEffortLow, ModelReasoningEffortMedium, ModelReasoningEffortHigh, ModelReasoningEffortXHigh:
		return true
	default:
		return false
	}
}

func isValidModelReasoningPolicy(v string) bool {
	switch strings.TrimSpace(strings.ToLower(v)) {
	case ModelReasoningPolicyBestEffort, ModelReasoningPolicyRequired:
		return true
	default:
		return false
	}
}

func isValidTraceProfile(v string) bool {
	switch strings.TrimSpace(strings.ToLower(v)) {
	case TraceProfileNone, TraceProfileStrictBrowserComp, TraceProfileMCPRequired:
		return true
	default:
		return false
	}
}

func resolveAgentReadableRoot(specPath string) string {
	root := filepath.Dir(strings.TrimSpace(specPath))
	cur := root
	for {
		gitDir := filepath.Join(cur, ".git")
		if _, err := os.Stat(gitDir); err == nil {
			return cur
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			break
		}
		cur = parent
	}
	return root
}

func pathWithinRoot(root string, target string) (bool, error) {
	root = strings.TrimSpace(root)
	target = strings.TrimSpace(target)
	if root == "" || target == "" {
		return false, nil
	}
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return false, err
	}
	targetAbs, err := filepath.Abs(target)
	if err != nil {
		return false, err
	}
	if evalRoot, err := filepath.EvalSymlinks(rootAbs); err == nil && strings.TrimSpace(evalRoot) != "" {
		rootAbs = evalRoot
	}
	if evalTarget, err := filepath.EvalSymlinks(targetAbs); err == nil && strings.TrimSpace(evalTarget) != "" {
		targetAbs = evalTarget
	}
	rel, err := filepath.Rel(rootAbs, targetAbs)
	if err != nil {
		return false, err
	}
	if rel == "." {
		return true, nil
	}
	prefix := ".." + string(os.PathSeparator)
	if rel == ".." || strings.HasPrefix(rel, prefix) {
		return false, nil
	}
	return true, nil
}

func FormatSelectionKey(index int, missionID string) string {
	return strconv.Itoa(index) + ":" + strings.TrimSpace(missionID)
}
