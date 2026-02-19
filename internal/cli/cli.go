package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/marcohefti/zero-context-lab/internal/attempt"
	"github.com/marcohefti/zero-context-lab/internal/blind"
	"github.com/marcohefti/zero-context-lab/internal/config"
	"github.com/marcohefti/zero-context-lab/internal/contract"
	"github.com/marcohefti/zero-context-lab/internal/doctor"
	"github.com/marcohefti/zero-context-lab/internal/enrich"
	"github.com/marcohefti/zero-context-lab/internal/expect"
	"github.com/marcohefti/zero-context-lab/internal/feedback"
	"github.com/marcohefti/zero-context-lab/internal/funnel/mcp_proxy"
	"github.com/marcohefti/zero-context-lab/internal/gc"
	"github.com/marcohefti/zero-context-lab/internal/note"
	"github.com/marcohefti/zero-context-lab/internal/planner"
	"github.com/marcohefti/zero-context-lab/internal/replay"
	"github.com/marcohefti/zero-context-lab/internal/report"
	"github.com/marcohefti/zero-context-lab/internal/schema"
	"github.com/marcohefti/zero-context-lab/internal/store"
	"github.com/marcohefti/zero-context-lab/internal/trace"
	"github.com/marcohefti/zero-context-lab/internal/validate"
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
	case "feedback":
		return r.runFeedback(args[1:])
	case "note":
		return r.runNote(args[1:])
	case "report":
		return r.runReport(args[1:])
	case "validate":
		return r.runValidate(args[1:])
	case "doctor":
		return r.runDoctor(args[1:])
	case "gc":
		return r.runGC(args[1:])
	case "pin":
		return r.runPin(args[1:])
	case "enrich":
		return r.runEnrich(args[1:])
	case "mcp":
		return r.runMCP(args[1:])
	case "http":
		return r.runHTTP(args[1:])
	case "run":
		return r.runRun(args[1:])
	case "attempt":
		return r.runAttempt(args[1:])
	case "suite":
		return r.runSuite(args[1:])
	case "replay":
		return r.runReplay(args[1:])
	case "expect":
		return r.runExpect(args[1:])
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

	outRoot := fs.String("out-root", "", "project output root (default from config/env, else .zcl)")
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

	m, err := config.LoadMerged(*outRoot)
	if err != nil {
		fmt.Fprintf(r.Stderr, "ZCL_E_IO: %s\n", err.Error())
		return 1
	}
	res, err := config.InitProject(*configPath, m.OutRoot)
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
	case "finish":
		return r.runAttemptFinish(args[1:])
	case "explain":
		return r.runAttemptExplain(args[1:])
	default:
		fmt.Fprintf(r.Stderr, "ZCL_E_USAGE: unknown attempt subcommand %q\n", args[0])
		printAttemptHelp(r.Stderr)
		return 2
	}
}

func (r Runner) runSuite(args []string) int {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" || args[0] == "help" {
		printSuiteHelp(r.Stdout)
		return 0
	}
	switch args[0] {
	case "plan":
		return r.runSuitePlan(args[1:])
	case "run":
		return r.runSuiteRun(args[1:])
	default:
		fmt.Fprintf(r.Stderr, "ZCL_E_USAGE: unknown suite subcommand %q\n", args[0])
		printSuiteHelp(r.Stderr)
		return 2
	}
}

