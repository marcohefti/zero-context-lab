package schema

const (
	// AttemptRuntimeEnvFileNameV1 is written to: .zcl/runs/<runId>/attempts/<attemptId>/attempt.runtime.env.json
	AttemptRuntimeEnvFileNameV1 = "attempt.runtime.env.json"
)

type AttemptRuntimeEnvJSONV1 struct {
	SchemaVersion int    `json:"schemaVersion"`
	RunID         string `json:"runId"`
	SuiteID       string `json:"suiteId"`
	MissionID     string `json:"missionId"`
	AttemptID     string `json:"attemptId"`
	AgentID       string `json:"agentId,omitempty"`

	CreatedAt string `json:"createdAt"`

	Runtime AttemptRuntimeContextV1     `json:"runtime"`
	Prompt  AttemptPromptMetadataV1     `json:"prompt"`
	Env     AttemptRuntimeEnvironmentV1 `json:"env"`
}

type AttemptRuntimeContextV1 struct {
	IsolationModel string `json:"isolationModel,omitempty"`
	ToolDriverKind string `json:"toolDriverKind,omitempty"`
	RuntimeID      string `json:"runtimeId,omitempty"`
	NativeMode     bool   `json:"nativeMode"`
}

type AttemptPromptMetadataV1 struct {
	SourceKind   string `json:"sourceKind"`
	SourcePath   string `json:"sourcePath,omitempty"`
	TemplatePath string `json:"templatePath,omitempty"`
	SHA256       string `json:"sha256"`
	Bytes        int64  `json:"bytes"`
}

type AttemptRuntimeEnvironmentV1 struct {
	// Explicit are env vars explicitly materialized by suite/campaign orchestration.
	// Values are redacted with native env policy redaction rules.
	Explicit map[string]string `json:"explicit,omitempty"`
	// EffectiveKeys are the runtime-effective environment variable names after merge/filter.
	EffectiveKeys []string `json:"effectiveKeys,omitempty"`
	// BlockedKeys are explicit/policy-blocked env names (native runtime only).
	BlockedKeys []string `json:"blockedKeys,omitempty"`
}
