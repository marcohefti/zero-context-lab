package gc

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestGC_RespectsPinnedAndAge(t *testing.T) {
	dir := t.TempDir()
	outRoot := filepath.Join(dir, ".zcl")
	runsDir := filepath.Join(outRoot, "runs")
	if err := os.MkdirAll(runsDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	writeRun(t, runsDir, "r1", "2026-02-10T00:00:00Z", false)
	writeRun(t, runsDir, "r2", "2026-02-01T00:00:00Z", true)  // pinned old
	writeRun(t, runsDir, "r3", "2026-01-01T00:00:00Z", false) // delete

	now := time.Date(2026, 2, 15, 0, 0, 0, 0, time.UTC)
	res, err := Run(Opts{
		OutRoot:    outRoot,
		Now:        now,
		MaxAgeDays: 30,
		DryRun:     true,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(res.Deleted) != 1 || res.Deleted[0].RunID != "r3" {
		t.Fatalf("unexpected deleted: %+v", res.Deleted)
	}
}

func writeRun(t *testing.T, runsDir, runID, createdAt string, pinned bool) {
	t.Helper()
	runDir := filepath.Join(runsDir, runID)
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatalf("mkdir run: %v", err)
	}
	pinField := ""
	if pinned {
		pinField = ",\"pinned\":true"
	}
	body := `{"schemaVersion":1,"artifactLayoutVersion":1,"runId":"` + runID + `","suiteId":"s","createdAt":"` + createdAt + `"` + pinField + `}`
	if err := os.WriteFile(filepath.Join(runDir, "run.json"), []byte(body), 0o644); err != nil {
		t.Fatalf("write run.json: %v", err)
	}
}
