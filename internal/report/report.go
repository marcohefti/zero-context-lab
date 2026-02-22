package report

import (
	"bufio"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/marcohefti/zero-context-lab/internal/blind"
	"github.com/marcohefti/zero-context-lab/internal/schema"
	"github.com/marcohefti/zero-context-lab/internal/store"
	"github.com/marcohefti/zero-context-lab/internal/suite"
)

type CliError struct {
	Code    string
	Message string
}

func (e *CliError) Error() string { return e.Message }

func BuildAttemptReport(now time.Time, attemptDir string, strict bool) (schema.AttemptReportJSONV1, error) {
	attemptPath := filepath.Join(attemptDir, "attempt.json")
	tracePath := filepath.Join(attemptDir, "tool.calls.jsonl")
	feedbackPath := filepath.Join(attemptDir, "feedback.json")
	notesPath := filepath.Join(attemptDir, "notes.jsonl")
	promptPath := filepath.Join(attemptDir, "prompt.txt")
	attemptEnvPath := filepath.Join(attemptDir, schema.AttemptEnvShFileNameV1)
	runnerCmdPath := filepath.Join(attemptDir, "runner.command.txt")
	runnerStdoutPath := filepath.Join(attemptDir, "runner.stdout.log")
	runnerStderrPath := filepath.Join(attemptDir, "runner.stderr.log")

	attemptBytes, err := os.ReadFile(attemptPath)
	if err != nil {
		if strict && os.IsNotExist(err) {
			return schema.AttemptReportJSONV1{}, &CliError{Code: "ZCL_E_MISSING_ARTIFACT", Message: "missing attempt.json"}
		}
		return schema.AttemptReportJSONV1{}, err
	}
	var attempt schema.AttemptJSONV1
	if err := json.Unmarshal(attemptBytes, &attempt); err != nil {
		return schema.AttemptReportJSONV1{}, err
	}

	// In ci mode, treat report as strict even if the caller didn't pass --strict.
	enforce := strict || attempt.Mode == "ci"

	var okPtr *bool
	var fb schema.FeedbackJSONV1
	feedbackPresent := false
	if fbBytes, err := os.ReadFile(feedbackPath); err == nil {
		feedbackPresent = true
		if err := json.Unmarshal(fbBytes, &fb); err != nil {
			return schema.AttemptReportJSONV1{}, err
		}
		ok := fb.OK
		okPtr = &ok
	} else if enforce && os.IsNotExist(err) {
		return schema.AttemptReportJSONV1{}, &CliError{Code: "ZCL_E_MISSING_ARTIFACT", Message: "missing feedback.json"}
	} else if err != nil && !os.IsNotExist(err) {
		return schema.AttemptReportJSONV1{}, err
	}

	metrics, signals, err := computeMetricsAndSignals(tracePath, enforce)
	if err != nil {
		return schema.AttemptReportJSONV1{}, err
	}
	traceSummary, err := scanTraceSummary(tracePath)
	if err != nil {
		if enforce {
			return schema.AttemptReportJSONV1{}, err
		}
	}

	tracePresent, traceNonEmpty := false, false
	if _, err := os.Stat(tracePath); err == nil {
		tracePresent = true
		nonEmpty, err := store.JSONLHasNonEmptyLine(tracePath)
		if err != nil {
			if enforce {
				return schema.AttemptReportJSONV1{}, err
			}
		} else {
			traceNonEmpty = nonEmpty
		}
	}

	promptContaminationTerms := promptContaminationTerms(attemptDir, attempt.BlindTerms)
	timedOutBeforeFirstToolCall := classifyTimedOutBeforeFirstToolCall(attempt, traceSummary)

	integrity := &schema.AttemptIntegrityV1{
		TracePresent:             tracePresent,
		TraceNonEmpty:            traceNonEmpty,
		FeedbackPresent:          feedbackPresent,
		FunnelBypassSuspected:    feedbackPresent && !traceNonEmpty,
		PromptContaminated:       len(promptContaminationTerms) > 0,
		PromptContaminationTerms: promptContaminationTerms,
	}

	artifacts := schema.AttemptArtifactsV1{
		AttemptJSON:  "attempt.json",
		TraceJSONL:   "tool.calls.jsonl",
		FeedbackJSON: "feedback.json",
	}
	if _, err := os.Stat(notesPath); err == nil {
		artifacts.NotesJSONL = "notes.jsonl"
	}
	if _, err := os.Stat(promptPath); err == nil {
		artifacts.PromptTXT = "prompt.txt"
	}
	if _, err := os.Stat(attemptEnvPath); err == nil {
		artifacts.AttemptEnvSH = schema.AttemptEnvShFileNameV1
	}
	if _, err := os.Stat(runnerCmdPath); err == nil {
		artifacts.RunnerCommandTXT = "runner.command.txt"
	}
	if _, err := os.Stat(runnerStdoutPath); err == nil {
		artifacts.RunnerStdoutLOG = "runner.stdout.log"
	}
	if _, err := os.Stat(runnerStderrPath); err == nil {
		artifacts.RunnerStderrLOG = "runner.stderr.log"
	}

	startedAt := attempt.StartedAt
	endedAt := ""
	if feedbackPresent && fb.CreatedAt != "" {
		endedAt = fb.CreatedAt
	} else if traceNonEmpty {
		// Best-effort: endedAt = max trace ts. We already parsed TS in computeMetrics; re-scan cheaply here
		// to avoid changing computeMetrics's signature.
		if ts, ok := maxTraceTS(tracePath); ok {
			endedAt = ts
		}
	}
	failureCodeHistogram := cloneCountMap(metrics.FailuresByCode)
	tokenEstimates := tokenEstimatesForAttempt(attemptDir, tracePath, metrics)
	decisionTags := deriveDecisionTags(fb.DecisionTags, okPtr, metrics, integrity, timedOutBeforeFirstToolCall)

	var expects *schema.ExpectationResultV1
	if sf, ok, err := loadSuiteForAttempt(attemptDir); err != nil {
		if enforce {
			return schema.AttemptReportJSONV1{}, err
		}
	} else if ok && feedbackPresent {
		var opNames []string
		for op := range metrics.ToolCallsByOp {
			if strings.TrimSpace(op) == "" {
				continue
			}
			opNames = append(opNames, op)
		}
		sort.Strings(opNames)
		tf := suite.TraceFacts{
			ToolCallsTotal:            metrics.ToolCallsTotal,
			FailuresTotal:             metrics.FailuresTotal,
			TimeoutsTotal:             metrics.TimeoutsTotal,
			RepeatMaxStreak:           0,
			DistinctCommandSignatures: 0,
			CommandNamesSeen:          nil,
			ToolOpsSeen:               opNames,
			MCPToolsSeen:              nil,
		}
		if signals != nil {
			tf.RepeatMaxStreak = signals.RepeatMaxStreak
			tf.DistinctCommandSignatures = signals.DistinctCommandSignatures
			tf.CommandNamesSeen = append([]string(nil), signals.CommandNamesSeen...)
		}
		er := suite.Evaluate(sf, attempt.MissionID, fb, &tf)
		expects = &schema.ExpectationResultV1{
			Evaluated: er.Evaluated,
			OK:        er.OK,
		}
		if len(er.Failures) > 0 {
			expects.Failures = make([]schema.ExpectationFailureV1, 0, len(er.Failures))
			for _, f := range er.Failures {
				expects.Failures = append(expects.Failures, schema.ExpectationFailureV1{Code: f.Code, Message: f.Message})
			}
		}
	}

	return schema.AttemptReportJSONV1{
		SchemaVersion:               schema.AttemptReportSchemaV1,
		RunID:                       attempt.RunID,
		SuiteID:                     attempt.SuiteID,
		MissionID:                   attempt.MissionID,
		AttemptID:                   attempt.AttemptID,
		ComputedAt:                  now.UTC().Format(time.RFC3339Nano),
		StartedAt:                   startedAt,
		EndedAt:                     endedAt,
		OK:                          okPtr,
		Result:                      fb.Result,
		ResultJSON:                  fb.ResultJSON,
		Classification:              fb.Classification,
		DecisionTags:                decisionTags,
		NativeResult:                cloneNativeResultProvenance(attempt.NativeResult),
		Metrics:                     metrics,
		FailureCodeHistogram:        failureCodeHistogram,
		TimedOutBeforeFirstToolCall: timedOutBeforeFirstToolCall,
		TokenEstimates:              tokenEstimates,
		Artifacts:                   artifacts,
		Integrity:                   integrity,
		Signals:                     signals,
		Expectations:                expects,
	}, nil
}

