package attempt

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/marcohefti/zero-context-lab/internal/ids"
	"github.com/marcohefti/zero-context-lab/internal/store"
)

type StartOpts struct {
	OutRoot   string
	RunID     string
	SuiteID   string
	MissionID string
	AgentID   string
	Mode      string
	Retry     int
}

type RunMeta struct {
	SchemaVersion int    `json:"schemaVersion"`
	RunID         string `json:"runId"`
	SuiteID       string `json:"suiteId"`
	CreatedAt     string `json:"createdAt"`
}

type AttemptMeta struct {
	SchemaVersion int    `json:"schemaVersion"`
	RunID         string `json:"runId"`
	SuiteID       string `json:"suiteId"`
	MissionID     string `json:"missionId"`
	AttemptID     string `json:"attemptId"`
	AgentID       string `json:"agentId,omitempty"`
	Mode          string `json:"mode"`
	StartedAt     string `json:"startedAt"`
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
	}

	runDir := filepath.Join(outRoot, "runs", runID)
	attemptsDir := filepath.Join(runDir, "attempts")
	if err := os.MkdirAll(attemptsDir, 0o755); err != nil {
		return nil, err
	}

	// Create run.json if missing (or validate it if present).
	runJSONPath := filepath.Join(runDir, "run.json")
	if _, err := os.Stat(runJSONPath); err == nil {
		raw, err := os.ReadFile(runJSONPath)
		if err != nil {
			return nil, err
		}
		var existing RunMeta
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
		runMeta := RunMeta{
			SchemaVersion: 1,
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

	attemptMeta := AttemptMeta{
		SchemaVersion: 1,
		RunID:         runID,
		SuiteID:       opts.SuiteID,
		MissionID:     opts.MissionID,
		AttemptID:     attemptID,
		AgentID:       opts.AgentID,
		Mode:          mode,
		StartedAt:     now.UTC().Format(time.RFC3339Nano),
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
