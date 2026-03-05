package validate

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/marcohefti/zero-context-lab/internal/kernel/ids"
	"github.com/marcohefti/zero-context-lab/internal/kernel/schema"
	"github.com/marcohefti/zero-context-lab/internal/kernel/store"
)

type Finding struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Path    string `json:"path,omitempty"`
}

type Result struct {
	OK       bool      `json:"ok"`
	Strict   bool      `json:"strict"`
	Target   string    `json:"target"` // attempt|run
	Path     string    `json:"path"`
	Errors   []Finding `json:"errors,omitempty"`
	Warnings []Finding `json:"warnings,omitempty"`
}

func ValidatePath(targetDir string, strict bool) (Result, error) {
	abs, err := filepath.Abs(targetDir)
	if err != nil {
		return Result{OK: false, Strict: strict, Target: "unknown", Path: targetDir, Errors: []Finding{{Code: "ZCL_E_IO", Message: err.Error(), Path: targetDir}}}, nil
	}
	info, err := os.Stat(abs)
	if err != nil {
		return Result{OK: false, Strict: strict, Target: "unknown", Path: abs, Errors: []Finding{{Code: "ZCL_E_IO", Message: err.Error(), Path: abs}}}, nil
	}
	if !info.IsDir() {
		return Result{OK: false, Strict: strict, Target: "unknown", Path: abs, Errors: []Finding{{Code: "ZCL_E_USAGE", Message: "target must be a directory", Path: abs}}}, nil
	}

	// Determine type by presence of attempt.json vs run.json.
	if _, err := os.Stat(filepath.Join(abs, "attempt.json")); err == nil {
		return validateAttempt(abs, strict), nil
	}
	if _, err := os.Stat(filepath.Join(abs, "run.json")); err == nil {
		return validateRun(abs, strict), nil
	}
	return Result{OK: false, Strict: strict, Target: "unknown", Path: abs, Errors: []Finding{{Code: "ZCL_E_USAGE", Message: "target does not look like an attemptDir or runDir", Path: abs}}}, nil
}

func validateRun(runDir string, strict bool) Result {
	res := Result{OK: true, Strict: strict, Target: "run", Path: runDir}

	run, ok := loadAndValidateRunJSON(runDir, strict, &res)
	if !ok {
		return finalize(res)
	}

	validateOptionalRunArtifacts(runDir, run, strict, &res)
	validateRunAttempts(runDir, strict, &res)

	return finalize(res)
}

func loadAndValidateRunJSON(runDir string, strict bool, res *Result) (schema.RunJSONV1, bool) {
	runJSONPath := filepath.Join(runDir, "run.json")
	if !requireFile(runJSONPath, true, true, res) {
		return schema.RunJSONV1{}, false
	}
	if !requireContained(runDir, runJSONPath, res) {
		return schema.RunJSONV1{}, false
	}
	raw, err := os.ReadFile(runJSONPath)
	if err != nil {
		addErr(res, "ZCL_E_IO", err.Error(), runJSONPath)
		return schema.RunJSONV1{}, false
	}
	var run schema.RunJSONV1
	if err := json.Unmarshal(raw, &run); err != nil {
		addErr(res, "ZCL_E_INVALID_JSON", "run.json is not valid json", runJSONPath)
		return schema.RunJSONV1{}, false
	}
	if run.SchemaVersion != schema.RunSchemaV1 {
		addErr(res, "ZCL_E_SCHEMA_UNSUPPORTED", "unsupported run.json schemaVersion", runJSONPath)
		return schema.RunJSONV1{}, false
	}
	if run.ArtifactLayoutVersion == 0 {
		addErr(res, "ZCL_E_CONTRACT", "artifactLayoutVersion is missing", runJSONPath)
		return schema.RunJSONV1{}, false
	}
	if run.ArtifactLayoutVersion != schema.ArtifactLayoutVersionV1 {
		addErr(res, "ZCL_E_SCHEMA_UNSUPPORTED", "unsupported artifactLayoutVersion", runJSONPath)
		return schema.RunJSONV1{}, false
	}
	if strings.TrimSpace(run.RunID) == "" || !ids.IsValidRunID(run.RunID) {
		addErr(res, "ZCL_E_CONTRACT", "runId is missing/invalid", runJSONPath)
		return schema.RunJSONV1{}, false
	}
	if strings.TrimSpace(run.SuiteID) == "" {
		addErr(res, "ZCL_E_CONTRACT", "suiteId is missing", runJSONPath)
		return schema.RunJSONV1{}, false
	}
	if strings.TrimSpace(run.CreatedAt) == "" {
		addErr(res, "ZCL_E_CONTRACT", "createdAt is missing", runJSONPath)
		return schema.RunJSONV1{}, false
	}
	if !validateRFC3339(run.CreatedAt, "createdAt is not RFC3339", strict, res, runJSONPath) {
		return schema.RunJSONV1{}, false
	}
	if base := filepath.Base(runDir); run.RunID != base {
		addErr(res, "ZCL_E_ID_MISMATCH", "runId does not match directory name", runJSONPath)
	}
	return run, true
}

func validateRFC3339(value string, message string, strict bool, res *Result, path string) bool {
	if _, err := time.Parse(time.RFC3339Nano, value); err == nil {
		return true
	}
	if _, err := time.Parse(time.RFC3339, value); err == nil {
		return true
	}
	if strict {
		addErr(res, "ZCL_E_CONTRACT", message, path)
		return false
	}
	addWarn(res, "ZCL_W_CONTRACT", message, path)
	return true
}

func validateOptionalRunArtifacts(runDir string, run schema.RunJSONV1, strict bool, res *Result) {
	suiteRunSummaryPath := filepath.Join(runDir, "suite.run.summary.json")
	if _, err := os.Stat(suiteRunSummaryPath); err == nil && requireContained(runDir, suiteRunSummaryPath, res) {
		validateSuiteRunSummary(suiteRunSummaryPath, run, strict, res)
	}
	runReportPath := filepath.Join(runDir, "run.report.json")
	if _, err := os.Stat(runReportPath); err == nil && requireContained(runDir, runReportPath, res) {
		validateRunReport(runReportPath, runDir, run, strict, res)
	}
}

func validateRunAttempts(runDir string, strict bool, res *Result) {
	attemptsDir := filepath.Join(runDir, "attempts")
	entries, err := os.ReadDir(attemptsDir)
	if err != nil {
		if strict && os.IsNotExist(err) {
			addErr(res, "ZCL_E_MISSING_ARTIFACT", "missing attempts directory", attemptsDir)
			return
		}
		if os.IsNotExist(err) {
			addWarn(res, "ZCL_W_MISSING_ARTIFACT", "missing attempts directory", attemptsDir)
		}
		return
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		attemptDir := filepath.Join(attemptsDir, e.Name())
		ar := validateAttempt(attemptDir, strict)
		if !ar.OK {
			res.OK = false
		}
		res.Errors = append(res.Errors, ar.Errors...)
		res.Warnings = append(res.Warnings, ar.Warnings...)
	}
}

