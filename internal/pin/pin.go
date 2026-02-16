package pin

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/marcohefti/zero-context-lab/internal/ids"
	"github.com/marcohefti/zero-context-lab/internal/schema"
	"github.com/marcohefti/zero-context-lab/internal/store"
)

type Result struct {
	OK     bool   `json:"ok"`
	RunID  string `json:"runId"`
	Pinned bool   `json:"pinned"`
	Path   string `json:"path"`
}

type Opts struct {
	OutRoot string
	RunID   string
	Pinned  bool
}

func Set(opts Opts) (Result, error) {
	outRoot := strings.TrimSpace(opts.OutRoot)
	if outRoot == "" {
		outRoot = ".zcl"
	}
	runID := strings.TrimSpace(opts.RunID)
	if !ids.IsValidRunID(runID) {
		return Result{}, fmt.Errorf("invalid --run-id (expected format YYYYMMDD-HHMMSSZ-<hex6>)")
	}

	runsDir := filepath.Join(outRoot, "runs")
	runDir := filepath.Join(runsDir, runID)
	runJSONPath := filepath.Join(runDir, "run.json")

	raw, err := os.ReadFile(runJSONPath)
	if err != nil {
		if os.IsNotExist(err) {
			return Result{}, fmt.Errorf("missing run.json for runId=%s", runID)
		}
		return Result{}, err
	}

	// Containment guard against symlink traversal.
	runsEval, err := filepath.EvalSymlinks(runsDir)
	if err != nil {
		return Result{}, err
	}
	runEval, err := filepath.EvalSymlinks(runDir)
	if err != nil {
		return Result{}, err
	}
	runsEval = filepath.Clean(runsEval)
	runEval = filepath.Clean(runEval)
	sep := string(os.PathSeparator)
	if !strings.HasPrefix(runEval, runsEval+sep) && runEval != runsEval {
		return Result{}, fmt.Errorf("run directory escapes outRoot (symlink traversal)")
	}

	var meta schema.RunJSONV1
	if err := json.Unmarshal(raw, &meta); err != nil {
		return Result{}, fmt.Errorf("invalid run.json: %w", err)
	}
	if meta.SchemaVersion != schema.RunSchemaV1 {
		return Result{}, fmt.Errorf("unsupported run.json schemaVersion=%d", meta.SchemaVersion)
	}
	if meta.ArtifactLayoutVersion != schema.ArtifactLayoutVersionV1 {
		return Result{}, fmt.Errorf("unsupported run.json artifactLayoutVersion=%d", meta.ArtifactLayoutVersion)
	}
	if meta.RunID != runID {
		return Result{}, fmt.Errorf("run.json mismatch: expected runId=%s", runID)
	}

	meta.Pinned = opts.Pinned
	if err := store.WriteJSONAtomic(runJSONPath, meta); err != nil {
		return Result{}, err
	}

	return Result{OK: true, RunID: runID, Pinned: meta.Pinned, Path: runJSONPath}, nil
}
