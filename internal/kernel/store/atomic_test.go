package store

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestWriteJSONAtomic_OverwritesExisting(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "x.json")

	if err := WriteJSONAtomic(path, map[string]any{"a": 1}); err != nil {
		t.Fatalf("WriteJSONAtomic: %v", err)
	}
	if err := WriteJSONAtomic(path, map[string]any{"a": 2}); err != nil {
		t.Fatalf("WriteJSONAtomic overwrite: %v", err)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	var v map[string]any
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if v["a"] != float64(2) {
		t.Fatalf("unexpected value: %#v", v["a"])
	}
}

func TestWriteFileAtomic_OverwritesExisting(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "x.txt")

	if err := WriteFileAtomic(path, []byte("a")); err != nil {
		t.Fatalf("WriteFileAtomic: %v", err)
	}
	if err := WriteFileAtomic(path, []byte("b")); err != nil {
		t.Fatalf("WriteFileAtomic overwrite: %v", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(raw) != "b" {
		t.Fatalf("unexpected content: %q", string(raw))
	}
}
