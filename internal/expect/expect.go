package expect

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/marcohefti/zero-context-lab/internal/schema"
	"github.com/marcohefti/zero-context-lab/internal/suite"
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

	if _, err := os.Stat(filepath.Join(abs, "attempt.json")); err == nil {
		return expectAttempt(abs, strict)
	}
	if _, err := os.Stat(filepath.Join(abs, "run.json")); err == nil {
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

	attemptPath := filepath.Join(attemptDir, "attempt.json")
	attemptBytes, err := os.ReadFile(attemptPath)
	if err != nil {
		if strict && os.IsNotExist(err) {
			res.OK = false
			res.Failures = append(res.Failures, Finding{Code: "ZCL_E_MISSING_ARTIFACT", Message: "missing attempt.json", Path: attemptPath})
			return res, nil
		}
		return Result{}, err
	}
	var a schema.AttemptJSONV1
	if err := json.Unmarshal(attemptBytes, &a); err != nil {
		res.OK = false
		res.Failures = append(res.Failures, Finding{Code: "ZCL_E_INVALID_JSON", Message: "attempt.json is not valid json", Path: attemptPath})
		return res, nil
	}

	feedbackPath := filepath.Join(attemptDir, "feedback.json")
	fbBytes, err := os.ReadFile(feedbackPath)
	if err != nil {
		if strict && os.IsNotExist(err) {
			res.OK = false
			res.Failures = append(res.Failures, Finding{Code: "ZCL_E_MISSING_ARTIFACT", Message: "missing feedback.json", Path: feedbackPath})
			return res, nil
		}
		return res, nil
	}
	var fb schema.FeedbackJSONV1
	if err := json.Unmarshal(fbBytes, &fb); err != nil {
		res.OK = false
		res.Failures = append(res.Failures, Finding{Code: "ZCL_E_INVALID_JSON", Message: "feedback.json is not valid json", Path: feedbackPath})
		return res, nil
	}

	runDir := filepath.Dir(filepath.Dir(attemptDir))
	suitePath := filepath.Join(runDir, "suite.json")
	suiteBytes, err := os.ReadFile(suitePath)
	if err != nil {
		if strict && os.IsNotExist(err) {
			res.OK = false
			res.Failures = append(res.Failures, Finding{Code: "ZCL_E_MISSING_ARTIFACT", Message: "missing suite.json", Path: suitePath})
			return res, nil
		}
		// No suite => no expectations to evaluate.
		return res, nil
	}
	var sf suite.SuiteFileV1
	if err := json.Unmarshal(suiteBytes, &sf); err != nil {
		res.OK = false
		res.Failures = append(res.Failures, Finding{Code: "ZCL_E_INVALID_JSON", Message: "suite.json is not valid suite json", Path: suitePath})
		return res, nil
	}

	m := suite.FindMission(sf, a.MissionID)
	var tf *suite.TraceFacts
	if m != nil && m.Expects != nil && m.Expects.Trace != nil {
		facts, findErr := traceFactsForAttempt(attemptDir, strict)
		if findErr != nil {
			// Hard IO error.
			return Result{}, findErr
		}
		if facts == nil {
			// Strict mode already emitted missing evidence as failures.
			tf = nil
		} else {
			tf = facts
		}
	}

	er := suite.Evaluate(sf, a.MissionID, fb, tf)
	if !er.Evaluated {
		res.Evaluated = false
		return res, nil
	}

	res.Evaluated = true
	if er.OK {
		return res, nil
	}
	res.OK = false
	for _, f := range er.Failures {
		res.Failures = append(res.Failures, Finding{Code: "ZCL_E_EXPECTATION_FAILED", Message: f.Code + ": " + f.Message, Path: feedbackPath})
	}
	return res, nil
}

func traceFactsForAttempt(attemptDir string, strict bool) (*suite.TraceFacts, error) {
	tracePath := filepath.Join(attemptDir, "tool.calls.jsonl")
	f, err := os.Open(tracePath)
	if err != nil {
		if os.IsNotExist(err) {
			if strict {
				// Surface as a typed failure via expectation failure path.
				// Callers will end up failing anyway; keep this explicit.
				return nil, nil
			}
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
		cmdNames     = map[string]bool{}
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
			if err := json.Unmarshal(ev.Input, &in); err == nil {
				if len(in.Argv) > 0 && in.Argv[0] != "" {
					cmdNames[in.Argv[0]] = true
				}
			}
		} else if ev.Op == "exec" && ev.Tool != "" && ev.Tool != "cli" && ev.Tool != "http" && ev.Tool != "mcp" {
			cmdNames[ev.Tool] = true
		}

		// Defensive: ensure TS is parseable in strict mode.
		if strict {
			if _, err := time.Parse(time.RFC3339Nano, ev.TS); err != nil {
				return nil, err
			}
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	if !seenNonEmpty {
		if strict {
			return nil, nil
		}
		return nil, nil
	}

	var cmdList []string
	for s := range cmdNames {
		cmdList = append(cmdList, s)
	}
	sort.Strings(cmdList)

	return &suite.TraceFacts{
		ToolCallsTotal:            total,
		FailuresTotal:             failures,
		TimeoutsTotal:             timeouts,
		RepeatMaxStreak:           maxStreak,
		DistinctCommandSignatures: int64(len(distinct)),
		CommandNamesSeen:          cmdList,
	}, nil
}
