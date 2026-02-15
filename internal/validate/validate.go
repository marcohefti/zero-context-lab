package validate

import (
	"bufio"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/marcohefti/zero-context-lab/internal/schema"
)

type CliError struct {
	Code    string
	Message string
	Path    string
}

func (e *CliError) Error() string {
	if e.Path == "" {
		return e.Code + ": " + e.Message
	}
	return e.Code + ": " + e.Message + " (" + e.Path + ")"
}

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
		return Result{}, err
	}
	info, err := os.Stat(abs)
	if err != nil {
		return Result{}, err
	}
	if !info.IsDir() {
		return Result{}, &CliError{Code: "ZCL_E_USAGE", Message: "target must be a directory", Path: abs}
	}

	// Determine type by presence of attempt.json vs run.json.
	if _, err := os.Stat(filepath.Join(abs, "attempt.json")); err == nil {
		return validateAttempt(abs, strict)
	}
	if _, err := os.Stat(filepath.Join(abs, "run.json")); err == nil {
		return validateRun(abs, strict)
	}
	return Result{}, &CliError{Code: "ZCL_E_USAGE", Message: "target does not look like an attemptDir or runDir", Path: abs}
}

func validateRun(runDir string, strict bool) (Result, error) {
	res := Result{OK: true, Strict: strict, Target: "run", Path: runDir}

	runJSONPath := filepath.Join(runDir, "run.json")
	if err := requireFile(runJSONPath, strict, &res); err != nil {
		return Result{}, err
	}
	if err := requireContained(runDir, runJSONPath); err != nil {
		return Result{}, err
	}
	raw, err := os.ReadFile(runJSONPath)
	if err != nil {
		return Result{}, err
	}
	var run schema.RunJSONV1
	if err := json.Unmarshal(raw, &run); err != nil {
		return Result{}, &CliError{Code: "ZCL_E_INVALID_JSON", Message: "run.json is not valid json", Path: runJSONPath}
	}
	if run.SchemaVersion != schema.ArtifactSchemaV1 {
		return Result{}, &CliError{Code: "ZCL_E_SCHEMA_UNSUPPORTED", Message: "unsupported run.json schemaVersion", Path: runJSONPath}
	}
	if base := filepath.Base(runDir); run.RunID != base {
		return Result{}, &CliError{Code: "ZCL_E_ID_MISMATCH", Message: "runId does not match directory name", Path: runJSONPath}
	}

	attemptsDir := filepath.Join(runDir, "attempts")
	entries, err := os.ReadDir(attemptsDir)
	if err != nil {
		if strict && os.IsNotExist(err) {
			return Result{}, &CliError{Code: "ZCL_E_MISSING_ARTIFACT", Message: "missing attempts directory", Path: attemptsDir}
		}
		// Best effort: no attempts, still OK.
		return res, nil
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		attemptDir := filepath.Join(attemptsDir, e.Name())
		if _, err := validateAttempt(attemptDir, strict); err != nil {
			return Result{}, err
		}
	}

	return res, nil
}

