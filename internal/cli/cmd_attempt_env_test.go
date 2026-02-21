package cli

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAttemptEnv_Command(t *testing.T) {
	outRoot := t.TempDir()
	now := time.Date(2026, 2, 21, 19, 0, 0, 0, time.UTC)
	r := Runner{
		Version: "0.0.0-dev",
		Now:     func() time.Time { return now },
	}

	var startStdout bytes.Buffer
	var startStderr bytes.Buffer
	r.Stdout = &startStdout
	r.Stderr = &startStderr
	if code := r.Run([]string{
		"attempt", "start",
		"--out-root", outRoot,
		"--suite", "smoke",
		"--mission", "m1",
		"--json",
	}); code != 0 {
		t.Fatalf("attempt start failed: code=%d stderr=%q", code, startStderr.String())
	}
	var started struct {
		OutDirAbs      string            `json:"outDirAbs"`
		AttemptEnvFile string            `json:"attemptEnvFile"`
		Env            map[string]string `json:"env"`
	}
	if err := json.Unmarshal(startStdout.Bytes(), &started); err != nil {
		t.Fatalf("unmarshal attempt start: %v", err)
	}
	if started.OutDirAbs == "" || started.AttemptEnvFile == "" {
		t.Fatalf("unexpected attempt start payload: %s", startStdout.String())
	}

	var envStdout bytes.Buffer
	var envStderr bytes.Buffer
	r.Stdout = &envStdout
	r.Stderr = &envStderr
	if code := r.Run([]string{"attempt", "env", "--json", started.OutDirAbs}); code != 0 {
		t.Fatalf("attempt env --json failed: code=%d stderr=%q", code, envStderr.String())
	}
	var got struct {
		OK             bool              `json:"ok"`
		AttemptDir     string            `json:"attemptDir"`
		AttemptEnvFile string            `json:"attemptEnvFile"`
		Env            map[string]string `json:"env"`
	}
	if err := json.Unmarshal(envStdout.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal attempt env --json: %v", err)
	}
	if !got.OK {
		t.Fatalf("expected ok=true: %s", envStdout.String())
	}
	if got.AttemptEnvFile != started.AttemptEnvFile {
		t.Fatalf("unexpected attemptEnvFile: got=%q want=%q", got.AttemptEnvFile, started.AttemptEnvFile)
	}
	if got.Env["ZCL_OUT_DIR"] != started.OutDirAbs {
		t.Fatalf("unexpected ZCL_OUT_DIR: got=%q want=%q", got.Env["ZCL_OUT_DIR"], started.OutDirAbs)
	}

	t.Setenv("ZCL_OUT_DIR", started.OutDirAbs)
	var txtStdout bytes.Buffer
	var txtStderr bytes.Buffer
	r.Stdout = &txtStdout
	r.Stderr = &txtStderr
	if code := r.Run([]string{"attempt", "env", "--format", "sh"}); code != 0 {
		t.Fatalf("attempt env --format sh failed: code=%d stderr=%q", code, txtStderr.String())
	}
	out := txtStdout.String()
	if !strings.Contains(out, "export ZCL_RUN_ID=") || !strings.Contains(out, "export ZCL_OUT_DIR=") {
		t.Fatalf("unexpected attempt env sh output: %q", out)
	}
	if !strings.Contains(got.AttemptDir, filepath.Base(started.OutDirAbs)) {
		t.Fatalf("unexpected attemptDir in output: %q", got.AttemptDir)
	}
}
