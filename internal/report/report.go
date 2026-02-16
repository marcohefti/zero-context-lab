package report

import (
	"bufio"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"time"

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

	metrics, err := computeMetrics(tracePath, enforce)
	if err != nil {
		return schema.AttemptReportJSONV1{}, err
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

	integrity := &schema.AttemptIntegrityV1{
		TracePresent:          tracePresent,
		TraceNonEmpty:         traceNonEmpty,
		FeedbackPresent:       feedbackPresent,
		FunnelBypassSuspected: feedbackPresent && !traceNonEmpty,
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

	var expects *schema.ExpectationResultV1
	if sf, ok, err := loadSuiteForAttempt(attemptDir); err != nil {
		if enforce {
			return schema.AttemptReportJSONV1{}, err
		}
	} else if ok && feedbackPresent {
		er := suite.Evaluate(sf, attempt.MissionID, fb)
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
		SchemaVersion:  schema.AttemptReportSchemaV1,
		RunID:          attempt.RunID,
		SuiteID:        attempt.SuiteID,
		MissionID:      attempt.MissionID,
		AttemptID:      attempt.AttemptID,
		ComputedAt:     now.UTC().Format(time.RFC3339Nano),
		StartedAt:      startedAt,
		EndedAt:        endedAt,
		OK:             okPtr,
		Result:         fb.Result,
		ResultJSON:     fb.ResultJSON,
		Classification: fb.Classification,
		Metrics:        metrics,
		Artifacts:      artifacts,
		Integrity:      integrity,
		Expectations:   expects,
	}, nil
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

func computeMetrics(tracePath string, strict bool) (schema.AttemptMetricsV1, error) {
	f, err := os.Open(tracePath)
	if err != nil {
		if strict && os.IsNotExist(err) {
			return schema.AttemptMetricsV1{}, &CliError{Code: "ZCL_E_MISSING_ARTIFACT", Message: "missing tool.calls.jsonl"}
		}
		if os.IsNotExist(err) {
			// Best-effort: missing trace yields zero metrics.
			return schema.AttemptMetricsV1{}, nil
		}
		return schema.AttemptMetricsV1{}, err
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
			return schema.AttemptMetricsV1{}, err
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
			if code == "" {
				code = "UNKNOWN"
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
			return schema.AttemptMetricsV1{}, err
		}
	}
	if err := sc.Err(); err != nil {
		return schema.AttemptMetricsV1{}, err
	}

	if metrics.ToolCallsTotal == 0 {
		if strict {
			return schema.AttemptMetricsV1{}, &CliError{Code: "ZCL_E_MISSING_EVIDENCE", Message: "tool.calls.jsonl is empty"}
		}
		metrics.FailuresByCode = nil
		metrics.ToolCallsByTool = nil
		metrics.ToolCallsByOp = nil
		return metrics, nil
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
	return metrics, nil
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
