package gc

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/marcohefti/zero-context-lab/internal/schema"
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

	var runs []RunInfo
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		runDir := filepath.Join(runsDir, e.Name())
		runJSONPath := filepath.Join(runDir, "run.json")
		raw, err := os.ReadFile(runJSONPath)
		if err != nil {
			continue
		}
		var meta schema.RunJSONV1
		if err := json.Unmarshal(raw, &meta); err != nil {
			continue
		}
		if meta.SchemaVersion != schema.RunSchemaV1 || meta.ArtifactLayoutVersion != schema.ArtifactLayoutVersionV1 {
			continue
		}
		createdAt, err := time.Parse(time.RFC3339Nano, meta.CreatedAt)
		if err != nil {
			createdAt, _ = time.Parse(time.RFC3339, meta.CreatedAt)
		}
		size, _ := dirSize(runDir)
		runs = append(runs, RunInfo{
			RunID:     meta.RunID,
			Path:      runDir,
			CreatedAt: createdAt,
			Pinned:    meta.Pinned,
			Bytes:     size,
		})
	}

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

	shouldDelete := make(map[string]bool)
	if opts.MaxAgeDays > 0 {
		cutoff := now.Add(-time.Duration(opts.MaxAgeDays) * 24 * time.Hour)
		for _, r := range runs {
			if r.Pinned {
				continue
			}
			if !r.CreatedAt.IsZero() && r.CreatedAt.Before(cutoff) {
				shouldDelete[r.RunID] = true
			}
		}
	}

	// Size-based: delete oldest unpinned runs until under threshold.
	if opts.MaxTotalBytes > 0 && total > opts.MaxTotalBytes {
		for _, r := range runs {
			if total <= opts.MaxTotalBytes {
				break
			}
			if r.Pinned {
				continue
			}
			if shouldDelete[r.RunID] {
				// already deleting via age, account below
				continue
			}
			shouldDelete[r.RunID] = true
			total -= r.Bytes
		}
	}

	for _, r := range runs {
		if shouldDelete[r.RunID] {
			res.Deleted = append(res.Deleted, r)
			res.TotalAfter -= r.Bytes
			if !opts.DryRun {
				_ = os.RemoveAll(r.Path)
			}
		} else {
			res.Kept = append(res.Kept, r)
		}
	}
	return res, nil
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