type traceSummary struct {
	HasEvent   bool
	FirstTS    time.Time
	FirstTSRaw string
	FirstCode  string
	InputBytes int64
}

func scanTraceSummary(tracePath string) (traceSummary, error) {
	f, err := os.Open(tracePath)
	if err != nil {
		if os.IsNotExist(err) {
			return traceSummary{}, nil
		}
		return traceSummary{}, err
	}
	defer func() { _ = f.Close() }()

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	out := traceSummary{}
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var ev schema.TraceEventV1
		if err := json.Unmarshal(line, &ev); err != nil {
			return traceSummary{}, err
		}
		out.HasEvent = true
		out.InputBytes += int64(len(ev.Input))
		ts, err := time.Parse(time.RFC3339Nano, ev.TS)
		if err != nil {
			continue
		}
		if out.FirstTS.IsZero() || ts.Before(out.FirstTS) {
			out.FirstTS = ts
			out.FirstTSRaw = ev.TS
			out.FirstCode = strings.TrimSpace(ev.Result.Code)
		}
	}
	if err := sc.Err(); err != nil {
		return traceSummary{}, err
	}
	return out, nil
}

func promptContaminationTerms(attemptDir string, configured []string) []string {
	promptPath := filepath.Join(attemptDir, "prompt.txt")
	b, err := os.ReadFile(promptPath)
	if err != nil {
		return nil
	}
	terms := configured
	if len(terms) == 0 {
		terms = blind.DefaultHarnessTermsV1()
	}
	return blind.FindContaminationTerms(string(b), terms)
}

