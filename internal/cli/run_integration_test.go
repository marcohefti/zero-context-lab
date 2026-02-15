package cli

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/marcohefti/zero-context-lab/internal/schema"
)

func TestRun_PassthroughAndTraceEmission(t *testing.T) {
	outDir := t.TempDir()
	setAttemptEnv(t, outDir)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	r := Runner{
		Version: "0.0.0-dev",
		Now:     func() time.Time { return time.Date(2026, 2, 15, 18, 0, 0, 0, time.UTC) },
		Stdout:  &stdout,
		Stderr:  &stderr,
	}

	code := r.Run([]string{"run", "--", os.Args[0], "-test.run=TestHelperProcess", "--", "stdout=hello\n", "stderr=oops\n", "exit=7"})
	if code != 7 {
		t.Fatalf("expected exit code 7, got %d (stderr=%q)", code, stderr.String())
	}
	if stdout.String() != "hello\n" {
		t.Fatalf("stdout passthrough mismatch: %q", stdout.String())
	}
	if !strings.Contains(stderr.String(), "oops\n") {
		t.Fatalf("stderr passthrough mismatch: %q", stderr.String())
	}

	ev := readSingleTraceEvent(t, filepath.Join(outDir, "tool.calls.jsonl"))
	if ev.V != 1 || ev.Tool != "cli" || ev.Op != "exec" {
		t.Fatalf("unexpected event header: %+v", ev)
	}
	if ev.Result.ExitCode == nil || *ev.Result.ExitCode != 7 {
		t.Fatalf("unexpected exitCode: %+v", ev.Result)
	}
	if ev.IO.OutPreview != "hello\n" || ev.IO.ErrPreview != "oops\n" {
		t.Fatalf("unexpected previews: out=%q err=%q", ev.IO.OutPreview, ev.IO.ErrPreview)
	}
}

func TestRun_BoundsEnforcedAndTruncationRecorded(t *testing.T) {
	outDir := t.TempDir()
	setAttemptEnv(t, outDir)

	// Write slightly more than the 16KiB preview cap.
	payload := strings.Repeat("a", 16*1024+123)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	r := Runner{
		Version: "0.0.0-dev",
		Now:     func() time.Time { return time.Date(2026, 2, 15, 18, 0, 0, 0, time.UTC) },
		Stdout:  &stdout,
		Stderr:  &stderr,
	}

	code := r.Run([]string{"run", "--", os.Args[0], "-test.run=TestHelperProcess", "--", "stdout=" + payload, "exit=0"})
	if code != 0 {
		t.Fatalf("expected exit code 0, got %d (stderr=%q)", code, stderr.String())
	}
	if len(stdout.String()) != len(payload) {
		t.Fatalf("stdout passthrough should include full payload (got %d want %d)", len(stdout.String()), len(payload))
	}

	ev := readSingleTraceEvent(t, filepath.Join(outDir, "tool.calls.jsonl"))
	if ev.Integrity == nil || !ev.Integrity.Truncated {
		t.Fatalf("expected truncation integrity flag, got: %+v", ev.Integrity)
	}
	if got := len(ev.IO.OutPreview); got != 16*1024 {
		t.Fatalf("expected outPreview length 16384, got %d", got)
	}
}

