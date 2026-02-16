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
	TimeoutMs int64  `json:"timeoutMs,omitempty" yaml:"timeoutMs,omitempty"`
	Mode      string `json:"mode,omitempty" yaml:"mode,omitempty"` // discovery|ci
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
}

type ResultExpectsV1 struct {
	// Type is "string" or "json".
	Type string `json:"type" yaml:"type"`

	// Pattern is an RE2 regex applied to feedback.result when Type=="string".
	Pattern string `json:"pattern,omitempty" yaml:"pattern,omitempty"`

	// Equals is an exact match applied to feedback.result when Type=="string".
	Equals string `json:"equals,omitempty" yaml:"equals,omitempty"`
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
	// Example: ["surfwright"] ensures the attempt actually exercised SurfWright.
	RequireCommandPrefix []string `json:"requireCommandPrefix,omitempty" yaml:"requireCommandPrefix,omitempty"`
}
