package contract

type Contract struct {
	Name                  string     `json:"name"`
	Version               string     `json:"version"`
	ArtifactLayoutVersion int        `json:"artifactLayoutVersion"`
	TraceSchemaVersion    int        `json:"traceSchemaVersion"`
	Artifacts             []Artifact `json:"artifacts"`
	Events                []Event    `json:"events"`
	Commands              []Command  `json:"commands"`
	Errors                []Error    `json:"errors"`
}

type Artifact struct {
	ID             string   `json:"id"`
	Kind           string   `json:"kind"` // json|jsonl
	SchemaVersions []int    `json:"schemaVersions"`
	Required       bool     `json:"required"`
	PathPattern    string   `json:"pathPattern"`
	RequiredFields []string `json:"requiredFields"`
}

type Event struct {
	Stream         string   `json:"stream"` // tool.calls.jsonl|notes.jsonl
	SchemaVersions []int    `json:"schemaVersions"`
	RequiredFields []string `json:"requiredFields"`
}

type Command struct {
	ID      string `json:"id"`
	Usage   string `json:"usage"`
	Summary string `json:"summary"`
}

type Error struct {
	Code      string `json:"code"`
	Summary   string `json:"summary"`
	Retryable bool   `json:"retryable"`
}

func Build(version string) Contract {
	return Contract{
		Name:                  "zcl",
		Version:               version,
		ArtifactLayoutVersion: 1,
		TraceSchemaVersion:    1,
		Artifacts: []Artifact{
			{
				ID:             "run.json",
				Kind:           "json",
				SchemaVersions: []int{1},
				Required:       true,
				PathPattern:    ".zcl/runs/<runId>/run.json",
				RequiredFields: []string{"schemaVersion", "runId", "suiteId", "createdAt"},
			},
			{
				ID:             "suite.json",
				Kind:           "json",
				SchemaVersions: []int{1},
				Required:       false,
				PathPattern:    ".zcl/runs/<runId>/suite.json",
				RequiredFields: []string{},
			},
			{
				ID:             "attempt.json",
				Kind:           "json",
				SchemaVersions: []int{1},
				Required:       true,
				PathPattern:    ".zcl/runs/<runId>/attempts/<attemptId>/attempt.json",
				RequiredFields: []string{"schemaVersion", "runId", "suiteId", "missionId", "attemptId", "mode", "startedAt"},
			},
			{
				ID:             "prompt.txt",
				Kind:           "text",
				SchemaVersions: []int{1},
				Required:       false,
				PathPattern:    ".zcl/runs/<runId>/attempts/<attemptId>/prompt.txt",
				RequiredFields: []string{},
			},
			{
				ID:             "feedback.json",
				Kind:           "json",
				SchemaVersions: []int{1},
				Required:       true,
				PathPattern:    ".zcl/runs/<runId>/attempts/<attemptId>/feedback.json",
				RequiredFields: []string{"schemaVersion", "runId", "suiteId", "missionId", "attemptId", "ok", "createdAt"},
			},
			{
				ID:             "attempt.report.json",
				Kind:           "json",
				SchemaVersions: []int{1},
				Required:       false,
				PathPattern:    ".zcl/runs/<runId>/attempts/<attemptId>/attempt.report.json",
				RequiredFields: []string{"schemaVersion", "runId", "suiteId", "missionId", "attemptId", "computedAt", "metrics"},
			},
		},
		Events: []Event{
			{
				Stream:         "tool.calls.jsonl",
				SchemaVersions: []int{1},
				RequiredFields: []string{"v", "ts", "runId", "missionId", "attemptId", "tool", "op", "result", "io"},
			},
			{
				Stream:         "notes.jsonl",
				SchemaVersions: []int{1},
				RequiredFields: []string{"v", "ts", "runId", "missionId", "attemptId", "kind"},
			},
		},
		Commands: []Command{
			{
				ID:      "init",
				Usage:   "zcl init [--out-root .zcl] [--config zcl.config.json] [--json]",
				Summary: "Initialize the project output root and write the minimal project config.",
			},
			{
				ID:      "run",
				Usage:   "zcl run -- <cmd> [args...]",
				Summary: "Run a command through the ZCL CLI funnel (passthrough stdout/stderr, bounded capture for traces).",
			},
			{
				ID:      "contract",
				Usage:   "zcl contract --json",
				Summary: "Print the ZCL surface contract (artifact layout + supported schema versions).",
			},
			{
				ID:      "attempt start",
				Usage:   "zcl attempt start --suite <suiteId> --mission <missionId> [--prompt <text>] [--suite-file <path>] --json",
				Summary: "Allocate a run/attempt directory and print canonical IDs + env for the spawned agent.",
			},
		},
		Errors: []Error{
			{Code: "ZCL_E_USAGE", Summary: "Invalid CLI usage (missing/invalid flags).", Retryable: false},
			{Code: "ZCL_E_IO", Summary: "Filesystem I/O error while writing artifacts.", Retryable: true},
		},
	}
}
