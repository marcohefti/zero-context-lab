package schema

import "encoding/json"

// Version constants for v1 artifacts/traces.
const (
	ArtifactSchemaV1        = 1
	TraceSchemaV1           = 1
	ArtifactLayoutVersionV1 = 1
)

// RunJSONV1 is written to: .zcl/runs/<runId>/run.json
type RunJSONV1 struct {
	SchemaVersion int `json:"schemaVersion"`
	// ArtifactLayoutVersion makes the directory contract explicit in evidence.
	ArtifactLayoutVersion int    `json:"artifactLayoutVersion"`
	RunID                 string `json:"runId"`
	SuiteID               string `json:"suiteId"`
	CreatedAt             string `json:"createdAt"` // RFC3339 UTC (use consistent precision)
	Pinned                bool   `json:"pinned,omitempty"`
}

// AttemptJSONV1 is written to: .zcl/runs/<runId>/attempts/<attemptId>/attempt.json
type AttemptJSONV1 struct {
	SchemaVersion int    `json:"schemaVersion"`
	RunID         string `json:"runId"`
	SuiteID       string `json:"suiteId"`
	MissionID     string `json:"missionId"`
	AttemptID     string `json:"attemptId"`
	AgentID       string `json:"agentId,omitempty"`
	Mode          string `json:"mode"`      // discovery|ci
	StartedAt     string `json:"startedAt"` // RFC3339 UTC (use consistent precision)
	TimeoutMs     int64  `json:"timeoutMs,omitempty"`
	// TimeoutStart controls when timeoutMs starts counting.
	// Allowed values: attempt_start|first_tool_call.
	TimeoutStart string `json:"timeoutStart,omitempty"`
	// TimeoutStartedAt is set when timeoutStart=first_tool_call and the first funnel action begins.
	TimeoutStartedAt string `json:"timeoutStartedAt,omitempty"`
	// Blind enables zero-context prompt contamination checks for this attempt.
	Blind bool `json:"blind,omitempty"`
	// BlindTerms is the normalized list of harness terms used for contamination checks.
	BlindTerms []string `json:"blindTerms,omitempty"`
	// ScratchDir is a per-attempt scratch directory under <outRoot>/tmp.
	// It is optional but recommended for tools that need temporary files.
	ScratchDir string `json:"scratchDir,omitempty"`
}

// FeedbackJSONV1 is written to: .zcl/runs/<runId>/attempts/<attemptId>/feedback.json
//
// Exactly one of Result or ResultJSON should be set by the writer.
type FeedbackJSONV1 struct {
	SchemaVersion int             `json:"schemaVersion"`
	RunID         string          `json:"runId"`
	SuiteID       string          `json:"suiteId"`
	MissionID     string          `json:"missionId"`
	AttemptID     string          `json:"attemptId"`
	OK            bool            `json:"ok"`
	Result        string          `json:"result,omitempty"`
	ResultJSON    json.RawMessage `json:"resultJson,omitempty"`
	// Classification is optional friction triage that must never override trace evidence.
	// Allowed values: missing_primitive|naming_ux|output_shape|already_possible_better_way
	Classification string `json:"classification,omitempty"`
	// DecisionTags are normalized outcome tags for cross-run comparability.
	DecisionTags []string `json:"decisionTags,omitempty"`
	CreatedAt    string   `json:"createdAt"` // RFC3339 UTC (use consistent precision)
	// RedactionsApplied is informational only; scoring must not depend on it.
	RedactionsApplied []string `json:"redactionsApplied,omitempty"`
}

