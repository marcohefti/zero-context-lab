package validate

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/marcohefti/zero-context-lab/internal/ids"
	"github.com/marcohefti/zero-context-lab/internal/schema"
	"github.com/marcohefti/zero-context-lab/internal/store"
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

	runJSONPath := filepath.Join(runDir, "run.json")
	if !requireFile(runJSONPath, true, true, &res) {
		return finalize(res)
	}
	if !requireContained(runDir, runJSONPath, &res) {
		return finalize(res)
	}
	raw, err := os.ReadFile(runJSONPath)
	if err != nil {
		addErr(&res, "ZCL_E_IO", err.Error(), runJSONPath)
		return finalize(res)
	}
	var run schema.RunJSONV1
	if err := json.Unmarshal(raw, &run); err != nil {
		addErr(&res, "ZCL_E_INVALID_JSON", "run.json is not valid json", runJSONPath)
		return finalize(res)
	}
	if run.SchemaVersion != schema.RunSchemaV1 {
		addErr(&res, "ZCL_E_SCHEMA_UNSUPPORTED", "unsupported run.json schemaVersion", runJSONPath)
		return finalize(res)
	}
	if run.ArtifactLayoutVersion == 0 {
		addErr(&res, "ZCL_E_CONTRACT", "artifactLayoutVersion is missing", runJSONPath)
		return finalize(res)
	}
	if run.ArtifactLayoutVersion != schema.ArtifactLayoutVersionV1 {
		addErr(&res, "ZCL_E_SCHEMA_UNSUPPORTED", "unsupported artifactLayoutVersion", runJSONPath)
		return finalize(res)
	}
	if strings.TrimSpace(run.RunID) == "" || !ids.IsValidRunID(run.RunID) {
		addErr(&res, "ZCL_E_CONTRACT", "runId is missing/invalid", runJSONPath)
		return finalize(res)
	}
	if strings.TrimSpace(run.SuiteID) == "" {
		addErr(&res, "ZCL_E_CONTRACT", "suiteId is missing", runJSONPath)
		return finalize(res)
	}
	if strings.TrimSpace(run.CreatedAt) == "" {
		addErr(&res, "ZCL_E_CONTRACT", "createdAt is missing", runJSONPath)
		return finalize(res)
	}
	if _, err := time.Parse(time.RFC3339Nano, run.CreatedAt); err != nil {
		if _, err2 := time.Parse(time.RFC3339, run.CreatedAt); err2 != nil {
			if strict {
				addErr(&res, "ZCL_E_CONTRACT", "createdAt is not RFC3339", runJSONPath)
				return finalize(res)
			}
			addWarn(&res, "ZCL_W_CONTRACT", "createdAt is not RFC3339", runJSONPath)
		}
	}
	if base := filepath.Base(runDir); run.RunID != base {
		addErr(&res, "ZCL_E_ID_MISMATCH", "runId does not match directory name", runJSONPath)
	}

	attemptsDir := filepath.Join(runDir, "attempts")
	entries, err := os.ReadDir(attemptsDir)
	if err != nil {
		if strict && os.IsNotExist(err) {
			addErr(&res, "ZCL_E_MISSING_ARTIFACT", "missing attempts directory", attemptsDir)
			return finalize(res)
		}
		// Best effort: no attempts.
		if os.IsNotExist(err) {
			addWarn(&res, "ZCL_W_MISSING_ARTIFACT", "missing attempts directory", attemptsDir)
			return finalize(res)
		}
		return finalize(res)
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

	return finalize(res)
}

