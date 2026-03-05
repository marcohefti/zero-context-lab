package cli

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/marcohefti/zero-context-lab/internal/contexts/evaluation/app/semantic"
	"github.com/marcohefti/zero-context-lab/internal/contexts/evaluation/domain/oracle"
	"github.com/marcohefti/zero-context-lab/internal/contexts/execution/app/campaign"
	"github.com/marcohefti/zero-context-lab/internal/contexts/execution/app/runners"
	"github.com/marcohefti/zero-context-lab/internal/contexts/runtime/infra/codex_app_server"
	"github.com/marcohefti/zero-context-lab/internal/kernel/codes"
	"github.com/marcohefti/zero-context-lab/internal/kernel/config"
	"github.com/marcohefti/zero-context-lab/internal/kernel/ids"
	"github.com/marcohefti/zero-context-lab/internal/kernel/schema"
	"github.com/marcohefti/zero-context-lab/internal/kernel/store"
)

type campaignSegment struct {
	MissionOffset int
	TotalMissions int
}

type missionPromptsBuildResult struct {
	SchemaVersion int                       `json:"schemaVersion"`
	CampaignID    string                    `json:"campaignId"`
	SpecPath      string                    `json:"specPath"`
	TemplatePath  string                    `json:"templatePath"`
	OutPath       string                    `json:"outPath"`
	CreatedAt     string                    `json:"createdAt"`
	Prompts       []missionPromptArtifactV1 `json:"prompts"`
}

type missionPromptArtifactV1 struct {
	ID           string `json:"id"`
	FlowID       string `json:"flowId"`
	SuiteID      string `json:"suiteId"`
	MissionID    string `json:"missionId"`
	MissionIndex int    `json:"missionIndex"`
	Prompt       string `json:"prompt"`
}

const oracleVerdictFileName = "oracle.verdict.json"

type oracleEvaluatorOutput struct {
	OK                bool              `json:"ok"`
	ReasonCodes       []string          `json:"reasonCodes,omitempty"`
	Message           string            `json:"message,omitempty"`
	Mismatches        []oracle.Mismatch `json:"mismatches,omitempty"`
	PolicyDisposition string            `json:"policyDisposition,omitempty"` // fail|warn|ignore
	Warnings          []string          `json:"warnings,omitempty"`
	Expected          any               `json:"expected,omitempty"`
	Actual            any               `json:"actual,omitempty"`
	Details           any               `json:"details,omitempty"`
}

type oracleVerdictArtifact struct {
	SchemaVersion     int               `json:"schemaVersion"`
	CampaignID        string            `json:"campaignId"`
	FlowID            string            `json:"flowId"`
	MissionID         string            `json:"missionId"`
	AttemptID         string            `json:"attemptId"`
	AttemptDir        string            `json:"attemptDir"`
	OraclePath        string            `json:"oraclePath"`
	EvaluatorKind     string            `json:"evaluatorKind"`
	EvaluatorCmd      []string          `json:"evaluatorCommand"`
	PromptMode        string            `json:"promptMode"`
	OK                bool              `json:"ok"`
	ReasonCodes       []string          `json:"reasonCodes,omitempty"`
	Message           string            `json:"message,omitempty"`
	Mismatches        []oracle.Mismatch `json:"mismatches,omitempty"`
	PolicyDisposition string            `json:"policyDisposition,omitempty"`
	Warnings          []string          `json:"warnings,omitempty"`
	ExecutedAt        string            `json:"executedAt"`
}

type attemptFeedbackSummary struct {
	Present       bool
	OKKnown       bool
	OK            bool
	ResultCode    string
	ResultKind    string
	HasValidProof bool
}

var oracleExpectedGotRE = regexp.MustCompile(`^\s*([A-Za-z0-9_./-]+)\s+expected\s+(.+)\s+got\s+(.+)\s*$`)

func (r Runner) runCampaign(args []string) int {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" || args[0] == "help" {
		printCampaignHelp(r.Stdout)
		return 0
	}
	switch args[0] {
	case "lint":
		return r.runCampaignLint(args[1:])
	case "run":
		return r.runCampaignRun(args[1:])
	case "canary":
		return r.runCampaignCanary(args[1:])
	case "resume":
		return r.runCampaignResume(args[1:])
	case "status":
		return r.runCampaignStatus(args[1:])
	case "report":
		return r.runCampaignReport(args[1:])
	case "publish-check":
		return r.runCampaignPublishCheck(args[1:])
	case "doctor":
		return r.runCampaignDoctor(args[1:])
	default:
		fmt.Fprintf(r.Stderr, codeUsage+": unknown campaign subcommand %q\n", args[0])
		printCampaignHelp(r.Stderr)
		return 2
	}
}

