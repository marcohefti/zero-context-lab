package cli

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/marcohefti/zero-context-lab/internal/attempt"
	"github.com/marcohefti/zero-context-lab/internal/config"
	"github.com/marcohefti/zero-context-lab/internal/contract"
	clifunnel "github.com/marcohefti/zero-context-lab/internal/funnel/cli_funnel"
	"github.com/marcohefti/zero-context-lab/internal/trace"
)

type CliError struct {
	Code    string
	Message string
}

func (e *CliError) Error() string { return e.Message }

type Runner struct {
	Version string
	Now     func() time.Time
	Stdout  io.Writer
	Stderr  io.Writer
}

func (r Runner) Run(args []string) int {
	if r.Stdout == nil {
		r.Stdout = os.Stdout
	}
	if r.Stderr == nil {
		r.Stderr = os.Stderr
	}
	if r.Now == nil {
		r.Now = time.Now
	}

	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" || args[0] == "help" {
		printRootHelp(r.Stdout)
		return 0
	}

	switch args[0] {
	case "contract":
		return r.runContract(args[1:])
	case "init":
		return r.runInit(args[1:])
	case "run":
		return r.runRun(args[1:])
	case "attempt":
		return r.runAttempt(args[1:])
	case "version":
		fmt.Fprintf(r.Stdout, "%s\n", r.Version)
		return 0
	default:
		fmt.Fprintf(r.Stderr, "ZCL_E_USAGE: unknown command %q\n", args[0])
		printRootHelp(r.Stderr)
		return 2
	}
}

func (r Runner) runContract(args []string) int {
	fs := flag.NewFlagSet("contract", flag.ContinueOnError)
	fs.SetOutput(io.Discard) // avoid flag package writing to stderr

	jsonOut := fs.Bool("json", false, "print JSON output")
	help := fs.Bool("help", false, "show help")

	if err := fs.Parse(args); err != nil {
		return r.failUsage("contract: invalid flags")
	}
	if *help {
		printContractHelp(r.Stdout)
		return 0
	}
	if !*jsonOut {
		printContractHelp(r.Stderr)
		return r.failUsage("contract: require --json for stable output")
	}

	payload := contract.Build(r.Version)
	return r.writeJSON(payload)
}

func (r Runner) runInit(args []string) int {
	fs := flag.NewFlagSet("init", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	outRoot := fs.String("out-root", ".zcl", "project output root (default .zcl)")
	configPath := fs.String("config", config.DefaultProjectConfigPath, "project config path (default zcl.config.json)")
	jsonOut := fs.Bool("json", false, "print JSON output")
	help := fs.Bool("help", false, "show help")

	if err := fs.Parse(args); err != nil {
		return r.failUsage("init: invalid flags")
	}
	if *help {
		printInitHelp(r.Stdout)
		return 0
	}

	res, err := config.InitProject(*configPath, *outRoot)
	if err != nil {
		fmt.Fprintf(r.Stderr, "ZCL_E_IO: %s\n", err.Error())
		return 1
	}

	if *jsonOut {
		return r.writeJSON(res)
	}
	fmt.Fprintf(r.Stdout, "init: OK outRoot=%s config=%s created=%v\n", res.OutRoot, res.ConfigPath, res.Created)
	return 0
}

func (r Runner) runAttempt(args []string) int {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" || args[0] == "help" {
		printAttemptHelp(r.Stdout)
		return 0
	}
	switch args[0] {
	case "start":
		return r.runAttemptStart(args[1:])
	default:
		fmt.Fprintf(r.Stderr, "ZCL_E_USAGE: unknown attempt subcommand %q\n", args[0])
		printAttemptHelp(r.Stderr)
		return 2
	}
}

