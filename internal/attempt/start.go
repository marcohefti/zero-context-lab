package attempt

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"time"

	"github.com/marcohefti/zero-context-lab/internal/ids"
	"github.com/marcohefti/zero-context-lab/internal/schema"
	"github.com/marcohefti/zero-context-lab/internal/store"
)

type StartOpts struct {
	OutRoot       string
	RunID         string
	SuiteID       string
	MissionID     string
	AgentID       string
	Mode          string
	Retry         int
	Prompt        string
	TimeoutMs     int64
	SuiteSnapshot any
}

type StartResult struct {
	OK        bool              `json:"ok"`
	RunID     string            `json:"runId"`
	SuiteID   string            `json:"suiteId"`
	MissionID string            `json:"missionId"`
	AttemptID string            `json:"attemptId"`
	AgentID   string            `json:"agentId,omitempty"`
	Mode      string            `json:"mode"`
	OutDir    string            `json:"outDir"`
	OutDirAbs string            `json:"outDirAbs"`
	Env       map[string]string `json:"env"`
	CreatedAt string            `json:"createdAt"`
}

func Start(now time.Time, opts StartOpts) (*StartResult, error) {
	opts.SuiteID = strings.TrimSpace(opts.SuiteID)
	opts.MissionID = strings.TrimSpace(opts.MissionID)
	opts.AgentID = strings.TrimSpace(opts.AgentID)
	opts.Mode = strings.TrimSpace(opts.Mode)
	opts.RunID = strings.TrimSpace(opts.RunID)
	opts.OutRoot = strings.TrimSpace(opts.OutRoot)

	if opts.SuiteID == "" {
		return nil, fmt.Errorf("missing --suite")
	}
	if opts.MissionID == "" {
		return nil, fmt.Errorf("missing --mission")
	}

	// Canonicalize ids (stable, path-safe, low-friction).
	opts.SuiteID = ids.SanitizeComponent(opts.SuiteID)
	if opts.SuiteID == "" {
		return nil, fmt.Errorf("invalid --suite (no usable characters)")
	}
	opts.MissionID = ids.SanitizeComponent(opts.MissionID)
	if opts.MissionID == "" {
		return nil, fmt.Errorf("invalid --mission (no usable characters)")
	}

	mode := opts.Mode
	if mode == "" {
		mode = "discovery"
	}
	if mode != "discovery" && mode != "ci" {
		return nil, fmt.Errorf("invalid --mode (expected discovery|ci)")
	}

	outRoot := opts.OutRoot
	if outRoot == "" {
		outRoot = ".zcl"
	}

	runID := opts.RunID
	if runID == "" {
		var err error
		runID, err = ids.NewRunID(now)
		if err != nil {
			return nil, err
		}
	} else if !ids.IsValidRunID(runID) {
		return nil, fmt.Errorf("invalid --run-id (expected format YYYYMMDD-HHMMSSZ-<hex6>)")
	}

	runDir := filepath.Join(outRoot, "runs", runID)
	attemptsDir := filepath.Join(runDir, "attempts")
	if err := os.MkdirAll(attemptsDir, 0o755); err != nil {
		return nil, err
	}

	// Optional: snapshot suite.json (canonical JSON) into the run directory.
	if opts.SuiteSnapshot != nil {
		b, err := json.Marshal(opts.SuiteSnapshot)
		if err != nil {
			return nil, err
		}
		var v any
		if err := json.Unmarshal(b, &v); err != nil {
			return nil, err
		}

		suiteJSONPath := filepath.Join(runDir, "suite.json")
		if _, err := os.Stat(suiteJSONPath); err == nil {
			existing, err := os.ReadFile(suiteJSONPath)
			if err != nil {
				return nil, err
			}
			var existingAny any
			if err := json.Unmarshal(existing, &existingAny); err != nil {
				return nil, err
			}
			// Compare semantic JSON, not bytes (snapshot is canonicalized by WriteJSONAtomic).
			if !reflect.DeepEqual(existingAny, v) {
				return nil, fmt.Errorf("suite.json mismatch for runId=%s", runID)
			}
		} else if os.IsNotExist(err) {
			if err := store.WriteJSONAtomic(suiteJSONPath, v); err != nil {
				return nil, err
			}
		} else if err != nil {
			return nil, err
		}
	}

	// Create run.json if missing (or validate it if present).
	runJSONPath := filepath.Join(runDir, "run.json")
	if _, err := os.Stat(runJSONPath); err == nil {
		raw, err := os.ReadFile(runJSONPath)
		if err != nil {
			return nil, err
		}
		var existing schema.RunJSONV1
		if err := json.Unmarshal(raw, &existing); err != nil {
			return nil, err
		}
		if existing.RunID != runID {
			return nil, fmt.Errorf("run.json mismatch: expected runId=%s", runID)
		}
		if existing.SuiteID != opts.SuiteID {
			return nil, fmt.Errorf("run.json mismatch: expected suiteId=%s", opts.SuiteID)
		}
	} else if os.IsNotExist(err) {
		runMeta := schema.RunJSONV1{
			SchemaVersion: schema.RunSchemaV1,
			RunID:         runID,
			SuiteID:       opts.SuiteID,
			CreatedAt:     now.UTC().Format(time.RFC3339Nano),
		}
		if err := store.WriteJSONAtomic(runJSONPath, runMeta); err != nil {
			return nil, err
		}
	} else if err != nil {
		return nil, err
	}

	count, err := store.CountChildDirs(attemptsDir)
	if err != nil {
		return nil, err
	}
	attemptID := ids.NewAttemptID(count+1, opts.MissionID, opts.Retry)
	outDir := filepath.Join(attemptsDir, attemptID)
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return nil, err
	}
	outDirAbs, err := filepath.Abs(outDir)
	if err != nil {
		return nil, err
	}

	if strings.TrimSpace(opts.Prompt) != "" {
		if err := store.WriteFileAtomic(filepath.Join(outDir, "prompt.txt"), []byte(opts.Prompt)); err != nil {
			return nil, err
		}
	}

	attemptMeta := schema.AttemptJSONV1{
		SchemaVersion: schema.AttemptSchemaV1,
		RunID:         runID,
		SuiteID:       opts.SuiteID,
		MissionID:     opts.MissionID,
		AttemptID:     attemptID,
		AgentID:       opts.AgentID,
		Mode:          mode,
		StartedAt:     now.UTC().Format(time.RFC3339Nano),
	}
	if opts.TimeoutMs > 0 {
		attemptMeta.TimeoutMs = opts.TimeoutMs
	}
	if err := store.WriteJSONAtomic(filepath.Join(outDir, "attempt.json"), attemptMeta); err != nil {
		return nil, err
	}

	env := map[string]string{
		"ZCL_RUN_ID":     runID,
		"ZCL_SUITE_ID":   opts.SuiteID,
		"ZCL_MISSION_ID": opts.MissionID,
		"ZCL_ATTEMPT_ID": attemptID,
		"ZCL_OUT_DIR":    outDirAbs,
	}
	if opts.AgentID != "" {
		env["ZCL_AGENT_ID"] = opts.AgentID
	}

	return &StartResult{
		OK:        true,
		RunID:     runID,
		SuiteID:   opts.SuiteID,
		MissionID: opts.MissionID,
		AttemptID: attemptID,
		AgentID:   opts.AgentID,
		Mode:      mode,
		OutDir:    outDir,
		OutDirAbs: outDirAbs,
		Env:       env,
		CreatedAt: now.UTC().Format(time.RFC3339Nano),
	}, nil
}
