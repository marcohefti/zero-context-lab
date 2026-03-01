package contract

import (
	"github.com/marcohefti/zero-context-lab/internal/campaign"
	"github.com/marcohefti/zero-context-lab/internal/codes"
	"github.com/marcohefti/zero-context-lab/internal/native"
	"github.com/marcohefti/zero-context-lab/internal/runners"
)

type Contract struct {
	Name                  string         `json:"name"`
	Version               string         `json:"version"`
	ArtifactLayoutVersion int            `json:"artifactLayoutVersion"`
	TraceSchemaVersion    int            `json:"traceSchemaVersion"`
	Artifacts             []Artifact     `json:"artifacts"`
	Events                []Event        `json:"events"`
	Commands              []Command      `json:"commands"`
	Errors                []Error        `json:"errors"`
	CampaignSchema        CampaignSchema `json:"campaignSchema,omitempty"`
	RuntimeSchema         RuntimeSchema  `json:"runtimeSchema,omitempty"`
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

type CampaignSchema struct {
	SchemaVersion      int                    `json:"schemaVersion"`
	SpecSchemaPath     string                 `json:"specSchemaPath"`
	TraceProfiles      []string               `json:"traceProfiles"`
	RunnerTypes        []string               `json:"runnerTypes"`
	ToolDriverKinds    []string               `json:"toolDriverKinds"`
	FinalizationModes  []string               `json:"finalizationModes"`
	ResultChannelKinds []string               `json:"resultChannelKinds"`
	Defaults           CampaignSchemaDefaults `json:"defaults"`
	PolicyErrorCodes   []string               `json:"policyErrorCodes"`
	Fields             []CampaignSchemaField  `json:"fields"`
}

type CampaignSchemaDefaults struct {
	PromptMode                  string   `json:"promptMode"`
	ForbiddenPromptTerms        []string `json:"forbiddenPromptTerms"`
	ExamForbiddenPromptTerms    []string `json:"examForbiddenPromptTerms,omitempty"`
	OracleVisibility            string   `json:"oracleVisibility,omitempty"`
	EvaluationMode              string   `json:"evaluationMode,omitempty"`
	EvaluatorKind               string   `json:"evaluatorKind,omitempty"`
	OraclePolicyMode            string   `json:"oraclePolicyMode,omitempty"`
	OracleFormatMismatchPolicy  string   `json:"oracleFormatMismatchPolicy,omitempty"`
	FlowMode                    string   `json:"flowMode"`
	TraceProfile                string   `json:"traceProfile"`
	ToolDriverKind              string   `json:"toolDriverKind"`
	RunnerCwdMode               string   `json:"runnerCwdMode"`
	RunnerCwdRetain             string   `json:"runnerCwdRetain"`
	ModelReasoningPolicy        string   `json:"modelReasoningPolicy"`
	FinalizationMode            string   `json:"finalizationMode"`
	ResultChannelKind           string   `json:"resultChannelKind"`
	ResultChannelPath           string   `json:"resultChannelPath"`
	ResultChannelMarker         string   `json:"resultChannelMarker"`
	ResultMinTurn               int      `json:"resultMinTurn"`
	FreshAgentPerAttempt        bool     `json:"freshAgentPerAttempt"`
	AdapterRequiredOutputFields []string `json:"adapterRequiredOutputFields"`
}

type CampaignSchemaField struct {
	Path        string   `json:"path"`
	Type        string   `json:"type"`
	Required    bool     `json:"required"`
	Enum        []string `json:"enum,omitempty"`
	Default     any      `json:"default,omitempty"`
	Description string   `json:"description"`
}

type RuntimeSchema struct {
	SchemaVersion        int                     `json:"schemaVersion"`
	StrategyChainEnv     string                  `json:"strategyChainEnv"`
	DefaultStrategyChain []string                `json:"defaultStrategyChain"`
	Capabilities         []string                `json:"capabilities"`
	HealthMetrics        []string                `json:"healthMetrics"`
	Strategies           []RuntimeStrategySchema `json:"strategies"`
}

type RuntimeStrategySchema struct {
	ID           string          `json:"id"`
	Description  string          `json:"description"`
	Recommended  bool            `json:"recommended"`
	Capabilities map[string]bool `json:"capabilities"`
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
				ID:             "campaign.state.json",
				Kind:           "json",
				SchemaVersions: []int{1},
				Required:       false,
				PathPattern:    ".zcl/campaigns/<campaignId>/campaign.state.json",
				RequiredFields: []string{"schemaVersion", "campaignId", "suiteId", "updatedAt", "latestRunId", "runs"},
			},
			{
				ID:             "campaign.run.state.json",
				Kind:           "json",
				SchemaVersions: []int{1},
				Required:       false,
				PathPattern:    ".zcl/campaigns/<campaignId>/campaign.run.state.json",
				RequiredFields: []string{"schemaVersion", "campaignId", "runId", "status", "updatedAt", "totalMissions", "missionsCompleted"},
			},
			{
				ID:             "campaign.plan.json",
				Kind:           "json",
				SchemaVersions: []int{1},
				Required:       false,
				PathPattern:    ".zcl/campaigns/<campaignId>/campaign.plan.json",
				RequiredFields: []string{"schemaVersion", "campaignId", "specPath", "missions", "createdAt", "updatedAt"},
			},
			{
				ID:             "campaign.progress.jsonl",
				Kind:           "jsonl",
				SchemaVersions: []int{1},
				Required:       false,
				PathPattern:    ".zcl/campaigns/<campaignId>/campaign.progress.jsonl",
				RequiredFields: []string{},
			},
			{
				ID:             "campaign.report.json",
				Kind:           "json",
				SchemaVersions: []int{1},
				Required:       false,
				PathPattern:    ".zcl/campaigns/<campaignId>/campaign.report.json",
				RequiredFields: []string{"schemaVersion", "campaignId", "runId", "status", "totalMissions", "missionsCompleted", "gatesPassed", "gatesFailed"},
			},
			{
				ID:             "campaign.summary.json",
				Kind:           "json",
				SchemaVersions: []int{1},
				Required:       false,
				PathPattern:    ".zcl/campaigns/<campaignId>/campaign.summary.json",
				RequiredFields: []string{"schemaVersion", "campaignId", "runId", "status", "totalMissions", "missionsCompleted", "gatesPassed", "gatesFailed", "claimedMissionsOk", "verifiedMissionsOk", "mismatchCount", "evidencePaths"},
			},
			{
				ID:             "RESULTS.md",
				Kind:           "text",
				SchemaVersions: []int{1},
				Required:       false,
				PathPattern:    ".zcl/campaigns/<campaignId>/RESULTS.md",
				RequiredFields: []string{},
			},
			{
				ID:             "mission.prompts.json",
				Kind:           "json",
				SchemaVersions: []int{1},
				Required:       false,
				PathPattern:    ".zcl/campaigns/<campaignId>/mission.prompts.json",
				RequiredFields: []string{"schemaVersion", "campaignId", "specPath", "templatePath", "outPath", "createdAt", "prompts"},
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
				ID:             "attempt.env.sh",
				Kind:           "text",
				SchemaVersions: []int{1},
				Required:       false,
				PathPattern:    ".zcl/runs/<runId>/attempts/<attemptId>/attempt.env.sh",
				RequiredFields: []string{},
			},
			{
				ID:             "attempt.runtime.env.json",
				Kind:           "json",
				SchemaVersions: []int{1},
				Required:       false,
				PathPattern:    ".zcl/runs/<runId>/attempts/<attemptId>/attempt.runtime.env.json",
				RequiredFields: []string{"schemaVersion", "runId", "suiteId", "missionId", "attemptId", "createdAt", "runtime", "prompt", "env"},
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
				ID:             "oracle.verdict.json",
				Kind:           "json",
				SchemaVersions: []int{1},
				Required:       false,
				PathPattern:    ".zcl/runs/<runId>/attempts/<attemptId>/oracle.verdict.json",
				RequiredFields: []string{"schemaVersion", "campaignId", "flowId", "missionId", "attemptId", "attemptDir", "oraclePath", "evaluatorKind", "evaluatorCommand", "promptMode", "ok", "executedAt"},
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
				ID:      "update status",
				Usage:   "zcl update status [--cached] [--json]",
				Summary: "Check latest release status (manual update policy; no runtime auto-update).",
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
				Usage:   "zcl validate [--strict] [--semantic] [--semantic-rules <path>] [--json] <attemptDir|runDir>",
				Summary: "Validate artifact integrity and optional semantic mission validity with typed error codes.",
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
				Usage:   "zcl enrich --runner " + runners.CLIUsageValues() + " --rollout <rollout.jsonl> [<attemptDir>]",
				Summary: "Optional runner enrichment (writes runner.ref.json + runner.metrics.json).",
			},
			{
				ID:      "mcp proxy",
				Usage:   "zcl mcp proxy [--max-tool-calls N] [--idle-timeout-ms N] [--shutdown-on-complete] [--sequential] -- <server-cmd> [args...]",
				Summary: "MCP stdio proxy funnel with lifecycle controls (records initialize/tools/list/tools/call).",
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
				ID:      "attempt env",
				Usage:   "zcl attempt env [--format sh|dotenv] [--json] [<attemptDir>]",
				Summary: "Print canonical attempt env (uses ZCL_OUT_DIR when <attemptDir> is omitted).",
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
				ID:      "attempt list",
				Usage:   "zcl attempt list [--out-root .zcl] [--suite <suiteId>] [--mission <missionId>] [--status any|ok|fail|missing_feedback] [--tag <tag>] [--limit N] --json",
				Summary: "List attempts as machine-readable index rows with optional suite/mission/status/tag filters.",
			},
			{
				ID:      "attempt latest",
				Usage:   "zcl attempt latest [--out-root .zcl] [--suite <suiteId>] [--mission <missionId>] [--status any|ok|fail|missing_feedback] [--tag <tag>] --json",
				Summary: "Return the latest attempt row matching filters (or found=false).",
			},
			{
				ID:      "runs list",
				Usage:   "zcl runs list [--out-root .zcl] [--suite <suiteId>] [--status any|ok|fail|missing_feedback] [--limit N] --json",
				Summary: "List run-level machine-readable index rows with aggregate attempt status counts.",
			},
			{
				ID:      "suite plan",
				Usage:   "zcl suite plan --file <suite.(yaml|yml|json)> [--run-id <runId>] [--mode discovery|ci] [--timeout-ms N] [--timeout-start attempt_start|first_tool_call] [--blind on|off] [--blind-terms <csv>] [--out-root .zcl] --json",
				Summary: "Allocate attempt dirs for every mission in a suite file and print env/pointers per mission (for orchestrators).",
			},
			{
				ID:      "suite run",
				Usage:   "zcl suite run --file <suite.(yaml|yml|json)> [--run-id <runId>] [--mode discovery|ci] [--timeout-ms N] [--timeout-start attempt_start|first_tool_call] [--feedback-policy strict|auto_fail] [--finalization-mode strict|auto_fail|auto_from_result_json] [--result-channel none|file_json|stdout_json] [--result-file <attempt-relative-path>] [--result-marker <prefix>] [--result-min-turn N] [--campaign-id <id>] [--campaign-state <path>] [--progress-jsonl <path|->] [--blind on|off] [--blind-terms <csv>] [--session-isolation auto|process|native] [--runtime-strategies <csv>] [--native-model <slug>] [--native-model-reasoning-effort none|minimal|low|medium|high|xhigh] [--native-model-reasoning-policy best_effort|required] [--parallel N] [--total M] [--mission-offset N] [--out-root .zcl] [--strict] [--strict-expect] [--shim <bin>] [--capture-runner-io] --json [-- <runner-cmd> [args...]]",
				Summary: "Run a suite with capability-aware isolation, optional campaign continuity/progress stream, and deterministic finish/validate/expect per attempt.",
			},
			{
				ID:      "campaign run",
				Usage:   "zcl campaign run --spec <campaign.(yaml|yml|json)> [--out-root .zcl] [--missions N] [--mission-offset N] [--json]",
				Summary: "Run a first-class campaign across configured flows with pair/semantic/timeout/artifact gates.",
			},
			{
				ID:      "campaign lint",
				Usage:   "zcl campaign lint --spec <campaign.(yaml|yml|json)> [--out-root .zcl] [--json]",
				Summary: "Validate campaign spec shape (strict unknown-field rejection) and print resolved mission selection/runtime defaults.",
			},
			{
				ID:      "campaign canary",
				Usage:   "zcl campaign canary --spec <campaign.(yaml|yml|json)> [--out-root .zcl] [--missions N] [--mission-offset N] [--json]",
				Summary: "Run a bounded canary mission window before full campaign execution.",
			},
			{
				ID:      "campaign resume",
				Usage:   "zcl campaign resume --campaign-id <id> [--out-root .zcl] [--json]",
				Summary: "Resume remaining missions from campaign.run.state.json continuity.",
			},
			{
				ID:      "campaign status",
				Usage:   "zcl campaign status --campaign-id <id> [--out-root .zcl] [--json]",
				Summary: "Read the latest first-class campaign execution state.",
			},
			{
				ID:      "campaign report",
				Usage:   "zcl campaign report [--campaign-id <id> | --spec <campaign.(yaml|yml|json)>] [--out-root .zcl] [--format json,md] [--allow-invalid] [--force] [--json]",
				Summary: "Export campaign aggregate reports with invalid-run publication guards.",
			},
			{
				ID:      "campaign publish-check",
				Usage:   "zcl campaign publish-check [--campaign-id <id> | --spec <campaign.(yaml|yml|json)>] [--out-root .zcl] [--force] [--json]",
				Summary: "Refuse publish-ready benchmark output unless campaign status is valid (unless forced).",
			},
			{
				ID:      "campaign doctor",
				Usage:   "zcl campaign doctor --spec <campaign.(yaml|yml|json)> [--out-root .zcl] [--json]",
				Summary: "Preflight campaign execution prerequisites (runner commands, script binaries, outRoot write access, lock state).",
			},
			{
				ID:      "mission prompts build",
				Usage:   "zcl mission prompts build --spec <campaign.(yaml|yml|json)> --template <template.txt|md> [--out <path>] [--out-root .zcl] [--json]",
				Summary: "Deterministically materialize mission prompts from campaign spec + template.",
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
			{Code: codes.Usage, Summary: "Invalid CLI usage (missing/invalid flags).", Retryable: false},
			{Code: codes.IO, Summary: "Filesystem I/O error while writing artifacts.", Retryable: true},
			{Code: codes.MissingArtifact, Summary: "Missing required artifact(s) for the requested operation.", Retryable: true},
			{Code: codes.MissingEvidence, Summary: "Primary evidence is missing/empty (e.g. empty tool.calls.jsonl).", Retryable: true},
			{Code: codes.InvalidJSON, Summary: "Invalid JSON in an artifact file.", Retryable: false},
			{Code: codes.InvalidJSONL, Summary: "Invalid JSONL stream (bad line or empty line in strict mode).", Retryable: false},
			{Code: codes.SchemaUnsupported, Summary: "Unsupported schema version for an artifact/event.", Retryable: false},
			{Code: codes.IDMismatch, Summary: "IDs in artifacts/events do not match expected attempt/run IDs.", Retryable: false},
			{Code: codes.Bounds, Summary: "Captured payload exceeds size bounds.", Retryable: false},
			{Code: codes.UnsafeEvidence, Summary: "Evidence violates safety policy (for example raw captures in strict CI mode).", Retryable: false},
			{Code: codes.Contract, Summary: "Artifact/event violates the ZCL contract shape.", Retryable: false},
			{Code: codes.Containment, Summary: "Artifact path escapes attempt/run directory (symlink traversal).", Retryable: false},
			{Code: codes.Spawn, Summary: "Failed to spawn or execute a wrapped command in the funnel.", Retryable: true},
			{Code: codes.ToolFailed, Summary: "Wrapped tool execution completed with a non-zero outcome.", Retryable: true},
			{Code: codes.Timeout, Summary: "Timed out waiting for a tool operation.", Retryable: true},
			{Code: codes.RuntimeStrategyUnsupported, Summary: "Configured runtime strategy ID is not registered.", Retryable: false},
			{Code: codes.RuntimeStrategyUnavailable, Summary: "No runtime strategy in the fallback chain is currently available.", Retryable: true},
			{Code: codes.RuntimeCapabilityUnsupported, Summary: "Selected runtime does not support required capabilities.", Retryable: false},
			{Code: codes.RuntimeCompatibility, Summary: "Runtime protocol/version is below the supported contract.", Retryable: false},
			{Code: codes.RuntimeStartup, Summary: "Failed to start native runtime process.", Retryable: true},
			{Code: codes.RuntimeTransport, Summary: "Native runtime transport I/O failure.", Retryable: true},
			{Code: codes.RuntimeProtocol, Summary: "Native runtime returned an invalid/unsupported protocol response.", Retryable: false},
			{Code: codes.RuntimeTimeout, Summary: "Native runtime request timed out.", Retryable: true},
			{Code: codes.RuntimeStreamDisconnect, Summary: "Native runtime event stream disconnected before completion.", Retryable: true},
			{Code: codes.RuntimeEnvPolicy, Summary: "Native runtime environment policy blocked explicit variables.", Retryable: false},
			{Code: codes.RuntimeAuth, Summary: "Native runtime authentication/authorization failure.", Retryable: false},
			{Code: codes.RuntimeRateLimit, Summary: "Native runtime/provider rate limit exceeded.", Retryable: true},
			{Code: codes.RuntimeListenerFailure, Summary: "Native runtime listener pipeline failed.", Retryable: true},
			{Code: codes.RuntimeCrash, Summary: "Native runtime process crashed before turn completion.", Retryable: true},
			{Code: codes.RuntimeStall, Summary: "Native runtime attempt stalled past deadline without terminal completion.", Retryable: true},
			{Code: codes.MCPMaxToolCalls, Summary: "MCP proxy stopped after configured max tool calls.", Retryable: true},
			{Code: codes.ContaminatedPrompt, Summary: "Blind mode rejected a prompt containing harness terms.", Retryable: false},
			{Code: codes.VersionFloor, Summary: "Installed zcl version does not satisfy required minimum version.", Retryable: false},
			{Code: codes.FunnelBypass, Summary: "Primary evidence missing/empty despite a final outcome being recorded (funnel bypass suspected).", Retryable: false},
			{Code: codes.ExpectationFailed, Summary: "Suite expectations did not match feedback.json.", Retryable: false},
			{Code: codes.Semantic, Summary: "Semantic mission validation failed.", Retryable: false},
			{Code: codes.MissionResultMissing, Summary: "Auto finalization could not find mission result payload on the configured result channel.", Retryable: true},
			{Code: codes.MissionResultInvalid, Summary: "Mission result payload is malformed or does not satisfy required fields.", Retryable: false},
			{Code: codes.MissionResultTurnTooEarly, Summary: "Mission result payload turn is below configured minimum finalizable turn.", Retryable: true},
			{Code: codes.CampaignGateFailed, Summary: "Campaign pair gate failed for one or more missions.", Retryable: false},
			{Code: codes.CampaignFirstMissionGateFailed, Summary: "Campaign first mission canary/pair gate failed.", Retryable: false},
			{Code: campaign.ReasonPromptModePolicy, Summary: "Campaign mission-only prompt policy violation (harness term leakage).", Retryable: false},
			{Code: campaign.ReasonExamPromptPolicy, Summary: "Campaign exam prompt policy violation (oracle contamination leakage).", Retryable: false},
			{Code: campaign.ReasonToolDriverShim, Summary: "Campaign flow with toolDriver.kind=cli_funnel is missing required shims.", Retryable: false},
			{Code: campaign.ReasonToolPolicy, Summary: "Campaign flow tool policy gate detected disallowed tool namespace/prefix usage in trace evidence.", Retryable: false},
			{Code: campaign.ReasonToolPolicyConfig, Summary: "Campaign flow tool policy configuration is invalid.", Retryable: false},
			{Code: campaign.ReasonOracleVisibility, Summary: "Campaign oracleSource host_only visibility policy violation.", Retryable: false},
			{Code: campaign.ReasonOracleEvaluator, Summary: "Campaign oracle evaluator configuration is missing or invalid for exam mode.", Retryable: false},
			{Code: campaign.ReasonOracleEvalFailed, Summary: "Campaign oracle evaluator returned a failing verdict for the attempt.", Retryable: false},
			{Code: campaign.ReasonOracleEvalError, Summary: "Campaign oracle evaluator execution or verdict parsing failed.", Retryable: true},
			{Code: codes.CampaignStateDrift, Summary: "Campaign run-state continuity drift detected (spec mission selection disagrees with persisted run-state).", Retryable: false},
			{Code: codes.CampaignLockTimeout, Summary: "Campaign lock acquisition failed (another campaign run/resume likely owns the lock).", Retryable: true},
		},
		CampaignSchema: CampaignSchema{
			SchemaVersion:      1,
			SpecSchemaPath:     "internal/campaign/campaign.spec.schema.json",
			TraceProfiles:      []string{campaign.TraceProfileNone, campaign.TraceProfileStrictBrowserComp, campaign.TraceProfileMCPRequired},
			RunnerTypes:        []string{campaign.RunnerTypeProcessCmd, campaign.RunnerTypeCodexExec, campaign.RunnerTypeCodexSub, campaign.RunnerTypeClaudeSub, campaign.RunnerTypeCodexAppSrv},
			ToolDriverKinds:    []string{campaign.ToolDriverShell, campaign.ToolDriverCLIFunnel, campaign.ToolDriverMCPProxy, campaign.ToolDriverHTTPProxy},
			FinalizationModes:  []string{campaign.FinalizationModeStrict, campaign.FinalizationModeAutoFail, campaign.FinalizationModeAutoFromResultJSON},
			ResultChannelKinds: []string{campaign.ResultChannelNone, campaign.ResultChannelFileJSON, campaign.ResultChannelStdoutJSON},
			Defaults: CampaignSchemaDefaults{
				PromptMode:                  campaign.PromptModeDefault,
				ForbiddenPromptTerms:        campaign.DefaultMissionOnlyForbiddenTerms(),
				ExamForbiddenPromptTerms:    campaign.DefaultExamForbiddenTerms(),
				OracleVisibility:            campaign.OracleVisibilityWorkspace,
				EvaluationMode:              campaign.EvaluationModeNone,
				EvaluatorKind:               campaign.EvaluatorKindScript,
				OraclePolicyMode:            campaign.OraclePolicyModeStrict,
				OracleFormatMismatchPolicy:  campaign.OracleFormatMismatchFail,
				FlowMode:                    campaign.FlowModeSequence,
				TraceProfile:                campaign.TraceProfileNone,
				ToolDriverKind:              campaign.ToolDriverShell,
				RunnerCwdMode:               campaign.RunnerCwdModeInherit,
				RunnerCwdRetain:             campaign.RunnerCwdRetainNever,
				ModelReasoningPolicy:        campaign.ModelReasoningPolicyBestEffort,
				FinalizationMode:            campaign.FinalizationModeAutoFail,
				ResultChannelKind:           campaign.ResultChannelNone,
				ResultChannelPath:           campaign.DefaultResultChannelPath,
				ResultChannelMarker:         campaign.DefaultResultChannelMarker,
				ResultMinTurn:               campaign.DefaultMinResultTurn,
				FreshAgentPerAttempt:        true,
				AdapterRequiredOutputFields: []string{"attemptDir", "status", "errors"},
			},
			PolicyErrorCodes: []string{
				campaign.ReasonPromptModePolicy,
				campaign.ReasonExamPromptPolicy,
				campaign.ReasonOracleVisibility,
				campaign.ReasonOracleEvaluator,
				campaign.ReasonToolDriverShim,
				campaign.ReasonToolPolicy,
				campaign.ReasonToolPolicyConfig,
				campaign.ReasonGateFailed,
				campaign.ReasonFirstMissionGate,
				campaign.ReasonSemanticFailed,
			},
			Fields: []CampaignSchemaField{
				{
					Path:        "promptMode",
					Type:        "string",
					Required:    false,
					Enum:        []string{campaign.PromptModeDefault, campaign.PromptModeMissionOnly, campaign.PromptModeExam},
					Default:     campaign.PromptModeDefault,
					Description: "Campaign prompt policy: mission_only blocks harness-term leakage; exam enforces split prompt/oracle architecture + host-side oracle evaluator.",
				},
				{
					Path:        "noContext.forbiddenPromptTerms",
					Type:        "string[]",
					Required:    false,
					Default:     campaign.DefaultMissionOnlyForbiddenTerms(),
					Description: "Forbidden mission prompt substrings enforced when promptMode=mission_only or exam (exam defaults target oracle leakage patterns).",
				},
				{
					Path:        "missionSource.promptSource.path",
					Type:        "string",
					Required:    false,
					Description: "Exam mode prompt source directory. Only this content is sent to the agent.",
				},
				{
					Path:        "missionSource.oracleSource.path",
					Type:        "string",
					Required:    false,
					Description: "Exam mode oracle source directory. Files are mapped to missions by basename and never sent to the agent prompt.",
				},
				{
					Path:        "missionSource.oracleSource.visibility",
					Type:        "string",
					Required:    false,
					Enum:        []string{campaign.OracleVisibilityWorkspace, campaign.OracleVisibilityHostOnly},
					Default:     campaign.OracleVisibilityWorkspace,
					Description: "Oracle visibility policy; host_only rejects oracle paths inside the agent-readable workspace root.",
				},
				{
					Path:        "evaluation.mode",
					Type:        "string",
					Required:    false,
					Enum:        []string{campaign.EvaluationModeNone, campaign.EvaluationModeOracle},
					Default:     campaign.EvaluationModeNone,
					Description: "Campaign evaluation mode; exam requires oracle.",
				},
				{
					Path:        "evaluation.evaluator.kind",
					Type:        "string",
					Required:    false,
					Enum:        []string{campaign.EvaluatorKindScript, campaign.EvaluatorKindBuiltin},
					Default:     campaign.EvaluatorKindScript,
					Description: "Host-side evaluator kind for oracle mode.",
				},
				{
					Path:        "evaluation.evaluator.command",
					Type:        "string[]",
					Required:    false,
					Description: "Host-side evaluator argv invoked per attempt in exam mode when evaluator.kind=script.",
				},
				{
					Path:        "evaluation.oraclePolicy.mode",
					Type:        "string",
					Required:    false,
					Enum:        []string{campaign.OraclePolicyModeStrict, campaign.OraclePolicyModeNormalized, campaign.OraclePolicyModeSemantic},
					Default:     campaign.OraclePolicyModeStrict,
					Description: "Campaign oracle grading mode for eq-style comparisons: strict|normalized|semantic.",
				},
				{
					Path:        "evaluation.oraclePolicy.formatMismatch",
					Type:        "string",
					Required:    false,
					Enum:        []string{campaign.OracleFormatMismatchFail, campaign.OracleFormatMismatchWarn, campaign.OracleFormatMismatchIgnore},
					Default:     campaign.OracleFormatMismatchFail,
					Description: "Campaign gate policy for format-only oracle mismatches.",
				},
				{
					Path:        "timeouts.missionEnvelopeMs",
					Type:        "integer",
					Required:    false,
					Description: "Optional watchdog envelope for each mission flow run; used for timeout/continue handling.",
				},
				{
					Path:        "timeouts.watchdogHeartbeatMs",
					Type:        "integer",
					Required:    false,
					Description: "Optional campaign watchdog heartbeat cadence while a mission flow run is in-flight.",
				},
				{
					Path:        "timeouts.watchdogHardKillContinue",
					Type:        "boolean",
					Required:    false,
					Description: "When true, mission envelope expiry marks flow infra_failed and continues the campaign.",
				},
				{
					Path:        "pairGate.traceProfile",
					Type:        "string",
					Required:    false,
					Enum:        []string{campaign.TraceProfileNone, campaign.TraceProfileStrictBrowserComp, campaign.TraceProfileMCPRequired},
					Default:     campaign.TraceProfileNone,
					Description: "Built-in traceability gate profile applied per attempt in pair-gate evaluation.",
				},
				{
					Path:        "flowGate.traceProfile",
					Type:        "string",
					Required:    false,
					Enum:        []string{campaign.TraceProfileNone, campaign.TraceProfileStrictBrowserComp, campaign.TraceProfileMCPRequired},
					Default:     campaign.TraceProfileNone,
					Description: "Alias of pairGate for multi-flow campaign semantics (must match pairGate when both are set).",
				},
				{
					Path:        "flows[].promptSource.path",
					Type:        "string",
					Required:    false,
					Description: "Per-flow mission prompt source override when suiteFile is omitted (exam/default mission-pack modes).",
				},
				{
					Path:        "flows[].promptTemplate.path",
					Type:        "string",
					Required:    false,
					Description: "Flow-level prompt template file path applied at campaign runtime to each mission prompt.",
				},
				{
					Path:        "flows[].promptTemplate.allowRunnerEnvKeys",
					Type:        "string[]",
					Required:    false,
					Description: "Allowlisted runner.env keys exposed to prompt templates as {{runnerEnv.KEY}} tokens.",
				},
				{
					Path:        "flows[].toolPolicy",
					Type:        "object",
					Required:    false,
					Description: "Per-flow hard tool policy with allow/deny namespace/prefix rules and optional alias expansion.",
				},
				{
					Path:        "flows[].runner.toolDriver.kind",
					Type:        "string",
					Required:    false,
					Enum:        []string{campaign.ToolDriverShell, campaign.ToolDriverCLIFunnel, campaign.ToolDriverMCPProxy, campaign.ToolDriverHTTPProxy},
					Default:     campaign.ToolDriverShell,
					Description: "Flow tool routing contract enforced at campaign parse/lint time.",
				},
				{
					Path:        "flows[].runner.toolDriver.shims",
					Type:        "string[]",
					Required:    false,
					Description: "Shim binaries for tool driver funneling. Required (or runner.shims) when promptMode=mission_only or exam with cli_funnel.",
				},
				{
					Path:        "flows[].runner.cwd.mode",
					Type:        "string",
					Required:    false,
					Enum:        []string{campaign.RunnerCwdModeInherit, campaign.RunnerCwdModeTempEmptyPerAttempt},
					Default:     campaign.RunnerCwdModeInherit,
					Description: "Agent thread/start cwd policy; temp_empty_per_attempt creates a fresh empty directory per attempt.",
				},
				{
					Path:        "flows[].runner.cwd.basePath",
					Type:        "string",
					Required:    false,
					Description: "Optional base path used by temp_empty_per_attempt runner cwd policy.",
				},
				{
					Path:        "flows[].runner.cwd.retain",
					Type:        "string",
					Required:    false,
					Enum:        []string{campaign.RunnerCwdRetainNever, campaign.RunnerCwdRetainOnFailure, campaign.RunnerCwdRetainAlways},
					Default:     campaign.RunnerCwdRetainNever,
					Description: "Retention policy for per-attempt temp cwd directories.",
				},
				{
					Path:        "flows[].runner.model",
					Type:        "string",
					Required:    false,
					Description: "Native thread/start model override for codex_app_server flows.",
				},
				{
					Path:        "flows[].runner.modelReasoningEffort",
					Type:        "string",
					Required:    false,
					Enum:        []string{campaign.ModelReasoningEffortNone, campaign.ModelReasoningEffortMinimal, campaign.ModelReasoningEffortLow, campaign.ModelReasoningEffortMedium, campaign.ModelReasoningEffortHigh, campaign.ModelReasoningEffortXHigh},
					Description: "Best-effort reasoning effort hint for native codex thread/start config.",
				},
				{
					Path:        "flows[].runner.modelReasoningPolicy",
					Type:        "string",
					Required:    false,
					Enum:        []string{campaign.ModelReasoningPolicyBestEffort, campaign.ModelReasoningPolicyRequired},
					Default:     campaign.ModelReasoningPolicyBestEffort,
					Description: "Behavior when modelReasoningEffort is unsupported: best_effort (fallback) or required (typed failure).",
				},
				{
					Path:        "flows[].runner.finalization.mode",
					Type:        "string",
					Required:    false,
					Enum:        []string{campaign.FinalizationModeStrict, campaign.FinalizationModeAutoFail, campaign.FinalizationModeAutoFromResultJSON},
					Default:     campaign.FinalizationModeAutoFail,
					Description: "Attempt finalization policy. mission_only and exam require auto_from_result_json.",
				},
				{
					Path:        "flows[].runner.finalization.resultChannel.kind",
					Type:        "string",
					Required:    false,
					Enum:        []string{campaign.ResultChannelNone, campaign.ResultChannelFileJSON, campaign.ResultChannelStdoutJSON},
					Default:     campaign.ResultChannelNone,
					Description: "Mission result source used by auto_from_result_json finalization.",
				},
				{
					Path:        "flows[].runner.finalization.resultChannel.path",
					Type:        "string",
					Required:    false,
					Default:     campaign.DefaultResultChannelPath,
					Description: "Attempt-relative file path used when resultChannel.kind=file_json.",
				},
				{
					Path:        "flows[].runner.finalization.resultChannel.marker",
					Type:        "string",
					Required:    false,
					Default:     campaign.DefaultResultChannelMarker,
					Description: "Stdout marker prefix used when resultChannel.kind=stdout_json.",
				},
				{
					Path:        "flows[].runner.finalization.minResultTurn",
					Type:        "integer",
					Required:    false,
					Default:     campaign.DefaultMinResultTurn,
					Description: "Minimum mission result payload turn accepted for finalization (supports 3-turn feedback loops).",
				},
			},
		},
		RuntimeSchema: RuntimeSchema{
			SchemaVersion:        1,
			StrategyChainEnv:     "ZCL_RUNTIME_STRATEGIES",
			DefaultStrategyChain: []string{"codex_app_server"},
			Capabilities: []string{
				string(native.CapabilityThreadStart),
				string(native.CapabilityTurnSteer),
				string(native.CapabilityInterrupt),
				string(native.CapabilityEventStream),
				string(native.CapabilityParallelSessions),
			},
			HealthMetrics: native.CanonicalHealthMetrics(),
			Strategies:    runtimeContractStrategies(),
		},
	}
}

func runtimeContractStrategies() []RuntimeStrategySchema {
	descriptors := native.BuiltinStrategyCatalog()
	out := make([]RuntimeStrategySchema, 0, len(descriptors))
	for _, d := range descriptors {
		out = append(out, RuntimeStrategySchema{
			ID:          string(d.ID),
			Description: d.Description,
			Recommended: d.Recommended,
			Capabilities: map[string]bool{
				string(native.CapabilityThreadStart):      d.Capabilities.SupportsThreadStart,
				string(native.CapabilityTurnSteer):        d.Capabilities.SupportsTurnSteer,
				string(native.CapabilityInterrupt):        d.Capabilities.SupportsInterrupt,
				string(native.CapabilityEventStream):      d.Capabilities.SupportsEventStream,
				string(native.CapabilityParallelSessions): d.Capabilities.SupportsParallelSessions,
			},
		})
	}
	return out
}
