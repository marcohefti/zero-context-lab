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
	ID              string   `json:"id"`
	Kind            string   `json:"kind"` // json|jsonl
	SchemaVersions  []int    `json:"schemaVersions"`
	Required        bool     `json:"required"`
	RequiredInModes []string `json:"requiredInModes,omitempty"`
	PathPattern     string   `json:"pathPattern"`
	RequiredFields  []string `json:"requiredFields"`
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
				RequiredFields: []string{"schemaVersion", "artifactLayoutVersion", "runId", "suiteId", "createdAt"},
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
				ID:             "suite.run.summary.json",
				Kind:           "json",
				SchemaVersions: []int{1},
				Required:       false,
				PathPattern:    ".zcl/runs/<runId>/suite.run.summary.json",
				RequiredFields: []string{"schemaVersion", "runId", "suiteId", "mode", "sessionIsolationRequested", "sessionIsolation", "attempts", "passed", "failed", "createdAt"},
			},
			{
				ID:             "run.report.json",
				Kind:           "json",
				SchemaVersions: []int{1},
				Required:       false,
				PathPattern:    ".zcl/runs/<runId>/run.report.json",
				RequiredFields: []string{"schemaVersion", "target", "runId", "suiteId", "path", "attempts", "aggregate"},
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
				ID:              "tool.calls.jsonl",
				Kind:            "jsonl",
				SchemaVersions:  []int{1},
				Required:        false,
				RequiredInModes: []string{"discovery", "ci"},
				PathPattern:     ".zcl/runs/<runId>/attempts/<attemptId>/tool.calls.jsonl",
				RequiredFields:  []string{},
			},
			{
				ID:              "feedback.json",
				Kind:            "json",
				SchemaVersions:  []int{1},
				Required:        false,
				RequiredInModes: []string{"discovery", "ci"},
				PathPattern:     ".zcl/runs/<runId>/attempts/<attemptId>/feedback.json",
				RequiredFields:  []string{"schemaVersion", "runId", "suiteId", "missionId", "attemptId", "ok", "createdAt"},
			},
			{
				ID:             "notes.jsonl",
				Kind:           "jsonl",
				SchemaVersions: []int{1},
				Required:       false,
				PathPattern:    ".zcl/runs/<runId>/attempts/<attemptId>/notes.jsonl",
				RequiredFields: []string{},
			},
			{
				ID:             "captures.jsonl",
				Kind:           "jsonl",
				SchemaVersions: []int{1},
				Required:       false,
				PathPattern:    ".zcl/runs/<runId>/attempts/<attemptId>/captures.jsonl",
				RequiredFields: []string{},
			},
			{
				ID:             "attempt.report.json",
				Kind:           "json",
				SchemaVersions: []int{1},
				Required:       false,
				PathPattern:    ".zcl/runs/<runId>/attempts/<attemptId>/attempt.report.json",
				RequiredFields: []string{"schemaVersion", "runId", "suiteId", "missionId", "attemptId", "computedAt", "metrics", "artifacts"},
			},
			{
				ID:             "runner.ref.json",
				Kind:           "json",
				SchemaVersions: []int{1},
				Required:       false,
				PathPattern:    ".zcl/runs/<runId>/attempts/<attemptId>/runner.ref.json",
				RequiredFields: []string{"schemaVersion", "runner", "runId", "suiteId", "missionId", "attemptId"},
			},
			{
				ID:             "runner.metrics.json",
				Kind:           "json",
				SchemaVersions: []int{1},
				Required:       false,
				PathPattern:    ".zcl/runs/<runId>/attempts/<attemptId>/runner.metrics.json",
				RequiredFields: []string{"schemaVersion", "runner"},
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
			{
				Stream:         "captures.jsonl",
				SchemaVersions: []int{1},
				RequiredFields: []string{"v", "ts", "runId", "missionId", "attemptId", "tool", "op", "maxBytes"},
			},
		},
		Commands: []Command{
			{
				ID:      "init",
				Usage:   "zcl init [--out-root .zcl] [--config zcl.config.json] [--json]",
				Summary: "Initialize the project output root and write the minimal project config.",
			},
			{
				ID:      "feedback",
				Usage:   "zcl feedback --ok|--fail --result <string>|--result-json <json> [--classification <...>] [--decision-tag <tag>] [--decision-tags <csv>]",
				Summary: "Write the canonical attempt outcome to feedback.json (primary evidence).",
			},
			{
				ID:      "note",
				Usage:   "zcl note [--kind agent|operator|system] --message <string>|--data-json <json>",
				Summary: "Append a bounded/redacted note event to notes.jsonl (secondary evidence).",
			},
			{
				ID:      "report",
				Usage:   "zcl report [--strict] [--json] <attemptDir|runDir>",
				Summary: "Compute attempt.report.json from tool.calls.jsonl + feedback.json.",
			},
			{
				ID:      "validate",
				Usage:   "zcl validate [--strict] [--json] <attemptDir|runDir>",
				Summary: "Validate artifact integrity (schemas, ids, bounds, containment) with typed error codes.",
			},
			{
				ID:      "doctor",
				Usage:   "zcl doctor [--out-root .zcl] [--json]",
				Summary: "Check environment/config sanity (write access, config parse, optional runner availability).",
			},
			{
				ID:      "gc",
				Usage:   "zcl gc [--out-root .zcl] [--max-age-days 30] [--max-total-bytes 0] [--dry-run] [--json]",
				Summary: "Retention cleanup under .zcl/runs (age/size; respects pinned runs).",
			},
			{
				ID:      "pin",
				Usage:   "zcl pin --run-id <runId> --on|--off [--out-root .zcl] [--json]",
				Summary: "Pin/unpin a run (toggles run.json.pinned) so gc will keep it.",
			},
			{
				ID:      "enrich",
				Usage:   "zcl enrich --runner codex --rollout <rollout.jsonl> [<attemptDir>]",
				Summary: "Optional runner enrichment (writes runner.ref.json + runner.metrics.json).",
			},
			{
				ID:      "mcp proxy",
				Usage:   "zcl mcp proxy -- <server-cmd> [args...]",
				Summary: "MCP stdio proxy funnel (records initialize/tools/list/tools/call).",
			},
			{
				ID:      "http proxy",
				Usage:   "zcl http proxy --upstream <url> [--listen 127.0.0.1:0] [--max-requests N] [--json]",
				Summary: "HTTP reverse proxy funnel (records inbound requests/responses as tool=http op=request).",
			},
			{
				ID:      "run",
				Usage:   "zcl run [--capture [--capture-raw] --capture-max-bytes N] -- <cmd> [args...]",
				Summary: "Run a command through the ZCL CLI funnel (default passthrough; bounded trace capture; optional full capture + JSON envelope).",
			},
			{
				ID:      "contract",
				Usage:   "zcl contract --json",
				Summary: "Print the ZCL surface contract (artifact layout + supported schema versions).",
			},
			{
				ID:      "attempt start",
				Usage:   "zcl attempt start --suite <suiteId> --mission <missionId> [--prompt <text>] [--suite-file <path>] [--run-id <runId>] [--agent-id <id>] [--isolation-model process_runner|native_spawn] [--mode discovery|ci] [--timeout-ms N] [--timeout-start attempt_start|first_tool_call] [--blind] [--blind-terms <csv>] [--out-root .zcl] [--retry 1] [--env-file <path>] [--env-format sh|dotenv] [--print-env sh|dotenv] --json",
				Summary: "Allocate a run/attempt directory and print canonical IDs + env for a fresh session attempt.",
			},
			{
				ID:      "attempt finish",
				Usage:   "zcl attempt finish [--strict] [--strict-expect] [--json] [<attemptDir>]",
				Summary: "Write attempt.report.json, then run validate + expect (uses ZCL_OUT_DIR when <attemptDir> is omitted).",
			},
			{
				ID:      "attempt explain",
				Usage:   "zcl attempt explain [--strict] [--json] [--tail N] [<attemptDir>]",
				Summary: "Fast post-mortem view: show ids/outcome, validate/expect status, and a tail of tool.calls.jsonl (uses ZCL_OUT_DIR when <attemptDir> is omitted).",
			},
			{
				ID:      "suite plan",
				Usage:   "zcl suite plan --file <suite.(yaml|yml|json)> [--run-id <runId>] [--mode discovery|ci] [--timeout-ms N] [--timeout-start attempt_start|first_tool_call] [--blind on|off] [--blind-terms <csv>] [--out-root .zcl] --json",
				Summary: "Allocate attempt dirs for every mission in a suite file and print env/pointers per mission (for orchestrators).",
			},
			{
				ID:      "suite run",
				Usage:   "zcl suite run --file <suite.(yaml|yml|json)> [--run-id <runId>] [--mode discovery|ci] [--timeout-ms N] [--timeout-start attempt_start|first_tool_call] [--blind on|off] [--blind-terms <csv>] [--session-isolation auto|process|native] [--parallel N] [--total M] [--out-root .zcl] [--strict] [--strict-expect] [--shim <bin>] [--capture-runner-io] --json -- <runner-cmd> [args...]",
				Summary: "Run a suite with capability-aware isolation selection, wave parallelism, and just-in-time attempt allocation, then finish/validate/expect each attempt.",
			},
			{
				ID:      "replay",
				Usage:   "zcl replay [--execute] [--allow <cmd1,cmd2>] [--allow-all] [--max-steps N] [--stdin] --json <attemptDir>",
				Summary: "Best-effort replay of tool.calls.jsonl to reproduce failures (partial support by tool/op).",
			},
			{
				ID:      "expect",
				Usage:   "zcl expect [--strict] --json <attemptDir|runDir>",
				Summary: "Evaluate suite expectations against feedback.json (JSON output includes failures; exit code indicates pass/fail).",
			},
		},
		Errors: []Error{
			{Code: "ZCL_E_USAGE", Summary: "Invalid CLI usage (missing/invalid flags).", Retryable: false},
			{Code: "ZCL_E_IO", Summary: "Filesystem I/O error while writing artifacts.", Retryable: true},
			{Code: "ZCL_E_MISSING_ARTIFACT", Summary: "Missing required artifact(s) for the requested operation.", Retryable: true},
			{Code: "ZCL_E_MISSING_EVIDENCE", Summary: "Primary evidence is missing/empty (e.g. empty tool.calls.jsonl).", Retryable: true},
			{Code: "ZCL_E_INVALID_JSON", Summary: "Invalid JSON in an artifact file.", Retryable: false},
			{Code: "ZCL_E_INVALID_JSONL", Summary: "Invalid JSONL stream (bad line or empty line in strict mode).", Retryable: false},
			{Code: "ZCL_E_SCHEMA_UNSUPPORTED", Summary: "Unsupported schema version for an artifact/event.", Retryable: false},
			{Code: "ZCL_E_ID_MISMATCH", Summary: "IDs in artifacts/events do not match expected attempt/run IDs.", Retryable: false},
			{Code: "ZCL_E_BOUNDS", Summary: "Captured payload exceeds size bounds.", Retryable: false},
			{Code: "ZCL_E_UNSAFE_EVIDENCE", Summary: "Evidence violates safety policy (for example raw captures in strict CI mode).", Retryable: false},
			{Code: "ZCL_E_CONTRACT", Summary: "Artifact/event violates the ZCL contract shape.", Retryable: false},
			{Code: "ZCL_E_CONTAINMENT", Summary: "Artifact path escapes attempt/run directory (symlink traversal).", Retryable: false},
			{Code: "ZCL_E_SPAWN", Summary: "Failed to spawn or execute a wrapped command in the funnel.", Retryable: true},
			{Code: "ZCL_E_TOOL_FAILED", Summary: "Wrapped tool execution completed with a non-zero outcome.", Retryable: true},
			{Code: "ZCL_E_TIMEOUT", Summary: "Timed out waiting for a tool operation.", Retryable: true},
			{Code: "ZCL_E_CONTAMINATED_PROMPT", Summary: "Blind mode rejected a prompt containing harness terms.", Retryable: false},
			{Code: "ZCL_E_FUNNEL_BYPASS", Summary: "Primary evidence missing/empty despite a final outcome being recorded (funnel bypass suspected).", Retryable: false},
			{Code: "ZCL_E_EXPECTATION_FAILED", Summary: "Suite expectations did not match feedback.json.", Retryable: false},
		},
	}
}
