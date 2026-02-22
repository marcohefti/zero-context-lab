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
	PromptModeDefault      = "default"
	PromptModeMissionOnly  = "mission_only"
	RunStatusValid         = "valid"
	RunStatusInvalid       = "invalid"
	RunStatusAborted       = "aborted"
	RunStatusRunning       = "running"
	ReasonGateFailed       = "ZCL_E_CAMPAIGN_GATE_FAILED"
	ReasonFirstMissionGate = "ZCL_E_CAMPAIGN_FIRST_MISSION_GATE_FAILED"
	ReasonFlowFailed       = "ZCL_E_CAMPAIGN_FLOW_FAILED"
	ReasonAborted          = "ZCL_E_CAMPAIGN_ABORTED"
	ReasonSemanticFailed   = "ZCL_E_CAMPAIGN_SEMANTIC_FAILED"
	ReasonPromptModePolicy = "ZCL_E_CAMPAIGN_PROMPT_MODE_VIOLATION"

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

	ResultChannelNone       = "none"
	ResultChannelFileJSON   = "file_json"
	ResultChannelStdoutJSON = "stdout_json"

	DefaultResultChannelPath   = "mission.result.json"
	DefaultResultChannelMarker = "ZCL_RESULT_JSON:"
)

type SpecV1 struct {
	SchemaVersion int    `json:"schemaVersion" yaml:"schemaVersion"`
	CampaignID    string `json:"campaignId" yaml:"campaignId"`
	OutRoot       string `json:"outRoot,omitempty" yaml:"outRoot,omitempty"`
	PromptMode    string `json:"promptMode,omitempty" yaml:"promptMode,omitempty"` // default|mission_only

	TotalMissions  int  `json:"totalMissions,omitempty" yaml:"totalMissions,omitempty"`
	CanaryMissions int  `json:"canaryMissions,omitempty" yaml:"canaryMissions,omitempty"`
	FailFast       bool `json:"failFast" yaml:"failFast"`

	MissionSource MissionSourceSpec `json:"missionSource,omitempty" yaml:"missionSource,omitempty"`
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
	Path      string               `json:"path,omitempty" yaml:"path,omitempty"`
	Selection MissionSelectionSpec `json:"selection,omitempty" yaml:"selection,omitempty"`
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
}

type PromptModeViolation struct {
	FlowID       string `json:"flowId"`
	MissionID    string `json:"missionId"`
	MissionIndex int    `json:"missionIndex"`
	Term         string `json:"term"`
}

type PromptModeViolationError struct {
	PromptMode string                `json:"promptMode"`
	Violations []PromptModeViolation `json:"violations"`
}

func (e *PromptModeViolationError) Error() string {
	if e == nil || len(e.Violations) == 0 {
		return "promptMode mission_only violation"
	}
	v := e.Violations[0]
	return fmt.Sprintf("promptMode mission_only violation: flow=%s mission=%s index=%d term=%q", v.FlowID, v.MissionID, v.MissionIndex, v.Term)
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
		return ParsedSpec{}, fmt.Errorf("invalid promptMode (expected %s|%s)", PromptModeDefault, PromptModeMissionOnly)
	}
	spec.NoContext.ForbiddenPromptTerms = normalizeTerms(spec.NoContext.ForbiddenPromptTerms)
	if spec.PromptMode == PromptModeMissionOnly && len(spec.NoContext.ForbiddenPromptTerms) == 0 {
		spec.NoContext.ForbiddenPromptTerms = defaultMissionOnlyForbiddenTerms()
	}
	spec.MissionSource.Path = strings.TrimSpace(spec.MissionSource.Path)
	if spec.MissionSource.Path != "" && !filepath.IsAbs(spec.MissionSource.Path) {
		spec.MissionSource.Path = filepath.Clean(filepath.Join(filepath.Dir(absPath), spec.MissionSource.Path))
	}
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
	for i := range spec.Flows {
		if strings.TrimSpace(spec.Flows[i].SuiteFile) == "" {
			needsMissionPack = true
			break
		}
	}
	var missionPackSuite *suite.ParsedSuite
	if needsMissionPack {
		if strings.TrimSpace(spec.MissionSource.Path) == "" {
			return ParsedSpec{}, fmt.Errorf("missionSource.path is required when any flow omits suiteFile")
		}
		loaded, err := LoadMissionPack(spec.MissionSource.Path, spec.CampaignID)
		if err != nil {
			return ParsedSpec{}, err
		}
		missionPackSuite = &loaded
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
			return ParsedSpec{}, fmt.Errorf("flow %q: invalid runner.type (expected %s|%s|%s|%s)", f.FlowID, RunnerTypeProcessCmd, RunnerTypeCodexExec, RunnerTypeCodexSub, RunnerTypeClaudeSub)
		}
		f.Runner.Command = normalizeCommand(f.Runner.Command)
		if len(f.Runner.Command) == 0 {
			return ParsedSpec{}, fmt.Errorf("flow %q: runner.command is required", f.FlowID)
		}
		if strings.TrimSpace(f.Runner.SessionIsolation) == "" {
			f.Runner.SessionIsolation = "process"
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
		if spec.PromptMode == PromptModeMissionOnly {
			if f.Runner.Finalization.Mode != FinalizationModeAutoFromResultJSON {
				return ParsedSpec{}, fmt.Errorf("flow %q: promptMode=mission_only requires runner.finalization.mode=%s", f.FlowID, FinalizationModeAutoFromResultJSON)
			}
			if f.Runner.ToolDriver.Kind == ToolDriverCLIFunnel && len(f.Runner.Shims) == 0 {
				return ParsedSpec{}, fmt.Errorf("flow %q: promptMode=mission_only with toolDriver.kind=%s requires runner.shims or runner.toolDriver.shims", f.FlowID, ToolDriverCLIFunnel)
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
		SpecPath:       absPath,
		Spec:           spec,
		BaseSuite:      base,
		FlowSuites:     flowSuites,
		MissionIndexes: indexes,
	}
	if spec.PromptMode == PromptModeMissionOnly {
		violations := EvaluatePromptModeViolations(parsed)
		if len(violations) > 0 {
			return ParsedSpec{}, &PromptModeViolationError{
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
	if parsed.Spec.PromptMode != PromptModeMissionOnly {
		return nil
	}
	terms := parsed.Spec.NoContext.ForbiddenPromptTerms
	if len(terms) == 0 {
		terms = defaultMissionOnlyForbiddenTerms()
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
	case RunnerTypeProcessCmd, RunnerTypeCodexExec, RunnerTypeCodexSub, RunnerTypeClaudeSub:
		return true
	default:
		return false
	}
}

func isValidPromptMode(v string) bool {
	switch strings.TrimSpace(strings.ToLower(v)) {
	case PromptModeDefault, PromptModeMissionOnly:
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

func isValidTraceProfile(v string) bool {
	switch strings.TrimSpace(strings.ToLower(v)) {
	case TraceProfileNone, TraceProfileStrictBrowserComp, TraceProfileMCPRequired:
		return true
	default:
		return false
	}
}

func FormatSelectionKey(index int, missionID string) string {
	return strconv.Itoa(index) + ":" + strings.TrimSpace(missionID)
}
