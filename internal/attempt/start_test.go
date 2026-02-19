package attempt

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/marcohefti/zero-context-lab/internal/schema"
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

func TestStart_RecordsIsolationModelInAttemptAndEnv(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	outRoot := filepath.Join(dir, ".zcl")
	now := time.Date(2026, 2, 15, 18, 0, 12, 0, time.UTC)

	res, err := Start(now, StartOpts{
		OutRoot:        outRoot,
		RunID:          "20260215-180012Z-09c5a6",
		SuiteID:        "heftiweb-smoke",
		MissionID:      "latest-blog-title",
		IsolationModel: schema.IsolationModelNativeSpawnV1,
		Retry:          1,
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if res.IsolationModel != schema.IsolationModelNativeSpawnV1 {
		t.Fatalf("expected start result isolationModel=%q, got %q", schema.IsolationModelNativeSpawnV1, res.IsolationModel)
	}
	if got := res.Env["ZCL_ISOLATION_MODEL"]; got != schema.IsolationModelNativeSpawnV1 {
		t.Fatalf("expected env ZCL_ISOLATION_MODEL=%q, got %q", schema.IsolationModelNativeSpawnV1, got)
	}

	b, err := os.ReadFile(filepath.Join(res.OutDirAbs, "attempt.json"))
	if err != nil {
		t.Fatalf("read attempt.json: %v", err)
	}
	var a schema.AttemptJSONV1
	if err := json.Unmarshal(b, &a); err != nil {
		t.Fatalf("unmarshal attempt.json: %v", err)
	}
	if a.IsolationModel != schema.IsolationModelNativeSpawnV1 {
		t.Fatalf("expected attempt.json isolationModel=%q, got %q", schema.IsolationModelNativeSpawnV1, a.IsolationModel)
	}
}