func validateAttempt(attemptDir string, strict bool) Result {
	res := Result{OK: true, Strict: strict, Target: "attempt", Path: attemptDir}
	attempt, enforce, ok := loadAndValidateAttemptHeader(attemptDir, strict, &res)
	if !ok {
		return finalize(res)
	}
	if !validateAttemptPrimaryArtifacts(attemptDir, attempt, enforce, &res) {
		return finalize(res)
	}
	validateAttemptOptionalArtifacts(attemptDir, attempt, enforce, &res)
	if !validateAttemptReportArtifact(attemptDir, attempt, enforce, &res) {
		return finalize(res)
	}
	return finalize(res)
}

func loadAndValidateAttemptHeader(attemptDir string, strict bool, res *Result) (schema.AttemptJSONV1, bool, bool) {
	attemptJSONPath := filepath.Join(attemptDir, "attempt.json")
	if !requireFile(attemptJSONPath, true, true, res) {
		return schema.AttemptJSONV1{}, false, false
	}
	if !requireContained(attemptDir, attemptJSONPath, res) {
		return schema.AttemptJSONV1{}, false, false
	}
	raw, err := os.ReadFile(attemptJSONPath)
	if err != nil {
		addErr(res, "ZCL_E_IO", err.Error(), attemptJSONPath)
		return schema.AttemptJSONV1{}, false, false
	}
	var attempt schema.AttemptJSONV1
	if err := json.Unmarshal(raw, &attempt); err != nil {
		addErr(res, "ZCL_E_INVALID_JSON", "attempt.json is not valid json", attemptJSONPath)
		return schema.AttemptJSONV1{}, false, false
	}
	if !validateAttemptContract(attemptDir, attempt, strict, attemptJSONPath, res) {
		return schema.AttemptJSONV1{}, false, false
	}
	return attempt, strict || attempt.Mode == "ci", true
}

func validateAttemptContract(attemptDir string, attempt schema.AttemptJSONV1, strict bool, path string, res *Result) bool {
	if !validateAttemptSchemaAndIDs(attempt, path, res) {
		return false
	}
	if !validateAttemptModeAndTiming(attempt, strict, path, res) {
		return false
	}
	if !validateAttemptBlindTerms(attempt.BlindTerms, path, res) {
		return false
	}
	if !validateNativeResultProvenance(attempt.NativeResult, strict, res, path, "attempt.json") {
		return false
	}
	if !validateAttemptCanonicalIDs(attempt, strict, path, res) {
		return false
	}
	validateAttemptDirMatch(attemptDir, attempt.AttemptID, path, res)
	return true
}

func validateAttemptSchemaAndIDs(attempt schema.AttemptJSONV1, path string, res *Result) bool {
	if attempt.SchemaVersion != schema.AttemptSchemaV1 {
		addErr(res, "ZCL_E_SCHEMA_UNSUPPORTED", "unsupported attempt.json schemaVersion", path)
		return false
	}
	if strings.TrimSpace(attempt.RunID) == "" || !ids.IsValidRunID(attempt.RunID) {
		addErr(res, "ZCL_E_CONTRACT", "attempt runId is missing/invalid", path)
		return false
	}
	if strings.TrimSpace(attempt.SuiteID) == "" {
		addErr(res, "ZCL_E_CONTRACT", "attempt suiteId is missing", path)
		return false
	}
	if strings.TrimSpace(attempt.MissionID) == "" {
		addErr(res, "ZCL_E_CONTRACT", "attempt missionId is missing", path)
		return false
	}
	if strings.TrimSpace(attempt.AttemptID) == "" {
		addErr(res, "ZCL_E_CONTRACT", "attempt attemptId is missing", path)
		return false
	}
	return true
}

func validateAttemptModeAndTiming(attempt schema.AttemptJSONV1, strict bool, path string, res *Result) bool {
	if attempt.Mode != "discovery" && attempt.Mode != "ci" {
		addErr(res, "ZCL_E_CONTRACT", "attempt mode is invalid (expected discovery|ci)", path)
		return false
	}
	if !schema.IsValidIsolationModelV1(strings.TrimSpace(attempt.IsolationModel)) {
		addErr(res, "ZCL_E_CONTRACT", "attempt isolationModel is invalid (expected process_runner|native_spawn)", path)
		return false
	}
	if strings.TrimSpace(attempt.StartedAt) == "" {
		addErr(res, "ZCL_E_CONTRACT", "attempt startedAt is missing", path)
		return false
	}
	if !validateRFC3339(attempt.StartedAt, "attempt startedAt is not RFC3339", strict, res, path) {
		return false
	}
	if !schema.IsValidTimeoutStartV1(attempt.TimeoutStart) {
		addErr(res, "ZCL_E_CONTRACT", "attempt timeoutStart is invalid", path)
		return false
	}
	if strings.TrimSpace(attempt.TimeoutStartedAt) != "" &&
		!validateRFC3339(attempt.TimeoutStartedAt, "attempt timeoutStartedAt is not RFC3339", strict, res, path) {
		return false
	}
	return true
}

func validateAttemptBlindTerms(terms []string, path string, res *Result) bool {
	for _, t := range terms {
		if strings.TrimSpace(t) != "" {
			continue
		}
		addErr(res, "ZCL_E_CONTRACT", "attempt blindTerms contains empty entry", path)
		return false
	}
	return true
}

func validateAttemptCanonicalIDs(attempt schema.AttemptJSONV1, strict bool, path string, res *Result) bool {
	if !strict {
		return true
	}
	if ids.SanitizeComponent(attempt.SuiteID) != attempt.SuiteID {
		addErr(res, "ZCL_E_CONTRACT", "attempt suiteId is not canonicalized", path)
		return false
	}
	if ids.SanitizeComponent(attempt.MissionID) != attempt.MissionID {
		addErr(res, "ZCL_E_CONTRACT", "attempt missionId is not canonicalized", path)
		return false
	}
	return true
}

func validateAttemptDirMatch(attemptDir, attemptID, path string, res *Result) {
	if base := filepath.Base(attemptDir); attemptID != base {
		addErr(res, "ZCL_E_ID_MISMATCH", "attemptId does not match directory name", path)
	}
}

func validateAttemptPrimaryArtifacts(attemptDir string, attempt schema.AttemptJSONV1, enforce bool, res *Result) bool {
	tracePath := filepath.Join(attemptDir, "tool.calls.jsonl")
	feedbackPath := filepath.Join(attemptDir, "feedback.json")
	if !validateFunnelBypass(attemptDir, tracePath, feedbackPath, enforce, res) {
		return false
	}
	requireFile(tracePath, true, enforce, res)
	requireFile(feedbackPath, true, enforce, res)
	if _, err := os.Stat(tracePath); err == nil && requireContained(attemptDir, tracePath, res) {
		validateTrace(tracePath, attemptDir, attempt, enforce, res)
	}
	if _, err := os.Stat(feedbackPath); err == nil && requireContained(attemptDir, feedbackPath, res) {
		validateFeedback(feedbackPath, attempt, enforce, res)
	}
	return true
}