func validateAttempt(attemptDir string, strict bool) Result {
	res := Result{OK: true, Strict: strict, Target: "attempt", Path: attemptDir}

	attemptJSONPath := filepath.Join(attemptDir, "attempt.json")
	if !requireFile(attemptJSONPath, true, true, &res) { // attempt.json is always required
		return finalize(res)
	}
	if !requireContained(attemptDir, attemptJSONPath, &res) {
		return finalize(res)
	}
	raw, err := os.ReadFile(attemptJSONPath)
	if err != nil {
		addErr(&res, "ZCL_E_IO", err.Error(), attemptJSONPath)
		return finalize(res)
	}
	var attempt schema.AttemptJSONV1
	if err := json.Unmarshal(raw, &attempt); err != nil {
		addErr(&res, "ZCL_E_INVALID_JSON", "attempt.json is not valid json", attemptJSONPath)
		return finalize(res)
	}
	if attempt.SchemaVersion != schema.AttemptSchemaV1 {
		addErr(&res, "ZCL_E_SCHEMA_UNSUPPORTED", "unsupported attempt.json schemaVersion", attemptJSONPath)
		return finalize(res)
	}
	if strings.TrimSpace(attempt.RunID) == "" || !ids.IsValidRunID(attempt.RunID) {
		addErr(&res, "ZCL_E_CONTRACT", "attempt runId is missing/invalid", attemptJSONPath)
		return finalize(res)
	}
	if strings.TrimSpace(attempt.SuiteID) == "" {
		addErr(&res, "ZCL_E_CONTRACT", "attempt suiteId is missing", attemptJSONPath)
		return finalize(res)
	}
	if strings.TrimSpace(attempt.MissionID) == "" {
		addErr(&res, "ZCL_E_CONTRACT", "attempt missionId is missing", attemptJSONPath)
		return finalize(res)
	}
	if strings.TrimSpace(attempt.AttemptID) == "" {
		addErr(&res, "ZCL_E_CONTRACT", "attempt attemptId is missing", attemptJSONPath)
		return finalize(res)
	}
	if attempt.Mode != "discovery" && attempt.Mode != "ci" {
		addErr(&res, "ZCL_E_CONTRACT", "attempt mode is invalid (expected discovery|ci)", attemptJSONPath)
		return finalize(res)
	}
	if strings.TrimSpace(attempt.StartedAt) == "" {
		addErr(&res, "ZCL_E_CONTRACT", "attempt startedAt is missing", attemptJSONPath)
		return finalize(res)
	}
	if _, err := time.Parse(time.RFC3339Nano, attempt.StartedAt); err != nil {
		if _, err2 := time.Parse(time.RFC3339, attempt.StartedAt); err2 != nil {
			if strict {
				addErr(&res, "ZCL_E_CONTRACT", "attempt startedAt is not RFC3339", attemptJSONPath)
				return finalize(res)
			}
			addWarn(&res, "ZCL_W_CONTRACT", "attempt startedAt is not RFC3339", attemptJSONPath)
		}
	}
	if strict {
		if ids.SanitizeComponent(attempt.SuiteID) != attempt.SuiteID {
			addErr(&res, "ZCL_E_CONTRACT", "attempt suiteId is not canonicalized", attemptJSONPath)
			return finalize(res)
		}
		if ids.SanitizeComponent(attempt.MissionID) != attempt.MissionID {
			addErr(&res, "ZCL_E_CONTRACT", "attempt missionId is not canonicalized", attemptJSONPath)
			return finalize(res)
		}
	}
	if base := filepath.Base(attemptDir); attempt.AttemptID != base {
		addErr(&res, "ZCL_E_ID_MISMATCH", "attemptId does not match directory name", attemptJSONPath)
	}
	// Validate runId vs the path segment if it matches the standard layout: .../runs/<runId>/attempts/<attemptId>
	if runDir := filepath.Dir(filepath.Dir(attemptDir)); filepath.Base(runDir) == attempt.RunID {
		// ok
	}

	enforce := strict || attempt.Mode == "ci"

	tracePath := filepath.Join(attemptDir, "tool.calls.jsonl")
	feedbackPath := filepath.Join(attemptDir, "feedback.json")

	// Funnel integrity gate (best-effort): in strict mode, if a final outcome exists,
	// require non-empty primary evidence. This is a cheap bypass detector.
	feedbackExists := false
	if _, err := os.Stat(feedbackPath); err == nil {
		feedbackExists = true
	}
	if feedbackExists {
		if _, err := os.Stat(tracePath); err != nil {
			if os.IsNotExist(err) {
				if enforce {
					addErr(&res, "ZCL_E_FUNNEL_BYPASS", "feedback.json exists but tool.calls.jsonl is missing", attemptDir)
					return finalize(res)
				}
				addWarn(&res, "ZCL_W_FUNNEL_BYPASS_SUSPECTED", "feedback.json exists but tool.calls.jsonl is missing", attemptDir)
				// continue validation best-effort
			} else {
				addErr(&res, "ZCL_E_IO", err.Error(), tracePath)
				return finalize(res)
			}
		}
		if _, err := os.Stat(tracePath); err == nil {
			nonEmpty, err := store.JSONLHasNonEmptyLine(tracePath)
			if err != nil {
				addErr(&res, "ZCL_E_IO", err.Error(), tracePath)
				return finalize(res)
			}
			if !nonEmpty {
				if enforce {
					addErr(&res, "ZCL_E_FUNNEL_BYPASS", "feedback.json exists but tool.calls.jsonl is empty", tracePath)
					return finalize(res)
				}
				addWarn(&res, "ZCL_W_FUNNEL_BYPASS_SUSPECTED", "feedback.json exists but tool.calls.jsonl is empty", tracePath)
			}
		}
	}

	// Requiredness is mode-dependent. In discovery mode we still warn on missing
	// primary evidence, but we don't fail unless strict is requested.
	requireFile(tracePath, true, enforce, &res)
	requireFile(feedbackPath, true, enforce, &res)

	if _, err := os.Stat(tracePath); err == nil {
		if requireContained(attemptDir, tracePath, &res) {
			validateTrace(tracePath, attemptDir, attempt, enforce, &res)
		}
	}

	if _, err := os.Stat(feedbackPath); err == nil {
		if requireContained(attemptDir, feedbackPath, &res) {
			validateFeedback(feedbackPath, attempt, enforce, &res)
		}
	}

	notesPath := filepath.Join(attemptDir, "notes.jsonl")
	if _, err := os.Stat(notesPath); err == nil {
		if requireContained(attemptDir, notesPath, &res) {
			validateNotes(notesPath, attempt, enforce, &res)
		}
	}

	capturesPath := filepath.Join(attemptDir, "captures.jsonl")
	if _, err := os.Stat(capturesPath); err == nil {
		if requireContained(attemptDir, capturesPath, &res) {
			validateCaptures(capturesPath, attemptDir, attempt, enforce, &res)
		}
	}

	// Optional attempt.report.json integrity (if present).
	reportPath := filepath.Join(attemptDir, "attempt.report.json")
	if _, err := os.Stat(reportPath); err == nil {
		if !requireContained(attemptDir, reportPath, &res) {
			return finalize(res)
		}
		raw, err := os.ReadFile(reportPath)
		if err != nil {
			addErr(&res, "ZCL_E_IO", err.Error(), reportPath)
			return finalize(res)
		}
		var rep schema.AttemptReportJSONV1
		if err := json.Unmarshal(raw, &rep); err != nil {
			addErr(&res, "ZCL_E_INVALID_JSON", "attempt.report.json is not valid json", reportPath)
			return finalize(res)
		}
		if rep.SchemaVersion != schema.AttemptReportSchemaV1 {
			addErr(&res, "ZCL_E_SCHEMA_UNSUPPORTED", "unsupported attempt.report.json schemaVersion", reportPath)
			return finalize(res)
		}
		if rep.RunID != attempt.RunID || rep.AttemptID != attempt.AttemptID || rep.MissionID != attempt.MissionID {
			addErr(&res, "ZCL_E_ID_MISMATCH", "attempt.report.json ids do not match attempt.json", reportPath)
		}
		if rep.Result != "" && rep.ResultJSON != nil {
			addErr(&res, "ZCL_E_CONTRACT", "attempt.report.json must set only one of result or resultJson", reportPath)
			return finalize(res)
		}
		if strings.TrimSpace(rep.Classification) != "" && !schema.IsValidClassificationV1(rep.Classification) {
			addErr(&res, "ZCL_E_CONTRACT", "attempt.report.json classification is invalid", reportPath)
			return finalize(res)
		}
		if strings.TrimSpace(rep.Artifacts.AttemptJSON) == "" || strings.TrimSpace(rep.Artifacts.TraceJSONL) == "" || strings.TrimSpace(rep.Artifacts.FeedbackJSON) == "" {
			if enforce {
				addErr(&res, "ZCL_E_CONTRACT", "attempt.report.json artifacts are missing required pointers", reportPath)
				return finalize(res)
			}
			addWarn(&res, "ZCL_W_CONTRACT", "attempt.report.json artifacts missing pointers", reportPath)
		}
	}

	return finalize(res)
}

