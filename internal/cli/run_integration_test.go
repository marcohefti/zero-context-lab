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

const helperProcessEnv = "ZCL_TEST_HELPER_PROCESS"

func init() {
	maybeRunHelperProcess()
}

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

	code := r.Run([]string{"run", "--", os.Args[0], "-test.run=TestHelperProcess", "--", "helper=1", "stdout=hello\n", "stderr=oops\n", "exit=7"})
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
	payloadPath := filepath.Join(outDir, "payload.txt")
	if err := os.WriteFile(payloadPath, []byte(payload), 0o644); err != nil {
		t.Fatalf("write payload: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	r := Runner{
		Version: "0.0.0-dev",
		Now:     func() time.Time { return time.Date(2026, 2, 15, 18, 0, 0, 0, time.UTC) },
		Stdout:  &stdout,
		Stderr:  &stderr,
	}

	code := r.Run([]string{"run", "--", os.Args[0], "-test.run=TestHelperProcess", "--", "helper=1", "stdout-file=" + payloadPath, "exit=0"})
	if code != 0 {
		t.Fatalf("expected exit code 0, got %d (stderr=%q)", code, stderr.String())
	}

	ev := readSingleTraceEvent(t, filepath.Join(outDir, "tool.calls.jsonl"))
	if ev.IO.OutBytes != int64(len(payload)) {
		t.Fatalf("expected outBytes=%d, got %d", len(payload), ev.IO.OutBytes)
	}
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

	payloadOut := "token=" + openAIKey
	payloadErr := "gh=" + ghToken + "\n"

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	r := Runner{
		Version: "0.0.0-dev",
		Now:     func() time.Time { return time.Date(2026, 2, 15, 18, 0, 0, 0, time.UTC) },
		Stdout:  &stdout,
		Stderr:  &stderr,
	}

	code := r.Run([]string{"run", "--", os.Args[0], "-test.run=TestHelperProcess", "--", "helper=1", "stdout=" + payloadOut, "stderr=" + payloadErr, "arg=" + openAIKey, "exit=0"})
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

func TestRun_CaptureRedactsByDefault(t *testing.T) {
	outDir := t.TempDir()
	setAttemptEnv(t, outDir)

	openAIKey := "sk-1234567890ABCDEF"
	payloadOut := "token=" + openAIKey

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	r := Runner{
		Version: "0.0.0-dev",
		Now:     func() time.Time { return time.Date(2026, 2, 15, 18, 0, 0, 0, time.UTC) },
		Stdout:  &stdout,
		Stderr:  &stderr,
	}

	code := r.Run([]string{"run", "--capture", "--capture-max-bytes", "4096", "--", os.Args[0], "-test.run=TestHelperProcess", "--", "helper=1", "stdout=" + payloadOut, "exit=0"})
	if code != 0 {
		t.Fatalf("expected exit code 0, got %d (stderr=%q)", code, stderr.String())
	}

	capEv := readSingleCaptureEvent(t, filepath.Join(outDir, "captures.jsonl"))
	raw, err := os.ReadFile(filepath.Join(outDir, capEv.StdoutPath))
	if err != nil {
		t.Fatalf("read captured stdout: %v", err)
	}
	if strings.Contains(string(raw), openAIKey) {
		t.Fatalf("expected captured stdout to be redacted, got: %q", string(raw))
	}
	if !strings.Contains(string(raw), "[REDACTED:OPENAI_KEY]") {
		t.Fatalf("expected captured stdout to include redaction marker, got: %q", string(raw))
	}
}

func TestRun_CaptureRawDoesNotRedact(t *testing.T) {
	outDir := t.TempDir()
	setAttemptEnv(t, outDir)

	openAIKey := "sk-1234567890ABCDEF"
	payloadOut := "token=" + openAIKey

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	r := Runner{
		Version: "0.0.0-dev",
		Now:     func() time.Time { return time.Date(2026, 2, 15, 18, 0, 0, 0, time.UTC) },
		Stdout:  &stdout,
		Stderr:  &stderr,
	}

	code := r.Run([]string{"run", "--capture", "--capture-raw", "--capture-max-bytes", "4096", "--", os.Args[0], "-test.run=TestHelperProcess", "--", "helper=1", "stdout=" + payloadOut, "exit=0"})
	if code != 0 {
		t.Fatalf("expected exit code 0, got %d (stderr=%q)", code, stderr.String())
	}

	capEv := readSingleCaptureEvent(t, filepath.Join(outDir, "captures.jsonl"))
	raw, err := os.ReadFile(filepath.Join(outDir, capEv.StdoutPath))
	if err != nil {
		t.Fatalf("read captured stdout: %v", err)
	}
	if !strings.Contains(string(raw), openAIKey) {
		t.Fatalf("expected captured stdout to be raw, got: %q", string(raw))
	}
}

func TestRun_CaptureRawBlockedInCIModeWithoutAllowFlag(t *testing.T) {
	outDir := t.TempDir()
	setAttemptEnv(t, outDir)
	setAttemptMode(t, outDir, "ci")

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	r := Runner{
		Version: "0.0.0-dev",
		Now:     func() time.Time { return time.Date(2026, 2, 15, 18, 0, 0, 0, time.UTC) },
		Stdout:  &stdout,
		Stderr:  &stderr,
	}

	code := r.Run([]string{"run", "--capture", "--capture-raw", "--capture-max-bytes", "4096", "--", os.Args[0], "-test.run=TestHelperProcess", "--", "helper=1", "stdout=hello\n", "exit=0"})
	if code != 2 {
		t.Fatalf("expected usage exit code 2, got %d (stderr=%q)", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "ZCL_ALLOW_UNSAFE_CAPTURE=1") {
		t.Fatalf("expected unsafe capture guard message, got: %q", stderr.String())
	}
}

func TestRun_CaptureRawAllowedInCIModeWithExplicitGuard(t *testing.T) {
	outDir := t.TempDir()
	setAttemptEnv(t, outDir)
	setAttemptMode(t, outDir, "ci")
	t.Setenv("ZCL_ALLOW_UNSAFE_CAPTURE", "1")

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	r := Runner{
		Version: "0.0.0-dev",
		Now:     func() time.Time { return time.Date(2026, 2, 15, 18, 0, 0, 0, time.UTC) },
		Stdout:  &stdout,
		Stderr:  &stderr,
	}

	code := r.Run([]string{"run", "--capture", "--capture-raw", "--capture-max-bytes", "4096", "--", os.Args[0], "-test.run=TestHelperProcess", "--", "helper=1", "stdout=raw\n", "exit=0"})
	if code != 0 {
		t.Fatalf("expected exit code 0, got %d (stderr=%q)", code, stderr.String())
	}

	capEv := readSingleCaptureEvent(t, filepath.Join(outDir, "captures.jsonl"))
	if capEv.Redacted {
		t.Fatalf("expected raw capture event, got redacted=true")
	}
	if _, err := os.Stat(filepath.Join(outDir, capEv.StdoutPath)); err != nil {
		t.Fatalf("expected captured stdout file to exist: %v", err)
	}
}

func TestRun_TimeoutStartFirstToolCall_DoesNotExpireBeforeFirstAction(t *testing.T) {
	outDir := t.TempDir()
	setAttemptEnv(t, outDir)

	// Simulate long queue delay before first action; first_tool_call should anchor timeout at first funnel call.
	meta := schema.AttemptJSONV1{
		SchemaVersion: schema.AttemptSchemaV1,
		RunID:         os.Getenv("ZCL_RUN_ID"),
		SuiteID:       os.Getenv("ZCL_SUITE_ID"),
		MissionID:     os.Getenv("ZCL_MISSION_ID"),
		AttemptID:     os.Getenv("ZCL_ATTEMPT_ID"),
		Mode:          "discovery",
		StartedAt:     "2026-01-01T00:00:00Z",
		TimeoutMs:     2000,
		TimeoutStart:  schema.TimeoutStartFirstToolCallV1,
	}
	b, err := json.Marshal(meta)
	if err != nil {
		t.Fatalf("marshal attempt.json: %v", err)
	}
	if err := os.WriteFile(filepath.Join(outDir, "attempt.json"), b, 0o644); err != nil {
		t.Fatalf("write attempt.json: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	r := Runner{
		Version: "0.0.0-dev",
		Now:     func() time.Time { return time.Date(2026, 2, 18, 8, 0, 0, 0, time.UTC) },
		Stdout:  &stdout,
		Stderr:  &stderr,
	}

	// First call should execute (not pre-expire).
	code := r.Run([]string{"run", "--", os.Args[0], "-test.run=TestHelperProcess", "--", "helper=1", "stdout=ok\n", "exit=0"})
	if code != 0 {
		t.Fatalf("expected first call to succeed, got %d (stderr=%q)", code, stderr.String())
	}

	ev := readSingleTraceEvent(t, filepath.Join(outDir, "tool.calls.jsonl"))
	if ev.Result.Code != "" {
		t.Fatalf("expected first call without timeout code, got=%q", ev.Result.Code)
	}
	raw, err := os.ReadFile(filepath.Join(outDir, "attempt.json"))
	if err != nil {
		t.Fatalf("read attempt.json: %v", err)
	}
	var updated schema.AttemptJSONV1
	if err := json.Unmarshal(raw, &updated); err != nil {
		t.Fatalf("unmarshal attempt.json: %v", err)
	}
	if updated.TimeoutStartedAt == "" {
		t.Fatalf("expected timeoutStartedAt to be anchored on first call")
	}
}

func TestRun_RepeatGuardBlocksNoProgressLoops(t *testing.T) {
	outDir := t.TempDir()
	setAttemptEnv(t, outDir)
	t.Setenv("ZCL_REPEAT_GUARD_MAX_STREAK", "2")

	runCmd := []string{"run", "--", os.Args[0], "-test.run=TestHelperProcess", "--", "helper=1", "stdout=ran\n", "exit=7"}
	for i := 0; i < 2; i++ {
		var stdout bytes.Buffer
		var stderr bytes.Buffer
		r := Runner{
			Version: "0.0.0-dev",
			Now: func() time.Time {
				return time.Date(2026, 2, 15, 18, 0, 0, 0, time.UTC).Add(time.Duration(i) * time.Second)
			},
			Stdout: &stdout,
			Stderr: &stderr,
		}
		code := r.Run(runCmd)
		if code != 7 {
			t.Fatalf("expected failed wrapped command before guard, got %d (stderr=%q)", code, stderr.String())
		}
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	r := Runner{
		Version: "0.0.0-dev",
		Now:     func() time.Time { return time.Date(2026, 2, 15, 18, 0, 3, 0, time.UTC) },
		Stdout:  &stdout,
		Stderr:  &stderr,
	}
	code := r.Run(runCmd)
	if code != 1 {
		t.Fatalf("expected guard to return 1, got %d (stderr=%q)", code, stderr.String())
	}
	if stdout.String() != "" {
		t.Fatalf("expected blocked run to avoid tool stdout passthrough, got %q", stdout.String())
	}
	if !strings.Contains(stderr.String(), "no-progress guard") {
		t.Fatalf("expected no-progress guard message, got stderr=%q", stderr.String())
	}

	evs := readTraceEvents(t, filepath.Join(outDir, "tool.calls.jsonl"))
	if len(evs) != 3 {
		t.Fatalf("expected three trace events, got %d", len(evs))
	}
	last := evs[len(evs)-1]
	if last.Result.Code != "ZCL_E_TOOL_FAILED" {
		t.Fatalf("expected guarded event code ZCL_E_TOOL_FAILED, got %+v", last.Result)
	}
	if !strings.Contains(last.IO.ErrPreview, "no-progress guard") {
		t.Fatalf("expected guard reason in errPreview, got %q", last.IO.ErrPreview)
	}
}

func TestHelperProcess(t *testing.T) {
	// Compatibility fallback in case helper mode was not triggered from init().
	cfg := parseHelperProcessConfig(os.Args)
	if !helperProcessEnabled() || !cfg.Helper {
		return
	}
	runHelperProcess(cfg)
	os.Exit(cfg.Exit)
}

func setAttemptEnv(t *testing.T, outDir string) {
	t.Helper()
	t.Setenv(helperProcessEnv, "1")
	t.Setenv("CI", "")
	t.Setenv("ZCL_ALLOW_UNSAFE_CAPTURE", "")
	t.Setenv("ZCL_OUT_DIR", outDir)
	t.Setenv("ZCL_RUN_ID", "20260215-180012Z-09c5a6")
	t.Setenv("ZCL_SUITE_ID", "heftiweb-smoke")
	t.Setenv("ZCL_MISSION_ID", "latest-blog-title")
	t.Setenv("ZCL_ATTEMPT_ID", "001-latest-blog-title-r1")

	// New invariant: funnels must have real attempt.json in ZCL_OUT_DIR.
	meta := schema.AttemptJSONV1{
		SchemaVersion: schema.AttemptSchemaV1,
		RunID:         os.Getenv("ZCL_RUN_ID"),
		SuiteID:       os.Getenv("ZCL_SUITE_ID"),
		MissionID:     os.Getenv("ZCL_MISSION_ID"),
		AttemptID:     os.Getenv("ZCL_ATTEMPT_ID"),
		Mode:          "discovery",
		StartedAt:     "2026-02-15T18:00:00Z",
	}
	b, err := json.Marshal(meta)
	if err != nil {
		t.Fatalf("marshal attempt.json: %v", err)
	}
	if err := os.WriteFile(filepath.Join(outDir, "attempt.json"), b, 0o644); err != nil {
		t.Fatalf("write attempt.json: %v", err)
	}
}

type helperProcessConfig struct {
	Helper  bool
	Stdout  string
	Stderr  string
	OutFile string
	Exit    int
}

func maybeRunHelperProcess() {
	if !helperProcessEnabled() {
		return
	}
	cfg := parseHelperProcessConfig(os.Args)
	if !cfg.Helper {
		return
	}
	runHelperProcess(cfg)
	os.Exit(cfg.Exit)
}

func helperProcessEnabled() bool {
	return os.Getenv(helperProcessEnv) == "1"
}

func parseHelperProcessConfig(args []string) helperProcessConfig {
	idx := 0
	for i := range args {
		if args[i] == "--" {
			idx = i + 1
			break
		}
	}
	cfg := helperProcessConfig{}
	for _, a := range args[idx:] {
		switch {
		case a == "helper=1":
			cfg.Helper = true
		case strings.HasPrefix(a, "stdout="):
			cfg.Stdout = strings.TrimPrefix(a, "stdout=")
		case strings.HasPrefix(a, "stdout-file="):
			cfg.OutFile = strings.TrimPrefix(a, "stdout-file=")
		case strings.HasPrefix(a, "stderr="):
			cfg.Stderr = strings.TrimPrefix(a, "stderr=")
		case strings.HasPrefix(a, "exit="):
			n, _ := strconv.Atoi(strings.TrimPrefix(a, "exit="))
			cfg.Exit = n
		}
	}
	return cfg
}

func runHelperProcess(cfg helperProcessConfig) {
	out := cfg.Stdout
	if cfg.OutFile != "" {
		b, err := os.ReadFile(cfg.OutFile)
		if err != nil {
			_, _ = os.Stderr.WriteString("helper read stdout-file: " + err.Error())
			_ = os.Stderr.Sync()
			os.Exit(2)
		}
		out = string(b)
	}
	_, _ = os.Stdout.WriteString(out)
	_, _ = os.Stderr.WriteString(cfg.Stderr)
	_ = os.Stdout.Sync()
	_ = os.Stderr.Sync()
}

func setAttemptMode(t *testing.T, outDir string, mode string) {
	t.Helper()
	path := filepath.Join(outDir, "attempt.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read attempt.json: %v", err)
	}
	var meta schema.AttemptJSONV1
	if err := json.Unmarshal(raw, &meta); err != nil {
		t.Fatalf("unmarshal attempt.json: %v", err)
	}
	meta.Mode = mode
	b, err := json.Marshal(meta)
	if err != nil {
		t.Fatalf("marshal attempt.json: %v", err)
	}
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatalf("write attempt.json: %v", err)
	}
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

func readTraceEvents(t *testing.T, path string) []schema.TraceEventV1 {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open trace: %v", err)
	}
	defer func() { _ = f.Close() }()

	var out []schema.TraceEventV1
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Bytes()
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var ev schema.TraceEventV1
		if err := json.Unmarshal(line, &ev); err != nil {
			t.Fatalf("unmarshal trace: %v", err)
		}
		out = append(out, ev)
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("scan: %v", err)
	}
	return out
}

func readSingleCaptureEvent(t *testing.T, path string) schema.CaptureEventV1 {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open captures: %v", err)
	}
	defer func() { _ = f.Close() }()

	sc := bufio.NewScanner(f)
	if !sc.Scan() {
		t.Fatalf("expected one captures line")
	}
	line := sc.Bytes()
	if err := sc.Err(); err != nil {
		t.Fatalf("scan: %v", err)
	}

	var ev schema.CaptureEventV1
	if err := json.Unmarshal(line, &ev); err != nil {
		t.Fatalf("unmarshal capture: %v", err)
	}
	if ev.StdoutPath == "" {
		t.Fatalf("expected stdoutPath")
	}
	return ev
}