func validateFunnelBypass(attemptDir string, tracePath string, feedbackPath string, enforce bool, res *Result) bool {
	if _, err := os.Stat(feedbackPath); err != nil {
		return true
	}
	if _, err := os.Stat(tracePath); err != nil {
		if os.IsNotExist(err) {
			if enforce {
				addErr(res, "ZCL_E_FUNNEL_BYPASS", "feedback.json exists but tool.calls.jsonl is missing", attemptDir)
				return false
			}
			addWarn(res, "ZCL_W_FUNNEL_BYPASS_SUSPECTED", "feedback.json exists but tool.calls.jsonl is missing", attemptDir)
			return true
		}
		addErr(res, "ZCL_E_IO", err.Error(), tracePath)
		return false
	}
	nonEmpty, err := store.JSONLHasNonEmptyLine(tracePath)
	if err != nil {
		addErr(res, "ZCL_E_IO", err.Error(), tracePath)
		return false
	}
	if nonEmpty {
		return true
	}
	if enforce {
		addErr(res, "ZCL_E_FUNNEL_BYPASS", "feedback.json exists but tool.calls.jsonl is empty", tracePath)
		return false
	}
	addWarn(res, "ZCL_W_FUNNEL_BYPASS_SUSPECTED", "feedback.json exists but tool.calls.jsonl is empty", tracePath)
	return true
}

func validateAttemptOptionalArtifacts(attemptDir string, attempt schema.AttemptJSONV1, enforce bool, res *Result) {
	notesPath := filepath.Join(attemptDir, "notes.jsonl")
	if _, err := os.Stat(notesPath); err == nil && requireContained(attemptDir, notesPath, res) {
		validateNotes(notesPath, attempt, enforce, res)
	}
	capturesPath := filepath.Join(attemptDir, "captures.jsonl")
	if _, err := os.Stat(capturesPath); err == nil && requireContained(attemptDir, capturesPath, res) {
		validateCaptures(capturesPath, attemptDir, attempt, enforce, res)
	}
}

func validateAttemptReportArtifact(attemptDir string, attempt schema.AttemptJSONV1, enforce bool, res *Result) bool {
	reportPath := filepath.Join(attemptDir, "attempt.report.json")
	if _, err := os.Stat(reportPath); err != nil {
		return true
	}
	if !requireContained(attemptDir, reportPath, res) {
		return false
	}
	raw, err := os.ReadFile(reportPath)
	if err != nil {
		addErr(res, "ZCL_E_IO", err.Error(), reportPath)
		return false
	}
	var rep schema.AttemptReportJSONV1
	if err := json.Unmarshal(raw, &rep); err != nil {
		addErr(res, "ZCL_E_INVALID_JSON", "attempt.report.json is not valid json", reportPath)
		return false
	}
	if !validateAttemptReportContract(rep, attempt, enforce, reportPath, res) {
		return false
	}
	return validateNativeResultProvenance(rep.NativeResult, enforce, res, reportPath, "attempt.report.json")
}

func validateAttemptReportContract(rep schema.AttemptReportJSONV1, attempt schema.AttemptJSONV1, enforce bool, reportPath string, res *Result) bool {
	if rep.SchemaVersion != schema.AttemptReportSchemaV1 {
		addErr(res, "ZCL_E_SCHEMA_UNSUPPORTED", "unsupported attempt.report.json schemaVersion", reportPath)
		return false
	}
	if rep.RunID != attempt.RunID || rep.AttemptID != attempt.AttemptID || rep.MissionID != attempt.MissionID {
		addErr(res, "ZCL_E_ID_MISMATCH", "attempt.report.json ids do not match attempt.json", reportPath)
	}
	if rep.Result != "" && rep.ResultJSON != nil {
		addErr(res, "ZCL_E_CONTRACT", "attempt.report.json must set only one of result or resultJson", reportPath)
		return false
	}
	if strings.TrimSpace(rep.Classification) != "" && !schema.IsValidClassificationV1(rep.Classification) {
		addErr(res, "ZCL_E_CONTRACT", "attempt.report.json classification is invalid", reportPath)
		return false
	}
	for _, tag := range rep.DecisionTags {
		if schema.IsValidDecisionTagV1(tag) {
			continue
		}
		addErr(res, "ZCL_E_CONTRACT", "attempt.report.json decisionTags contains invalid tag", reportPath)
		return false
	}
	if hasAttemptReportArtifacts(rep) {
		return true
	}
	if enforce {
		addErr(res, "ZCL_E_CONTRACT", "attempt.report.json artifacts are missing required pointers", reportPath)
		return false
	}
	addWarn(res, "ZCL_W_CONTRACT", "attempt.report.json artifacts missing pointers", reportPath)
	return true
}

func hasAttemptReportArtifacts(rep schema.AttemptReportJSONV1) bool {
	return strings.TrimSpace(rep.Artifacts.AttemptJSON) != "" &&
		strings.TrimSpace(rep.Artifacts.TraceJSONL) != "" &&
		strings.TrimSpace(rep.Artifacts.FeedbackJSON) != ""
}

func validateNativeResultProvenance(v *schema.NativeResultProvenanceV1, strict bool, res *Result, path string, label string) bool {
	if v == nil {
		return true
	}
	if src := strings.TrimSpace(v.ResultSource); src != "" && !schema.IsValidNativeResultSourceV1(src) {
		if strict {
			addErr(res, "ZCL_E_CONTRACT", label+" nativeResult.resultSource is invalid", path)
			return false
		}
		addWarn(res, "ZCL_W_CONTRACT", label+" nativeResult.resultSource is invalid", path)
	}
	if v.CommentaryMessagesObserved < 0 {
		addErr(res, "ZCL_E_CONTRACT", label+" nativeResult.commentaryMessagesObserved must be >= 0", path)
		return false
	}
	if v.ReasoningItemsObserved < 0 {
		addErr(res, "ZCL_E_CONTRACT", label+" nativeResult.reasoningItemsObserved must be >= 0", path)
		return false
	}
	if strings.TrimSpace(v.ResultSource) == schema.NativeResultSourcePhaseFinalAnswerV1 && !v.PhaseAware {
		if strict {
			addErr(res, "ZCL_E_CONTRACT", label+" nativeResult.phaseAware must be true when resultSource=phase_final_answer", path)
			return false
		}
		addWarn(res, "ZCL_W_CONTRACT", label+" nativeResult.phaseAware should be true when resultSource=phase_final_answer", path)
	}
	return true
}

func validateTrace(path string, attemptDir string, attempt schema.AttemptJSONV1, strict bool, res *Result) {
	f, err := os.Open(path)
	if err != nil {
		addErr(res, "ZCL_E_IO", err.Error(), path)
		return
	}
	defer func() { _ = f.Close() }()

	count, ok := scanAndValidateTraceEvents(f, path, attemptDir, attempt, strict, res)
	if !ok {
		return
	}
	if strict && count == 0 {
		addErr(res, "ZCL_E_MISSING_EVIDENCE", "tool.calls.jsonl is empty", path)
	}
}

