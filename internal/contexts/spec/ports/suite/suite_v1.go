package suite

// SuiteFileV1 is the minimal runner-agnostic suite definition format described in CONCEPT.md.
// It is intentionally small: defaults + missions + optional expectations that validate feedback.json.
type SuiteFileV1 struct {
	Version  int         `json:"version" yaml:"version"`
	SuiteID  string      `json:"suiteId" yaml:"suiteId"`
	Defaults DefaultsV1  `json:"defaults,omitempty" yaml:"defaults,omitempty"`
	Missions []MissionV1 `json:"missions" yaml:"missions"`
}

type DefaultsV1 struct {
	TimeoutMs    int64  `json:"timeoutMs,omitempty" yaml:"timeoutMs,omitempty"`
	TimeoutStart string `json:"timeoutStart,omitempty" yaml:"timeoutStart,omitempty"` // attempt_start|first_tool_call
	Mode         string `json:"mode,omitempty" yaml:"mode,omitempty"`                 // discovery|ci
	// FeedbackPolicy controls missing-feedback behavior in suite orchestration.
	// Allowed values: strict|auto_fail (default auto_fail).
	FeedbackPolicy string   `json:"feedbackPolicy,omitempty" yaml:"feedbackPolicy,omitempty"`
	Blind          bool     `json:"blind,omitempty" yaml:"blind,omitempty"`
	BlindTerms     []string `json:"blindTerms,omitempty" yaml:"blindTerms,omitempty"`
}

type MissionV1 struct {
	MissionID string     `json:"missionId" yaml:"missionId"`
	Prompt    string     `json:"prompt,omitempty" yaml:"prompt,omitempty"`
	Tags      []string   `json:"tags,omitempty" yaml:"tags,omitempty"`
	Expects   *ExpectsV1 `json:"expects,omitempty" yaml:"expects,omitempty"`
}

type ExpectsV1 struct {
	OK     *bool            `json:"ok,omitempty" yaml:"ok,omitempty"`
	Result *ResultExpectsV1 `json:"result,omitempty" yaml:"result,omitempty"`
	Trace  *TraceExpectsV1  `json:"trace,omitempty" yaml:"trace,omitempty"`
	// Semantic expectations evaluate mission-level validity of feedback/trace content.
	// Unlike ResultExpectsV1 (shape) and TraceExpectsV1 (counts/patterns), semantic rules
	// can express non-empty/placeholder/boilerplate constraints.
	Semantic *SemanticExpectsV1 `json:"semantic,omitempty" yaml:"semantic,omitempty"`
}

type ResultExpectsV1 struct {
	// Type is "string" or "json".
	Type string `json:"type" yaml:"type"`

	// Pattern is an RE2 regex applied to feedback.result when Type=="string".
	Pattern string `json:"pattern,omitempty" yaml:"pattern,omitempty"`

	// Equals is an exact match applied to feedback.result when Type=="string".
	Equals string `json:"equals,omitempty" yaml:"equals,omitempty"`

	// RequiredJSONPointers are JSON pointers (RFC 6901) that must exist in
	// feedback.resultJson when Type=="json".
	RequiredJSONPointers []string `json:"requiredJsonPointers,omitempty" yaml:"requiredJsonPointers,omitempty"`
}

// TraceExpectsV1 are expectations derived from tool.calls.jsonl evidence (not from agent claims).
// They are evaluated against attempt metrics/signals computed from the trace.
type TraceExpectsV1 struct {
	MaxToolCallsTotal int64 `json:"maxToolCallsTotal,omitempty" yaml:"maxToolCallsTotal,omitempty"`
	MaxFailuresTotal  int64 `json:"maxFailuresTotal,omitempty" yaml:"maxFailuresTotal,omitempty"`
	MaxTimeoutsTotal  int64 `json:"maxTimeoutsTotal,omitempty" yaml:"maxTimeoutsTotal,omitempty"`

	// MaxRepeatStreak guards against obvious looping (consecutive identical tool calls).
	MaxRepeatStreak int64 `json:"maxRepeatStreak,omitempty" yaml:"maxRepeatStreak,omitempty"`

	// RequireCommandPrefix requires that at least one CLI exec argv[0] has one of these prefixes.
	// Example: ["tool-cli"] ensures the attempt actually exercised the intended tool.
	RequireCommandPrefix []string `json:"requireCommandPrefix,omitempty" yaml:"requireCommandPrefix,omitempty"`
}

