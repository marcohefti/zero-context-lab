package schema

// RunnerRefJSONV1 is written to: .zcl/runs/<runId>/attempts/<attemptId>/runner.ref.json
type RunnerRefJSONV1 struct {
	SchemaVersion int    `json:"schemaVersion"`
	Runner        string `json:"runner"` // e.g. "codex"

	RunID     string `json:"runId"`
	SuiteID   string `json:"suiteId"`
	MissionID string `json:"missionId"`
	AttemptID string `json:"attemptId"`
	AgentID   string `json:"agentId,omitempty"`

	RolloutPath string `json:"rolloutPath,omitempty"`
	ThreadID    string `json:"threadId,omitempty"`

	RuntimeID string `json:"runtimeId,omitempty"`
	SessionID string `json:"sessionId,omitempty"`
	Transport string `json:"transport,omitempty"`
}

// RunnerMetricsJSONV1 is written to: .zcl/runs/<runId>/attempts/<attemptId>/runner.metrics.json
type RunnerMetricsJSONV1 struct {
	SchemaVersion int    `json:"schemaVersion"`
	Runner        string `json:"runner"`

	Model string `json:"model,omitempty"`

	TotalTokens           *int64 `json:"totalTokens,omitempty"`
	InputTokens           *int64 `json:"inputTokens,omitempty"`
	OutputTokens          *int64 `json:"outputTokens,omitempty"`
	CachedInputTokens     *int64 `json:"cachedInputTokens,omitempty"`
	ReasoningOutputTokens *int64 `json:"reasoningOutputTokens,omitempty"`
}