func validateTrace(path string, attemptDir string, attempt schema.AttemptJSONV1, strict bool, res *Result) {
	f, err := os.Open(path)
	if err != nil {
		addErr(res, "ZCL_E_IO", err.Error(), path)
		return
	}
	defer func() { _ = f.Close() }()

	sc := bufio.NewScanner(f)
	// Allow reasonably large lines, but keep bounded.
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	n := 0
	for sc.Scan() {
		line := sc.Bytes()
		if len(bytesTrim(line)) == 0 {
			if strict {
				addErr(res, "ZCL_E_INVALID_JSONL", "empty line in tool.calls.jsonl", path)
				return
			}
			continue
		}
		var ev schema.TraceEventV1
		if err := json.Unmarshal(line, &ev); err != nil {
			addErr(res, "ZCL_E_INVALID_JSONL", "invalid jsonl line in tool.calls.jsonl", path)
			return
		}
		n++
		if ev.V != schema.TraceSchemaV1 {
			addErr(res, "ZCL_E_SCHEMA_UNSUPPORTED", "unsupported trace event version", path)
			return
		}
		if strings.TrimSpace(ev.TS) == "" {
			addErr(res, "ZCL_E_CONTRACT", "trace ts is missing", path)
			return
		}
		if _, err := time.Parse(time.RFC3339Nano, ev.TS); err != nil {
			if _, err2 := time.Parse(time.RFC3339, ev.TS); err2 != nil {
				if strict {
					addErr(res, "ZCL_E_CONTRACT", "trace ts is not RFC3339", path)
					return
				}
				addWarn(res, "ZCL_W_CONTRACT", "trace ts is not RFC3339", path)
			}
		}
		if strings.TrimSpace(ev.Tool) == "" || strings.TrimSpace(ev.Op) == "" {
			addErr(res, "ZCL_E_CONTRACT", "trace tool/op is missing", path)
			return
		}
		if ev.RunID != attempt.RunID || ev.AttemptID != attempt.AttemptID || ev.MissionID != attempt.MissionID {
			addErr(res, "ZCL_E_ID_MISMATCH", "trace ids do not match attempt.json", path)
			return
		}
		if attempt.SuiteID != "" && ev.SuiteID != attempt.SuiteID {
			if strict {
				addErr(res, "ZCL_E_ID_MISMATCH", "trace suiteId does not match attempt.json", path)
				return
			}
			addWarn(res, "ZCL_W_ID_MISMATCH", "trace suiteId does not match attempt.json", path)
		}
		if ev.AgentID != "" && attempt.AgentID != "" && ev.AgentID != attempt.AgentID {
			if strict {
				addErr(res, "ZCL_E_ID_MISMATCH", "trace agentId does not match attempt.json", path)
				return
			}
			addWarn(res, "ZCL_W_ID_MISMATCH", "trace agentId does not match attempt.json", path)
		}
		if len(ev.IO.OutPreview) > schema.PreviewMaxBytesV1 || len(ev.IO.ErrPreview) > schema.PreviewMaxBytesV1 {
			addErr(res, "ZCL_E_BOUNDS", "trace preview exceeds bounds", path)
			return
		}
		if len(ev.Input) > schema.ToolInputMaxBytesV1 {
			addErr(res, "ZCL_E_BOUNDS", "trace input exceeds bounds", path)
			return
		}
		if len(ev.Input) == 0 {
			if strict {
				addErr(res, "ZCL_E_CONTRACT", "trace input is missing", path)
				return
			}
			addWarn(res, "ZCL_W_CONTRACT", "trace input is missing", path)
		} else if !json.Valid(ev.Input) {
			addErr(res, "ZCL_E_CONTRACT", "trace input is not valid json", path)
			return
		} else {
			validateKnownInputShape(ev, strict, res, path)
		}
		if len(ev.Enrichment) > 0 && !json.Valid(ev.Enrichment) {
			if strict {
				addErr(res, "ZCL_E_CONTRACT", "trace enrichment is not valid json", path)
				return
			}
			addWarn(res, "ZCL_W_CONTRACT", "trace enrichment is not valid json", path)
		} else if len(ev.Enrichment) > schema.EnrichmentMaxBytesV1 {
			addErr(res, "ZCL_E_BOUNDS", "trace enrichment exceeds bounds", path)
			return
		} else if len(ev.Enrichment) > 0 && ev.Tool == "cli" {
			validateCLICaptureEnrichment(ev, attemptDir, strict, res, path)
		}
		if len(ev.RedactionsApplied) > schema.RedactionsAppliedMaxCountV1 {
			addErr(res, "ZCL_E_BOUNDS", "trace redactionsApplied exceeds bounds", path)
			return
		}
		for _, n := range ev.RedactionsApplied {
			if len([]byte(n)) > schema.RedactionNameMaxBytesV1 {
				addErr(res, "ZCL_E_BOUNDS", "trace redaction name exceeds bounds", path)
				return
			}
		}
	}
	if err := sc.Err(); err != nil {
		addErr(res, "ZCL_E_IO", err.Error(), path)
		return
	}
	if strict && n == 0 {
		addErr(res, "ZCL_E_MISSING_EVIDENCE", "tool.calls.jsonl is empty", path)
		return
	}
}

