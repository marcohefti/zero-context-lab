package cli

import (
	"encoding/json"
	"flag"
	"fmt"
	"github.com/marcohefti/zero-context-lab/internal/kernel/artifacts"
	"io"
	"os"
	"path/filepath"

	"github.com/marcohefti/zero-context-lab/internal/contexts/evaluation/app/expect"
	"github.com/marcohefti/zero-context-lab/internal/contexts/evaluation/app/report"
	"github.com/marcohefti/zero-context-lab/internal/contexts/evaluation/app/validate"
	"github.com/marcohefti/zero-context-lab/internal/contexts/execution/app/attempt"
	"github.com/marcohefti/zero-context-lab/internal/kernel/schema"
)

func (r Runner) runAttemptFinish(args []string) int {
	opts, exit, done := r.parseAttemptFinishOptions(args)
	if done {
		return exit
	}
	if exit, done := r.validateAttemptFinishDir(opts.attemptDir); done {
		return exit
	}

	strict := attempt.EffectiveStrict(opts.attemptDir, opts.strict)
	rep, valRes, expRes, ok, exit, done := r.executeAttemptFinish(strict, opts.strictExpect, opts.attemptDir)
	if done {
		return exit
	}
	if opts.jsonOut {
		return r.writeAttemptFinishJSON(ok, strict, opts.strictExpect, opts.attemptDir, rep, valRes, expRes)
	}
	return r.writeAttemptFinishText(ok, strict, rep, valRes, expRes)
}

type attemptFinishOptions struct {
	strict       bool
	strictExpect bool
	jsonOut      bool
	attemptDir   string
}

func (r Runner) parseAttemptFinishOptions(args []string) (attemptFinishOptions, int, bool) {
	fs := flag.NewFlagSet("attempt finish", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	strict := fs.Bool("strict", false, "strict mode (defaults to true in ci attempts)")
	strictExpect := fs.Bool("strict-expect", false, "strict mode for expect (missing suite.json/feedback.json fails)")
	jsonOut := fs.Bool("json", false, "print JSON output")
	help := fs.Bool("help", false, "show help")

	if err := fs.Parse(args); err != nil {
		return attemptFinishOptions{}, r.failUsage("attempt finish: invalid flags"), true
	}
	if *help {
		printAttemptFinishHelp(r.Stdout)
		return attemptFinishOptions{}, 0, true
	}

	rest := fs.Args()
	attemptDir := os.Getenv("ZCL_OUT_DIR")
	switch len(rest) {
	case 0:
		// Use ZCL_OUT_DIR fallback.
	case 1:
		attemptDir = rest[0]
	default:
		printAttemptFinishHelp(r.Stderr)
		return attemptFinishOptions{}, r.failUsage("attempt finish: require at most one <attemptDir> (or use ZCL_OUT_DIR)"), true
	}
	if attemptDir == "" {
		printAttemptFinishHelp(r.Stderr)
		return attemptFinishOptions{}, r.failUsage("attempt finish: missing <attemptDir> (or set ZCL_OUT_DIR)"), true
	}
	return attemptFinishOptions{
		strict:       *strict,
		strictExpect: *strictExpect,
		jsonOut:      *jsonOut,
		attemptDir:   attemptDir,
	}, 0, false
}

func (r Runner) validateAttemptFinishDir(attemptDir string) (int, bool) {
	info, err := os.Stat(attemptDir)
	if err != nil {
		fmt.Fprintf(r.Stderr, codeIO+": %s\n", err.Error())
		return 1, true
	}
	if !info.IsDir() {
		return r.failUsage("attempt finish: target must be a directory"), true
	}
	return 0, false
}

func (r Runner) executeAttemptFinish(strict, strictExpect bool, attemptDir string) (schema.AttemptReportJSONV1, validate.Result, expect.Result, bool, int, bool) {
	rep, err := report.BuildAttemptReport(r.Now(), attemptDir, strict)
	if err != nil {
		return schema.AttemptReportJSONV1{}, validate.Result{}, expect.Result{}, false, r.printReportErr(err), true
	}
	if err := report.WriteAttemptReportAtomic(filepath.Join(attemptDir, artifacts.AttemptReportJSON), rep); err != nil {
		fmt.Fprintf(r.Stderr, codeIO+": %s\n", err.Error())
		return schema.AttemptReportJSONV1{}, validate.Result{}, expect.Result{}, false, 1, true
	}

	valRes, err := validate.ValidatePath(attemptDir, strict)
	if err != nil {
		fmt.Fprintf(r.Stderr, codeIO+": %s\n", err.Error())
		return schema.AttemptReportJSONV1{}, validate.Result{}, expect.Result{}, false, 1, true
	}
	expRes, err := expect.ExpectPath(attemptDir, strictExpect)
	if err != nil {
		fmt.Fprintf(r.Stderr, codeIO+": %s\n", err.Error())
		return schema.AttemptReportJSONV1{}, validate.Result{}, expect.Result{}, false, 1, true
	}

	ok := valRes.OK && expRes.OK
	if rep.OK != nil && !*rep.OK {
		ok = false
	}
	return rep, valRes, expRes, ok, 0, false
}

func (r Runner) writeAttemptFinishJSON(ok, strict, strictExpect bool, attemptDir string, rep schema.AttemptReportJSONV1, valRes validate.Result, expRes expect.Result) int {
	out := struct {
		OK           bool            `json:"ok"`
		Strict       bool            `json:"strict"`
		StrictExpect bool            `json:"strictExpect"`
		AttemptDir   string          `json:"attemptDir"`
		Report       any             `json:"report"`
		Validate     validate.Result `json:"validate"`
		Expect       expect.Result   `json:"expect"`
	}{
		OK:           ok,
		Strict:       strict,
		StrictExpect: strictExpect,
		AttemptDir:   attemptDir,
		Report:       rep,
		Validate:     valRes,
		Expect:       expRes,
	}
	enc := json.NewEncoder(r.Stdout)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	if err := enc.Encode(out); err != nil {
		fmt.Fprintf(r.Stderr, codeIO+": failed to encode json\n")
		return 1
	}
	if ok {
		return 0
	}
	return 2
}

func (r Runner) writeAttemptFinishText(ok, strict bool, rep schema.AttemptReportJSONV1, valRes validate.Result, expRes expect.Result) int {
	if ok {
		fmt.Fprintf(r.Stdout, "attempt finish: OK strict=%v\n", strict)
		return 0
	}
	fmt.Fprintf(r.Stderr, "attempt finish: FAIL strict=%v\n", strict)
	if !valRes.OK {
		fmt.Fprintf(r.Stderr, "  validate: FAIL\n")
	}
	if !expRes.OK {
		fmt.Fprintf(r.Stderr, "  expect: FAIL evaluated=%v\n", expRes.Evaluated)
	}
	if rep.OK != nil && !*rep.OK {
		fmt.Fprintf(r.Stderr, "  outcome: ok=false\n")
	}
	return 2
}

func printAttemptFinishHelp(w io.Writer) {
	fmt.Fprint(w, `Usage:
  zcl attempt finish [--strict] [--strict-expect] [--json] [<attemptDir>]

Notes:
  - If <attemptDir> is omitted, ZCL_OUT_DIR is used.
  - Writes attempt.report.json, then runs validate + expect.
`)
}
