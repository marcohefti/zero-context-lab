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
	"strconv"
	"strings"
	"time"

	"github.com/marcohefti/zero-context-lab/internal/contexts/evaluation/app/expect"
	"github.com/marcohefti/zero-context-lab/internal/contexts/evaluation/app/report"
	"github.com/marcohefti/zero-context-lab/internal/contexts/evaluation/app/semantic"
	"github.com/marcohefti/zero-context-lab/internal/contexts/evaluation/app/validate"
	"github.com/marcohefti/zero-context-lab/internal/contexts/evidence/app/feedback"
	"github.com/marcohefti/zero-context-lab/internal/contexts/evidence/app/mcp_proxy"
	"github.com/marcohefti/zero-context-lab/internal/contexts/evidence/app/note"
	"github.com/marcohefti/zero-context-lab/internal/contexts/evidence/app/replay"
	"github.com/marcohefti/zero-context-lab/internal/contexts/evidence/app/trace"
	"github.com/marcohefti/zero-context-lab/internal/contexts/execution/app/attempt"
	"github.com/marcohefti/zero-context-lab/internal/contexts/execution/app/planner"
	"github.com/marcohefti/zero-context-lab/internal/contexts/ops/app/doctor"
	"github.com/marcohefti/zero-context-lab/internal/contexts/ops/app/gc"
	"github.com/marcohefti/zero-context-lab/internal/contexts/runtime/app/enrich"
	"github.com/marcohefti/zero-context-lab/internal/contexts/runtime/infra/codex_app_server"
	"github.com/marcohefti/zero-context-lab/internal/contexts/runtime/ports/native"
	"github.com/marcohefti/zero-context-lab/internal/interfaces/contract"
	"github.com/marcohefti/zero-context-lab/internal/kernel/blind"
	"github.com/marcohefti/zero-context-lab/internal/kernel/config"
	"github.com/marcohefti/zero-context-lab/internal/kernel/runnerid"
	"github.com/marcohefti/zero-context-lab/internal/kernel/schema"
	"github.com/marcohefti/zero-context-lab/internal/kernel/store"
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
	r = r.withDefaults()
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" || args[0] == "help" {
		printRootHelp(r.Stdout)
		return 0
	}
	if args[0] == "--version" || args[0] == "-v" {
		fmt.Fprintf(r.Stdout, "%s\n", r.Version)
		return 0
	}
	if exit, stop := r.enforceVersionFloor(args); stop {
		return exit
	}
	r.maybePrintUpdateNotice(args)
	return r.runRootCommand(args[0], args[1:])
}

func (r Runner) withDefaults() Runner {
	if r.Stdout == nil {
		r.Stdout = os.Stdout
	}
	if r.Stderr == nil {
		r.Stderr = os.Stderr
	}
	if r.Now == nil {
		r.Now = time.Now
	}
	return r
}

func (r Runner) runRootCommand(command string, args []string) int {
	handlers := map[string]func([]string) int{
		"contract": r.runContract,
		"init":     r.runInit,
		"update":   r.runUpdate,
		"feedback": r.runFeedback,
		"note":     r.runNote,
		"report":   r.runReport,
		"validate": r.runValidate,
		"doctor":   r.runDoctor,
		"gc":       r.runGC,
		"pin":      r.runPin,
		"enrich":   r.runEnrich,
		"mcp":      r.runMCP,
		"http":     r.runHTTP,
		"run":      r.runRun,
		"attempt":  r.runAttempt,
		"suite":    r.runSuite,
		"campaign": r.runCampaign,
		"mission":  r.runMission,
		"runs":     r.runRuns,
		"replay":   r.runReplay,
		"expect":   r.runExpect,
	}
	if handler, ok := handlers[command]; ok {
		return handler(args)
	}
	if command == "version" {
		fmt.Fprintf(r.Stdout, "%s\n", r.Version)
		return 0
	}
	fmt.Fprintf(r.Stderr, codeUsage+": unknown command %q\n", command)
	printRootHelp(r.Stderr)
	return 2
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
		fmt.Fprintf(r.Stderr, codeIO+": %s\n", err.Error())
		return 1
	}
	res, err := config.InitProject(*configPath, m.OutRoot)
	if err != nil {
		fmt.Fprintf(r.Stderr, codeIO+": %s\n", err.Error())
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
	case "env":
		return r.runAttemptEnv(args[1:])
	case "finish":
		return r.runAttemptFinish(args[1:])
	case "explain":
		return r.runAttemptExplain(args[1:])
	case "list":
		return r.runAttemptList(args[1:])
	case "latest":
		return r.runAttemptLatest(args[1:])
	default:
		fmt.Fprintf(r.Stderr, codeUsage+": unknown attempt subcommand %q\n", args[0])
		printAttemptHelp(r.Stderr)
		return 2
	}
}