func classifyTimedOutBeforeFirstToolCall(a schema.AttemptJSONV1, s traceSummary) bool {
	if a.TimeoutMs <= 0 || !s.HasEvent || s.FirstTS.IsZero() {
		return false
	}
	timeoutStart := strings.TrimSpace(a.TimeoutStart)
	if timeoutStart == "" {
		timeoutStart = schema.TimeoutStartAttemptStartV1
	}
	if timeoutStart != schema.TimeoutStartAttemptStartV1 {
		return false
	}
	start, err := time.Parse(time.RFC3339Nano, a.StartedAt)
	if err != nil {
		return false
	}
	deadline := start.Add(time.Duration(a.TimeoutMs) * time.Millisecond)
	return (s.FirstTS.Equal(deadline) || s.FirstTS.After(deadline)) && s.FirstCode == "ZCL_E_TIMEOUT"
}

func tokenEstimatesForAttempt(attemptDir string, tracePath string, metrics schema.AttemptMetricsV1) *schema.TokenEstimatesV1 {
	if m, ok := loadRunnerTokenEstimates(filepath.Join(attemptDir, "runner.metrics.json")); ok {
		return m
	}
	s, err := scanTraceSummary(tracePath)
	if err != nil || !s.HasEvent {
		return nil
	}
	in := approxTokens(s.InputBytes)
	out := approxTokens(metrics.OutBytesTotal + metrics.ErrBytesTotal)
	total := in + out
	return &schema.TokenEstimatesV1{
		Source:       "trace-heuristic",
		TotalTokens:  i64Ptr(total),
		InputTokens:  i64Ptr(in),
		OutputTokens: i64Ptr(out),
	}
}