// SemanticExpectsV1 captures mission-level semantic checks for feedback.resultJson + trace.
// These checks are deterministic and runner-agnostic by design.
type SemanticExpectsV1 struct {
	// RequiredJSONPointers are RFC 6901 pointers that must exist in feedback.resultJson.
	RequiredJSONPointers []string `json:"requiredJsonPointers,omitempty" yaml:"requiredJsonPointers,omitempty"`
	// NonEmptyJSONPointers are RFC 6901 pointers that must exist and be meaningful
	// (not null, not empty, not configured placeholder values).
	NonEmptyJSONPointers []string `json:"nonEmptyJsonPointers,omitempty" yaml:"nonEmptyJsonPointers,omitempty"`
	// PlaceholderValues are case-insensitive values considered semantically invalid
	// for NonEmptyJSONPointers checks. Example: ["n/a","unknown","todo"].
	PlaceholderValues []string `json:"placeholderValues,omitempty" yaml:"placeholderValues,omitempty"`

	// RequireToolOps requires at least one of these trace ops to appear (e.g. tools/call).
	RequireToolOps []string `json:"requireToolOps,omitempty" yaml:"requireToolOps,omitempty"`
	// RequireCommandPrefix requires at least one observed CLI command prefix.
	RequireCommandPrefix []string `json:"requireCommandPrefix,omitempty" yaml:"requireCommandPrefix,omitempty"`
	// RequireMCPTool requires at least one observed MCP tool name from tools/call.
	RequireMCPTool []string `json:"requireMCPTool,omitempty" yaml:"requireMCPTool,omitempty"`

	// MinMeaningfulFields enforces a lower bound on meaningful feedback.resultJson fields.
	// Meaningful fields are non-null, non-empty, and not placeholders.
	MinMeaningfulFields int64 `json:"minMeaningfulFields,omitempty" yaml:"minMeaningfulFields,omitempty"`

	// SuspiciousBoilerplate enables conservative boilerplate detection.
	SuspiciousBoilerplate bool `json:"suspiciousBoilerplate,omitempty" yaml:"suspiciousBoilerplate,omitempty"`
	// BoilerplateMCPTools defines MCP tool names treated as generic boilerplate when
	// SuspiciousBoilerplate=true. If omitted, defaults are used.
	BoilerplateMCPTools []string `json:"boilerplateMcpTools,omitempty" yaml:"boilerplateMcpTools,omitempty"`
	// BoilerplateCommandPrefixes defines CLI command prefixes treated as generic boilerplate
	// when SuspiciousBoilerplate=true.
	BoilerplateCommandPrefixes []string `json:"boilerplateCommandPrefixes,omitempty" yaml:"boilerplateCommandPrefixes,omitempty"`
	// MaxMeaningfulFieldsForBoilerplate sets the upper bound of meaningful fields where
	// boilerplate traces still count as suspicious. Default is 1 when unset.
	MaxMeaningfulFieldsForBoilerplate int64 `json:"maxMeaningfulFieldsForBoilerplate,omitempty" yaml:"maxMeaningfulFieldsForBoilerplate,omitempty"`

	// HookCommand runs an external semantic hook command for mission-specific checks.
	// The command receives attempt context via env vars and may emit JSON to stdout.
	HookCommand []string `json:"hookCommand,omitempty" yaml:"hookCommand,omitempty"`
	// HookTimeoutMs limits HookCommand execution time. Default is 10000ms when unset.
	HookTimeoutMs int64 `json:"hookTimeoutMs,omitempty" yaml:"hookTimeoutMs,omitempty"`
}