func (r Runner) runRuns(args []string) int {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" || args[0] == "help" {
		printRunsHelp(r.Stdout)
		return 0
	}
	switch args[0] {
	case "list":
		return r.runRunsList(args[1:])
	default:
		fmt.Fprintf(r.Stderr, codeUsage+": unknown runs subcommand %q\n", args[0])
		printRunsHelp(r.Stderr)
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
		fmt.Fprintf(r.Stderr, codeUsage+": unknown suite subcommand %q\n", args[0])
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
		fmt.Fprintf(r.Stderr, codeIO+": %s\n", err.Error())
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
			fmt.Fprintf(r.Stderr, codeUsage+": suite plan: invalid --blind (expected on|off)\n")
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
		fmt.Fprintf(r.Stderr, codeUsage+": %s\n", err.Error())
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
		fmt.Fprintf(r.Stderr, codeIO+": %s\n", err.Error())
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
		fmt.Fprintf(r.Stderr, codeIO+": %s\n", err.Error())
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
	env, hasEnv := loadFeedbackAttemptEnv()
	if !hasEnv {
		printFeedbackHelp(r.Stderr)
		return r.failUsage("feedback: missing ZCL attempt context (need ZCL_* env)")
	}
	decisionTags = append(decisionTags, parseDecisionTagsCSV(*decisionTagsCSV)...)
	if err := feedback.Write(r.Now(), env, feedback.WriteOpts{
		OK:             *ok,
		Result:         *result,
		ResultJSON:     *resultJSON,
		Classification: *classification,
		DecisionTags:   []string(decisionTags),
	}); err != nil {
		msg := err.Error()
		fmt.Fprintf(r.Stderr, codeUsage+": %s\n", msg)
		if hint := feedbackHint(msg); hint != "" {
			fmt.Fprintf(r.Stderr, "hint: %s\n", hint)
		}
		return 2
	}
	fmt.Fprintf(r.Stdout, "feedback: OK\n")
	return 0
}

func loadFeedbackAttemptEnv() (trace.Env, bool) {
	env, err := trace.EnvFromProcess()
	if err != nil {
		return trace.Env{}, false
	}
	return env, true
}

func parseDecisionTagsCSV(csv string) []string {
	if strings.TrimSpace(csv) == "" {
		return nil
	}
	out := make([]string, 0, 4)
	for _, s := range strings.Split(csv, ",") {
		if tag := strings.TrimSpace(s); tag != "" {
			out = append(out, tag)
		}
	}
	return out
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
		fmt.Fprintf(r.Stderr, codeUsage+": %s\n", err.Error())
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
		fmt.Fprintf(r.Stderr, codeIO+": %s\n", err.Error())
		return 1
	}
	var suiteSnap any
	if strings.TrimSpace(*suiteFile) != "" {
		snap, err := planner.ParseSuiteSnapshot(*suiteFile, *suite)
		if err != nil {
			fmt.Fprintf(r.Stderr, codeUsage+": %s\n", err.Error())
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
		fmt.Fprintf(r.Stderr, codeUsage+": %s\n", err.Error())
		return 2
	}

	envOutUsed, outFile, outFormat, exit := r.emitAttemptStartEnv(res.Env, strings.TrimSpace(*envFile), strings.TrimSpace(*envFormat), strings.TrimSpace(*printEnv))
	if exit != 0 {
		return exit
	}

	// Wrap JSON output so we can surface env-file metadata without changing attempt.Start.
	type attemptStartJSON struct {
		*attempt.StartResult
		EnvFile   string `json:"envFile,omitempty"`
		EnvFormat string `json:"envFormat,omitempty"`
	}
	out := attemptStartJSON{StartResult: res}
	if envOutUsed && outFile != "" {
		out.EnvFile = outFile
		out.EnvFormat = outFormat
	}
	return r.writeJSON(out)
}

func (r Runner) emitAttemptStartEnv(env map[string]string, envFile, envFormat, printEnv string) (bool, string, string, int) {
	envOutUsed := false
	if envFile != "" {
		envOut, ok := formatEnv(env, envFormat)
		if !ok {
			fmt.Fprintf(r.Stderr, codeUsage+": attempt start: invalid --env-format (expected sh|dotenv)\n")
			return false, "", "", 2
		}
		if err := store.WriteFileAtomic(envFile, []byte(envOut)); err != nil {
			fmt.Fprintf(r.Stderr, codeIO+": %s\n", err.Error())
			return false, "", "", 1
		}
		envOutUsed = true
	}
	if printEnv != "" {
		envOut, ok := formatEnv(env, printEnv)
		if !ok {
			fmt.Fprintf(r.Stderr, codeUsage+": attempt start: invalid --print-env (expected sh|dotenv)\n")
			return false, "", "", 2
		}
		fmt.Fprint(r.Stderr, envOut)
		envOutUsed = true
	}
	return envOutUsed, envFile, envFormat, 0
}

func (r Runner) runReport(args []string) int {
	opts, exit, ok := r.parseReportArgs(args)
	if !ok {
		return exit
	}
	target, strict, exit, ok := r.resolveReportTarget(opts.target, opts.strict)
	if !ok {
		return exit
	}
	if isRunReportTarget(target) {
		return r.runReportForRun(target, strict, opts.jsonOut)
	}
	return r.runReportForAttempt(target, strict, opts.jsonOut)
}

type reportArgs struct {
	target  string
	strict  bool
	jsonOut bool
}

