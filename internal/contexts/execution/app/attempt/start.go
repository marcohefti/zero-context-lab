package attempt

import (
	"encoding/json"
	"fmt"
	"github.com/marcohefti/zero-context-lab/internal/kernel/artifacts"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"time"

	"github.com/marcohefti/zero-context-lab/internal/kernel/ids"
	"github.com/marcohefti/zero-context-lab/internal/kernel/schema"
	"github.com/marcohefti/zero-context-lab/internal/kernel/store"
)

type StartOpts struct {
	OutRoot        string
	RunID          string
	SuiteID        string
	MissionID      string
	AgentID        string
	IsolationModel string
	Mode           string
	Retry          int
	Prompt         string
	TimeoutMs      int64
	TimeoutStart   string
	Blind          bool
	BlindTerms     []string
	SuiteSnapshot  any
}

type StartResult struct {
	OK             bool              `json:"ok"`
	RunID          string            `json:"runId"`
	SuiteID        string            `json:"suiteId"`
	MissionID      string            `json:"missionId"`
	AttemptID      string            `json:"attemptId"`
	AgentID        string            `json:"agentId,omitempty"`
	IsolationModel string            `json:"isolationModel,omitempty"`
	Mode           string            `json:"mode"`
	OutDir         string            `json:"outDir"`
	OutDirAbs      string            `json:"outDirAbs"`
	AttemptEnvFile string            `json:"attemptEnvFile,omitempty"`
	Env            map[string]string `json:"env"`
	CreatedAt      string            `json:"createdAt"`
}

func Start(now time.Time, opts StartOpts) (*StartResult, error) {
	normalized, mode, outRoot, err := normalizeStartOpts(opts)
	if err != nil {
		return nil, err
	}
	runID, err := resolveRunID(now, normalized.RunID)
	if err != nil {
		return nil, err
	}
	runDir, attemptsDir, err := ensureRunDirs(outRoot, runID)
	if err != nil {
		return nil, err
	}
	if err := ensureSuiteSnapshot(runDir, normalized.SuiteSnapshot, runID); err != nil {
		return nil, err
	}
	if err := ensureRunJSON(runDir, runID, normalized.SuiteID, now); err != nil {
		return nil, err
	}
	attemptID, outDir, outDirAbs, err := createAttemptDir(attemptsDir, normalized.MissionID, normalized.Retry)
	if err != nil {
		return nil, err
	}
	if err := writePromptSnapshot(outDir, normalized.Prompt); err != nil {
		return nil, err
	}
	attemptMeta, scratchAbs, err := buildAttemptMeta(now, normalized, runID, attemptID, mode, outRoot)
	if err != nil {
		return nil, err
	}
	env := buildAttemptEnv(normalized, runID, attemptID, outDirAbs, scratchAbs)
	if err := store.WriteJSONAtomic(filepath.Join(outDir, artifacts.AttemptJSON), attemptMeta); err != nil {
		return nil, err
	}
	attemptEnvFile := filepath.Join(outDir, attemptMeta.AttemptEnvSH)
	if err := WriteEnvSh(attemptEnvFile, env); err != nil {
		return nil, err
	}

	return &StartResult{
		OK:             true,
		RunID:          runID,
		SuiteID:        opts.SuiteID,
		MissionID:      opts.MissionID,
		AttemptID:      attemptID,
		AgentID:        opts.AgentID,
		IsolationModel: opts.IsolationModel,
		Mode:           mode,
		OutDir:         outDir,
		OutDirAbs:      outDirAbs,
		AttemptEnvFile: attemptEnvFile,
		Env:            env,
		CreatedAt:      now.UTC().Format(time.RFC3339Nano),
	}, nil
}

