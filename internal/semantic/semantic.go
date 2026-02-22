package semantic

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/marcohefti/zero-context-lab/internal/schema"
	"github.com/marcohefti/zero-context-lab/internal/suite"
	"gopkg.in/yaml.v3"
)

type RulePackV1 struct {
	SchemaVersion int                                 `json:"schemaVersion" yaml:"schemaVersion"`
	Default       *suite.SemanticExpectsV1            `json:"default,omitempty" yaml:"default,omitempty"`
	Missions      map[string]*suite.SemanticExpectsV1 `json:"missions,omitempty" yaml:"missions,omitempty"`
}

type Options struct {
	RulesPath string
}

type Finding struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Path    string `json:"path,omitempty"`
}

type AttemptResult struct {
	AttemptDir string    `json:"attemptDir"`
	RunID      string    `json:"runId,omitempty"`
	SuiteID    string    `json:"suiteId,omitempty"`
	MissionID  string    `json:"missionId,omitempty"`
	AttemptID  string    `json:"attemptId,omitempty"`
	OK         bool      `json:"ok"`
	Evaluated  bool      `json:"evaluated"`
	RuleSource string    `json:"ruleSource,omitempty"`
	Failures   []Finding `json:"failures,omitempty"`
}

type Result struct {
	OK        bool            `json:"ok"`
	Target    string          `json:"target"` // attempt|run
	Path      string          `json:"path"`
	RulePath  string          `json:"rulePath,omitempty"`
	Evaluated bool            `json:"evaluated"`
	Attempts  []AttemptResult `json:"attempts,omitempty"`
	Failures  []Finding       `json:"failures,omitempty"`
}

func ValidatePath(targetDir string, opts Options) (Result, error) {
	abs, err := filepath.Abs(targetDir)
	if err != nil {
		return Result{}, err
	}
	info, err := os.Stat(abs)
	if err != nil {
		return Result{}, err
	}
	if !info.IsDir() {
		return Result{
			OK:     false,
			Target: "unknown",
			Path:   abs,
			Failures: []Finding{{
				Code:    "ZCL_E_USAGE",
				Message: "target must be a directory",
				Path:    abs,
			}},
		}, nil
	}

	var pack *RulePackV1
	if strings.TrimSpace(opts.RulesPath) != "" {
		rp, err := loadRulePack(strings.TrimSpace(opts.RulesPath))
		if err != nil {
			return Result{}, err
		}
		pack = &rp
	}

	if _, err := os.Stat(filepath.Join(abs, "attempt.json")); err == nil {
		ar, err := evaluateAttempt(abs, pack)
		if err != nil {
			return Result{}, err
		}
		res := Result{
			OK:        ar.OK,
			Target:    "attempt",
			Path:      abs,
			RulePath:  strings.TrimSpace(opts.RulesPath),
			Evaluated: ar.Evaluated,
		}
		if ar.Evaluated && !ar.OK {
			res.Failures = append(res.Failures, ar.Failures...)
		}
		if ar.Evaluated {
			res.Attempts = []AttemptResult{ar}
		}
		return res, nil
	}

	if _, err := os.Stat(filepath.Join(abs, "run.json")); err == nil {
		attemptsDir := filepath.Join(abs, "attempts")
		entries, err := os.ReadDir(attemptsDir)
		if err != nil {
			return Result{}, err
		}
		out := Result{
			OK:       true,
			Target:   "run",
			Path:     abs,
			RulePath: strings.TrimSpace(opts.RulesPath),
		}
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			ar, err := evaluateAttempt(filepath.Join(attemptsDir, e.Name()), pack)
			if err != nil {
				return Result{}, err
			}
			if ar.Evaluated {
				out.Evaluated = true
			}
			if !ar.OK {
				out.OK = false
				out.Failures = append(out.Failures, ar.Failures...)
			}
			out.Attempts = append(out.Attempts, ar)
		}
		sort.Slice(out.Attempts, func(i, j int) bool {
			return out.Attempts[i].AttemptID < out.Attempts[j].AttemptID
		})
		return out, nil
	}

	return Result{
		OK:     false,
		Target: "unknown",
		Path:   abs,
		Failures: []Finding{{
			Code:    "ZCL_E_USAGE",
			Message: "target does not look like an attemptDir or runDir",
			Path:    abs,
		}},
	}, nil
}