// AttemptReportJSONV1 is written to: .zcl/runs/<runId>/attempts/<attemptId>/attempt.report.json
type AttemptReportJSONV1 struct {
	SchemaVersion int    `json:"schemaVersion"`
	RunID         string `json:"runId"`
	SuiteID       string `json:"suiteId"`
	MissionID     string `json:"missionId"`
	AttemptID     string `json:"attemptId"`
	ComputedAt    string `json:"computedAt"` // RFC3339 UTC (use consistent precision)

	StartedAt string `json:"startedAt,omitempty"`
	EndedAt   string `json:"endedAt,omitempty"`

	OK *bool `json:"ok,omitempty"` // copied from feedback when present

	// Exactly one of Result or ResultJSON may be set (copied from feedback when present).
	Result     string          `json:"result,omitempty"`
	ResultJSON json.RawMessage `json:"resultJson,omitempty"`

	Classification string   `json:"classification,omitempty"`
	DecisionTags   []string `json:"decisionTags,omitempty"`

	Metrics AttemptMetricsV1 `json:"metrics"`
	// FailureCodeHistogram mirrors metrics.failuresByCode at the top-level
	// for easier scoring/aggregation pipelines.
	FailureCodeHistogram map[string]int64 `json:"failureCodeHistogram,omitempty"`
	// TimedOutBeforeFirstToolCall indicates timeout expired before first traced action could execute.
	TimedOutBeforeFirstToolCall bool `json:"timedOutBeforeFirstToolCall,omitempty"`
	// TokenEstimates are lightweight per-attempt token usage estimates.
	TokenEstimates *TokenEstimatesV1 `json:"tokenEstimates,omitempty"`

	Artifacts AttemptArtifactsV1 `json:"artifacts"`

	Integrity    *AttemptIntegrityV1  `json:"integrity,omitempty"`
	Signals      *AttemptSignalsV1    `json:"signals,omitempty"`
	Expectations *ExpectationResultV1 `json:"expectations,omitempty"`
}

type AttemptArtifactsV1 struct {
	AttemptJSON  string `json:"attemptJson"`
	TraceJSONL   string `json:"toolCallsJsonl"`
	FeedbackJSON string `json:"feedbackJson"`
	NotesJSONL   string `json:"notesJsonl,omitempty"`
	PromptTXT    string `json:"promptTxt,omitempty"`
	// Runner* are produced by suite orchestration when runner IO capture is enabled.
	RunnerCommandTXT string `json:"runnerCommandTxt,omitempty"`
	RunnerStdoutLOG  string `json:"runnerStdoutLog,omitempty"`
	RunnerStderrLOG  string `json:"runnerStderrLog,omitempty"`
}

type AttemptIntegrityV1 struct {
	TracePresent             bool     `json:"tracePresent"`
	TraceNonEmpty            bool     `json:"traceNonEmpty"`
	FeedbackPresent          bool     `json:"feedbackPresent"`
	FunnelBypassSuspected    bool     `json:"funnelBypassSuspected,omitempty"`
	PromptContaminated       bool     `json:"promptContaminated,omitempty"`
	PromptContaminationTerms []string `json:"promptContaminationTerms,omitempty"`
}

// AttemptSignalsV1 are lightweight, trace-derived “stuck/thrash” signals intended for quick triage.
// They must be explainable from evidence and deterministic to compute.
type AttemptSignalsV1 struct {
	// RepeatMaxStreak is the maximum consecutive streak of identical tool/op/input signatures.
	RepeatMaxStreak int64 `json:"repeatMaxStreak"`
	// DistinctCommandSignatures counts unique tool/op/input signatures observed.
	DistinctCommandSignatures int64 `json:"distinctCommandSignatures"`
	// FailureRateBps is failures/total expressed in basis points (0..10000).
	FailureRateBps int64 `json:"failureRateBps"`
	// NoProgressSuspected is a conservative heuristic based on repeats + low diversity.
	NoProgressSuspected bool `json:"noProgressSuspected,omitempty"`
	// CommandNamesSeen is a best-effort list of distinct command names (argv[0]) observed for CLI exec calls.
	CommandNamesSeen []string `json:"commandNamesSeen,omitempty"`
}

type ExpectationResultV1 struct {
	Evaluated bool                   `json:"evaluated"`
	OK        bool                   `json:"ok"`
	Failures  []ExpectationFailureV1 `json:"failures,omitempty"`
}

