package campaign

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/marcohefti/zero-context-lab/internal/contexts/spec/ports/suite"
	"github.com/marcohefti/zero-context-lab/internal/kernel/codes"
	"github.com/marcohefti/zero-context-lab/internal/kernel/ids"
	"github.com/marcohefti/zero-context-lab/internal/kernel/schema"
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
	ReasonToolPolicy       = codes.CampaignToolPolicyViolation
	ReasonToolPolicyConfig = codes.CampaignToolPolicyInvalid
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

	RunnerCwdModeInherit             = "inherit"
	RunnerCwdModeTempEmptyPerAttempt = "temp_empty_per_attempt"

	RunnerCwdRetainNever     = "never"
	RunnerCwdRetainOnFailure = "on_failure"
	RunnerCwdRetainAlways    = "always"

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
	EvaluatorKindBuiltin = "builtin_rules"

	OraclePolicyModeStrict     = "strict"
	OraclePolicyModeNormalized = "normalized"
	OraclePolicyModeSemantic   = "semantic"

	OracleFormatMismatchFail   = "fail"
	OracleFormatMismatchWarn   = "warn"
	OracleFormatMismatchIgnore = "ignore"

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
	FlowGate      PairGateSpec      `json:"flowGate,omitempty" yaml:"flowGate,omitempty"`
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
	Mode         string           `json:"mode,omitempty" yaml:"mode,omitempty"` // none|oracle
	Evaluator    EvaluatorSpec    `json:"evaluator,omitempty" yaml:"evaluator,omitempty"`
	OraclePolicy OraclePolicySpec `json:"oraclePolicy,omitempty" yaml:"oraclePolicy,omitempty"`
}

type EvaluatorSpec struct {
	Kind    string   `json:"kind,omitempty" yaml:"kind,omitempty"` // script|builtin_rules
	Command []string `json:"command,omitempty" yaml:"command,omitempty"`
}

type OraclePolicySpec struct {
	// Mode controls built-in evaluator behavior.
	// strict: exact unless per-rule tolerant ops/normalizers are configured.
	// normalized: eq rules accept normalization-equivalent values.
	// semantic: normalized + phrase-equivalent eq rules.
	Mode string `json:"mode,omitempty" yaml:"mode,omitempty"` // strict|normalized|semantic
	// FormatMismatch controls gate disposition when mismatches are format-only.
	FormatMismatch string `json:"formatMismatch,omitempty" yaml:"formatMismatch,omitempty"` // fail|warn|ignore
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
	CampaignGlobalTimeoutMs  int64  `json:"campaignGlobalTimeoutMs,omitempty" yaml:"campaignGlobalTimeoutMs,omitempty"`
	DefaultAttemptTimeoutMs  int64  `json:"defaultAttemptTimeoutMs,omitempty" yaml:"defaultAttemptTimeoutMs,omitempty"`
	CleanupHookTimeoutMs     int64  `json:"cleanupHookTimeoutMs,omitempty" yaml:"cleanupHookTimeoutMs,omitempty"`
	MissionEnvelopeMs        int64  `json:"missionEnvelopeMs,omitempty" yaml:"missionEnvelopeMs,omitempty"`
	WatchdogHeartbeatMs      int64  `json:"watchdogHeartbeatMs,omitempty" yaml:"watchdogHeartbeatMs,omitempty"`
	WatchdogHardKillContinue bool   `json:"watchdogHardKillContinue,omitempty" yaml:"watchdogHardKillContinue,omitempty"`
	TimeoutStart             string `json:"timeoutStart,omitempty" yaml:"timeoutStart,omitempty"`
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
	PromptSource     PromptSourceSpec    `json:"promptSource,omitempty" yaml:"promptSource,omitempty"`
	PromptTemplate   PromptTemplateSpec  `json:"promptTemplate,omitempty" yaml:"promptTemplate,omitempty"`
	ToolPolicy       ToolPolicySpec      `json:"toolPolicy,omitempty" yaml:"toolPolicy,omitempty"`
	Runner           RunnerAdapterSpec   `json:"runner" yaml:"runner"`
	AdapterContract  AdapterContractSpec `json:"adapterContract,omitempty" yaml:"adapterContract,omitempty"`
	RunnerExtensions map[string]any      `json:"-" yaml:"-"`
}

type PromptTemplateSpec struct {
	Path               string   `json:"path,omitempty" yaml:"path,omitempty"`
	AllowRunnerEnvKeys []string `json:"allowRunnerEnvKeys,omitempty" yaml:"allowRunnerEnvKeys,omitempty"`
}

type ToolPolicySpec struct {
	Allow   []ToolPolicyRuleSpec `json:"allow,omitempty" yaml:"allow,omitempty"`
	Deny    []ToolPolicyRuleSpec `json:"deny,omitempty" yaml:"deny,omitempty"`
	Aliases map[string][]string  `json:"aliases,omitempty" yaml:"aliases,omitempty"`
}