func (r Runner) runSuitePlan(args []string) int {
	fs := flag.NewFlagSet("suite plan", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	file := fs.String("file", "", "suite file path (.json|.yaml|.yml) (required)")
	runID := fs.String("run-id", "", "existing run id (optional)")
	mode := fs.String("mode", "", "optional mode override: discovery|ci (default from suite file)")
	timeoutMs := fs.Int64("timeout-ms", 0, "optional attempt timeout override in ms (default from suite defaults.timeoutMs)")
	timeoutStart := fs.String("timeout-start", "", "optional timeout anchor override: attempt_start|first_tool_call")
	blindOverride := fs.String("blind", "", "optional blind-mode override: on|off")
	blindTerms := fs.String("blind-terms", "", "optional comma-separated blind harness terms override")
	outRoot := fs.String("out-root", "", "project output root (default from config/env, else .zcl)")
	jsonOut := fs.Bool("json", false, "print JSON output")
	help := fs.Bool("help", false, "show help")

	if err := fs.Parse(args); err != nil {
		return r.failUsage("suite plan: invalid flags")
	}
	if *help {
		printSuitePlanHelp(r.Stdout)
		return 0
	}
	if !*jsonOut {
		printSuitePlanHelp(r.Stderr)
		return r.failUsage("suite plan: require --json for stable output")
	}

	m, err := config.LoadMerged(*outRoot)
	if err != nil {
		fmt.Fprintf(r.Stderr, "ZCL_E_IO: %s\n", err.Error())
		return 1
	}

	var blindPtr *bool
	if strings.TrimSpace(*blindOverride) != "" {
		switch strings.ToLower(strings.TrimSpace(*blindOverride)) {
		case "on", "true", "1", "yes":
			v := true
			blindPtr = &v
		case "off", "false", "0", "no":
			v := false
			blindPtr = &v
		default:
			fmt.Fprintf(r.Stderr, "ZCL_E_USAGE: suite plan: invalid --blind (expected on|off)\n")
			return 2
		}
	}

	res, err := planner.PlanSuite(r.Now(), planner.SuitePlanOpts{
		OutRoot:      m.OutRoot,
		RunID:        strings.TrimSpace(*runID),
		SuiteFile:    strings.TrimSpace(*file),
		Mode:         strings.TrimSpace(*mode),
		TimeoutMs:    *timeoutMs,
		TimeoutStart: strings.TrimSpace(*timeoutStart),
		Blind:        blindPtr,
		BlindTerms:   blind.ParseTermsCSV(*blindTerms),
	})
	if err != nil {
		fmt.Fprintf(r.Stderr, "ZCL_E_USAGE: %s\n", err.Error())
		return 2
	}
	return r.writeJSON(res)
}

func (r Runner) runReplay(args []string) int {
	fs := flag.NewFlagSet("replay", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	execute := fs.Bool("execute", false, "execute replayable steps (default is dry-run)")
	allowAll := fs.Bool("allow-all", false, "allow executing any replayable command (unsafe)")
	allowCSV := fs.String("allow", "", "comma-separated command basenames allowed when executing (e.g. echo,cat)")
	maxSteps := fs.Int("max-steps", 50, "max steps to read from tool.calls.jsonl")
	useStdin := fs.Bool("stdin", false, "forward stdin to executed commands (default is empty stdin)")
	jsonOut := fs.Bool("json", false, "print JSON output")
	help := fs.Bool("help", false, "show help")
	if err := fs.Parse(args); err != nil {
		return r.failUsage("replay: invalid flags")
	}
	if *help {
		printReplayHelp(r.Stdout)
		return 0
	}
	if !*jsonOut {
		printReplayHelp(r.Stderr)
		return r.failUsage("replay: require --json for stable output")
	}

	rest := fs.Args()
	if len(rest) != 1 {
		printReplayHelp(r.Stderr)
		return r.failUsage("replay: require exactly one <attemptDir>")
	}

	allowCmds := map[string]bool{}
	if strings.TrimSpace(*allowCSV) != "" {
		for _, part := range strings.Split(*allowCSV, ",") {
			if v := strings.TrimSpace(part); v != "" {
				allowCmds[v] = true
			}
		}
	}
	res, err := replay.ReplayAttempt(context.Background(), rest[0], replay.Opts{
		Execute:   *execute,
		AllowAll:  *allowAll,
		AllowCmds: allowCmds,
		MaxSteps:  *maxSteps,
		UseStdin:  *useStdin,
	})
	if err != nil {
		fmt.Fprintf(r.Stderr, "ZCL_E_IO: %s\n", err.Error())
		return 1
	}
	return r.writeJSON(res)
}

func (r Runner) runExpect(args []string) int {
	fs := flag.NewFlagSet("expect", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	strict := fs.Bool("strict", false, "strict mode (missing suite.json/feedback.json fails)")
	jsonOut := fs.Bool("json", false, "print JSON output")
	help := fs.Bool("help", false, "show help")

	if err := fs.Parse(args); err != nil {
		return r.failUsage("expect: invalid flags")
	}
	if *help {
		printExpectHelp(r.Stdout)
		return 0
	}
	if !*jsonOut {
		printExpectHelp(r.Stderr)
		return r.failUsage("expect: require --json for stable output")
	}

	paths := fs.Args()
	if len(paths) != 1 {
		printExpectHelp(r.Stderr)
		return r.failUsage("expect: require exactly one <attemptDir|runDir>")
	}

	res, err := expect.ExpectPath(paths[0], *strict)
	if err != nil {
		fmt.Fprintf(r.Stderr, "ZCL_E_IO: %s\n", err.Error())
		return 1
	}
	exit := r.writeJSON(res)
	if exit != 0 {
		return exit
	}
	if !res.OK {
		return 2
	}
	return 0
}

func (r Runner) runFeedback(args []string) int {
	fs := flag.NewFlagSet("feedback", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	ok := fs.Bool("ok", false, "mark attempt as success")
	fail := fs.Bool("fail", false, "mark attempt as failure")
	result := fs.String("result", "", "result string (bounded/redacted)")
	resultJSON := fs.String("result-json", "", "result json (bounded/canonicalized)")
	classification := fs.String("classification", "", "optional friction classification: missing_primitive|naming_ux|output_shape|already_possible_better_way")
	decisionTagsCSV := fs.String("decision-tags", "", "comma-separated decision tags")
	var decisionTags stringListFlag
	fs.Var(&decisionTags, "decision-tag", "decision tag (repeatable)")
	help := fs.Bool("help", false, "show help")

	if err := fs.Parse(args); err != nil {
		return r.failUsage("feedback: invalid flags")
	}
	if *help {
		printFeedbackHelp(r.Stdout)
		return 0
	}
	if (*ok && *fail) || (!*ok && !*fail) {
		printFeedbackHelp(r.Stderr)
		return r.failUsage("feedback: require exactly one of --ok or --fail")
	}

	env, err := trace.EnvFromProcess()
	if err != nil {
		printFeedbackHelp(r.Stderr)
		return r.failUsage("feedback: missing ZCL attempt context (need ZCL_* env)")
	}

	if strings.TrimSpace(*decisionTagsCSV) != "" {
		for _, s := range strings.Split(*decisionTagsCSV, ",") {
			s = strings.TrimSpace(s)
			if s == "" {
				continue
			}
			decisionTags = append(decisionTags, s)
		}
	}

	if err := feedback.Write(r.Now(), env, feedback.WriteOpts{
		OK:             *ok,
		Result:         *result,
		ResultJSON:     *resultJSON,
		Classification: *classification,
		DecisionTags:   []string(decisionTags),
	}); err != nil {
		fmt.Fprintf(r.Stderr, "ZCL_E_USAGE: %s\n", err.Error())
		return 2
	}

	fmt.Fprintf(r.Stdout, "feedback: OK\n")
	return 0
}

func (r Runner) runNote(args []string) int {
	fs := flag.NewFlagSet("note", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	kind := fs.String("kind", "agent", "note kind: agent|operator|system")
	message := fs.String("message", "", "note message (bounded/redacted)")
	dataJSON := fs.String("data-json", "", "structured note payload as json (bounded/canonicalized)")
	tagsCSV := fs.String("tags", "", "comma-separated tags (optional)")
	help := fs.Bool("help", false, "show help")

	if err := fs.Parse(args); err != nil {
		return r.failUsage("note: invalid flags")
	}
	if *help {
		printNoteHelp(r.Stdout)
		return 0
	}

	env, err := trace.EnvFromProcess()
	if err != nil {
		printNoteHelp(r.Stderr)
		return r.failUsage("note: missing ZCL attempt context (need ZCL_* env)")
	}

	var tags []string
	if *tagsCSV != "" {
		for _, t := range strings.Split(*tagsCSV, ",") {
			if v := strings.TrimSpace(t); v != "" {
				tags = append(tags, v)
			}
		}
	}

	if err := note.Append(r.Now(), env, note.AppendOpts{
		Kind:     *kind,
		Message:  *message,
		DataJSON: *dataJSON,
		Tags:     tags,
	}); err != nil {
		fmt.Fprintf(r.Stderr, "ZCL_E_USAGE: %s\n", err.Error())
		return 2
	}

	fmt.Fprintf(r.Stdout, "note: OK\n")
	return 0
}

func (r Runner) runAttemptStart(args []string) int {
	fs := flag.NewFlagSet("attempt start", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	suite := fs.String("suite", "", "suite id (required)")
	mission := fs.String("mission", "", "mission id (required)")
	prompt := fs.String("prompt", "", "optional mission prompt to snapshot into prompt.txt")
	suiteFile := fs.String("suite-file", "", "optional suite file (.json|.yaml|.yml) to snapshot into suite.json")
	runID := fs.String("run-id", "", "existing run id (optional)")
	agentID := fs.String("agent-id", "", "runner agent id (optional)")
	isolationModel := fs.String("isolation-model", "", "attempt isolation model: process_runner|native_spawn (optional)")
	mode := fs.String("mode", "discovery", "run mode: discovery|ci")
	timeoutMs := fs.Int64("timeout-ms", 0, "attempt timeout in ms (optional; also used by funnels as a mission deadline)")
	timeoutStart := fs.String("timeout-start", "", "timeout anchor: attempt_start|first_tool_call (default: first_tool_call in discovery, attempt_start in ci)")
	blindMode := fs.Bool("blind", false, "enable zero-context prompt contamination checks")
	blindTerms := fs.String("blind-terms", "", "comma-separated contamination terms (default harness terms)")
	outRoot := fs.String("out-root", "", "project output root (default from config/env, else .zcl)")
	retry := fs.Int("retry", 1, "attempt retry number (default 1)")
	envFile := fs.String("env-file", "", "optional path to write attempt env in sh/dotenv format (does not affect JSON output)")
	envFormat := fs.String("env-format", "sh", "env format for --env-file: sh|dotenv")
	printEnv := fs.String("print-env", "", "print env to stderr in given format: sh|dotenv (does not affect JSON output)")
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

	m, err := config.LoadMerged(*outRoot)
	if err != nil {
		fmt.Fprintf(r.Stderr, "ZCL_E_IO: %s\n", err.Error())
		return 1
	}
	var suiteSnap any
	if strings.TrimSpace(*suiteFile) != "" {
		snap, err := planner.ParseSuiteSnapshot(*suiteFile, *suite)
		if err != nil {
			fmt.Fprintf(r.Stderr, "ZCL_E_USAGE: %s\n", err.Error())
			return 2
		}
		suiteSnap = snap
	}

	res, err := attempt.Start(r.Now(), attempt.StartOpts{
		OutRoot:        m.OutRoot,
		RunID:          *runID,
		SuiteID:        *suite,
		MissionID:      *mission,
		AgentID:        *agentID,
		IsolationModel: strings.TrimSpace(*isolationModel),
		Mode:           *mode,
		Retry:          *retry,
		Prompt:         *prompt,
		TimeoutMs:      *timeoutMs,
		TimeoutStart:   strings.TrimSpace(*timeoutStart),
		Blind:          *blindMode,
		BlindTerms:     blind.ParseTermsCSV(*blindTerms),
		SuiteSnapshot:  suiteSnap,
	})
	if err != nil {
		fmt.Fprintf(r.Stderr, "ZCL_E_USAGE: %s\n", err.Error())
		return 2
	}

	var (
		envOut     string
		envOutOK   bool
		envOutUsed bool
	)
	if strings.TrimSpace(*envFile) != "" {
		envOut, envOutOK = formatEnv(res.Env, *envFormat)
		if !envOutOK {
			fmt.Fprintf(r.Stderr, "ZCL_E_USAGE: attempt start: invalid --env-format (expected sh|dotenv)\n")
			return 2
		}
		if err := store.WriteFileAtomic(*envFile, []byte(envOut)); err != nil {
			fmt.Fprintf(r.Stderr, "ZCL_E_IO: %s\n", err.Error())
			return 1
		}
		envOutUsed = true
	}
	if strings.TrimSpace(*printEnv) != "" {
		envOut, envOutOK = formatEnv(res.Env, *printEnv)
		if !envOutOK {
			fmt.Fprintf(r.Stderr, "ZCL_E_USAGE: attempt start: invalid --print-env (expected sh|dotenv)\n")
			return 2
		}
		fmt.Fprint(r.Stderr, envOut)
		envOutUsed = true
	}

	// Wrap JSON output so we can surface env-file metadata without changing attempt.Start.
	type attemptStartJSON struct {
		*attempt.StartResult
		EnvFile   string `json:"envFile,omitempty"`
		EnvFormat string `json:"envFormat,omitempty"`
	}
	out := attemptStartJSON{StartResult: res}
	if envOutUsed && strings.TrimSpace(*envFile) != "" {
		out.EnvFile = *envFile
		out.EnvFormat = *envFormat
	}
	return r.writeJSON(out)
}

func (r Runner) runReport(args []string) int {
	fs := flag.NewFlagSet("report", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	strict := fs.Bool("strict", false, "strict mode (missing required artifacts fails)")
	jsonOut := fs.Bool("json", false, "print JSON output (also writes attempt.report.json)")
	help := fs.Bool("help", false, "show help")

	if err := fs.Parse(args); err != nil {
		return r.failUsage("report: invalid flags")
	}
	if *help {
		printReportHelp(r.Stdout)
		return 0
	}

	paths := fs.Args()
	if len(paths) != 1 {
		printReportHelp(r.Stderr)
		return r.failUsage("report: require exactly one <attemptDir|runDir>")
	}

	target := paths[0]
	targetAbs, err := filepath.Abs(target)
	if err != nil {
		fmt.Fprintf(r.Stderr, "ZCL_E_IO: %s\n", err.Error())
		return 1
	}
	target = targetAbs
	info, err := os.Stat(target)
	if err != nil {
		fmt.Fprintf(r.Stderr, "ZCL_E_IO: %s\n", err.Error())
		return 1
	}
	if !info.IsDir() {
		return r.failUsage("report: target must be a directory")
	}

	*strict = attempt.EffectiveStrict(target, *strict)

	// If target is a run dir, compute for each attempt under attempts/.
	if _, err := os.Stat(filepath.Join(target, "run.json")); err == nil {
		attemptsDir := filepath.Join(target, "attempts")
		entries, err := os.ReadDir(attemptsDir)
		if err != nil {
			fmt.Fprintf(r.Stderr, "ZCL_E_IO: %s\n", err.Error())
			return 1
		}
		var reports []schema.AttemptReportJSONV1
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			attemptDir := filepath.Join(attemptsDir, e.Name())
			rep, err := report.BuildAttemptReport(r.Now(), attemptDir, *strict)
			if err != nil {
				return r.printReportErr(err)
			}
			if err := report.WriteAttemptReportAtomic(filepath.Join(attemptDir, "attempt.report.json"), rep); err != nil {
				fmt.Fprintf(r.Stderr, "ZCL_E_IO: %s\n", err.Error())
				return 1
			}
			reports = append(reports, rep)
		}
		out := buildRunReportJSON(target, reports)
		if err := store.WriteJSONAtomic(filepath.Join(target, "run.report.json"), out); err != nil {
			fmt.Fprintf(r.Stderr, "ZCL_E_IO: %s\n", err.Error())
			return 1
		}
		if *jsonOut {
			return r.writeJSON(out)
		}
		return 0
	}

	rep, err := report.BuildAttemptReport(r.Now(), target, *strict)
	if err != nil {
		return r.printReportErr(err)
	}
	if err := report.WriteAttemptReportAtomic(filepath.Join(target, "attempt.report.json"), rep); err != nil {
		fmt.Fprintf(r.Stderr, "ZCL_E_IO: %s\n", err.Error())
		return 1
	}
	if *jsonOut {
		return r.writeJSON(rep)
	}
	fmt.Fprintf(r.Stdout, "report: OK\n")
	return 0
}

func (r Runner) runValidate(args []string) int {
	fs := flag.NewFlagSet("validate", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	strict := fs.Bool("strict", false, "strict mode (missing required artifacts fails)")
	jsonOut := fs.Bool("json", false, "print JSON output")
	help := fs.Bool("help", false, "show help")

	if err := fs.Parse(args); err != nil {
		return r.failUsage("validate: invalid flags")
	}
	if *help {
		printValidateHelp(r.Stdout)
		return 0
	}

	paths := fs.Args()
	if len(paths) != 1 {
		printValidateHelp(r.Stderr)
		return r.failUsage("validate: require exactly one <attemptDir|runDir>")
	}

	*strict = attempt.EffectiveStrict(paths[0], *strict)

	res, err := validate.ValidatePath(paths[0], *strict)
	if err != nil {
		fmt.Fprintf(r.Stderr, "ZCL_E_IO: %s\n", err.Error())
		return 1
	}
	if *jsonOut {
		exit := r.writeJSON(res)
		if exit != 0 {
			return exit
		}
		if res.OK {
			return 0
		}
		// Distinguish I/O-ish failures vs contract/usage failures for automation.
		for _, f := range res.Errors {
			if f.Code == "ZCL_E_IO" {
				return 1
			}
		}
		return 2
	}
	if res.OK {
		fmt.Fprintf(r.Stdout, "validate: OK\n")
		for _, f := range res.Warnings {
			if f.Path != "" {
				fmt.Fprintf(r.Stderr, "  WARN %s: %s (%s)\n", f.Code, f.Message, f.Path)
			} else {
				fmt.Fprintf(r.Stderr, "  WARN %s: %s\n", f.Code, f.Message)
			}
		}
		return 0
	}
	fmt.Fprintf(r.Stderr, "validate: FAIL\n")
	for _, f := range res.Errors {
		if f.Path != "" {
			fmt.Fprintf(r.Stderr, "  %s: %s (%s)\n", f.Code, f.Message, f.Path)
		} else {
			fmt.Fprintf(r.Stderr, "  %s: %s\n", f.Code, f.Message)
		}
	}
	for _, f := range res.Errors {
		if f.Code == "ZCL_E_IO" {
			return 1
		}
	}
	return 2
}

func (r Runner) runDoctor(args []string) int {
	fs := flag.NewFlagSet("doctor", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	outRoot := fs.String("out-root", "", "project output root (default from config/env, else .zcl)")
	jsonOut := fs.Bool("json", false, "print JSON output")
	help := fs.Bool("help", false, "show help")

	if err := fs.Parse(args); err != nil {
		return r.failUsage("doctor: invalid flags")
	}
	if *help {
		printDoctorHelp(r.Stdout)
		return 0
	}

	res, err := doctor.Run(*outRoot)
	if err != nil {
		fmt.Fprintf(r.Stderr, "ZCL_E_IO: %s\n", err.Error())
		return 1
	}
	if *jsonOut {
		return r.writeJSON(res)
	}
	if res.OK {
		fmt.Fprintf(r.Stdout, "doctor: OK outRoot=%s\n", res.OutRoot)
		return 0
	}
	fmt.Fprintf(r.Stderr, "doctor: FAIL outRoot=%s\n", res.OutRoot)
	for _, c := range res.Checks {
		if !c.OK {
			fmt.Fprintf(r.Stderr, "  FAIL %s: %s\n", c.ID, c.Message)
		}
	}
	return 1
}

func (r Runner) runGC(args []string) int {
	fs := flag.NewFlagSet("gc", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	outRoot := fs.String("out-root", "", "project output root (default from config/env, else .zcl)")
	maxAgeDays := fs.Int("max-age-days", 30, "delete runs older than N days (unpinned only); 0 disables")
	maxTotalBytes := fs.Int64("max-total-bytes", 0, "delete oldest runs until total size is under this threshold (unpinned only); 0 disables")
	dryRun := fs.Bool("dry-run", false, "print what would be deleted without deleting")
	jsonOut := fs.Bool("json", false, "print JSON output")
	help := fs.Bool("help", false, "show help")

	if err := fs.Parse(args); err != nil {
		return r.failUsage("gc: invalid flags")
	}
	if *help {
		printGCHelp(r.Stdout)
		return 0
	}

	m, err := config.LoadMerged(*outRoot)
	if err != nil {
		fmt.Fprintf(r.Stderr, "ZCL_E_IO: %s\n", err.Error())
		return 1
	}

	res, err := gc.Run(gc.Opts{
		OutRoot:       m.OutRoot,
		Now:           r.Now(),
		MaxAgeDays:    *maxAgeDays,
		MaxTotalBytes: *maxTotalBytes,
		DryRun:        *dryRun,
	})
	if err != nil {
		fmt.Fprintf(r.Stderr, "ZCL_E_IO: %s\n", err.Error())
		return 1
	}
	if *jsonOut {
		return r.writeJSON(res)
	}
	fmt.Fprintf(r.Stdout, "gc: OK deleted=%d kept=%d dryRun=%v\n", len(res.Deleted), len(res.Kept), res.DryRun)
	return 0
}

func (r Runner) runEnrich(args []string) int {
	fs := flag.NewFlagSet("enrich", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	runner := fs.String("runner", "", "runner kind (required): codex")
	rollout := fs.String("rollout", "", "path to runner rollout jsonl (required for codex)")
	help := fs.Bool("help", false, "show help")

	if err := fs.Parse(args); err != nil {
		return r.failUsage("enrich: invalid flags")
	}
	if *help {
		printEnrichHelp(r.Stdout)
		return 0
	}
	if strings.TrimSpace(*runner) == "" {
		printEnrichHelp(r.Stderr)
		return r.failUsage("enrich: missing --runner")
	}

	target := ""
	if rest := fs.Args(); len(rest) == 1 {
		target = rest[0]
	} else if len(rest) == 0 {
		if env, err := trace.EnvFromProcess(); err == nil {
			target = env.OutDirAbs
		}
	} else {
		printEnrichHelp(r.Stderr)
		return r.failUsage("enrich: require at most one <attemptDir>")
	}
	if target == "" {
		printEnrichHelp(r.Stderr)
		return r.failUsage("enrich: missing <attemptDir> (or set ZCL_OUT_DIR)")
	}

	switch *runner {
	case "codex":
		if err := enrich.EnrichCodexAttempt(target, *rollout); err != nil {
			var ce *enrich.CliError
			if errors.As(err, &ce) {
				fmt.Fprintf(r.Stderr, "%s: %s\n", ce.Code, ce.Message)
				return 2
			}
			fmt.Fprintf(r.Stderr, "ZCL_E_IO: %s\n", err.Error())
			return 1
		}
	default:
		return r.failUsage("enrich: unsupported --runner (expected codex)")
	}

	fmt.Fprintf(r.Stdout, "enrich: OK\n")
	return 0
}

func (r Runner) runMCP(args []string) int {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" || args[0] == "help" {
		printMCPHelp(r.Stdout)
		return 0
	}
	switch args[0] {
	case "proxy":
		return r.runMCPProxy(args[1:])
	default:
		fmt.Fprintf(r.Stderr, "ZCL_E_USAGE: unknown mcp subcommand %q\n", args[0])
		printMCPHelp(r.Stderr)
		return 2
	}
}

func (r Runner) runMCPProxy(args []string) int {
	fs := flag.NewFlagSet("mcp proxy", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	help := fs.Bool("help", false, "show help")
	if err := fs.Parse(args); err != nil {
		return r.failUsage("mcp proxy: invalid flags")
	}
	if *help {
		printMCPProxyHelp(r.Stdout)
		return 0
	}

	env, err := trace.EnvFromProcess()
	if err != nil {
		printMCPProxyHelp(r.Stderr)
		return r.failUsage("mcp proxy: missing ZCL attempt context (need ZCL_* env)")
	}
	if a, err := attempt.ReadAttempt(env.OutDirAbs); err != nil {
		printMCPProxyHelp(r.Stderr)
		return r.failUsage("mcp proxy: missing/invalid attempt.json in ZCL_OUT_DIR (need zcl attempt start context)")
	} else if a.RunID != env.RunID || a.SuiteID != env.SuiteID || a.MissionID != env.MissionID || a.AttemptID != env.AttemptID {
		printMCPProxyHelp(r.Stderr)
		return r.failUsage("mcp proxy: attempt.json ids do not match ZCL_* env (refuse to run)")
	}

	argv := fs.Args()
	if len(argv) >= 1 && argv[0] == "--" {
		argv = argv[1:]
	}
	if len(argv) == 0 {
		printMCPProxyHelp(r.Stderr)
		return r.failUsage("mcp proxy: missing server command (use: zcl mcp proxy -- <server-cmd> ...)")
	}

	now := r.Now()
	if _, err := attempt.EnsureTimeoutAnchor(now, env.OutDirAbs); err != nil {
		fmt.Fprintf(r.Stderr, "ZCL_E_IO: %s\n", err.Error())
		return 1
	}
	ctx, cancel, timedOut := attemptCtxForDeadline(now, env.OutDirAbs)
	if cancel != nil {
		defer cancel()
	}
	if timedOut {
		fmt.Fprintf(r.Stderr, "ZCL_E_TIMEOUT: attempt deadline exceeded\n")
		return 1
	}
	if err := mcpproxy.Proxy(ctx, env, argv, os.Stdin, r.Stdout, schema.PreviewMaxBytesV1); err != nil {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
			fmt.Fprintf(r.Stderr, "ZCL_E_TIMEOUT: attempt deadline exceeded\n")
			return 1
		}
		fmt.Fprintf(r.Stderr, "ZCL_E_IO: %s\n", err.Error())
		return 1
	}
	return 0
}

func (r Runner) printReportErr(err error) int {
	var ce *report.CliError
	if errors.As(err, &ce) {
		fmt.Fprintf(r.Stderr, "%s: %s\n", ce.Code, ce.Message)
		// Strict/validation-like errors should be non-zero and typed.
		return 2
	}
	fmt.Fprintf(r.Stderr, "ZCL_E_IO: %s\n", err.Error())
	return 1
}

type runReportAggregateJSON struct {
	AttemptsTotal int `json:"attemptsTotal"`
	Passed        int `json:"passed"`
	Failed        int `json:"failed"`

	FailureCodeHistogram             map[string]int64         `json:"failureCodeHistogram,omitempty"`
	TimedOutBeforeFirstToolCallTotal int64                    `json:"timedOutBeforeFirstToolCallTotal,omitempty"`
	Task                             runTaskAxisJSON          `json:"task"`
	Evidence                         runEvidenceAxisJSON      `json:"evidence"`
	Orchestration                    runOrchestrationAxisJSON `json:"orchestration"`

	TokenEstimates *schema.TokenEstimatesV1 `json:"tokenEstimates,omitempty"`
}

type runTaskAxisJSON struct {
	Passed  int `json:"passed"`
	Failed  int `json:"failed"`
	Unknown int `json:"unknown"`
}

type runEvidenceAxisJSON struct {
	Complete   int `json:"complete"`
	Incomplete int `json:"incomplete"`
}

type runOrchestrationAxisJSON struct {
	Healthy            int              `json:"healthy"`
	InfraFailed        int              `json:"infraFailed"`
	InfraFailureByCode map[string]int64 `json:"infraFailureByCode,omitempty"`
}

type runReportJSON struct {
	SchemaVersion int    `json:"schemaVersion"`
	OK            bool   `json:"ok"`
	Target        string `json:"target"`
	RunID         string `json:"runId,omitempty"`
	SuiteID       string `json:"suiteId,omitempty"`
	Path          string `json:"path"`

	Attempts  []schema.AttemptReportJSONV1 `json:"attempts"`
	Aggregate runReportAggregateJSON       `json:"aggregate"`
}

func buildRunReportJSON(runDir string, reports []schema.AttemptReportJSONV1) runReportJSON {
	out := runReportJSON{
		SchemaVersion: 1,
		OK:            true,
		Target:        "run",
		Path:          runDir,
		Attempts:      reports,
		Aggregate: runReportAggregateJSON{
			AttemptsTotal:        len(reports),
			FailureCodeHistogram: map[string]int64{},
			Orchestration: runOrchestrationAxisJSON{
				InfraFailureByCode: map[string]int64{},
			},
			TokenEstimates: &schema.TokenEstimatesV1{Source: "attempt.report"},
		},
	}
	if len(reports) > 0 {
		out.RunID = reports[0].RunID
		out.SuiteID = reports[0].SuiteID
	}

	var (
		inputTotal           int64
		outputTotal          int64
		totalTotal           int64
		cachedInputTotal     int64
		reasoningOutputTotal int64
		hasInput             bool
		hasOutput            bool
		hasTotal             bool
		hasCached            bool
		hasReasoning         bool
	)

	for _, rep := range reports {
		if rep.OK != nil && *rep.OK {
			out.Aggregate.Passed++
			out.Aggregate.Task.Passed++
		} else {
			out.Aggregate.Failed++
			out.OK = false
			if rep.OK == nil {
				out.Aggregate.Task.Unknown++
			} else {
				out.Aggregate.Task.Failed++
			}
		}
		if reportEvidenceComplete(rep) {
			out.Aggregate.Evidence.Complete++
		} else {
			out.Aggregate.Evidence.Incomplete++
		}
		if rep.TimedOutBeforeFirstToolCall {
			out.Aggregate.TimedOutBeforeFirstToolCallTotal++
		}
		for code, n := range rep.FailureCodeHistogram {
			out.Aggregate.FailureCodeHistogram[code] += n
			if isOrchestrationInfraCode(code) {
				out.Aggregate.Orchestration.InfraFailureByCode[code] += n
			}
		}
		if attemptHasInfraFailure(rep) {
			out.Aggregate.Orchestration.InfraFailed++
		} else {
			out.Aggregate.Orchestration.Healthy++
		}
		if rep.TokenEstimates != nil {
			if rep.TokenEstimates.InputTokens != nil {
				inputTotal += *rep.TokenEstimates.InputTokens
				hasInput = true
			}
			if rep.TokenEstimates.OutputTokens != nil {
				outputTotal += *rep.TokenEstimates.OutputTokens
				hasOutput = true
			}
			if rep.TokenEstimates.TotalTokens != nil {
				totalTotal += *rep.TokenEstimates.TotalTokens
				hasTotal = true
			}
			if rep.TokenEstimates.CachedInputTokens != nil {
				cachedInputTotal += *rep.TokenEstimates.CachedInputTokens
				hasCached = true
			}
			if rep.TokenEstimates.ReasoningOutputTokens != nil {
				reasoningOutputTotal += *rep.TokenEstimates.ReasoningOutputTokens
				hasReasoning = true
			}
		}
	}

	if len(out.Aggregate.FailureCodeHistogram) == 0 {
		out.Aggregate.FailureCodeHistogram = nil
	}
	if len(out.Aggregate.Orchestration.InfraFailureByCode) == 0 {
		out.Aggregate.Orchestration.InfraFailureByCode = nil
	}
	if hasInput {
		out.Aggregate.TokenEstimates.InputTokens = i64ptr(inputTotal)
	}
	if hasOutput {
		out.Aggregate.TokenEstimates.OutputTokens = i64ptr(outputTotal)
	}
	if hasTotal {
		out.Aggregate.TokenEstimates.TotalTokens = i64ptr(totalTotal)
	} else if hasInput || hasOutput {
		out.Aggregate.TokenEstimates.TotalTokens = i64ptr(inputTotal + outputTotal)
	}
	if hasCached {
		out.Aggregate.TokenEstimates.CachedInputTokens = i64ptr(cachedInputTotal)
	}
	if hasReasoning {
		out.Aggregate.TokenEstimates.ReasoningOutputTokens = i64ptr(reasoningOutputTotal)
	}
	if out.Aggregate.TokenEstimates.TotalTokens == nil &&
		out.Aggregate.TokenEstimates.InputTokens == nil &&
		out.Aggregate.TokenEstimates.OutputTokens == nil &&
		out.Aggregate.TokenEstimates.CachedInputTokens == nil &&
		out.Aggregate.TokenEstimates.ReasoningOutputTokens == nil {
		out.Aggregate.TokenEstimates = nil
	}
	return out
}

func reportEvidenceComplete(rep schema.AttemptReportJSONV1) bool {
	if rep.Integrity == nil {
		return false
	}
	return rep.Integrity.TraceNonEmpty && rep.Integrity.FeedbackPresent
}

func isOrchestrationInfraCode(code string) bool {
	switch strings.TrimSpace(code) {
	case "ZCL_E_TIMEOUT", "ZCL_E_SPAWN", "ZCL_E_IO":
		return true
	default:
		return false
	}
}

func attemptHasInfraFailure(rep schema.AttemptReportJSONV1) bool {
	for code := range rep.FailureCodeHistogram {
		if isOrchestrationInfraCode(code) {
			return true
		}
	}
	return false
}

func i64ptr(v int64) *int64 {
	x := v
	return &x
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
  zcl attempt finish [--strict] [--json] [<attemptDir>]
  zcl attempt explain [--json] [--tail N] [<attemptDir>]
  zcl suite plan --file <suite.(yaml|yml|json)> --json
  zcl suite run --file <suite.(yaml|yml|json)> [--session-isolation auto|process|native] --json -- <runner-cmd> [args...]
  zcl feedback --ok|--fail --result <string>|--result-json <json>
  zcl note [--kind agent|operator|system] --message <string>|--data-json <json>
  zcl report [--strict] [--json] <attemptDir|runDir>
  zcl validate [--strict] [--json] <attemptDir|runDir>
  zcl replay --json <attemptDir>
  zcl expect [--strict] --json <attemptDir|runDir>
  zcl doctor [--json]
  zcl gc [--dry-run] [--json]
  zcl pin --run-id <runId> --on|--off [--json]
  zcl enrich --runner codex --rollout <path> [<attemptDir>]
  zcl mcp proxy -- <server-cmd> [args...]
  zcl http proxy --upstream <url> [--listen 127.0.0.1:0] [--max-requests N] [--json]
  zcl run -- <cmd> [args...]

Commands:
  init            Initialize the project (.zcl output root + zcl.config.json).
  contract        Print the ZCL surface contract (use --json).
  attempt start   Allocate a run/attempt dir and print canonical IDs + env (use --json).
  attempt finish  Write attempt.report.json, then validate + expect (use --json for automation).
  attempt explain Fast post-mortem view from artifacts (tail trace + pointers).
  suite plan      Allocate attempt dirs for every mission in a suite file (use --json).
  suite run       Run a suite end-to-end with capability-aware isolation selection.
  feedback        Write the canonical attempt outcome to feedback.json.
  note            Append a secondary evidence note to notes.jsonl.
  report           Compute attempt.report.json from tool.calls.jsonl + feedback.json.
  validate         Validate artifact integrity with typed error codes.
  replay           Best-effort replay of tool.calls.jsonl (use --json).
  expect           Evaluate suite expectations against feedback.json (use --json).
  doctor           Check environment/config sanity for running ZCL.
  gc               Retention cleanup under .zcl/runs (supports pinning).
  pin              Pin/unpin a run so gc will keep it.
  enrich           Optional runner enrichment (does not affect scoring).
  mcp proxy        MCP stdio proxy funnel (records initialize/tools/list/tools/call).
  http proxy       HTTP reverse proxy funnel (records method/url/status/latency/bytes).
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
  zcl attempt finish [--strict] [--json] [<attemptDir>]
  zcl attempt explain [--json] [--tail N] [<attemptDir>]
`)
}

func printAttemptStartHelp(w io.Writer) {
	fmt.Fprint(w, `Usage:
	  zcl attempt start --suite <suiteId> --mission <missionId> [--prompt <text>] [--suite-file <path>] [--run-id <runId>] [--agent-id <id>] [--isolation-model process_runner|native_spawn] [--mode discovery|ci] [--timeout-ms N] [--timeout-start attempt_start|first_tool_call] [--blind] [--blind-terms a,b,c] [--out-root .zcl] [--retry 1] [--env-file <path>] [--env-format sh|dotenv] [--print-env sh|dotenv] --json
		`)
}

func printSuiteHelp(w io.Writer) {
	fmt.Fprint(w, `Usage:
  zcl suite plan --file <suite.(yaml|yml|json)> --json
  zcl suite run --file <suite.(yaml|yml|json)> [--session-isolation auto|process|native] --json -- <runner-cmd> [args...]
`)
}

func printSuitePlanHelp(w io.Writer) {
	fmt.Fprint(w, `Usage:
	  zcl suite plan --file <suite.(yaml|yml|json)> [--run-id <runId>] [--mode discovery|ci] [--timeout-ms N] [--timeout-start attempt_start|first_tool_call] [--blind on|off] [--blind-terms a,b,c] [--out-root .zcl] --json
`)
}

func printReplayHelp(w io.Writer) {
	fmt.Fprint(w, `Usage:
  zcl replay [--execute] [--allow <cmd1,cmd2>] [--allow-all] [--max-steps N] [--stdin] --json <attemptDir>
  
Notes:
  - Default is dry-run; use --execute to actually run replayable steps.
  - When executing, commands are denied unless explicitly allowed (or --allow-all).
`)
}

func printExpectHelp(w io.Writer) {
	fmt.Fprint(w, `Usage:
  zcl expect [--strict] --json <attemptDir|runDir>
`)
}

func printFeedbackHelp(w io.Writer) {
	fmt.Fprint(w, `Usage:
  zcl feedback --ok|--fail --result <string>
  zcl feedback --ok|--fail --result-json <json>
  zcl feedback --ok|--fail --result <string> --classification <missing_primitive|naming_ux|output_shape|already_possible_better_way>
  zcl feedback --ok|--fail --result <string> --decision-tag blocked --decision-tag timeout
  zcl feedback --ok|--fail --result <string> --decision-tags blocked,timeout
`)
}

func printNoteHelp(w io.Writer) {
	fmt.Fprint(w, `Usage:
  zcl note [--kind agent|operator|system] --message <string> [--tags a,b,c]
  zcl note [--kind agent|operator|system] --data-json <json> [--tags a,b,c]
`)
}

func printReportHelp(w io.Writer) {
	fmt.Fprint(w, `Usage:
  zcl report [--strict] [--json] <attemptDir|runDir>

Notes:
  - Always writes attempt.report.json for attempts under the target.
  - When target is a runDir, also writes run.report.json (same shape as --json output).
`)
}

func printValidateHelp(w io.Writer) {
	fmt.Fprint(w, `Usage:
  zcl validate [--strict] [--json] <attemptDir|runDir>
`)
}

func printDoctorHelp(w io.Writer) {
	fmt.Fprint(w, `Usage:
  zcl doctor [--out-root .zcl] [--json]
`)
}

func printGCHelp(w io.Writer) {
	fmt.Fprint(w, `Usage:
  zcl gc [--out-root .zcl] [--max-age-days 30] [--max-total-bytes 0] [--dry-run] [--json]
`)
}

func printEnrichHelp(w io.Writer) {
	fmt.Fprint(w, `Usage:
  zcl enrich --runner codex --rollout <rollout.jsonl> [<attemptDir>]
`)
}

func printMCPHelp(w io.Writer) {
	fmt.Fprint(w, `Usage:
  zcl mcp proxy -- <server-cmd> [args...]
`)
}

func printMCPProxyHelp(w io.Writer) {
	fmt.Fprint(w, `Usage:
  zcl mcp proxy -- <server-cmd> [args...]
`)
}
