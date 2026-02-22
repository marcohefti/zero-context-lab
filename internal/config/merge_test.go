package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadMerged_PrecedenceFlagEnvProjectGlobalDefault(t *testing.T) {
	dir := t.TempDir()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(wd)
	})
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

func TestLoadMerged_RuntimeStrategyChainPrecedence(t *testing.T) {
	dir := t.TempDir()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(wd)
	})
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}

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
	if err := os.WriteFile(globalPath, []byte(`{"schemaVersion":1,"runtime":{"strategyChain":["codex_app_server","fallback"]}}`), 0o644); err != nil {
		t.Fatalf("write global: %v", err)
	}

	m, err := LoadMerged("")
	if err != nil {
		t.Fatalf("LoadMerged global: %v", err)
	}
	if len(m.RuntimeStrategyChain) != 2 || m.RuntimeStrategyChain[0] != "codex_app_server" || m.RuntimeStrategyChain[1] != "fallback" {
		t.Fatalf("unexpected global runtime chain: %#v", m.RuntimeStrategyChain)
	}

	if err := os.WriteFile(DefaultProjectConfigPath, []byte(`{"schemaVersion":1,"outRoot":".zcl","runtime":{"strategyChain":["project_strategy"]}}`), 0o644); err != nil {
		t.Fatalf("write project: %v", err)
	}
	m, err = LoadMerged("")
	if err != nil {
		t.Fatalf("LoadMerged project: %v", err)
	}
	if len(m.RuntimeStrategyChain) != 1 || m.RuntimeStrategyChain[0] != "project_strategy" {
		t.Fatalf("unexpected project runtime chain: %#v", m.RuntimeStrategyChain)
	}

	t.Setenv("ZCL_RUNTIME_STRATEGIES", "env_a, env_b , env_a")
	m, err = LoadMerged("")
	if err != nil {
		t.Fatalf("LoadMerged env: %v", err)
	}
	if len(m.RuntimeStrategyChain) != 2 || m.RuntimeStrategyChain[0] != "env_a" || m.RuntimeStrategyChain[1] != "env_b" {
		t.Fatalf("unexpected env runtime chain: %#v", m.RuntimeStrategyChain)
	}
}
