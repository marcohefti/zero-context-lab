package gc

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/marcohefti/zero-context-lab/internal/kernel/schema"
)

type RunInfo struct {
	RunID     string    `json:"runId"`
	Path      string    `json:"path"`
	CreatedAt time.Time `json:"createdAt"`
	Pinned    bool      `json:"pinned"`
	Bytes     int64     `json:"bytes"`
}

type Result struct {
	OK          bool      `json:"ok"`
	OutRoot     string    `json:"outRoot"`
	DryRun      bool      `json:"dryRun"`
	Deleted     []RunInfo `json:"deleted,omitempty"`
	Kept        []RunInfo `json:"kept,omitempty"`
	Errors      []string  `json:"errors,omitempty"`
	TotalBefore int64     `json:"totalBeforeBytes"`
	TotalAfter  int64     `json:"totalAfterBytes"`
}

type Opts struct {
	OutRoot       string
	Now           time.Time
	MaxAgeDays    int
	MaxTotalBytes int64
	DryRun        bool
}

func Run(opts Opts) (Result, error) {
	outRoot := opts.OutRoot
	if outRoot == "" {
		outRoot = ".zcl"
	}
	now := opts.Now
	if now.IsZero() {
		now = time.Now()
	}

	runsDir := filepath.Join(outRoot, "runs")
	entries, err := os.ReadDir(runsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return Result{OK: true, OutRoot: outRoot, DryRun: opts.DryRun}, nil
		}
		return Result{}, err
	}

	runs := collectRuns(entries, runsDir)

	sort.Slice(runs, func(i, j int) bool {
		if runs[i].CreatedAt.Equal(runs[j].CreatedAt) {
			return runs[i].RunID < runs[j].RunID
		}
		return runs[i].CreatedAt.Before(runs[j].CreatedAt)
	})

	var total int64
	for _, r := range runs {
		total += r.Bytes
	}
	res := Result{OK: true, OutRoot: outRoot, DryRun: opts.DryRun, TotalBefore: total, TotalAfter: total}
	shouldDelete := planDeletion(runs, now, opts, total)
	applyDeletion(&res, runs, shouldDelete, opts.DryRun)
	return res, nil
}

func collectRuns(entries []os.DirEntry, runsDir string) []RunInfo {
	runs := make([]RunInfo, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		run, ok := loadRunInfo(filepath.Join(runsDir, e.Name()))
		if !ok {
			continue
		}
		runs = append(runs, run)
	}
	return runs
}

func loadRunInfo(runDir string) (RunInfo, bool) {
	runJSONPath := filepath.Join(runDir, "run.json")
	raw, err := os.ReadFile(runJSONPath)
	if err != nil {
		return RunInfo{}, false
	}
	var meta schema.RunJSONV1
	if err := json.Unmarshal(raw, &meta); err != nil {
		return RunInfo{}, false
	}
	if meta.SchemaVersion != schema.RunSchemaV1 || meta.ArtifactLayoutVersion != schema.ArtifactLayoutVersionV1 {
		return RunInfo{}, false
	}
	createdAt, err := time.Parse(time.RFC3339Nano, meta.CreatedAt)
	if err != nil {
		createdAt, _ = time.Parse(time.RFC3339, meta.CreatedAt)
	}
	size, _ := dirSize(runDir)
	return RunInfo{
		RunID:     meta.RunID,
		Path:      runDir,
		CreatedAt: createdAt,
		Pinned:    meta.Pinned,
		Bytes:     size,
	}, true
}

func planDeletion(runs []RunInfo, now time.Time, opts Opts, total int64) map[string]bool {
	shouldDelete := selectAgeBasedRuns(runs, now, opts.MaxAgeDays)
	applySizeBasedSelection(runs, opts.MaxTotalBytes, total, shouldDelete)
	return shouldDelete
}

func selectAgeBasedRuns(runs []RunInfo, now time.Time, maxAgeDays int) map[string]bool {
	shouldDelete := make(map[string]bool)
	if maxAgeDays <= 0 {
		return shouldDelete
	}
	cutoff := now.Add(-time.Duration(maxAgeDays) * 24 * time.Hour)
	for _, r := range runs {
		if r.Pinned {
			continue
		}
		if !r.CreatedAt.IsZero() && r.CreatedAt.Before(cutoff) {
			shouldDelete[r.RunID] = true
		}
	}
	return shouldDelete
}

func applySizeBasedSelection(runs []RunInfo, maxTotalBytes int64, total int64, shouldDelete map[string]bool) {
	if maxTotalBytes <= 0 || total <= maxTotalBytes {
		return
	}
	for _, r := range runs {
		if total <= maxTotalBytes {
			return
		}
		if r.Pinned || shouldDelete[r.RunID] {
			continue
		}
		shouldDelete[r.RunID] = true
		total -= r.Bytes
	}
}

func applyDeletion(res *Result, runs []RunInfo, shouldDelete map[string]bool, dryRun bool) {
	for _, r := range runs {
		if !shouldDelete[r.RunID] {
			res.Kept = append(res.Kept, r)
			continue
		}
		res.Deleted = append(res.Deleted, r)
		res.TotalAfter -= r.Bytes
		if !dryRun {
			_ = os.RemoveAll(r.Path)
		}
	}
}

func dirSize(root string) (int64, error) {
	var total int64
	err := filepath.WalkDir(root, func(_ string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		total += info.Size()
		return nil
	})
	return total, err
}
