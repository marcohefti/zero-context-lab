package cli

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/marcohefti/zero-context-lab/internal/campaign"
	"github.com/marcohefti/zero-context-lab/internal/config"
	"github.com/marcohefti/zero-context-lab/internal/ids"
	"github.com/marcohefti/zero-context-lab/internal/runners"
	"github.com/marcohefti/zero-context-lab/internal/schema"
	"github.com/marcohefti/zero-context-lab/internal/semantic"
	"github.com/marcohefti/zero-context-lab/internal/store"
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
	default:
		fmt.Fprintf(r.Stderr, "ZCL_E_USAGE: unknown campaign subcommand %q\n", args[0])
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
		fmt.Fprintf(r.Stderr, "ZCL_E_IO: %s\n", err.Error())
		return 1
	}
	out := map[string]any{
		"ok":            true,
		"schemaVersion": parsed.Spec.SchemaVersion,
		"campaignId":    parsed.Spec.CampaignID,
		"specPath":      parsed.SpecPath,
		"outRoot":       resolvedOutRoot,
		"flows":         len(parsed.Spec.Flows),
		"execution": map[string]any{
			"flowMode": parsed.Spec.Execution.FlowMode,
		},
		"missions": map[string]any{
			"selectedTotal": len(parsed.MissionIndexes),
			"selectionMode": parsed.Spec.MissionSource.Selection.Mode,
			"indexes":       parsed.MissionIndexes,
		},
		"pairGate": map[string]any{
			"enabled":                   parsed.Spec.PairGateEnabled(),
			"stopOnFirstMissionFailure": parsed.Spec.PairGate.StopOnFirstMissionFailure,
			"traceProfile":              parsed.Spec.PairGate.TraceProfile,
		},
		"semantic": map[string]any{
			"enabled":   parsed.Spec.Semantic.Enabled,
			"rulesPath": parsed.Spec.Semantic.RulesPath,
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
	fs := flag.NewFlagSet("campaign run", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	spec := fs.String("spec", "", "campaign spec file (.json|.yaml|.yml) (required)")
	outRoot := fs.String("out-root", "", "project output root (default from config/env, else spec.outRoot, else .zcl)")
	missions := fs.Int("missions", 0, "optional mission count override (default spec.totalMissions)")
	missionOffset := fs.Int("mission-offset", 0, "0-based mission offset (default 0)")
	jsonOut := fs.Bool("json", false, "print JSON output")
	help := fs.Bool("help", false, "show help")

	if err := fs.Parse(args); err != nil {
		return r.failUsage("campaign run: invalid flags")
	}
	if *help {
		printCampaignRunHelp(r.Stdout)
		return 0
	}
	if strings.TrimSpace(*spec) == "" {
		printCampaignRunHelp(r.Stderr)
		return r.failUsage("campaign run: missing --spec")
	}
	if *missionOffset < 0 {
		return r.failUsage("campaign run: --mission-offset must be >= 0")
	}
	if *missions < 0 {
		return r.failUsage("campaign run: --missions must be >= 0")
	}

	parsed, resolvedOutRoot, err := r.loadCampaignSpec(*spec, *outRoot)
	if err != nil {
		fmt.Fprintf(r.Stderr, "ZCL_E_IO: %s\n", err.Error())
		return 1
	}
	total := *missions
	if total == 0 {
		total = parsed.Spec.TotalMissions
	}
	if total <= 0 {
		total = len(parsed.MissionIndexes)
	}
	indexes, err := campaign.WindowMissionIndexes(parsed.MissionIndexes, *missionOffset, total)
	if err != nil {
		return r.failUsage("campaign run: " + err.Error())
	}
	if len(indexes) == 0 {
		return r.failUsage("campaign run: no missions to run")
	}

	st, exit := r.executeCampaign(parsed, resolvedOutRoot, campaignExecutionInput{
		TotalMissions:  total,
		MissionOffset:  *missionOffset,
		MissionIndexes: indexes,
		Canary:         false,
	})
	if *jsonOut {
		writeExit := r.writeJSON(st)
		if writeExit != 0 {
			return writeExit
		}
	}
	if !*jsonOut {
		fmt.Fprintf(r.Stdout, "campaign run: %s (%s)\n", st.Status, st.RunID)
	}
	return exit
}

func (r Runner) runCampaignCanary(args []string) int {
	fs := flag.NewFlagSet("campaign canary", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	spec := fs.String("spec", "", "campaign spec file (.json|.yaml|.yml) (required)")
	outRoot := fs.String("out-root", "", "project output root (default from config/env, else spec.outRoot, else .zcl)")
	missions := fs.Int("missions", 0, "canary mission count (default spec.canaryMissions, else 3)")
	missionOffset := fs.Int("mission-offset", 0, "0-based mission offset (default 0)")
	jsonOut := fs.Bool("json", false, "print JSON output")
	help := fs.Bool("help", false, "show help")

	if err := fs.Parse(args); err != nil {
		return r.failUsage("campaign canary: invalid flags")
	}
	if *help {
		printCampaignCanaryHelp(r.Stdout)
		return 0
	}
	if strings.TrimSpace(*spec) == "" {
		printCampaignCanaryHelp(r.Stderr)
		return r.failUsage("campaign canary: missing --spec")
	}
	if *missionOffset < 0 {
		return r.failUsage("campaign canary: --mission-offset must be >= 0")
	}
	if *missions < 0 {
		return r.failUsage("campaign canary: --missions must be >= 0")
	}

	parsed, resolvedOutRoot, err := r.loadCampaignSpec(*spec, *outRoot)
	if err != nil {
		fmt.Fprintf(r.Stderr, "ZCL_E_IO: %s\n", err.Error())
		return 1
	}
	total := *missions
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
		return r.failUsage("campaign canary: no missions to run")
	}
	indexes, err := campaign.WindowMissionIndexes(parsed.MissionIndexes, *missionOffset, total)
	if err != nil {
		return r.failUsage("campaign canary: " + err.Error())
	}
	if len(indexes) == 0 {
		return r.failUsage("campaign canary: no missions to run")
	}

	st, exit := r.executeCampaign(parsed, resolvedOutRoot, campaignExecutionInput{
		TotalMissions:  total,
		MissionOffset:  *missionOffset,
		MissionIndexes: indexes,
		Canary:         true,
	})
	if *jsonOut {
		writeExit := r.writeJSON(st)
		if writeExit != 0 {
			return writeExit
		}
	}
	if !*jsonOut {
		fmt.Fprintf(r.Stdout, "campaign canary: %s (%s)\n", st.Status, st.RunID)
	}
	return exit
}

func (r Runner) runCampaignResume(args []string) int {
	fs := flag.NewFlagSet("campaign resume", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	campaignID := fs.String("campaign-id", "", "campaign id (required)")
	outRoot := fs.String("out-root", "", "project output root (default from config/env, else state.outRoot)")
	jsonOut := fs.Bool("json", false, "print JSON output")
	help := fs.Bool("help", false, "show help")

	if err := fs.Parse(args); err != nil {
		return r.failUsage("campaign resume: invalid flags")
	}
	if *help {
		printCampaignResumeHelp(r.Stdout)
		return 0
	}
	cid := ids.SanitizeComponent(strings.TrimSpace(*campaignID))
	if cid == "" {
		printCampaignResumeHelp(r.Stderr)
		return r.failUsage("campaign resume: missing/invalid --campaign-id")
	}

	m, err := config.LoadMerged(*outRoot)
	if err != nil {
		fmt.Fprintf(r.Stderr, "ZCL_E_IO: %s\n", err.Error())
		return 1
	}
	resolvedOutRoot := m.OutRoot
	statePath := campaign.RunStatePath(resolvedOutRoot, cid)
	st, err := campaign.LoadRunState(statePath)
	if err != nil {
		fmt.Fprintf(r.Stderr, "ZCL_E_IO: %s\n", err.Error())
		return 1
	}
	if strings.TrimSpace(st.OutRoot) != "" && strings.TrimSpace(*outRoot) == "" {
		resolvedOutRoot = st.OutRoot
		statePath = campaign.RunStatePath(resolvedOutRoot, cid)
		st, err = campaign.LoadRunState(statePath)
		if err != nil {
			fmt.Fprintf(r.Stderr, "ZCL_E_IO: %s\n", err.Error())
			return 1
		}
	}
	if strings.TrimSpace(st.SpecPath) == "" {
		return r.failUsage("campaign resume: existing campaign state is missing specPath")
	}
	parsed, resolvedOutRoot, err := r.loadCampaignSpec(st.SpecPath, resolvedOutRoot)
	if err != nil {
		fmt.Fprintf(r.Stderr, "ZCL_E_IO: %s\n", err.Error())
		return 1
	}
	if parsed.Spec.CampaignID != cid {
		return r.failUsage("campaign resume: campaign-id does not match stored spec")
	}
	missionCount := len(parsed.MissionIndexes)
	if missionCount == 0 {
		return r.failUsage("campaign resume: spec has no missions")
	}
	remaining := st.TotalMissions - st.MissionsCompleted
	if remaining <= 0 {
		// There may still be pending missions not reflected by counters; engine will reconcile against progress ledger.
		remaining = 0
	}

	next, exit := r.executeCampaign(parsed, resolvedOutRoot, campaignExecutionInput{
		TotalMissions:    remaining,
		MissionOffset:    0,
		MissionIndexes:   parsed.MissionIndexes,
		Canary:           false,
		ResumedFromRunID: st.RunID,
	})
	if *jsonOut {
		writeExit := r.writeJSON(next)
		if writeExit != 0 {
			return writeExit
		}
	}
	if !*jsonOut {
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
		fmt.Fprintf(r.Stderr, "ZCL_E_IO: %s\n", err.Error())
		return 1
	}
	st, err := campaign.LoadRunState(campaign.RunStatePath(m.OutRoot, cid))
	if err != nil {
		fmt.Fprintf(r.Stderr, "ZCL_E_IO: %s\n", err.Error())
		return 1
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

	campaignID := fs.String("campaign-id", "", "campaign id (required)")
	outRoot := fs.String("out-root", "", "project output root (default from config/env, else .zcl)")
	format := fs.String("format", "json", "output format list: json,md")
	force := fs.Bool("force", false, "allow report export when campaign status is invalid|aborted")
	jsonOut := fs.Bool("json", false, "print JSON output")
	help := fs.Bool("help", false, "show help")

	if err := fs.Parse(args); err != nil {
		return r.failUsage("campaign report: invalid flags")
	}
	if *help {
		printCampaignReportHelp(r.Stdout)
		return 0
	}
	cid := ids.SanitizeComponent(strings.TrimSpace(*campaignID))
	if cid == "" {
		printCampaignReportHelp(r.Stderr)
		return r.failUsage("campaign report: missing/invalid --campaign-id")
	}

	m, err := config.LoadMerged(*outRoot)
	if err != nil {
		fmt.Fprintf(r.Stderr, "ZCL_E_IO: %s\n", err.Error())
		return 1
	}
	st, err := campaign.LoadRunState(campaign.RunStatePath(m.OutRoot, cid))
	if err != nil {
		fmt.Fprintf(r.Stderr, "ZCL_E_IO: %s\n", err.Error())
		return 1
	}
	rep := campaign.BuildReport(st)
	sum := campaign.BuildSummary(st)
	if err := r.persistCampaignArtifacts(st); err != nil {
		fmt.Fprintf(r.Stderr, "ZCL_E_IO: %s\n", err.Error())
		return 1
	}

	policy := resolveCampaignInvalidRunPolicy(st)
	if !*force && policy.PublishRequiresValid && (st.Status == campaign.RunStatusInvalid || st.Status == campaign.RunStatusAborted) {
		if *jsonOut {
			_ = r.writeJSON(rep)
		}
		fmt.Fprintf(r.Stderr, "ZCL_E_USAGE: campaign report: status=%s (use --force to export)\n", st.Status)
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
	fs := flag.NewFlagSet("campaign publish-check", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	campaignID := fs.String("campaign-id", "", "campaign id (required)")
	outRoot := fs.String("out-root", "", "project output root (default from config/env, else .zcl)")
	force := fs.Bool("force", false, "allow publish-check to pass even when campaign is invalid|aborted")
	jsonOut := fs.Bool("json", false, "print JSON output")
	help := fs.Bool("help", false, "show help")

	if err := fs.Parse(args); err != nil {
		return r.failUsage("campaign publish-check: invalid flags")
	}
	if *help {
		printCampaignPublishCheckHelp(r.Stdout)
		return 0
	}
	cid := ids.SanitizeComponent(strings.TrimSpace(*campaignID))
	if cid == "" {
		printCampaignPublishCheckHelp(r.Stderr)
		return r.failUsage("campaign publish-check: missing/invalid --campaign-id")
	}

	m, err := config.LoadMerged(*outRoot)
	if err != nil {
		fmt.Fprintf(r.Stderr, "ZCL_E_IO: %s\n", err.Error())
		return 1
	}
	st, err := campaign.LoadRunState(campaign.RunStatePath(m.OutRoot, cid))
	if err != nil {
		fmt.Fprintf(r.Stderr, "ZCL_E_IO: %s\n", err.Error())
		return 1
	}
	policy := resolveCampaignInvalidRunPolicy(st)
	ok := st.Status == campaign.RunStatusValid
	if !policy.PublishRequiresValid {
		ok = st.Status != campaign.RunStatusAborted
	}
	if len(policy.Statuses) > 0 && !containsString(policy.Statuses, st.Status) {
		ok = false
	}
	if *force && !ok {
		ok = true
	}
	out := map[string]any{
		"ok":          ok,
		"campaignId":  st.CampaignID,
		"runId":       st.RunID,
		"status":      st.Status,
		"reasonCodes": st.ReasonCodes,
	}
	if *jsonOut {
		writeExit := r.writeJSON(out)
		if writeExit != 0 {
			return writeExit
		}
	} else if ok {
		fmt.Fprintf(r.Stdout, "publish-check: OK campaign=%s run=%s\n", st.CampaignID, st.RunID)
	} else {
		fmt.Fprintf(r.Stderr, "publish-check: FAIL campaign=%s run=%s status=%s\n", st.CampaignID, st.RunID, st.Status)
	}
	if ok {
		return 0
	}
	return 2
}

type campaignExecutionInput struct {
	TotalMissions    int
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
		fmt.Fprintf(r.Stderr, "ZCL_E_IO: %s\n", err.Error())
		return campaign.RunStateV1{}, 1
	}

	if strings.TrimSpace(outRoot) == "" {
		outRoot = ".zcl"
	}
	if len(parsed.MissionIndexes) == 0 {
		fmt.Fprintf(r.Stderr, "ZCL_E_USAGE: campaign requires at least one mission\n")
		return campaign.RunStateV1{}, 2
	}
	missionIndexes := in.MissionIndexes
	if len(missionIndexes) == 0 {
		missionIndexes = parsed.MissionIndexes
	}
	if in.TotalMissions > 0 && len(missionIndexes) > in.TotalMissions {
		missionIndexes = missionIndexes[:in.TotalMissions]
	}
	if in.TotalMissions <= 0 {
		in.TotalMissions = len(missionIndexes)
	}
	execAdapter, err := runners.NewCampaignExecutor(func(_ context.Context, flow campaign.FlowSpec, missionIndex int, missionID string) (campaign.FlowRunV1, error) {
		fr, _, runErr := r.runCampaignFlowSuite(parsed, outRoot, flow, campaignSegment{MissionOffset: missionIndex, TotalMissions: 1})
		if len(fr.Attempts) == 0 {
			fr.Attempts = []campaign.AttemptStatusV1{{
				MissionIndex: missionIndex,
				MissionID:    missionID,
				Status:       campaign.AttemptStatusInvalid,
				Errors:       []string{"ZCL_E_CAMPAIGN_MISSING_ATTEMPT"},
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
		fmt.Fprintf(r.Stderr, "ZCL_E_IO: %s\n", err.Error())
		return campaign.RunStateV1{}, 1
	}
	engineResult, err := campaign.ExecuteMissionEngine(
		parsed,
		execAdapter,
		r.evaluateCampaignGateForMission,
		r.runCampaignHook,
		campaign.EngineOptions{
			OutRoot:              outRoot,
			RunID:                runID,
			Canary:               in.Canary,
			ResumedFromRunID:     strings.TrimSpace(in.ResumedFromRunID),
			MissionIndexes:       missionIndexes,
			MissionOffset:        in.MissionOffset,
			TotalMissions:        in.TotalMissions,
			GlobalTimeoutMs:      parsed.Spec.Timeouts.CampaignGlobalTimeoutMs,
			CleanupHookTimeoutMs: parsed.Spec.Timeouts.CleanupHookTimeoutMs,
			LockWait:             750 * time.Millisecond,
			Now:                  r.Now,
		},
	)
	if err != nil {
		fmt.Fprintf(r.Stderr, "ZCL_E_IO: %s\n", err.Error())
		return campaign.RunStateV1{}, 1
	}
	if err := r.persistCampaignArtifacts(engineResult.State); err != nil {
		fmt.Fprintf(r.Stderr, "ZCL_E_IO: %s\n", err.Error())
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
		fr := &missionFlowRuns[fidx]
		ma := campaign.MissionGateAttemptV1{
			FlowID: fr.FlowID,
			Status: campaign.AttemptStatusInvalid,
			OK:     false,
		}
		if len(fr.Attempts) == 0 {
			ma.Errors = []string{"ZCL_E_CAMPAIGN_MISSING_ATTEMPT"}
			mg.Attempts = append(mg.Attempts, ma)
			if parsed.Spec.PairGateEnabled() {
				mg.Reasons = append(mg.Reasons, "ZCL_E_CAMPAIGN_MISSING_ATTEMPT")
				mg.OK = false
			}
			continue
		}
		ar := &fr.Attempts[0]
		ma.AttemptID = ar.AttemptID
		ma.AttemptDir = ar.AttemptDir
		ma.Status = ar.Status
		ma.Errors = append(ma.Errors, ar.Errors...)

		gateErrors := make([]string, 0, 8)
		if parsed.Spec.PairGateEnabled() && ar.Status != campaign.AttemptStatusValid {
			gateErrors = append(gateErrors, "ZCL_E_CAMPAIGN_ATTEMPT_NOT_VALID")
		}
		if strings.TrimSpace(ar.AttemptDir) == "" {
			if parsed.Spec.PairGateEnabled() {
				gateErrors = append(gateErrors, "ZCL_E_CAMPAIGN_ARTIFACT_GATE")
			}
		} else {
			rep, err := readAttemptReport(ar.AttemptDir)
			if err != nil {
				if parsed.Spec.PairGateEnabled() {
					gateErrors = append(gateErrors, "ZCL_E_CAMPAIGN_ARTIFACT_GATE")
				}
			} else if parsed.Spec.PairGateEnabled() {
				if rep.Integrity == nil || !rep.Integrity.TracePresent || !rep.Integrity.TraceNonEmpty || !rep.Integrity.FeedbackPresent {
					gateErrors = append(gateErrors, "ZCL_E_CAMPAIGN_TRACE_GATE")
				}
				if rep.TimedOutBeforeFirstToolCall {
					gateErrors = append(gateErrors, "ZCL_E_CAMPAIGN_TIMEOUT_GATE")
				}
				if rep.FailureCodeHistogram["ZCL_E_TIMEOUT"] > 0 {
					gateErrors = append(gateErrors, "ZCL_E_CAMPAIGN_TIMEOUT_GATE")
				}
			}
			if parsed.Spec.PairGateEnabled() {
				profileFindings, err := campaign.EvaluateTraceProfile(parsed.Spec.PairGate.TraceProfile, ar.AttemptDir)
				if err != nil {
					return mg, err
				}
				gateErrors = append(gateErrors, profileFindings...)
			}
		}
		if parsed.Spec.Semantic.Enabled {
			if strings.TrimSpace(ar.AttemptDir) == "" {
				gateErrors = append(gateErrors, campaign.ReasonSemanticFailed)
			} else {
				semRes, err := semantic.ValidatePath(ar.AttemptDir, semantic.Options{RulesPath: parsed.Spec.Semantic.RulesPath})
				if err != nil {
					return mg, err
				}
				if !semRes.Evaluated || !semRes.OK {
					gateErrors = append(gateErrors, campaign.ReasonSemanticFailed)
				}
			}
		}
		if len(gateErrors) == 0 {
			ma.OK = true
			ma.Status = campaign.AttemptStatusValid
			ma.Errors = nil
			ar.Status = campaign.AttemptStatusValid
		} else {
			gateErrors = dedupeSortedStrings(gateErrors)
			ma.OK = false
			ma.Errors = dedupeSortedStrings(append(ma.Errors, gateErrors...))
			ar.Errors = dedupeSortedStrings(append(ar.Errors, gateErrors...))
			if containsString(gateErrors, "ZCL_E_CAMPAIGN_TIMEOUT_GATE") {
				ma.Status = campaign.AttemptStatusInfraFailed
				ar.Status = campaign.AttemptStatusInfraFailed
			} else if ar.Status == campaign.AttemptStatusSkipped {
				ma.Status = campaign.AttemptStatusSkipped
			} else {
				ma.Status = campaign.AttemptStatusInvalid
				ar.Status = campaign.AttemptStatusInvalid
			}
			if parsed.Spec.PairGateEnabled() || parsed.Spec.Semantic.Enabled {
				mg.OK = false
				mg.Reasons = append(mg.Reasons, gateErrors...)
			}
		}
		mg.Attempts = append(mg.Attempts, ma)
	}
	mg.Reasons = dedupeSortedStrings(mg.Reasons)
	return mg, nil
}

func (r Runner) runCampaignFlowSuite(parsed campaign.ParsedSpec, outRoot string, flow campaign.FlowSpec, seg campaignSegment) (campaign.FlowRunV1, *suiteRunSummary, error) {
	suiteFile, err := materializeCampaignFlowSuite(parsed, outRoot, flow)
	if err != nil {
		return campaign.FlowRunV1{}, nil, err
	}
	args := []string{
		"--file", suiteFile,
		"--out-root", outRoot,
		"--campaign-id", parsed.Spec.CampaignID,
		"--session-isolation", flow.Runner.SessionIsolation,
		"--feedback-policy", flow.Runner.FeedbackPolicy,
		"--parallel", "1",
		"--total", strconv.Itoa(seg.TotalMissions),
		"--mission-offset", strconv.Itoa(seg.MissionOffset),
		"--fail-fast=" + strconv.FormatBool(parsed.Spec.FailFast),
		"--json",
	}
	if strings.TrimSpace(flow.Runner.Mode) != "" {
		args = append(args, "--mode", strings.TrimSpace(flow.Runner.Mode))
	}
	if flow.Runner.TimeoutMs > 0 {
		args = append(args, "--timeout-ms", strconv.FormatInt(flow.Runner.TimeoutMs, 10))
	}
	if strings.TrimSpace(flow.Runner.TimeoutStart) != "" {
		args = append(args, "--timeout-start", strings.TrimSpace(flow.Runner.TimeoutStart))
	}
	if flow.Runner.Strict != nil {
		args = append(args, "--strict="+strconv.FormatBool(*flow.Runner.Strict))
	}
	if flow.Runner.StrictExpect != nil {
		args = append(args, "--strict-expect="+strconv.FormatBool(*flow.Runner.StrictExpect))
	}
	for _, shim := range flow.Runner.Shims {
		args = append(args, "--shim", shim)
	}
	args = append(args, "--")
	args = append(args, flow.Runner.Command...)

	env := map[string]string{}
	for k, v := range flow.Runner.Env {
		if strings.TrimSpace(k) == "" {
			continue
		}
		env[k] = v
	}
	env["ZCL_CAMPAIGN_RUNNER_TYPE"] = strings.TrimSpace(flow.Runner.Type)
	env["ZCL_FRESH_AGENT_PER_ATTEMPT"] = "1"
	if flow.Runner.MCP.MaxToolCalls > 0 {
		env["ZCL_MCP_MAX_TOOL_CALLS"] = strconv.FormatInt(flow.Runner.MCP.MaxToolCalls, 10)
	}
	if flow.Runner.MCP.IdleTimeoutMs > 0 {
		env["ZCL_MCP_IDLE_TIMEOUT_MS"] = strconv.FormatInt(flow.Runner.MCP.IdleTimeoutMs, 10)
	}
	if flow.Runner.MCP.ShutdownOnComplete {
		env["ZCL_MCP_SHUTDOWN_ON_COMPLETE"] = "1"
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	sub := Runner{
		Version: r.Version,
		Now:     r.Now,
		Stdout:  &stdout,
		Stderr:  io.MultiWriter(r.Stderr, &stderr),
	}
	exit := sub.runSuiteRunWithEnv(args, env)

	fr := campaign.FlowRunV1{
		FlowID:      flow.FlowID,
		RunnerType:  flow.Runner.Type,
		SuiteFile:   suiteFile,
		ExitCode:    exit,
		OK:          exit == 0,
		ErrorOutput: trimText(stderr.String(), 4096),
	}
	if exit != 0 {
		fr.Errors = append(fr.Errors, fmt.Sprintf("ZCL_E_CAMPAIGN_FLOW_EXIT_%d", exit))
	}

	var sum suiteRunSummary
	if strings.TrimSpace(stdout.String()) != "" {
		if err := json.Unmarshal(stdout.Bytes(), &sum); err != nil {
			fr.OK = false
			fr.Errors = append(fr.Errors, "ZCL_E_CAMPAIGN_SUMMARY_PARSE")
			return fr, nil, fmt.Errorf("flow %s summary parse: %w", flow.FlowID, err)
		}
		fr.RunID = sum.RunID
		if !sum.OK {
			fr.OK = false
		}
		fr.Attempts = make([]campaign.AttemptStatusV1, 0, len(sum.Attempts))
		for i, a := range sum.Attempts {
			ar := campaign.AttemptStatusV1{
				MissionIndex:     seg.MissionOffset + i,
				MissionID:        a.MissionID,
				AttemptID:        a.AttemptID,
				AttemptDir:       a.AttemptDir,
				RunnerRef:        strings.TrimSpace(sum.RunID + ":" + a.AttemptID),
				RunnerErrorCode:  a.RunnerErrorCode,
				AutoFeedbackCode: a.AutoFeedbackCode,
			}
			switch {
			case a.Skipped:
				ar.Status = campaign.AttemptStatusSkipped
				ar.Errors = append(ar.Errors, "ZCL_E_CAMPAIGN_SKIPPED")
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
			fr.Attempts = append(fr.Attempts, ar)
		}
		return fr, &sum, nil
	}
	if exit != 0 {
		return fr, nil, fmt.Errorf("flow %s failed before emitting suite summary", flow.FlowID)
	}
	return fr, nil, nil
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
			fmt.Fprintf(&b, "- `%s` (%s): attempts=%d valid=%d invalid=%d skipped=%d infra_failed=%d\n", f.FlowID, f.RunnerType, f.AttemptsTotal, f.Valid, f.Invalid, f.Skipped, f.InfraFailed)
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
		fmt.Fprintf(r.Stderr, "ZCL_E_USAGE: unknown mission subcommand %q\n", args[0])
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
		fmt.Fprintf(r.Stderr, "ZCL_E_USAGE: unknown mission prompts subcommand %q\n", args[0])
		printMissionPromptsHelp(r.Stderr)
		return 2
	}
}

func (r Runner) runMissionPromptsBuild(args []string) int {
	fs := flag.NewFlagSet("mission prompts build", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	spec := fs.String("spec", "", "campaign spec file (.json|.yaml|.yml) (required)")
	template := fs.String("template", "", "prompt template file (required)")
	out := fs.String("out", "", "output artifact path (default <outRoot>/campaigns/<campaignId>/mission.prompts.json)")
	outRoot := fs.String("out-root", "", "project output root (default from config/env, else spec.outRoot, else .zcl)")
	jsonOut := fs.Bool("json", false, "print JSON output")
	help := fs.Bool("help", false, "show help")

	if err := fs.Parse(args); err != nil {
		return r.failUsage("mission prompts build: invalid flags")
	}
	if *help {
		printMissionPromptsBuildHelp(r.Stdout)
		return 0
	}
	if strings.TrimSpace(*spec) == "" {
		printMissionPromptsBuildHelp(r.Stderr)
		return r.failUsage("mission prompts build: missing --spec")
	}
	if strings.TrimSpace(*template) == "" {
		printMissionPromptsBuildHelp(r.Stderr)
		return r.failUsage("mission prompts build: missing --template")
	}

	parsed, resolvedOutRoot, err := r.loadCampaignSpec(*spec, *outRoot)
	if err != nil {
		fmt.Fprintf(r.Stderr, "ZCL_E_IO: %s\n", err.Error())
		return 1
	}
	templateRaw, err := os.ReadFile(*template)
	if err != nil {
		fmt.Fprintf(r.Stderr, "ZCL_E_IO: %s\n", err.Error())
		return 1
	}
	tpl := string(templateRaw)

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

	outPath := strings.TrimSpace(*out)
	if outPath == "" {
		outPath = filepath.Join(resolvedOutRoot, "campaigns", parsed.Spec.CampaignID, "mission.prompts.json")
	}
	absSpec, _ := filepath.Abs(*spec)
	absTemplate, _ := filepath.Abs(*template)
	createdAt := deterministicBuildTimestamp(absSpec, absTemplate, tpl, prompts)
	result := missionPromptsBuildResult{
		SchemaVersion: 1,
		CampaignID:    parsed.Spec.CampaignID,
		SpecPath:      absSpec,
		TemplatePath:  absTemplate,
		OutPath:       outPath,
		CreatedAt:     createdAt,
		Prompts:       prompts,
	}
	if err := store.WriteJSONAtomic(outPath, result); err != nil {
		fmt.Fprintf(r.Stderr, "ZCL_E_IO: %s\n", err.Error())
		return 1
	}
	if *jsonOut {
		return r.writeJSON(result)
	}
	fmt.Fprintf(r.Stdout, "mission prompts build: OK %s\n", outPath)
	return 0
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
  zcl campaign report --campaign-id <id> [--format json,md] [--force] [--json]
  zcl campaign publish-check --campaign-id <id> [--force] [--json]
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
  zcl campaign report --campaign-id <id> [--out-root .zcl] [--format json,md] [--force] [--json]
`)
}

func printCampaignPublishCheckHelp(w io.Writer) {
	fmt.Fprint(w, `Usage:
  zcl campaign publish-check --campaign-id <id> [--out-root .zcl] [--force] [--json]
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
