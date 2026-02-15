package note

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/marcohefti/zero-context-lab/internal/schema"
	"github.com/marcohefti/zero-context-lab/internal/trace"
)

func TestAppend_MessageRedactsAndWritesJSONL(t *testing.T) {
	t.Parallel()

	outDir := t.TempDir()
	env := trace.Env{
		RunID:     "20260215-180012Z-09c5a6",
		SuiteID:   "heftiweb-smoke",
		MissionID: "latest-blog-title",
		AttemptID: "001-latest-blog-title-r1",
		OutDirAbs: outDir,
	}

	now := time.Date(2026, 2, 15, 18, 0, 0, 0, time.UTC)
	if err := Append(now, env, AppendOpts{
		Kind:    "operator",
		Message: "key=sk-ABCDEF1234567890",
		Tags:    []string{"ux"},
	}); err != nil {
		t.Fatalf("Append: %v", err)
	}

	f, err := os.Open(filepath.Join(outDir, "notes.jsonl"))
	if err != nil {
		t.Fatalf("open notes.jsonl: %v", err)
	}
	defer func() { _ = f.Close() }()

	sc := bufio.NewScanner(f)
	if !sc.Scan() {
		t.Fatalf("expected one note line")
	}
	var ev schema.NoteEventV1
	if err := json.Unmarshal(sc.Bytes(), &ev); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if ev.Kind != "operator" || ev.Message != "key=[REDACTED:OPENAI_KEY]" {
		t.Fatalf("unexpected note: %+v", ev)
	}
	if len(ev.RedactionsApplied) == 0 {
		t.Fatalf("expected redactionsApplied to be non-empty")
	}
}
