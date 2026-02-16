package cli

import (
	"flag"
	"fmt"
	"io"

	"github.com/marcohefti/zero-context-lab/internal/config"
	"github.com/marcohefti/zero-context-lab/internal/pin"
)

func (r Runner) runPin(args []string) int {
	fs := flag.NewFlagSet("pin", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	runID := fs.String("run-id", "", "run id to pin/unpin (required)")
	on := fs.Bool("on", false, "pin the run")
	off := fs.Bool("off", false, "unpin the run")
	outRoot := fs.String("out-root", "", "project output root (default from config/env, else .zcl)")
	jsonOut := fs.Bool("json", false, "print JSON output")
	help := fs.Bool("help", false, "show help")

	if err := fs.Parse(args); err != nil {
		return r.failUsage("pin: invalid flags")
	}
	if *help {
		printPinHelp(r.Stdout)
		return 0
	}
	if (*on && *off) || (!*on && !*off) {
		printPinHelp(r.Stderr)
		return r.failUsage("pin: require exactly one of --on or --off")
	}

	m, err := config.LoadMerged(*outRoot)
	if err != nil {
		fmt.Fprintf(r.Stderr, "ZCL_E_IO: %s\n", err.Error())
		return 1
	}

	res, err := pin.Set(pin.Opts{OutRoot: m.OutRoot, RunID: *runID, Pinned: *on})
	if err != nil {
		fmt.Fprintf(r.Stderr, "ZCL_E_USAGE: %s\n", err.Error())
		return 2
	}
	if *jsonOut {
		return r.writeJSON(res)
	}
	if res.Pinned {
		fmt.Fprintf(r.Stdout, "pin: OK runId=%s pinned=true\n", res.RunID)
	} else {
		fmt.Fprintf(r.Stdout, "pin: OK runId=%s pinned=false\n", res.RunID)
	}
	return 0
}

func printPinHelp(w io.Writer) {
	fmt.Fprint(w, `Usage:
  zcl pin --run-id <runId> --on|--off [--out-root .zcl] [--json]
`)
}