func loadRunnerTokenEstimates(path string) (*schema.TokenEstimatesV1, bool) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, false
	}
	var m schema.RunnerMetricsJSONV1
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, false
	}
	if m.TotalTokens == nil && m.InputTokens == nil && m.OutputTokens == nil && m.CachedInputTokens == nil && m.ReasoningOutputTokens == nil {
		return nil, false
	}
	return &schema.TokenEstimatesV1{
		Source:                "runner.metrics",
		TotalTokens:           m.TotalTokens,
		InputTokens:           m.InputTokens,
		OutputTokens:          m.OutputTokens,
		CachedInputTokens:     m.CachedInputTokens,
		ReasoningOutputTokens: m.ReasoningOutputTokens,
	}, true
}

func approxTokens(bytes int64) int64 {
	if bytes <= 0 {
		return 0
	}
	return (bytes + 3) / 4
}

func i64Ptr(v int64) *int64 {
	x := v
	return &x
}

func cloneCountMap(in map[string]int64) map[string]int64 {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]int64, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func cloneNativeResultProvenance(in *schema.NativeResultProvenanceV1) *schema.NativeResultProvenanceV1 {
	if in == nil {
		return nil
	}
	out := *in
	return &out
}

func deriveDecisionTags(fromFeedback []string, okPtr *bool, metrics schema.AttemptMetricsV1, integrity *schema.AttemptIntegrityV1, timedOutBeforeFirst bool) []string {
	out := append([]string(nil), fromFeedback...)
	if okPtr != nil {
		if *okPtr {
			out = append(out, schema.DecisionTagSuccess)
		} else {
			out = append(out, schema.DecisionTagBlocked)
		}
	}
	if timedOutBeforeFirst || metrics.TimeoutsTotal > 0 {
		out = append(out, schema.DecisionTagTimeout)
	}
	if integrity != nil && integrity.FunnelBypassSuspected {
		out = append(out, schema.DecisionTagFunnelBypass)
	}
	if integrity != nil && integrity.PromptContaminated {
		out = append(out, schema.DecisionTagContaminatedPrompt)
	}
	if integrity != nil && !integrity.FeedbackPresent {
		out = append(out, schema.DecisionTagMissingEvidence)
	}
	return schema.NormalizeDecisionTagsV1(out)
}

func WriteAttemptReportAtomic(path string, report schema.AttemptReportJSONV1) error {
	return store.WriteJSONAtomic(path, report)
}

func maxTraceTS(tracePath string) (string, bool) {
	f, err := os.Open(tracePath)
	if err != nil {
		return "", false
	}
	defer func() { _ = f.Close() }()

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	var (
		maxTS  time.Time
		maxStr string
	)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var ev schema.TraceEventV1
		if err := json.Unmarshal(line, &ev); err != nil {
			continue
		}
		ts, err := time.Parse(time.RFC3339Nano, ev.TS)
		if err != nil {
			continue
		}
		if maxTS.IsZero() || ts.After(maxTS) {
			maxTS = ts
			maxStr = ev.TS
		}
	}
	if maxTS.IsZero() {
		return "", false
	}
	return maxStr, true
}