func TestRun_RedactsSecretsInTraceButNotPassthrough(t *testing.T) {
	outDir := t.TempDir()
	setAttemptEnv(t, outDir)

	openAIKey := "sk-1234567890ABCDEF"
	ghToken := "ghp_1234567890abcdef"

	payloadOut := "token=" + openAIKey + "\n"
	payloadErr := "gh=" + ghToken + "\n"

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	r := Runner{
		Version: "0.0.0-dev",
		Now:     func() time.Time { return time.Date(2026, 2, 15, 18, 0, 0, 0, time.UTC) },
		Stdout:  &stdout,
		Stderr:  &stderr,
	}

	code := r.Run([]string{"run", "--", os.Args[0], "-test.run=TestHelperProcess", "--", "stdout=" + payloadOut, "stderr=" + payloadErr, "arg=" + openAIKey, "exit=0"})
	if code != 0 {
		t.Fatalf("expected exit code 0, got %d (stderr=%q)", code, stderr.String())
	}

	// Passthrough should not be redacted.
	if stdout.String() != payloadOut {
		t.Fatalf("stdout passthrough mismatch: %q", stdout.String())
	}
	if !strings.Contains(stderr.String(), payloadErr) {
		t.Fatalf("stderr passthrough mismatch: %q", stderr.String())
	}

	ev := readSingleTraceEvent(t, filepath.Join(outDir, "tool.calls.jsonl"))
	if strings.Contains(ev.IO.OutPreview, openAIKey) || strings.Contains(ev.IO.ErrPreview, ghToken) {
		t.Fatalf("expected redaction in previews, got out=%q err=%q", ev.IO.OutPreview, ev.IO.ErrPreview)
	}
	if !strings.Contains(ev.IO.OutPreview, "[REDACTED:OPENAI_KEY]") {
		t.Fatalf("expected OPENAI key redaction in outPreview, got: %q", ev.IO.OutPreview)
	}
	if !strings.Contains(ev.IO.ErrPreview, "[REDACTED:GITHUB_TOKEN]") {
		t.Fatalf("expected GitHub token redaction in errPreview, got: %q", ev.IO.ErrPreview)
	}

	// Input argv should also be redacted.
	var in struct {
		Argv []string `json:"argv"`
	}
	if err := json.Unmarshal(ev.Input, &in); err != nil {
		t.Fatalf("unmarshal input: %v", err)
	}
	for _, a := range in.Argv {
		if strings.Contains(a, openAIKey) {
			t.Fatalf("expected redaction in input argv, got: %q", a)
		}
	}
}

func TestHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_HELPER_PROCESS") != "1" {
		return
	}
	// This is executed as a subprocess of the test binary.
	args := os.Args
	idx := 0
	for i := range args {
		if args[i] == "--" {
			idx = i + 1
			break
		}
	}
	out := ""
	errOut := ""
	exit := 0
	for _, a := range args[idx:] {
		if strings.HasPrefix(a, "stdout=") {
			out = strings.TrimPrefix(a, "stdout=")
		} else if strings.HasPrefix(a, "stderr=") {
			errOut = strings.TrimPrefix(a, "stderr=")
		} else if strings.HasPrefix(a, "exit=") {
			n, _ := strconv.Atoi(strings.TrimPrefix(a, "exit="))
			exit = n
		}
	}
	_, _ = os.Stdout.WriteString(out)
	_, _ = os.Stderr.WriteString(errOut)
	os.Exit(exit)
}

func setAttemptEnv(t *testing.T, outDir string) {
	t.Helper()
	t.Setenv("GO_WANT_HELPER_PROCESS", "1")
	t.Setenv("ZCL_OUT_DIR", outDir)
	t.Setenv("ZCL_RUN_ID", "20260215-180012Z-09c5a6")
	t.Setenv("ZCL_SUITE_ID", "heftiweb-smoke")
	t.Setenv("ZCL_MISSION_ID", "latest-blog-title")
	t.Setenv("ZCL_ATTEMPT_ID", "001-latest-blog-title-r1")
}

func readSingleTraceEvent(t *testing.T, path string) schema.TraceEventV1 {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open trace: %v", err)
	}
	defer func() { _ = f.Close() }()

	sc := bufio.NewScanner(f)
	if !sc.Scan() {
		t.Fatalf("expected one trace line")
	}
	line := sc.Bytes()
	if sc.Scan() {
		t.Fatalf("expected exactly one trace line")
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("scan: %v", err)
	}

	var ev schema.TraceEventV1
	if err := json.Unmarshal(line, &ev); err != nil {
		t.Fatalf("unmarshal trace: %v", err)
	}
	return ev
}
