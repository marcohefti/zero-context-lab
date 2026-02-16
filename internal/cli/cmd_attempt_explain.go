package cli

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/marcohefti/zero-context-lab/internal/attempt"
	"github.com/marcohefti/zero-context-lab/internal/expect"
	"github.com/marcohefti/zero-context-lab/internal/report"
	"github.com/marcohefti/zero-context-lab/internal/schema"
	"github.com/marcohefti/zero-context-lab/internal/validate"
)

func (r Runner) runAttemptExplain(args []string) int {
	fs := flag.NewFlagSet("attempt explain", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	tailN := fs.Int("tail", 20, "number of tail trace events to include")
	strict := fs.Bool("strict", false, "strict mode (defaults to true in ci attempts)")
	jsonOut := fs.Bool("json", false, "print JSON output")
	help := fs.Bool("help", false, "show help")

	if err := fs.Parse(args); err != nil {
		return r.failUsage("attempt explain: invalid flags")
	}
	if *help {
		printAttemptExplainHelp(r.Stdout)
		return 0
	}
	if *tailN < 0 {
		printAttemptExplainHelp(r.Stderr)
		return r.failUsage("attempt explain: --tail must be >= 0")
	}

	rest := fs.Args()
	attemptDir := ""
	switch len(rest) {
	case 0:
		attemptDir = os.Getenv("ZCL_OUT_DIR")
	case 1:
		attemptDir = rest[0]
	default:
		printAttemptExplainHelp(r.Stderr)
		return r.failUsage("attempt explain: require at most one <attemptDir> (or use ZCL_OUT_DIR)")
	}
	if attemptDir == "" {
		printAttemptExplainHelp(r.Stderr)
		return r.failUsage("attempt explain: missing <attemptDir> (or set ZCL_OUT_DIR)")
	}

	info, err := os.Stat(attemptDir)
	if err != nil {
		fmt.Fprintf(r.Stderr, "ZCL_E_IO: %s\n", err.Error())
		return 1
	}
	if !info.IsDir() {
		return r.failUsage("attempt explain: target must be a directory")
	}

	*strict = attempt.EffectiveStrict(attemptDir, *strict)

	// Read attempt.json for ids even if other artifacts are missing.
	var a schema.AttemptJSONV1
	if b, err := os.ReadFile(filepath.Join(attemptDir, "attempt.json")); err == nil {
		_ = json.Unmarshal(b, &a)
	}

	var rep schema.AttemptReportJSONV1
	repPresent := false
	if b, err := os.ReadFile(filepath.Join(attemptDir, "attempt.report.json")); err == nil {
		if err := json.Unmarshal(b, &rep); err == nil {
			repPresent = true
		}
	}
	// Best-effort compute (without writing) when report is missing or invalid.
	if !repPresent {
		if r2, err := report.BuildAttemptReport(r.Now(), attemptDir, *strict); err == nil {
			rep = r2
			repPresent = true
		}
	}

	valRes, _ := validate.ValidatePath(attemptDir, *strict)
	expRes, _ := expect.ExpectPath(attemptDir, false)

	tail, tailErr := tailTraceEvents(filepath.Join(attemptDir, "tool.calls.jsonl"), *tailN)
	if tailErr != nil && *strict {
		fmt.Fprintf(r.Stderr, "ZCL_E_IO: %s\n", tailErr.Error())
		return 1
	}

	out := struct {
		AttemptDir string `json:"attemptDir"`
		Strict     bool   `json:"strict"`

		RunID     string `json:"runId,omitempty"`
		SuiteID   string `json:"suiteId,omitempty"`
		MissionID string `json:"missionId,omitempty"`
		AttemptID string `json:"attemptId,omitempty"`

		ReportPresent bool                        `json:"reportPresent"`
		Report        *schema.AttemptReportJSONV1 `json:"report,omitempty"`
		Validate      validate.Result             `json:"validate"`
		Expect        expect.Result               `json:"expect"`

		Tail []schema.TraceEventV1 `json:"tail,omitempty"`

		RunnerCommandPath string `json:"runnerCommandPath,omitempty"`
		RunnerStdoutPath  string `json:"runnerStdoutPath,omitempty"`
		RunnerStderrPath  string `json:"runnerStderrPath,omitempty"`
	}{
		AttemptDir:    attemptDir,
		Strict:        *strict,
		RunID:         a.RunID,
		SuiteID:       a.SuiteID,
		MissionID:     a.MissionID,
		AttemptID:     a.AttemptID,
		ReportPresent: repPresent,
		Validate:      valRes,
		Expect:        expRes,
		Tail:          tail,
	}
	if repPresent {
		out.Report = &rep
		// If attempt.json couldn't be loaded, fall back to report ids.
		if out.RunID == "" {
			out.RunID = rep.RunID
			out.SuiteID = rep.SuiteID
			out.MissionID = rep.MissionID
			out.AttemptID = rep.AttemptID
		}
	}

	// Pointers to runner IO artifacts when present.
	if p := filepath.Join(attemptDir, "runner.command.txt"); fileExists(p) {
		out.RunnerCommandPath = p
	}
	if p := filepath.Join(attemptDir, "runner.stdout.log"); fileExists(p) {
		out.RunnerStdoutPath = p
	}
	if p := filepath.Join(attemptDir, "runner.stderr.log"); fileExists(p) {
		out.RunnerStderrPath = p
	}

	if *jsonOut {
		return r.writeJSON(out)
	}

	// Human output: one screen, pointers + tail.
	fmt.Fprintf(r.Stdout, "attempt explain: %s\n", attemptDir)
	if out.RunID != "" {
		fmt.Fprintf(r.Stdout, "  ids: run=%s suite=%s mission=%s attempt=%s\n", out.RunID, out.SuiteID, out.MissionID, out.AttemptID)
	}
	if repPresent && rep.OK != nil {
		fmt.Fprintf(r.Stdout, "  outcome: ok=%v result=%s\n", *rep.OK, strings.TrimSpace(rep.Result))
	}
	if repPresent && rep.Signals != nil && rep.Signals.NoProgressSuspected {
		fmt.Fprintf(r.Stdout, "  signals: no_progress_suspected=true repeatMaxStreak=%d distinct=%d failureRateBps=%d\n",
			rep.Signals.RepeatMaxStreak, rep.Signals.DistinctCommandSignatures, rep.Signals.FailureRateBps)
	}
	if !valRes.OK {
		fmt.Fprintf(r.Stdout, "  validate: FAIL (%d issues)\n", len(valRes.Errors))
	}
	if !expRes.OK {
		fmt.Fprintf(r.Stdout, "  expect: FAIL evaluated=%v (%d failures)\n", expRes.Evaluated, len(expRes.Failures))
	}
	if out.RunnerCommandPath != "" {
		fmt.Fprintf(r.Stdout, "  runner: %s\n", out.RunnerCommandPath)
	}
	if len(tail) > 0 {
		fmt.Fprintf(r.Stdout, "  tail (last %d):\n", len(tail))
		for _, ev := range tail {
			fmt.Fprintf(r.Stdout, "    %s %s %s ok=%v code=%s\n", ev.Tool, ev.Op, oneLineInput(ev.Input), ev.Result.OK, ev.Result.Code)
		}
	}
	return 0
}

func printAttemptExplainHelp(w io.Writer) {
	fmt.Fprint(w, `Usage:
  zcl attempt explain [--strict] [--json] [--tail N] [<attemptDir>]

Notes:
  - If <attemptDir> is omitted, ZCL_OUT_DIR is used.
  - Best-effort: reads attempt.report.json when present, otherwise computes an in-memory report.
  - Includes a tail of tool.calls.jsonl to make post-mortems fast from artifacts alone.
`)
}

func tailTraceEvents(path string, n int) ([]schema.TraceEventV1, error) {
	if n <= 0 {
		return nil, nil
	}
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer func() { _ = f.Close() }()

	// Ring buffer of the last n parsed events.
	events := make([]schema.TraceEventV1, 0, n)
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var ev schema.TraceEventV1
		if err := json.Unmarshal(line, &ev); err != nil {
			continue
		}
		if len(events) < n {
			events = append(events, ev)
			continue
		}
		// Shift left by 1 (n is small by design).
		copy(events, events[1:])
		events[len(events)-1] = ev
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return events, nil
}

func oneLineInput(in json.RawMessage) string {
	if len(in) == 0 {
		return "-"
	}
	s := strings.TrimSpace(string(in))
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "\r", " ")
	if len(s) > 120 {
		s = s[:120] + "..."
	}
	return s
}
