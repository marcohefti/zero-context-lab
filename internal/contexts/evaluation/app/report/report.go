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

	"github.com/marcohefti/zero-context-lab/internal/contexts/spec/ports/suite"
	"github.com/marcohefti/zero-context-lab/internal/kernel/blind"
	"github.com/marcohefti/zero-context-lab/internal/kernel/schema"
	"github.com/marcohefti/zero-context-lab/internal/kernel/store"
)

type CliError struct {
	Code    string
	Message string
}

func (e *CliError) Error() string { return e.Message }

func BuildAttemptReport(now time.Time, attemptDir string, strict bool) (schema.AttemptReportJSONV1, error) {
	tracePath := filepath.Join(attemptDir, "tool.calls.jsonl")
	feedbackPath := filepath.Join(attemptDir, "feedback.json")
	attempt, enforce, err := loadAttemptForReport(attemptDir, strict)
	if err != nil {
		return schema.AttemptReportJSONV1{}, err
	}
	fb, okPtr, feedbackPresent, err := loadFeedbackForReport(feedbackPath, enforce)
	if err != nil {
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
	tracePresent, traceNonEmpty, err := tracePresenceAndNonEmpty(tracePath, enforce)
	if err != nil {
		return schema.AttemptReportJSONV1{}, err
	}

	promptContaminationTerms := promptContaminationTerms(attemptDir, attempt.BlindTerms)
	timedOutBeforeFirstToolCall := classifyTimedOutBeforeFirstToolCall(attempt, traceSummary)
	integrity := buildAttemptIntegrity(tracePresent, traceNonEmpty, feedbackPresent, promptContaminationTerms)
	artifacts := discoverAttemptArtifacts(attemptDir)

	startedAt := attempt.StartedAt
	endedAt := resolveAttemptEndedAt(feedbackPresent, fb.CreatedAt, traceNonEmpty, tracePath)
	failureCodeHistogram := cloneCountMap(metrics.FailuresByCode)
	tokenEstimates := tokenEstimatesForAttempt(attemptDir, tracePath, metrics)
	decisionTags := deriveDecisionTags(fb.DecisionTags, okPtr, metrics, integrity, timedOutBeforeFirstToolCall)

	expects, err := buildExpectationsForReport(attemptDir, attempt.MissionID, fb, feedbackPresent, metrics, signals, enforce)
	if err != nil {
		return schema.AttemptReportJSONV1{}, err
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

func loadAttemptForReport(attemptDir string, strict bool) (schema.AttemptJSONV1, bool, error) {
	attemptPath := filepath.Join(attemptDir, "attempt.json")
	attemptBytes, err := os.ReadFile(attemptPath)
	if err != nil {
		if strict && os.IsNotExist(err) {
			return schema.AttemptJSONV1{}, false, &CliError{Code: "ZCL_E_MISSING_ARTIFACT", Message: "missing attempt.json"}
		}
		return schema.AttemptJSONV1{}, false, err
	}
	var attempt schema.AttemptJSONV1
	if err := json.Unmarshal(attemptBytes, &attempt); err != nil {
		return schema.AttemptJSONV1{}, false, err
	}
	return attempt, strict || attempt.Mode == "ci", nil
}

func loadFeedbackForReport(feedbackPath string, enforce bool) (schema.FeedbackJSONV1, *bool, bool, error) {
	var fb schema.FeedbackJSONV1
	fbBytes, err := os.ReadFile(feedbackPath)
	if err == nil {
		if err := json.Unmarshal(fbBytes, &fb); err != nil {
			return schema.FeedbackJSONV1{}, nil, false, err
		}
		ok := fb.OK
		return fb, &ok, true, nil
	}
	if enforce && os.IsNotExist(err) {
		return schema.FeedbackJSONV1{}, nil, false, &CliError{Code: "ZCL_E_MISSING_ARTIFACT", Message: "missing feedback.json"}
	}
	if err != nil && !os.IsNotExist(err) {
		return schema.FeedbackJSONV1{}, nil, false, err
	}
	return schema.FeedbackJSONV1{}, nil, false, nil
}

func tracePresenceAndNonEmpty(tracePath string, enforce bool) (bool, bool, error) {
	if _, err := os.Stat(tracePath); err != nil {
		return false, false, nil
	}
	nonEmpty, err := store.JSONLHasNonEmptyLine(tracePath)
	if err != nil {
		if enforce {
			return true, false, err
		}
		return true, false, nil
	}
	return true, nonEmpty, nil
}

func buildAttemptIntegrity(tracePresent, traceNonEmpty, feedbackPresent bool, terms []string) *schema.AttemptIntegrityV1 {
	return &schema.AttemptIntegrityV1{
		TracePresent:             tracePresent,
		TraceNonEmpty:            traceNonEmpty,
		FeedbackPresent:          feedbackPresent,
		FunnelBypassSuspected:    feedbackPresent && !traceNonEmpty,
		PromptContaminated:       len(terms) > 0,
		PromptContaminationTerms: terms,
	}
}

func discoverAttemptArtifacts(attemptDir string) schema.AttemptArtifactsV1 {
	artifacts := schema.AttemptArtifactsV1{
		AttemptJSON:  "attempt.json",
		TraceJSONL:   "tool.calls.jsonl",
		FeedbackJSON: "feedback.json",
	}
	setArtifactIfPresent(filepath.Join(attemptDir, "notes.jsonl"), &artifacts.NotesJSONL, "notes.jsonl")
	setArtifactIfPresent(filepath.Join(attemptDir, "prompt.txt"), &artifacts.PromptTXT, "prompt.txt")
	setArtifactIfPresent(filepath.Join(attemptDir, schema.AttemptEnvShFileNameV1), &artifacts.AttemptEnvSH, schema.AttemptEnvShFileNameV1)
	setArtifactIfPresent(filepath.Join(attemptDir, schema.AttemptRuntimeEnvFileNameV1), &artifacts.AttemptRuntimeEnvJSON, schema.AttemptRuntimeEnvFileNameV1)
	setArtifactIfPresent(filepath.Join(attemptDir, "runner.command.txt"), &artifacts.RunnerCommandTXT, "runner.command.txt")
	setArtifactIfPresent(filepath.Join(attemptDir, "runner.stdout.log"), &artifacts.RunnerStdoutLOG, "runner.stdout.log")
	setArtifactIfPresent(filepath.Join(attemptDir, "runner.stderr.log"), &artifacts.RunnerStderrLOG, "runner.stderr.log")
	return artifacts
}

func setArtifactIfPresent(path string, out *string, name string) {
	if _, err := os.Stat(path); err == nil {
		*out = name
	}
}

func resolveAttemptEndedAt(feedbackPresent bool, feedbackCreatedAt string, traceNonEmpty bool, tracePath string) string {
	if feedbackPresent && feedbackCreatedAt != "" {
		return feedbackCreatedAt
	}
	if !traceNonEmpty {
		return ""
	}
	if ts, ok := maxTraceTS(tracePath); ok {
		return ts
	}
	return ""
}

func buildExpectationsForReport(attemptDir, missionID string, fb schema.FeedbackJSONV1, feedbackPresent bool, metrics schema.AttemptMetricsV1, signals *schema.AttemptSignalsV1, enforce bool) (*schema.ExpectationResultV1, error) {
	sf, ok, err := loadSuiteForAttempt(attemptDir)
	if err != nil {
		if enforce {
			return nil, err
		}
		return nil, nil
	}
	if !ok || !feedbackPresent {
		return nil, nil
	}
	tf := buildSuiteTraceFacts(metrics, signals)
	er := suite.Evaluate(sf, missionID, fb, &tf)
	expects := &schema.ExpectationResultV1{
		Evaluated: er.Evaluated,
		OK:        er.OK,
	}
	if len(er.Failures) == 0 {
		return expects, nil
	}
	expects.Failures = make([]schema.ExpectationFailureV1, 0, len(er.Failures))
	for _, f := range er.Failures {
		expects.Failures = append(expects.Failures, schema.ExpectationFailureV1{Code: f.Code, Message: f.Message})
	}
	return expects, nil
}

func buildSuiteTraceFacts(metrics schema.AttemptMetricsV1, signals *schema.AttemptSignalsV1) suite.TraceFacts {
	opNames := make([]string, 0, len(metrics.ToolCallsByOp))
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
	return tf
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
	f, missing, err := openTraceForMetrics(tracePath, strict)
	if err != nil {
		return schema.AttemptMetricsV1{}, nil, err
	}
	if missing {
		return schema.AttemptMetricsV1{}, nil, nil
	}
	defer func() { _ = f.Close() }()

	acc := newTraceMetricsAccumulator()
	if err := scanTraceMetrics(f, strict, acc); err != nil {
		return schema.AttemptMetricsV1{}, nil, err
	}
	if acc.metrics.ToolCallsTotal == 0 {
		return emptyMetricsResult(strict)
	}
	acc.finalizeMetrics()
	signals := acc.buildSignals()
	return acc.metrics, signals, nil
}

func openTraceForMetrics(tracePath string, strict bool) (*os.File, bool, error) {
	f, err := os.Open(tracePath)
	if err == nil {
		return f, false, nil
	}
	if strict && os.IsNotExist(err) {
		return nil, false, &CliError{Code: "ZCL_E_MISSING_ARTIFACT", Message: "missing tool.calls.jsonl"}
	}
	if os.IsNotExist(err) {
		// Best-effort: missing trace yields zero metrics.
		return nil, true, nil
	}
	return nil, false, err
}

func emptyMetricsResult(strict bool) (schema.AttemptMetricsV1, *schema.AttemptSignalsV1, error) {
	if strict {
		return schema.AttemptMetricsV1{}, nil, &CliError{Code: "ZCL_E_MISSING_EVIDENCE", Message: "tool.calls.jsonl is empty"}
	}
	return schema.AttemptMetricsV1{}, nil, nil
}

type retryMetric struct {
	count    int64
	failures int64
}

type traceMetricsAccumulator struct {
	metrics schema.AttemptMetricsV1

	minTS time.Time
	maxTS time.Time
	durs  []int64

	retryStats map[string]retryMetric

	lastSig   string
	streak    int64
	maxStreak int64

	distinctSigs map[string]bool
	cmdNames     map[string]bool
}

func newTraceMetricsAccumulator() *traceMetricsAccumulator {
	return &traceMetricsAccumulator{
		metrics: schema.AttemptMetricsV1{
			FailuresByCode:  map[string]int64{},
			ToolCallsByTool: map[string]int64{},
			ToolCallsByOp:   map[string]int64{},
		},
		retryStats:   map[string]retryMetric{},
		distinctSigs: map[string]bool{},
		cmdNames:     map[string]bool{},
	}
}

func scanTraceMetrics(f *os.File, strict bool, acc *traceMetricsAccumulator) error {
	sc := bufio.NewScanner(f)
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

func (a *traceMetricsAccumulator) observeEvent(ev schema.TraceEventV1, strict bool) error {
	a.observeCounts(ev)
	a.observeFailures(ev)
	a.observeTruncation(ev)
	a.observeIO(ev)
	if err := a.observeTimestamp(ev, strict); err != nil {
		return err
	}
	a.observeSignals(ev)
	a.observeCommandNames(ev)
	return nil
}

func (a *traceMetricsAccumulator) observeCounts(ev schema.TraceEventV1) {
	a.metrics.ToolCallsTotal++
	a.metrics.ToolCallsByTool[ev.Tool]++
	a.metrics.ToolCallsByOp[ev.Op]++
	a.durs = append(a.durs, ev.Result.DurationMs)

	key := eventSignature(ev)
	st := a.retryStats[key]
	st.count++
	if !ev.Result.OK {
		st.failures++
	}
	a.retryStats[key] = st
}

func (a *traceMetricsAccumulator) observeFailures(ev schema.TraceEventV1) {
	if ev.Result.OK {
		return
	}
	a.metrics.FailuresTotal++
	code := strings.TrimSpace(ev.Result.Code)
	if code == "" {
		code = "ZCL_E_TOOL_FAILED"
	}
	a.metrics.FailuresByCode[code]++
	if code == "ZCL_E_TIMEOUT" {
		a.metrics.TimeoutsTotal++
	}
}

func (a *traceMetricsAccumulator) observeTruncation(ev schema.TraceEventV1) {
	if ev.Integrity == nil || !ev.Integrity.Truncated {
		return
	}
	// We only know "truncated happened"; attribute it to both streams for now.
	// Once we emit per-stream truncation, we can split precisely.
	if ev.IO.OutPreview != "" {
		a.metrics.OutPreviewTruncations++
	}
	if ev.IO.ErrPreview != "" {
		a.metrics.ErrPreviewTruncations++
	}
}

func (a *traceMetricsAccumulator) observeIO(ev schema.TraceEventV1) {
	a.metrics.OutBytesTotal += ev.IO.OutBytes
	a.metrics.ErrBytesTotal += ev.IO.ErrBytes
}

func (a *traceMetricsAccumulator) observeTimestamp(ev schema.TraceEventV1, strict bool) error {
	ts, err := time.Parse(time.RFC3339Nano, ev.TS)
	if err != nil {
		if strict {
			return err
		}
		return nil
	}
	if a.minTS.IsZero() || ts.Before(a.minTS) {
		a.minTS = ts
	}
	if a.maxTS.IsZero() || ts.After(a.maxTS) {
		a.maxTS = ts
	}
	return nil
}

func (a *traceMetricsAccumulator) observeSignals(ev schema.TraceEventV1) {
	sig := eventSignature(ev)
	a.distinctSigs[sig] = true
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

func (a *traceMetricsAccumulator) observeCommandNames(ev schema.TraceEventV1) {
	if ev.Op != "exec" {
		return
	}
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
		// Fallback: some older traces use tool=<cmd>.
		a.cmdNames[ev.Tool] = true
	}
}

func (a *traceMetricsAccumulator) finalizeMetrics() {
	if !a.minTS.IsZero() && !a.maxTS.IsZero() {
		a.metrics.WallTimeMs = a.maxTS.Sub(a.minTS).Milliseconds()
	}
	a.metrics.DurationMsTotal, a.metrics.DurationMsMin, a.metrics.DurationMsMax, a.metrics.DurationMsAvg, a.metrics.DurationMsP50, a.metrics.DurationMsP95 = summarizeDurations(a.durs)
	a.metrics.RetriesTotal = retriesTotal(a.retryStats)
	normalizeMetricMaps(&a.metrics)
}

func retriesTotal(stats map[string]retryMetric) int64 {
	var retries int64
	for _, st := range stats {
		if st.count > 1 && st.failures > 0 {
			retries += st.count - 1
		}
	}
	return retries
}

func normalizeMetricMaps(metrics *schema.AttemptMetricsV1) {
	if len(metrics.FailuresByCode) == 0 {
		metrics.FailuresByCode = nil
	}
	if len(metrics.ToolCallsByTool) == 0 {
		metrics.ToolCallsByTool = nil
	}
	if len(metrics.ToolCallsByOp) == 0 {
		metrics.ToolCallsByOp = nil
	}
}

func (a *traceMetricsAccumulator) buildSignals() *schema.AttemptSignalsV1 {
	return &schema.AttemptSignalsV1{
		RepeatMaxStreak:           a.maxStreak,
		DistinctCommandSignatures: int64(len(a.distinctSigs)),
		FailureRateBps:            failureRateBps(a.metrics),
		NoProgressSuspected:       noProgressSuspected(a.metrics.ToolCallsTotal, int64(len(a.distinctSigs)), a.maxStreak),
		CommandNamesSeen:          sortedKeys(a.cmdNames),
	}
}

func eventSignature(ev schema.TraceEventV1) string {
	return ev.Tool + "\x1f" + ev.Op + "\x1f" + string(ev.Input)
}

func sortedKeys(in map[string]bool) []string {
	if len(in) == 0 {
		return nil
	}
	out := make([]string, 0, len(in))
	for key := range in {
		out = append(out, key)
	}
	sort.Strings(out)
	return out
}

func failureRateBps(metrics schema.AttemptMetricsV1) int64 {
	if metrics.ToolCallsTotal <= 0 {
		return 0
	}
	rate := (metrics.FailuresTotal * 10000) / metrics.ToolCallsTotal
	if rate < 0 {
		return 0
	}
	if rate > 10000 {
		return 10000
	}
	return rate
}

func noProgressSuspected(totalCalls, distinctCount, maxStreak int64) bool {
	// Conservative stuck heuristic: lots of calls, very low diversity, long repeats.
	return totalCalls >= 20 && distinctCount <= 3 && maxStreak >= 10
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
