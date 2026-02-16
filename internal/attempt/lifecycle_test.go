package attempt

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestStart_LifecycleTwoAttemptsStableAndNoTmpFilesLeft(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	outRoot := filepath.Join(dir, ".zcl")
	suiteSnap := map[string]any{
		"version": 1,
		"suiteId": "heftiweb-smoke",
		"missions": []any{
			map[string]any{"missionId": "latest-blog-title"},
		},
	}

	now := time.Date(2026, 2, 15, 18, 0, 12, 123, time.UTC)
	runID := "20260215-180012Z-09c5a6"

	a1, err := Start(now, StartOpts{
		OutRoot:       outRoot,
		RunID:         runID,
		SuiteID:       "Heftiweb Smoke",
		MissionID:     "Latest Blog Title",
		Retry:         1,
		Prompt:        "p1",
		SuiteSnapshot: suiteSnap,
	})
	if err != nil {
		t.Fatalf("Start a1: %v", err)
	}
	if a1.AttemptID != "001-latest-blog-title-r1" {
		t.Fatalf("unexpected attemptId a1: %q", a1.AttemptID)
	}

	a2, err := Start(now.Add(1*time.Second), StartOpts{
		OutRoot:   outRoot,
		RunID:     runID,
		SuiteID:   "Heftiweb Smoke",
		MissionID: "Latest Blog Title",
		Retry:     1,
		Prompt:    "p2",
	})
	if err != nil {
		t.Fatalf("Start a2: %v", err)
	}
	if a2.AttemptID != "002-latest-blog-title-r1" {
		t.Fatalf("unexpected attemptId a2: %q", a2.AttemptID)
	}

	runDir := filepath.Join(outRoot, "runs", runID)
	if _, err := os.Stat(filepath.Join(runDir, "run.json")); err != nil {
		t.Fatalf("missing run.json: %v", err)
	}
	if _, err := os.Stat(filepath.Join(runDir, "suite.json")); err != nil {
		t.Fatalf("missing suite.json: %v", err)
	}
	if _, err := os.Stat(filepath.Join(runDir, "attempts", a1.AttemptID, "attempt.json")); err != nil {
		t.Fatalf("missing attempt.json (a1): %v", err)
	}
	if _, err := os.Stat(filepath.Join(runDir, "attempts", a2.AttemptID, "attempt.json")); err != nil {
		t.Fatalf("missing attempt.json (a2): %v", err)
	}
	if _, err := os.Stat(filepath.Join(runDir, "attempts", a1.AttemptID, "prompt.txt")); err != nil {
		t.Fatalf("missing prompt.txt (a1): %v", err)
	}
	if _, err := os.Stat(filepath.Join(runDir, "attempts", a2.AttemptID, "prompt.txt")); err != nil {
		t.Fatalf("missing prompt.txt (a2): %v", err)
	}

	// Atomic write helpers should not leave temp files behind.
	if err := filepath.WalkDir(runDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if strings.Contains(d.Name(), ".tmp-") {
			t.Fatalf("unexpected temp file left behind: %s", path)
		}
		return nil
	}); err != nil {
		t.Fatalf("walk: %v", err)
	}
}