func evaluateAttempt(attemptDir string, pack *RulePackV1) (AttemptResult, error) {
	out := AttemptResult{
		AttemptDir: attemptDir,
		OK:         true,
	}

	attemptPath := filepath.Join(attemptDir, "attempt.json")
	feedbackPath := filepath.Join(attemptDir, "feedback.json")
	tracePath := filepath.Join(attemptDir, "tool.calls.jsonl")

	attemptRaw, err := os.ReadFile(attemptPath)
	if err != nil {
		out.OK = false
		out.Failures = append(out.Failures, Finding{Code: "ZCL_E_MISSING_ARTIFACT", Message: "missing attempt.json", Path: attemptPath})
		return out, nil
	}
	var a schema.AttemptJSONV1
	if err := json.Unmarshal(attemptRaw, &a); err != nil {
		out.OK = false
		out.Failures = append(out.Failures, Finding{Code: "ZCL_E_INVALID_JSON", Message: "attempt.json is not valid json", Path: attemptPath})
		return out, nil
	}
	out.RunID = a.RunID
	out.SuiteID = a.SuiteID
	out.MissionID = a.MissionID
	out.AttemptID = a.AttemptID

	feedbackRaw, err := os.ReadFile(feedbackPath)
	if err != nil {
		out.OK = false
		out.Failures = append(out.Failures, Finding{Code: "ZCL_E_MISSING_ARTIFACT", Message: "missing feedback.json", Path: feedbackPath})
		return out, nil
	}
	var fb schema.FeedbackJSONV1
	if err := json.Unmarshal(feedbackRaw, &fb); err != nil {
		out.OK = false
		out.Failures = append(out.Failures, Finding{Code: "ZCL_E_INVALID_JSON", Message: "feedback.json is not valid json", Path: feedbackPath})
		return out, nil
	}

	rules, source, err := selectRules(attemptDir, a.MissionID, pack)
	if err != nil {
		return out, err
	}
	if rules == nil {
		return out, nil
	}
	out.Evaluated = true
	out.RuleSource = source

	tf, err := traceFacts(tracePath)
	if err != nil {
		return out, err
	}

	for _, f := range suite.ValidateSemantic(rules, fb, tf) {
		out.Failures = append(out.Failures, Finding{
			Code:    "ZCL_E_SEMANTIC",
			Message: f.Code + ": " + f.Message,
			Path:    attemptDir,
		})
	}

	hookFindings, err := evaluateHook(attemptDir, a, rules)
	if err != nil {
		return out, err
	}
	out.Failures = append(out.Failures, hookFindings...)
	if len(out.Failures) > 0 {
		out.OK = false
	}
	return out, nil
}

func selectRules(attemptDir string, missionID string, pack *RulePackV1) (*suite.SemanticExpectsV1, string, error) {
	if pack != nil {
		if m, ok := pack.Missions[missionID]; ok && m != nil {
			return m, "rulepack:missions." + missionID, nil
		}
		if pack.Default != nil {
			return pack.Default, "rulepack:default", nil
		}
		return nil, "", nil
	}

	runDir := filepath.Dir(filepath.Dir(attemptDir))
	suitePath := filepath.Join(runDir, "suite.json")
	raw, err := os.ReadFile(suitePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, "", nil
		}
		return nil, "", err
	}
	var sf suite.SuiteFileV1
	if err := json.Unmarshal(raw, &sf); err != nil {
		return nil, "", err
	}
	m := suite.FindMission(sf, missionID)
	if m == nil || m.Expects == nil || m.Expects.Semantic == nil {
		return nil, "", nil
	}
	return m.Expects.Semantic, "suite.json:mission." + missionID, nil
}

func loadRulePack(path string) (RulePackV1, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return RulePackV1{}, err
	}
	var rp RulePackV1
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".yaml", ".yml":
		if err := yaml.Unmarshal(raw, &rp); err != nil {
			return RulePackV1{}, err
		}
	default:
		if err := json.Unmarshal(raw, &rp); err != nil {
			return RulePackV1{}, err
		}
	}
	if rp.SchemaVersion == 0 {
		rp.SchemaVersion = 1
	}
	if rp.SchemaVersion != 1 {
		return RulePackV1{}, fmt.Errorf("unsupported semantic rule pack schemaVersion (expected 1)")
	}
	return rp, nil
}

