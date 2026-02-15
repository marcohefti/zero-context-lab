package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestInitProject_CreatesConfigAndOutRoot(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "zcl.config.json")
	outRoot := filepath.Join(dir, ".zcl")

	res, err := InitProject(cfgPath, outRoot)
	if err != nil {
		t.Fatalf("InitProject: %v", err)
	}
	if !res.OK || !res.Created || !res.OutRootReady {
		t.Fatalf("unexpected result: %+v", *res)
	}
	if _, err := os.Stat(filepath.Join(outRoot, "runs")); err != nil {
		t.Fatalf("missing runs dir: %v", err)
	}

	raw, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	var cfg ProjectConfigV1
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatalf("unmarshal config: %v", err)
	}
	if cfg.SchemaVersion != ProjectConfigSchemaV1 || cfg.OutRoot != outRoot {
		t.Fatalf("unexpected config: %+v", cfg)
	}
}

func TestInitProject_Idempotent(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "zcl.config.json")
	outRoot := filepath.Join(dir, ".zcl")

	if _, err := InitProject(cfgPath, outRoot); err != nil {
		t.Fatalf("InitProject (first): %v", err)
	}
	res, err := InitProject(cfgPath, outRoot)
	if err != nil {
		t.Fatalf("InitProject (second): %v", err)
	}
	if !res.OK || res.Created {
		t.Fatalf("unexpected result: %+v", *res)
	}
}

func TestInitProject_ErrorsOnOutRootMismatch(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "zcl.config.json")

	if _, err := InitProject(cfgPath, filepath.Join(dir, ".zcl-a")); err != nil {
		t.Fatalf("InitProject: %v", err)
	}
	if _, err := InitProject(cfgPath, filepath.Join(dir, ".zcl-b")); err == nil {
		t.Fatalf("expected error on outRoot mismatch")
	}
}
