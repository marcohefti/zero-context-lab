package expect

import (
	"bufio"
	"encoding/json"
	"github.com/marcohefti/zero-context-lab/internal/kernel/artifacts"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/marcohefti/zero-context-lab/internal/contexts/spec/ports/suite"
	"github.com/marcohefti/zero-context-lab/internal/kernel/schema"
)

type Finding struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Path    string `json:"path,omitempty"`
}

type Result struct {
	OK        bool      `json:"ok"`
	Target    string    `json:"target"` // attempt|run
	Path      string    `json:"path"`
	Evaluated bool      `json:"evaluated"`
	Failures  []Finding `json:"failures,omitempty"`
}

func ExpectPath(targetDir string, strict bool) (Result, error) {
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

	if _, err := os.Stat(filepath.Join(abs, artifacts.AttemptJSON)); err == nil {
		return expectAttempt(abs, strict)
	}
	if _, err := os.Stat(filepath.Join(abs, artifacts.RunJSON)); err == nil {
		return expectRun(abs, strict)
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

func expectRun(runDir string, strict bool) (Result, error) {
	res := Result{OK: true, Target: "run", Path: runDir}
	attemptsDir := filepath.Join(runDir, "attempts")
	entries, err := os.ReadDir(attemptsDir)
	if err != nil {
		if strict && os.IsNotExist(err) {
			res.OK = false
			res.Failures = append(res.Failures, Finding{Code: "ZCL_E_MISSING_ARTIFACT", Message: "missing attempts directory", Path: attemptsDir})
			return res, nil
		}
		return res, nil
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		r, err := expectAttempt(filepath.Join(attemptsDir, e.Name()), strict)
		if err != nil {
			return Result{}, err
		}
		if !r.OK {
			res.OK = false
			res.Failures = append(res.Failures, r.Failures...)
		}
		if r.Evaluated {
			res.Evaluated = true
		}
	}
	return res, nil
}

func expectAttempt(attemptDir string, strict bool) (Result, error) {
	res := Result{OK: true, Target: "attempt", Path: attemptDir}
	a, feedbackPath, fb, done, err := loadAttemptEvidence(attemptDir, strict, &res)
	if err != nil || done {
		return resOrErr(res, err)
	}
	sf, done, err := loadSuiteSnapshot(attemptDir, strict, &res)
	if err != nil || done {
		return resOrErr(res, err)
	}

	tf, err := maybeTraceFactsForMission(attemptDir, strict, sf, a.MissionID)
	if err != nil {
		return Result{}, err
	}
	er := suite.Evaluate(sf, a.MissionID, fb, tf)
	return finalizeExpectationResult(res, er, feedbackPath), nil
}

func resOrErr(res Result, err error) (Result, error) {
	if err != nil {
		return Result{}, err
	}
	return res, nil
}

func loadAttemptEvidence(attemptDir string, strict bool, res *Result) (schema.AttemptJSONV1, string, schema.FeedbackJSONV1, bool, error) {
	a, done, err := loadAttemptHeader(attemptDir, strict, res)
	if err != nil || done {
		return schema.AttemptJSONV1{}, "", schema.FeedbackJSONV1{}, done, err
	}
	feedbackPath := filepath.Join(attemptDir, artifacts.FeedbackJSON)
	fb, done, err := loadFeedbackForExpect(feedbackPath, strict, res)
	return a, feedbackPath, fb, done, err
}

func loadAttemptHeader(attemptDir string, strict bool, res *Result) (schema.AttemptJSONV1, bool, error) {
	attemptPath := filepath.Join(attemptDir, artifacts.AttemptJSON)
	attemptBytes, err := os.ReadFile(attemptPath)
	if err != nil {
		if strict && os.IsNotExist(err) {
			appendFailure(res, "ZCL_E_MISSING_ARTIFACT", "missing attempt.json", attemptPath)
			return schema.AttemptJSONV1{}, true, nil
		}
		return schema.AttemptJSONV1{}, false, err
	}
	var a schema.AttemptJSONV1
	if err := json.Unmarshal(attemptBytes, &a); err != nil {
		appendFailure(res, "ZCL_E_INVALID_JSON", "attempt.json is not valid json", attemptPath)
		return schema.AttemptJSONV1{}, true, nil
	}
	return a, false, nil
}

func loadFeedbackForExpect(feedbackPath string, strict bool, res *Result) (schema.FeedbackJSONV1, bool, error) {
	fbBytes, err := os.ReadFile(feedbackPath)
	if err != nil {
		if strict && os.IsNotExist(err) {
			appendFailure(res, "ZCL_E_MISSING_ARTIFACT", "missing feedback.json", feedbackPath)
			return schema.FeedbackJSONV1{}, true, nil
		}
		return schema.FeedbackJSONV1{}, true, nil
	}
	var fb schema.FeedbackJSONV1
	if err := json.Unmarshal(fbBytes, &fb); err != nil {
		appendFailure(res, "ZCL_E_INVALID_JSON", "feedback.json is not valid json", feedbackPath)
		return schema.FeedbackJSONV1{}, true, nil
	}
	return fb, false, nil
}

func loadSuiteSnapshot(attemptDir string, strict bool, res *Result) (suite.SuiteFileV1, bool, error) {
	runDir := filepath.Dir(filepath.Dir(attemptDir))
	runPath := filepath.Join(runDir, artifacts.RunJSON)
	if _, err := os.Stat(runPath); err != nil {
		if strict && os.IsNotExist(err) {
			appendFailure(res, "ZCL_E_MISSING_ARTIFACT", "missing run.json", runPath)
		}
		return suite.SuiteFileV1{}, true, nil
	}
	suitePath := filepath.Join(runDir, artifacts.SuiteJSON)
	suiteBytes, err := os.ReadFile(suitePath)
	if err != nil {
		if strict && os.IsNotExist(err) {
			appendFailure(res, "ZCL_E_MISSING_ARTIFACT", "missing suite.json", suitePath)
		}
		return suite.SuiteFileV1{}, true, nil
	}
	var sf suite.SuiteFileV1
	if err := json.Unmarshal(suiteBytes, &sf); err != nil {
		appendFailure(res, "ZCL_E_INVALID_JSON", "suite.json is not valid suite json", suitePath)
		return suite.SuiteFileV1{}, true, nil
	}
	return sf, false, nil
}

func maybeTraceFactsForMission(attemptDir string, strict bool, sf suite.SuiteFileV1, missionID string) (*suite.TraceFacts, error) {
	m := suite.FindMission(sf, missionID)
	if m == nil || m.Expects == nil || m.Expects.Trace == nil {
		return nil, nil
	}
	return traceFactsForAttempt(attemptDir, strict)
}

func finalizeExpectationResult(res Result, er suite.ExpectationResult, feedbackPath string) Result {
	if !er.Evaluated {
		res.Evaluated = false
		return res
	}
	res.Evaluated = true
	if er.OK {
		return res
	}
	res.OK = false
	for _, f := range er.Failures {
		res.Failures = append(res.Failures, Finding{Code: "ZCL_E_EXPECTATION_FAILED", Message: f.Code + ": " + f.Message, Path: feedbackPath})
	}
	return res
}

func appendFailure(res *Result, code, message, path string) {
	res.OK = false
	res.Failures = append(res.Failures, Finding{Code: code, Message: message, Path: path})
}

func traceFactsForAttempt(attemptDir string, strict bool) (*suite.TraceFacts, error) {
	tracePath := filepath.Join(attemptDir, artifacts.ToolCallsJSONL)
	f, missing, err := openAttemptTrace(tracePath, strict)
	if err != nil {
		return nil, err
	}
	if missing {
		return nil, nil
	}
	defer func() { _ = f.Close() }()

	acc := newTraceFactsAccumulator()
	if err := scanAttemptTrace(f, strict, acc); err != nil {
		return nil, err
	}
	if !acc.seenNonEmpty {
		return nil, nil
	}
	return acc.facts(), nil
}

func openAttemptTrace(tracePath string, strict bool) (*os.File, bool, error) {
	f, err := os.Open(tracePath)
	if err == nil {
		return f, false, nil
	}
	if os.IsNotExist(err) {
		_ = strict // strict keeps nil facts; caller turns this into expectation failures.
		return nil, true, nil
	}
	return nil, false, err
}

type traceFactsAccumulator struct {
	total        int64
	failures     int64
	timeouts     int64
	lastSig      string
	streak       int64
	maxStreak    int64
	seenNonEmpty bool

	distinct map[string]bool
	cmdNames map[string]bool
	toolOps  map[string]bool
	mcpTools map[string]bool
}

func newTraceFactsAccumulator() *traceFactsAccumulator {
	return &traceFactsAccumulator{
		distinct: map[string]bool{},
		cmdNames: map[string]bool{},
		toolOps:  map[string]bool{},
		mcpTools: map[string]bool{},
	}
}

func scanAttemptTrace(f *os.File, strict bool, acc *traceFactsAccumulator) error {
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var ev schema.TraceEventV1
		if err := json.Unmarshal(line, &ev); err != nil {
			return err
		}
		if err := acc.observeEvent(ev, strict); err != nil {
			return err
		}
	}
	return sc.Err()
}

func (a *traceFactsAccumulator) observeEvent(ev schema.TraceEventV1, strict bool) error {
	a.seenNonEmpty = true
	a.total++
	a.observeResult(ev)
	a.observeOperation(ev)
	a.observeSignature(ev)
	a.observeCommandHints(ev)
	if strict {
		if _, err := time.Parse(time.RFC3339Nano, ev.TS); err != nil {
			return err
		}
	}
	return nil
}

func (a *traceFactsAccumulator) observeResult(ev schema.TraceEventV1) {
	if ev.Result.OK {
		return
	}
	a.failures++
	if ev.Result.Code == "ZCL_E_TIMEOUT" {
		a.timeouts++
	}
}

func (a *traceFactsAccumulator) observeOperation(ev schema.TraceEventV1) {
	if ev.Op != "" {
		a.toolOps[ev.Op] = true
	}
}

func (a *traceFactsAccumulator) observeSignature(ev schema.TraceEventV1) {
	sig := ev.Tool + "\x1f" + ev.Op + "\x1f" + string(ev.Input)
	a.distinct[sig] = true
	if sig == a.lastSig {
		a.streak++
	} else {
		a.lastSig = sig
		a.streak = 1
	}
	if a.streak > a.maxStreak {
		a.maxStreak = a.streak
	}
}

func (a *traceFactsAccumulator) observeCommandHints(ev schema.TraceEventV1) {
	switch {
	case ev.Op == "exec":
		a.observeExecCommand(ev)
	case ev.Tool == "mcp" && ev.Op == "tools/call":
		a.observeMCPTool(ev)
	}
}

func (a *traceFactsAccumulator) observeExecCommand(ev schema.TraceEventV1) {
	if len(ev.Input) > 0 {
		var in struct {
			Argv []string `json:"argv"`
		}
		if err := json.Unmarshal(ev.Input, &in); err == nil && len(in.Argv) > 0 && in.Argv[0] != "" {
			a.cmdNames[in.Argv[0]] = true
			return
		}
	}
	if ev.Tool != "" && ev.Tool != "cli" && ev.Tool != "http" && ev.Tool != "mcp" {
		a.cmdNames[ev.Tool] = true
	}
}

func (a *traceFactsAccumulator) observeMCPTool(ev schema.TraceEventV1) {
	if len(ev.Input) == 0 {
		return
	}
	var in struct {
		Params struct {
			Name string `json:"name"`
		} `json:"params"`
	}
	if err := json.Unmarshal(ev.Input, &in); err != nil || in.Params.Name == "" {
		return
	}
	a.mcpTools[in.Params.Name] = true
}

func (a *traceFactsAccumulator) facts() *suite.TraceFacts {
	return &suite.TraceFacts{
		ToolCallsTotal:            a.total,
		FailuresTotal:             a.failures,
		TimeoutsTotal:             a.timeouts,
		RepeatMaxStreak:           a.maxStreak,
		DistinctCommandSignatures: int64(len(a.distinct)),
		CommandNamesSeen:          sortedKeys(a.cmdNames),
		ToolOpsSeen:               sortedKeys(a.toolOps),
		MCPToolsSeen:              sortedKeys(a.mcpTools),
	}
}

func sortedKeys(in map[string]bool) []string {
	out := make([]string, 0, len(in))
	for key := range in {
		out = append(out, key)
	}
	sort.Strings(out)
	return out
}