func scanAndValidateTraceEvents(f *os.File, path, attemptDir string, attempt schema.AttemptJSONV1, strict bool, res *Result) (int, bool) {
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	count := 0
	for sc.Scan() {
		line := sc.Bytes()
		if !validateNonEmptyJSONLLine(line, strict, "tool.calls.jsonl", path, res) {
			return 0, false
		}
		if len(bytesTrim(line)) == 0 {
			continue
		}
		if !validateTraceLine(line, path, attemptDir, attempt, strict, res) {
			return 0, false
		}
		count++
	}
	if err := sc.Err(); err != nil {
		addErr(res, "ZCL_E_IO", err.Error(), path)
		return 0, false
	}
	return count, true
}

func validateNonEmptyJSONLLine(line []byte, strict bool, artifact string, path string, res *Result) bool {
	if len(bytesTrim(line)) != 0 {
		return true
	}
	if strict {
		addErr(res, "ZCL_E_INVALID_JSONL", "empty line in "+artifact, path)
		return false
	}
	return true
}

func validateTraceLine(line []byte, path, attemptDir string, attempt schema.AttemptJSONV1, strict bool, res *Result) bool {
	ev, ok := parseTraceEvent(line, path, res)
	if !ok {
		return false
	}
	if !validateTraceEnvelope(ev, attempt, strict, path, res) {
		return false
	}
	if !validateTraceInputAndIO(ev, strict, path, res) {
		return false
	}
	return validateTraceEnrichmentFields(ev, attemptDir, strict, path, res)
}

func parseTraceEvent(line []byte, path string, res *Result) (schema.TraceEventV1, bool) {
	var ev schema.TraceEventV1
	if err := json.Unmarshal(line, &ev); err != nil {
		addErr(res, "ZCL_E_INVALID_JSONL", "invalid jsonl line in tool.calls.jsonl", path)
		return schema.TraceEventV1{}, false
	}
	return ev, true
}

func validateTraceEnvelope(ev schema.TraceEventV1, attempt schema.AttemptJSONV1, strict bool, path string, res *Result) bool {
	if ev.V != schema.TraceSchemaV1 {
		addErr(res, "ZCL_E_SCHEMA_UNSUPPORTED", "unsupported trace event version", path)
		return false
	}
	if strings.TrimSpace(ev.TS) == "" {
		addErr(res, "ZCL_E_CONTRACT", "trace ts is missing", path)
		return false
	}
	if !validateRFC3339(ev.TS, "trace ts is not RFC3339", strict, res, path) {
		return false
	}
	if strings.TrimSpace(ev.Tool) == "" || strings.TrimSpace(ev.Op) == "" {
		addErr(res, "ZCL_E_CONTRACT", "trace tool/op is missing", path)
		return false
	}
	if ev.RunID != attempt.RunID || ev.AttemptID != attempt.AttemptID || ev.MissionID != attempt.MissionID {
		addErr(res, "ZCL_E_ID_MISMATCH", "trace ids do not match attempt.json", path)
		return false
	}
	if !validateTraceOptionalIDs(ev, attempt, strict, path, res) {
		return false
	}
	return true
}

func validateTraceOptionalIDs(ev schema.TraceEventV1, attempt schema.AttemptJSONV1, strict bool, path string, res *Result) bool {
	if attempt.SuiteID != "" && ev.SuiteID != attempt.SuiteID {
		if strict {
			addErr(res, "ZCL_E_ID_MISMATCH", "trace suiteId does not match attempt.json", path)
			return false
		}
		addWarn(res, "ZCL_W_ID_MISMATCH", "trace suiteId does not match attempt.json", path)
	}
	if ev.AgentID != "" && attempt.AgentID != "" && ev.AgentID != attempt.AgentID {
		if strict {
			addErr(res, "ZCL_E_ID_MISMATCH", "trace agentId does not match attempt.json", path)
			return false
		}
		addWarn(res, "ZCL_W_ID_MISMATCH", "trace agentId does not match attempt.json", path)
	}
	return true
}

func validateTraceInputAndIO(ev schema.TraceEventV1, strict bool, path string, res *Result) bool {
	if len(ev.IO.OutPreview) > schema.PreviewMaxBytesV1 || len(ev.IO.ErrPreview) > schema.PreviewMaxBytesV1 {
		addErr(res, "ZCL_E_BOUNDS", "trace preview exceeds bounds", path)
		return false
	}
	if len(ev.Input) > schema.ToolInputMaxBytesV1 {
		addErr(res, "ZCL_E_BOUNDS", "trace input exceeds bounds", path)
		return false
	}
	if len(ev.Input) == 0 {
		if strict {
			addErr(res, "ZCL_E_CONTRACT", "trace input is missing", path)
			return false
		}
		addWarn(res, "ZCL_W_CONTRACT", "trace input is missing", path)
		return true
	}
	if !json.Valid(ev.Input) {
		addErr(res, "ZCL_E_CONTRACT", "trace input is not valid json", path)
		return false
	}
	validateKnownInputShape(ev, strict, res, path)
	return true
}

func validateTraceEnrichmentFields(ev schema.TraceEventV1, attemptDir string, strict bool, path string, res *Result) bool {
	if len(ev.Enrichment) > 0 && !json.Valid(ev.Enrichment) {
		if strict {
			addErr(res, "ZCL_E_CONTRACT", "trace enrichment is not valid json", path)
			return false
		}
		addWarn(res, "ZCL_W_CONTRACT", "trace enrichment is not valid json", path)
	} else if len(ev.Enrichment) > schema.EnrichmentMaxBytesV1 {
		addErr(res, "ZCL_E_BOUNDS", "trace enrichment exceeds bounds", path)
		return false
	} else if len(ev.Enrichment) > 0 && ev.Tool == "cli" {
		validateCLICaptureEnrichment(ev, attemptDir, strict, res, path)
	}
	if len(ev.RedactionsApplied) > schema.RedactionsAppliedMaxCountV1 {
		addErr(res, "ZCL_E_BOUNDS", "trace redactionsApplied exceeds bounds", path)
		return false
	}
	for _, n := range ev.RedactionsApplied {
		if len([]byte(n)) <= schema.RedactionNameMaxBytesV1 {
			continue
		}
		addErr(res, "ZCL_E_BOUNDS", "trace redaction name exceeds bounds", path)
		return false
	}
	return true
}

func validateCLICaptureEnrichment(ev schema.TraceEventV1, attemptDir string, strict bool, res *Result, path string) {
	capture, ok := parseCLICaptureMap(ev.Enrichment, strict, path, res)
	if !ok {
		return
	}
	validateTraceCapturePath(attemptDir, capture, "stdoutPath", strict, path, res)
	validateTraceCapturePath(attemptDir, capture, "stderrPath", strict, path, res)
}

func parseCLICaptureMap(raw json.RawMessage, strict bool, path string, res *Result) (map[string]any, bool) {
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		if strict {
			addErr(res, "ZCL_E_CONTRACT", "trace enrichment is not parseable json", path)
			return nil, false
		}
		addWarn(res, "ZCL_W_CONTRACT", "trace enrichment is not parseable json", path)
		return nil, false
	}
	m, ok := v.(map[string]any)
	if !ok {
		return nil, false
	}
	capAny, ok := m["capture"]
	if !ok {
		return nil, false
	}
	capture, ok := capAny.(map[string]any)
	if !ok {
		return nil, false
	}
	return capture, true
}

