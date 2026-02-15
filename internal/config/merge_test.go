package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadMerged_PrecedenceFlagEnvProjectGlobalDefault(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}

	// Default
	m, err := LoadMerged("")
	if err != nil {
		t.Fatalf("LoadMerged: %v", err)
	}
	if m.OutRoot != ".zcl" || m.Source != "default" {
		t.Fatalf("unexpected default: %+v", m)
	}

	// Global
	home := filepath.Join(dir, "home")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	t.Setenv("HOME", home)
	globalPath, err := DefaultGlobalConfigPath()
	if err != nil {
		t.Fatalf("DefaultGlobalConfigPath: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(globalPath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(globalPath, []byte(`{"schemaVersion":1,"outRoot":".zcl-global"}`), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	m, err = LoadMerged("")
	if err != nil {
		t.Fatalf("LoadMerged: %v", err)
	}
	if m.OutRoot != ".zcl-global" {
		t.Fatalf("unexpected global: %+v", m)
	}

	// Project overrides global
	if err := os.WriteFile(DefaultProjectConfigPath, []byte(`{"schemaVersion":1,"outRoot":".zcl-project"}`), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	m, err = LoadMerged("")
	if err != nil {
		t.Fatalf("LoadMerged: %v", err)
	}
	if m.OutRoot != ".zcl-project" {
		t.Fatalf("unexpected project: %+v", m)
	}

	// Env overrides project
	t.Setenv("ZCL_OUT_ROOT", ".zcl-env")
	m, err = LoadMerged("")
	if err != nil {
		t.Fatalf("LoadMerged: %v", err)
	}
	if m.OutRoot != ".zcl-env" {
		t.Fatalf("unexpected env: %+v", m)
	}

	// Flag overrides env
	m, err = LoadMerged(".zcl-flag")
	if err != nil {
		t.Fatalf("LoadMerged: %v", err)
	}
	if m.OutRoot != ".zcl-flag" {
		t.Fatalf("unexpected flag: %+v", m)
	}
}