type ExpectationFailureV1 struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type AttemptMetricsV1 struct {
	ToolCallsTotal int64            `json:"toolCallsTotal"`
	FailuresTotal  int64            `json:"failuresTotal"`
	FailuresByCode map[string]int64 `json:"failuresByCode,omitempty"`
	RetriesTotal   int64            `json:"retriesTotal"`
	TimeoutsTotal  int64            `json:"timeoutsTotal"`
	WallTimeMs     int64            `json:"wallTimeMs"`

	DurationMsTotal int64 `json:"durationMsTotal"`
	DurationMsMin   int64 `json:"durationMsMin"`
	DurationMsMax   int64 `json:"durationMsMax"`
	DurationMsAvg   int64 `json:"durationMsAvg"`
	DurationMsP50   int64 `json:"durationMsP50"`
	DurationMsP95   int64 `json:"durationMsP95"`

	OutBytesTotal int64 `json:"outBytesTotal"`
	ErrBytesTotal int64 `json:"errBytesTotal"`

	OutPreviewTruncations int64 `json:"outPreviewTruncations"`
	ErrPreviewTruncations int64 `json:"errPreviewTruncations"`

	ToolCallsByTool map[string]int64 `json:"toolCallsByTool,omitempty"`
	ToolCallsByOp   map[string]int64 `json:"toolCallsByOp,omitempty"`
}

type TokenEstimatesV1 struct {
	Source string `json:"source"` // runner.metrics|trace-heuristic

	TotalTokens           *int64 `json:"totalTokens,omitempty"`
	InputTokens           *int64 `json:"inputTokens,omitempty"`
	OutputTokens          *int64 `json:"outputTokens,omitempty"`
	CachedInputTokens     *int64 `json:"cachedInputTokens,omitempty"`
	ReasoningOutputTokens *int64 `json:"reasoningOutputTokens,omitempty"`
}

// TraceEventV1 is one line in: tool.calls.jsonl
type TraceEventV1 struct {
	V  int    `json:"v"`  // TraceSchemaV1
	TS string `json:"ts"` // RFC3339 UTC (use consistent precision)

	RunID     string `json:"runId"`
	SuiteID   string `json:"suiteId,omitempty"`
	MissionID string `json:"missionId"`
	AttemptID string `json:"attemptId"`
	AgentID   string `json:"agentId,omitempty"`

	Tool string `json:"tool"`
	Op   string `json:"op"`

	// Input is a canonicalized/bounded representation of the tool invocation.
	Input json.RawMessage `json:"input,omitempty"`

	Result TraceResultV1 `json:"result"`
	IO     TraceIOV1     `json:"io"`

	RedactionsApplied []string          `json:"redactionsApplied,omitempty"`
	Enrichment        json.RawMessage   `json:"enrichment,omitempty"`
	Warnings          []TraceWarningV1  `json:"warnings,omitempty"`
	Integrity         *TraceIntegrityV1 `json:"integrity,omitempty"`
}

type TraceResultV1 struct {
	OK         bool   `json:"ok"`
	Code       string `json:"code,omitempty"`     // typed ZCL or normalized tool error code
	ExitCode   *int   `json:"exitCode,omitempty"` // CLI funnel only
	DurationMs int64  `json:"durationMs"`
}

type TraceIOV1 struct {
	OutBytes   int64  `json:"outBytes"`
	ErrBytes   int64  `json:"errBytes"`
	OutPreview string `json:"outPreview,omitempty"` // bounded
	ErrPreview string `json:"errPreview,omitempty"` // bounded
}

type TraceWarningV1 struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// TraceIntegrityV1 records integrity metadata about how the event was written,
// not about the underlying tool.
type TraceIntegrityV1 struct {
	Truncated bool `json:"truncated,omitempty"`
}

// NoteEventV1 is one line in: notes.jsonl
type NoteEventV1 struct {
	V  int    `json:"v"`  // TraceSchemaV1 (notes share trace schema versioning)
	TS string `json:"ts"` // RFC3339 UTC (use consistent precision)

	RunID     string `json:"runId"`
	SuiteID   string `json:"suiteId,omitempty"`
	MissionID string `json:"missionId"`
	AttemptID string `json:"attemptId"`
	AgentID   string `json:"agentId,omitempty"`

	Kind              string          `json:"kind"`              // agent|operator|system
	Message           string          `json:"message,omitempty"` // free-form, bounded/redacted
	Data              json.RawMessage `json:"data,omitempty"`    // structured note payload (optional)
	Tags              []string        `json:"tags,omitempty"`    // optional indexing
	RedactionsApplied []string        `json:"redactionsApplied,omitempty"`
}
