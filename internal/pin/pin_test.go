package pin

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/marcohefti/zero-context-lab/internal/schema"
)

func TestSet_TogglesPinned(t *testing.T) {
	dir := t.TempDir()
	outRoot := filepath.Join(dir, ".zcl")
	runID := "20260215-180012Z-09c5a6"
	runDir := filepath.Join(outRoot, "runs", runID)
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	meta := schema.RunJSONV1{
		SchemaVersion: 1,
		RunID:         runID,
		SuiteID:       "s",
		CreatedAt:     "2026-02-15T18:00:00Z",
		Pinned:        false,
	}
	b, _ := json.Marshal(meta)
	if err := os.WriteFile(filepath.Join(runDir, "run.json"), b, 0o644); err != nil {
		t.Fatalf("write run.json: %v", err)
	}

	res, err := Set(Opts{OutRoot: outRoot, RunID: runID, Pinned: true})
	if err != nil {
		t.Fatalf("Set: %v", err)
	}
	if !res.OK || !res.Pinned {
		t.Fatalf("unexpected res: %+v", res)
	}

	raw, err := os.ReadFile(filepath.Join(runDir, "run.json"))
	if err != nil {
		t.Fatalf("read run.json: %v", err)
	}
	var got schema.RunJSONV1
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !got.Pinned {
		t.Fatalf("expected pinned=true")
	}
}