func validateAttempt(attemptDir string, strict bool) (Result, error) {
	res := Result{OK: true, Strict: strict, Target: "attempt", Path: attemptDir}

	attemptJSONPath := filepath.Join(attemptDir, "attempt.json")
	if err := requireFile(attemptJSONPath, true, &res); err != nil { // attempt.json is always required
		return Result{}, err
	}
	if err := requireContained(attemptDir, attemptJSONPath); err != nil {
		return Result{}, err
	}
	raw, err := os.ReadFile(attemptJSONPath)
	if err != nil {
		return Result{}, err
	}
	var attempt schema.AttemptJSONV1
	if err := json.Unmarshal(raw, &attempt); err != nil {
		return Result{}, &CliError{Code: "ZCL_E_INVALID_JSON", Message: "attempt.json is not valid json", Path: attemptJSONPath}
	}
	if attempt.SchemaVersion != schema.ArtifactSchemaV1 {
		return Result{}, &CliError{Code: "ZCL_E_SCHEMA_UNSUPPORTED", Message: "unsupported attempt.json schemaVersion", Path: attemptJSONPath}
	}
	if base := filepath.Base(attemptDir); attempt.AttemptID != base {
		return Result{}, &CliError{Code: "ZCL_E_ID_MISMATCH", Message: "attemptId does not match directory name", Path: attemptJSONPath}
	}
	// Validate runId vs the path segment if it matches the standard layout: .../runs/<runId>/attempts/<attemptId>
	if runDir := filepath.Dir(filepath.Dir(attemptDir)); filepath.Base(runDir) == attempt.RunID {
		// ok
	}

	tracePath := filepath.Join(attemptDir, "tool.calls.jsonl")
	feedbackPath := filepath.Join(attemptDir, "feedback.json")

	if err := requireFile(tracePath, strict, &res); err != nil {
		return Result{}, err
	}
	if err := requireFile(feedbackPath, strict, &res); err != nil {
		return Result{}, err
	}

	if _, err := os.Stat(tracePath); err == nil {
		if err := requireContained(attemptDir, tracePath); err != nil {
			return Result{}, err
		}
		if err := validateTrace(tracePath, attempt, strict); err != nil {
			return Result{}, err
		}
	}

	if _, err := os.Stat(feedbackPath); err == nil {
		if err := requireContained(attemptDir, feedbackPath); err != nil {
			return Result{}, err
		}
		if err := validateFeedback(feedbackPath, attempt, strict); err != nil {
			return Result{}, err
		}
	}

	notesPath := filepath.Join(attemptDir, "notes.jsonl")
	if _, err := os.Stat(notesPath); err == nil {
		if err := requireContained(attemptDir, notesPath); err != nil {
			return Result{}, err
		}
		if err := validateNotes(notesPath, attempt, strict); err != nil {
			return Result{}, err
		}
	}

	return res, nil
}

func validateTrace(path string, attempt schema.AttemptJSONV1, strict bool) error {
	f, err := os.Open(path)
	if err != nil {
		return err
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
				return &CliError{Code: "ZCL_E_INVALID_JSONL", Message: "empty line in tool.calls.jsonl", Path: path}
			}
			continue
		}
		var ev schema.TraceEventV1
		if err := json.Unmarshal(line, &ev); err != nil {
			return &CliError{Code: "ZCL_E_INVALID_JSONL", Message: "invalid jsonl line in tool.calls.jsonl", Path: path}
		}
		n++
		if ev.V != schema.TraceSchemaV1 {
			return &CliError{Code: "ZCL_E_SCHEMA_UNSUPPORTED", Message: "unsupported trace event version", Path: path}
		}
		if ev.RunID != attempt.RunID || ev.AttemptID != attempt.AttemptID || ev.MissionID != attempt.MissionID {
			return &CliError{Code: "ZCL_E_ID_MISMATCH", Message: "trace ids do not match attempt.json", Path: path}
		}
		if len(ev.IO.OutPreview) > schema.PreviewMaxBytesV1 || len(ev.IO.ErrPreview) > schema.PreviewMaxBytesV1 {
			return &CliError{Code: "ZCL_E_BOUNDS", Message: "trace preview exceeds bounds", Path: path}
		}
		if len(ev.Input) > schema.ToolInputMaxBytesV1 {
			return &CliError{Code: "ZCL_E_BOUNDS", Message: "trace input exceeds bounds", Path: path}
		}
	}
	if err := sc.Err(); err != nil {
		return err
	}
	if strict && n == 0 {
		return &CliError{Code: "ZCL_E_MISSING_EVIDENCE", Message: "tool.calls.jsonl is empty", Path: path}
	}
	return nil
}