func traceFacts(tracePath string) (*suite.TraceFacts, error) {
	f, err := os.Open(tracePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer func() { _ = f.Close() }()

	var (
		sc           = bufio.NewScanner(f)
		total        int64
		failures     int64
		timeouts     int64
		lastSig      string
		streak       int64
		maxStreak    int64
		distinct     = map[string]bool{}
		commandNames = map[string]bool{}
		toolOps      = map[string]bool{}
		mcpTools     = map[string]bool{}
		seenNonEmpty bool
	)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		seenNonEmpty = true

		var ev schema.TraceEventV1
		if err := json.Unmarshal(line, &ev); err != nil {
			return nil, err
		}
		total++
		if !ev.Result.OK {
			failures++
			if ev.Result.Code == "ZCL_E_TIMEOUT" {
				timeouts++
			}
		}
		if ev.Op != "" {
			toolOps[ev.Op] = true
		}

		sig := ev.Tool + "\x1f" + ev.Op + "\x1f" + string(ev.Input)
		distinct[sig] = true
		if sig == lastSig {
			streak++
		} else {
			lastSig = sig
			streak = 1
		}
		if streak > maxStreak {
			maxStreak = streak
		}

		if ev.Op == "exec" && len(ev.Input) > 0 {
			var in struct {
				Argv []string `json:"argv"`
			}
			if err := json.Unmarshal(ev.Input, &in); err == nil && len(in.Argv) > 0 && in.Argv[0] != "" {
				commandNames[in.Argv[0]] = true
			}
		}
		if ev.Tool == "mcp" && ev.Op == "tools/call" && len(ev.Input) > 0 {
			var in struct {
				Params struct {
					Name string `json:"name"`
				} `json:"params"`
			}
			if err := json.Unmarshal(ev.Input, &in); err == nil && in.Params.Name != "" {
				mcpTools[in.Params.Name] = true
			}
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	if !seenNonEmpty {
		return nil, nil
	}

	var cmdNames []string
	for s := range commandNames {
		cmdNames = append(cmdNames, s)
	}
	sort.Strings(cmdNames)
	var ops []string
	for s := range toolOps {
		ops = append(ops, s)
	}
	sort.Strings(ops)
	var mcp []string
	for s := range mcpTools {
		mcp = append(mcp, s)
	}
	sort.Strings(mcp)

	return &suite.TraceFacts{
		ToolCallsTotal:            total,
		FailuresTotal:             failures,
		TimeoutsTotal:             timeouts,
		RepeatMaxStreak:           maxStreak,
		DistinctCommandSignatures: int64(len(distinct)),
		CommandNamesSeen:          cmdNames,
		ToolOpsSeen:               ops,
		MCPToolsSeen:              mcp,
	}, nil
}

func evaluateHook(attemptDir string, a schema.AttemptJSONV1, rules *suite.SemanticExpectsV1) ([]Finding, error) {
	if rules == nil || len(rules.HookCommand) == 0 {
		return nil, nil
	}

	timeout := rules.HookTimeoutMs
	if timeout <= 0 {
		timeout = 10000
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeout)*time.Millisecond)
	defer cancel()

	cmd := exec.CommandContext(ctx, rules.HookCommand[0], rules.HookCommand[1:]...)
	cmd.Env = append(os.Environ(),
		"ZCL_ATTEMPT_DIR="+attemptDir,
		"ZCL_RUN_ID="+a.RunID,
		"ZCL_SUITE_ID="+a.SuiteID,
		"ZCL_MISSION_ID="+a.MissionID,
		"ZCL_ATTEMPT_ID="+a.AttemptID,
	)
	out, err := cmd.CombinedOutput()
	output := strings.TrimSpace(string(out))
	if err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return []Finding{{
				Code:    "ZCL_E_SEMANTIC_HOOK",
				Message: "semantic hook timed out",
				Path:    attemptDir,
			}}, nil
		}
		msg := "semantic hook failed"
		if output != "" {
			msg += ": " + output
		}
		return []Finding{{
			Code:    "ZCL_E_SEMANTIC_HOOK",
			Message: msg,
			Path:    attemptDir,
		}}, nil
	}

	if output == "" {
		return nil, nil
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(output), &payload); err != nil {
		// Non-JSON output after success is accepted as informational.
		return nil, nil
	}
	if semPass, ok := payload["semanticPass"].(bool); ok && !semPass {
		msg := "semantic hook reported failure"
		if s, ok := payload["message"].(string); ok && strings.TrimSpace(s) != "" {
			msg = strings.TrimSpace(s)
		}
		return []Finding{{
			Code:    "ZCL_E_SEMANTIC_HOOK",
			Message: msg,
			Path:    attemptDir,
		}}, nil
	}
	if okVal, ok := payload["ok"].(bool); ok && !okVal {
		msg := "semantic hook reported ok=false"
		if s, ok := payload["message"].(string); ok && strings.TrimSpace(s) != "" {
			msg = strings.TrimSpace(s)
		}
		return []Finding{{
			Code:    "ZCL_E_SEMANTIC_HOOK",
			Message: msg,
			Path:    attemptDir,
		}}, nil
	}
	return nil, nil
}