func normalizeStartOpts(opts StartOpts) (StartOpts, string, string, error) {
	opts.SuiteID = strings.TrimSpace(opts.SuiteID)
	opts.MissionID = strings.TrimSpace(opts.MissionID)
	opts.AgentID = strings.TrimSpace(opts.AgentID)
	opts.IsolationModel = strings.TrimSpace(opts.IsolationModel)
	opts.Mode = strings.TrimSpace(opts.Mode)
	opts.RunID = strings.TrimSpace(opts.RunID)
	opts.OutRoot = strings.TrimSpace(opts.OutRoot)
	if opts.SuiteID == "" {
		return StartOpts{}, "", "", fmt.Errorf("missing --suite")
	}
	if opts.MissionID == "" {
		return StartOpts{}, "", "", fmt.Errorf("missing --mission")
	}
	opts.SuiteID = ids.SanitizeComponent(opts.SuiteID)
	if opts.SuiteID == "" {
		return StartOpts{}, "", "", fmt.Errorf("invalid --suite (no usable characters)")
	}
	opts.MissionID = ids.SanitizeComponent(opts.MissionID)
	if opts.MissionID == "" {
		return StartOpts{}, "", "", fmt.Errorf("invalid --mission (no usable characters)")
	}
	mode := opts.Mode
	if mode == "" {
		mode = "discovery"
	}
	if mode != "discovery" && mode != "ci" {
		return StartOpts{}, "", "", fmt.Errorf("invalid --mode (expected discovery|ci)")
	}
	if !schema.IsValidIsolationModelV1(opts.IsolationModel) {
		return StartOpts{}, "", "", fmt.Errorf("invalid --isolation-model (expected %s|%s)", schema.IsolationModelProcessRunnerV1, schema.IsolationModelNativeSpawnV1)
	}
	outRoot := opts.OutRoot
	if outRoot == "" {
		outRoot = ".zcl"
	}
	return opts, mode, outRoot, nil
}

func resolveRunID(now time.Time, runID string) (string, error) {
	if runID == "" {
		return ids.NewRunID(now)
	}
	if ids.IsValidRunID(runID) {
		return runID, nil
	}
	return "", fmt.Errorf("invalid --run-id (expected format YYYYMMDD-HHMMSSZ-<hex6>)")
}

func ensureRunDirs(outRoot string, runID string) (string, string, error) {
	runDir := filepath.Join(outRoot, "runs", runID)
	attemptsDir := filepath.Join(runDir, "attempts")
	if err := os.MkdirAll(attemptsDir, 0o755); err != nil {
		return "", "", err
	}
	return runDir, attemptsDir, nil
}

func ensureSuiteSnapshot(runDir string, suiteSnapshot any, runID string) error {
	if suiteSnapshot == nil {
		return nil
	}
	b, err := json.Marshal(suiteSnapshot)
	if err != nil {
		return err
	}
	var v any
	if err := json.Unmarshal(b, &v); err != nil {
		return err
	}
	suiteJSONPath := filepath.Join(runDir, artifacts.SuiteJSON)
	_, statErr := os.Stat(suiteJSONPath)
	if statErr == nil {
		existing, err := os.ReadFile(suiteJSONPath)
		if err != nil {
			return err
		}
		var existingAny any
		if err := json.Unmarshal(existing, &existingAny); err != nil {
			return err
		}
		if !reflect.DeepEqual(existingAny, v) {
			return fmt.Errorf("suite.json mismatch for runId=%s", runID)
		}
		return nil
	}
	if os.IsNotExist(statErr) {
		return store.WriteJSONAtomic(suiteJSONPath, v)
	}
	return statErr
}

func ensureRunJSON(runDir string, runID string, suiteID string, now time.Time) error {
	runJSONPath := filepath.Join(runDir, artifacts.RunJSON)
	_, statErr := os.Stat(runJSONPath)
	if statErr == nil {
		return validateExistingRunJSON(runJSONPath, runID, suiteID)
	}
	if !os.IsNotExist(statErr) {
		return statErr
	}
	runMeta := schema.RunJSONV1{
		SchemaVersion:         schema.RunSchemaV1,
		ArtifactLayoutVersion: schema.ArtifactLayoutVersionV1,
		RunID:                 runID,
		SuiteID:               suiteID,
		CreatedAt:             now.UTC().Format(time.RFC3339Nano),
	}
	return store.WriteJSONAtomic(runJSONPath, runMeta)
}