func validateTraceCapturePath(attemptDir string, capture map[string]any, key string, strict bool, path string, res *Result) {
	val, _ := capture[key].(string)
	val = strings.TrimSpace(val)
	if val == "" {
		return
	}
	if filepath.IsAbs(val) || strings.Contains(val, "..") {
		addErr(res, "ZCL_E_CONTRACT", "trace capture path must be a safe relative path", path)
		return
	}
	abs := filepath.Join(attemptDir, val)
	if !requireContained(attemptDir, abs, res) {
		return
	}
	if _, err := os.Stat(abs); err != nil {
		if strict && os.IsNotExist(err) {
			addErr(res, "ZCL_E_MISSING_ARTIFACT", "trace capture path does not exist", abs)
			return
		}
		if os.IsNotExist(err) {
			addWarn(res, "ZCL_W_MISSING_ARTIFACT", "trace capture path does not exist", abs)
		}
	}
}

func validateFeedback(path string, attempt schema.AttemptJSONV1, strict bool, res *Result) {
	fb, ok := readFeedbackArtifact(path, res)
	if !ok {
		return
	}
	if !validateFeedbackEnvelope(fb, strict, path, res) {
		return
	}
	if !validateFeedbackIDMatch(fb, attempt, path, res) {
		return
	}
	if !validateFeedbackResultShape(fb, strict, path, res) {
		return
	}
	validateFeedbackClassificationAndTags(fb, path, res)
}

func readFeedbackArtifact(path string, res *Result) (schema.FeedbackJSONV1, bool) {
	raw, err := os.ReadFile(path)
	if err != nil {
		addErr(res, "ZCL_E_IO", err.Error(), path)
		return schema.FeedbackJSONV1{}, false
	}
	if len(raw) > schema.FeedbackMaxBytesV1*2 {
		addErr(res, "ZCL_E_BOUNDS", "feedback.json exceeds bounds", path)
		return schema.FeedbackJSONV1{}, false
	}
	var fb schema.FeedbackJSONV1
	if err := json.Unmarshal(raw, &fb); err != nil {
		addErr(res, "ZCL_E_INVALID_JSON", "feedback.json is not valid json", path)
		return schema.FeedbackJSONV1{}, false
	}
	return fb, true
}

func validateFeedbackEnvelope(fb schema.FeedbackJSONV1, strict bool, path string, res *Result) bool {
	if fb.SchemaVersion != schema.FeedbackSchemaV1 {
		addErr(res, "ZCL_E_SCHEMA_UNSUPPORTED", "unsupported feedback.json schemaVersion", path)
		return false
	}
	if strings.TrimSpace(fb.RunID) == "" || strings.TrimSpace(fb.SuiteID) == "" || strings.TrimSpace(fb.MissionID) == "" || strings.TrimSpace(fb.AttemptID) == "" {
		addErr(res, "ZCL_E_CONTRACT", "feedback ids are missing", path)
		return false
	}
	if strings.TrimSpace(fb.CreatedAt) == "" {
		addErr(res, "ZCL_E_CONTRACT", "feedback createdAt is missing", path)
		return false
	}
	return validateRFC3339(fb.CreatedAt, "feedback createdAt is not RFC3339", strict, res, path)
}

func validateFeedbackIDMatch(fb schema.FeedbackJSONV1, attempt schema.AttemptJSONV1, path string, res *Result) bool {
	if fb.RunID != attempt.RunID || fb.AttemptID != attempt.AttemptID || fb.MissionID != attempt.MissionID {
		addErr(res, "ZCL_E_ID_MISMATCH", "feedback ids do not match attempt.json", path)
		return false
	}
	if fb.SuiteID != attempt.SuiteID {
		addErr(res, "ZCL_E_ID_MISMATCH", "feedback suiteId does not match attempt.json", path)
		return false
	}
	return true
}

func validateFeedbackResultShape(fb schema.FeedbackJSONV1, strict bool, path string, res *Result) bool {
	if fb.Result != "" && fb.ResultJSON != nil {
		addErr(res, "ZCL_E_CONTRACT", "feedback must set exactly one of result or resultJson", path)
		return false
	}
	if fb.Result == "" && fb.ResultJSON == nil {
		if strict {
			addErr(res, "ZCL_E_CONTRACT", "feedback missing result/resultJson", path)
			return false
		}
		addWarn(res, "ZCL_W_CONTRACT", "feedback missing result/resultJson", path)
	}
	if fb.Result != "" && len([]byte(fb.Result)) > schema.FeedbackMaxBytesV1 {
		addErr(res, "ZCL_E_BOUNDS", "feedback result exceeds bounds", path)
		return false
	}
	if fb.ResultJSON != nil && len(fb.ResultJSON) > schema.FeedbackMaxBytesV1 {
		addErr(res, "ZCL_E_BOUNDS", "feedback resultJson exceeds bounds", path)
		return false
	}
	return true
}

func validateFeedbackClassificationAndTags(fb schema.FeedbackJSONV1, path string, res *Result) {
	if strings.TrimSpace(fb.Classification) != "" && !schema.IsValidClassificationV1(fb.Classification) {
		addErr(res, "ZCL_E_CONTRACT", "feedback classification is invalid", path)
		return
	}
	for _, tag := range fb.DecisionTags {
		if schema.IsValidDecisionTagV1(tag) {
			continue
		}
		addErr(res, "ZCL_E_CONTRACT", "feedback decisionTags contains invalid tag", path)
		return
	}
}

func validateNotes(path string, attempt schema.AttemptJSONV1, strict bool, res *Result) {
	f, err := os.Open(path)
	if err != nil {
		addErr(res, "ZCL_E_IO", err.Error(), path)
		return
	}
	defer func() { _ = f.Close() }()

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	scanAndValidateNotes(sc, path, attempt, strict, res)
}

func scanAndValidateNotes(sc *bufio.Scanner, path string, attempt schema.AttemptJSONV1, strict bool, res *Result) bool {
	for sc.Scan() {
		line := sc.Bytes()
		if !validateNonEmptyJSONLLine(line, strict, "notes.jsonl", path, res) {
			return false
		}
		if len(bytesTrim(line)) == 0 {
			continue
		}
		if !validateNoteLine(line, path, attempt, strict, res) {
			return false
		}
	}
	if err := sc.Err(); err != nil {
		addErr(res, "ZCL_E_IO", err.Error(), path)
		return false
	}
	return true
}

func validateNoteLine(line []byte, path string, attempt schema.AttemptJSONV1, strict bool, res *Result) bool {
	var ev schema.NoteEventV1
	if err := json.Unmarshal(line, &ev); err != nil {
		addErr(res, "ZCL_E_INVALID_JSONL", "invalid jsonl line in notes.jsonl", path)
		return false
	}
	if !validateNoteEnvelope(ev, attempt, strict, path, res) {
		return false
	}
	return validateNotePayload(ev, path, res)
}

