package schema

import "encoding/json"

// Version constants for v1 artifacts/traces.
const (
	ArtifactSchemaV1 = 1
	TraceSchemaV1    = 1
)

// RunJSONV1 is written to: .zcl/runs/<runId>/run.json
type RunJSONV1 struct {
	SchemaVersion int    `json:"schemaVersion"`
	RunID         string `json:"runId"`
	SuiteID       string `json:"suiteId"`
	CreatedAt     string `json:"createdAt"` // RFC3339 UTC (use consistent precision)
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
	CreatedAt     string          `json:"createdAt"` // RFC3339 UTC (use consistent precision)
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

	OK *bool `json:"ok,omitempty"` // copied from feedback when present

	Metrics AttemptMetricsV1 `json:"metrics"`
}

type AttemptMetricsV1 struct {
	ToolCallsTotal int64            `json:"toolCallsTotal"`
	FailuresTotal  int64            `json:"failuresTotal"`
	FailuresByCode map[string]int64 `json:"failuresByCode,omitempty"`
	RetriesTotal   int64            `json:"retriesTotal"`
	TimeoutsTotal  int64            `json:"timeoutsTotal"`
	WallTimeMs     int64            `json:"wallTimeMs"`

	OutBytesTotal int64 `json:"outBytesTotal"`
	ErrBytesTotal int64 `json:"errBytesTotal"`

	OutPreviewTruncations int64 `json:"outPreviewTruncations"`
	ErrPreviewTruncations int64 `json:"errPreviewTruncations"`
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
