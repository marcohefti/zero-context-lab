package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadMerged_PrecedenceFlagEnvProjectGlobalDefault(t *testing.T) {
	dir := t.TempDir()
	wd := mustGetwd(t)
	t.Cleanup(func() {
		_ = os.Chdir(wd)
	})
	mustNoErr(t, "chdir", os.Chdir(dir))

	// Default
	m := mustLoadMerged(t, "")
	if m.OutRoot != ".zcl" || m.Source != "default" {
		t.Fatalf("unexpected default: %+v", m)
	}

	// Global
	home := filepath.Join(dir, "home")
	mustNoErr(t, "mkdir", os.MkdirAll(home, 0o755))
	t.Setenv("HOME", home)
	globalPath := mustGlobalConfigPath(t)
	mustNoErr(t, "mkdir", os.MkdirAll(filepath.Dir(globalPath), 0o755))
	mustNoErr(t, "write", os.WriteFile(globalPath, []byte(`{"schemaVersion":1,"outRoot":".zcl-global"}`), 0o644))
	m = mustLoadMerged(t, "")
	if m.OutRoot != ".zcl-global" {
		t.Fatalf("unexpected global: %+v", m)
	}

	// Project overrides global
	mustNoErr(t, "write", os.WriteFile(DefaultProjectConfigPath, []byte(`{"schemaVersion":1,"outRoot":".zcl-project"}`), 0o644))
	m = mustLoadMerged(t, "")
	if m.OutRoot != ".zcl-project" {
		t.Fatalf("unexpected project: %+v", m)
	}

	// Env overrides project
	t.Setenv("ZCL_OUT_ROOT", ".zcl-env")
	m = mustLoadMerged(t, "")
	if m.OutRoot != ".zcl-env" {
		t.Fatalf("unexpected env: %+v", m)
	}

	// Flag overrides env
	m = mustLoadMerged(t, ".zcl-flag")
	if m.OutRoot != ".zcl-flag" {
		t.Fatalf("unexpected flag: %+v", m)
	}
}

func TestLoadMerged_RuntimeStrategyChainPrecedence(t *testing.T) {
	dir := t.TempDir()
	wd := mustGetwd(t)
	t.Cleanup(func() {
		_ = os.Chdir(wd)
	})
	mustNoErr(t, "chdir", os.Chdir(dir))

	home := filepath.Join(dir, "home")
	mustNoErr(t, "mkdir", os.MkdirAll(home, 0o755))
	t.Setenv("HOME", home)
	globalPath := mustGlobalConfigPath(t)
	mustNoErr(t, "mkdir", os.MkdirAll(filepath.Dir(globalPath), 0o755))
	mustNoErr(t, "write global", os.WriteFile(globalPath, []byte(`{"schemaVersion":1,"runtime":{"strategyChain":["codex_app_server","fallback"]}}`), 0o644))

	m := mustLoadMerged(t, "")
	if len(m.RuntimeStrategyChain) != 2 || m.RuntimeStrategyChain[0] != "codex_app_server" || m.RuntimeStrategyChain[1] != "fallback" {
		t.Fatalf("unexpected global runtime chain: %#v", m.RuntimeStrategyChain)
	}

	mustNoErr(t, "write project", os.WriteFile(DefaultProjectConfigPath, []byte(`{"schemaVersion":1,"outRoot":".zcl","runtime":{"strategyChain":["project_strategy"]}}`), 0o644))
	m = mustLoadMerged(t, "")
	if len(m.RuntimeStrategyChain) != 1 || m.RuntimeStrategyChain[0] != "project_strategy" {
		t.Fatalf("unexpected project runtime chain: %#v", m.RuntimeStrategyChain)
	}

	t.Setenv("ZCL_RUNTIME_STRATEGIES", "env_a, env_b , env_a")
	m = mustLoadMerged(t, "")
	if len(m.RuntimeStrategyChain) != 2 || m.RuntimeStrategyChain[0] != "env_a" || m.RuntimeStrategyChain[1] != "env_b" {
		t.Fatalf("unexpected env runtime chain: %#v", m.RuntimeStrategyChain)
	}
}

func mustGetwd(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	mustNoErr(t, "getwd", err)
	return wd
}

func mustGlobalConfigPath(t *testing.T) string {
	t.Helper()
	path, err := DefaultGlobalConfigPath()
	mustNoErr(t, "DefaultGlobalConfigPath", err)
	return path
}

func mustLoadMerged(t *testing.T, outRoot string) Merged {
	t.Helper()
	m, err := LoadMerged(outRoot)
	mustNoErr(t, "LoadMerged", err)
	return m
}

func mustNoErr(t *testing.T, op string, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("%s: %v", op, err)
	}
}
