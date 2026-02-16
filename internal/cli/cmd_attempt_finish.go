package cli

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/marcohefti/zero-context-lab/internal/attempt"
	"github.com/marcohefti/zero-context-lab/internal/expect"
	"github.com/marcohefti/zero-context-lab/internal/report"
	"github.com/marcohefti/zero-context-lab/internal/validate"
)

func (r Runner) runAttemptFinish(args []string) int {
	fs := flag.NewFlagSet("attempt finish", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	strict := fs.Bool("strict", false, "strict mode (defaults to true in ci attempts)")
	strictExpect := fs.Bool("strict-expect", false, "strict mode for expect (missing suite.json/feedback.json fails)")
	jsonOut := fs.Bool("json", false, "print JSON output")
	help := fs.Bool("help", false, "show help")

	if err := fs.Parse(args); err != nil {
		return r.failUsage("attempt finish: invalid flags")
	}
	if *help {
		printAttemptFinishHelp(r.Stdout)
		return 0
	}

	rest := fs.Args()
	attemptDir := ""
	switch len(rest) {
	case 0:
		attemptDir = os.Getenv("ZCL_OUT_DIR")
	case 1:
		attemptDir = rest[0]
	default:
		printAttemptFinishHelp(r.Stderr)
		return r.failUsage("attempt finish: require at most one <attemptDir> (or use ZCL_OUT_DIR)")
	}
	if attemptDir == "" {
		printAttemptFinishHelp(r.Stderr)
		return r.failUsage("attempt finish: missing <attemptDir> (or set ZCL_OUT_DIR)")
	}

	info, err := os.Stat(attemptDir)
	if err != nil {
		fmt.Fprintf(r.Stderr, "ZCL_E_IO: %s\n", err.Error())
		return 1
	}
	if !info.IsDir() {
		return r.failUsage("attempt finish: target must be a directory")
	}

	*strict = attempt.EffectiveStrict(attemptDir, *strict)

	rep, err := report.BuildAttemptReport(r.Now(), attemptDir, *strict)
	if err != nil {
		return r.printReportErr(err)
	}
	if err := report.WriteAttemptReportAtomic(filepath.Join(attemptDir, "attempt.report.json"), rep); err != nil {
		fmt.Fprintf(r.Stderr, "ZCL_E_IO: %s\n", err.Error())
		return 1
	}

	valRes, err := validate.ValidatePath(attemptDir, *strict)
	if err != nil {
		fmt.Fprintf(r.Stderr, "ZCL_E_IO: %s\n", err.Error())
		return 1
	}
	expRes, err := expect.ExpectPath(attemptDir, *strictExpect)
	if err != nil {
		fmt.Fprintf(r.Stderr, "ZCL_E_IO: %s\n", err.Error())
		return 1
	}

	ok := valRes.OK && expRes.OK
	if rep.OK != nil && !*rep.OK {
		ok = false
	}

	if *jsonOut {
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
			Strict:       *strict,
			StrictExpect: *strictExpect,
			AttemptDir:   attemptDir,
			Report:       rep,
			Validate:     valRes,
			Expect:       expRes,
		}
		enc := json.NewEncoder(r.Stdout)
		enc.SetIndent("", "  ")
		enc.SetEscapeHTML(false)
		if err := enc.Encode(out); err != nil {
			fmt.Fprintf(r.Stderr, "ZCL_E_IO: failed to encode json\n")
			return 1
		}
		if ok {
			return 0
		}
		return 2
	}

	if ok {
		fmt.Fprintf(r.Stdout, "attempt finish: OK strict=%v\n", *strict)
		return 0
	}
	fmt.Fprintf(r.Stderr, "attempt finish: FAIL strict=%v\n", *strict)
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