type ToolPolicyRuleSpec struct {
	Namespace string `json:"namespace,omitempty" yaml:"namespace,omitempty"`
	Prefix    string `json:"prefix,omitempty" yaml:"prefix,omitempty"`
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
	Cwd              RunnerCwdSpec    `json:"cwd,omitempty" yaml:"cwd,omitempty"`

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

type RunnerCwdSpec struct {
	Mode     string `json:"mode,omitempty" yaml:"mode,omitempty"`         // inherit|temp_empty_per_attempt
	BasePath string `json:"basePath,omitempty" yaml:"basePath,omitempty"` // optional root for temp_empty_per_attempt
	Retain   string `json:"retain,omitempty" yaml:"retain,omitempty"`     // never|on_failure|always
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

type ToolPolicyConfigViolation struct {
	FlowID      string `json:"flowId"`
	Description string `json:"description"`
}

type ToolPolicyConfigError struct {
	Code      string                    `json:"code"`
	Violation ToolPolicyConfigViolation `json:"violation"`
}

func (e *ToolPolicyConfigError) Error() string {
	if e == nil {
		return "tool policy config violation"
	}
	v := e.Violation
	msg := strings.TrimSpace(v.Description)
	if msg == "" {
		msg = "tool policy config violation"
	}
	if strings.TrimSpace(e.Code) == "" {
		return fmt.Sprintf("flow %q: %s", v.FlowID, msg)
	}
	return fmt.Sprintf("%s: flow %q: %s", e.Code, v.FlowID, msg)
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
	absPath, spec, err := loadSpecFromPath(path)
	if err != nil {
		return ParsedSpec{}, err
	}
	p := newSpecParser(absPath, spec)
	if err := p.prepare(); err != nil {
		return ParsedSpec{}, err
	}
	if err := p.parseFlows(); err != nil {
		return ParsedSpec{}, err
	}
	return p.buildParsedSpec()
}

type specParser struct {
	absPath                   string
	spec                      SpecV1
	needsInlineMissionPack    bool
	allFlowsOmitSuiteFile     bool
	oracleByMissionID         map[string]string
	inlineMissionPackCache    map[string]suite.ParsedSuite
	inlineSplitMissionPackMap map[string]SplitMissionPackResult
	flowSuites                map[string]suite.ParsedSuite
	flowIDs                   []string
}

func loadSpecFromPath(path string) (string, SpecV1, error) {
	absPath, err := filepath.Abs(strings.TrimSpace(path))
	if err != nil {
		return "", SpecV1{}, err
	}
	raw, err := os.ReadFile(absPath)
	if err != nil {
		return "", SpecV1{}, err
	}
	spec, err := decodeSpecStrict(absPath, raw)
	if err != nil {
		return "", SpecV1{}, err
	}
	return absPath, spec, nil
}

func newSpecParser(absPath string, spec SpecV1) *specParser {
	return &specParser{
		absPath:                   absPath,
		spec:                      spec,
		oracleByMissionID:         map[string]string{},
		inlineMissionPackCache:    map[string]suite.ParsedSuite{},
		inlineSplitMissionPackMap: map[string]SplitMissionPackResult{},
		flowSuites:                make(map[string]suite.ParsedSuite, len(spec.Flows)),
		flowIDs:                   make([]string, 0, len(spec.Flows)),
	}
}

func (p *specParser) prepare() error {
	if err := normalizeSpecCore(&p.spec, p.absPath); err != nil {
		return err
	}
	p.detectInlineMissionPackNeeds()
	return p.validateExamPromptMode()
}

func normalizeSpecCore(spec *SpecV1, absPath string) error {
	if err := normalizeSpecIdentity(spec); err != nil {
		return err
	}
	if err := normalizeSpecPromptMode(spec); err != nil {
		return err
	}
	normalizeSpecMissionSource(spec, absPath)
	if err := normalizeSpecOracleVisibility(spec); err != nil {
		return err
	}
	if err := normalizeSpecEvaluation(spec); err != nil {
		return err
	}
	normalizeSpecOutputAndSemantic(spec, absPath)
	if err := normalizeSpecTimeouts(spec); err != nil {
		return err
	}
	if err := normalizeSpecExecutionAndGates(spec); err != nil {
		return err
	}
	normalizeSpecCleanup(spec)
	if len(spec.Flows) == 0 {
		return fmt.Errorf("campaign requires at least one flow")
	}
	return nil
}

func normalizeSpecIdentity(spec *SpecV1) error {
	if spec.SchemaVersion == 0 {
		spec.SchemaVersion = 1
	}
	if spec.SchemaVersion != 1 {
		return fmt.Errorf("unsupported campaign schemaVersion (expected 1)")
	}
	spec.CampaignID = ids.SanitizeComponent(strings.TrimSpace(spec.CampaignID))
	if spec.CampaignID == "" {
		return fmt.Errorf("missing/invalid campaignId")
	}
	if spec.TotalMissions < 0 {
		return fmt.Errorf("totalMissions must be >= 0")
	}
	if spec.CanaryMissions < 0 {
		return fmt.Errorf("canaryMissions must be >= 0")
	}
	return nil
}

func normalizeSpecPromptMode(spec *SpecV1) error {
	spec.PromptMode = strings.ToLower(strings.TrimSpace(spec.PromptMode))
	if spec.PromptMode == "" {
		spec.PromptMode = PromptModeDefault
	}
	if !isValidPromptMode(spec.PromptMode) {
		return fmt.Errorf("invalid promptMode (expected %s|%s|%s)", PromptModeDefault, PromptModeMissionOnly, PromptModeExam)
	}
	spec.NoContext.ForbiddenPromptTerms = normalizeTerms(spec.NoContext.ForbiddenPromptTerms)
	switch spec.PromptMode {
	case PromptModeMissionOnly:
		if len(spec.NoContext.ForbiddenPromptTerms) == 0 {
			spec.NoContext.ForbiddenPromptTerms = defaultMissionOnlyForbiddenTerms()
		}
	case PromptModeExam:
		if len(spec.NoContext.ForbiddenPromptTerms) == 0 {
			spec.NoContext.ForbiddenPromptTerms = defaultExamForbiddenTerms()
		}
	}
	return nil
}

func normalizeSpecMissionSource(spec *SpecV1, absPath string) {
	spec.MissionSource.Path = resolveSpecRelativePath(absPath, spec.MissionSource.Path, false)
	spec.MissionSource.PromptSource.Path = resolveSpecRelativePath(absPath, spec.MissionSource.PromptSource.Path, false)
	spec.MissionSource.OracleSource.Path = resolveSpecRelativePath(absPath, spec.MissionSource.OracleSource.Path, false)
}

func resolveSpecRelativePath(absSpecPath string, raw string, allowDash bool) string {
	val := strings.TrimSpace(raw)
	if val == "" || filepath.IsAbs(val) {
		return val
	}
	if allowDash && val == "-" {
		return val
	}
	return filepath.Clean(filepath.Join(filepath.Dir(absSpecPath), val))
}

func normalizeSpecOracleVisibility(spec *SpecV1) error {
	spec.MissionSource.OracleSource.Visibility = strings.ToLower(strings.TrimSpace(spec.MissionSource.OracleSource.Visibility))
	if spec.MissionSource.OracleSource.Visibility == "" {
		spec.MissionSource.OracleSource.Visibility = OracleVisibilityWorkspace
	}
	if !isValidOracleVisibility(spec.MissionSource.OracleSource.Visibility) {
		return fmt.Errorf("invalid missionSource.oracleSource.visibility (expected %s|%s)", OracleVisibilityWorkspace, OracleVisibilityHostOnly)
	}
	return nil
}

func normalizeSpecEvaluation(spec *SpecV1) error {
	spec.Evaluation.Mode = strings.ToLower(strings.TrimSpace(spec.Evaluation.Mode))
	if spec.Evaluation.Mode == "" {
		spec.Evaluation.Mode = EvaluationModeNone
	}
	if !isValidEvaluationMode(spec.Evaluation.Mode) {
		return fmt.Errorf("invalid evaluation.mode (expected %s|%s)", EvaluationModeNone, EvaluationModeOracle)
	}
	spec.Evaluation.Evaluator.Kind = strings.ToLower(strings.TrimSpace(spec.Evaluation.Evaluator.Kind))
	if spec.Evaluation.Mode == EvaluationModeOracle && spec.Evaluation.Evaluator.Kind == "" {
		spec.Evaluation.Evaluator.Kind = EvaluatorKindScript
	}
	if spec.Evaluation.Evaluator.Kind != "" && !isValidEvaluatorKind(spec.Evaluation.Evaluator.Kind) {
		return fmt.Errorf("invalid evaluation.evaluator.kind (expected %s|%s)", EvaluatorKindScript, EvaluatorKindBuiltin)
	}
	spec.Evaluation.Evaluator.Command = normalizeCommand(spec.Evaluation.Evaluator.Command)
	spec.Evaluation.OraclePolicy.Mode = strings.ToLower(strings.TrimSpace(spec.Evaluation.OraclePolicy.Mode))
	if spec.Evaluation.OraclePolicy.Mode == "" {
		spec.Evaluation.OraclePolicy.Mode = OraclePolicyModeStrict
	}
	if !isValidOraclePolicyMode(spec.Evaluation.OraclePolicy.Mode) {
		return fmt.Errorf("invalid evaluation.oraclePolicy.mode (expected %s|%s|%s)", OraclePolicyModeStrict, OraclePolicyModeNormalized, OraclePolicyModeSemantic)
	}
	spec.Evaluation.OraclePolicy.FormatMismatch = strings.ToLower(strings.TrimSpace(spec.Evaluation.OraclePolicy.FormatMismatch))
	if spec.Evaluation.OraclePolicy.FormatMismatch == "" {
		spec.Evaluation.OraclePolicy.FormatMismatch = OracleFormatMismatchFail
	}
	if !isValidOracleFormatMismatchPolicy(spec.Evaluation.OraclePolicy.FormatMismatch) {
		return fmt.Errorf("invalid evaluation.oraclePolicy.formatMismatch (expected %s|%s|%s)", OracleFormatMismatchFail, OracleFormatMismatchWarn, OracleFormatMismatchIgnore)
	}
	return nil
}

func normalizeSpecOutputAndSemantic(spec *SpecV1, absPath string) {
	spec.Semantic.RulesPath = resolveSpecRelativePath(absPath, spec.Semantic.RulesPath, false)
	spec.Output.ReportPath = resolveSpecRelativePath(absPath, spec.Output.ReportPath, false)
	spec.Output.SummaryPath = resolveSpecRelativePath(absPath, spec.Output.SummaryPath, false)
	spec.Output.ResultsMDPath = resolveSpecRelativePath(absPath, spec.Output.ResultsMDPath, false)
	spec.Output.ProgressJSONL = resolveSpecRelativePath(absPath, spec.Output.ProgressJSONL, true)
}

func normalizeSpecTimeouts(spec *SpecV1) error {
	if spec.Timeouts.CampaignGlobalTimeoutMs < 0 ||
		spec.Timeouts.DefaultAttemptTimeoutMs < 0 ||
		spec.Timeouts.CleanupHookTimeoutMs < 0 ||
		spec.Timeouts.MissionEnvelopeMs < 0 ||
		spec.Timeouts.WatchdogHeartbeatMs < 0 {
		return fmt.Errorf("timeouts fields must be >= 0")
	}
	if strings.TrimSpace(spec.Timeouts.TimeoutStart) != "" && !schema.IsValidTimeoutStartV1(spec.Timeouts.TimeoutStart) {
		return fmt.Errorf("invalid timeouts.timeoutStart")
	}
	return nil
}

func normalizeSpecExecutionAndGates(spec *SpecV1) error {
	spec.Execution.FlowMode = strings.ToLower(strings.TrimSpace(spec.Execution.FlowMode))
	if spec.Execution.FlowMode == "" {
		spec.Execution.FlowMode = FlowModeSequence
	}
	if spec.Execution.FlowMode != FlowModeSequence && spec.Execution.FlowMode != FlowModeParallel {
		return fmt.Errorf("invalid execution.flowMode (expected %s|%s)", FlowModeSequence, FlowModeParallel)
	}
	spec.PairGate.TraceProfile = strings.ToLower(strings.TrimSpace(spec.PairGate.TraceProfile))
	spec.FlowGate.TraceProfile = strings.ToLower(strings.TrimSpace(spec.FlowGate.TraceProfile))
	pairSpecified := pairGateSpecConfigured(spec.PairGate)
	flowSpecified := pairGateSpecConfigured(spec.FlowGate)
	if pairSpecified && flowSpecified && !pairGateSpecsEqual(spec.PairGate, spec.FlowGate) {
		return fmt.Errorf("pairGate and flowGate both set but differ; define one or keep both identical")
	}
	if !pairSpecified && flowSpecified {
		spec.PairGate = spec.FlowGate
	}
	if spec.PairGate.TraceProfile == "" {
		spec.PairGate.TraceProfile = TraceProfileNone
	}
	if !isValidTraceProfile(spec.PairGate.TraceProfile) {
		return fmt.Errorf("invalid pairGate.traceProfile (expected %s|%s|%s)", TraceProfileNone, TraceProfileStrictBrowserComp, TraceProfileMCPRequired)
	}
	return nil
}

func normalizeSpecCleanup(spec *SpecV1) {
	spec.Cleanup.BeforeMission = normalizeCommand(append(append([]string{}, spec.Cleanup.BeforeMission...), spec.Cleanup.PreMission...))
	spec.Cleanup.AfterMission = normalizeCommand(append(append([]string{}, spec.Cleanup.AfterMission...), spec.Cleanup.PostMission...))
	spec.Cleanup.OnFailure = normalizeCommand(spec.Cleanup.OnFailure)
	spec.Cleanup.PreMission = nil
	spec.Cleanup.PostMission = nil
}

func (p *specParser) detectInlineMissionPackNeeds() {
	p.allFlowsOmitSuiteFile = true
	for i := range p.spec.Flows {
		if strings.TrimSpace(p.spec.Flows[i].SuiteFile) == "" {
			p.needsInlineMissionPack = true
			continue
		}
		p.allFlowsOmitSuiteFile = false
	}
}

func (p *specParser) validateExamPromptMode() error {
	if p.spec.PromptMode != PromptModeExam {
		return nil
	}
	if strings.TrimSpace(p.spec.MissionSource.Path) != "" {
		return newOraclePolicyViolation(ReasonExamPromptPolicy, "missionSource.path", p.spec.PromptMode, "promptMode=exam requires missionSource.promptSource.path and forbids missionSource.path")
	}
	if err := p.validateExamPromptSource(); err != nil {
		return err
	}
	if strings.TrimSpace(p.spec.MissionSource.OracleSource.Path) == "" {
		return newOraclePolicyViolation(ReasonOracleEvaluator, "missionSource.oracleSource.path", p.spec.PromptMode, "promptMode=exam requires missionSource.oracleSource.path")
	}
	if p.spec.Evaluation.Mode != EvaluationModeOracle {
		return newOraclePolicyViolation(ReasonOracleEvaluator, "evaluation.mode", p.spec.PromptMode, "promptMode=exam requires evaluation.mode=oracle")
	}
	if err := validateExamEvaluatorSettings(p.spec); err != nil {
		return err
	}
	if !p.allFlowsOmitSuiteFile {
		return newOraclePolicyViolation(ReasonExamPromptPolicy, "flows[].suiteFile", p.spec.PromptMode, "promptMode=exam requires missionSource prompt/oracle split; remove flows[].suiteFile")
	}
	if err := validateExamOracleVisibility(p.spec, p.absPath); err != nil {
		return err
	}
	p.needsInlineMissionPack = true
	return nil
}

func (p *specParser) validateExamPromptSource() error {
	if strings.TrimSpace(p.spec.MissionSource.PromptSource.Path) != "" {
		return nil
	}
	for i := range p.spec.Flows {
		if strings.TrimSpace(p.spec.Flows[i].SuiteFile) != "" {
			continue
		}
		if strings.TrimSpace(p.spec.Flows[i].PromptSource.Path) == "" {
			return newOraclePolicyViolation(ReasonExamPromptPolicy, "missionSource.promptSource.path", p.spec.PromptMode, "promptMode=exam requires missionSource.promptSource.path or flows[].promptSource.path for inline mission-pack flows")
		}
	}
	return nil
}

func validateExamEvaluatorSettings(spec SpecV1) error {
	if spec.Evaluation.Evaluator.Kind == "" {
		return newOraclePolicyViolation(ReasonOracleEvaluator, "evaluation.evaluator.kind", spec.PromptMode, "promptMode=exam requires evaluation.evaluator.kind")
	}
	if spec.Evaluation.Evaluator.Kind == EvaluatorKindScript && len(spec.Evaluation.Evaluator.Command) == 0 {
		return newOraclePolicyViolation(ReasonOracleEvaluator, "evaluation.evaluator.command", spec.PromptMode, "promptMode=exam with evaluation.evaluator.kind=script requires non-empty evaluation.evaluator.command")
	}
	return nil
}

func validateExamOracleVisibility(spec SpecV1, absPath string) error {
	if spec.MissionSource.OracleSource.Visibility != OracleVisibilityHostOnly {
		return nil
	}
	agentRoot := resolveAgentReadableRoot(absPath)
	within, err := pathWithinRoot(agentRoot, spec.MissionSource.OracleSource.Path)
	if err != nil {
		return err
	}
	if !within {
		return nil
	}
	return &OraclePolicyViolationError{
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

func newOraclePolicyViolation(code string, field string, promptMode string, description string) error {
	return &OraclePolicyViolationError{
		Code: code,
		Violation: OraclePolicyViolation{
			Field:       field,
			PromptMode:  promptMode,
			Description: description,
		},
	}
}

func (p *specParser) parseFlows() error {
	for i := range p.spec.Flows {
		if err := p.parseFlow(i); err != nil {
			return err
		}
	}
	sort.Strings(p.flowIDs)
	return nil
}

func (p *specParser) parseFlow(index int) error {
	flow := &p.spec.Flows[index]
	hasInlineMissionPack, err := p.normalizeFlowBasics(index, flow)
	if err != nil {
		return err
	}
	if err := p.normalizeFlowRunner(flow); err != nil {
		return err
	}
	if err := p.normalizeFlowPolicy(flow); err != nil {
		return err
	}
	parsedSuite, err := p.loadFlowSuite(*flow, hasInlineMissionPack)
	if err != nil {
		return err
	}
	if strings.TrimSpace(flow.PromptTemplate.Path) != "" {
		parsedSuite, err = applyFlowPromptTemplate(parsedSuite, p.spec, *flow)
		if err != nil {
			return fmt.Errorf("flow %q: %w", flow.FlowID, err)
		}
	}
	p.flowSuites[flow.FlowID] = parsedSuite
	p.flowIDs = append(p.flowIDs, flow.FlowID)
	return nil
}

func (p *specParser) normalizeFlowBasics(index int, flow *FlowSpec) (bool, error) {
	flow.FlowID = ids.SanitizeComponent(strings.TrimSpace(flow.FlowID))
	if flow.FlowID == "" {
		return false, fmt.Errorf("flow[%d]: missing/invalid flowId", index)
	}
	if _, ok := p.flowSuites[flow.FlowID]; ok {
		return false, fmt.Errorf("duplicate flowId %q", flow.FlowID)
	}
	flow.SuiteFile = strings.TrimSpace(flow.SuiteFile)
	flow.PromptSource.Path = resolveSpecRelativePath(p.absPath, flow.PromptSource.Path, false)
	flow.PromptTemplate.Path = resolveSpecRelativePath(p.absPath, flow.PromptTemplate.Path, false)
	if flow.SuiteFile == "" {
		if !p.needsInlineMissionPack {
			return false, fmt.Errorf("flow %q: missing suiteFile (set flows[].suiteFile or mission source path)", flow.FlowID)
		}
		return true, nil
	}
	if flow.PromptSource.Path != "" {
		return false, fmt.Errorf("flow %q: promptSource.path is only supported when suiteFile is omitted", flow.FlowID)
	}
	if !filepath.IsAbs(flow.SuiteFile) {
		flow.SuiteFile = filepath.Clean(filepath.Join(filepath.Dir(p.absPath), flow.SuiteFile))
	}
	return false, nil
}

func (p *specParser) normalizeFlowRunner(flow *FlowSpec) error {
	flow.Runner.Type = strings.TrimSpace(strings.ToLower(flow.Runner.Type))
	if flow.Runner.Type == "" {
		flow.Runner.Type = RunnerTypeProcessCmd
	}
	if !isValidRunnerType(flow.Runner.Type) {
		return fmt.Errorf("flow %q: invalid runner.type (expected %s|%s|%s|%s|%s)", flow.FlowID, RunnerTypeProcessCmd, RunnerTypeCodexExec, RunnerTypeCodexSub, RunnerTypeClaudeSub, RunnerTypeCodexAppSrv)
	}
	if err := normalizeFlowRunnerModel(flow); err != nil {
		return err
	}
	normalizeFlowRunnerDefaults(flow, p.spec.Timeouts, p.absPath)
	if err := validateFlowRunnerBasics(flow); err != nil {
		return err
	}
	if err := validateFlowRunnerCwd(flow); err != nil {
		return err
	}
	if err := normalizeFlowResultChannel(flow); err != nil {
		return err
	}
	return validateFlowPromptModeRunnerRequirements(flow, p.spec.PromptMode)
}

func normalizeFlowRunnerModel(flow *FlowSpec) error {
	flow.Runner.Command = normalizeCommand(flow.Runner.Command)
	flow.Runner.RuntimeStrategies = normalizeLowerTerms(flow.Runner.RuntimeStrategies)
	flow.Runner.Model = strings.TrimSpace(flow.Runner.Model)
	flow.Runner.ModelReasoningEffort = strings.ToLower(strings.TrimSpace(flow.Runner.ModelReasoningEffort))
	flow.Runner.ModelReasoningPolicy = strings.ToLower(strings.TrimSpace(flow.Runner.ModelReasoningPolicy))
	if len(flow.Runner.Command) == 0 && flow.Runner.Type != RunnerTypeCodexAppSrv {
		return fmt.Errorf("flow %q: runner.command is required", flow.FlowID)
	}
	if flow.Runner.Type != RunnerTypeCodexAppSrv {
		if flow.Runner.Model != "" || flow.Runner.ModelReasoningEffort != "" || flow.Runner.ModelReasoningPolicy != "" {
			return fmt.Errorf("flow %q: runner.model and runner.modelReasoning* are supported only for runner.type=%s", flow.FlowID, RunnerTypeCodexAppSrv)
		}
	}
	if flow.Runner.ModelReasoningEffort == "" && flow.Runner.ModelReasoningPolicy != "" {
		return fmt.Errorf("flow %q: runner.modelReasoningPolicy requires runner.modelReasoningEffort", flow.FlowID)
	}
	if flow.Runner.ModelReasoningEffort != "" && !isValidModelReasoningEffort(flow.Runner.ModelReasoningEffort) {
		return fmt.Errorf("flow %q: invalid runner.modelReasoningEffort (expected %s|%s|%s|%s|%s|%s)", flow.FlowID, ModelReasoningEffortNone, ModelReasoningEffortMinimal, ModelReasoningEffortLow, ModelReasoningEffortMedium, ModelReasoningEffortHigh, ModelReasoningEffortXHigh)
	}
	if flow.Runner.ModelReasoningEffort != "" && flow.Runner.ModelReasoningPolicy == "" {
		flow.Runner.ModelReasoningPolicy = ModelReasoningPolicyBestEffort
	}
	if flow.Runner.ModelReasoningPolicy != "" && !isValidModelReasoningPolicy(flow.Runner.ModelReasoningPolicy) {
		return fmt.Errorf("flow %q: invalid runner.modelReasoningPolicy (expected %s|%s)", flow.FlowID, ModelReasoningPolicyBestEffort, ModelReasoningPolicyRequired)
	}
	return nil
}

func normalizeFlowRunnerDefaults(flow *FlowSpec, timeouts TimeoutsSpec, absPath string) {
	if strings.TrimSpace(flow.Runner.SessionIsolation) == "" {
		if flow.Runner.Type == RunnerTypeCodexAppSrv {
			flow.Runner.SessionIsolation = "native"
		} else {
			flow.Runner.SessionIsolation = "process"
		}
	}
	if strings.TrimSpace(flow.Runner.FeedbackPolicy) == "" {
		flow.Runner.FeedbackPolicy = schema.FeedbackPolicyAutoFailV1
	}
	normalizeFlowToolDriverDefaults(flow)
	applyFlowTimeoutDefaults(flow, timeouts)
	normalizeFlowRunnerShims(flow)
	normalizeFlowFinalizationDefaults(flow)
	normalizeFlowRunnerCwdDefaults(flow, absPath)
	if flow.Runner.FreshAgentPerAttempt == nil {
		def := true
		flow.Runner.FreshAgentPerAttempt = &def
	}
}

func normalizeFlowToolDriverDefaults(flow *FlowSpec) {
	flow.Runner.ToolDriver.Kind = strings.ToLower(strings.TrimSpace(flow.Runner.ToolDriver.Kind))
	if flow.Runner.ToolDriver.Kind == "" {
		flow.Runner.ToolDriver.Kind = ToolDriverShell
	}
	flow.Runner.ToolDriver.Shims = normalizeCommand(flow.Runner.ToolDriver.Shims)
}

func applyFlowTimeoutDefaults(flow *FlowSpec, timeouts TimeoutsSpec) {
	if strings.TrimSpace(flow.Runner.TimeoutStart) == "" && strings.TrimSpace(timeouts.TimeoutStart) != "" {
		flow.Runner.TimeoutStart = timeouts.TimeoutStart
	}
	if flow.Runner.TimeoutMs <= 0 && timeouts.DefaultAttemptTimeoutMs > 0 {
		flow.Runner.TimeoutMs = timeouts.DefaultAttemptTimeoutMs
	}
}

func normalizeFlowRunnerShims(flow *FlowSpec) {
	flow.PromptTemplate.AllowRunnerEnvKeys = dedupeStringsStable(normalizeCommand(flow.PromptTemplate.AllowRunnerEnvKeys))
	flow.Runner.Shims = dedupeStringsStable(normalizeCommand(append(append([]string{}, flow.Runner.Shims...), flow.Runner.ToolDriver.Shims...)))
}

func normalizeFlowFinalizationDefaults(flow *FlowSpec) {
	flow.Runner.Finalization.Mode = strings.ToLower(strings.TrimSpace(flow.Runner.Finalization.Mode))
	if flow.Runner.Finalization.Mode == "" {
		flow.Runner.Finalization.Mode = normalizedFinalizationMode(flow.Runner.FeedbackPolicy)
	}
	if flow.Runner.Finalization.MinResultTurn == 0 {
		flow.Runner.Finalization.MinResultTurn = DefaultMinResultTurn
	}
	flow.Runner.Finalization.ResultChannel.Kind = strings.ToLower(strings.TrimSpace(flow.Runner.Finalization.ResultChannel.Kind))
	if flow.Runner.Finalization.ResultChannel.Kind == "" {
		flow.Runner.Finalization.ResultChannel.Kind = defaultResultChannelKind(flow.Runner.Finalization.Mode)
	}
}

func normalizeFlowRunnerCwdDefaults(flow *FlowSpec, absPath string) {
	flow.Runner.Cwd.Mode = strings.ToLower(strings.TrimSpace(flow.Runner.Cwd.Mode))
	if flow.Runner.Cwd.Mode == "" {
		flow.Runner.Cwd.Mode = RunnerCwdModeInherit
	}
	flow.Runner.Cwd.BasePath = strings.TrimSpace(flow.Runner.Cwd.BasePath)
	if flow.Runner.Cwd.BasePath != "" {
		flow.Runner.Cwd.BasePath = normalizeCwdBasePath(flow.Runner.Cwd.BasePath, absPath)
	}
	flow.Runner.Cwd.Retain = strings.ToLower(strings.TrimSpace(flow.Runner.Cwd.Retain))
	if flow.Runner.Cwd.Retain == "" {
		flow.Runner.Cwd.Retain = RunnerCwdRetainNever
	}
}

func normalizedFinalizationMode(feedbackPolicy string) string {
	switch schema.NormalizeFeedbackPolicyV1(feedbackPolicy) {
	case schema.FeedbackPolicyStrictV1:
		return FinalizationModeStrict
	default:
		return FinalizationModeAutoFail
	}
}

func normalizeCwdBasePath(basePath string, absPath string) string {
	if filepath.IsAbs(basePath) {
		return filepath.Clean(basePath)
	}
	return filepath.Clean(filepath.Join(filepath.Dir(absPath), basePath))
}

func defaultResultChannelKind(mode string) string {
	if mode == FinalizationModeAutoFromResultJSON {
		return ResultChannelFileJSON
	}
	return ResultChannelNone
}

func validateFlowRunnerBasics(flow *FlowSpec) error {
	if !schema.IsValidFeedbackPolicyV1(flow.Runner.FeedbackPolicy) {
		return fmt.Errorf("flow %q: invalid runner.feedbackPolicy", flow.FlowID)
	}
	if !isValidToolDriverKind(flow.Runner.ToolDriver.Kind) {
		return fmt.Errorf("flow %q: invalid runner.toolDriver.kind (expected %s|%s|%s|%s)", flow.FlowID, ToolDriverShell, ToolDriverCLIFunnel, ToolDriverMCPProxy, ToolDriverHTTPProxy)
	}
	if !schema.IsValidTimeoutStartV1(flow.Runner.TimeoutStart) {
		return fmt.Errorf("flow %q: invalid runner.timeoutStart", flow.FlowID)
	}
	if flow.Runner.MCP.MaxToolCalls < 0 || flow.Runner.MCP.IdleTimeoutMs < 0 {
		return fmt.Errorf("flow %q: runner.mcp fields must be >= 0", flow.FlowID)
	}
	if !isValidFinalizationMode(flow.Runner.Finalization.Mode) {
		return fmt.Errorf("flow %q: invalid runner.finalization.mode (expected %s|%s|%s)", flow.FlowID, FinalizationModeStrict, FinalizationModeAutoFail, FinalizationModeAutoFromResultJSON)
	}
	if flow.Runner.Finalization.MinResultTurn < 0 {
		return fmt.Errorf("flow %q: runner.finalization.minResultTurn must be >= 1 when set", flow.FlowID)
	}
	return nil
}

func validateFlowRunnerCwd(flow *FlowSpec) error {
	if !isValidRunnerCwdMode(flow.Runner.Cwd.Mode) {
		return fmt.Errorf("flow %q: invalid runner.cwd.mode (expected %s|%s)", flow.FlowID, RunnerCwdModeInherit, RunnerCwdModeTempEmptyPerAttempt)
	}
	if !isValidRunnerCwdRetain(flow.Runner.Cwd.Retain) {
		return fmt.Errorf("flow %q: invalid runner.cwd.retain (expected %s|%s|%s)", flow.FlowID, RunnerCwdRetainNever, RunnerCwdRetainOnFailure, RunnerCwdRetainAlways)
	}
	if flow.Runner.Cwd.Mode == RunnerCwdModeInherit && flow.Runner.Cwd.BasePath != "" {
		return fmt.Errorf("flow %q: runner.cwd.basePath requires runner.cwd.mode=%s", flow.FlowID, RunnerCwdModeTempEmptyPerAttempt)
	}
	if flow.Runner.Cwd.Mode == RunnerCwdModeInherit && flow.Runner.Cwd.Retain != RunnerCwdRetainNever {
		return fmt.Errorf("flow %q: runner.cwd.retain requires runner.cwd.mode=%s", flow.FlowID, RunnerCwdModeTempEmptyPerAttempt)
	}
	if flow.Runner.Cwd.Mode != RunnerCwdModeInherit && flow.Runner.Type != RunnerTypeCodexAppSrv {
		return fmt.Errorf("flow %q: runner.cwd.mode=%s is supported only for runner.type=%s", flow.FlowID, flow.Runner.Cwd.Mode, RunnerTypeCodexAppSrv)
	}
	return nil
}

func normalizeFlowResultChannel(flow *FlowSpec) error {
	if !isValidResultChannelKind(flow.Runner.Finalization.ResultChannel.Kind) {
		return fmt.Errorf("flow %q: invalid runner.finalization.resultChannel.kind (expected %s|%s|%s)", flow.FlowID, ResultChannelNone, ResultChannelFileJSON, ResultChannelStdoutJSON)
	}
	switch flow.Runner.Finalization.ResultChannel.Kind {
	case ResultChannelFileJSON:
		flow.Runner.Finalization.ResultChannel.Path = strings.TrimSpace(flow.Runner.Finalization.ResultChannel.Path)
		if flow.Runner.Finalization.ResultChannel.Path == "" {
			flow.Runner.Finalization.ResultChannel.Path = DefaultResultChannelPath
		}
		if filepath.IsAbs(flow.Runner.Finalization.ResultChannel.Path) {
			return fmt.Errorf("flow %q: runner.finalization.resultChannel.path must be attempt-relative", flow.FlowID)
		}
		flow.Runner.Finalization.ResultChannel.Marker = ""
	case ResultChannelStdoutJSON:
		flow.Runner.Finalization.ResultChannel.Marker = strings.TrimSpace(flow.Runner.Finalization.ResultChannel.Marker)
		if flow.Runner.Finalization.ResultChannel.Marker == "" {
			flow.Runner.Finalization.ResultChannel.Marker = DefaultResultChannelMarker
		}
		flow.Runner.Finalization.ResultChannel.Path = ""
	default:
		flow.Runner.Finalization.ResultChannel.Path = ""
		flow.Runner.Finalization.ResultChannel.Marker = ""
	}
	if flow.Runner.Finalization.Mode == FinalizationModeAutoFromResultJSON && flow.Runner.Finalization.ResultChannel.Kind == ResultChannelNone {
		return fmt.Errorf("flow %q: runner.finalization.mode=%s requires runner.finalization.resultChannel.kind", flow.FlowID, FinalizationModeAutoFromResultJSON)
	}
	return nil
}

func validateFlowPromptModeRunnerRequirements(flow *FlowSpec, promptMode string) error {
	if promptMode != PromptModeMissionOnly && promptMode != PromptModeExam {
		return nil
	}
	if flow.Runner.Finalization.Mode != FinalizationModeAutoFromResultJSON {
		return fmt.Errorf("flow %q: promptMode=%s requires runner.finalization.mode=%s", flow.FlowID, promptMode, FinalizationModeAutoFromResultJSON)
	}
	if flow.Runner.ToolDriver.Kind == ToolDriverCLIFunnel && len(flow.Runner.Shims) == 0 {
		return &ToolDriverShimRequirementError{
			Code: ReasonToolDriverShim,
			Violation: ToolDriverShimRequirement{
				FlowID:         flow.FlowID,
				PromptMode:     promptMode,
				ToolDriverKind: flow.Runner.ToolDriver.Kind,
				RequiredOneOf:  []string{"runner.shims", "runner.toolDriver.shims"},
				Snippet:        "runner.shims: [\"tool-cli\"]\n# or\nrunner.toolDriver.shims: [\"tool-cli\"]",
			},
		}
	}
	return nil
}

func (p *specParser) normalizeFlowPolicy(flow *FlowSpec) error {
	if !*flow.Runner.FreshAgentPerAttempt {
		return fmt.Errorf("flow %q: runner.freshAgentPerAttempt=false is not supported (fresh sessions are required)", flow.FlowID)
	}
	if err := normalizeToolPolicySpec(&flow.ToolPolicy); err != nil {
		return &ToolPolicyConfigError{
			Code: ReasonToolPolicyConfig,
			Violation: ToolPolicyConfigViolation{
				FlowID:      flow.FlowID,
				Description: err.Error(),
			},
		}
	}
	flow.AdapterContract.RequiredOutputFields = normalizeCommand(flow.AdapterContract.RequiredOutputFields)
	if len(flow.AdapterContract.RequiredOutputFields) == 0 {
		flow.AdapterContract.RequiredOutputFields = []string{"attemptDir", "status", "errors"}
	}
	if !containsAllRequiredFields(flow.AdapterContract.RequiredOutputFields) {
		return fmt.Errorf("flow %q: adapterContract.requiredOutputFields must include attemptDir,status,errors", flow.FlowID)
	}
	return nil
}

func (p *specParser) loadFlowSuite(flow FlowSpec, hasInlineMissionPack bool) (suite.ParsedSuite, error) {
	if hasInlineMissionPack {
		return p.loadInlineFlowSuite(flow)
	}
	parsed, err := suite.ParseFile(flow.SuiteFile)
	if err != nil {
		return suite.ParsedSuite{}, fmt.Errorf("flow %q: parse suite: %w", flow.FlowID, err)
	}
	return parsed, nil
}

func (p *specParser) loadInlineFlowSuite(flow FlowSpec) (suite.ParsedSuite, error) {
	if p.spec.PromptMode == PromptModeExam {
		return p.loadExamFlowSuite(flow)
	}
	return p.loadMissionSourceFlowSuite(flow)
}

func (p *specParser) loadExamFlowSuite(flow FlowSpec) (suite.ParsedSuite, error) {
	promptSourcePath := strings.TrimSpace(flow.PromptSource.Path)
	if promptSourcePath == "" {
		promptSourcePath = strings.TrimSpace(p.spec.MissionSource.PromptSource.Path)
	}
	if promptSourcePath == "" {
		return suite.ParsedSuite{}, fmt.Errorf("flow %q: missing promptSource.path for exam mission-pack flow", flow.FlowID)
	}
	loaded, ok := p.inlineSplitMissionPackMap[promptSourcePath]
	if !ok {
		var err error
		loaded, err = LoadMissionPackSplit(promptSourcePath, p.spec.MissionSource.OracleSource.Path, p.spec.CampaignID)
		if err != nil {
			return suite.ParsedSuite{}, fmt.Errorf("flow %q: %w", flow.FlowID, err)
		}
		p.inlineSplitMissionPackMap[promptSourcePath] = loaded
	}
	if err := p.mergeOracleByMissionID(flow.FlowID, loaded.OracleByMissionID); err != nil {
		return suite.ParsedSuite{}, err
	}
	return loaded.Parsed, nil
}

func (p *specParser) loadMissionSourceFlowSuite(flow FlowSpec) (suite.ParsedSuite, error) {
	missionSourcePath := strings.TrimSpace(flow.PromptSource.Path)
	if missionSourcePath == "" {
		missionSourcePath = strings.TrimSpace(p.spec.MissionSource.Path)
	}
	if missionSourcePath == "" {
		return suite.ParsedSuite{}, fmt.Errorf("flow %q: missing suiteFile (set flows[].suiteFile, missionSource.path, or flows[].promptSource.path)", flow.FlowID)
	}
	loaded, ok := p.inlineMissionPackCache[missionSourcePath]
	if !ok {
		var err error
		loaded, err = LoadMissionPack(missionSourcePath, p.spec.CampaignID)
		if err != nil {
			return suite.ParsedSuite{}, fmt.Errorf("flow %q: %w", flow.FlowID, err)
		}
		p.inlineMissionPackCache[missionSourcePath] = loaded
	}
	return loaded, nil
}

func (p *specParser) mergeOracleByMissionID(flowID string, incoming map[string]string) error {
	if len(p.oracleByMissionID) == 0 {
		p.oracleByMissionID = cloneOracleByMissionID(incoming)
		return nil
	}
	if oracleByMissionIDEqual(p.oracleByMissionID, incoming) {
		return nil
	}
	return fmt.Errorf("flow %q: oracle mission mapping mismatch across prompt sources", flowID)
}

func (p *specParser) buildParsedSpec() (ParsedSpec, error) {
	base, err := p.baseFlowSuite()
	if err != nil {
		return ParsedSpec{}, err
	}
	if err := validateFlowSuiteAlignment(base, p.flowSuites, p.flowIDs); err != nil {
		return ParsedSpec{}, err
	}
	indexes, err := ResolveMissionIndexes(base.Suite, p.spec.MissionSource.Selection)
	if err != nil {
		return ParsedSpec{}, err
	}
	if len(indexes) == 0 {
		return ParsedSpec{}, fmt.Errorf("campaign selection resolved to zero missions")
	}
	if err := applyTotalMissionWindow(&p.spec, indexes); err != nil {
		return ParsedSpec{}, err
	}
	parsed := ParsedSpec{
		SpecPath:          p.absPath,
		Spec:              p.spec,
		BaseSuite:         base,
		FlowSuites:        p.flowSuites,
		MissionIndexes:    indexes,
		OracleByMissionID: p.oracleByMissionID,
	}
	return validatePromptModeViolations(parsed)
}

func (p *specParser) baseFlowSuite() (suite.ParsedSuite, error) {
	baseFlowID := p.spec.Flows[0].FlowID
	base := p.flowSuites[baseFlowID]
	if len(base.Suite.Missions) == 0 {
		return suite.ParsedSuite{}, fmt.Errorf("base flow suite has no missions")
	}
	return base, nil
}

func validateFlowSuiteAlignment(base suite.ParsedSuite, flowSuites map[string]suite.ParsedSuite, flowIDs []string) error {
	baseMissionCount := len(base.Suite.Missions)
	for _, flowID := range flowIDs {
		cur := flowSuites[flowID]
		if len(cur.Suite.Missions) != baseMissionCount {
			return fmt.Errorf("flow %q mission count %d does not match base flow %d", flowID, len(cur.Suite.Missions), baseMissionCount)
		}
		if err := validateMissionOrder(base, cur, flowID); err != nil {
			return err
		}
	}
	return nil
}

func validateMissionOrder(base suite.ParsedSuite, cur suite.ParsedSuite, flowID string) error {
	for i := range base.Suite.Missions {
		baseID := strings.TrimSpace(base.Suite.Missions[i].MissionID)
		curID := strings.TrimSpace(cur.Suite.Missions[i].MissionID)
		if baseID != curID {
			return fmt.Errorf("flow %q mission order mismatch at index %d (base=%q flow=%q)", flowID, i, baseID, curID)
		}
	}
	return nil
}

func applyTotalMissionWindow(spec *SpecV1, indexes []int) error {
	if spec.TotalMissions == 0 {
		spec.TotalMissions = len(indexes)
	}
	if spec.TotalMissions > len(indexes) {
		spec.TotalMissions = len(indexes)
	}
	if spec.TotalMissions > 0 && spec.TotalMissions < 1 {
		return fmt.Errorf("totalMissions must be >= 1 when set")
	}
	return nil
}

func validatePromptModeViolations(parsed ParsedSpec) (ParsedSpec, error) {
	if parsed.Spec.PromptMode != PromptModeMissionOnly && parsed.Spec.PromptMode != PromptModeExam {
		return parsed, nil
	}
	violations := EvaluatePromptModeViolations(parsed)
	if len(violations) == 0 {
		return parsed, nil
	}
	code := ReasonPromptModePolicy
	if parsed.Spec.PromptMode == PromptModeExam {
		code = ReasonExamPromptPolicy
	}
	return ParsedSpec{}, &PromptModeViolationError{
		Code:       code,
		PromptMode: parsed.Spec.PromptMode,
		Violations: violations,
	}
}
func (s SpecV1) PairGateEnabled() bool {
	if s.PairGate.Enabled == nil {
		return true
	}
	return *s.PairGate.Enabled
}

func pairGateSpecConfigured(in PairGateSpec) bool {
	return in.Enabled != nil || in.StopOnFirstMissionFailure || strings.TrimSpace(in.TraceProfile) != ""
}

func pairGateSpecsEqual(a PairGateSpec, b PairGateSpec) bool {
	if (a.Enabled == nil) != (b.Enabled == nil) {
		return false
	}
	if a.Enabled != nil && b.Enabled != nil && *a.Enabled != *b.Enabled {
		return false
	}
	return a.StopOnFirstMissionFailure == b.StopOnFirstMissionFailure &&
		strings.TrimSpace(strings.ToLower(a.TraceProfile)) == strings.TrimSpace(strings.ToLower(b.TraceProfile))
}

func cloneOracleByMissionID(in map[string]string) map[string]string {
	if len(in) == 0 {
		return map[string]string{}
	}
	out := make(map[string]string, len(in))
	for missionID, oraclePath := range in {
		out[missionID] = oraclePath
	}
	return out
}

func oracleByMissionIDEqual(a map[string]string, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for missionID, oraclePath := range a {
		if b[missionID] != oraclePath {
			return false
		}
	}
	return true
}

func normalizeToolPolicySpec(policy *ToolPolicySpec) error {
	if policy == nil {
		return nil
	}
	policy.Allow = normalizeToolPolicyRules(policy.Allow)
	policy.Deny = normalizeToolPolicyRules(policy.Deny)
	aliases, err := normalizeToolPolicyAliases(policy.Aliases)
	if err != nil {
		return err
	}
	policy.Aliases = aliases
	if err := validateToolPolicyRules(policy.Allow); err != nil {
		return err
	}
	if err := validateToolPolicyRules(policy.Deny); err != nil {
		return err
	}
	if len(policy.Allow) == 0 && len(policy.Deny) == 0 {
		policy.Aliases = nil
	}
	return nil
}

func normalizeToolPolicyAliases(in map[string][]string) (map[string][]string, error) {
	aliases := map[string][]string{}
	for rawKey, rawValues := range in {
		key := strings.ToLower(strings.TrimSpace(rawKey))
		if key == "" {
			return nil, fmt.Errorf("toolPolicy.aliases keys must be non-empty")
		}
		values := normalizeToolPolicyAliasValues(rawValues)
		if len(values) > 0 {
			aliases[key] = values
		}
	}
	return aliases, nil
}

func normalizeToolPolicyAliasValues(in []string) []string {
	values := make([]string, 0, len(in))
	seen := map[string]bool{}
	for _, raw := range in {
		val := strings.ToLower(strings.TrimSpace(raw))
		if val == "" || seen[val] {
			continue
		}
		seen[val] = true
		values = append(values, val)
	}
	sort.Strings(values)
	return values
}

func normalizeToolPolicyRules(in []ToolPolicyRuleSpec) []ToolPolicyRuleSpec {
	if len(in) == 0 {
		return nil
	}
	seen := map[string]bool{}
	out := make([]ToolPolicyRuleSpec, 0, len(in))
	for _, rule := range in {
		norm := ToolPolicyRuleSpec{
			Namespace: strings.ToLower(strings.TrimSpace(rule.Namespace)),
			Prefix:    strings.ToLower(strings.TrimSpace(rule.Prefix)),
		}
		key := norm.Namespace + "\x1f" + norm.Prefix
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, norm)
	}
	return out
}

func validateToolPolicyRules(in []ToolPolicyRuleSpec) error {
	for _, rule := range in {
		if strings.TrimSpace(rule.Namespace) == "" && strings.TrimSpace(rule.Prefix) == "" {
			return fmt.Errorf("toolPolicy rules require namespace and/or prefix")
		}
	}
	return nil
}

var promptTemplateTokenRE = regexp.MustCompile(`\{\{([a-zA-Z0-9_.-]+)\}\}`)

func applyFlowPromptTemplate(parsed suite.ParsedSuite, spec SpecV1, flow FlowSpec) (suite.ParsedSuite, error) {
	templatePath := strings.TrimSpace(flow.PromptTemplate.Path)
	if templatePath == "" {
		return parsed, nil
	}
	raw, err := os.ReadFile(templatePath)
	if err != nil {
		return suite.ParsedSuite{}, err
	}
	tpl := string(raw)
	rendered := parsed
	rendered.Suite.Missions = append([]suite.MissionV1(nil), parsed.Suite.Missions...)
	for idx := range rendered.Suite.Missions {
		mission := rendered.Suite.Missions[idx]
		vars := map[string]string{
			"campaignId":   spec.CampaignID,
			"flowId":       flow.FlowID,
			"suiteId":      rendered.Suite.SuiteID,
			"missionId":    mission.MissionID,
			"missionIndex": strconv.Itoa(idx),
			"prompt":       mission.Prompt,
			"tagsCsv":      strings.Join(mission.Tags, ","),
		}
		for _, envKey := range flow.PromptTemplate.AllowRunnerEnvKeys {
			envVal, ok := flow.Runner.Env[envKey]
			if !ok {
				return suite.ParsedSuite{}, fmt.Errorf("promptTemplate allowRunnerEnvKeys references runner.env[%q], but key is missing", envKey)
			}
			vars["runnerEnv."+envKey] = envVal
		}
		renderedPrompt, err := applyPromptTemplateStrict(tpl, vars)
		if err != nil {
			return suite.ParsedSuite{}, err
		}
		rendered.Suite.Missions[idx].Prompt = renderedPrompt
	}
	rendered.CanonicalJSON = rendered.Suite
	return rendered, nil
}

func applyPromptTemplateStrict(tpl string, vars map[string]string) (string, error) {
	matches := promptTemplateTokenRE.FindAllStringSubmatch(tpl, -1)
	for _, m := range matches {
		if len(m) != 2 {
			continue
		}
		token := strings.TrimSpace(m[1])
		if token == "" {
			continue
		}
		if _, ok := vars[token]; !ok {
			return "", fmt.Errorf("prompt template contains unresolved token %q", token)
		}
	}
	out := tpl
	keys := make([]string, 0, len(vars))
	for k := range vars {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, key := range keys {
		out = strings.ReplaceAll(out, "{{"+key+"}}", vars[key])
	}
	return out, nil
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
	out, err := resolveMissionIndexesByMode(sf, sel, mode, missionCount)
	if err != nil {
		return nil, err
	}
	out = dedupeIntsStable(out)
	if len(out) == 0 {
		return nil, fmt.Errorf("mission selection resolved to zero missions")
	}
	return out, nil
}

func resolveMissionIndexesByMode(sf suite.SuiteFileV1, sel MissionSelectionSpec, mode string, missionCount int) ([]int, error) {
	switch mode {
	case SelectionModeAll:
		return resolveMissionIndexesAll(missionCount), nil
	case SelectionModeMissionID:
		return resolveMissionIndexesByMissionID(sf, sel.MissionIDs, mode)
	case SelectionModeIndex:
		return resolveMissionIndexesByIndex(sel.Indexes, mode, missionCount)
	case SelectionModeRange:
		return resolveMissionIndexesByRange(sel.Range, missionCount)
	default:
		return nil, fmt.Errorf("invalid mission selection mode %q (expected all|mission_id|index|range)", mode)
	}
}

func resolveMissionIndexesAll(missionCount int) []int {
	out := make([]int, 0, missionCount)
	for i := 0; i < missionCount; i++ {
		out = append(out, i)
	}
	return out
}

func resolveMissionIndexesByMissionID(sf suite.SuiteFileV1, missionIDs []string, mode string) ([]int, error) {
	if len(missionIDs) == 0 {
		return nil, fmt.Errorf("mission selection mode %q requires missionIds", mode)
	}
	indexByID := map[string]int{}
	for i := range sf.Missions {
		indexByID[sf.Missions[i].MissionID] = i
	}
	out := make([]int, 0, len(missionIDs))
	for _, raw := range missionIDs {
		id := strings.TrimSpace(raw)
		idx, ok := indexByID[id]
		if !ok {
			return nil, fmt.Errorf("mission selection id not found: %q", id)
		}
		out = append(out, idx)
	}
	return out, nil
}

func resolveMissionIndexesByIndex(indexes []int, mode string, missionCount int) ([]int, error) {
	if len(indexes) == 0 {
		return nil, fmt.Errorf("mission selection mode %q requires indexes", mode)
	}
	out := make([]int, 0, len(indexes))
	for _, idx := range indexes {
		if err := validateMissionIndexBounds(idx, missionCount); err != nil {
			return nil, err
		}
		out = append(out, idx)
	}
	return out, nil
}

func resolveMissionIndexesByRange(window MissionRangeWindow, missionCount int) ([]int, error) {
	if window.End < window.Start {
		return nil, fmt.Errorf("mission selection range end must be >= start")
	}
	out := make([]int, 0, window.End-window.Start+1)
	for idx := window.Start; idx <= window.End; idx++ {
		if err := validateMissionIndexBounds(idx, missionCount); err != nil {
			return nil, err
		}
		out = append(out, idx)
	}
	return out, nil
}

func validateMissionIndexBounds(index int, missionCount int) error {
	if index < 0 || index >= missionCount {
		return fmt.Errorf("mission selection index out of range: %d", index)
	}
	return nil
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
	terms := resolvePromptModeTerms(parsed.Spec)
	if len(terms) == 0 || len(parsed.MissionIndexes) == 0 || len(parsed.FlowSuites) == 0 {
		return nil
	}
	return collectPromptModeViolations(parsed, terms)
}

func resolvePromptModeTerms(spec SpecV1) []string {
	terms := spec.NoContext.ForbiddenPromptTerms
	if len(terms) > 0 {
		return terms
	}
	if spec.PromptMode == PromptModeExam {
		return defaultExamForbiddenTerms()
	}
	return defaultMissionOnlyForbiddenTerms()
}

func collectPromptModeViolations(parsed ParsedSpec, terms []string) []PromptModeViolation {
	seen := map[string]bool{}
	out := make([]PromptModeViolation, 0, 8)
	for _, flow := range parsed.Spec.Flows {
		ps, ok := parsed.FlowSuites[flow.FlowID]
		if !ok {
			continue
		}
		out = append(out, collectFlowPromptModeViolations(flow.FlowID, ps, parsed.MissionIndexes, terms, seen)...)
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

func collectFlowPromptModeViolations(flowID string, parsedSuite suite.ParsedSuite, indexes []int, terms []string, seen map[string]bool) []PromptModeViolation {
	out := make([]PromptModeViolation, 0, 4)
	for _, idx := range indexes {
		if idx < 0 || idx >= len(parsedSuite.Suite.Missions) {
			continue
		}
		mission := parsedSuite.Suite.Missions[idx]
		promptLower := strings.ToLower(mission.Prompt)
		for _, term := range terms {
			trimmed := strings.TrimSpace(term)
			needle := strings.ToLower(trimmed)
			if trimmed == "" || !strings.Contains(promptLower, needle) {
				continue
			}
			key := flowID + "|" + strconv.Itoa(idx) + "|" + mission.MissionID + "|" + needle
			if seen[key] {
				continue
			}
			seen[key] = true
			out = append(out, PromptModeViolation{
				FlowID:       flowID,
				MissionID:    mission.MissionID,
				MissionIndex: idx,
				Term:         trimmed,
			})
		}
	}
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
	case EvaluatorKindScript, EvaluatorKindBuiltin:
		return true
	default:
		return false
	}
}

func isValidOraclePolicyMode(v string) bool {
	switch strings.TrimSpace(strings.ToLower(v)) {
	case OraclePolicyModeStrict, OraclePolicyModeNormalized, OraclePolicyModeSemantic:
		return true
	default:
		return false
	}
}

func isValidOracleFormatMismatchPolicy(v string) bool {
	switch strings.TrimSpace(strings.ToLower(v)) {
	case OracleFormatMismatchFail, OracleFormatMismatchWarn, OracleFormatMismatchIgnore:
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

func isValidRunnerCwdMode(v string) bool {
	switch strings.TrimSpace(strings.ToLower(v)) {
	case RunnerCwdModeInherit, RunnerCwdModeTempEmptyPerAttempt:
		return true
	default:
		return false
	}
}

func isValidRunnerCwdRetain(v string) bool {
	switch strings.TrimSpace(strings.ToLower(v)) {
	case RunnerCwdRetainNever, RunnerCwdRetainOnFailure, RunnerCwdRetainAlways:
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
