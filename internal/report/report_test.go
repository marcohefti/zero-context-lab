package report

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/marcohefti/zero-context-lab/internal/schema"
)

func TestBuildAttemptReport_GoldenFixture(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)
	fixtureIn := filepath.Join(root, "test", "fixtures", "report", "v1", "input")
	fixtureExpected := filepath.Join(root, "test", "fixtures", "report", "v1", "expected", "attempt.report.json")

	attemptDir := t.TempDir()
	copyFile(t, filepath.Join(fixtureIn, "attempt.json"), filepath.Join(attemptDir, "attempt.json"))
	copyFile(t, filepath.Join(fixtureIn, "feedback.json"), filepath.Join(attemptDir, "feedback.json"))
	copyFile(t, filepath.Join(fixtureIn, "tool.calls.jsonl"), filepath.Join(attemptDir, "tool.calls.jsonl"))

	now := time.Date(2026, 2, 15, 18, 0, 10, 0, time.UTC)
	got, err := BuildAttemptReport(now, attemptDir, true)
	if err != nil {
		t.Fatalf("BuildAttemptReport: %v", err)
	}

	wantBytes, err := os.ReadFile(fixtureExpected)
	if err != nil {
		t.Fatalf("read expected: %v", err)
	}
	var want schema.AttemptReportJSONV1
	if err := json.Unmarshal(wantBytes, &want); err != nil {
		t.Fatalf("unmarshal expected: %v", err)
	}

	if !reflect.DeepEqual(want, got) {
		gotBytes, _ := json.MarshalIndent(got, "", "  ")
		t.Fatalf("report mismatch\nwant=%s\ngot=%s", string(wantBytes), string(gotBytes))
	}
}

func TestBuildAttemptReport_StrictMissingRequiredArtifacts(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	now := time.Date(2026, 2, 15, 18, 0, 10, 0, time.UTC)
	_, err := BuildAttemptReport(now, dir, true)
	if err == nil {
		t.Fatalf("expected error")
	}
	if !IsCliError(err, "ZCL_E_MISSING_ARTIFACT") {
		t.Fatalf("expected ZCL_E_MISSING_ARTIFACT, got: %v", err)
	}
}

func repoRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	// When tests run under `./...`, wd is the package dir.
	// internal/report -> repo root is ../..
	return filepath.Clean(filepath.Join(wd, "..", ".."))
}

func copyFile(t *testing.T, src, dst string) {
	t.Helper()
	b, err := os.ReadFile(src)
	if err != nil {
		t.Fatalf("read %s: %v", src, err)
	}
	if err := os.WriteFile(dst, b, 0o644); err != nil {
		t.Fatalf("write %s: %v", dst, err)
	}
}