func validateNoteEnvelope(ev schema.NoteEventV1, attempt schema.AttemptJSONV1, strict bool, path string, res *Result) bool {
	if ev.V != schema.TraceSchemaV1 {
		addErr(res, "ZCL_E_SCHEMA_UNSUPPORTED", "unsupported note event version", path)
		return false
	}
	if ev.RunID != attempt.RunID || ev.AttemptID != attempt.AttemptID || ev.MissionID != attempt.MissionID {
		addErr(res, "ZCL_E_ID_MISMATCH", "note ids do not match attempt.json", path)
		return false
	}
	if attempt.SuiteID != "" && ev.SuiteID != attempt.SuiteID {
		if strict {
			addErr(res, "ZCL_E_ID_MISMATCH", "note suiteId does not match attempt.json", path)
			return false
		}
		addWarn(res, "ZCL_W_ID_MISMATCH", "note suiteId does not match attempt.json", path)
	}
	if ev.AgentID != "" && attempt.AgentID != "" && ev.AgentID != attempt.AgentID {
		if strict {
			addErr(res, "ZCL_E_ID_MISMATCH", "note agentId does not match attempt.json", path)
			return false
		}
		addWarn(res, "ZCL_W_ID_MISMATCH", "note agentId does not match attempt.json", path)
	}
	return true
}

func validateNotePayload(ev schema.NoteEventV1, path string, res *Result) bool {
	if ev.Message != "" && ev.Data != nil {
		addErr(res, "ZCL_E_CONTRACT", "note must set only one of message or data", path)
		return false
	}
	if ev.Message != "" && len([]byte(ev.Message)) > schema.NoteMessageMaxBytesV1 {
		addErr(res, "ZCL_E_BOUNDS", "note message exceeds bounds", path)
		return false
	}
	if ev.Data != nil && len(ev.Data) > schema.NoteDataMaxBytesV1 {
		addErr(res, "ZCL_E_BOUNDS", "note data exceeds bounds", path)
		return false
	}
	return true
}

func validateCaptures(path string, attemptDir string, attempt schema.AttemptJSONV1, strict bool, res *Result) {
	f, err := os.Open(path)
	if err != nil {
		addErr(res, "ZCL_E_IO", err.Error(), path)
		return
	}
	defer func() { _ = f.Close() }()

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	scanAndValidateCaptures(sc, path, attemptDir, attempt, strict, res)
}

func scanAndValidateCaptures(sc *bufio.Scanner, path, attemptDir string, attempt schema.AttemptJSONV1, strict bool, res *Result) bool {
	for sc.Scan() {
		line := sc.Bytes()
		if !validateNonEmptyJSONLLine(line, strict, "captures.jsonl", path, res) {
			return false
		}
		if len(bytesTrim(line)) == 0 {
			continue
		}
		if !validateCaptureLine(line, path, attemptDir, attempt, strict, res) {
			return false
		}
	}
	if err := sc.Err(); err != nil {
		addErr(res, "ZCL_E_IO", err.Error(), path)
		return false
	}
	return true
}

func validateCaptureLine(line []byte, path, attemptDir string, attempt schema.AttemptJSONV1, strict bool, res *Result) bool {
	ev, ok := parseCaptureEvent(line, path, res)
	if !ok {
		return false
	}
	if !validateCaptureEnvelope(ev, attempt, strict, path, res) {
		return false
	}
	if !validateCapturePayloadBounds(ev, attempt.Mode, strict, path, res) {
		return false
	}
	if !validateCaptureInput(ev, path, res) {
		return false
	}
	return validateCapturePaths(attemptDir, ev, strict, path, res)
}

func parseCaptureEvent(line []byte, path string, res *Result) (schema.CaptureEventV1, bool) {
	var ev schema.CaptureEventV1
	if err := json.Unmarshal(line, &ev); err != nil {
		addErr(res, "ZCL_E_INVALID_JSONL", "invalid jsonl line in captures.jsonl", path)
		return schema.CaptureEventV1{}, false
	}
	return ev, true
}

func validateCaptureEnvelope(ev schema.CaptureEventV1, attempt schema.AttemptJSONV1, strict bool, path string, res *Result) bool {
	if ev.V != 1 {
		addErr(res, "ZCL_E_SCHEMA_UNSUPPORTED", "unsupported capture event version", path)
		return false
	}
	if ev.RunID != attempt.RunID || ev.AttemptID != attempt.AttemptID || ev.MissionID != attempt.MissionID {
		addErr(res, "ZCL_E_ID_MISMATCH", "capture ids do not match attempt.json", path)
		return false
	}
	if !validateCaptureOptionalIDs(ev, attempt, strict, path, res) {
		return false
	}
	if strings.TrimSpace(ev.TS) == "" {
		addErr(res, "ZCL_E_CONTRACT", "capture ts is missing", path)
		return false
	}
	if !validateRFC3339(ev.TS, "capture ts is not RFC3339", strict, res, path) {
		return false
	}
	if strings.TrimSpace(ev.Tool) == "" || strings.TrimSpace(ev.Op) == "" {
		addErr(res, "ZCL_E_CONTRACT", "capture tool/op is missing", path)
		return false
	}
	if ev.MaxBytes <= 0 {
		addErr(res, "ZCL_E_CONTRACT", "capture maxBytes must be > 0", path)
		return false
	}
	return true
}

func validateCaptureOptionalIDs(ev schema.CaptureEventV1, attempt schema.AttemptJSONV1, strict bool, path string, res *Result) bool {
	if attempt.SuiteID != "" && ev.SuiteID != attempt.SuiteID {
		if strict {
			addErr(res, "ZCL_E_ID_MISMATCH", "capture suiteId does not match attempt.json", path)
			return false
		}
		addWarn(res, "ZCL_W_ID_MISMATCH", "capture suiteId does not match attempt.json", path)
	}
	if ev.AgentID != "" && attempt.AgentID != "" && ev.AgentID != attempt.AgentID {
		if strict {
			addErr(res, "ZCL_E_ID_MISMATCH", "capture agentId does not match attempt.json", path)
			return false
		}
		addWarn(res, "ZCL_W_ID_MISMATCH", "capture agentId does not match attempt.json", path)
	}
	return true
}

func validateCapturePayloadBounds(ev schema.CaptureEventV1, mode string, strict bool, path string, res *Result) bool {
	if len(ev.RedactionsApplied) > schema.RedactionsAppliedMaxCountV1 {
		addErr(res, "ZCL_E_BOUNDS", "capture redactionsApplied exceeds bounds", path)
		return false
	}
	if mode == "ci" && !ev.Redacted {
		if strict {
			addErr(res, "ZCL_E_UNSAFE_EVIDENCE", "capture event is raw (redacted=false) in ci mode", path)
			return false
		}
		addWarn(res, "ZCL_W_UNSAFE_EVIDENCE", "capture event is raw (redacted=false) in ci mode", path)
	}
	for _, n := range ev.RedactionsApplied {
		if len([]byte(n)) > schema.RedactionNameMaxBytesV1 {
			addErr(res, "ZCL_E_BOUNDS", "capture redaction name exceeds bounds", path)
			return false
		}
	}
	return true
}