func computeMetricsAndSignals(tracePath string, strict bool) (schema.AttemptMetricsV1, *schema.AttemptSignalsV1, error) {
	f, err := os.Open(tracePath)
	if err != nil {
		if strict && os.IsNotExist(err) {
			return schema.AttemptMetricsV1{}, nil, &CliError{Code: "ZCL_E_MISSING_ARTIFACT", Message: "missing tool.calls.jsonl"}
		}
		if os.IsNotExist(err) {
			// Best-effort: missing trace yields zero metrics.
			return schema.AttemptMetricsV1{}, nil, nil
		}
		return schema.AttemptMetricsV1{}, nil, err
	}
	defer func() { _ = f.Close() }()

	var (
		sc            = bufio.NewScanner(f)
		metrics       schema.AttemptMetricsV1
		minTS         time.Time
		maxTS         time.Time
		durs          []int64
		retryKeyStats = map[string]struct {
			count    int64
			failures int64
		}{}
		lastSig      string
		streak       int64
		maxStreak    int64
		distinctSigs = map[string]bool{}
		cmdNames     = map[string]bool{}
	)
	metrics.FailuresByCode = map[string]int64{}
	metrics.ToolCallsByTool = map[string]int64{}
	metrics.ToolCallsByOp = map[string]int64{}

	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var ev schema.TraceEventV1
		if err := json.Unmarshal(line, &ev); err != nil {
			return schema.AttemptMetricsV1{}, nil, err
		}
		metrics.ToolCallsTotal++
		metrics.ToolCallsByTool[ev.Tool]++
		metrics.ToolCallsByOp[ev.Op]++
		durs = append(durs, ev.Result.DurationMs)

		key := ev.Tool + "\x1f" + ev.Op + "\x1f" + string(ev.Input)
		st := retryKeyStats[key]
		st.count++
		if !ev.Result.OK {
			st.failures++
		}
		retryKeyStats[key] = st

		if !ev.Result.OK {
			metrics.FailuresTotal++
			code := ev.Result.Code
			if strings.TrimSpace(code) == "" {
				if ev.Result.ExitCode != nil && *ev.Result.ExitCode != 0 {
					code = "ZCL_E_TOOL_FAILED"
				} else {
					code = "ZCL_E_TOOL_FAILED"
				}
			}
			metrics.FailuresByCode[code]++
			if code == "ZCL_E_TIMEOUT" {
				metrics.TimeoutsTotal++
			}
		}

		if ev.Integrity != nil && ev.Integrity.Truncated {
			// We only know "truncated happened"; attribute it to both streams for now.
			// Once we emit per-stream truncation, we can split precisely.
			if ev.IO.OutPreview != "" {
				metrics.OutPreviewTruncations++
			}
			if ev.IO.ErrPreview != "" {
				metrics.ErrPreviewTruncations++
			}
		}

		metrics.OutBytesTotal += ev.IO.OutBytes
		metrics.ErrBytesTotal += ev.IO.ErrBytes

		if ts, err := time.Parse(time.RFC3339Nano, ev.TS); err == nil {
			if minTS.IsZero() || ts.Before(minTS) {
				minTS = ts
			}
			if maxTS.IsZero() || ts.After(maxTS) {
				maxTS = ts
			}
		} else if strict {
			return schema.AttemptMetricsV1{}, nil, err
		}

		// Signals: signature-based streak and diversity.
		sig := ev.Tool + "\x1f" + ev.Op + "\x1f" + string(ev.Input)
		distinctSigs[sig] = true
		if sig == lastSig {
			streak++
		} else {
			lastSig = sig
			streak = 1
		}
		if streak > maxStreak {
			maxStreak = streak
		}

		// Best-effort: record argv[0] command names for exec calls (supports both legacy tool=<cmd>
		// and current tool=cli with input.argv).
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
			// Fallback: some older traces use tool=<cmd>.
			cmdNames[ev.Tool] = true
		}
	}
	if err := sc.Err(); err != nil {
		return schema.AttemptMetricsV1{}, nil, err
	}

	if metrics.ToolCallsTotal == 0 {
		if strict {
			return schema.AttemptMetricsV1{}, nil, &CliError{Code: "ZCL_E_MISSING_EVIDENCE", Message: "tool.calls.jsonl is empty"}
		}
		metrics.FailuresByCode = nil
		metrics.ToolCallsByTool = nil
		metrics.ToolCallsByOp = nil
		return metrics, nil, nil
	}

	if !minTS.IsZero() && !maxTS.IsZero() {
		metrics.WallTimeMs = maxTS.Sub(minTS).Milliseconds()
	}
	metrics.DurationMsTotal, metrics.DurationMsMin, metrics.DurationMsMax, metrics.DurationMsAvg, metrics.DurationMsP50, metrics.DurationMsP95 = summarizeDurations(durs)

	var retries int64
	for _, st := range retryKeyStats {
		if st.count > 1 && st.failures > 0 {
			retries += st.count - 1
		}
	}
	metrics.RetriesTotal = retries

	if len(metrics.FailuresByCode) == 0 {
		metrics.FailuresByCode = nil
	}
	if len(metrics.ToolCallsByTool) == 0 {
		metrics.ToolCallsByTool = nil
	}
	if len(metrics.ToolCallsByOp) == 0 {
		metrics.ToolCallsByOp = nil
	}

	// Derive signals (deterministic, evidence-backed).
	var cmdList []string
	for s := range cmdNames {
		cmdList = append(cmdList, s)
	}
	sort.Strings(cmdList)

	failureRateBps := int64(0)
	if metrics.ToolCallsTotal > 0 {
		failureRateBps = (metrics.FailuresTotal * 10000) / metrics.ToolCallsTotal
		if failureRateBps < 0 {
			failureRateBps = 0
		}
		if failureRateBps > 10000 {
			failureRateBps = 10000
		}
	}

	// Conservative stuck heuristic: lots of calls, very low diversity, long repeats.
	noProgress := false
	if metrics.ToolCallsTotal >= 20 && int64(len(distinctSigs)) <= 3 && maxStreak >= 10 {
		noProgress = true
	}

	signals := &schema.AttemptSignalsV1{
		RepeatMaxStreak:           maxStreak,
		DistinctCommandSignatures: int64(len(distinctSigs)),
		FailureRateBps:            failureRateBps,
		NoProgressSuspected:       noProgress,
		CommandNamesSeen:          cmdList,
	}
	return metrics, signals, nil
}