func (r Runner) runCampaignLint(args []string) int {
	fs := flag.NewFlagSet("campaign lint", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	spec := fs.String("spec", "", "campaign spec file (.json|.yaml|.yml) (required)")
	outRoot := fs.String("out-root", "", "project output root (default from config/env, else spec.outRoot, else .zcl)")
	jsonOut := fs.Bool("json", false, "print JSON output")
	help := fs.Bool("help", false, "show help")
	if err := fs.Parse(args); err != nil {
		return r.failUsage("campaign lint: invalid flags")
	}
	if *help {
		printCampaignLintHelp(r.Stdout)
		return 0
	}
	if strings.TrimSpace(*spec) == "" {
		printCampaignLintHelp(r.Stderr)
		return r.failUsage("campaign lint: missing --spec")
	}

	parsed, resolvedOutRoot, err := r.loadCampaignSpec(*spec, *outRoot)
	if err != nil {
		if exit, handled := r.writeCampaignSpecPolicyError(err, *jsonOut); handled {
			return exit
		}
		fmt.Fprintf(r.Stderr, codeIO+": %s\n", err.Error())
		return 1
	}
	out := map[string]any{
		"ok":            true,
		"schemaVersion": parsed.Spec.SchemaVersion,
		"campaignId":    parsed.Spec.CampaignID,
		"specPath":      parsed.SpecPath,
		"outRoot":       resolvedOutRoot,
		"promptMode":    parsed.Spec.PromptMode,
		"flows":         len(parsed.Spec.Flows),
		"execution": map[string]any{
			"flowMode": parsed.Spec.Execution.FlowMode,
		},
		"executionMode": campaign.ResolveExecutionMode(parsed),
		"missions": map[string]any{
			"selectedTotal": len(parsed.MissionIndexes),
			"selectionMode": parsed.Spec.MissionSource.Selection.Mode,
			"indexes":       parsed.MissionIndexes,
		},
		"missionSource": map[string]any{
			"path": parsed.Spec.MissionSource.Path,
			"promptSource": map[string]any{
				"path": parsed.Spec.MissionSource.PromptSource.Path,
			},
			"oracleSource": map[string]any{
				"path":       parsed.Spec.MissionSource.OracleSource.Path,
				"visibility": parsed.Spec.MissionSource.OracleSource.Visibility,
			},
		},
		"evaluation": map[string]any{
			"mode": parsed.Spec.Evaluation.Mode,
			"evaluator": map[string]any{
				"kind":    parsed.Spec.Evaluation.Evaluator.Kind,
				"command": parsed.Spec.Evaluation.Evaluator.Command,
			},
			"oraclePolicy": map[string]any{
				"mode":           parsed.Spec.Evaluation.OraclePolicy.Mode,
				"formatMismatch": parsed.Spec.Evaluation.OraclePolicy.FormatMismatch,
			},
		},
		"pairGate": map[string]any{
			"enabled":                   parsed.Spec.PairGateEnabled(),
			"stopOnFirstMissionFailure": parsed.Spec.PairGate.StopOnFirstMissionFailure,
			"traceProfile":              parsed.Spec.PairGate.TraceProfile,
		},
		"flowGate": map[string]any{
			"enabled":                   parsed.Spec.PairGateEnabled(),
			"stopOnFirstMissionFailure": parsed.Spec.PairGate.StopOnFirstMissionFailure,
			"traceProfile":              parsed.Spec.PairGate.TraceProfile,
		},
		"semantic": map[string]any{
			"enabled":   parsed.Spec.Semantic.Enabled,
			"rulesPath": parsed.Spec.Semantic.RulesPath,
		},
		"noContext": map[string]any{
			"forbiddenPromptTerms": parsed.Spec.NoContext.ForbiddenPromptTerms,
			"violations":           campaign.EvaluatePromptModeViolations(parsed),
		},
		"extensions": parsed.Spec.Extensions,
	}
	if *jsonOut {
		return r.writeJSON(out)
	}
	fmt.Fprintf(r.Stdout, "campaign lint: OK campaign=%s flows=%d selectedMissions=%d\n", parsed.Spec.CampaignID, len(parsed.Spec.Flows), len(parsed.MissionIndexes))
	return 0
}

func (r Runner) runCampaignRun(args []string) int {
	opts, exit, ok := r.parseCampaignRunOptions(args)
	if !ok {
		return exit
	}
	parsed, resolvedOutRoot, exit, ok := r.loadCampaignSpecForExecution(opts.spec, opts.outRoot, opts.jsonOut)
	if !ok {
		return exit
	}
	indexes, msg, ok := resolveCampaignRunIndexes(parsed, opts.missionOffset, opts.missions)
	if !ok {
		return r.failUsage("campaign run: " + msg)
	}
	return r.executeCampaignAndWrite(parsed, resolvedOutRoot, campaignExecutionInput{
		MissionOffset:  opts.missionOffset,
		MissionIndexes: indexes,
		Canary:         false,
	}, opts.jsonOut, "campaign run")
}

type campaignRunOptions struct {
	spec          string
	outRoot       string
	missions      int
	missionOffset int
	jsonOut       bool
}

func (r Runner) parseCampaignRunOptions(args []string) (campaignRunOptions, int, bool) {
	fs := flag.NewFlagSet("campaign run", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	spec := fs.String("spec", "", "campaign spec file (.json|.yaml|.yml) (required)")
	outRoot := fs.String("out-root", "", "project output root (default from config/env, else spec.outRoot, else .zcl)")
	missions := fs.Int("missions", 0, "optional mission count override (default spec.totalMissions)")
	missionOffset := fs.Int("mission-offset", 0, "0-based mission offset (default 0)")
	jsonOut := fs.Bool("json", false, "print JSON output")
	help := fs.Bool("help", false, "show help")

	if err := fs.Parse(args); err != nil {
		return campaignRunOptions{}, r.failUsage("campaign run: invalid flags"), false
	}
	if *help {
		printCampaignRunHelp(r.Stdout)
		return campaignRunOptions{}, 0, false
	}
	if strings.TrimSpace(*spec) == "" {
		printCampaignRunHelp(r.Stderr)
		return campaignRunOptions{}, r.failUsage("campaign run: missing --spec"), false
	}
	if *missionOffset < 0 {
		return campaignRunOptions{}, r.failUsage("campaign run: --mission-offset must be >= 0"), false
	}
	if *missions < 0 {
		return campaignRunOptions{}, r.failUsage("campaign run: --missions must be >= 0"), false
	}
	return campaignRunOptions{
		spec:          *spec,
		outRoot:       *outRoot,
		missions:      *missions,
		missionOffset: *missionOffset,
		jsonOut:       *jsonOut,
	}, 0, true
}

func (r Runner) loadCampaignSpecForExecution(spec, outRoot string, jsonOut bool) (campaign.ParsedSpec, string, int, bool) {
	parsed, resolvedOutRoot, err := r.loadCampaignSpec(spec, outRoot)
	if err != nil {
		if exit, handled := r.writeCampaignSpecPolicyError(err, jsonOut); handled {
			return campaign.ParsedSpec{}, "", exit, false
		}
		fmt.Fprintf(r.Stderr, codeIO+": %s\n", err.Error())
		return campaign.ParsedSpec{}, "", 1, false
	}
	return parsed, resolvedOutRoot, 0, true
}

func resolveCampaignRunIndexes(parsed campaign.ParsedSpec, missionOffset, missions int) ([]int, string, bool) {
	total := missions
	if total == 0 {
		total = parsed.Spec.TotalMissions
	}
	if total <= 0 {
		total = len(parsed.MissionIndexes)
	}
	indexes, err := campaign.WindowMissionIndexes(parsed.MissionIndexes, missionOffset, total)
	if err != nil {
		return nil, err.Error(), false
	}
	if len(indexes) == 0 {
		return nil, "no missions to run", false
	}
	return indexes, "", true
}

func (r Runner) runCampaignCanary(args []string) int {
	opts, exit, ok := r.parseCampaignCanaryOptions(args)
	if !ok {
		return exit
	}
	parsed, resolvedOutRoot, exit, ok := r.loadCampaignSpecForExecution(opts.spec, opts.outRoot, opts.jsonOut)
	if !ok {
		return exit
	}
	indexes, msg, ok := resolveCampaignCanaryIndexes(parsed, opts.missionOffset, opts.missions)
	if !ok {
		return r.failUsage("campaign canary: " + msg)
	}
	return r.executeCampaignAndWrite(parsed, resolvedOutRoot, campaignExecutionInput{
		MissionOffset:  opts.missionOffset,
		MissionIndexes: indexes,
		Canary:         true,
	}, opts.jsonOut, "campaign canary")
}

type campaignCanaryOptions struct {
	spec          string
	outRoot       string
	missions      int
	missionOffset int
	jsonOut       bool
}

func (r Runner) parseCampaignCanaryOptions(args []string) (campaignCanaryOptions, int, bool) {
	fs := flag.NewFlagSet("campaign canary", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	spec := fs.String("spec", "", "campaign spec file (.json|.yaml|.yml) (required)")
	outRoot := fs.String("out-root", "", "project output root (default from config/env, else spec.outRoot, else .zcl)")
	missions := fs.Int("missions", 0, "canary mission count (default spec.canaryMissions, else 3)")
	missionOffset := fs.Int("mission-offset", 0, "0-based mission offset (default 0)")
	jsonOut := fs.Bool("json", false, "print JSON output")
	help := fs.Bool("help", false, "show help")

	if err := fs.Parse(args); err != nil {
		return campaignCanaryOptions{}, r.failUsage("campaign canary: invalid flags"), false
	}
	if *help {
		printCampaignCanaryHelp(r.Stdout)
		return campaignCanaryOptions{}, 0, false
	}
	if strings.TrimSpace(*spec) == "" {
		printCampaignCanaryHelp(r.Stderr)
		return campaignCanaryOptions{}, r.failUsage("campaign canary: missing --spec"), false
	}
	if *missionOffset < 0 {
		return campaignCanaryOptions{}, r.failUsage("campaign canary: --mission-offset must be >= 0"), false
	}
	if *missions < 0 {
		return campaignCanaryOptions{}, r.failUsage("campaign canary: --missions must be >= 0"), false
	}
	return campaignCanaryOptions{
		spec:          *spec,
		outRoot:       *outRoot,
		missions:      *missions,
		missionOffset: *missionOffset,
		jsonOut:       *jsonOut,
	}, 0, true
}

func resolveCampaignCanaryIndexes(parsed campaign.ParsedSpec, missionOffset, missions int) ([]int, string, bool) {
	total := missions
	if total == 0 {
		total = parsed.Spec.CanaryMissions
	}
	if total <= 0 {
		total = 3
	}
	if total > len(parsed.MissionIndexes) {
		total = len(parsed.MissionIndexes)
	}
	if total <= 0 {
		return nil, "no missions to run", false
	}
	indexes, err := campaign.WindowMissionIndexes(parsed.MissionIndexes, missionOffset, total)
	if err != nil {
		return nil, err.Error(), false
	}
	if len(indexes) == 0 {
		return nil, "no missions to run", false
	}
	return indexes, "", true
}

func (r Runner) executeCampaignAndWrite(parsed campaign.ParsedSpec, resolvedOutRoot string, in campaignExecutionInput, jsonOut bool, label string) int {
	st, exit := r.executeCampaign(parsed, resolvedOutRoot, in)
	if jsonOut {
		writeExit := r.writeJSON(st)
		if writeExit != 0 {
			return writeExit
		}
	}
	if !jsonOut {
		fmt.Fprintf(r.Stdout, "%s: %s (%s)\n", label, st.Status, st.RunID)
	}
	return exit
}

func (r Runner) runCampaignResume(args []string) int {
	opts, cid, exit, ok := r.parseCampaignResumeOptions(args)
	if !ok {
		return exit
	}
	st, resolvedOutRoot, exit, ok := r.loadCampaignResumeState(cid, opts.outRoot)
	if !ok {
		return exit
	}
	return r.resumeCampaignFromState(opts.jsonOut, cid, st, resolvedOutRoot)
}

type campaignResumeOptions struct {
	outRoot string
	jsonOut bool
}

func (r Runner) parseCampaignResumeOptions(args []string) (campaignResumeOptions, string, int, bool) {
	fs := flag.NewFlagSet("campaign resume", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	campaignID := fs.String("campaign-id", "", "campaign id (required)")
	outRoot := fs.String("out-root", "", "project output root (default from config/env, else state.outRoot)")
	jsonOut := fs.Bool("json", false, "print JSON output")
	help := fs.Bool("help", false, "show help")

	if err := fs.Parse(args); err != nil {
		return campaignResumeOptions{}, "", r.failUsage("campaign resume: invalid flags"), false
	}
	if *help {
		printCampaignResumeHelp(r.Stdout)
		return campaignResumeOptions{}, "", 0, false
	}
	cid := ids.SanitizeComponent(strings.TrimSpace(*campaignID))
	if cid == "" {
		printCampaignResumeHelp(r.Stderr)
		return campaignResumeOptions{}, "", r.failUsage("campaign resume: missing/invalid --campaign-id"), false
	}
	return campaignResumeOptions{outRoot: *outRoot, jsonOut: *jsonOut}, cid, 0, true
}

func (r Runner) loadCampaignResumeState(campaignID, outRoot string) (campaign.RunStateV1, string, int, bool) {
	m, err := config.LoadMerged(outRoot)
	if err != nil {
		fmt.Fprintf(r.Stderr, codeIO+": %s\n", err.Error())
		return campaign.RunStateV1{}, "", 1, false
	}
	resolvedOutRoot := m.OutRoot
	statePath := campaign.RunStatePath(resolvedOutRoot, campaignID)
	st, err := campaign.LoadRunState(statePath)
	if err != nil {
		fmt.Fprintf(r.Stderr, codeIO+": %s\n", err.Error())
		return campaign.RunStateV1{}, "", 1, false
	}
	if strings.TrimSpace(st.OutRoot) != "" && strings.TrimSpace(outRoot) == "" {
		resolvedOutRoot = st.OutRoot
		statePath = campaign.RunStatePath(resolvedOutRoot, campaignID)
		st, err = campaign.LoadRunState(statePath)
		if err != nil {
			fmt.Fprintf(r.Stderr, codeIO+": %s\n", err.Error())
			return campaign.RunStateV1{}, "", 1, false
		}
	}
	if strings.TrimSpace(st.SpecPath) == "" {
		return campaign.RunStateV1{}, "", r.failUsage("campaign resume: existing campaign state is missing specPath"), false
	}
	return st, resolvedOutRoot, 0, true
}

func (r Runner) resumeCampaignFromState(jsonOut bool, campaignID string, st campaign.RunStateV1, resolvedOutRoot string) int {
	parsed, resolvedOutRoot, err := r.loadCampaignSpec(st.SpecPath, resolvedOutRoot)
	if err != nil {
		if exit, handled := r.writeCampaignSpecPolicyError(err, jsonOut); handled {
			return exit
		}
		fmt.Fprintf(r.Stderr, codeIO+": %s\n", err.Error())
		return 1
	}
	if parsed.Spec.CampaignID != campaignID {
		return r.failUsage("campaign resume: campaign-id does not match stored spec")
	}
	if msg, drift := campaignStateDriftMessage(st); drift {
		return r.writeCampaignStateDrift(jsonOut, st.CampaignID, st.RunID, msg)
	}
	if len(parsed.MissionIndexes) == 0 {
		return r.failUsage("campaign resume: spec has no missions")
	}
	next, exit := r.executeCampaign(parsed, resolvedOutRoot, campaignExecutionInput{
		MissionOffset:    0,
		MissionIndexes:   parsed.MissionIndexes,
		Canary:           false,
		ResumedFromRunID: st.RunID,
	})
	if jsonOut {
		writeExit := r.writeJSON(next)
		if writeExit != 0 {
			return writeExit
		}
	}
	if !jsonOut {
		fmt.Fprintf(r.Stdout, "campaign resume: %s (%s)\n", next.Status, next.RunID)
	}
	return exit
}

func (r Runner) runCampaignStatus(args []string) int {
	fs := flag.NewFlagSet("campaign status", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	campaignID := fs.String("campaign-id", "", "campaign id (required)")
	outRoot := fs.String("out-root", "", "project output root (default from config/env, else .zcl)")
	jsonOut := fs.Bool("json", false, "print JSON output")
	help := fs.Bool("help", false, "show help")

	if err := fs.Parse(args); err != nil {
		return r.failUsage("campaign status: invalid flags")
	}
	if *help {
		printCampaignStatusHelp(r.Stdout)
		return 0
	}
	cid := ids.SanitizeComponent(strings.TrimSpace(*campaignID))
	if cid == "" {
		printCampaignStatusHelp(r.Stderr)
		return r.failUsage("campaign status: missing/invalid --campaign-id")
	}

	m, err := config.LoadMerged(*outRoot)
	if err != nil {
		fmt.Fprintf(r.Stderr, codeIO+": %s\n", err.Error())
		return 1
	}
	st, err := campaign.LoadRunState(campaign.RunStatePath(m.OutRoot, cid))
	if err != nil {
		fmt.Fprintf(r.Stderr, codeIO+": %s\n", err.Error())
		return 1
	}
	if msg, drift := campaignStateDriftMessage(st); drift {
		return r.writeCampaignStateDrift(*jsonOut, st.CampaignID, st.RunID, msg)
	}
	if *jsonOut {
		return r.writeJSON(st)
	}
	fmt.Fprintf(r.Stdout, "campaign status: %s runId=%s completed=%d/%d\n", st.Status, st.RunID, st.MissionsCompleted, st.TotalMissions)
	return 0
}

func (r Runner) runCampaignReport(args []string) int {
	fs := flag.NewFlagSet("campaign report", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	campaignID := fs.String("campaign-id", "", "campaign id (required unless --spec is provided)")
	spec := fs.String("spec", "", "campaign spec file (.json|.yaml|.yml) (optional alternative to --campaign-id)")
	outRoot := fs.String("out-root", "", "project output root (default from config/env, else .zcl)")
	format := fs.String("format", "json", "output format list: json,md")
	force := fs.Bool("force", false, "allow report export when campaign status is invalid|aborted")
	allowInvalid := fs.Bool("allow-invalid", false, "export report and return exit 0 even when campaign status is invalid|aborted")
	jsonOut := fs.Bool("json", false, "print JSON output")
	help := fs.Bool("help", false, "show help")

	if err := fs.Parse(args); err != nil {
		return r.failUsage("campaign report: invalid flags")
	}
	if *help {
		printCampaignReportHelp(r.Stdout)
		return 0
	}
	st, exit, ok := r.resolveCampaignRunState(*campaignID, *spec, *outRoot, *jsonOut, "campaign report", printCampaignReportHelp)
	if !ok {
		return exit
	}
	rep := campaign.BuildReport(st)
	sum := campaign.BuildSummary(st)
	if err := r.persistCampaignArtifacts(st); err != nil {
		fmt.Fprintf(r.Stderr, codeIO+": %s\n", err.Error())
		return 1
	}

	policy := resolveCampaignInvalidRunPolicy(st)
	if !*force && !*allowInvalid && policy.PublishRequiresValid && (st.Status == campaign.RunStatusInvalid || st.Status == campaign.RunStatusAborted) {
		if *jsonOut {
			_ = r.writeJSON(rep)
		}
		fmt.Fprintf(r.Stderr, codeUsage+": campaign report: status=%s (use --allow-invalid or --force to export)\n", st.Status)
		return 2
	}

	if *jsonOut {
		return r.writeJSON(rep)
	}
	fmts := parseFormatList(*format)
	if fmts["md"] {
		fmt.Fprint(r.Stdout, formatCampaignResultsMarkdown(sum))
		return 0
	}
	fmt.Fprintf(r.Stdout, "campaign report: status=%s gates=%d/%d\n", rep.Status, rep.GatesPassed, rep.GatesPassed+rep.GatesFailed)
	return 0
}

func (r Runner) runCampaignPublishCheck(args []string) int {
	opts, exit, ok := r.parseCampaignPublishCheckOptions(args)
	if !ok {
		return exit
	}
	st, exit, resolved := r.resolveCampaignRunState(opts.campaignID, opts.spec, opts.outRoot, opts.jsonOut, "campaign publish-check", printCampaignPublishCheckHelp)
	if !resolved {
		return exit
	}
	outcome, exit, ok := r.evaluateCampaignPublishCheckOutcome(st, opts.force)
	if !ok {
		return exit
	}
	return r.writeCampaignPublishCheckOutcome(outcome, opts.jsonOut)
}

type campaignPublishCheckOptions struct {
	campaignID string
	spec       string
	outRoot    string
	force      bool
	jsonOut    bool
}

type campaignPublishCheckOutcome struct {
	publishOK bool
	state     campaign.RunStateV1
	payload   map[string]any
}

func (r Runner) parseCampaignPublishCheckOptions(args []string) (campaignPublishCheckOptions, int, bool) {
	fs := flag.NewFlagSet("campaign publish-check", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	campaignID := fs.String("campaign-id", "", "campaign id (required unless --spec is provided)")
	spec := fs.String("spec", "", "campaign spec file (.json|.yaml|.yml) (optional alternative to --campaign-id)")
	outRoot := fs.String("out-root", "", "project output root (default from config/env, else .zcl)")
	force := fs.Bool("force", false, "allow publish-check to pass even when campaign is invalid|aborted")
	jsonOut := fs.Bool("json", false, "print JSON output")
	help := fs.Bool("help", false, "show help")

	if err := fs.Parse(args); err != nil {
		return campaignPublishCheckOptions{}, r.failUsage("campaign publish-check: invalid flags"), false
	}
	if *help {
		printCampaignPublishCheckHelp(r.Stdout)
		return campaignPublishCheckOptions{}, 0, false
	}
	return campaignPublishCheckOptions{
		campaignID: *campaignID,
		spec:       *spec,
		outRoot:    *outRoot,
		force:      *force,
		jsonOut:    *jsonOut,
	}, 0, true
}

func (r Runner) evaluateCampaignPublishCheckOutcome(st campaign.RunStateV1, force bool) (campaignPublishCheckOutcome, int, bool) {
	policy := resolveCampaignInvalidRunPolicy(st)
	publishOK := campaignPublishStatusOK(policy, st.Status)
	promptModeCompliance := map[string]any{"ok": true, "code": campaign.ReasonPromptModePolicy, "promptMode": ""}
	oraclePolicyCompliance := map[string]any{"ok": true}
	toolDriverCompliance := map[string]any{"ok": true, "code": campaign.ReasonToolDriverShim}

	nextState, publishOK, exit, ok := r.applyCampaignPublishSpecCompliance(st, publishOK, promptModeCompliance, oraclePolicyCompliance, toolDriverCompliance)
	if !ok {
		return campaignPublishCheckOutcome{}, exit, false
	}
	if force && !publishOK {
		publishOK = true
	}
	out := map[string]any{
		"ok":                     publishOK,
		"campaignId":             nextState.CampaignID,
		"runId":                  nextState.RunID,
		"status":                 nextState.Status,
		"reasonCodes":            nextState.ReasonCodes,
		"promptModeCompliance":   promptModeCompliance,
		"oraclePolicyCompliance": oraclePolicyCompliance,
		"toolDriverCompliance":   toolDriverCompliance,
	}
	return campaignPublishCheckOutcome{publishOK: publishOK, state: nextState, payload: out}, 0, true
}

func campaignPublishStatusOK(policy resolvedInvalidRunPolicy, status string) bool {
	publishOK := status == campaign.RunStatusValid
	if !policy.PublishRequiresValid {
		publishOK = status != campaign.RunStatusAborted
	}
	if len(policy.Statuses) > 0 && !containsString(policy.Statuses, status) {
		publishOK = false
	}
	return publishOK
}

func (r Runner) writeCampaignPublishCheckOutcome(outcome campaignPublishCheckOutcome, jsonOut bool) int {
	if jsonOut {
		writeExit := r.writeJSON(outcome.payload)
		if writeExit != 0 {
			return writeExit
		}
	} else if outcome.publishOK {
		fmt.Fprintf(r.Stdout, "publish-check: OK campaign=%s run=%s\n", outcome.state.CampaignID, outcome.state.RunID)
	} else {
		for _, code := range outcome.state.ReasonCodes {
			fmt.Fprintf(r.Stderr, "%s\n", code)
		}
		fmt.Fprintf(r.Stderr, "publish-check: FAIL campaign=%s run=%s status=%s\n", outcome.state.CampaignID, outcome.state.RunID, outcome.state.Status)
	}
	if outcome.publishOK {
		return 0
	}
	return 2
}

func (r Runner) applyCampaignPublishSpecCompliance(st campaign.RunStateV1, publishOK bool, promptModeCompliance, oraclePolicyCompliance, toolDriverCompliance map[string]any) (campaign.RunStateV1, bool, int, bool) {
	if strings.TrimSpace(st.SpecPath) == "" {
		return st, publishOK, 0, true
	}
	parsed, perr := campaign.ParseSpecFile(st.SpecPath)
	if perr != nil {
		return r.applyCampaignPublishPolicyError(st, perr, publishOK, promptModeCompliance, oraclePolicyCompliance, toolDriverCompliance)
	}
	nextState, nextPublishOK := r.applyCampaignPublishPromptCompliance(st, parsed, publishOK, promptModeCompliance)
	return nextState, nextPublishOK, 0, true
}

func (r Runner) applyCampaignPublishPolicyError(st campaign.RunStateV1, perr error, publishOK bool, promptModeCompliance, oraclePolicyCompliance, toolDriverCompliance map[string]any) (campaign.RunStateV1, bool, int, bool) {
	policyPayload, policyErr := campaignPolicyErrorPayload(perr)
	if !policyErr {
		fmt.Fprintf(r.Stderr, codeIO+": %s\n", perr.Error())
		return st, publishOK, 1, false
	}
	publishOK = false
	code, _ := policyPayload["code"].(string)
	if code != "" {
		st.ReasonCodes = dedupeSortedStrings(append(st.ReasonCodes, code))
	}
	applyCampaignPublishPolicyPayload(code, policyPayload, promptModeCompliance, oraclePolicyCompliance, toolDriverCompliance)
	return st, publishOK, 0, true
}

func applyCampaignPublishPolicyPayload(code string, policyPayload, promptModeCompliance, oraclePolicyCompliance, toolDriverCompliance map[string]any) {
	switch code {
	case campaign.ReasonPromptModePolicy, campaign.ReasonExamPromptPolicy:
		promptModeCompliance["ok"] = false
		promptModeCompliance["code"] = code
		promptModeCompliance["error"] = policyPayload["error"]
		promptModeCompliance["promptMode"] = policyPayload["promptMode"]
		promptModeCompliance["violations"] = policyPayload["violations"]
	case campaign.ReasonToolDriverShim:
		toolDriverCompliance["ok"] = false
		toolDriverCompliance["error"] = policyPayload["error"]
		toolDriverCompliance["violation"] = policyPayload["violation"]
	case campaign.ReasonOracleVisibility, campaign.ReasonOracleEvaluator:
		oraclePolicyCompliance["ok"] = false
		oraclePolicyCompliance["code"] = code
		oraclePolicyCompliance["error"] = policyPayload["error"]
		oraclePolicyCompliance["violation"] = policyPayload["violation"]
	default:
		toolDriverCompliance["ok"] = false
		toolDriverCompliance["error"] = policyPayload["error"]
	}
}

func (r Runner) applyCampaignPublishPromptCompliance(st campaign.RunStateV1, parsed campaign.ParsedSpec, publishOK bool, promptModeCompliance map[string]any) (campaign.RunStateV1, bool) {
	violations := campaign.EvaluatePromptModeViolations(parsed)
	promptModeCompliance["promptMode"] = parsed.Spec.PromptMode
	if parsed.Spec.PromptMode == campaign.PromptModeExam {
		promptModeCompliance["code"] = campaign.ReasonExamPromptPolicy
	}
	if len(violations) == 0 {
		return st, publishOK
	}
	publishOK = false
	promptModeCompliance["ok"] = false
	code := campaign.ReasonPromptModePolicy
	if parsed.Spec.PromptMode == campaign.PromptModeExam {
		code = campaign.ReasonExamPromptPolicy
	}
	promptModeCompliance["code"] = code
	promptModeCompliance["violations"] = violations
	promptModeCompliance["error"] = (&campaign.PromptModeViolationError{
		Code:       code,
		PromptMode: parsed.Spec.PromptMode,
		Violations: violations,
	}).Error()
	st.ReasonCodes = dedupeSortedStrings(append(st.ReasonCodes, code))
	return st, publishOK
}

func (r Runner) resolveCampaignRunState(campaignID string, specPath string, outRoot string, jsonOut bool, cmdName string, printHelp func(io.Writer)) (campaign.RunStateV1, int, bool) {
	rawSpec := strings.TrimSpace(specPath)
	if rawSpec != "" {
		return r.resolveCampaignRunStateBySpec(rawSpec, campaignID, outRoot, jsonOut, cmdName, printHelp)
	}
	return r.resolveCampaignRunStateByCampaignID(campaignID, outRoot, jsonOut, cmdName, printHelp)
}

func (r Runner) resolveCampaignRunStateBySpec(rawSpec, campaignID, outRoot string, jsonOut bool, cmdName string, printHelp func(io.Writer)) (campaign.RunStateV1, int, bool) {
	parsed, resolvedOutRoot, err := r.loadCampaignSpec(rawSpec, outRoot)
	if err != nil {
		if exit, handled := r.writeCampaignSpecPolicyError(err, jsonOut); handled {
			return campaign.RunStateV1{}, exit, false
		}
		fmt.Fprintf(r.Stderr, codeIO+": %s\n", err.Error())
		return campaign.RunStateV1{}, 1, false
	}
	cid := parsed.Spec.CampaignID
	if !campaignIDMatchesRequested(campaignID, cid) {
		if printHelp != nil {
			printHelp(r.Stderr)
		}
		return campaign.RunStateV1{}, r.failUsage(cmdName + ": --campaign-id does not match --spec campaignId"), false
	}
	return r.loadCampaignStateWithDriftGuard(campaign.RunStatePath(resolvedOutRoot, cid), jsonOut)
}

func (r Runner) resolveCampaignRunStateByCampaignID(campaignID, outRoot string, jsonOut bool, cmdName string, printHelp func(io.Writer)) (campaign.RunStateV1, int, bool) {
	cid := ids.SanitizeComponent(strings.TrimSpace(campaignID))
	if cid == "" {
		if printHelp != nil {
			printHelp(r.Stderr)
		}
		return campaign.RunStateV1{}, r.failUsage(cmdName + ": missing/invalid --campaign-id (or pass --spec)"), false
	}
	m, err := config.LoadMerged(outRoot)
	if err != nil {
		fmt.Fprintf(r.Stderr, codeIO+": %s\n", err.Error())
		return campaign.RunStateV1{}, 1, false
	}
	return r.loadCampaignStateWithDriftGuard(campaign.RunStatePath(m.OutRoot, cid), jsonOut)
}

func campaignIDMatchesRequested(requested, actual string) bool {
	rawCampaignID := strings.TrimSpace(requested)
	if rawCampaignID == "" {
		return true
	}
	sanitized := ids.SanitizeComponent(rawCampaignID)
	return sanitized != "" && sanitized == actual
}

func (r Runner) loadCampaignStateWithDriftGuard(statePath string, jsonOut bool) (campaign.RunStateV1, int, bool) {
	st, err := campaign.LoadRunState(statePath)
	if err != nil {
		fmt.Fprintf(r.Stderr, codeIO+": %s\n", err.Error())
		return campaign.RunStateV1{}, 1, false
	}
	if msg, drift := campaignStateDriftMessage(st); drift {
		return campaign.RunStateV1{}, r.writeCampaignStateDrift(jsonOut, st.CampaignID, st.RunID, msg), false
	}
	return st, 0, true
}

func (r Runner) runCampaignDoctor(args []string) int {
	opts, exit, ok := r.parseCampaignDoctorOptions(args)
	if !ok {
		return exit
	}
	parsed, resolvedOutRoot, exit, ok := r.loadCampaignSpecForExecution(opts.spec, opts.outRoot, opts.jsonOut)
	if !ok {
		return exit
	}
	res := r.buildCampaignDoctorResult(parsed, resolvedOutRoot)
	r.runCampaignDoctorChecks(parsed, resolvedOutRoot, &res)
	return r.writeCampaignDoctorResult(parsed, res, opts.jsonOut)
}

type campaignDoctorOptions struct {
	spec    string
	outRoot string
	jsonOut bool
}

func (r Runner) parseCampaignDoctorOptions(args []string) (campaignDoctorOptions, int, bool) {
	fs := flag.NewFlagSet("campaign doctor", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	spec := fs.String("spec", "", "campaign spec file (.json|.yaml|.yml) (required)")
	outRoot := fs.String("out-root", "", "project output root (default from config/env, else spec.outRoot, else .zcl)")
	jsonOut := fs.Bool("json", false, "print JSON output")
	help := fs.Bool("help", false, "show help")
	if err := fs.Parse(args); err != nil {
		return campaignDoctorOptions{}, r.failUsage("campaign doctor: invalid flags"), false
	}
	if *help {
		printCampaignDoctorHelp(r.Stdout)
		return campaignDoctorOptions{}, 0, false
	}
	if strings.TrimSpace(*spec) == "" {
		printCampaignDoctorHelp(r.Stderr)
		return campaignDoctorOptions{}, r.failUsage("campaign doctor: missing --spec"), false
	}
	return campaignDoctorOptions{spec: *spec, outRoot: *outRoot, jsonOut: *jsonOut}, 0, true
}

func (r Runner) buildCampaignDoctorResult(parsed campaign.ParsedSpec, resolvedOutRoot string) campaignDoctorResult {
	return campaignDoctorResult{
		OK:            true,
		CampaignID:    parsed.Spec.CampaignID,
		SpecPath:      parsed.SpecPath,
		OutRoot:       resolvedOutRoot,
		ExecutionMode: campaign.ResolveExecutionMode(parsed),
		Checks:        make([]campaignDoctorCheck, 0, 16),
	}
}

func (r Runner) runCampaignDoctorChecks(parsed campaign.ParsedSpec, resolvedOutRoot string, res *campaignDoctorResult) {
	addCheck := func(id string, ok bool, message string) {
		res.Checks = append(res.Checks, campaignDoctorCheck{
			ID:      id,
			OK:      ok,
			Message: strings.TrimSpace(message),
		})
		if !ok {
			res.OK = false
		}
	}
	r.runCampaignDoctorOutRootCheck(resolvedOutRoot, addCheck)
	requiredBins := r.runCampaignDoctorFlowChecks(parsed, addCheck)
	r.runCampaignDoctorRequiredBinaryChecks(requiredBins, addCheck)
	r.runCampaignDoctorLockCheck(parsed, resolvedOutRoot, addCheck)
}

func (r Runner) runCampaignDoctorOutRootCheck(resolvedOutRoot string, addCheck func(string, bool, string)) {
	if err := os.MkdirAll(filepath.Join(resolvedOutRoot, "runs"), 0o755); err != nil {
		addCheck("out_root_write_access", false, err.Error())
		return
	}
	tmp := filepath.Join(resolvedOutRoot, ".campaign-doctor.tmp")
	if err := os.WriteFile(tmp, []byte("ok\n"), 0o644); err != nil {
		addCheck("out_root_write_access", false, err.Error())
		return
	}
	_ = os.Remove(tmp)
	addCheck("out_root_write_access", true, "")
}

func (r Runner) runCampaignDoctorFlowChecks(parsed campaign.ParsedSpec, addCheck func(string, bool, string)) map[string]bool {
	requiredBins := map[string]bool{}
	for _, flow := range parsed.Spec.Flows {
		r.runCampaignDoctorFlowCheck(parsed, flow, requiredBins, addCheck)
	}
	return requiredBins
}

func (r Runner) runCampaignDoctorFlowCheck(parsed campaign.ParsedSpec, flow campaign.FlowSpec, requiredBins map[string]bool, addCheck func(string, bool, string)) {
	if flow.Runner.Type == campaign.RunnerTypeCodexAppSrv {
		r.runCampaignDoctorNativeFlowCheck(flow, addCheck)
		return
	}
	cmd0, ok := validateCampaignDoctorRunnerCommand(flow, addCheck)
	if !ok {
		return
	}
	requiredBins[cmd0] = true
	if _, err := exec.LookPath(cmd0); err != nil {
		addCheck("runner_command_"+flow.FlowID, false, fmt.Sprintf("command not found on PATH: %s", cmd0))
	} else {
		addCheck("runner_command_"+flow.FlowID, true, "")
	}
	scriptBins := r.runCampaignDoctorRunnerScriptCheck(parsed, flow, addCheck)
	for _, bin := range scriptBins {
		requiredBins[bin] = true
	}
}

func (r Runner) runCampaignDoctorNativeFlowCheck(flow campaign.FlowSpec, addCheck func(string, bool, string)) {
	addCheck("runner_command_"+flow.FlowID, true, "native runtime mode (no runner.command required)")
	runtime := codexappserver.NewRuntime(codexappserver.Config{
		Command: codexappserver.DefaultCommandFromEnv(),
	})
	if err := runtime.Probe(context.Background()); err != nil {
		addCheck("native_runtime_"+flow.FlowID, false, err.Error())
	} else {
		addCheck("native_runtime_"+flow.FlowID, true, "")
	}
	if len(flow.Runner.RuntimeStrategies) == 0 {
		return
	}
	if strings.TrimSpace(flow.Runner.RuntimeStrategies[0]) != string(campaign.RunnerTypeCodexAppSrv) {
		addCheck("native_runtime_chain_"+flow.FlowID, false, "first runtime strategy must be codex_app_server for this build")
		return
	}
	addCheck("native_runtime_chain_"+flow.FlowID, true, "")
}

func validateCampaignDoctorRunnerCommand(flow campaign.FlowSpec, addCheck func(string, bool, string)) (string, bool) {
	if len(flow.Runner.Command) == 0 {
		addCheck("runner_command_"+flow.FlowID, false, "runner.command is empty")
		return "", false
	}
	cmd0 := strings.TrimSpace(flow.Runner.Command[0])
	if cmd0 == "" {
		addCheck("runner_command_"+flow.FlowID, false, "runner.command[0] is empty")
		return "", false
	}
	return cmd0, true
}

func (r Runner) runCampaignDoctorRunnerScriptCheck(parsed campaign.ParsedSpec, flow campaign.FlowSpec, addCheck func(string, bool, string)) []string {
	scriptPath := campaignRunnerScriptPath(flow.Runner.Command)
	if strings.TrimSpace(scriptPath) == "" {
		return nil
	}
	resolvedScript := scriptPath
	if !filepath.IsAbs(resolvedScript) {
		resolvedScript = filepath.Clean(filepath.Join(filepath.Dir(parsed.SpecPath), resolvedScript))
	}
	info, err := os.Stat(resolvedScript)
	if err != nil {
		addCheck("runner_script_"+flow.FlowID, false, fmt.Sprintf("script not found: %s", resolvedScript))
		return nil
	}
	if info.Mode().IsRegular() && info.Mode().Perm()&0o111 == 0 {
		addCheck("runner_script_"+flow.FlowID, false, fmt.Sprintf("script not executable: %s", resolvedScript))
		return nil
	}
	addCheck("runner_script_"+flow.FlowID, true, "")
	scriptRaw, err := os.ReadFile(resolvedScript)
	if err != nil {
		return nil
	}
	return detectCampaignScriptRequiredBinaries(string(scriptRaw))
}

func (r Runner) runCampaignDoctorRequiredBinaryChecks(requiredBins map[string]bool, addCheck func(string, bool, string)) {
	required := make([]string, 0, len(requiredBins))
	for bin := range requiredBins {
		bin = strings.TrimSpace(bin)
		if bin == "" || strings.Contains(bin, string(os.PathSeparator)) {
			continue
		}
		required = append(required, bin)
	}
	sort.Strings(required)
	for _, bin := range required {
		if _, err := exec.LookPath(bin); err != nil {
			addCheck("required_binary_"+bin, false, fmt.Sprintf("binary not found on PATH: %s", bin))
		} else {
			addCheck("required_binary_"+bin, true, "")
		}
	}
}

func (r Runner) runCampaignDoctorLockCheck(parsed campaign.ParsedSpec, resolvedOutRoot string, addCheck func(string, bool, string)) {
	lockPath := campaign.LockPath(resolvedOutRoot, parsed.Spec.CampaignID)
	lockInfo, err := os.Stat(lockPath)
	switch {
	case err == nil:
		msg := campaignDoctorLockMessage(r, lockPath, lockInfo.ModTime())
		addCheck("campaign_lock", false, msg)
	case os.IsNotExist(err):
		addCheck("campaign_lock", true, "")
	default:
		addCheck("campaign_lock", false, err.Error())
	}
}

func campaignDoctorLockMessage(r Runner, lockPath string, modTime time.Time) string {
	age := r.Now().Sub(modTime).Round(time.Second)
	msg := fmt.Sprintf("campaign lock is present at %s (age=%s)", lockPath, age)
	if age > 2*time.Minute {
		msg += "; stale_candidate=true"
	}
	ownerPath := filepath.Join(lockPath, "owner.json")
	ownerRaw, readErr := os.ReadFile(ownerPath)
	if readErr != nil {
		return msg
	}
	var owner struct {
		PID       int    `json:"pid"`
		StartedAt string `json:"startedAt"`
	}
	if json.Unmarshal(ownerRaw, &owner) != nil || owner.PID <= 0 {
		return msg
	}
	msg += fmt.Sprintf("; owner.pid=%d", owner.PID)
	if strings.TrimSpace(owner.StartedAt) != "" {
		msg += fmt.Sprintf("; owner.startedAt=%s", owner.StartedAt)
	}
	return msg
}

func (r Runner) writeCampaignDoctorResult(parsed campaign.ParsedSpec, res campaignDoctorResult, jsonOut bool) int {
	if jsonOut {
		writeExit := r.writeJSON(res)
		if writeExit != 0 {
			return writeExit
		}
	} else if res.OK {
		fmt.Fprintf(r.Stdout, "campaign doctor: OK campaign=%s\n", parsed.Spec.CampaignID)
	} else {
		for _, c := range res.Checks {
			if c.OK {
				continue
			}
			fmt.Fprintf(r.Stderr, "campaign doctor: %s: %s\n", c.ID, c.Message)
		}
		fmt.Fprintf(r.Stderr, "campaign doctor: FAIL campaign=%s\n", parsed.Spec.CampaignID)
	}
	if res.OK {
		return 0
	}
	return 2
}

type campaignDoctorCheck struct {
	ID      string `json:"id"`
	OK      bool   `json:"ok"`
	Message string `json:"message,omitempty"`
}

type campaignDoctorResult struct {
	OK            bool                          `json:"ok"`
	CampaignID    string                        `json:"campaignId"`
	SpecPath      string                        `json:"specPath"`
	OutRoot       string                        `json:"outRoot"`
	ExecutionMode campaign.ExecutionModeSummary `json:"executionMode"`
	Checks        []campaignDoctorCheck         `json:"checks"`
}

func campaignStateDriftMessage(st campaign.RunStateV1) (string, bool) {
	specPath := strings.TrimSpace(st.SpecPath)
	if specPath == "" {
		return "", false
	}
	parsed, err := campaign.ParseSpecFile(specPath)
	if err != nil {
		return "", false
	}
	expected := len(parsed.MissionIndexes)
	if expected <= 0 {
		return "", false
	}
	if st.Status == campaign.RunStatusValid && st.TotalMissions == 0 && st.MissionsCompleted == 0 {
		msg := fmt.Sprintf("spec selects %d mission(s), but persisted campaign.run.state reports totalMissions=0 and missionsCompleted=0", expected)
		return msg, true
	}
	return "", false
}

func (r Runner) writeCampaignStateDrift(jsonOut bool, campaignID string, runID string, msg string) int {
	msg = strings.TrimSpace(msg)
	if msg == "" {
		msg = "campaign run-state drift detected"
	}
	if jsonOut {
		out := map[string]any{
			"ok":         false,
			"code":       codeCampaignStateDrift,
			"campaignId": strings.TrimSpace(campaignID),
			"runId":      strings.TrimSpace(runID),
			"error":      msg,
		}
		if exit := r.writeJSON(out); exit != 0 {
			return exit
		}
	} else {
		fmt.Fprintf(r.Stderr, "%s: %s\n", codeCampaignStateDrift, msg)
	}
	return 2
}

func campaignRunnerScriptPath(command []string) string {
	if len(command) == 0 {
		return ""
	}
	cmd0 := strings.TrimSpace(command[0])
	if cmd0 == "" {
		return ""
	}
	base := strings.ToLower(filepath.Base(cmd0))
	if (base == "bash" || base == "sh" || base == "zsh") && len(command) >= 3 {
		switch strings.TrimSpace(command[1]) {
		case "-lc", "-c":
			expr := strings.TrimSpace(command[2])
			if expr == "" {
				return ""
			}
			fields := strings.Fields(expr)
			if len(fields) == 0 {
				return ""
			}
			return strings.Trim(fields[0], `"'`)
		}
	}
	if strings.HasPrefix(cmd0, ".") || strings.Contains(cmd0, string(os.PathSeparator)) || strings.HasSuffix(strings.ToLower(cmd0), ".sh") {
		return cmd0
	}
	return ""
}

func detectCampaignScriptRequiredBinaries(script string) []string {
	candidates := []string{"jq", "npx", "zcl", "pkill", "sed"}
	out := make([]string, 0, len(candidates))
	for _, bin := range candidates {
		if strings.Contains(script, bin) {
			out = append(out, bin)
		}
	}
	return out
}

type campaignExecutionInput struct {
	MissionOffset    int
	MissionIndexes   []int
	Canary           bool
	ResumedFromRunID string
}

type resolvedInvalidRunPolicy struct {
	Statuses             []string
	PublishRequiresValid bool
	ForceFlag            string
}

func (r Runner) executeCampaign(parsed campaign.ParsedSpec, outRoot string, in campaignExecutionInput) (campaign.RunStateV1, int) {
	now := r.Now()
	runID, err := ids.NewRunID(now)
	if err != nil {
		fmt.Fprintf(r.Stderr, codeIO+": %s\n", err.Error())
		return campaign.RunStateV1{}, 1
	}

	if strings.TrimSpace(outRoot) == "" {
		outRoot = ".zcl"
	}
	if len(parsed.MissionIndexes) == 0 {
		fmt.Fprintf(r.Stderr, codeUsage+": campaign requires at least one mission\n")
		return campaign.RunStateV1{}, 2
	}
	missionIndexes := in.MissionIndexes
	if len(missionIndexes) == 0 {
		missionIndexes = parsed.MissionIndexes
	}
	stderrMu := &sync.Mutex{}
	execAdapter, err := runners.NewCampaignExecutor(func(ctx context.Context, flow campaign.FlowSpec, missionIndex int, missionID string) (campaign.FlowRunV1, error) {
		fr, _, runErr := r.runCampaignFlowSuite(ctx, parsed, outRoot, flow, campaignSegment{MissionOffset: missionIndex, TotalMissions: 1}, stderrMu)
		if len(fr.Attempts) == 0 {
			fr.Attempts = []campaign.AttemptStatusV1{{
				MissionIndex: missionIndex,
				MissionID:    missionID,
				Status:       campaign.AttemptStatusInvalid,
				Errors:       []string{codeCampaignMissingAttempt},
			}}
		} else {
			for i := range fr.Attempts {
				fr.Attempts[i].MissionIndex = missionIndex
				if strings.TrimSpace(fr.Attempts[i].MissionID) == "" {
					fr.Attempts[i].MissionID = missionID
				}
			}
		}
		return fr, runErr
	})
	if err != nil {
		fmt.Fprintf(r.Stderr, codeIO+": %s\n", err.Error())
		return campaign.RunStateV1{}, 1
	}
	engineResult, err := campaign.ExecuteMissionEngine(
		parsed,
		execAdapter,
		r.evaluateCampaignGateForMission,
		r.runCampaignHook,
		campaign.EngineOptions{
			OutRoot:                  outRoot,
			RunID:                    runID,
			Canary:                   in.Canary,
			ResumedFromRunID:         strings.TrimSpace(in.ResumedFromRunID),
			MissionIndexes:           missionIndexes,
			MissionOffset:            in.MissionOffset,
			GlobalTimeoutMs:          parsed.Spec.Timeouts.CampaignGlobalTimeoutMs,
			CleanupHookTimeoutMs:     parsed.Spec.Timeouts.CleanupHookTimeoutMs,
			MissionEnvelopeMs:        parsed.Spec.Timeouts.MissionEnvelopeMs,
			WatchdogHeartbeatMs:      parsed.Spec.Timeouts.WatchdogHeartbeatMs,
			WatchdogHardKillContinue: parsed.Spec.Timeouts.WatchdogHardKillContinue,
			LockWait:                 750 * time.Millisecond,
			Now:                      r.Now,
		},
	)
	if err != nil {
		fmt.Fprintf(r.Stderr, codeIO+": %s\n", err.Error())
		return campaign.RunStateV1{}, 1
	}
	if err := r.persistCampaignArtifacts(engineResult.State); err != nil {
		fmt.Fprintf(r.Stderr, codeIO+": %s\n", err.Error())
		return engineResult.State, 1
	}
	return engineResult.State, engineResult.Exit
}

func (r Runner) persistCampaignArtifacts(st campaign.RunStateV1) error {
	rep := campaign.BuildReport(st)
	sum := campaign.BuildSummary(st)
	reportPath, summaryPath, resultsMDPath := resolveCampaignOutputPaths(st)
	if err := store.WriteJSONAtomic(reportPath, rep); err != nil {
		return err
	}
	if err := store.WriteJSONAtomic(summaryPath, sum); err != nil {
		return err
	}
	if err := store.WriteFileAtomic(resultsMDPath, []byte(formatCampaignResultsMarkdown(sum))); err != nil {
		return err
	}
	return nil
}

func resolveCampaignInvalidRunPolicy(st campaign.RunStateV1) resolvedInvalidRunPolicy {
	pol := resolvedInvalidRunPolicy{
		Statuses:             []string{campaign.RunStatusValid, campaign.RunStatusInvalid, campaign.RunStatusAborted},
		PublishRequiresValid: true,
		ForceFlag:            "--force",
	}
	var src campaign.InvalidRunPolicySpec
	if strings.TrimSpace(st.SpecPath) != "" {
		if parsed, err := campaign.ParseSpecFile(st.SpecPath); err == nil {
			switch strings.ToLower(strings.TrimSpace(parsed.Spec.Output.PublishCheck)) {
			case "required":
				pol.PublishRequiresValid = true
			case "advisory", "optional", "off", "disabled":
				pol.PublishRequiresValid = false
			}
			src = parsed.Spec.InvalidRunPolicy
		}
	}
	if len(src.Statuses) > 0 {
		pol.Statuses = dedupeSortedStrings(src.Statuses)
	}
	if src.PublishRequiresValid != nil {
		pol.PublishRequiresValid = *src.PublishRequiresValid
	}
	if strings.TrimSpace(src.ForceFlag) != "" {
		pol.ForceFlag = strings.TrimSpace(src.ForceFlag)
	}
	return pol
}

func resolveCampaignOutputPaths(st campaign.RunStateV1) (reportPath string, summaryPath string, resultsMDPath string) {
	reportPath = campaign.ReportPath(st.OutRoot, st.CampaignID)
	summaryPath = campaign.SummaryPath(st.OutRoot, st.CampaignID)
	resultsMDPath = campaign.ResultsMDPath(st.OutRoot, st.CampaignID)
	if strings.TrimSpace(st.SpecPath) == "" {
		return reportPath, summaryPath, resultsMDPath
	}
	parsed, err := campaign.ParseSpecFile(st.SpecPath)
	if err != nil {
		return reportPath, summaryPath, resultsMDPath
	}
	if strings.TrimSpace(parsed.Spec.Output.ReportPath) != "" {
		reportPath = parsed.Spec.Output.ReportPath
	}
	if strings.TrimSpace(parsed.Spec.Output.SummaryPath) != "" {
		summaryPath = parsed.Spec.Output.SummaryPath
	}
	if strings.TrimSpace(parsed.Spec.Output.ResultsMDPath) != "" {
		resultsMDPath = parsed.Spec.Output.ResultsMDPath
	}
	return reportPath, summaryPath, resultsMDPath
}

func (r Runner) runCampaignHook(ctx context.Context, command string) error {
	cmd := strings.TrimSpace(command)
	if cmd == "" {
		return nil
	}
	argv := []string{"-lc", cmd}
	shell := "bash"
	if runtimeOS := strings.ToLower(strings.TrimSpace(os.Getenv("SHELL"))); runtimeOS == "" {
		// Keep bash default for deterministic behavior in harness docs/tests.
	} else if strings.HasSuffix(runtimeOS, "zsh") {
		shell = "zsh"
	}
	execCmd := exec.CommandContext(ctx, shell, argv...)
	out, err := execCmd.CombinedOutput()
	if err != nil {
		msg := trimText(strings.TrimSpace(string(out)), 512)
		if msg == "" {
			msg = err.Error()
		}
		return fmt.Errorf("hook command failed: %s", msg)
	}
	return nil
}

func (r Runner) evaluateCampaignGateForMission(parsed campaign.ParsedSpec, missionIndex int, missionID string, missionFlowRuns []campaign.FlowRunV1) (campaign.MissionGateV1, error) {
	mg := campaign.MissionGateV1{
		MissionIndex: missionIndex,
		MissionID:    missionID,
		OK:           true,
	}
	for fidx := range missionFlowRuns {
		eval, err := r.evaluateMissionFlowGate(parsed, missionID, &missionFlowRuns[fidx])
		if err != nil {
			return mg, err
		}
		mg.Attempts = append(mg.Attempts, eval.attempt)
		if eval.failMission {
			mg.OK = false
			mg.Reasons = append(mg.Reasons, eval.reasons...)
		}
	}
	mg.Reasons = dedupeSortedStrings(mg.Reasons)
	return mg, nil
}

type missionFlowGateEvaluation struct {
	attempt     campaign.MissionGateAttemptV1
	reasons     []string
	failMission bool
}

func (r Runner) evaluateMissionFlowGate(parsed campaign.ParsedSpec, missionID string, fr *campaign.FlowRunV1) (missionFlowGateEvaluation, error) {
	ma := campaign.MissionGateAttemptV1{
		FlowID: fr.FlowID,
		Status: campaign.AttemptStatusInvalid,
		OK:     false,
	}
	if len(fr.Attempts) == 0 {
		return evaluateMissingMissionAttempt(parsed, ma), nil
	}
	ar := &fr.Attempts[0]
	seedMissionGateAttempt(ar, &ma)
	feedbackSummary := loadAttemptFeedbackSummaryBestEffort(ar.AttemptDir)
	infraDetected, infraCode := inferAttemptInfraFailure(ar, feedbackSummary)
	gateErrors, err := r.collectMissionGateErrors(parsed, fr.FlowID, missionID, ar, feedbackSummary, infraDetected, infraCode)
	if err != nil {
		return missionFlowGateEvaluation{}, err
	}
	return finalizeMissionFlowGate(parsed, ar, ma, gateErrors, infraDetected), nil
}

func evaluateMissingMissionAttempt(parsed campaign.ParsedSpec, ma campaign.MissionGateAttemptV1) missionFlowGateEvaluation {
	ma.Errors = []string{codeCampaignMissingAttempt}
	if parsed.Spec.PairGateEnabled() {
		return missionFlowGateEvaluation{
			attempt:     ma,
			reasons:     []string{codeCampaignMissingAttempt},
			failMission: true,
		}
	}
	return missionFlowGateEvaluation{attempt: ma}
}

func seedMissionGateAttempt(ar *campaign.AttemptStatusV1, ma *campaign.MissionGateAttemptV1) {
	ma.AttemptID = ar.AttemptID
	ma.AttemptDir = ar.AttemptDir
	ma.Status = ar.Status
	ma.Errors = append(ma.Errors, ar.Errors...)
}

func loadAttemptFeedbackSummaryBestEffort(attemptDir string) attemptFeedbackSummary {
	if strings.TrimSpace(attemptDir) == "" {
		return attemptFeedbackSummary{}
	}
	fb, err := readAttemptFeedbackSummary(attemptDir)
	if err != nil {
		return attemptFeedbackSummary{}
	}
	return fb
}

func (r Runner) collectMissionGateErrors(parsed campaign.ParsedSpec, flowID, missionID string, ar *campaign.AttemptStatusV1, feedbackSummary attemptFeedbackSummary, infraDetected bool, infraCode string) ([]string, error) {
	gateErrors := make([]string, 0, 8)
	gateErrors = append(gateErrors, baseMissionGateErrors(parsed, ar, infraDetected, infraCode)...)
	extraErrors, err := r.collectMissionAttemptDirGateErrors(parsed, flowID, ar)
	if err != nil {
		return nil, err
	}
	gateErrors = append(gateErrors, extraErrors...)
	semErrors, err := collectMissionSemanticGateErrors(parsed, ar)
	if err != nil {
		return nil, err
	}
	gateErrors = append(gateErrors, semErrors...)
	gateErrors = append(gateErrors, collectExamProofGateErrors(parsed, feedbackSummary, infraDetected)...)
	oracleErrors, err := r.collectOracleGateErrors(parsed, flowID, missionID, ar, feedbackSummary, infraDetected)
	if err != nil {
		return nil, err
	}
	gateErrors = append(gateErrors, oracleErrors...)
	return gateErrors, nil
}

func baseMissionGateErrors(parsed campaign.ParsedSpec, ar *campaign.AttemptStatusV1, infraDetected bool, infraCode string) []string {
	out := make([]string, 0, 2)
	if infraDetected {
		if strings.TrimSpace(infraCode) != "" {
			out = append(out, strings.TrimSpace(infraCode))
		} else {
			out = append(out, codeCampaignAttemptNotValid)
		}
	}
	if parsed.Spec.PairGateEnabled() && ar.Status != campaign.AttemptStatusValid {
		out = append(out, codeCampaignAttemptNotValid)
	}
	return out
}

func (r Runner) collectMissionAttemptDirGateErrors(parsed campaign.ParsedSpec, flowID string, ar *campaign.AttemptStatusV1) ([]string, error) {
	if strings.TrimSpace(ar.AttemptDir) == "" {
		if parsed.Spec.PairGateEnabled() {
			return []string{codeCampaignArtifactGate}, nil
		}
		return nil, nil
	}
	out := make([]string, 0, 8)
	reportErrors := collectAttemptReportGateErrors(parsed, ar.AttemptDir)
	out = append(out, reportErrors...)
	if parsed.Spec.PairGateEnabled() {
		profileFindings, err := campaign.EvaluateTraceProfile(parsed.Spec.PairGate.TraceProfile, ar.AttemptDir)
		if err != nil {
			return nil, err
		}
		out = append(out, profileFindings...)
	}
	policyFindings, err := campaign.EvaluateToolPolicy(resolveFlowToolPolicy(parsed, flowID), ar.AttemptDir)
	if err != nil {
		return nil, err
	}
	out = append(out, policyFindings...)
	return out, nil
}

func collectAttemptReportGateErrors(parsed campaign.ParsedSpec, attemptDir string) []string {
	if !parsed.Spec.PairGateEnabled() {
		return nil
	}
	rep, err := readAttemptReport(attemptDir)
	if err != nil {
		return []string{codeCampaignArtifactGate}
	}
	out := make([]string, 0, 2)
	if rep.Integrity == nil || !rep.Integrity.TracePresent || !rep.Integrity.TraceNonEmpty || !rep.Integrity.FeedbackPresent {
		out = append(out, codeCampaignTraceGate)
	}
	if rep.TimedOutBeforeFirstToolCall || rep.FailureCodeHistogram[codeTimeout] > 0 {
		out = append(out, codeCampaignTimeoutGate)
	}
	return out
}

func resolveFlowToolPolicy(parsed campaign.ParsedSpec, flowID string) campaign.ToolPolicySpec {
	for _, flow := range parsed.Spec.Flows {
		if flow.FlowID == flowID {
			return flow.ToolPolicy
		}
	}
	return campaign.ToolPolicySpec{}
}

func collectMissionSemanticGateErrors(parsed campaign.ParsedSpec, ar *campaign.AttemptStatusV1) ([]string, error) {
	if !parsed.Spec.Semantic.Enabled {
		return nil, nil
	}
	if strings.TrimSpace(ar.AttemptDir) == "" {
		return []string{campaign.ReasonSemanticFailed}, nil
	}
	semRes, err := semantic.ValidatePath(ar.AttemptDir, semantic.Options{RulesPath: parsed.Spec.Semantic.RulesPath})
	if err != nil {
		return nil, err
	}
	if !semRes.Evaluated || !semRes.OK {
		return []string{campaign.ReasonSemanticFailed}, nil
	}
	return nil, nil
}

func collectExamProofGateErrors(parsed campaign.ParsedSpec, feedbackSummary attemptFeedbackSummary, infraDetected bool) []string {
	if parsed.Spec.PromptMode != campaign.PromptModeExam {
		return nil
	}
	if infraDetected || feedbackSummary.HasValidProof {
		return nil
	}
	return []string{codeCampaignAttemptNotValid}
}

func (r Runner) collectOracleGateErrors(parsed campaign.ParsedSpec, flowID, missionID string, ar *campaign.AttemptStatusV1, feedbackSummary attemptFeedbackSummary, infraDetected bool) ([]string, error) {
	if parsed.Spec.PromptMode != campaign.PromptModeExam || infraDetected || !feedbackSummary.HasValidProof {
		return nil, nil
	}
	oracleVerdict, oracleErr := r.evaluateOracleForAttempt(parsed, flowID, missionID, ar)
	if oracleErr != nil {
		return []string{campaign.ReasonOracleEvalError}, nil
	}
	return oracleFailureReasonCodes(parsed.Spec.Evaluation.OraclePolicy, oracleVerdict), nil
}

func finalizeMissionFlowGate(parsed campaign.ParsedSpec, ar *campaign.AttemptStatusV1, ma campaign.MissionGateAttemptV1, gateErrors []string, infraDetected bool) missionFlowGateEvaluation {
	if len(gateErrors) == 0 {
		ma.OK = true
		ma.Status = campaign.AttemptStatusValid
		ma.Errors = nil
		ar.Status = campaign.AttemptStatusValid
		return missionFlowGateEvaluation{attempt: ma}
	}
	gateErrors = dedupeSortedStrings(gateErrors)
	ma.OK = false
	ma.Errors = dedupeSortedStrings(append(ma.Errors, gateErrors...))
	ar.Errors = dedupeSortedStrings(append(ar.Errors, gateErrors...))
	ma.Status = missionGateAttemptStatus(ar, gateErrors, infraDetected)
	if ma.Status == campaign.AttemptStatusInfraFailed || ma.Status == campaign.AttemptStatusInvalid {
		ar.Status = ma.Status
	}
	hardPolicyFailure := containsString(gateErrors, campaign.ReasonToolPolicy)
	failMission := parsed.Spec.PairGateEnabled() || parsed.Spec.Semantic.Enabled || hardPolicyFailure
	if !failMission {
		return missionFlowGateEvaluation{attempt: ma}
	}
	return missionFlowGateEvaluation{
		attempt:     ma,
		reasons:     gateErrors,
		failMission: true,
	}
}

func missionGateAttemptStatus(ar *campaign.AttemptStatusV1, gateErrors []string, infraDetected bool) string {
	if containsString(gateErrors, codeCampaignTimeoutGate) || infraDetected || ar.Status == campaign.AttemptStatusInfraFailed {
		return campaign.AttemptStatusInfraFailed
	}
	if ar.Status == campaign.AttemptStatusSkipped {
		return campaign.AttemptStatusSkipped
	}
	return campaign.AttemptStatusInvalid
}

func (r Runner) evaluateOracleForAttempt(parsed campaign.ParsedSpec, flowID string, missionID string, ar *campaign.AttemptStatusV1) (oracleEvaluatorOutput, error) {
	out := defaultOracleEvaluatorOutput()
	if !hasOracleAttemptDir(ar) {
		out.Message = "oracle evaluator requires attemptDir"
		return out, nil
	}
	oraclePath, ok := resolveOraclePathForMission(parsed, missionID)
	if !ok {
		out.ReasonCodes = []string{campaign.ReasonOracleEvaluator}
		out.Message = fmt.Sprintf("missing oracle path for mission %q", missionID)
		_, _ = r.writeOracleVerdict(parsed, flowID, missionID, ar, oraclePath, out)
		return out, nil
	}
	if parsed.Spec.Evaluation.Evaluator.Kind == campaign.EvaluatorKindBuiltin {
		return r.evaluateOracleBuiltinForAttempt(parsed, flowID, missionID, ar, oraclePath)
	}
	return r.evaluateOracleCommandForAttempt(parsed, flowID, missionID, ar, oraclePath)
}

func defaultOracleEvaluatorOutput() oracleEvaluatorOutput {
	return oracleEvaluatorOutput{
		OK:          false,
		ReasonCodes: []string{campaign.ReasonOracleEvalError},
	}
}

func hasOracleAttemptDir(ar *campaign.AttemptStatusV1) bool {
	return ar != nil && strings.TrimSpace(ar.AttemptDir) != ""
}

func resolveOraclePathForMission(parsed campaign.ParsedSpec, missionID string) (string, bool) {
	oraclePath := strings.TrimSpace(parsed.OracleByMissionID[missionID])
	return oraclePath, oraclePath != ""
}

func (r Runner) evaluateOracleBuiltinForAttempt(parsed campaign.ParsedSpec, flowID, missionID string, ar *campaign.AttemptStatusV1, oraclePath string) (oracleEvaluatorOutput, error) {
	out := defaultOracleEvaluatorOutput()
	verdict, err := r.evaluateBuiltinOracle(parsed, missionID, ar, oraclePath)
	if err != nil {
		out.ReasonCodes = []string{campaign.ReasonOracleEvalError}
		out.Message = trimText(err.Error(), 1024)
		_, _ = r.writeOracleVerdict(parsed, flowID, missionID, ar, oraclePath, out)
		return out, nil
	}
	verdict.PolicyDisposition = parsed.Spec.Evaluation.OraclePolicy.FormatMismatch
	if !verdict.OK && oracle.AllMismatchesClass(verdict.Mismatches, oracle.MismatchFormat) &&
		parsed.Spec.Evaluation.OraclePolicy.FormatMismatch == campaign.OracleFormatMismatchWarn {
		verdict.Warnings = dedupeSortedStrings(append(verdict.Warnings, "format_only_oracle_mismatch"))
	}
	verdict.ReasonCodes = dedupeSortedStrings(verdict.ReasonCodes)
	verdict.Warnings = dedupeSortedStrings(verdict.Warnings)
	if _, err := r.writeOracleVerdict(parsed, flowID, missionID, ar, oraclePath, verdict); err != nil {
		return verdict, err
	}
	return verdict, nil
}

func (r Runner) evaluateOracleCommandForAttempt(parsed campaign.ParsedSpec, flowID, missionID string, ar *campaign.AttemptStatusV1, oraclePath string) (oracleEvaluatorOutput, error) {
	out := defaultOracleEvaluatorOutput()
	cmdArgs := normalizedOracleEvaluatorCommand(parsed.Spec.Evaluation.Evaluator.Command)
	if len(cmdArgs) == 0 {
		out.ReasonCodes = []string{campaign.ReasonOracleEvaluator}
		out.Message = "missing evaluation.evaluator.command"
		_, _ = r.writeOracleVerdict(parsed, flowID, missionID, ar, oraclePath, out)
		return out, nil
	}
	stdout, stderr, timedOut, err := runOracleEvaluatorCommand(parsed, flowID, missionID, ar, oraclePath, cmdArgs)
	if err != nil {
		out.ReasonCodes = []string{campaign.ReasonOracleEvalError}
		out.Message = oracleEvaluatorRunErrorMessage(stderr, err, timedOut)
		_, _ = r.writeOracleVerdict(parsed, flowID, missionID, ar, oraclePath, out)
		return out, nil
	}
	return r.parseAndPersistOracleEvaluatorOutput(parsed, flowID, missionID, ar, oraclePath, stdout, out)
}

func normalizedOracleEvaluatorCommand(command []string) []string {
	cmdArgs := make([]string, 0, len(command))
	for _, part := range command {
		part = strings.TrimSpace(part)
		if part != "" {
			cmdArgs = append(cmdArgs, part)
		}
	}
	return cmdArgs
}

func runOracleEvaluatorCommand(parsed campaign.ParsedSpec, flowID, missionID string, ar *campaign.AttemptStatusV1, oraclePath string, cmdArgs []string) ([]byte, []byte, bool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, cmdArgs[0], cmdArgs[1:]...)
	cmd.Dir = filepath.Dir(parsed.SpecPath)
	cmd.Env = mergeEnviron(os.Environ(), oracleEvaluatorEnv(parsed, flowID, missionID, ar, oraclePath))
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return stdout.Bytes(), stderr.Bytes(), ctx.Err() == context.DeadlineExceeded, err
}

func oracleEvaluatorEnv(parsed campaign.ParsedSpec, flowID, missionID string, ar *campaign.AttemptStatusV1, oraclePath string) map[string]string {
	return map[string]string{
		"ZCL_EVALUATION_MODE":   parsed.Spec.Evaluation.Mode,
		"ZCL_PROMPT_MODE":       parsed.Spec.PromptMode,
		"ZCL_CAMPAIGN_ID":       parsed.Spec.CampaignID,
		"ZCL_FLOW_ID":           flowID,
		"ZCL_MISSION_ID":        missionID,
		"ZCL_ATTEMPT_ID":        ar.AttemptID,
		"ZCL_ATTEMPT_DIR":       ar.AttemptDir,
		"ZCL_ORACLE_PATH":       oraclePath,
		"ZCL_CAMPAIGN_SPEC":     parsed.SpecPath,
		"ZCL_ORACLE_VISIBILITY": parsed.Spec.MissionSource.OracleSource.Visibility,
	}
}

func oracleEvaluatorRunErrorMessage(stderr []byte, runErr error, timedOut bool) string {
	msg := trimText(strings.TrimSpace(string(stderr)), 1024)
	if msg == "" {
		msg = runErr.Error()
	}
	if timedOut {
		return "oracle evaluator timed out"
	}
	return msg
}

func (r Runner) parseAndPersistOracleEvaluatorOutput(parsed campaign.ParsedSpec, flowID, missionID string, ar *campaign.AttemptStatusV1, oraclePath string, stdout []byte, fallback oracleEvaluatorOutput) (oracleEvaluatorOutput, error) {
	var evaluatorOut oracleEvaluatorOutput
	if err := json.Unmarshal(stdout, &evaluatorOut); err != nil {
		fallback.ReasonCodes = []string{campaign.ReasonOracleEvalError}
		fallback.Message = "oracle evaluator output must be valid json"
		_, _ = r.writeOracleVerdict(parsed, flowID, missionID, ar, oraclePath, fallback)
		return fallback, nil
	}
	evaluatorOut.ReasonCodes = dedupeSortedStrings(evaluatorOut.ReasonCodes)
	evaluatorOut.Message = trimText(strings.TrimSpace(evaluatorOut.Message), 1024)
	evaluatorOut.PolicyDisposition = parsed.Spec.Evaluation.OraclePolicy.FormatMismatch
	normalizeOracleEvaluatorOutput(&evaluatorOut, parsed.Spec.Evaluation.OraclePolicy.Mode)
	if !evaluatorOut.OK && oracle.AllMismatchesClass(evaluatorOut.Mismatches, oracle.MismatchFormat) &&
		parsed.Spec.Evaluation.OraclePolicy.FormatMismatch == campaign.OracleFormatMismatchWarn {
		evaluatorOut.Warnings = dedupeSortedStrings(append(evaluatorOut.Warnings, "format_only_oracle_mismatch"))
	}
	if !evaluatorOut.OK && len(evaluatorOut.ReasonCodes) == 0 {
		evaluatorOut.ReasonCodes = []string{campaign.ReasonOracleEvalFailed}
	}
	if _, err := r.writeOracleVerdict(parsed, flowID, missionID, ar, oraclePath, evaluatorOut); err != nil {
		return evaluatorOut, err
	}
	return evaluatorOut, nil
}

func (r Runner) evaluateBuiltinOracle(parsed campaign.ParsedSpec, missionID string, ar *campaign.AttemptStatusV1, oraclePath string) (oracleEvaluatorOutput, error) {
	out := oracleEvaluatorOutput{
		OK:                false,
		PolicyDisposition: parsed.Spec.Evaluation.OraclePolicy.FormatMismatch,
	}
	file, err := oracle.LoadFile(oraclePath)
	if err != nil {
		out.Message = "invalid oracle file"
		out.ReasonCodes = []string{campaign.ReasonOracleEvalError}
		return out, err
	}
	proof, err := loadOracleProofFromAttempt(ar.AttemptDir)
	if err != nil {
		out.Message = trimText(err.Error(), 1024)
		out.ReasonCodes = []string{campaign.ReasonOracleEvalError}
		return out, nil
	}
	verdict := oracle.EvaluateProof(file, proof, parsed.Spec.Evaluation.OraclePolicy.Mode)
	out.OK = verdict.OK
	out.Message = trimText(strings.TrimSpace(verdict.Message), 1024)
	out.Mismatches = verdict.Mismatches
	if verdict.OK {
		out.ReasonCodes = nil
		return out, nil
	}
	out.ReasonCodes = []string{campaign.ReasonOracleEvalFailed}
	return out, nil
}

func loadOracleProofFromAttempt(attemptDir string) (map[string]any, error) {
	raw, err := os.ReadFile(filepath.Join(strings.TrimSpace(attemptDir), "feedback.json"))
	if err != nil {
		return nil, err
	}
	var fb struct {
		Result     string          `json:"result"`
		ResultJSON json.RawMessage `json:"resultJson"`
	}
	if err := json.Unmarshal(raw, &fb); err != nil {
		return nil, fmt.Errorf("feedback json is invalid")
	}
	if len(fb.ResultJSON) > 0 {
		var proof map[string]any
		if err := json.Unmarshal(fb.ResultJSON, &proof); err != nil {
			return nil, fmt.Errorf("feedback.resultJson must be valid json object")
		}
		return proof, nil
	}
	trimmed := strings.TrimSpace(fb.Result)
	if trimmed == "" {
		return nil, fmt.Errorf("feedback.result is empty")
	}
	var proof map[string]any
	if err := json.Unmarshal([]byte(trimmed), &proof); err != nil {
		return nil, fmt.Errorf("feedback.result must resolve to a JSON object")
	}
	return proof, nil
}

func readAttemptFeedbackSummary(attemptDir string) (attemptFeedbackSummary, error) {
	path := filepath.Join(strings.TrimSpace(attemptDir), "feedback.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		return attemptFeedbackSummary{}, err
	}
	var fb struct {
		OK         *bool           `json:"ok"`
		Result     string          `json:"result"`
		ResultJSON json.RawMessage `json:"resultJson"`
	}
	if err := json.Unmarshal(raw, &fb); err != nil {
		return attemptFeedbackSummary{}, err
	}
	out := attemptFeedbackSummary{Present: true}
	if fb.OK != nil {
		out.OKKnown = true
		out.OK = *fb.OK
	}
	if len(fb.ResultJSON) > 0 {
		var obj map[string]any
		if err := json.Unmarshal(fb.ResultJSON, &obj); err == nil {
			out.HasValidProof = true
			if code, ok := obj["code"].(string); ok {
				out.ResultCode = strings.TrimSpace(code)
			}
			if kind, ok := obj["kind"].(string); ok {
				out.ResultKind = strings.TrimSpace(kind)
			}
		}
		return out, nil
	}
	trimmed := strings.TrimSpace(fb.Result)
	if trimmed == "" {
		return out, nil
	}
	var obj map[string]any
	if err := json.Unmarshal([]byte(trimmed), &obj); err == nil {
		out.HasValidProof = true
	}
	return out, nil
}

func inferAttemptInfraFailure(ar *campaign.AttemptStatusV1, fb attemptFeedbackSummary) (bool, string) {
	if detected, code := inferInfraFailureFromAttempt(ar); detected {
		return true, code
	}
	return inferInfraFailureFromFeedback(fb)
}

func inferInfraFailureFromAttempt(ar *campaign.AttemptStatusV1) (bool, string) {
	if ar == nil {
		return false, ""
	}
	if code := strings.TrimSpace(ar.RunnerErrorCode); code != "" {
		return true, code
	}
	if code := strings.TrimSpace(ar.AutoFeedbackCode); code != "" {
		return true, code
	}
	if ar.Status != campaign.AttemptStatusInfraFailed {
		return false, ""
	}
	for _, code := range ar.Errors {
		code = strings.TrimSpace(code)
		if isInfraFailureCode(code) {
			return true, code
		}
	}
	return true, ""
}

func inferInfraFailureFromFeedback(fb attemptFeedbackSummary) (bool, string) {
	if !fb.Present || !fb.OKKnown || fb.OK {
		return false, ""
	}
	code := strings.TrimSpace(fb.ResultCode)
	if code == "" {
		return false, ""
	}
	if strings.EqualFold(strings.TrimSpace(fb.ResultKind), "infra_failure") || isInfraFailureCode(code) {
		return true, code
	}
	return false, ""
}

func isInfraFailureCode(code string) bool {
	code = strings.TrimSpace(code)
	if code == "" {
		return false
	}
	switch code {
	case codeIO, codeMissingArtifact, codeSpawn, codeTimeout, codeToolFailed:
		return true
	}
	return strings.HasPrefix(code, runtimeCodePrefix())
}

func runtimeCodePrefix() string {
	return strings.TrimSuffix(codes.RuntimeTimeout, "TIMEOUT")
}

func normalizeOracleEvaluatorOutput(out *oracleEvaluatorOutput, policyMode string) {
	if out == nil {
		return
	}
	out.Message = trimText(strings.TrimSpace(out.Message), 1024)
	out.ReasonCodes = dedupeSortedStrings(out.ReasonCodes)
	out.Warnings = dedupeSortedStrings(out.Warnings)
	if len(out.Mismatches) == 0 {
		// Backward compatibility: legacy script evaluators emit a single message.
		if mm := parseOracleMessageMismatch(out.Message); mm != nil {
			out.Mismatches = []oracle.Mismatch{*mm}
		} else if (out.Expected != nil || out.Actual != nil) && !out.OK {
			field := "value"
			mm := oracle.InferMismatch(field, oracle.OpEQ, out.Expected, out.Actual, oracle.PolicyModeStrict)
			if mm != nil {
				out.Mismatches = []oracle.Mismatch{*mm}
			}
		}
	}
}

func parseOracleMessageMismatch(message string) *oracle.Mismatch {
	trimmed := strings.TrimSpace(message)
	if trimmed == "" {
		return nil
	}
	m := oracleExpectedGotRE.FindStringSubmatch(trimmed)
	if len(m) != 4 {
		return nil
	}
	field := strings.TrimSpace(m[1])
	expected := parseOracleMessageLiteral(m[2])
	actual := parseOracleMessageLiteral(m[3])
	mm := oracle.InferMismatch(field, oracle.OpEQ, expected, actual, oracle.PolicyModeStrict)
	if mm == nil {
		return nil
	}
	mm.Message = trimmed
	return mm
}

func parseOracleMessageLiteral(raw string) any {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return raw
	}
	var decoded any
	if err := json.Unmarshal([]byte(raw), &decoded); err == nil {
		return decoded
	}
	if strings.HasPrefix(raw, "\"") && strings.HasSuffix(raw, "\"") {
		if err := json.Unmarshal([]byte(raw), &decoded); err == nil {
			return decoded
		}
	}
	return strings.Trim(raw, "\"")
}

func oracleFailureReasonCodes(policy campaign.OraclePolicySpec, verdict oracleEvaluatorOutput) []string {
	if verdict.OK {
		return nil
	}
	reasonCodes := dedupeSortedStrings(verdict.ReasonCodes)
	if len(reasonCodes) == 0 {
		reasonCodes = []string{campaign.ReasonOracleEvalFailed}
	}
	if !oracle.AllMismatchesClass(verdict.Mismatches, oracle.MismatchFormat) {
		return reasonCodes
	}
	switch strings.TrimSpace(strings.ToLower(policy.FormatMismatch)) {
	case campaign.OracleFormatMismatchWarn:
		return nil
	case campaign.OracleFormatMismatchIgnore:
		return nil
	default:
		return reasonCodes
	}
}

func (r Runner) writeOracleVerdict(parsed campaign.ParsedSpec, flowID string, missionID string, ar *campaign.AttemptStatusV1, oraclePath string, out oracleEvaluatorOutput) (string, error) {
	if ar == nil || strings.TrimSpace(ar.AttemptDir) == "" {
		return "", nil
	}
	now := time.Now().UTC()
	if r.Now != nil {
		now = r.Now().UTC()
	}
	artifact := oracleVerdictArtifact{
		SchemaVersion:     1,
		CampaignID:        parsed.Spec.CampaignID,
		FlowID:            flowID,
		MissionID:         missionID,
		AttemptID:         ar.AttemptID,
		AttemptDir:        ar.AttemptDir,
		OraclePath:        oraclePath,
		EvaluatorKind:     parsed.Spec.Evaluation.Evaluator.Kind,
		EvaluatorCmd:      append([]string{}, parsed.Spec.Evaluation.Evaluator.Command...),
		PromptMode:        parsed.Spec.PromptMode,
		OK:                out.OK,
		ReasonCodes:       dedupeSortedStrings(out.ReasonCodes),
		Message:           trimText(strings.TrimSpace(out.Message), 1024),
		Mismatches:        out.Mismatches,
		PolicyDisposition: strings.TrimSpace(strings.ToLower(out.PolicyDisposition)),
		Warnings:          dedupeSortedStrings(out.Warnings),
		ExecutedAt:        now.Format(time.RFC3339Nano),
	}
	path := filepath.Join(ar.AttemptDir, oracleVerdictFileName)
	if err := store.WriteJSONAtomic(path, artifact); err != nil {
		return path, err
	}
	return path, nil
}

func (r Runner) runCampaignFlowSuite(ctx context.Context, parsed campaign.ParsedSpec, outRoot string, flow campaign.FlowSpec, seg campaignSegment, sharedStderrMu *sync.Mutex) (campaign.FlowRunV1, *suiteRunSummary, error) {
	suiteFile, err := materializeCampaignFlowSuite(parsed, outRoot, flow)
	if err != nil {
		return campaign.FlowRunV1{}, nil, err
	}
	args := buildCampaignFlowSuiteArgs(parsed, outRoot, suiteFile, flow, seg)
	env := buildCampaignFlowSuiteEnv(parsed, flow)
	stdout, stderr, exit, timedOut := r.invokeCampaignFlowSuite(ctx, parsed, args, env, sharedStderrMu)
	fr := buildCampaignFlowRunBase(flow, suiteFile, exit, stderr)
	if timedOut {
		markCampaignFlowTimeout(&fr, seg.MissionOffset)
		return fr, nil, nil
	}
	if exit != 0 {
		fr.Errors = append(fr.Errors, campaignFlowExitCode(exit))
	}
	sum, hasSummary, err := parseSuiteRunSummaryOutput(stdout)
	if err != nil {
		fr.OK = false
		fr.Errors = append(fr.Errors, codeCampaignSummaryParse)
		return fr, nil, fmt.Errorf("flow %s summary parse: %w", flow.FlowID, err)
	}
	if hasSummary {
		applySuiteSummaryToFlowRun(&fr, sum, seg)
		return fr, &sum, nil
	}
	if exit != 0 {
		return fr, nil, fmt.Errorf("flow %s failed before emitting suite summary", flow.FlowID)
	}
	return fr, nil, nil
}

func buildCampaignFlowSuiteArgs(parsed campaign.ParsedSpec, outRoot string, suiteFile string, flow campaign.FlowSpec, seg campaignSegment) []string {
	args := []string{
		"--file", suiteFile,
		"--out-root", outRoot,
		"--campaign-id", parsed.Spec.CampaignID,
		"--session-isolation", flow.Runner.SessionIsolation,
		"--feedback-policy", flow.Runner.FeedbackPolicy,
		"--finalization-mode", flow.Runner.Finalization.Mode,
		"--result-channel", flow.Runner.Finalization.ResultChannel.Kind,
		"--result-min-turn", strconv.Itoa(flow.Runner.Finalization.MinResultTurn),
		"--parallel", "1",
		"--total", strconv.Itoa(seg.TotalMissions),
		"--mission-offset", strconv.Itoa(seg.MissionOffset),
		"--fail-fast=" + strconv.FormatBool(parsed.Spec.FailFast),
		"--json",
	}
	return appendCampaignFlowSuiteOptionalArgs(args, flow)
}

func appendCampaignFlowSuiteOptionalArgs(args []string, flow campaign.FlowSpec) []string {
	if strings.TrimSpace(flow.Runner.Mode) != "" {
		args = append(args, "--mode", strings.TrimSpace(flow.Runner.Mode))
	}
	if flow.Runner.TimeoutMs > 0 {
		args = append(args, "--timeout-ms", strconv.FormatInt(flow.Runner.TimeoutMs, 10))
	}
	if strings.TrimSpace(flow.Runner.TimeoutStart) != "" {
		args = append(args, "--timeout-start", strings.TrimSpace(flow.Runner.TimeoutStart))
	}
	args = appendCampaignFlowSuiteResultChannelArgs(args, flow)
	if flow.Runner.Strict != nil {
		args = append(args, "--strict="+strconv.FormatBool(*flow.Runner.Strict))
	}
	if flow.Runner.StrictExpect != nil {
		args = append(args, "--strict-expect="+strconv.FormatBool(*flow.Runner.StrictExpect))
	}
	for _, shim := range flow.Runner.Shims {
		args = append(args, "--shim", shim)
	}
	if len(flow.Runner.RuntimeStrategies) > 0 {
		args = append(args, "--runtime-strategies", strings.Join(flow.Runner.RuntimeStrategies, ","))
	}
	if strings.TrimSpace(flow.Runner.Model) != "" {
		args = append(args, "--native-model", strings.TrimSpace(flow.Runner.Model))
	}
	if strings.TrimSpace(flow.Runner.ModelReasoningEffort) != "" {
		args = append(args, "--native-model-reasoning-effort", strings.TrimSpace(flow.Runner.ModelReasoningEffort))
	}
	if strings.TrimSpace(flow.Runner.ModelReasoningPolicy) != "" {
		args = append(args, "--native-model-reasoning-policy", strings.TrimSpace(flow.Runner.ModelReasoningPolicy))
	}
	if len(flow.Runner.Command) > 0 {
		args = append(args, "--")
		args = append(args, flow.Runner.Command...)
	}
	return args
}

func appendCampaignFlowSuiteResultChannelArgs(args []string, flow campaign.FlowSpec) []string {
	switch flow.Runner.Finalization.ResultChannel.Kind {
	case campaign.ResultChannelFileJSON:
		if strings.TrimSpace(flow.Runner.Finalization.ResultChannel.Path) != "" {
			args = append(args, "--result-file", strings.TrimSpace(flow.Runner.Finalization.ResultChannel.Path))
		}
	case campaign.ResultChannelStdoutJSON:
		if strings.TrimSpace(flow.Runner.Finalization.ResultChannel.Marker) != "" {
			args = append(args, "--result-marker", strings.TrimSpace(flow.Runner.Finalization.ResultChannel.Marker))
		}
	}
	return args
}

func buildCampaignFlowSuiteEnv(parsed campaign.ParsedSpec, flow campaign.FlowSpec) map[string]string {
	env := map[string]string{}
	for k, v := range flow.Runner.Env {
		if strings.TrimSpace(k) == "" {
			continue
		}
		env[k] = v
	}
	env["ZCL_FLOW_ID"] = flow.FlowID
	env["ZCL_CAMPAIGN_RUNNER_TYPE"] = strings.TrimSpace(flow.Runner.Type)
	env["ZCL_FRESH_AGENT_PER_ATTEMPT"] = "1"
	env["ZCL_TOOL_DRIVER_KIND"] = strings.TrimSpace(flow.Runner.ToolDriver.Kind)
	env[suiteRunEnvRunnerCwdMode] = strings.TrimSpace(flow.Runner.Cwd.Mode)
	if strings.TrimSpace(flow.Runner.Cwd.BasePath) != "" {
		env[suiteRunEnvRunnerCwdBasePath] = strings.TrimSpace(flow.Runner.Cwd.BasePath)
	}
	if strings.TrimSpace(flow.Runner.Cwd.Retain) != "" {
		env[suiteRunEnvRunnerCwdRetain] = strings.TrimSpace(flow.Runner.Cwd.Retain)
	}
	if kind, sourcePath, templatePath := flowPromptMetadata(parsed, flow); kind != "" {
		env["ZCL_PROMPT_SOURCE_KIND"] = kind
		if sourcePath != "" {
			env["ZCL_PROMPT_SOURCE_PATH"] = sourcePath
		}
		if templatePath != "" {
			env["ZCL_PROMPT_TEMPLATE_PATH"] = templatePath
		}
	}
	if strings.TrimSpace(parsed.Spec.PromptMode) != "" && parsed.Spec.PromptMode != campaign.PromptModeDefault {
		env["ZCL_PROMPT_MODE"] = parsed.Spec.PromptMode
	}
	if flow.Runner.MCP.MaxToolCalls > 0 {
		env["ZCL_MCP_MAX_TOOL_CALLS"] = strconv.FormatInt(flow.Runner.MCP.MaxToolCalls, 10)
	}
	if flow.Runner.MCP.IdleTimeoutMs > 0 {
		env["ZCL_MCP_IDLE_TIMEOUT_MS"] = strconv.FormatInt(flow.Runner.MCP.IdleTimeoutMs, 10)
	}
	if flow.Runner.MCP.ShutdownOnComplete {
		env["ZCL_MCP_SHUTDOWN_ON_COMPLETE"] = "1"
	}
	return env
}

func (r Runner) invokeCampaignFlowSuite(ctx context.Context, parsed campaign.ParsedSpec, args []string, env map[string]string, sharedStderrMu *sync.Mutex) ([]byte, string, int, bool) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	stderrTarget := r.Stderr
	if sharedStderrMu != nil && r.Stderr != nil {
		stderrTarget = &lockedWriter{
			mu: sharedStderrMu,
			w:  r.Stderr,
		}
	}
	sub := Runner{
		Version: r.Version,
		Now:     r.Now,
		Stdout:  &stdout,
		Stderr:  io.MultiWriter(stderrTarget, &stderr),
	}
	exit, timedOut := runCampaignSuiteInvocation(ctx, sub, args, env, parsed.Spec.Timeouts.WatchdogHardKillContinue)
	return stdout.Bytes(), stderr.String(), exit, timedOut
}

func buildCampaignFlowRunBase(flow campaign.FlowSpec, suiteFile string, exit int, stderr string) campaign.FlowRunV1 {
	return campaign.FlowRunV1{
		FlowID:      flow.FlowID,
		RunnerType:  flow.Runner.Type,
		SuiteFile:   suiteFile,
		ExitCode:    exit,
		OK:          exit == 0,
		ErrorOutput: trimText(stderr, 4096),
	}
}

func markCampaignFlowTimeout(fr *campaign.FlowRunV1, missionOffset int) {
	fr.OK = false
	fr.Errors = dedupeSortedStrings(append(fr.Errors, codeCampaignTimeoutGate))
	fr.Attempts = []campaign.AttemptStatusV1{{
		MissionIndex: missionOffset,
		Status:       campaign.AttemptStatusInfraFailed,
		Errors:       []string{codeCampaignTimeoutGate},
	}}
}

func parseSuiteRunSummaryOutput(stdout []byte) (suiteRunSummary, bool, error) {
	if strings.TrimSpace(string(stdout)) == "" {
		return suiteRunSummary{}, false, nil
	}
	var sum suiteRunSummary
	if err := json.Unmarshal(stdout, &sum); err != nil {
		return suiteRunSummary{}, true, err
	}
	return sum, true, nil
}

func applySuiteSummaryToFlowRun(fr *campaign.FlowRunV1, sum suiteRunSummary, seg campaignSegment) {
	fr.RunID = sum.RunID
	if !sum.OK {
		fr.OK = false
	}
	fr.Attempts = make([]campaign.AttemptStatusV1, 0, len(sum.Attempts))
	for i, a := range sum.Attempts {
		fr.Attempts = append(fr.Attempts, buildCampaignAttemptStatusFromSummary(sum.RunID, seg, i, a))
	}
}

func buildCampaignAttemptStatusFromSummary(runID string, seg campaignSegment, idx int, a suiteRunAttemptResult) campaign.AttemptStatusV1 {
	ar := campaign.AttemptStatusV1{
		MissionIndex:     seg.MissionOffset + idx,
		MissionID:        a.MissionID,
		AttemptID:        a.AttemptID,
		AttemptDir:       a.AttemptDir,
		RunnerRef:        strings.TrimSpace(runID + ":" + a.AttemptID),
		RunnerErrorCode:  a.RunnerErrorCode,
		AutoFeedbackCode: a.AutoFeedbackCode,
	}
	switch {
	case a.Skipped:
		ar.Status = campaign.AttemptStatusSkipped
		ar.Errors = append(ar.Errors, codeCampaignSkipped)
	case a.RunnerErrorCode != "" || a.AutoFeedbackCode != "":
		ar.Status = campaign.AttemptStatusInfraFailed
	case a.OK && a.Finish.OK:
		ar.Status = campaign.AttemptStatusValid
	default:
		ar.Status = campaign.AttemptStatusInvalid
	}
	if a.RunnerErrorCode != "" {
		ar.Errors = append(ar.Errors, a.RunnerErrorCode)
	}
	if a.AutoFeedbackCode != "" {
		ar.Errors = append(ar.Errors, a.AutoFeedbackCode)
	}
	if !a.Finish.Validate.OK {
		for _, v := range a.Finish.Validate.Errors {
			ar.Errors = append(ar.Errors, v.Code)
		}
	}
	if a.Finish.Expect.Evaluated && !a.Finish.Expect.OK {
		for _, f := range a.Finish.Expect.Failures {
			ar.Errors = append(ar.Errors, f.Code)
		}
	}
	ar.Errors = dedupeSortedStrings(ar.Errors)
	return ar
}

func runCampaignSuiteInvocation(ctx context.Context, sub Runner, args []string, env map[string]string, hardKillContinue bool) (int, bool) {
	if ctx == nil {
		return sub.runSuiteRunWithEnv(args, env), false
	}
	type result struct {
		exit int
	}
	done := make(chan result, 1)
	go func() {
		done <- result{exit: sub.runSuiteRunWithEnv(args, env)}
	}()
	select {
	case out := <-done:
		return out.exit, false
	case <-ctx.Done():
		if hardKillContinue {
			return 1, true
		}
		timer := time.NewTimer(2 * time.Second)
		defer timer.Stop()
		select {
		case out := <-done:
			return out.exit, false
		case <-timer.C:
			// Do not block mission scheduling forever on a non-responsive suite runner.
			return 1, true
		}
	}
}

func materializeCampaignFlowSuite(parsed campaign.ParsedSpec, outRoot string, flow campaign.FlowSpec) (string, error) {
	if strings.TrimSpace(flow.SuiteFile) != "" {
		return flow.SuiteFile, nil
	}
	ps, ok := parsed.FlowSuites[flow.FlowID]
	if !ok {
		return "", fmt.Errorf("flow %s: missing parsed suite", flow.FlowID)
	}
	base := filepath.Join(outRoot, "campaigns", parsed.Spec.CampaignID, "generated-suites")
	if err := os.MkdirAll(base, 0o755); err != nil {
		return "", err
	}
	path := filepath.Join(base, flow.FlowID+".suite.json")
	if err := store.WriteJSONAtomic(path, ps.Suite); err != nil {
		return "", err
	}
	return path, nil
}

func flowPromptMetadata(parsed campaign.ParsedSpec, flow campaign.FlowSpec) (kind string, sourcePath string, templatePath string) {
	sourcePath = strings.TrimSpace(flow.PromptSource.Path)
	templatePath = strings.TrimSpace(flow.PromptTemplate.Path)
	if sourcePath == "" {
		switch {
		case strings.TrimSpace(flow.SuiteFile) != "":
			sourcePath = strings.TrimSpace(flow.SuiteFile)
		case parsed.Spec.PromptMode == campaign.PromptModeExam:
			sourcePath = strings.TrimSpace(parsed.Spec.MissionSource.PromptSource.Path)
		default:
			sourcePath = strings.TrimSpace(parsed.Spec.MissionSource.Path)
		}
	}
	switch {
	case templatePath != "":
		kind = "flow_prompt_template"
	case strings.TrimSpace(flow.PromptSource.Path) != "":
		kind = "flow_prompt_source"
	case strings.TrimSpace(flow.SuiteFile) != "":
		kind = "suite_file"
	case parsed.Spec.PromptMode == campaign.PromptModeExam:
		kind = "campaign_prompt_source"
	default:
		kind = "campaign_mission_source"
	}
	return kind, sourcePath, templatePath
}

func readAttemptReport(attemptDir string) (schema.AttemptReportJSONV1, error) {
	path := filepath.Join(attemptDir, "attempt.report.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		return schema.AttemptReportJSONV1{}, err
	}
	var rep schema.AttemptReportJSONV1
	if err := json.Unmarshal(raw, &rep); err != nil {
		return schema.AttemptReportJSONV1{}, err
	}
	return rep, nil
}

func trimText(s string, max int) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	if max <= 0 || len(s) <= max {
		return s
	}
	return s[:max]
}

func containsString(in []string, target string) bool {
	for _, s := range in {
		if s == target {
			return true
		}
	}
	return false
}

func parseFormatList(v string) map[string]bool {
	out := map[string]bool{}
	for _, part := range strings.Split(v, ",") {
		part = strings.ToLower(strings.TrimSpace(part))
		if part != "" {
			out[part] = true
		}
	}
	if len(out) == 0 {
		out["json"] = true
	}
	return out
}

func formatCampaignResultsMarkdown(sum campaign.SummaryV1) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# RESULTS\n\n")
	fmt.Fprintf(&b, "- campaignId: `%s`\n", sum.CampaignID)
	fmt.Fprintf(&b, "- runId: `%s`\n", sum.RunID)
	fmt.Fprintf(&b, "- status: `%s`\n", sum.Status)
	if len(sum.ReasonCodes) > 0 {
		fmt.Fprintf(&b, "- reasonCodes: `%s`\n", strings.Join(sum.ReasonCodes, "`, `"))
	}
	fmt.Fprintf(&b, "- missionsCompleted: `%d/%d`\n", sum.MissionsCompleted, sum.TotalMissions)
	fmt.Fprintf(&b, "- claimedMissionsOk: `%d`\n", sum.ClaimedMissionsOK)
	fmt.Fprintf(&b, "- verifiedMissionsOk: `%d`\n", sum.VerifiedMissionsOK)
	fmt.Fprintf(&b, "- mismatchCount: `%d`\n", sum.MismatchCount)
	fmt.Fprintf(&b, "- gatesPassed: `%d`\n", sum.GatesPassed)
	fmt.Fprintf(&b, "- gatesFailed: `%d`\n\n", sum.GatesFailed)
	fmt.Fprintf(&b, "- failureBuckets: infra_failed=%d oracle_failed=%d mission_failed=%d\n\n",
		sum.FailureBuckets.InfraFailed,
		sum.FailureBuckets.OracleFailed,
		sum.FailureBuckets.MissionFailed,
	)
	if len(sum.TopFailureCodes) > 0 {
		fmt.Fprintf(&b, "## Top Failure Codes\n\n")
		for _, f := range sum.TopFailureCodes {
			fmt.Fprintf(&b, "- `%s`: %d\n", f.Code, f.Count)
		}
		fmt.Fprintf(&b, "\n")
	}
	if len(sum.Missions) > 0 {
		fmt.Fprintf(&b, "## Per-Mission A/B\n\n")
		for _, m := range sum.Missions {
			fmt.Fprintf(&b, "- `%d:%s` claimed=%v verified=%v mismatch=%v\n", m.MissionIndex, m.MissionID, m.ClaimedOK, m.VerifiedOK, m.Mismatch)
			for _, f := range m.Flows {
				fmt.Fprintf(&b, "  - `%s` status=%s attempt=%s\n", f.FlowID, f.Status, strings.TrimSpace(f.AttemptID))
			}
		}
		fmt.Fprintf(&b, "\n")
	}
	if len(sum.Flows) > 0 {
		fmt.Fprintf(&b, "## Flows\n\n")
		for _, f := range sum.Flows {
			fmt.Fprintf(&b, "- `%s` (%s): attempts=%d valid=%d invalid=%d skipped=%d infra_failed=%d oracle_failed=%d mission_failed=%d\n",
				f.FlowID, f.RunnerType, f.AttemptsTotal, f.Valid, f.Invalid, f.Skipped, f.InfraFailed, f.OracleFailed, f.MissionFailed)
		}
		fmt.Fprintf(&b, "\n")
	}
	fmt.Fprintf(&b, "## Evidence Paths\n\n")
	fmt.Fprintf(&b, "- runState: `%s`\n", sum.EvidencePaths.RunStatePath)
	fmt.Fprintf(&b, "- report: `%s`\n", sum.EvidencePaths.ReportPath)
	fmt.Fprintf(&b, "- summary: `%s`\n", sum.EvidencePaths.SummaryPath)
	fmt.Fprintf(&b, "- resultsMd: `%s`\n", sum.EvidencePaths.ResultsMDPath)
	for _, p := range sum.EvidencePaths.AttemptDirs {
		fmt.Fprintf(&b, "- attemptDir: `%s`\n", p)
	}
	return b.String()
}

func campaignPolicyErrorPayload(err error) (map[string]any, bool) {
	if err == nil {
		return nil, false
	}
	var promptErr *campaign.PromptModeViolationError
	if errors.As(err, &promptErr) {
		code := strings.TrimSpace(promptErr.Code)
		if code == "" {
			code = campaign.ReasonPromptModePolicy
			if promptErr.PromptMode == campaign.PromptModeExam {
				code = campaign.ReasonExamPromptPolicy
			}
		}
		return map[string]any{
			"ok":         false,
			"code":       code,
			"promptMode": promptErr.PromptMode,
			"violations": promptErr.Violations,
			"error":      promptErr.Error(),
		}, true
	}
	var shimErr *campaign.ToolDriverShimRequirementError
	if errors.As(err, &shimErr) {
		return map[string]any{
			"ok":        false,
			"code":      campaign.ReasonToolDriverShim,
			"violation": shimErr.Violation,
			"error":     shimErr.Error(),
		}, true
	}
	var toolPolicyErr *campaign.ToolPolicyConfigError
	if errors.As(err, &toolPolicyErr) {
		code := strings.TrimSpace(toolPolicyErr.Code)
		if code == "" {
			code = campaign.ReasonToolPolicyConfig
		}
		return map[string]any{
			"ok":        false,
			"code":      code,
			"violation": toolPolicyErr.Violation,
			"error":     toolPolicyErr.Error(),
		}, true
	}
	var oracleErr *campaign.OraclePolicyViolationError
	if errors.As(err, &oracleErr) {
		return map[string]any{
			"ok":        false,
			"code":      oracleErr.Code,
			"violation": oracleErr.Violation,
			"error":     oracleErr.Error(),
		}, true
	}
	return nil, false
}

func (r Runner) writeCampaignSpecPolicyError(err error, jsonOut bool) (int, bool) {
	payload, ok := campaignPolicyErrorPayload(err)
	if !ok {
		return 0, false
	}
	code, _ := payload["code"].(string)
	msg, _ := payload["error"].(string)
	if jsonOut {
		if exit := r.writeJSON(payload); exit != 0 {
			return exit, true
		}
	} else {
		fmt.Fprintf(r.Stderr, "%s: %s\n", code, msg)
	}
	return 2, true
}

func (r Runner) loadCampaignSpec(specPath string, outRoot string) (campaign.ParsedSpec, string, error) {
	absSpec, err := filepath.Abs(strings.TrimSpace(specPath))
	if err != nil {
		return campaign.ParsedSpec{}, "", err
	}
	parsed, err := campaign.ParseSpecFile(absSpec)
	if err != nil {
		return campaign.ParsedSpec{}, "", err
	}
	m, err := config.LoadMerged(outRoot)
	if err != nil {
		return campaign.ParsedSpec{}, "", err
	}
	resolvedOutRoot := m.OutRoot
	if strings.TrimSpace(parsed.Spec.OutRoot) != "" && strings.TrimSpace(outRoot) == "" {
		resolvedOutRoot = strings.TrimSpace(parsed.Spec.OutRoot)
	}
	if strings.TrimSpace(resolvedOutRoot) == "" {
		resolvedOutRoot = ".zcl"
	}
	return parsed, resolvedOutRoot, nil
}

func (r Runner) runMission(args []string) int {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" || args[0] == "help" {
		printMissionHelp(r.Stdout)
		return 0
	}
	switch args[0] {
	case "prompts":
		return r.runMissionPrompts(args[1:])
	default:
		fmt.Fprintf(r.Stderr, codeUsage+": unknown mission subcommand %q\n", args[0])
		printMissionHelp(r.Stderr)
		return 2
	}
}

func (r Runner) runMissionPrompts(args []string) int {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" || args[0] == "help" {
		printMissionPromptsHelp(r.Stdout)
		return 0
	}
	switch args[0] {
	case "build":
		return r.runMissionPromptsBuild(args[1:])
	default:
		fmt.Fprintf(r.Stderr, codeUsage+": unknown mission prompts subcommand %q\n", args[0])
		printMissionPromptsHelp(r.Stderr)
		return 2
	}
}

func (r Runner) runMissionPromptsBuild(args []string) int {
	opts, exit, ok := r.parseMissionPromptsBuildOptions(args)
	if !ok {
		return exit
	}
	parsed, resolvedOutRoot, tpl, absSpec, absTemplate, exit, ok := r.loadMissionPromptsBuildInputs(opts.spec, opts.template, opts.outRoot)
	if !ok {
		return exit
	}
	prompts := buildMissionPromptArtifacts(parsed, tpl)
	result := buildMissionPromptsBuildResult(parsed, resolvedOutRoot, absSpec, absTemplate, opts.out, tpl, prompts)
	if err := store.WriteJSONAtomic(result.OutPath, result); err != nil {
		fmt.Fprintf(r.Stderr, codeIO+": %s\n", err.Error())
		return 1
	}
	if opts.jsonOut {
		return r.writeJSON(result)
	}
	fmt.Fprintf(r.Stdout, "mission prompts build: OK %s\n", result.OutPath)
	return 0
}

type missionPromptsBuildOptions struct {
	spec     string
	template string
	out      string
	outRoot  string
	jsonOut  bool
}

func (r Runner) parseMissionPromptsBuildOptions(args []string) (missionPromptsBuildOptions, int, bool) {
	fs := flag.NewFlagSet("mission prompts build", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	spec := fs.String("spec", "", "campaign spec file (.json|.yaml|.yml) (required)")
	template := fs.String("template", "", "prompt template file (required)")
	out := fs.String("out", "", "output artifact path (default <outRoot>/campaigns/<campaignId>/mission.prompts.json)")
	outRoot := fs.String("out-root", "", "project output root (default from config/env, else spec.outRoot, else .zcl)")
	jsonOut := fs.Bool("json", false, "print JSON output")
	help := fs.Bool("help", false, "show help")

	if err := fs.Parse(args); err != nil {
		return missionPromptsBuildOptions{}, r.failUsage("mission prompts build: invalid flags"), false
	}
	if *help {
		printMissionPromptsBuildHelp(r.Stdout)
		return missionPromptsBuildOptions{}, 0, false
	}
	if strings.TrimSpace(*spec) == "" {
		printMissionPromptsBuildHelp(r.Stderr)
		return missionPromptsBuildOptions{}, r.failUsage("mission prompts build: missing --spec"), false
	}
	if strings.TrimSpace(*template) == "" {
		printMissionPromptsBuildHelp(r.Stderr)
		return missionPromptsBuildOptions{}, r.failUsage("mission prompts build: missing --template"), false
	}
	return missionPromptsBuildOptions{
		spec:     *spec,
		template: *template,
		out:      *out,
		outRoot:  *outRoot,
		jsonOut:  *jsonOut,
	}, 0, true
}

func (r Runner) loadMissionPromptsBuildInputs(spec, template, outRoot string) (campaign.ParsedSpec, string, string, string, string, int, bool) {
	parsed, resolvedOutRoot, err := r.loadCampaignSpec(spec, outRoot)
	if err != nil {
		fmt.Fprintf(r.Stderr, codeIO+": %s\n", err.Error())
		return campaign.ParsedSpec{}, "", "", "", "", 1, false
	}
	templateRaw, err := os.ReadFile(template)
	if err != nil {
		fmt.Fprintf(r.Stderr, codeIO+": %s\n", err.Error())
		return campaign.ParsedSpec{}, "", "", "", "", 1, false
	}
	absSpec, _ := filepath.Abs(spec)
	absTemplate, _ := filepath.Abs(template)
	return parsed, resolvedOutRoot, string(templateRaw), absSpec, absTemplate, 0, true
}

func buildMissionPromptArtifacts(parsed campaign.ParsedSpec, tpl string) []missionPromptArtifactV1 {
	flowIDs := make([]string, 0, len(parsed.FlowSuites))
	for _, f := range parsed.Spec.Flows {
		flowIDs = append(flowIDs, f.FlowID)
	}
	sort.Strings(flowIDs)

	var prompts []missionPromptArtifactV1
	for _, flowID := range flowIDs {
		ps := parsed.FlowSuites[flowID]
		for _, idx := range parsed.MissionIndexes {
			if idx < 0 || idx >= len(ps.Suite.Missions) {
				continue
			}
			m := ps.Suite.Missions[idx]
			rendered := applyPromptTemplate(tpl, map[string]string{
				"campaignId":   parsed.Spec.CampaignID,
				"flowId":       flowID,
				"suiteId":      ps.Suite.SuiteID,
				"missionId":    m.MissionID,
				"missionIndex": strconv.Itoa(idx),
				"prompt":       m.Prompt,
				"tagsCsv":      strings.Join(m.Tags, ","),
			})
			promptID := stablePromptID(parsed.Spec.CampaignID, flowID, ps.Suite.SuiteID, m.MissionID, idx, rendered)
			prompts = append(prompts, missionPromptArtifactV1{
				ID:           promptID,
				FlowID:       flowID,
				SuiteID:      ps.Suite.SuiteID,
				MissionID:    m.MissionID,
				MissionIndex: idx,
				Prompt:       rendered,
			})
		}
	}
	return prompts
}

func buildMissionPromptsBuildResult(parsed campaign.ParsedSpec, resolvedOutRoot, absSpec, absTemplate, outPath, tpl string, prompts []missionPromptArtifactV1) missionPromptsBuildResult {
	outPath = strings.TrimSpace(outPath)
	if outPath == "" {
		outPath = filepath.Join(resolvedOutRoot, "campaigns", parsed.Spec.CampaignID, "mission.prompts.json")
	}
	createdAt := deterministicBuildTimestamp(absSpec, absTemplate, tpl, prompts)
	return missionPromptsBuildResult{
		SchemaVersion: 1,
		CampaignID:    parsed.Spec.CampaignID,
		SpecPath:      absSpec,
		TemplatePath:  absTemplate,
		OutPath:       outPath,
		CreatedAt:     createdAt,
		Prompts:       prompts,
	}
}

func applyPromptTemplate(tpl string, vars map[string]string) string {
	out := tpl
	keys := make([]string, 0, len(vars))
	for k := range vars {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		out = strings.ReplaceAll(out, "{{"+k+"}}", vars[k])
	}
	return out
}

func stablePromptID(campaignID string, flowID string, suiteID string, missionID string, missionIndex int, prompt string) string {
	seed := strings.Join([]string{
		strings.TrimSpace(campaignID),
		strings.TrimSpace(flowID),
		strings.TrimSpace(suiteID),
		strings.TrimSpace(missionID),
		strconv.Itoa(missionIndex),
		prompt,
	}, "\n")
	sum := sha256.Sum256([]byte(seed))
	base := ids.SanitizeComponent(fmt.Sprintf("%s-%s-%03d-%x", flowID, missionID, missionIndex, sum[:6]))
	if base == "" {
		return fmt.Sprintf("prompt-%x", sum[:8])
	}
	return base
}

func deterministicBuildTimestamp(specPath string, templatePath string, template string, prompts []missionPromptArtifactV1) string {
	h := sha256.New()
	_, _ = io.WriteString(h, strings.TrimSpace(specPath))
	_, _ = io.WriteString(h, "\n")
	_, _ = io.WriteString(h, strings.TrimSpace(templatePath))
	_, _ = io.WriteString(h, "\n")
	_, _ = io.WriteString(h, template)
	_, _ = io.WriteString(h, "\n")
	for _, p := range prompts {
		_, _ = io.WriteString(h, p.ID)
		_, _ = io.WriteString(h, "\n")
		_, _ = io.WriteString(h, p.Prompt)
		_, _ = io.WriteString(h, "\n")
	}
	sum := h.Sum(nil)
	var sec int64
	for i := 0; i < 8 && i < len(sum); i++ {
		sec = (sec << 8) | int64(sum[i])
	}
	if sec < 0 {
		sec = -sec
	}
	// Keep deterministic timestamps in a bounded modern range.
	const window = int64(40 * 365 * 24 * 60 * 60)
	base := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC).Unix()
	return time.Unix(base+(sec%window), 0).UTC().Format(time.RFC3339)
}

func printCampaignHelp(w io.Writer) {
	fmt.Fprint(w, `Usage:
  zcl campaign lint --spec <campaign.(yaml|yml|json)> [--json]
  zcl campaign run --spec <campaign.(yaml|yml|json)> [--missions N] [--mission-offset N] [--json]
  zcl campaign canary --spec <campaign.(yaml|yml|json)> [--missions N] [--mission-offset N] [--json]
  zcl campaign resume --campaign-id <id> [--json]
  zcl campaign status --campaign-id <id> [--json]
  zcl campaign report [--campaign-id <id> | --spec <campaign.(yaml|yml|json)>] [--format json,md] [--allow-invalid] [--force] [--json]
  zcl campaign publish-check [--campaign-id <id> | --spec <campaign.(yaml|yml|json)>] [--force] [--json]
  zcl campaign doctor --spec <campaign.(yaml|yml|json)> [--json]
`)
}

func printCampaignLintHelp(w io.Writer) {
	fmt.Fprint(w, `Usage:
  zcl campaign lint --spec <campaign.(yaml|yml|json)> [--out-root .zcl] [--json]
`)
}

func printCampaignRunHelp(w io.Writer) {
	fmt.Fprint(w, `Usage:
  zcl campaign run --spec <campaign.(yaml|yml|json)> [--out-root .zcl] [--missions N] [--mission-offset N] [--json]
`)
}

func printCampaignCanaryHelp(w io.Writer) {
	fmt.Fprint(w, `Usage:
  zcl campaign canary --spec <campaign.(yaml|yml|json)> [--out-root .zcl] [--missions N] [--mission-offset N] [--json]
`)
}

func printCampaignResumeHelp(w io.Writer) {
	fmt.Fprint(w, `Usage:
  zcl campaign resume --campaign-id <id> [--out-root .zcl] [--json]
`)
}

func printCampaignStatusHelp(w io.Writer) {
	fmt.Fprint(w, `Usage:
  zcl campaign status --campaign-id <id> [--out-root .zcl] [--json]
`)
}

func printCampaignReportHelp(w io.Writer) {
	fmt.Fprint(w, `Usage:
  zcl campaign report [--campaign-id <id> | --spec <campaign.(yaml|yml|json)>] [--out-root .zcl] [--format json,md] [--allow-invalid] [--force] [--json]
`)
}

func printCampaignPublishCheckHelp(w io.Writer) {
	fmt.Fprint(w, `Usage:
  zcl campaign publish-check [--campaign-id <id> | --spec <campaign.(yaml|yml|json)>] [--out-root .zcl] [--force] [--json]
`)
}

func printCampaignDoctorHelp(w io.Writer) {
	fmt.Fprint(w, `Usage:
  zcl campaign doctor --spec <campaign.(yaml|yml|json)> [--out-root .zcl] [--json]
`)
}

func printMissionHelp(w io.Writer) {
	fmt.Fprint(w, `Usage:
  zcl mission prompts build --spec <campaign.(yaml|yml|json)> --template <template.txt|md> [--out <path>] [--json]
`)
}

func printMissionPromptsHelp(w io.Writer) {
	fmt.Fprint(w, `Usage:
  zcl mission prompts build --spec <campaign.(yaml|yml|json)> --template <template.txt|md> [--out <path>] [--json]
`)
}

func printMissionPromptsBuildHelp(w io.Writer) {
	fmt.Fprint(w, `Usage:
  zcl mission prompts build --spec <campaign.(yaml|yml|json)> --template <template.txt|md> [--out <path>] [--out-root .zcl] [--json]

Template placeholders:
  {{campaignId}} {{flowId}} {{suiteId}} {{missionId}} {{missionIndex}} {{prompt}} {{tagsCsv}}
`)
}