func validateCaptureInput(ev schema.CaptureEventV1, path string, res *Result) bool {
	if len(ev.Input) == 0 {
		return true
	}
	if len(ev.Input) > schema.ToolInputMaxBytesV1 {
		addErr(res, "ZCL_E_BOUNDS", "capture input exceeds bounds", path)
		return false
	}
	if !json.Valid(ev.Input) {
		addErr(res, "ZCL_E_CONTRACT", "capture input is not valid json", path)
		return false
	}
	return true
}

func validateCapturePaths(attemptDir string, ev schema.CaptureEventV1, strict bool, path string, res *Result) bool {
	return validateCapturePath(attemptDir, ev.StdoutPath, ev.MaxBytes, strict, path, res) &&
		validateCapturePath(attemptDir, ev.StderrPath, ev.MaxBytes, strict, path, res)
}

func validateCapturePath(attemptDir, rel string, maxBytes int64, strict bool, path string, res *Result) bool {
	rel = strings.TrimSpace(rel)
	if rel == "" {
		return true
	}
	if filepath.IsAbs(rel) || strings.Contains(rel, "..") {
		addErr(res, "ZCL_E_CONTRACT", "capture path must be a safe relative path", path)
		return false
	}
	abs := filepath.Join(attemptDir, rel)
	if !requireContained(attemptDir, abs, res) {
		return false
	}
	info, err := os.Stat(abs)
	if err != nil {
		if strict && os.IsNotExist(err) {
			addErr(res, "ZCL_E_MISSING_ARTIFACT", "capture file is missing", abs)
			return false
		}
		if os.IsNotExist(err) {
			addWarn(res, "ZCL_W_MISSING_ARTIFACT", "capture file is missing", abs)
		}
		return true
	}
	if info.Size() > maxBytes {
		addErr(res, "ZCL_E_BOUNDS", "capture file exceeds maxBytes", abs)
		return false
	}
	return true
}

func requireFile(path string, required bool, strict bool, res *Result) bool {
	_, err := os.Stat(path)
	if err == nil {
		return true
	}
	if os.IsNotExist(err) {
		if required {
			if strict {
				addErr(res, "ZCL_E_MISSING_ARTIFACT", "missing required artifact", path)
			} else {
				addWarn(res, "ZCL_W_MISSING_ARTIFACT", "missing artifact", path)
			}
		}
		return false
	}
	addErr(res, "ZCL_E_IO", err.Error(), path)
	return false
}

func requireContained(root, path string, res *Result) bool {
	rootEval, err := filepath.EvalSymlinks(root)
	if err != nil {
		addErr(res, "ZCL_E_IO", err.Error(), root)
		return false
	}
	pEval, err := filepath.EvalSymlinks(path)
	if err != nil {
		// If the artifact doesn't exist yet, containment doesn't apply.
		if os.IsNotExist(err) {
			return true
		}
		addErr(res, "ZCL_E_IO", err.Error(), path)
		return false
	}
	rootEval = filepath.Clean(rootEval)
	pEval = filepath.Clean(pEval)
	sep := string(os.PathSeparator)
	if !strings.HasPrefix(pEval, rootEval+sep) && pEval != rootEval {
		addErr(res, "ZCL_E_CONTAINMENT", "artifact path escapes attempt/run directory (symlink traversal)", path)
		return false
	}
	return true
}

func bytesTrim(b []byte) []byte {
	i := 0
	j := len(b)
	for i < j {
		c := b[i]
		if c != ' ' && c != '\n' && c != '\t' && c != '\r' {
			break
		}
		i++
	}
	for j > i {
		c := b[j-1]
		if c != ' ' && c != '\n' && c != '\t' && c != '\r' {
			break
		}
		j--
	}
	return b[i:j]
}

type suiteRunSummaryV1 struct {
	SchemaVersion             int               `json:"schemaVersion"`
	RunID                     string            `json:"runId"`
	SuiteID                   string            `json:"suiteId"`
	Mode                      string            `json:"mode"`
	SessionIsolationRequested string            `json:"sessionIsolationRequested"`
	SessionIsolation          string            `json:"sessionIsolation"`
	Attempts                  []json.RawMessage `json:"attempts"`
	Passed                    int               `json:"passed"`
	Failed                    int               `json:"failed"`
	CreatedAt                 string            `json:"createdAt"`
}

func validateSuiteRunSummary(path string, run schema.RunJSONV1, strict bool, res *Result) {
	s, ok := readSuiteRunSummary(path, res)
	if !ok {
		return
	}
	if !validateSuiteRunSummaryIdentity(s, run, path, res) {
		return
	}
	if !validateSuiteRunSummaryFields(s, strict, path, res) {
		return
	}
	validateSuiteRunSummaryCounts(s, strict, path, res)
}

func readSuiteRunSummary(path string, res *Result) (suiteRunSummaryV1, bool) {
	raw, err := os.ReadFile(path)
	if err != nil {
		addErr(res, "ZCL_E_IO", err.Error(), path)
		return suiteRunSummaryV1{}, false
	}
	var s suiteRunSummaryV1
	if err := json.Unmarshal(raw, &s); err != nil {
		addErr(res, "ZCL_E_INVALID_JSON", "suite.run.summary.json is not valid json", path)
		return suiteRunSummaryV1{}, false
	}
	return s, true
}

func validateSuiteRunSummaryIdentity(s suiteRunSummaryV1, run schema.RunJSONV1, path string, res *Result) bool {
	if s.SchemaVersion != 1 {
		addErr(res, "ZCL_E_SCHEMA_UNSUPPORTED", "unsupported suite.run.summary.json schemaVersion", path)
		return false
	}
	if strings.TrimSpace(s.RunID) == "" || strings.TrimSpace(s.SuiteID) == "" {
		addErr(res, "ZCL_E_CONTRACT", "suite.run.summary.json missing runId/suiteId", path)
		return false
	}
	if s.RunID != run.RunID || s.SuiteID != run.SuiteID {
		addErr(res, "ZCL_E_ID_MISMATCH", "suite.run.summary.json ids do not match run.json", path)
		return false
	}
	return true
}

func validateSuiteRunSummaryFields(s suiteRunSummaryV1, strict bool, path string, res *Result) bool {
	if strings.TrimSpace(s.Mode) == "" {
		addErr(res, "ZCL_E_CONTRACT", "suite.run.summary.json mode is missing", path)
		return false
	}
	if strings.TrimSpace(s.CreatedAt) == "" {
		addErr(res, "ZCL_E_CONTRACT", "suite.run.summary.json createdAt is missing", path)
		return false
	}
	if !validateRFC3339(s.CreatedAt, "suite.run.summary.json createdAt is not RFC3339", strict, res, path) {
		return false
	}
	if strings.TrimSpace(s.SessionIsolationRequested) == "" || strings.TrimSpace(s.SessionIsolation) == "" {
		addErr(res, "ZCL_E_CONTRACT", "suite.run.summary.json session isolation fields are missing", path)
		return false
	}
	if !isValidSessionIsolationRequested(s.SessionIsolationRequested) {
		addErr(res, "ZCL_E_CONTRACT", "suite.run.summary.json sessionIsolationRequested is invalid", path)
		return false
	}
	if !schema.IsValidIsolationModelV1(strings.TrimSpace(s.SessionIsolation)) {
		addErr(res, "ZCL_E_CONTRACT", "suite.run.summary.json sessionIsolation is invalid", path)
		return false
	}
	return true
}