func (r Runner) parseReportArgs(args []string) (reportArgs, int, bool) {
	fs := flag.NewFlagSet("report", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	strict := fs.Bool("strict", false, "strict mode (missing required artifacts fails)")
	jsonOut := fs.Bool("json", false, "print JSON output (also writes attempt.report.json)")
	help := fs.Bool("help", false, "show help")
	if err := fs.Parse(args); err != nil {
		return reportArgs{}, r.failUsage("report: invalid flags"), false
	}
	if *help {
		printReportHelp(r.Stdout)
		return reportArgs{}, 0, false
	}
	paths := fs.Args()
	if len(paths) != 1 {
		printReportHelp(r.Stderr)
		return reportArgs{}, r.failUsage("report: require exactly one <attemptDir|runDir>"), false
	}
	return reportArgs{target: paths[0], strict: *strict, jsonOut: *jsonOut}, 0, true
}

func (r Runner) resolveReportTarget(target string, strict bool) (string, bool, int, bool) {
	targetAbs, err := filepath.Abs(target)
	if err != nil {
		fmt.Fprintf(r.Stderr, codeIO+": %s\n", err.Error())
		return "", false, 1, false
	}
	info, err := os.Stat(targetAbs)
	if err != nil {
		fmt.Fprintf(r.Stderr, codeIO+": %s\n", err.Error())
		return "", false, 1, false
	}
	if !info.IsDir() {
		return "", false, r.failUsage("report: target must be a directory"), false
	}
	return targetAbs, attempt.EffectiveStrict(targetAbs, strict), 0, true
}

func isRunReportTarget(target string) bool {
	_, err := os.Stat(filepath.Join(target, "run.json"))
	return err == nil
}

func (r Runner) runReportForRun(target string, strict bool, jsonOut bool) int {
	reports, exit, ok := r.buildRunAttemptReports(target, strict)
	if !ok {
		return exit
	}
	out := buildRunReportJSON(target, reports)
	if err := store.WriteJSONAtomic(filepath.Join(target, "run.report.json"), out); err != nil {
		fmt.Fprintf(r.Stderr, codeIO+": %s\n", err.Error())
		return 1
	}
	if jsonOut {
		return r.writeJSON(out)
	}
	return 0
}

func (r Runner) buildRunAttemptReports(target string, strict bool) ([]schema.AttemptReportJSONV1, int, bool) {
	attemptsDir := filepath.Join(target, "attempts")
	entries, err := os.ReadDir(attemptsDir)
	if err != nil {
		fmt.Fprintf(r.Stderr, codeIO+": %s\n", err.Error())
		return nil, 1, false
	}
	reports := make([]schema.AttemptReportJSONV1, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		attemptDir := filepath.Join(attemptsDir, e.Name())
		rep, err := report.BuildAttemptReport(r.Now(), attemptDir, strict)
		if err != nil {
			return nil, r.printReportErr(err), false
		}
		if err := report.WriteAttemptReportAtomic(filepath.Join(attemptDir, "attempt.report.json"), rep); err != nil {
			fmt.Fprintf(r.Stderr, codeIO+": %s\n", err.Error())
			return nil, 1, false
		}
		reports = append(reports, rep)
	}
	return reports, 0, true
}

func (r Runner) runReportForAttempt(target string, strict bool, jsonOut bool) int {
	rep, err := report.BuildAttemptReport(r.Now(), target, strict)
	if err != nil {
		return r.printReportErr(err)
	}
	if err := report.WriteAttemptReportAtomic(filepath.Join(target, "attempt.report.json"), rep); err != nil {
		fmt.Fprintf(r.Stderr, codeIO+": %s\n", err.Error())
		return 1
	}
	if jsonOut {
		return r.writeJSON(rep)
	}
	fmt.Fprintf(r.Stdout, "report: OK\n")
	return 0
}

func (r Runner) runValidate(args []string) int {
	opts, exit, ok := r.parseValidateArgs(args)
	if !ok {
		return exit
	}
	opts.strict = attempt.EffectiveStrict(opts.path, opts.strict)
	if opts.semanticMode {
		return r.runSemanticValidate(opts.path, opts.semanticRules, opts.jsonOut)
	}
	return r.runStandardValidate(opts.path, opts.strict, opts.jsonOut)
}

type validateArgs struct {
	path          string
	strict        bool
	semanticMode  bool
	semanticRules string
	jsonOut       bool
}

func (r Runner) parseValidateArgs(args []string) (validateArgs, int, bool) {
	fs := flag.NewFlagSet("validate", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	strict := fs.Bool("strict", false, "strict mode (missing required artifacts fails)")
	semanticMode := fs.Bool("semantic", false, "run semantic validation gates (feedback semantics + trace signals)")
	semanticRules := fs.String("semantic-rules", "", "optional semantic rules file (.json|.yaml|.yml)")
	jsonOut := fs.Bool("json", false, "print JSON output")
	help := fs.Bool("help", false, "show help")
	if err := fs.Parse(args); err != nil {
		return validateArgs{}, r.failUsage("validate: invalid flags"), false
	}
	if *help {
		printValidateHelp(r.Stdout)
		return validateArgs{}, 0, false
	}
	paths := fs.Args()
	if len(paths) != 1 {
		printValidateHelp(r.Stderr)
		return validateArgs{}, r.failUsage("validate: require exactly one <attemptDir|runDir>"), false
	}
	return validateArgs{
		path:          paths[0],
		strict:        *strict,
		semanticMode:  *semanticMode,
		semanticRules: strings.TrimSpace(*semanticRules),
		jsonOut:       *jsonOut,
	}, 0, true
}

func (r Runner) runSemanticValidate(path string, rulesPath string, jsonOut bool) int {
	res, err := semantic.ValidatePath(path, semantic.Options{RulesPath: rulesPath})
	if err != nil {
		fmt.Fprintf(r.Stderr, codeIO+": %s\n", err.Error())
		return 1
	}
	if jsonOut {
		exit := r.writeJSON(res)
		if exit != 0 {
			return exit
		}
		if res.OK {
			return 0
		}
		return 2
	}
	if res.OK {
		fmt.Fprintf(r.Stdout, "validate: OK\n")
		return 0
	}
	fmt.Fprintf(r.Stderr, "validate: FAIL\n")
	for _, f := range res.Failures {
		if f.Path != "" {
			fmt.Fprintf(r.Stderr, "  %s: %s (%s)\n", f.Code, f.Message, f.Path)
		} else {
			fmt.Fprintf(r.Stderr, "  %s: %s\n", f.Code, f.Message)
		}
	}
	return 2
}

func (r Runner) runStandardValidate(path string, strict bool, jsonOut bool) int {
	res, err := validate.ValidatePath(path, strict)
	if err != nil {
		fmt.Fprintf(r.Stderr, codeIO+": %s\n", err.Error())
		return 1
	}
	if jsonOut {
		exit := r.writeJSON(res)
		if exit != 0 {
			return exit
		}
		if res.OK {
			return 0
		}
		if validateHasIOError(res.Errors) {
			return 1
		}
		return 2
	}
	if res.OK {
		fmt.Fprintf(r.Stdout, "validate: OK\n")
		printValidateFindings(r.Stderr, "WARN", res.Warnings)
		return 0
	}
	fmt.Fprintf(r.Stderr, "validate: FAIL\n")
	printValidateFindings(r.Stderr, "", res.Errors)
	if validateHasIOError(res.Errors) {
		return 1
	}
	return 2
}

func printValidateFindings(w io.Writer, prefix string, findings []validate.Finding) {
	for _, f := range findings {
		if prefix != "" {
			if f.Path != "" {
				fmt.Fprintf(w, "  %s %s: %s (%s)\n", prefix, f.Code, f.Message, f.Path)
			} else {
				fmt.Fprintf(w, "  %s %s: %s\n", prefix, f.Code, f.Message)
			}
			continue
		}
		if f.Path != "" {
			fmt.Fprintf(w, "  %s: %s (%s)\n", f.Code, f.Message, f.Path)
		} else {
			fmt.Fprintf(w, "  %s: %s\n", f.Code, f.Message)
		}
	}
}

func validateHasIOError(findings []validate.Finding) bool {
	for _, f := range findings {
		if f.Code == codeIO {
			return true
		}
	}
	return false
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

	rt := codexappserver.NewRuntime(codexappserver.Config{
		Command: codexappserver.DefaultCommandFromEnv(),
	})
	res, err := doctor.Run(context.Background(), doctor.Opts{
		OutRootFlag:    *outRoot,
		NativeRuntimes: []native.Runtime{rt},
	})
	if err != nil {
		fmt.Fprintf(r.Stderr, codeIO+": %s\n", err.Error())
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
		fmt.Fprintf(r.Stderr, codeIO+": %s\n", err.Error())
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
		fmt.Fprintf(r.Stderr, codeIO+": %s\n", err.Error())
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

	runner := fs.String("runner", "", "runner kind (required): "+runnerid.CLIUsageValues())
	rollout := fs.String("rollout", "", "path to runner rollout/session jsonl (required)")
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

	target, targetErr := resolveEnrichTarget(fs.Args())
	if targetErr != "" {
		printEnrichHelp(r.Stderr)
		return r.failUsage(targetErr)
	}
	if target == "" {
		printEnrichHelp(r.Stderr)
		return r.failUsage("enrich: missing <attemptDir> (or set ZCL_OUT_DIR)")
	}
	return r.runEnrichForRunner(*runner, target, *rollout)
}

func resolveEnrichTarget(rest []string) (string, string) {
	if len(rest) == 1 {
		return rest[0], ""
	}
	if len(rest) > 1 {
		return "", "enrich: require at most one <attemptDir>"
	}
	if env, err := trace.EnvFromProcess(); err == nil {
		return env.OutDirAbs, ""
	}
	return "", ""
}

func (r Runner) runEnrichForRunner(runner, target, rollout string) int {
	switch runner {
	case string(runnerid.Codex):
		return r.executeEnrich(target, rollout, enrich.EnrichCodexAttempt)
	case string(runnerid.Claude):
		return r.executeEnrich(target, rollout, enrich.EnrichClaudeAttempt)
	default:
		return r.failUsage("enrich: unsupported --runner (expected " + runnerid.CLIUsageValues() + ")")
	}
}

func (r Runner) executeEnrich(target, rollout string, fn func(string, string) error) int {
	if err := fn(target, rollout); err != nil {
		var ce *enrich.CliError
		if errors.As(err, &ce) {
			fmt.Fprintf(r.Stderr, "%s: %s\n", ce.Code, ce.Message)
			return 2
		}
		fmt.Fprintf(r.Stderr, codeIO+": %s\n", err.Error())
		return 1
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
		fmt.Fprintf(r.Stderr, codeUsage+": unknown mcp subcommand %q\n", args[0])
		printMCPHelp(r.Stderr)
		return 2
	}
}

func (r Runner) runMCPProxy(args []string) int {
	opts, exit, ok := r.parseMCPProxyArgs(args)
	if !ok {
		return exit
	}
	return r.executeMCPProxy(opts)
}

type mcpProxyArgs struct {
	env                trace.Env
	argv               []string
	maxToolCalls       int64
	idleTimeoutMs      int64
	shutdownOnComplete bool
	sequential         bool
}

func (r Runner) parseMCPProxyArgs(args []string) (mcpProxyArgs, int, bool) {
	fs := flag.NewFlagSet("mcp proxy", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	maxToolCalls := fs.Int64("max-tool-calls", 0, "max tools/call responses before proxy stops (0 disables)")
	idleTimeoutMs := fs.Int64("idle-timeout-ms", 0, "idle timeout in ms with no MCP traffic (0 disables)")
	shutdownOnComplete := fs.Bool("shutdown-on-complete", false, "terminate MCP server when request stream is complete and in-flight calls drain")
	sequential := fs.Bool("sequential", false, "forward MCP requests sequentially (wait for each id response before sending the next request)")
	help := fs.Bool("help", false, "show help")
	if err := fs.Parse(args); err != nil {
		return mcpProxyArgs{}, r.failUsage("mcp proxy: invalid flags"), false
	}
	if *help {
		printMCPProxyHelp(r.Stdout)
		return mcpProxyArgs{}, 0, false
	}
	env, exit, ok := r.loadMCPProxyEnv()
	if !ok {
		return mcpProxyArgs{}, exit, false
	}
	if err := validateMCPProxyAttemptContext(env); err != nil {
		printMCPProxyHelp(r.Stderr)
		return mcpProxyArgs{}, r.failUsage(err.Error()), false
	}
	argv, argvErr := parseMCPProxyCommand(fs.Args())
	if argvErr != "" {
		printMCPProxyHelp(r.Stderr)
		return mcpProxyArgs{}, r.failUsage(argvErr), false
	}
	resolvedMax, resolvedIdle, resolvedShutdown, errMsg := resolveMCPProxyRuntimeOptions(*maxToolCalls, *idleTimeoutMs, *shutdownOnComplete)
	if errMsg != "" {
		return mcpProxyArgs{}, r.failUsage(errMsg), false
	}
	return mcpProxyArgs{
		env:                env,
		argv:               argv,
		maxToolCalls:       resolvedMax,
		idleTimeoutMs:      resolvedIdle,
		shutdownOnComplete: resolvedShutdown,
		sequential:         *sequential,
	}, 0, true
}

func (r Runner) loadMCPProxyEnv() (trace.Env, int, bool) {
	env, err := trace.EnvFromProcess()
	if err != nil {
		printMCPProxyHelp(r.Stderr)
		return trace.Env{}, r.failUsage("mcp proxy: missing ZCL attempt context (need ZCL_* env)"), false
	}
	return env, 0, true
}

func validateMCPProxyAttemptContext(env trace.Env) error {
	a, err := attempt.ReadAttempt(env.OutDirAbs)
	if err != nil {
		return fmt.Errorf("mcp proxy: missing/invalid attempt.json in ZCL_OUT_DIR (need zcl attempt start context)")
	}
	if a.RunID != env.RunID || a.SuiteID != env.SuiteID || a.MissionID != env.MissionID || a.AttemptID != env.AttemptID {
		return fmt.Errorf("mcp proxy: attempt.json ids do not match ZCL_* env (refuse to run)")
	}
	return nil
}

func parseMCPProxyCommand(argv []string) ([]string, string) {
	if len(argv) >= 1 && argv[0] == "--" {
		argv = argv[1:]
	}
	if len(argv) == 0 {
		return nil, "mcp proxy: missing server command (use: zcl mcp proxy -- <server-cmd> ...)"
	}
	return argv, ""
}

func resolveMCPProxyRuntimeOptions(maxToolCalls int64, idleTimeoutMs int64, shutdownOnComplete bool) (int64, int64, bool, string) {
	if maxToolCalls < 0 {
		return 0, 0, false, "mcp proxy: --max-tool-calls must be >= 0"
	}
	if idleTimeoutMs < 0 {
		return 0, 0, false, "mcp proxy: --idle-timeout-ms must be >= 0"
	}
	var err string
	maxToolCalls, err = resolveProxyLimit(maxToolCalls, "ZCL_MCP_MAX_TOOL_CALLS", "mcp proxy: invalid ZCL_MCP_MAX_TOOL_CALLS")
	if err != "" {
		return 0, 0, false, err
	}
	idleTimeoutMs, err = resolveProxyLimit(idleTimeoutMs, "ZCL_MCP_IDLE_TIMEOUT_MS", "mcp proxy: invalid ZCL_MCP_IDLE_TIMEOUT_MS")
	if err != "" {
		return 0, 0, false, err
	}
	if !shutdownOnComplete && envBoolish("ZCL_MCP_SHUTDOWN_ON_COMPLETE") {
		shutdownOnComplete = true
	}
	return maxToolCalls, idleTimeoutMs, shutdownOnComplete, ""
}

func resolveProxyLimit(current int64, envKey, errMessage string) (int64, string) {
	if current != 0 {
		return current, ""
	}
	n, ok, err := parseNonNegativeInt64Env(envKey)
	if err != nil {
		return 0, errMessage
	}
	if ok {
		return n, ""
	}
	return current, ""
}

func parseNonNegativeInt64Env(key string) (int64, bool, error) {
	s := strings.TrimSpace(os.Getenv(key))
	if s == "" {
		return 0, false, nil
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil || n < 0 {
		return 0, false, fmt.Errorf("invalid %s", key)
	}
	return n, true, nil
}

func (r Runner) executeMCPProxy(opts mcpProxyArgs) int {
	now := r.Now()
	if _, err := attempt.EnsureTimeoutAnchor(now, opts.env.OutDirAbs); err != nil {
		fmt.Fprintf(r.Stderr, codeIO+": %s\n", err.Error())
		return 1
	}
	ctx, cancel, timedOut := attemptCtxForDeadline(now, opts.env.OutDirAbs)
	if cancel != nil {
		defer cancel()
	}
	if timedOut {
		fmt.Fprintf(r.Stderr, codeTimeout+": attempt deadline exceeded\n")
		return 1
	}
	if err := mcpproxy.ProxyWithOptions(ctx, opts.env, opts.argv, os.Stdin, r.Stdout, mcpproxy.Options{
		MaxPreviewBytes:    schema.PreviewMaxBytesV1,
		MaxToolCalls:       opts.maxToolCalls,
		IdleTimeoutMs:      opts.idleTimeoutMs,
		ShutdownOnComplete: opts.shutdownOnComplete,
		SequentialRequests: opts.sequential,
	}); err != nil {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
			fmt.Fprintf(r.Stderr, codeTimeout+": attempt deadline exceeded\n")
			return 1
		}
		fmt.Fprintf(r.Stderr, codeIO+": %s\n", err.Error())
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
	fmt.Fprintf(r.Stderr, codeIO+": %s\n", err.Error())
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

	var tokens runReportTokenAccumulator
	for _, rep := range reports {
		applyRunAttemptOutcome(&out, rep)
		applyRunAttemptEvidence(&out, rep)
		if rep.TimedOutBeforeFirstToolCall {
			out.Aggregate.TimedOutBeforeFirstToolCallTotal++
		}
		applyRunAttemptFailures(&out, rep)
		applyRunAttemptOrchestration(&out, rep)
		tokens.add(rep)
	}
	finalizeRunReportAggregate(&out, tokens)
	return out
}

func applyRunAttemptOutcome(out *runReportJSON, rep schema.AttemptReportJSONV1) {
	if rep.OK != nil && *rep.OK {
		out.Aggregate.Passed++
		out.Aggregate.Task.Passed++
		return
	}
	out.Aggregate.Failed++
	out.OK = false
	if rep.OK == nil {
		out.Aggregate.Task.Unknown++
		return
	}
	out.Aggregate.Task.Failed++
}

func applyRunAttemptEvidence(out *runReportJSON, rep schema.AttemptReportJSONV1) {
	if reportEvidenceComplete(rep) {
		out.Aggregate.Evidence.Complete++
		return
	}
	out.Aggregate.Evidence.Incomplete++
}

func applyRunAttemptFailures(out *runReportJSON, rep schema.AttemptReportJSONV1) {
	for code, n := range rep.FailureCodeHistogram {
		out.Aggregate.FailureCodeHistogram[code] += n
		if isOrchestrationInfraCode(code) {
			out.Aggregate.Orchestration.InfraFailureByCode[code] += n
		}
	}
}

func applyRunAttemptOrchestration(out *runReportJSON, rep schema.AttemptReportJSONV1) {
	if attemptHasInfraFailure(rep) {
		out.Aggregate.Orchestration.InfraFailed++
		return
	}
	out.Aggregate.Orchestration.Healthy++
}

type runReportTokenAccumulator struct {
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
}

func (t *runReportTokenAccumulator) add(rep schema.AttemptReportJSONV1) {
	if rep.TokenEstimates == nil {
		return
	}
	if rep.TokenEstimates.InputTokens != nil {
		t.inputTotal += *rep.TokenEstimates.InputTokens
		t.hasInput = true
	}
	if rep.TokenEstimates.OutputTokens != nil {
		t.outputTotal += *rep.TokenEstimates.OutputTokens
		t.hasOutput = true
	}
	if rep.TokenEstimates.TotalTokens != nil {
		t.totalTotal += *rep.TokenEstimates.TotalTokens
		t.hasTotal = true
	}
	if rep.TokenEstimates.CachedInputTokens != nil {
		t.cachedInputTotal += *rep.TokenEstimates.CachedInputTokens
		t.hasCached = true
	}
	if rep.TokenEstimates.ReasoningOutputTokens != nil {
		t.reasoningOutputTotal += *rep.TokenEstimates.ReasoningOutputTokens
		t.hasReasoning = true
	}
}

func finalizeRunReportAggregate(out *runReportJSON, tokens runReportTokenAccumulator) {
	if len(out.Aggregate.FailureCodeHistogram) == 0 {
		out.Aggregate.FailureCodeHistogram = nil
	}
	if len(out.Aggregate.Orchestration.InfraFailureByCode) == 0 {
		out.Aggregate.Orchestration.InfraFailureByCode = nil
	}
	if tokens.hasInput {
		out.Aggregate.TokenEstimates.InputTokens = i64ptr(tokens.inputTotal)
	}
	if tokens.hasOutput {
		out.Aggregate.TokenEstimates.OutputTokens = i64ptr(tokens.outputTotal)
	}
	if tokens.hasTotal {
		out.Aggregate.TokenEstimates.TotalTokens = i64ptr(tokens.totalTotal)
	} else if tokens.hasInput || tokens.hasOutput {
		out.Aggregate.TokenEstimates.TotalTokens = i64ptr(tokens.inputTotal + tokens.outputTotal)
	}
	if tokens.hasCached {
		out.Aggregate.TokenEstimates.CachedInputTokens = i64ptr(tokens.cachedInputTotal)
	}
	if tokens.hasReasoning {
		out.Aggregate.TokenEstimates.ReasoningOutputTokens = i64ptr(tokens.reasoningOutputTotal)
	}
	if out.Aggregate.TokenEstimates.TotalTokens == nil &&
		out.Aggregate.TokenEstimates.InputTokens == nil &&
		out.Aggregate.TokenEstimates.OutputTokens == nil &&
		out.Aggregate.TokenEstimates.CachedInputTokens == nil &&
		out.Aggregate.TokenEstimates.ReasoningOutputTokens == nil {
		out.Aggregate.TokenEstimates = nil
	}
}

func reportEvidenceComplete(rep schema.AttemptReportJSONV1) bool {
	if rep.Integrity == nil {
		return false
	}
	return rep.Integrity.TraceNonEmpty && rep.Integrity.FeedbackPresent
}

func isOrchestrationInfraCode(code string) bool {
	switch strings.TrimSpace(code) {
	case codeTimeout, codeSpawn, codeIO, codeRuntimeStall:
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
		fmt.Fprintf(r.Stderr, codeIO+": failed to encode json\n")
		return 1
	}
	return 0
}

func (r Runner) failUsage(msg string) int {
	fmt.Fprintf(r.Stderr, codeUsage+": %s\n", msg)
	return 2
}

func printRootHelp(w io.Writer) {
	fmt.Fprint(w, `ZCL (ZeroContext Lab)

Usage:
  zcl init [--out-root .zcl] [--config zcl.config.json] [--json]
  zcl update status [--cached] [--json]
  zcl contract --json
  zcl attempt start --suite <suiteId> --mission <missionId> --json
  zcl attempt env [--format sh|dotenv] [--json] [<attemptDir>]
  zcl attempt finish [--strict] [--json] [<attemptDir>]
  zcl attempt explain [--json] [--tail N] [<attemptDir>]
  zcl suite plan --file <suite.(yaml|yml|json)> --json
  zcl suite run --file <suite.(yaml|yml|json)> [--session-isolation auto|process|native] [--runtime-strategies <csv>] [--finalization-mode strict|auto_fail|auto_from_result_json] [--result-channel none|file_json|stdout_json] [--result-min-turn N] --json [-- <runner-cmd> [args...]]
  zcl campaign lint --spec <campaign.(yaml|yml|json)> [--json]
  zcl campaign run --spec <campaign.(yaml|yml|json)> [--json]
  zcl campaign canary --spec <campaign.(yaml|yml|json)> [--json]
  zcl campaign resume --campaign-id <id> [--json]
  zcl campaign status --campaign-id <id> [--json]
  zcl campaign report [--campaign-id <id> | --spec <campaign.(yaml|yml|json)>] [--json]
  zcl campaign publish-check [--campaign-id <id> | --spec <campaign.(yaml|yml|json)>] [--json]
  zcl campaign doctor --spec <campaign.(yaml|yml|json)> [--json]
  zcl runs list --json
  zcl attempt list [filters...] --json
  zcl attempt latest [filters...] --json
  zcl feedback --ok|--fail --result <string>|--result-json <json>
  zcl note [--kind agent|operator|system] --message <string>|--data-json <json>
  zcl report [--strict] [--json] <attemptDir|runDir>
  zcl validate [--strict] [--semantic] [--semantic-rules <path>] [--json] <attemptDir|runDir>
  zcl mission prompts build --spec <campaign.(yaml|yml|json)> --template <template.txt|md> [--json]
  zcl replay --json <attemptDir>
  zcl expect [--strict] --json <attemptDir|runDir>
  zcl doctor [--json]
  zcl gc [--dry-run] [--json]
  zcl pin --run-id <runId> --on|--off [--json]
`)
	fmt.Fprintf(w, "  %s\n", enrichUsage())
	fmt.Fprint(w, `  zcl mcp proxy [--max-tool-calls N] [--idle-timeout-ms N] [--shutdown-on-complete] -- <server-cmd> [args...]
  zcl http proxy --upstream <url> [--listen 127.0.0.1:0] [--max-requests N] [--json]
  zcl run -- <cmd> [args...]

Commands:
  init            Initialize the project (.zcl output root + zcl.config.json).
  update status   Check latest release status (manual updates only; no auto-update).
  contract        Print the ZCL surface contract (use --json).
  attempt start   Allocate a run/attempt dir and print canonical IDs + env (use --json).
  attempt env     Print canonical attempt env (or return it as JSON).
  attempt finish  Write attempt.report.json, then validate + expect (use --json for automation).
  attempt explain Fast post-mortem view from artifacts (tail trace + pointers).
  suite plan      Allocate attempt dirs for every mission in a suite file (use --json).
  suite run       Run a suite end-to-end with capability-aware isolation selection.
  campaign        First-class campaign orchestration (lint/run/canary/resume/status/report/publish-check/doctor).
  runs list       List run index rows for automation (use --json).
  attempt list    List attempts with filters (suite/mission/status/tags) as JSON index rows.
  attempt latest  Return latest attempt matching filters as one JSON row.
  feedback        Write the canonical attempt outcome to feedback.json.
  note            Append a secondary evidence note to notes.jsonl.
  report           Compute attempt.report.json from tool.calls.jsonl + feedback.json.
  validate         Validate artifact integrity and optional semantic validity with typed error codes.
  mission          Deterministic mission prompt materialization commands.
  replay           Best-effort replay of tool.calls.jsonl (use --json).
  expect           Evaluate suite expectations against feedback.json (use --json).
  doctor           Check environment/config sanity for running ZCL.
  gc               Retention cleanup under .zcl/runs (supports pinning).
  pin              Pin/unpin a run so gc will keep it.
  enrich           Optional runner enrichment (does not affect scoring).
  mcp proxy        MCP stdio proxy funnel (records initialize/tools/list/tools/call; optional sequential request mode).
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
  zcl attempt env [--format sh|dotenv] [--json] [<attemptDir>]
  zcl attempt finish [--strict] [--json] [<attemptDir>]
  zcl attempt explain [--json] [--tail N] [<attemptDir>]
  zcl attempt list [--out-root .zcl] [--suite <suiteId>] [--mission <missionId>] [--status any|ok|fail|missing_feedback] [--tag <tag>] [--limit N] --json
  zcl attempt latest [--out-root .zcl] [--suite <suiteId>] [--mission <missionId>] [--status any|ok|fail|missing_feedback] [--tag <tag>] --json
`)
}

func printRunsHelp(w io.Writer) {
	fmt.Fprint(w, `Usage:
  zcl runs list [--out-root .zcl] [--suite <suiteId>] [--status any|ok|fail|missing_feedback] [--limit N] --json
`)
}

func printAttemptStartHelp(w io.Writer) {
	fmt.Fprint(w, `Usage:
	  zcl attempt start --suite <suiteId> --mission <missionId> [--prompt <text>] [--suite-file <path>] [--run-id <runId>] [--agent-id <id>] [--isolation-model process_runner|native_spawn] [--mode discovery|ci] [--timeout-ms N] [--timeout-start attempt_start|first_tool_call] [--blind] [--blind-terms a,b,c] [--out-root .zcl] [--retry 1] [--env-file <path>] [--env-format sh|dotenv] [--print-env sh|dotenv] --json

	Notes:
	  - Always writes <attemptDir>/attempt.env.sh and records it in attempt.json.
			`)
}

func printAttemptEnvHelp(w io.Writer) {
	fmt.Fprint(w, `Usage:
  zcl attempt env [--format sh|dotenv] [--json] [<attemptDir>]

Notes:
  - If <attemptDir> is omitted, ZCL_OUT_DIR is used.
  - Backfills attempt.env.sh for older attempts when missing.
`)
}

func printSuiteHelp(w io.Writer) {
	fmt.Fprint(w, `Usage:
  zcl suite plan --file <suite.(yaml|yml|json)> --json
  zcl suite run --file <suite.(yaml|yml|json)> [--session-isolation auto|process|native] [--runtime-strategies <csv>] [--finalization-mode strict|auto_fail|auto_from_result_json] [--result-channel none|file_json|stdout_json] [--result-min-turn N] --json [-- <runner-cmd> [args...]]
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

Notes:
  - Requires ZCL attempt context (ZCL_* env from zcl attempt start/suite run).
  - Requires non-empty tool.calls.jsonl before writing feedback (funnel-first evidence).
`)
}

func feedbackHint(msg string) string {
	switch {
	case strings.Contains(msg, "missing attempt.json in attempt directory"):
		return "start an attempt first (`zcl attempt start --json`) and run feedback inside that attempt context (ZCL_* env)."
	case strings.Contains(msg, "tool.calls.jsonl is required before feedback"),
		strings.Contains(msg, "tool.calls.jsonl must be non-empty before feedback"):
		return "record at least one funnel action first (for example: `zcl run -- echo hi`), then run `zcl feedback` again."
	default:
		return ""
	}
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
  zcl validate [--strict] [--semantic] [--semantic-rules <path>] [--json] <attemptDir|runDir>
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
	fmt.Fprintf(w, "Usage:\n  %s\n", enrichUsage())
}

func enrichUsage() string {
	return fmt.Sprintf("zcl enrich --runner %s --rollout <rollout.jsonl> [<attemptDir>]", runnerid.CLIUsageValues())
}

func printMCPHelp(w io.Writer) {
	fmt.Fprint(w, `Usage:
  zcl mcp proxy --max-tool-calls N --idle-timeout-ms N --shutdown-on-complete --sequential -- <server-cmd> [args...]
`)
}

func printMCPProxyHelp(w io.Writer) {
	fmt.Fprint(w, `Usage:
  zcl mcp proxy [--max-tool-calls N] [--idle-timeout-ms N] [--shutdown-on-complete] [--sequential] -- <server-cmd> [args...]
`)
}