func validateExistingRunJSON(path string, runID string, suiteID string) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var existing schema.RunJSONV1
	if err := json.Unmarshal(raw, &existing); err != nil {
		return err
	}
	if existing.ArtifactLayoutVersion != schema.ArtifactLayoutVersionV1 {
		return fmt.Errorf("run.json mismatch: expected artifactLayoutVersion=%d", schema.ArtifactLayoutVersionV1)
	}
	if existing.RunID != runID {
		return fmt.Errorf("run.json mismatch: expected runId=%s", runID)
	}
	if existing.SuiteID != suiteID {
		return fmt.Errorf("run.json mismatch: expected suiteId=%s", suiteID)
	}
	return nil
}

func createAttemptDir(attemptsDir string, missionID string, retry int) (string, string, string, error) {
	count, err := store.CountChildDirs(attemptsDir)
	if err != nil {
		return "", "", "", err
	}
	attemptID := ids.NewAttemptID(count+1, missionID, retry)
	outDir := filepath.Join(attemptsDir, attemptID)
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return "", "", "", err
	}
	outDirAbs, err := filepath.Abs(outDir)
	if err != nil {
		return "", "", "", err
	}
	return attemptID, outDir, outDirAbs, nil
}

func writePromptSnapshot(outDir string, prompt string) error {
	if strings.TrimSpace(prompt) == "" {
		return nil
	}
	return store.WriteFileAtomic(filepath.Join(outDir, artifacts.PromptTXT), []byte(prompt))
}

func buildAttemptMeta(now time.Time, opts StartOpts, runID string, attemptID string, mode string, outRoot string) (schema.AttemptJSONV1, string, error) {
	meta := schema.AttemptJSONV1{
		SchemaVersion:  schema.AttemptSchemaV1,
		RunID:          runID,
		SuiteID:        opts.SuiteID,
		MissionID:      opts.MissionID,
		AttemptID:      attemptID,
		AgentID:        opts.AgentID,
		IsolationModel: opts.IsolationModel,
		Mode:           mode,
		StartedAt:      now.UTC().Format(time.RFC3339Nano),
		Blind:          opts.Blind,
		BlindTerms:     append([]string(nil), opts.BlindTerms...),
		AttemptEnvSH:   schema.AttemptEnvShFileNameV1,
	}
	if err := applyAttemptTimeouts(&meta, opts.TimeoutMs, opts.TimeoutStart, mode); err != nil {
		return schema.AttemptJSONV1{}, "", err
	}
	scratchRel := filepath.Join("tmp", runID, attemptID)
	scratchAbs, err := filepath.Abs(filepath.Join(outRoot, scratchRel))
	if err != nil {
		return schema.AttemptJSONV1{}, "", err
	}
	if err := os.MkdirAll(scratchAbs, 0o755); err != nil {
		return schema.AttemptJSONV1{}, "", err
	}
	meta.ScratchDir = scratchRel
	return meta, scratchAbs, nil
}

func applyAttemptTimeouts(meta *schema.AttemptJSONV1, timeoutMs int64, timeoutStart string, mode string) error {
	if timeoutMs <= 0 {
		return nil
	}
	start := strings.TrimSpace(timeoutStart)
	if start == "" {
		if mode == "ci" {
			start = schema.TimeoutStartAttemptStartV1
		} else {
			start = schema.TimeoutStartFirstToolCallV1
		}
	}
	if !schema.IsValidTimeoutStartV1(start) {
		return fmt.Errorf("invalid --timeout-start (expected attempt_start|first_tool_call)")
	}
	meta.TimeoutMs = timeoutMs
	meta.TimeoutStart = start
	return nil
}

func buildAttemptEnv(opts StartOpts, runID string, attemptID string, outDirAbs string, scratchAbs string) map[string]string {
	env := map[string]string{
		"ZCL_RUN_ID":     runID,
		"ZCL_SUITE_ID":   opts.SuiteID,
		"ZCL_MISSION_ID": opts.MissionID,
		"ZCL_ATTEMPT_ID": attemptID,
		"ZCL_OUT_DIR":    outDirAbs,
		"ZCL_TMP_DIR":    scratchAbs,
	}
	if opts.AgentID != "" {
		env["ZCL_AGENT_ID"] = opts.AgentID
	}
	if opts.IsolationModel != "" {
		env["ZCL_ISOLATION_MODEL"] = opts.IsolationModel
	}
	return env
}