func isValidSessionIsolationRequested(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "auto", "process", "native":
		return true
	default:
		return false
	}
}

func validateSuiteRunSummaryCounts(s suiteRunSummaryV1, strict bool, path string, res *Result) {
	if s.Passed < 0 || s.Failed < 0 {
		addErr(res, "ZCL_E_CONTRACT", "suite.run.summary.json passed/failed must be >= 0", path)
		return
	}
	total := s.Passed + s.Failed
	if total == len(s.Attempts) {
		return
	}
	if strict {
		addErr(res, "ZCL_E_CONTRACT", "suite.run.summary.json attempts count does not match passed+failed", path)
		return
	}
	addWarn(res, "ZCL_W_CONTRACT", "suite.run.summary.json attempts count does not match passed+failed", path)
}

type runReportV1 struct {
	SchemaVersion int                `json:"schemaVersion"`
	Target        string             `json:"target"`
	RunID         string             `json:"runId"`
	SuiteID       string             `json:"suiteId"`
	Path          string             `json:"path"`
	Attempts      []json.RawMessage  `json:"attempts"`
	Aggregate     runReportAggregate `json:"aggregate"`
}

type runReportAggregate struct {
	AttemptsTotal int `json:"attemptsTotal"`
}

func validateRunReport(path string, runDir string, run schema.RunJSONV1, strict bool, res *Result) {
	raw, err := os.ReadFile(path)
	if err != nil {
		addErr(res, "ZCL_E_IO", err.Error(), path)
		return
	}
	var rr runReportV1
	if err := json.Unmarshal(raw, &rr); err != nil {
		addErr(res, "ZCL_E_INVALID_JSON", "run.report.json is not valid json", path)
		return
	}
	if rr.SchemaVersion != 1 {
		addErr(res, "ZCL_E_SCHEMA_UNSUPPORTED", "unsupported run.report.json schemaVersion", path)
		return
	}
	if strings.TrimSpace(rr.Target) != "run" {
		addErr(res, "ZCL_E_CONTRACT", "run.report.json target must be run", path)
		return
	}
	if rr.RunID != run.RunID || rr.SuiteID != run.SuiteID {
		addErr(res, "ZCL_E_ID_MISMATCH", "run.report.json ids do not match run.json", path)
		return
	}
	if strings.TrimSpace(rr.Path) == "" {
		addErr(res, "ZCL_E_CONTRACT", "run.report.json path is missing", path)
		return
	}
	runDirAbs, _ := filepath.Abs(runDir)
	pathAbs, err := filepath.Abs(rr.Path)
	if err == nil && runDirAbs != "" && pathAbs != runDirAbs {
		if strict {
			addErr(res, "ZCL_E_CONTRACT", "run.report.json path does not match run directory", path)
			return
		}
		addWarn(res, "ZCL_W_CONTRACT", "run.report.json path does not match run directory", path)
	}
	if rr.Aggregate.AttemptsTotal != len(rr.Attempts) {
		if strict {
			addErr(res, "ZCL_E_CONTRACT", "run.report.json aggregate.attemptsTotal does not match attempts length", path)
			return
		}
		addWarn(res, "ZCL_W_CONTRACT", "run.report.json aggregate.attemptsTotal does not match attempts length", path)
	}
}

func addErr(res *Result, code, msg, path string) {
	res.OK = false
	res.Errors = append(res.Errors, Finding{Code: code, Message: msg, Path: path})
}

func addWarn(res *Result, code, msg, path string) {
	res.Warnings = append(res.Warnings, Finding{Code: code, Message: msg, Path: path})
}

func finalize(res Result) Result {
	if len(res.Errors) > 0 {
		res.OK = false
	}
	return res
}

func validateKnownInputShape(ev schema.TraceEventV1, strict bool, res *Result, path string) {
	// Only enforce for tools that ZCL itself emits. Unknown tools are allowed.
	if ev.Tool != "cli" && ev.Tool != "mcp" && ev.Tool != "http" {
		return
	}

	var v any
	if err := json.Unmarshal(ev.Input, &v); err != nil {
		if strict {
			addErr(res, "ZCL_E_CONTRACT", "trace input is not parseable json", path)
			return
		}
		addWarn(res, "ZCL_W_CONTRACT", "trace input is not parseable json", path)
		return
	}
	m, ok := v.(map[string]any)
	if !ok {
		if strict {
			addErr(res, "ZCL_E_CONTRACT", "trace input must be a json object", path)
			return
		}
		addWarn(res, "ZCL_W_CONTRACT", "trace input must be a json object", path)
		return
	}
	validateKnownToolInputShape(ev.Tool, ev.Op, m, strict, res, path)
}

func validateKnownToolInputShape(tool, op string, input map[string]any, strict bool, res *Result, path string) {
	switch tool {
	case "cli":
		validateCLIInputShape(op, input, strict, res, path)
	case "mcp":
		validateMCPInputShape(op, input, strict, res, path)
	case "http":
		validateHTTPInputShape(input, strict, res, path)
	}
}

func validateCLIInputShape(op string, input map[string]any, strict bool, res *Result, path string) {
	if op != "exec" {
		return
	}
	argv, ok := input["argv"].([]any)
	if ok && len(argv) > 0 {
		return
	}
	if strict {
		addErr(res, "ZCL_E_CONTRACT", "cli exec input must include non-empty argv", path)
		return
	}
	addWarn(res, "ZCL_W_CONTRACT", "cli exec input should include non-empty argv", path)
}

func validateMCPInputShape(op string, input map[string]any, strict bool, res *Result, path string) {
	if op == "spawn" || op == "stderr" || op == "timeout" {
		return
	}
	method, ok := input["method"].(string)
	if ok && strings.TrimSpace(method) != "" {
		return
	}
	if strict {
		addErr(res, "ZCL_E_CONTRACT", "mcp input must include method", path)
		return
	}
	addWarn(res, "ZCL_W_CONTRACT", "mcp input should include method", path)
}

func validateHTTPInputShape(input map[string]any, strict bool, res *Result, path string) {
	method, methodOK := input["method"].(string)
	if !methodOK || strings.TrimSpace(method) == "" {
		if strict {
			addErr(res, "ZCL_E_CONTRACT", "http input must include method", path)
			return
		}
		addWarn(res, "ZCL_W_CONTRACT", "http input should include method", path)
	}
	url, urlOK := input["url"].(string)
	if urlOK && strings.TrimSpace(url) != "" {
		return
	}
	if strict {
		addErr(res, "ZCL_E_CONTRACT", "http input must include url", path)
		return
	}
	addWarn(res, "ZCL_W_CONTRACT", "http input should include url", path)
}