func (r Runner) runAttemptStart(args []string) int {
	fs := flag.NewFlagSet("attempt start", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	suite := fs.String("suite", "", "suite id (required)")
	mission := fs.String("mission", "", "mission id (required)")
	prompt := fs.String("prompt", "", "optional mission prompt to snapshot into prompt.txt")
	suiteFile := fs.String("suite-file", "", "optional JSON suite file to snapshot into suite.json")
	runID := fs.String("run-id", "", "existing run id (optional)")
	agentID := fs.String("agent-id", "", "runner agent id (optional)")
	mode := fs.String("mode", "discovery", "run mode: discovery|ci")
	outRoot := fs.String("out-root", ".zcl", "project output root (default .zcl)")
	retry := fs.Int("retry", 1, "attempt retry number (default 1)")
	jsonOut := fs.Bool("json", false, "print JSON output")
	help := fs.Bool("help", false, "show help")

	if err := fs.Parse(args); err != nil {
		return r.failUsage("attempt start: invalid flags")
	}
	if *help {
		printAttemptStartHelp(r.Stdout)
		return 0
	}
	if !*jsonOut {
		printAttemptStartHelp(r.Stderr)
		return r.failUsage("attempt start: require --json for stable output")
	}

	res, err := attempt.Start(r.Now(), attempt.StartOpts{
		OutRoot:   *outRoot,
		RunID:     *runID,
		SuiteID:   *suite,
		MissionID: *mission,
		AgentID:   *agentID,
		Mode:      *mode,
		Retry:     *retry,
		Prompt:    *prompt,
		SuiteFile: *suiteFile,
	})
	if err != nil {
		fmt.Fprintf(r.Stderr, "ZCL_E_USAGE: %s\n", err.Error())
		return 2
	}
	return r.writeJSON(res)
}

func (r Runner) runRun(args []string) int {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	help := fs.Bool("help", false, "show help")
	if err := fs.Parse(args); err != nil {
		return r.failUsage("run: invalid flags")
	}
	if *help {
		printRunHelp(r.Stdout)
		return 0
	}

	env, err := trace.EnvFromProcess()
	if err != nil {
		printRunHelp(r.Stderr)
		return r.failUsage("run: missing ZCL attempt context (run `zcl attempt start --json` and pass the returned env)")
	}

	argv := fs.Args()
	if len(argv) >= 1 && argv[0] == "--" {
		argv = argv[1:]
	}
	if len(argv) == 0 {
		printRunHelp(r.Stderr)
		return r.failUsage("run: missing command (use: zcl run -- <cmd> ...)")
	}

	// Keep this intentionally small for MVP; richer capture/trace wiring comes in Phase 3 Step 2+.
	res, runErr := clifunnel.Run(context.Background(), argv, r.Stdout, r.Stderr, 16*1024)

	traceRes := trace.ResultForTrace{
		ExitCode:     res.ExitCode,
		DurationMs:   res.DurationMs,
		OutBytes:     res.OutBytes,
		ErrBytes:     res.ErrBytes,
		OutPreview:   res.OutPreview,
		ErrPreview:   res.ErrPreview,
		OutTruncated: res.OutTruncated,
		ErrTruncated: res.ErrTruncated,
	}
	if runErr != nil {
		traceRes.SpawnError = "ZCL_E_SPAWN"
	}
	if err := trace.AppendCLIRunEvent(r.Now(), env, argv, traceRes); err != nil {
		fmt.Fprintf(r.Stderr, "ZCL_E_IO: failed to append tool.calls.jsonl: %s\n", err.Error())
		return 1
	}
	if runErr != nil {
		fmt.Fprintf(r.Stderr, "ZCL_E_IO: run failed: %s\n", runErr.Error())
		return 1
	}

	// Preserve the wrapped command's exit code for operator parity.
	if res.ExitCode != 0 {
		return res.ExitCode
	}
	return 0
}

func (r Runner) writeJSON(v any) int {
	enc := json.NewEncoder(r.Stdout)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		fmt.Fprintf(r.Stderr, "ZCL_E_IO: failed to encode json\n")
		return 1
	}
	return 0
}

func (r Runner) failUsage(msg string) int {
	fmt.Fprintf(r.Stderr, "ZCL_E_USAGE: %s\n", msg)
	return 2
}

func printRootHelp(w io.Writer) {
	fmt.Fprint(w, `ZCL (ZeroContext Lab)

Usage:
  zcl init [--out-root .zcl] [--config zcl.config.json] [--json]
  zcl contract --json
  zcl attempt start --suite <suiteId> --mission <missionId> --json
  zcl run -- <cmd> [args...]

Commands:
  init            Initialize the project (.zcl output root + zcl.config.json).
  contract        Print the ZCL surface contract (use --json).
  attempt start   Allocate a run/attempt dir and print canonical IDs + env (use --json).
  run             Run a command through the ZCL CLI funnel.
  version         Print version.
`)
}

func printInitHelp(w io.Writer) {
	fmt.Fprint(w, `Usage:
  zcl init [--out-root .zcl] [--config zcl.config.json] [--json]
`)
}

func printContractHelp(w io.Writer) {
	fmt.Fprint(w, `Usage:
  zcl contract --json
`)
}

func printAttemptHelp(w io.Writer) {
	fmt.Fprint(w, `Usage:
  zcl attempt start --suite <suiteId> --mission <missionId> --json
`)
}

func printAttemptStartHelp(w io.Writer) {
	fmt.Fprint(w, `Usage:
  zcl attempt start --suite <suiteId> --mission <missionId> [--prompt <text>] [--suite-file <path>] [--run-id <runId>] [--agent-id <id>] [--mode discovery|ci] [--out-root .zcl] [--retry 1] --json
`)
}

func printRunHelp(w io.Writer) {
	fmt.Fprint(w, `Usage:
  zcl run -- <cmd> [args...]
`)
}