func validateFeedback(path string, attempt schema.AttemptJSONV1, strict bool) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if len(raw) > schema.FeedbackMaxBytesV1*2 {
		// feedback.json includes envelope + possible pretty-print; enforce loose cap.
		return &CliError{Code: "ZCL_E_BOUNDS", Message: "feedback.json exceeds bounds", Path: path}
	}
	var fb schema.FeedbackJSONV1
	if err := json.Unmarshal(raw, &fb); err != nil {
		return &CliError{Code: "ZCL_E_INVALID_JSON", Message: "feedback.json is not valid json", Path: path}
	}
	if fb.SchemaVersion != schema.ArtifactSchemaV1 {
		return &CliError{Code: "ZCL_E_SCHEMA_UNSUPPORTED", Message: "unsupported feedback.json schemaVersion", Path: path}
	}
	if fb.RunID != attempt.RunID || fb.AttemptID != attempt.AttemptID || fb.MissionID != attempt.MissionID {
		return &CliError{Code: "ZCL_E_ID_MISMATCH", Message: "feedback ids do not match attempt.json", Path: path}
	}
	if fb.Result != "" && fb.ResultJSON != nil {
		return &CliError{Code: "ZCL_E_CONTRACT", Message: "feedback must set exactly one of result or resultJson", Path: path}
	}
	if fb.Result == "" && fb.ResultJSON == nil {
		if strict {
			return &CliError{Code: "ZCL_E_CONTRACT", Message: "feedback missing result/resultJson", Path: path}
		}
	}
	if fb.Result != "" && len([]byte(fb.Result)) > schema.FeedbackMaxBytesV1 {
		return &CliError{Code: "ZCL_E_BOUNDS", Message: "feedback result exceeds bounds", Path: path}
	}
	if fb.ResultJSON != nil && len(fb.ResultJSON) > schema.FeedbackMaxBytesV1 {
		return &CliError{Code: "ZCL_E_BOUNDS", Message: "feedback resultJson exceeds bounds", Path: path}
	}
	return nil
}

func validateNotes(path string, attempt schema.AttemptJSONV1, strict bool) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := sc.Bytes()
		if len(bytesTrim(line)) == 0 {
			if strict {
				return &CliError{Code: "ZCL_E_INVALID_JSONL", Message: "empty line in notes.jsonl", Path: path}
			}
			continue
		}
		var ev schema.NoteEventV1
		if err := json.Unmarshal(line, &ev); err != nil {
			return &CliError{Code: "ZCL_E_INVALID_JSONL", Message: "invalid jsonl line in notes.jsonl", Path: path}
		}
		if ev.V != schema.TraceSchemaV1 {
			return &CliError{Code: "ZCL_E_SCHEMA_UNSUPPORTED", Message: "unsupported note event version", Path: path}
		}
		if ev.RunID != attempt.RunID || ev.AttemptID != attempt.AttemptID || ev.MissionID != attempt.MissionID {
			return &CliError{Code: "ZCL_E_ID_MISMATCH", Message: "note ids do not match attempt.json", Path: path}
		}
		if ev.Message != "" && ev.Data != nil {
			return &CliError{Code: "ZCL_E_CONTRACT", Message: "note must set only one of message or data", Path: path}
		}
		if ev.Message != "" && len([]byte(ev.Message)) > schema.NoteMessageMaxBytesV1 {
			return &CliError{Code: "ZCL_E_BOUNDS", Message: "note message exceeds bounds", Path: path}
		}
		if ev.Data != nil && len(ev.Data) > schema.NoteDataMaxBytesV1 {
			return &CliError{Code: "ZCL_E_BOUNDS", Message: "note data exceeds bounds", Path: path}
		}
	}
	if err := sc.Err(); err != nil {
		return err
	}
	return nil
}

func requireFile(path string, required bool, res *Result) error {
	_, err := os.Stat(path)
	if err == nil {
		return nil
	}
	if os.IsNotExist(err) {
		if required {
			return &CliError{Code: "ZCL_E_MISSING_ARTIFACT", Message: "missing required artifact", Path: path}
		}
		return nil
	}
	return err
}

func requireContained(root, path string) error {
	rootEval, err := filepath.EvalSymlinks(root)
	if err != nil {
		return err
	}
	pEval, err := filepath.EvalSymlinks(path)
	if err != nil {
		// If the artifact doesn't exist yet, containment doesn't apply.
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	rootEval = filepath.Clean(rootEval)
	pEval = filepath.Clean(pEval)
	sep := string(os.PathSeparator)
	if !strings.HasPrefix(pEval, rootEval+sep) && pEval != rootEval {
		return &CliError{Code: "ZCL_E_CONTAINMENT", Message: "artifact path escapes attempt/run directory (symlink traversal)", Path: path}
	}
	return nil
}

func IsCliError(err error, code string) bool {
	var e *CliError
	if errors.As(err, &e) {
		return e.Code == code
	}
	return false
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