func IsCliError(err error, code string) bool {
	var e *CliError
	if errors.As(err, &e) {
		return e.Code == code
	}
	return false
}

func summarizeDurations(durs []int64) (total, min, max, avg, p50, p95 int64) {
	if len(durs) == 0 {
		return 0, 0, 0, 0, 0, 0
	}
	total = 0
	min, max = durs[0], durs[0]
	for _, d := range durs {
		total += d
		if d < min {
			min = d
		}
		if d > max {
			max = d
		}
	}
	avg = total / int64(len(durs))

	sorted := append([]int64(nil), durs...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	p50 = quantileMillis(sorted, 0.50)
	p95 = quantileMillis(sorted, 0.95)
	return total, min, max, avg, p50, p95
}

func quantileMillis(sorted []int64, q float64) int64 {
	n := len(sorted)
	if n == 0 {
		return 0
	}
	if n == 1 {
		return sorted[0]
	}
	if q < 0 {
		q = 0
	}
	if q > 1 {
		q = 1
	}

	// Linear interpolation between closest ranks.
	pos := q * float64(n-1)
	lo := int(pos)
	hi := lo + 1
	if hi >= n {
		return sorted[n-1]
	}
	frac := pos - float64(lo)
	v := float64(sorted[lo]) + (float64(sorted[hi])-float64(sorted[lo]))*frac
	if v < 0 {
		return 0
	}
	return int64(v + 0.5)
}

func loadSuiteForAttempt(attemptDir string) (suite.SuiteFileV1, bool, error) {
	runDir := filepath.Dir(filepath.Dir(attemptDir))
	if _, err := os.Stat(filepath.Join(runDir, "run.json")); err != nil {
		if os.IsNotExist(err) {
			return suite.SuiteFileV1{}, false, nil
		}
		return suite.SuiteFileV1{}, false, err
	}
	suitePath := filepath.Join(runDir, "suite.json")
	raw, err := os.ReadFile(suitePath)
	if err != nil {
		if os.IsNotExist(err) {
			return suite.SuiteFileV1{}, false, nil
		}
		return suite.SuiteFileV1{}, false, err
	}
	var sf suite.SuiteFileV1
	if err := json.Unmarshal(raw, &sf); err != nil {
		return suite.SuiteFileV1{}, false, &CliError{Code: "ZCL_E_INVALID_JSON", Message: "suite.json is not valid suite json"}
	}
	return sf, true, nil
}
