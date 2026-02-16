package attempt

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestStart_WritesPromptAndSuiteSnapshotWhenProvided(t *testing.T) {
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

	now := time.Date(2026, 2, 15, 18, 0, 12, 0, time.UTC)
	res, err := Start(now, StartOpts{
		OutRoot:       outRoot,
		RunID:         "20260215-180012Z-09c5a6",
		SuiteID:       "heftiweb-smoke",
		MissionID:     "latest-blog-title",
		Retry:         1,
		Prompt:        "Mission prompt\nSecond line",
		SuiteSnapshot: suiteSnap,
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	runDir := filepath.Join(outRoot, "runs", res.RunID)
	if _, err := os.Stat(filepath.Join(runDir, "suite.json")); err != nil {
		t.Fatalf("missing suite.json: %v", err)
	}

	promptPath := filepath.Join(runDir, "attempts", res.AttemptID, "prompt.txt")
	gotPrompt, err := os.ReadFile(promptPath)
	if err != nil {
		t.Fatalf("read prompt.txt: %v", err)
	}
	if string(gotPrompt) != "Mission prompt\nSecond line" {
		t.Fatalf("unexpected prompt.txt contents: %q", string(gotPrompt))
	}
}
