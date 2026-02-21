package suite

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseFile_RejectsRequiredJSONPointersWithoutJSONType(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "suite.json")
	raw := `{
  "version": 1,
  "suiteId": "s",
  "missions": [
    {
      "missionId": "m",
      "expects": {
        "result": {
          "type": "string",
          "requiredJsonPointers": ["/proof/value"]
        }
      }
    }
  ]
}`
	if err := os.WriteFile(path, []byte(raw), 0o644); err != nil {
		t.Fatalf("write suite file: %v", err)
	}
	_, err := ParseFile(path)
	if err == nil || !strings.Contains(err.Error(), "requiredJsonPointers requires expects.result.type=json") {
		t.Fatalf("expected requiredJsonPointers/type error, got: %v", err)
	}
}

func TestParseFile_RejectsInvalidJSONPointer(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "suite.json")
	raw := `{
  "version": 1,
  "suiteId": "s",
  "missions": [
    {
      "missionId": "m",
      "expects": {
        "result": {
          "type": "json",
          "requiredJsonPointers": ["proof/value"]
        }
      }
    }
  ]
}`
	if err := os.WriteFile(path, []byte(raw), 0o644); err != nil {
		t.Fatalf("write suite file: %v", err)
	}
	_, err := ParseFile(path)
	if err == nil || !strings.Contains(err.Error(), "invalid expects.result.requiredJsonPointers entry") {
		t.Fatalf("expected invalid pointer error, got: %v", err)
	}
}

func TestParseFile_NormalizesRequiredJSONPointers(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "suite.json")
	raw := `{
  "version": 1,
  "suiteId": "s",
  "missions": [
    {
      "missionId": "m",
      "expects": {
        "result": {
          "type": "json",
          "requiredJsonPointers": [" /proof/value ", "/proof/value", "/proof/other"]
        }
      }
    }
  ]
}`
	if err := os.WriteFile(path, []byte(raw), 0o644); err != nil {
		t.Fatalf("write suite file: %v", err)
	}
	ps, err := ParseFile(path)
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}
	got := ps.Suite.Missions[0].Expects.Result.RequiredJSONPointers
	if len(got) != 2 || got[0] != "/proof/value" || got[1] != "/proof/other" {
		t.Fatalf("unexpected normalized pointers: %#v", got)
	}
}
