package cli

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"github.com/marcohefti/zero-context-lab/internal/kernel/artifacts"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/marcohefti/zero-context-lab/internal/contexts/evaluation/app/expect"
	"github.com/marcohefti/zero-context-lab/internal/contexts/evaluation/app/report"
	"github.com/marcohefti/zero-context-lab/internal/contexts/evaluation/app/validate"
	"github.com/marcohefti/zero-context-lab/internal/contexts/execution/app/attempt"
	"github.com/marcohefti/zero-context-lab/internal/kernel/schema"
)

func (r Runner) runAttemptExplain(args []string) int {
	opts, exit, ok := r.parseAttemptExplainArgs(args)
	if !ok {
		return exit
	}
	opts.strict = attempt.EffectiveStrict(opts.attemptDir, opts.strict)
	ids := loadAttemptExplainIDs(opts.attemptDir)
	rep, repPresent := r.loadAttemptExplainReport(opts.attemptDir, opts.strict)
	valRes, _ := validate.ValidatePath(opts.attemptDir, opts.strict)
	expRes, _ := expect.ExpectPath(opts.attemptDir, false)
	tail, tailErr := tailTraceEvents(filepath.Join(opts.attemptDir, artifacts.ToolCallsJSONL), opts.tailN)
	if tailErr != nil && opts.strict {
		fmt.Fprintf(r.Stderr, codeIO+": %s\n", tailErr.Error())
		return 1
	}
	out := buildAttemptExplainOutput(opts.attemptDir, opts.strict, ids, rep, repPresent, valRes, expRes, tail)
	if opts.jsonOut {
		return r.writeJSON(out)
	}
	r.printAttemptExplainHuman(out)
	return 0
}

type attemptExplainArgs struct {
	attemptDir string
	tailN      int
	strict     bool
	jsonOut    bool
}

type attemptExplainOutput struct {
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
}