func validateCLICaptureEnrichment(ev schema.TraceEventV1, attemptDir string, strict bool, res *Result, path string) {
	var v any
	if err := json.Unmarshal(ev.Enrichment, &v); err != nil {
		if strict {
			addErr(res, "ZCL_E_CONTRACT", "trace enrichment is not parseable json", path)
			return
		}
		addWarn(res, "ZCL_W_CONTRACT", "trace enrichment is not parseable json", path)
		return
	}
	m, ok := v.(map[string]any)
	if !ok {
		return
	}
	capAny, ok := m["capture"]
	if !ok {
		return
	}
	capMap, ok := capAny.(map[string]any)
	if !ok {
		return
	}

	checkRel := func(key string) {
		val, _ := capMap[key].(string)
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
				return
			}
		}
	}

	checkRel("stdoutPath")
	checkRel("stderrPath")
}

func validateFeedback(path string, attempt schema.AttemptJSONV1, strict bool, res *Result) {
	raw, err := os.ReadFile(path)
	if err != nil {
		addErr(res, "ZCL_E_IO", err.Error(), path)
		return
	}
	if len(raw) > schema.FeedbackMaxBytesV1*2 {
		// feedback.json includes envelope + possible pretty-print; enforce loose cap.
		addErr(res, "ZCL_E_BOUNDS", "feedback.json exceeds bounds", path)
		return
	}
	var fb schema.FeedbackJSONV1
	if err := json.Unmarshal(raw, &fb); err != nil {
		addErr(res, "ZCL_E_INVALID_JSON", "feedback.json is not valid json", path)
		return
	}
	if fb.SchemaVersion != schema.FeedbackSchemaV1 {
		addErr(res, "ZCL_E_SCHEMA_UNSUPPORTED", "unsupported feedback.json schemaVersion", path)
		return
	}
	if strings.TrimSpace(fb.RunID) == "" || strings.TrimSpace(fb.SuiteID) == "" || strings.TrimSpace(fb.MissionID) == "" || strings.TrimSpace(fb.AttemptID) == "" {
		addErr(res, "ZCL_E_CONTRACT", "feedback ids are missing", path)
		return
	}
	if strings.TrimSpace(fb.CreatedAt) == "" {
		addErr(res, "ZCL_E_CONTRACT", "feedback createdAt is missing", path)
		return
	}
	if _, err := time.Parse(time.RFC3339Nano, fb.CreatedAt); err != nil {
		if _, err2 := time.Parse(time.RFC3339, fb.CreatedAt); err2 != nil {
			if strict {
				addErr(res, "ZCL_E_CONTRACT", "feedback createdAt is not RFC3339", path)
				return
			}
			addWarn(res, "ZCL_W_CONTRACT", "feedback createdAt is not RFC3339", path)
		}
	}
	if fb.RunID != attempt.RunID || fb.AttemptID != attempt.AttemptID || fb.MissionID != attempt.MissionID {
		addErr(res, "ZCL_E_ID_MISMATCH", "feedback ids do not match attempt.json", path)
		return
	}
	if fb.SuiteID != attempt.SuiteID {
		addErr(res, "ZCL_E_ID_MISMATCH", "feedback suiteId does not match attempt.json", path)
		return
	}
	if fb.Result != "" && fb.ResultJSON != nil {
		addErr(res, "ZCL_E_CONTRACT", "feedback must set exactly one of result or resultJson", path)
		return
	}
	if fb.Result == "" && fb.ResultJSON == nil {
		if strict {
			addErr(res, "ZCL_E_CONTRACT", "feedback missing result/resultJson", path)
			return
		}
		addWarn(res, "ZCL_W_CONTRACT", "feedback missing result/resultJson", path)
	}
	if fb.Result != "" && len([]byte(fb.Result)) > schema.FeedbackMaxBytesV1 {
		addErr(res, "ZCL_E_BOUNDS", "feedback result exceeds bounds", path)
		return
	}
	if fb.ResultJSON != nil && len(fb.ResultJSON) > schema.FeedbackMaxBytesV1 {
		addErr(res, "ZCL_E_BOUNDS", "feedback resultJson exceeds bounds", path)
		return
	}
	if strings.TrimSpace(fb.Classification) != "" && !schema.IsValidClassificationV1(fb.Classification) {
		addErr(res, "ZCL_E_CONTRACT", "feedback classification is invalid", path)
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
	for sc.Scan() {
		line := sc.Bytes()
		if len(bytesTrim(line)) == 0 {
			if strict {
				addErr(res, "ZCL_E_INVALID_JSONL", "empty line in notes.jsonl", path)
				return
			}
			continue
		}
		var ev schema.NoteEventV1
		if err := json.Unmarshal(line, &ev); err != nil {
			addErr(res, "ZCL_E_INVALID_JSONL", "invalid jsonl line in notes.jsonl", path)
			return
		}
		if ev.V != schema.TraceSchemaV1 {
			addErr(res, "ZCL_E_SCHEMA_UNSUPPORTED", "unsupported note event version", path)
			return
		}
		if ev.RunID != attempt.RunID || ev.AttemptID != attempt.AttemptID || ev.MissionID != attempt.MissionID {
			addErr(res, "ZCL_E_ID_MISMATCH", "note ids do not match attempt.json", path)
			return
		}
		if attempt.SuiteID != "" && ev.SuiteID != attempt.SuiteID {
			if strict {
				addErr(res, "ZCL_E_ID_MISMATCH", "note suiteId does not match attempt.json", path)
				return
			}
			addWarn(res, "ZCL_W_ID_MISMATCH", "note suiteId does not match attempt.json", path)
		}
		if ev.AgentID != "" && attempt.AgentID != "" && ev.AgentID != attempt.AgentID {
			if strict {
				addErr(res, "ZCL_E_ID_MISMATCH", "note agentId does not match attempt.json", path)
				return
			}
			addWarn(res, "ZCL_W_ID_MISMATCH", "note agentId does not match attempt.json", path)
		}
		if ev.Message != "" && ev.Data != nil {
			addErr(res, "ZCL_E_CONTRACT", "note must set only one of message or data", path)
			return
		}
		if ev.Message != "" && len([]byte(ev.Message)) > schema.NoteMessageMaxBytesV1 {
			addErr(res, "ZCL_E_BOUNDS", "note message exceeds bounds", path)
			return
		}
		if ev.Data != nil && len(ev.Data) > schema.NoteDataMaxBytesV1 {
			addErr(res, "ZCL_E_BOUNDS", "note data exceeds bounds", path)
			return
		}
	}
	if err := sc.Err(); err != nil {
		addErr(res, "ZCL_E_IO", err.Error(), path)
		return
	}
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
	for sc.Scan() {
		line := sc.Bytes()
		if len(bytesTrim(line)) == 0 {
			if strict {
				addErr(res, "ZCL_E_INVALID_JSONL", "empty line in captures.jsonl", path)
				return
			}
			continue
		}
		var ev schema.CaptureEventV1
		if err := json.Unmarshal(line, &ev); err != nil {
			addErr(res, "ZCL_E_INVALID_JSONL", "invalid jsonl line in captures.jsonl", path)
			return
		}
		if ev.V != 1 {
			addErr(res, "ZCL_E_SCHEMA_UNSUPPORTED", "unsupported capture event version", path)
			return
		}
		if ev.RunID != attempt.RunID || ev.AttemptID != attempt.AttemptID || ev.MissionID != attempt.MissionID {
			addErr(res, "ZCL_E_ID_MISMATCH", "capture ids do not match attempt.json", path)
			return
		}
		if attempt.SuiteID != "" && ev.SuiteID != attempt.SuiteID {
			if strict {
				addErr(res, "ZCL_E_ID_MISMATCH", "capture suiteId does not match attempt.json", path)
				return
			}
			addWarn(res, "ZCL_W_ID_MISMATCH", "capture suiteId does not match attempt.json", path)
		}
		if ev.AgentID != "" && attempt.AgentID != "" && ev.AgentID != attempt.AgentID {
			if strict {
				addErr(res, "ZCL_E_ID_MISMATCH", "capture agentId does not match attempt.json", path)
				return
			}
			addWarn(res, "ZCL_W_ID_MISMATCH", "capture agentId does not match attempt.json", path)
		}
		if strings.TrimSpace(ev.TS) == "" {
			addErr(res, "ZCL_E_CONTRACT", "capture ts is missing", path)
			return
		}
		if _, err := time.Parse(time.RFC3339Nano, ev.TS); err != nil {
			if _, err2 := time.Parse(time.RFC3339, ev.TS); err2 != nil {
				if strict {
					addErr(res, "ZCL_E_CONTRACT", "capture ts is not RFC3339", path)
					return
				}
				addWarn(res, "ZCL_W_CONTRACT", "capture ts is not RFC3339", path)
			}
		}
		if strings.TrimSpace(ev.Tool) == "" || strings.TrimSpace(ev.Op) == "" {
			addErr(res, "ZCL_E_CONTRACT", "capture tool/op is missing", path)
			return
		}
		if ev.MaxBytes <= 0 {
			addErr(res, "ZCL_E_CONTRACT", "capture maxBytes must be > 0", path)
			return
		}
		if len(ev.RedactionsApplied) > schema.RedactionsAppliedMaxCountV1 {
			addErr(res, "ZCL_E_BOUNDS", "capture redactionsApplied exceeds bounds", path)
			return
		}
		for _, n := range ev.RedactionsApplied {
			if len([]byte(n)) > schema.RedactionNameMaxBytesV1 {
				addErr(res, "ZCL_E_BOUNDS", "capture redaction name exceeds bounds", path)
				return
			}
		}
		if len(ev.Input) > 0 {
			if len(ev.Input) > schema.ToolInputMaxBytesV1 {
				addErr(res, "ZCL_E_BOUNDS", "capture input exceeds bounds", path)
				return
			}
			if !json.Valid(ev.Input) {
				addErr(res, "ZCL_E_CONTRACT", "capture input is not valid json", path)
				return
			}
		}

		checkRel := func(val string) {
			val = strings.TrimSpace(val)
			if val == "" {
				return
			}
			if filepath.IsAbs(val) || strings.Contains(val, "..") {
				addErr(res, "ZCL_E_CONTRACT", "capture path must be a safe relative path", path)
				return
			}
			abs := filepath.Join(attemptDir, val)
			if !requireContained(attemptDir, abs, res) {
				return
			}
			info, err := os.Stat(abs)
			if err != nil {
				if strict && os.IsNotExist(err) {
					addErr(res, "ZCL_E_MISSING_ARTIFACT", "capture file is missing", abs)
					return
				}
				if os.IsNotExist(err) {
					addWarn(res, "ZCL_W_MISSING_ARTIFACT", "capture file is missing", abs)
				}
				return
			}
			if info.Size() > ev.MaxBytes {
				addErr(res, "ZCL_E_BOUNDS", "capture file exceeds maxBytes", abs)
				return
			}
		}

		checkRel(ev.StdoutPath)
		checkRel(ev.StderrPath)
	}
	if err := sc.Err(); err != nil {
		addErr(res, "ZCL_E_IO", err.Error(), path)
		return
	}
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
	if ev.Tool != "cli" && ev.Tool != "mcp" {
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

	switch ev.Tool {
	case "cli":
		if ev.Op != "exec" {
			return
		}
		argv, ok := m["argv"].([]any)
		if !ok || len(argv) == 0 {
			if strict {
				addErr(res, "ZCL_E_CONTRACT", "cli exec input must include non-empty argv", path)
				return
			}
			addWarn(res, "ZCL_W_CONTRACT", "cli exec input should include non-empty argv", path)
		}
	case "mcp":
		method, ok := m["method"].(string)
		if !ok || strings.TrimSpace(method) == "" {
			if strict {
				addErr(res, "ZCL_E_CONTRACT", "mcp input must include method", path)
				return
			}
			addWarn(res, "ZCL_W_CONTRACT", "mcp input should include method", path)
		}
	}
}
