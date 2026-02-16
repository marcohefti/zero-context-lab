package schema

import "encoding/json"

// CaptureEventV1 is one line in: captures.jsonl
// It is an index of large outputs captured to files (secondary evidence).
type CaptureEventV1 struct {
	V  int    `json:"v"`  // 1
	TS string `json:"ts"` // RFC3339 UTC

	RunID     string `json:"runId"`
	SuiteID   string `json:"suiteId,omitempty"`
	MissionID string `json:"missionId"`
	AttemptID string `json:"attemptId"`
	AgentID   string `json:"agentId,omitempty"`

	Tool string `json:"tool"`
	Op   string `json:"op"`

	Input json.RawMessage `json:"input,omitempty"` // bounded/canonical JSON when possible

	StdoutPath string `json:"stdoutPath,omitempty"`
	StderrPath string `json:"stderrPath,omitempty"`

	StdoutBytes  int64  `json:"stdoutBytes,omitempty"`
	StderrBytes  int64  `json:"stderrBytes,omitempty"`
	StdoutSHA256 string `json:"stdoutSha256,omitempty"`
	StderrSHA256 string `json:"stderrSha256,omitempty"`

	StdoutTruncated bool `json:"stdoutTruncated,omitempty"`
	StderrTruncated bool `json:"stderrTruncated,omitempty"`

	MaxBytes int64 `json:"maxBytes"`
}