func (r Runner) parseAttemptExplainArgs(args []string) (attemptExplainArgs, int, bool) {
	fs := flag.NewFlagSet("attempt explain", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	tailN := fs.Int("tail", 20, "number of tail trace events to include")
	strict := fs.Bool("strict", false, "strict mode (defaults to true in ci attempts)")
	jsonOut := fs.Bool("json", false, "print JSON output")
	help := fs.Bool("help", false, "show help")
	if err := fs.Parse(args); err != nil {
		return attemptExplainArgs{}, r.failUsage("attempt explain: invalid flags"), false
	}
	if *help {
		printAttemptExplainHelp(r.Stdout)
		return attemptExplainArgs{}, 0, false
	}
	if *tailN < 0 {
		printAttemptExplainHelp(r.Stderr)
		return attemptExplainArgs{}, r.failUsage("attempt explain: --tail must be >= 0"), false
	}
	attemptDir, exit, ok := r.resolveAttemptExplainTarget(fs.Args())
	if !ok {
		return attemptExplainArgs{}, exit, false
	}
	return attemptExplainArgs{
		attemptDir: attemptDir,
		tailN:      *tailN,
		strict:     *strict,
		jsonOut:    *jsonOut,
	}, 0, true
}

func (r Runner) resolveAttemptExplainTarget(rest []string) (string, int, bool) {
	attemptDir := ""
	switch len(rest) {
	case 0:
		attemptDir = os.Getenv("ZCL_OUT_DIR")
	case 1:
		attemptDir = rest[0]
	default:
		printAttemptExplainHelp(r.Stderr)
		return "", r.failUsage("attempt explain: require at most one <attemptDir> (or use ZCL_OUT_DIR)"), false
	}
	if attemptDir == "" {
		printAttemptExplainHelp(r.Stderr)
		return "", r.failUsage("attempt explain: missing <attemptDir> (or set ZCL_OUT_DIR)"), false
	}
	info, err := os.Stat(attemptDir)
	if err != nil {
		fmt.Fprintf(r.Stderr, codeIO+": %s\n", err.Error())
		return "", 1, false
	}
	if !info.IsDir() {
		return "", r.failUsage("attempt explain: target must be a directory"), false
	}
	return attemptDir, 0, true
}

func loadAttemptExplainIDs(attemptDir string) schema.AttemptJSONV1 {
	var out schema.AttemptJSONV1
	if b, err := os.ReadFile(filepath.Join(attemptDir, artifacts.AttemptJSON)); err == nil {
		_ = json.Unmarshal(b, &out)
	}
	return out
}

func (r Runner) loadAttemptExplainReport(attemptDir string, strict bool) (schema.AttemptReportJSONV1, bool) {
	var rep schema.AttemptReportJSONV1
	if b, err := os.ReadFile(filepath.Join(attemptDir, artifacts.AttemptReportJSON)); err == nil {
		if err := json.Unmarshal(b, &rep); err == nil {
			return rep, true
		}
	}
	if generated, err := report.BuildAttemptReport(r.Now(), attemptDir, strict); err == nil {
		return generated, true
	}
	return schema.AttemptReportJSONV1{}, false
}

func buildAttemptExplainOutput(attemptDir string, strict bool, ids schema.AttemptJSONV1, rep schema.AttemptReportJSONV1, repPresent bool, valRes validate.Result, expRes expect.Result, tail []schema.TraceEventV1) attemptExplainOutput {
	out := attemptExplainOutput{
		AttemptDir:    attemptDir,
		Strict:        strict,
		RunID:         ids.RunID,
		SuiteID:       ids.SuiteID,
		MissionID:     ids.MissionID,
		AttemptID:     ids.AttemptID,
		ReportPresent: repPresent,
		Validate:      valRes,
		Expect:        expRes,
		Tail:          tail,
	}
	if repPresent {
		out.Report = &rep
		if out.RunID == "" {
			out.RunID = rep.RunID
			out.SuiteID = rep.SuiteID
			out.MissionID = rep.MissionID
			out.AttemptID = rep.AttemptID
		}
	}
	if p := filepath.Join(attemptDir, "runner.command.txt"); fileExists(p) {
		out.RunnerCommandPath = p
	}
	if p := filepath.Join(attemptDir, "runner.stdout.log"); fileExists(p) {
		out.RunnerStdoutPath = p
	}
	if p := filepath.Join(attemptDir, "runner.stderr.log"); fileExists(p) {
		out.RunnerStderrPath = p
	}
	return out
}

func (r Runner) printAttemptExplainHuman(out attemptExplainOutput) {
	fmt.Fprintf(r.Stdout, "attempt explain: %s\n", out.AttemptDir)
	if out.RunID != "" {
		fmt.Fprintf(r.Stdout, "  ids: run=%s suite=%s mission=%s attempt=%s\n", out.RunID, out.SuiteID, out.MissionID, out.AttemptID)
	}
	if out.ReportPresent && out.Report != nil && out.Report.OK != nil {
		fmt.Fprintf(r.Stdout, "  outcome: ok=%v result=%s\n", *out.Report.OK, strings.TrimSpace(out.Report.Result))
	}
	if out.ReportPresent && out.Report != nil && out.Report.Signals != nil && out.Report.Signals.NoProgressSuspected {
		fmt.Fprintf(r.Stdout, "  signals: no_progress_suspected=true repeatMaxStreak=%d distinct=%d failureRateBps=%d\n",
			out.Report.Signals.RepeatMaxStreak, out.Report.Signals.DistinctCommandSignatures, out.Report.Signals.FailureRateBps)
	}
	if !out.Validate.OK {
		fmt.Fprintf(r.Stdout, "  validate: FAIL (%d issues)\n", len(out.Validate.Errors))
	}
	if !out.Expect.OK {
		fmt.Fprintf(r.Stdout, "  expect: FAIL evaluated=%v (%d failures)\n", out.Expect.Evaluated, len(out.Expect.Failures))
	}
	if out.RunnerCommandPath != "" {
		fmt.Fprintf(r.Stdout, "  runner: %s\n", out.RunnerCommandPath)
	}
	if len(out.Tail) > 0 {
		fmt.Fprintf(r.Stdout, "  tail (last %d):\n", len(out.Tail))
		for _, ev := range out.Tail {
			fmt.Fprintf(r.Stdout, "    %s %s %s ok=%v code=%s\n", ev.Tool, ev.Op, oneLineInput(ev.Input), ev.Result.OK, ev.Result.Code)
		}
	}
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
